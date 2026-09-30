package parser

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

const (
	WhitespaceChars = " \f\n\r\t\v\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff"
	Re2Dot          = "[^\r\n\u2028\u2029]"
	regexp2Dot      = `[^\n\r\u2028\u2029]`
	// regexp2NoLandmarks starts a pattern for regexp2 that may start with an
	// unbounded loop of a set (see leadingUnboundedLoop): an empty lookahead
	// that always succeeds, which regexp2 does not drop.
	regexp2NoLandmarks = `(?!(?!))`
)

type regexpParseError struct {
	offset int
	err    string
}

type RegexpErrorIncompatible struct {
	regexpParseError
}
type RegexpSyntaxError struct {
	regexpParseError
}

func (s regexpParseError) Error() string {
	return s.err
}

// termKind is the kind of the last term scanned, which decides whether a
// quantifier may follow it (checked with the u flag only).
type termKind int

const (
	termNone       termKind = iota // start of an alternative
	termAtom                       // quantifiable
	termAssertion                  // ^ $ \b \B
	termLookahead                  // (?= ) (?! )
	termLookbehind                 // (?<= ) (?<! )
	termQuantified                 // an atom and its quantifier
)

type _RegExp_parser struct {
	str    string
	length int

	chr       rune // The current character
	chrOffset int  // The offset of current character
	offset    int  // The offset after current character (may be greater than 1)

	err error

	goRegexp   bytes.Buffer
	passOffset int

	dotAll     bool // Enable dotAll mode
	unicode    bool
	ignoreCase bool

	// Scanning goes on after a construct Go's regexp does not support, to
	// validate the whole pattern and write it for regexp2; incompat keeps the
	// first one.
	incompat error
	prev     termKind

	// The pattern for regexp2, which differs from the input when it has
	// property escapes: regexp2 gets them as explicit character sets.
	regexp2       bytes.Buffer
	regexp2Offset int // the input before it has been copied to regexp2
	regexp2Used   bool

	// The class being scanned: the set of its atoms (with the u flag) and its
	// property escapes, which regexp2 gets when the class ends; and where it
	// starts in the output for Go's regexp, which gets it as a set when
	// ignoring case (-1 if it does not).
	classStart    int
	classSet      runeSet
	classUnsorted bool // classSet has ranges out of order (see addClassSet)
	// The class has \D, \W or \S, and is written for regexp2 as its set.
	classNegEscape bool
	classProps     []classProperty
	classGoStart   int

	capturesScanned bool
	captureCount    int
	captureNames    []string
	namesSeen       map[string]bool
	// The capturing groups being scanned, and how many have opened.
	openCaptures   []openCapture
	capturesOpened int

	// What the pattern costs each engine (see maxRegexpExpansion and
	// termCost): for Go's regexp, besides the instructions of its program
	// (goInsts), with those of the last atom starting at atomInsts.
	goExpansion, regexp2Expansion int
	goInsts, atomInsts            int64

	// V8 reports these errors only once the whole pattern has parsed: a
	// named reference to no group (deferred), and a pattern too large to
	// compile (refused, when nothing more is written for either engine).
	deferred string
	refused  bool

	depth int // of the group being scanned

	// The loops told by a group, and the hidden groups that are some of
	// their bodies (see planIterations), nil if there are none.
	iter *iterPlan

	// Without the u flag, ignoring case (see canonFold): where the Go text
	// of the atom being scanned starts, and where the escape being scanned
	// starts in the input, and whether it is a backreference.
	canonGoStart              int
	canonR2Len, canonR2Offset int
	classR2Len, classR2Offset int // the regexp2 text before the class
	escStart                  int
	canonChars                map[rune]canonChar
	backref                   bool

	// The items of the class being scanned, the classes seen, and those
	// rewritten as their sets closed under case folding, by their text.
	classItems    int
	classTexts    map[string]bool
	foldedClasses map[string]foldedClass
	wholeClasses  map[string]*wholeClass
}

// maxRegexpExpansion bounds what a pattern costs an engine to compile, in
// units of about 100 ns of compile time, measured term by term (see
// termCost) and property escape by escape: a pattern of thousands of
// escapes, or of a million other terms, would take seconds to compile,
// which cannot be interrupted, and the bound keeps an engine's compile to
// about 100 ms (patterns at the bound compile in 60 to 150 ms: many terms
// compile faster together than one by one, and the model errs high). Every
// property escape costs expansionEscape. Go's regexp copies a named table into
// its program, a range in about 40 ns (\p{L} is about 680 ranges, 25 µs);
// expansionClassTable units a range in a class, whose ranges it sorts
// together (see classUnits), or when ignoring case, when it adds the case
// variants of the table's ranges. A range written out (see writeClass)
// costs Go's regexp expansionTextRange units to parse, and regexp2 more,
// and more per range the more a class has (see regexp2TextUnits); a name
// costs regexp2 expansionName. A class rewritten as the set it stands for
// costs expansionClass units to compute, for both engines.
//
// The cost of the engines that compile the pattern is bounded, each on its
// own: regexp2's for every pattern, since it compiles one Go's regexp runs
// when a match needs it (a split of a large input with \b, or of a string
// with a lone surrogate), and Go's unless the pattern has a construct Go's
// regexp lacks, when nothing is written or counted for Go's regexp. A
// pattern is refused at the term that takes regexp2 past the bound, and
// Go's regexp is written for no more once it is past it, so the text
// written for each stays small; one whose program, with its repeats
// expanded, would take Go's regexp past it is left to regexp2 (see
// countQuantifier). V8 refuses patterns of about 12,500 escapes, or of
// over 32,767 terms in a row, with this message.
const (
	maxRegexpExpansion  = 3 << 19
	expansionEscape     = 40
	expansionTextRange  = 3
	expansionClassTable = 4
	expansionName       = 10
	expansionClass      = 1500
)

// maxGoRepeat is the largest count Go's regexp takes in a repeat.
const maxGoRepeat = 1000

// maxGroupNesting is how deeply groups may nest: Go's regexp takes no more,
// and regexp2 is refused more (see goja's maxRegexpNesting).
const maxGroupNesting = 1000

// TransformRegExp transforms a JavaScript pattern into  a Go "regexp" pattern.
//
// re2 (Go) cannot do backtracking, so the presence of a lookahead (?=) (?!) or
// backreference (\1, \2, ...) will cause an error.
//
// re2 (Go) has a different definition for \s: [\t\n\f\r ].
// The JavaScript definition, on the other hand, also includes \v, Unicode "Separator, Space", etc.
//
// If the pattern is valid, but incompatible (contains a lookahead or backreference),
// then this function returns an empty string an error of type RegexpErrorIncompatible.
//
// If the pattern is invalid (not valid even in JavaScript), then this function
// returns an empty string and a generic error.
func TransformRegExp(pattern string, dotAll, unicode bool) (transformed string, err error) {
	transformed, _, err = TransformRegExp2(pattern, dotAll, unicode, false)
	return
}

// TransformRegExp2 is TransformRegExp that also returns the pattern to give
// regexp2 (github.com/dlclark/regexp2) in ECMAScript mode, for when the
// result is an error of type RegexpErrorIncompatible or the caller needs
// regexp2 for other reasons. It is the input pattern, except that Unicode
// property escapes (\p{...} and \P{...} with the u flag), which regexp2
// does not implement as ECMAScript specifies, are replaced by character
// sets; ignoreCase is the i flag, which those sets depend on. A pattern
// either engine would take too long to compile is refused (see
// maxRegexpExpansion).
func TransformRegExp2(pattern string, dotAll, unicode, ignoreCase bool) (transformed, regexp2Pattern string, err error) {
	transformed, regexp2Pattern, _, err = TransformRegExpIter(pattern, dotAll, unicode, ignoreCase)
	return
}

// TransformRegExpIter is TransformRegExp2 that also returns the loops of
// the pattern whose iterations a group tells, and the hidden groups both
// patterns have for them (see Iterations), nil if there are none: the
// caller must drop the captures ECMAScript does not keep, and the hidden
// groups, from a match.
func TransformRegExpIter(pattern string, dotAll, unicode, ignoreCase bool) (transformed, regexp2Pattern string, iter *Iterations, err error) {
	if pattern == "" {
		return "", "", nil, nil
	}

	parser := _RegExp_parser{
		str:        pattern,
		length:     len(pattern),
		dotAll:     dotAll,
		unicode:    unicode,
		ignoreCase: ignoreCase,
	}
	if strings.IndexByte(pattern, '(') >= 0 {
		parser.iter = parser.planIterations()
	}
	if parser.iter != nil {
		iter = &parser.iter.Iterations
	}
	err = parser.parse()
	if err != nil {
		if _, incompat := err.(RegexpErrorIncompatible); incompat {
			return "", parser.regexp2Pattern(), iter, err
		}
		return "", "", nil, err
	}

	return parser.ResultString(), parser.regexp2Pattern(), iter, nil
}

func (self *_RegExp_parser) ResultString() string {
	if self.passOffset != -1 {
		return self.str[:self.passOffset]
	}
	return self.goRegexp.String()
}

func (self *_RegExp_parser) regexp2Pattern() string {
	if !self.regexp2Used {
		return self.str
	}
	self.regexp2.WriteString(self.str[self.regexp2Offset:])
	return self.regexp2.String()
}

func (self *_RegExp_parser) parse() (err error) {
	self.read() // Pull in the first character
	if self.leadingUnboundedLoop() {
		self.count(costAround)
		self.writeRegexp2(0, 0, regexp2NoLandmarks)
	}
	self.scan()
	if self.err == nil && self.deferred != "" {
		self.syntaxError(self.deferred)
	}
	if self.err == nil && (self.refused || self.incompat == nil && self.goCost() > maxRegexpExpansion) {
		self.syntaxError("Regular expression too large")
	}
	if self.err == nil {
		return self.incompat
	}
	return self.err
}

func (self *_RegExp_parser) read() {
	if self.offset < self.length {
		self.chrOffset = self.offset
		chr, width := rune(self.str[self.offset]), 1
		if chr >= utf8.RuneSelf { // !ASCII
			chr, width = utf8.DecodeRuneInString(self.str[self.offset:])
			if chr == utf8.RuneError && width == 1 {
				self.error(true, "Invalid UTF-8 character")
				// The scan ends here even if an error was reported before
				// (and the scan went back, as for \c).
				self.offset = self.length
				self.chr = -1
				return
			}
		}
		self.offset += width
		self.chr = chr
	} else {
		self.chrOffset = self.length
		self.chr = -1 // EOF
	}
}

// seek makes the character at offset the current one.
func (self *_RegExp_parser) seek(offset int) {
	self.offset = offset
	self.read()
}

func (self *_RegExp_parser) stopPassing() {
	self.goRegexp.Grow(3 * len(self.str) / 2)
	self.goRegexp.WriteString(self.str[:self.passOffset])
	self.passOffset = -1
}

func (self *_RegExp_parser) write(p []byte) {
	if self.passOffset != -1 {
		self.stopPassing()
	}
	self.goRegexp.Write(p)
}

func (self *_RegExp_parser) writeByte(b byte) {
	if self.passOffset != -1 {
		self.stopPassing()
	}
	self.goRegexp.WriteByte(b)
}

func (self *_RegExp_parser) writeString(s string) {
	if self.passOffset != -1 {
		self.stopPassing()
	}
	self.goRegexp.WriteString(s)
}

func (self *_RegExp_parser) scan() {
	for self.chr != -1 {
		if self.chr == ')' {
			self.fatal("Unmatched ')'")
			return
		}
		self.scanTerm()
	}
}

// scanTerm scans an atom, an assertion, a quantifier or a '|'.
func (self *_RegExp_parser) scanTerm() {
	switch self.chr {
	case '\\':
		start := self.chrOffset
		self.read()
		kind := termAtom
		if self.chr == 'b' || self.chr == 'B' {
			kind = termAssertion
		}
		self.startAtom()
		esc := self.chr
		self.countEscape()
		if self.canonFold() {
			self.markCanon()
		}
		self.escStart, self.backref = start, false
		self.scanEscape(false)
		if (esc == 'b' || esc == 'B') && self.err == nil {
			self.writeRegexp2(start, self.chrOffset, self.regexp2Boundary(esc == 'B'))
		}
		if self.canonFold() && self.err == nil && !self.backref && !isCharacterClassEscape(esc) && esc != 'b' && esc != 'B' {
			if self.unicode {
				if esc != 'p' && esc != 'P' {
					self.writeCanonChar(start, self.chrOffset, classAtomValue(self.str[start:self.chrOffset]))
				}
			} else if set, isClass := nonUnicodeClassAtom(self.str[start:self.chrOffset], false); !isClass {
				self.writeCanonChar(start, self.chrOffset, set[0])
			}
		}
		if self.ignoreCase && self.unicode && self.err == nil && (esc == 'w' || esc == 'W') {
			// With the u and i flags, \w stands for U+017F and U+212A too,
			// whose case variants are word characters (see regexp2FoldWord).
			if esc == 'w' {
				self.rewriteAtom(start, self.chrOffset, goFoldWord, regexp2FoldWord)
			} else {
				self.rewriteAtom(start, self.chrOffset, goFoldNonWord, regexp2FoldNonWord)
			}
		}
		self.prev = kind
	case '(':
		self.pass()
		self.scanGroup()
	case '[':
		self.startAtom()
		self.scanBracket()
		self.prev = termAtom
	case '.':
		self.startAtom()
		self.count(costDot)
		if self.dotAll {
			self.pass()
		} else {
			// regexp2's . takes U+2028 and U+2029, ECMAScript's does not.
			self.writeRegexp2(self.chrOffset, self.offset, regexp2Dot)
			self.writeString(Re2Dot)
			self.read()
		}
		self.prev = termAtom
	default:
		if self.unicode {
			self.scanUnicodeTermChar()
		} else {
			self.scanNonUnicodeTermChar()
		}
	}
}

// countEscape counts the escape whose letter is the current character.
func (self *_RegExp_parser) countEscape() {
	switch self.chr {
	case 'b', 'B':
		self.count(costBoundary)
	case 's', 'S':
		self.count(costSpaceEsc)
		if self.ignoreCase && self.goUsed() {
			self.goExpansion += goFoldSpaceUnits
		}
	case 'd', 'D', 'w', 'W':
		self.count(costClassEsc)
		if self.ignoreCase && (self.chr == 'w' || self.chr == 'W') && self.goUsed() {
			self.goExpansion += goFoldWordUnits
		}
	case 'k':
		self.count(costBackref)
	case 'p', 'P':
		if self.unicode {
			// The property escape counts itself (see expandGo).
			if self.goUsed() {
				self.goInsts++
			}
			return
		}
		self.count(costLiteral)
	case '1', '2', '3', '4', '5', '6', '7', '8', '9':
		self.count(costBackref)
	default:
		self.count(costEscape)
	}
}

// scanNonUnicodeTermChar scans a character outside atoms without the u flag.
func (self *_RegExp_parser) scanNonUnicodeTermChar() {
	switch self.chr {
	case '^', '$':
		self.count(costAssertion)
		self.pass()
		self.prev = termAssertion
	case '|':
		self.count(costAlternate)
		self.pass()
		self.prev = termNone
	case '*', '+', '?':
		if !self.checkQuantifiable(false) {
			return
		}
		switch self.chr {
		case '*':
			self.countQuantifier(0, -1)
		case '+':
			self.countQuantifier(1, -1)
		default:
			self.countQuantifier(0, 1)
		}
		self.passQuantifier(self.offset)
	case '{':
		// Annex B: a { that does not start a quantifier is itself.
		if min, max, end, ok := self.intervalQuantifier(); ok {
			if !self.checkQuantifiable(max >= 0 && max < min) {
				return
			}
			self.countQuantifier(min, max)
			self.passQuantifier(end)
			return
		}
		fallthrough
	default:
		self.startAtom()
		self.count(costLiteral)
		if self.canonFold() {
			chr, start := self.chr, self.chrOffset
			self.markCanon()
			self.pass()
			self.writeCanonChar(start, self.chrOffset, chr)
		} else {
			self.pass()
		}
		self.prev = termAtom
	}
}

// scanUnicodeTermChar checks what the u flag forbids outside atoms: lone
// quantifier brackets, incomplete quantifiers and quantifiers with nothing,
// or an assertion, to repeat.
func (self *_RegExp_parser) scanUnicodeTermChar() {
	switch self.chr {
	case '^', '$':
		self.count(costAssertion)
		self.pass()
		self.prev = termAssertion
	case '|':
		self.count(costAlternate)
		self.pass()
		self.prev = termNone
	case '*', '+', '?':
		if !self.checkQuantifiable(false) {
			return
		}
		switch self.chr {
		case '*':
			self.countQuantifier(0, -1)
		case '+':
			self.countQuantifier(1, -1)
		default:
			self.countQuantifier(0, 1)
		}
		self.passQuantifier(self.offset)
	case '{':
		min, max, end, ok := self.intervalQuantifier()
		if !ok {
			if self.prev == termAtom || self.prev == termLookahead || self.prev == termLookbehind {
				self.syntaxError("Incomplete quantifier")
			} else {
				self.syntaxError("Lone quantifier brackets")
			}
			return
		}
		if !self.checkQuantifiable(max >= 0 && max < min) {
			return
		}
		self.countQuantifier(min, max)
		self.passQuantifier(end)
	case '}', ']':
		self.syntaxError("Lone quantifier brackets")
	default:
		self.startAtom()
		self.count(costLiteral)
		if self.canonFold() {
			chr, start := self.chr, self.chrOffset
			self.markCanon()
			self.pass()
			self.writeCanonChar(start, self.chrOffset, chr)
		} else {
			self.pass()
		}
		self.prev = termAtom
	}
}

// checkQuantifiable reports whether a quantifier may follow the last term,
// and reports V8's error if not.
func (self *_RegExp_parser) checkQuantifiable(outOfOrder bool) bool {
	switch self.prev {
	case termNone, termAssertion, termQuantified:
		self.syntaxError("Nothing to repeat")
		return false
	}
	if outOfOrder {
		self.syntaxError("numbers out of order in {} quantifier")
		return false
	}
	if self.prev != termAtom && !(self.prev == termLookahead && !self.unicode) {
		// Annex B: without the u flag a lookahead may be repeated.
		self.syntaxError("Invalid quantifier")
		return false
	}
	return true
}

// passQuantifier passes the quantifier that ends at end and a following '?'.
func (self *_RegExp_parser) passQuantifier(end int) {
	for self.chr != -1 && self.chrOffset < end {
		self.pass()
	}
	if self.chr == '?' {
		self.pass()
	}
	self.prev = termQuantified
}

// intervalQuantifier parses {n}, {n,} or {n,m} at the current character.
// max is -1 when there is no upper bound.
func (self *_RegExp_parser) intervalQuantifier() (min, max, end int, ok bool) {
	i := self.chrOffset + 1
	number := func() (int, bool) {
		start := i
		n := 0
		for i < self.length && self.str[i] >= '0' && self.str[i] <= '9' {
			if n < 1<<30 {
				n = n*10 + int(self.str[i]-'0')
			}
			i++
		}
		return n, i > start
	}
	if min, ok = number(); !ok {
		return
	}
	max = min
	if i < self.length && self.str[i] == ',' {
		i++
		var bounded bool
		if max, bounded = number(); !bounded {
			max = -1
		}
	}
	if i >= self.length || self.str[i] != '}' {
		return 0, 0, 0, false
	}
	return min, max, i + 1, true
}

// (...)
func (self *_RegExp_parser) scanGroup() {
	// The scan recurses per group: a pattern of a million nested groups
	// would overflow the stack, which cannot be recovered from. Neither
	// engine compiles more than maxGroupNesting.
	if self.depth++; self.depth > maxGroupNesting {
		self.syntaxError("Regular expression too large")
		return
	}
	defer func() { self.depth-- }()
	open := self.chrOffset - 1 // the '('
	kind := termAtom
	start := self.goInsts
	name := ""
	capturing := false
	str := self.str[self.chrOffset:]
	if len(str) > 0 { // A possibility of (?= or (?!
		if str[0] == '?' && len(str) > 1 {
			ch := str[1]
			switch {
			case ch == '=' || ch == '!':
				self.error(false, "re2: Invalid (%s) <lookahead>", self.str[self.chrOffset:self.chrOffset+2])
				self.count(costAround)
				kind = termLookahead
				self.pass()
				self.pass()
			case ch == '<':
				if len(str) > 2 && (str[2] == '=' || str[2] == '!') {
					self.error(false, "re2: Invalid (%s) <lookbehind>", self.str[self.chrOffset:self.chrOffset+2])
					self.count(costAround)
					kind = termLookbehind
					self.pass()
					self.pass()
					self.pass()
					break
				}
				self.count(costNamed)
				capturing = true
				self.pass()         // ?
				self.writeByte('P') // older Go versions compatibility
				self.pass()         // <
				name = self.scanGroupName()
			case ch == ':':
				self.count(costGroup)
				self.pass()
				self.pass()
			default:
				self.fatal("Invalid group")
				return
			}
		} else if str[0] == '?' {
			self.fatal("Invalid group")
			return
		} else {
			self.count(costCapture)
			capturing = true
		}
	} else {
		self.count(costCapture)
		capturing = true
	}
	if capturing {
		// A backreference in the group is to it (see writeBackref).
		self.capturesOpened++
		self.openCaptures = append(self.openCaptures, openCapture{self.capturesOpened, name})
		defer func() { self.openCaptures = self.openCaptures[:len(self.openCaptures)-1] }()
	}
	hidden := self.iter != nil && self.err == nil && self.iter.hidden[open]
	if hidden {
		// The body is a hidden group (see planIterations).
		self.count(costCapture)
		self.writeByte('(')
		self.writeRegexp2(self.chrOffset, self.chrOffset, "(")
	}
	self.prev = termNone
	for self.chr != -1 && self.chr != ')' {
		self.scanTerm()
	}
	if self.chr != ')' {
		self.fatal("Unterminated group")
		return
	}
	if hidden {
		self.writeByte(')')
		self.writeRegexp2(self.chrOffset, self.chrOffset, ")")
	}
	if name != "" {
		// V8 finds a name taken when the group ends.
		if self.namesSeen[name] {
			self.syntaxError("Duplicate capture group name")
			return
		}
		if self.namesSeen == nil {
			self.namesSeen = make(map[string]bool)
		}
		self.namesSeen[name] = true
	}
	self.pass()
	self.prev = kind
	self.atomInsts = start
}

// scanGroupName scans a group's name, and returns it.
func (self *_RegExp_parser) scanGroupName() string {
	supported := true
	if !(self.chr >= 'a' && self.chr <= 'z' || self.chr >= 'A' && self.chr <= 'Z' && self.chr == '_') {
		supported = false
	}
	start := self.chrOffset
	for self.chr != -1 && self.chr != '>' {
		if !(self.chr >= 'a' && self.chr <= 'z' || self.chr >= 'A' && self.chr <= 'Z' && self.chr == '_' || self.chr >= '0' && self.chr <= '9') {
			supported = false
		}
		self.pass()
	}
	name, valid := decodeGroupName(self.str[start:self.chrOffset])
	if self.chr != '>' || !valid {
		self.syntaxError("Invalid capture group name")
		return ""
	}
	if strings.IndexByte(self.str[start:self.chrOffset], '\\') >= 0 {
		// regexp2 takes no escapes in a name.
		self.writeRegexp2(start, self.chrOffset, name)
	}
	if !supported {
		self.error(false, "Unsupported group name")
	}
	self.pass()
	return name
}

// decodeGroupName decodes the \u escapes in a group name and reports whether
// the name is an identifier.
func decodeGroupName(s string) (string, bool) {
	var runes []rune
	for i := 0; i < len(s); {
		if s[i] != '\\' {
			r, w := utf8.DecodeRuneInString(s[i:])
			runes = append(runes, r)
			i += w
			continue
		}
		if i+1 >= len(s) || s[i+1] != 'u' {
			return "", false
		}
		i += 2
		var hex string
		if i < len(s) && s[i] == '{' {
			end := strings.IndexByte(s[i:], '}')
			if end < 0 {
				return "", false
			}
			hex = strings.TrimLeft(s[i+1:i+end], "0")
			if hex == "" && end > 1 {
				hex = "0"
			}
			i += end + 1
		} else if i+4 <= len(s) {
			hex = s[i : i+4]
			i += 4
		}
		v, err := strconv.ParseUint(hex, 16, 32)
		if err != nil || v > unicode.MaxRune {
			return "", false
		}
		r := rune(v)
		if n := len(runes); n > 0 && utf16.IsSurrogate(runes[n-1]) && runes[n-1] < 0xdc00 && r >= 0xdc00 && r <= 0xdfff {
			runes[n-1] = utf16.DecodeRune(runes[n-1], r)
			continue
		}
		runes = append(runes, r)
	}
	if len(runes) == 0 {
		return "", false
	}
	for i, r := range runes {
		if r == '\\' || i == 0 && !isIdentifierStart(r) || i > 0 && !isIdentifierPart(r) {
			return "", false
		}
	}
	return string(runes), true
}

// scanCaptures counts the capturing groups of the whole pattern and collects
// their names, for validating backreferences with the u flag.
func (self *_RegExp_parser) scanCaptures() {
	if self.capturesScanned {
		return
	}
	self.capturesScanned = true
	inClass := false
	for i := 0; i < self.length; i++ {
		switch self.str[i] {
		case '\\':
			i++
		case '[':
			inClass = true
		case ']':
			inClass = false
		case '(':
			if inClass {
				break
			}
			if i+1 < self.length && self.str[i+1] == '?' {
				if i+3 < self.length && self.str[i+2] == '<' && self.str[i+3] != '=' && self.str[i+3] != '!' {
					self.captureCount++
					if end := strings.IndexByte(self.str[i+3:], '>'); end >= 0 {
						name, _ := decodeGroupName(self.str[i+3 : i+3+end])
						self.captureNames = append(self.captureNames, name)
					}
				}
				break
			}
			self.captureCount++
		}
	}
}

// [...]
func (self *_RegExp_parser) scanBracket() {
	str := self.str[self.chrOffset:]
	if strings.HasPrefix(str, "[]") || strings.HasPrefix(str, "[^]") {
		self.count(termCost{goUnits: goClassUnits, goInsts: 1, r2: 10, r2Fold: 10})
	}
	if strings.HasPrefix(str, "[]") {
		// [] -- Empty character class
		self.writeString(`[^\x00-\x{10FFFF}]`)
		self.offset += 1
		self.read()
		return
	}

	if strings.HasPrefix(str, "[^]") {
		self.writeString(`[\x00-\x{10FFFF}]`)
		self.offset += 2
		self.read()
		return
	}

	self.classStart = self.chrOffset
	self.classR2Len, self.classR2Offset = self.regexp2.Len(), self.regexp2Offset
	self.classSet, self.classUnsorted, self.classNegEscape = nil, false, false
	self.classProps = self.classProps[:0]
	self.classGoStart = -1
	self.classItems = 0
	if self.ignoreCase {
		if self.passOffset != -1 {
			self.stopPassing()
		}
		self.classGoStart = self.goRegexp.Len()
	}
	self.pass()
	negated := self.chr == '^'
	if self.chr == '^' {
		self.pass()
	}
	for self.chr != -1 && self.chr != ']' {
		leftStart := self.chrOffset
		leftIsClass := self.scanClassAtom()
		left := self.str[leftStart:self.chrOffset]
		if self.unicode {
			if self.chr == '-' && self.offset < self.length && self.str[self.offset] != ']' {
				self.pass()
				rightStart := self.chrOffset
				rightIsClass := self.scanClassAtom()
				if self.err != nil {
					return
				}
				if leftIsClass || rightIsClass {
					self.syntaxError("Invalid character class")
					return
				}
				lo, hi := classAtomValue(left), classAtomValue(self.str[rightStart:self.chrOffset])
				if lo > hi {
					self.syntaxError("Range out of order in character class")
					return
				}
				if !self.refused {
					self.addClassSet(runeSet{lo, hi})
					self.classItem(runeSet{lo, hi})
				}
			} else if self.err == nil && !self.refused {
				// Past the bound the pattern is only validated.
				self.classNegEscape = self.classNegEscape || isNegatedClassEscape(left)
				set := self.classAtomSet(left)
				self.addClassSet(set)
				self.classItem(set)
			}
			continue
		}
		if self.err != nil {
			return
		}
		if self.chr != '-' || self.offset >= self.length || self.str[self.offset] == ']' {
			self.nonUnicodeClassItem(left)
			continue
		}
		if leftIsClass || self.nextIsCharacterClassEscape() {
			// A '-' next to a class escape is a literal, not a range operator.
			self.nonUnicodeClassItem(left)
			self.writeString(`\-`)
			self.read()
			self.nonUnicodeClassItem("-")
			rightStart := self.chrOffset
			self.scanClassAtom()
			self.nonUnicodeClassItem(self.str[rightStart:self.chrOffset])
			continue
		}
		self.pass()
		rightStart := self.chrOffset
		self.scanClassAtom()
		if self.err != nil {
			return
		}
		lo, _ := nonUnicodeClassAtom(left, true)
		hi, _ := nonUnicodeClassAtom(self.str[rightStart:self.chrOffset], true)
		if lo[0] > hi[0] {
			self.syntaxError("Range out of order in character class")
			return
		}
		self.classItem(runeSet{lo[0], hi[0]})
	}
	if self.chr != ']' {
		self.fatal("Unterminated character class")
		return
	}
	self.sortClassSet()
	self.pass()
	cost := termCost{goUnits: goClassUnits + goClassItemUnits*self.classItems, goInsts: 1}
	if negated {
		cost.goUnits += goNegatedUnits
	}
	self.count(cost)
	self.expandRegexp2(regexp2TextUnits(self.classItems))
	if text := self.str[self.classStart:self.chrOffset]; !self.classTexts[text] && !self.refused {
		if self.classTexts == nil {
			self.classTexts = make(map[string]bool)
		}
		self.classTexts[text] = true
		self.expandRegexp2(r2DistinctClassUnits)
	}
	if self.refused {
		return
	}
	if len(self.classProps) > 0 {
		self.writeGoClass(negated)
		self.writeRegexp2Class(negated)
	} else if self.canonFold() {
		self.canonFoldClass(negated)
	} else if self.classNegEscape {
		self.writeRegexp2Set(negated)
	}
}

// writeRegexp2Set writes the class just scanned, which has \D, \W or \S,
// for regexp2 as the set it stands for: regexp2 drops the items after
// such an escape ([a\Dx] did not match x).
func (self *_RegExp_parser) writeRegexp2Set(negated bool) {
	set := self.classSet
	if negated {
		set = set.complement()
	}
	var b strings.Builder
	var units int
	if self.unicode {
		units = writeClass(&b, set, true)
	} else {
		units = writeBMPClass(&b, set)
	}
	self.restartRegexp2Class()
	self.writeRegexp2(self.classStart, self.chrOffset, b.String())
	self.expandRegexp2(units)
}

// isNegatedClassEscape reports whether a class atom is \D, \W or \S.
func isNegatedClassEscape(atom string) bool {
	return len(atom) == 2 && atom[0] == '\\' && (atom[1] == 'D' || atom[1] == 'W' || atom[1] == 'S')
}

// nonUnicodeClassItem counts a class item without the u flag (see
// classItem).
func (self *_RegExp_parser) nonUnicodeClassItem(atom string) {
	if atom == "" {
		self.classItems++
		return
	}
	self.classNegEscape = self.classNegEscape || isNegatedClassEscape(atom)
	// Canonicalize takes no variants of \w's characters for word characters.
	set, _ := nonUnicodeClassAtom(atom, false)
	self.classItem(set)
}

// expandGo and expandRegexp2 count what a property escape costs an engine
// (see maxRegexpExpansion). The pattern is refused as soon as it is past the
// bound for regexp2, which stops the scan, and at its end if it is past it
// for Go's regexp and has no construct Go's regexp lacks; past it, or with
// such a construct, nothing more is written or counted for Go's regexp. The
// text written for each engine grows with its cost (and the escapes'
// length), so it is bounded too.
func (self *_RegExp_parser) expandGo(units int) {
	if self.goUsed() {
		self.goExpansion += units
	}
}

// goUsed reports whether the pattern is still written for Go's regexp.
func (self *_RegExp_parser) goUsed() bool {
	return !self.refused && self.incompat == nil && self.goCost() <= maxRegexpExpansion
}

func (self *_RegExp_parser) expandRegexp2(units int) {
	self.regexp2Expansion += units
	if self.regexp2Expansion > maxRegexpExpansion {
		self.refused = true
	}
}

// writeGoClass rewrites the class that has just been scanned, which has
// property escapes, for Go's regexp when ignoring case: it gets the set the
// class stands for, closed under case folding and matched case-sensitively
// (see canonFold).
func (self *_RegExp_parser) writeGoClass(negated bool) {
	if self.classGoStart < 0 || !self.goUsed() {
		return
	}
	self.goRegexp.Truncate(self.classGoStart)
	w := self.wholeClass()
	if !w.goDone {
		set := caseClosure(self.classSet)
		if negated {
			set = set.complement()
		}
		var b strings.Builder
		w.goUnits = writeClass(&b, set, false)
		w.goText, w.goDone = b.String(), true
		self.expandGo(expansionClass)
		self.expandRegexp2(expansionClass)
	}
	self.goRegexp.WriteString(`(?-i:`)
	self.goRegexp.WriteString(w.goText)
	self.expandGo(w.goUnits)
	self.goRegexp.WriteByte(')')
}

// wholeClass is a class with property escapes written whole as the set it
// stands for, for Go's regexp and regexp2, and what the text costs them:
// computing it takes up to about 0.3 ms, so a pattern computes each
// distinct class once.
type wholeClass struct {
	goText, r2Text   string
	goUnits, r2Units int
	goDone, r2Done   bool
}

// wholeClass returns the class just scanned's, by its text.
func (self *_RegExp_parser) wholeClass() *wholeClass {
	text := self.str[self.classStart:self.chrOffset]
	w := self.wholeClasses[text]
	if w == nil {
		if self.wholeClasses == nil {
			self.wholeClasses = make(map[string]*wholeClass)
		}
		w = &wholeClass{}
		self.wholeClasses[text] = w
	}
	return w
}

// classProperty is a property escape in a class.
type classProperty struct {
	start, end int
	prop       regexpProperty
	negate     bool
}

// writeRegexp2Class writes the class that has just been scanned, which has
// property escapes, to regexp2 (see writeRegexp2Property). Positive
// properties are items of the class. With negated ones, or ignoring case,
// regexp2 gets the set the class stands for (see writeClass).
func (self *_RegExp_parser) writeRegexp2Class(negated bool) {
	self.restartRegexp2Class()
	b := &self.regexp2
	end := self.chrOffset // after the ]
	b.WriteString(self.str[self.regexp2Offset:self.classStart])
	self.regexp2Offset = end
	whole := self.ignoreCase || self.classNegEscape
	for _, p := range self.classProps {
		whole = whole || p.negate
	}
	if !whole {
		pos, names, ranges := self.classStart, 0, 0
		for _, p := range self.classProps {
			b.WriteString(self.str[pos:p.start])
			if name := p.prop.goName(); name != "" {
				b.WriteString(`\p{` + name + `}`)
				names++
			} else {
				c := p.prop.classItems(false, true)
				b.WriteString(c.text)
				names += c.names
				ranges += c.ranges
			}
			pos = p.end
		}
		b.WriteString(self.str[pos:end])
		// The class's ranges are sorted together.
		self.expandRegexp2(expansionName*names + regexp2TextUnits(ranges))
		return
	}
	w := self.wholeClass()
	if !w.r2Done {
		set := self.classSet
		if self.ignoreCase {
			// ECMAScript matches a character against a class ignoring case
			// if one of its case variants is in it.
			set = caseClosure(set)
		}
		if negated {
			set = set.complement()
		}
		var cb strings.Builder
		w.r2Units = writeClass(&cb, set, true)
		w.r2Text, w.r2Done = cb.String(), true
		self.expandRegexp2(expansionClass)
		self.expandGo(expansionClass)
	}
	if self.ignoreCase {
		b.WriteString(`(?-i:`)
	}
	b.WriteString(w.r2Text)
	self.expandRegexp2(w.r2Units)
	if self.ignoreCase {
		b.WriteByte(')')
	}
	self.classProps = self.classProps[:0]
}

// restartRegexp2Class drops what the class's items wrote for regexp2 (see
// writeRegexp2), so that the class can be written whole.
func (self *_RegExp_parser) restartRegexp2Class() {
	if self.regexp2Offset > self.classStart {
		self.regexp2.Truncate(self.classR2Len)
		self.regexp2Offset = self.classR2Offset
	}
}

// classAtomSet returns the set a validated class atom stands for, with the u
// flag.
func (self *_RegExp_parser) classAtomSet(atom string) runeSet {
	if len(atom) < 2 || atom[0] != '\\' {
		r := classAtomValue(atom)
		return runeSet{r, r}
	}
	var s runeSet
	switch atom[1] {
	case 'd', 'D':
		s = runeSet{'0', '9'}
	case 's', 'S':
		for _, r := range WhitespaceChars {
			s = s.union(runeSet{r, r})
		}
	case 'w', 'W':
		s = runeSet{'0', '9', 'A', 'Z', '_', '_', 'a', 'z'}
		if self.ignoreCase {
			// With the u and i flags, the characters whose simple case
			// folding is a word character are word characters too.
			s = s.union(runeSet{0x17f, 0x17f, 0x212a, 0x212a})
		}
	case 'p', 'P':
		prop, _ := lookupRegexpProperty(atom[3 : len(atom)-1])
		return prop.charSet(atom[1] == 'P', false)
	default:
		r := classAtomValue(atom)
		return runeSet{r, r}
	}
	if atom[1] >= 'A' && atom[1] <= 'Z' {
		return s.complement()
	}
	return s
}

// scanClassAtom writes a single class atom and reports whether it stands for more than one character.
func (self *_RegExp_parser) scanClassAtom() bool {
	if self.chr == '\\' {
		self.read()
		isClass := isCharacterClassEscape(self.chr) || self.unicode && (self.chr == 'p' || self.chr == 'P')
		self.scanEscape(true)
		return isClass
	}
	if self.chr == '-' && !self.unicode {
		// A '-' in atom position is a literal; escape it so re2 cannot pair it with the next atom.
		self.writeString(`\-`)
		self.read()
		return false
	}
	self.pass()
	return false
}

// isCharacterClassEscape reports whether c is a class escape standing for more than one character.
func isCharacterClassEscape(c rune) bool {
	return c == 'd' || c == 'D' || c == 's' || c == 'S' || c == 'w' || c == 'W'
}

// nextIsCharacterClassEscape reports whether the input after the current character is a class escape.
func (self *_RegExp_parser) nextIsCharacterClassEscape() bool {
	return self.offset+1 < self.length && self.str[self.offset] == '\\' && isCharacterClassEscape(rune(self.str[self.offset+1]))
}

// classAtomValue returns the code point of a class atom that has been
// validated with the u flag and is not a class escape.
func classAtomValue(atom string) rune {
	if atom[0] != '\\' {
		r, _ := utf8.DecodeRuneInString(atom)
		return r
	}
	switch c := atom[1]; c {
	case 'x':
		v, _ := strconv.ParseUint(atom[2:4], 16, 32)
		return rune(v)
	case 'u':
		if atom[2] == '{' {
			// Up to 0x10FFFF, but with any number of leading zeros.
			v, _ := strconv.ParseUint("0"+strings.TrimLeft(atom[3:len(atom)-1], "0"), 16, 32)
			return rune(v)
		}
		v, _ := strconv.ParseUint(atom[2:6], 16, 32)
		return rune(v)
	case 'c':
		return rune(atom[2] % 32)
	case '0':
		return 0
	case 'b':
		return '\b'
	case 'f':
		return '\f'
	case 'n':
		return '\n'
	case 'r':
		return '\r'
	case 't':
		return '\t'
	case 'v':
		return '\v'
	default:
		r, _ := utf8.DecodeRuneInString(atom[1:])
		return r
	}
}

// isSyntaxCharacter reports whether c may be escaped with the u flag.
func isSyntaxCharacter(c rune) bool {
	return strings.ContainsRune(`^$\.*+?()[]{}|/`, c)
}

// \...
func (self *_RegExp_parser) scanEscape(inClass bool) {
	offset := self.chrOffset

	var length, base uint32
	switch self.chr {

	case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		if self.unicode {
			self.scanUnicodeDecimalEscape(inClass)
			return
		}
		self.scanLegacyDecimalEscape(inClass)
		return

	case 'x':
		self.read()
		length, base = 2, 16

	case 'u':
		self.read()
		if self.chr == '{' && self.unicode {
			self.read()
			length, base = 0, 16
		} else {
			length, base = 4, 16
		}

	case 'p', 'P':
		if self.unicode {
			self.scanPropertyEscape(inClass)
			return
		}
		// An identity escape (see the default case).
		self.pass()
		self.writeRegexp2(offset-1, self.chrOffset, self.str[offset:self.chrOffset])
		return

	case 'b':
		if inClass {
			self.write([]byte{'\\', 'x', '0', '8'})
			self.read()
			return
		}
		fallthrough

	case 'B':
		if inClass {
			if self.unicode {
				self.syntaxError("Invalid escape")
				return
			}
			// An identity escape (see the default case).
			self.pass()
			self.writeRegexp2(offset-1, self.chrOffset, "B")
			return
		}
		fallthrough

	case 'd', 'D', 'w', 'W':
		// This is slightly broken, because ECMAScript
		// includes \v in \s, \S, while re2 does not
		fallthrough

	case '\\':
		fallthrough

	case 'f', 'n', 'r', 't', 'v':
		self.passString(offset-1, self.offset)
		self.read()
		return

	case 'c':
		self.read()
		var value int64
		if 'a' <= self.chr && self.chr <= 'z' {
			value = int64(self.chr - 'a' + 1)
		} else if 'A' <= self.chr && self.chr <= 'Z' {
			value = int64(self.chr - 'A' + 1)
		} else {
			if self.unicode {
				self.syntaxError("Invalid Unicode escape")
				return
			}
			if inClass && (self.chr >= '0' && self.chr <= '9' || self.chr == '_') {
				// Annex B: in a class, a digit or _ is a control letter too.
				self.writeLegacyChar(offset-1, self.offset, self.chr%32)
				self.read()
				return
			}
			if self.err != nil {
				return
			}
			// Annex B: the backslash is itself, and the c is read next.
			self.writeString(`\\`)
			self.writeRegexp2(offset-1, offset, `\\`)
			self.seek(offset)
			return
		}
		tmp := []byte{'\\', 'x', '0', 0}
		if value >= 16 {
			tmp = tmp[0:2]
		} else {
			tmp = tmp[0:3]
		}
		tmp = strconv.AppendInt(tmp, value, 16)
		self.write(tmp)
		self.read()
		return
	case 's':
		if inClass {
			self.writeString(WhitespaceChars)
		} else {
			self.writeString("[" + WhitespaceChars + "]")
		}
		self.read()
		return
	case 'S':
		if inClass {
			self.error(false, "S in class")
			self.read()
			return
		} else {
			self.writeString("[^" + WhitespaceChars + "]")
		}
		self.read()
		return
	case 'k':
		if self.unicode {
			self.scanUnicodeNamedReference(inClass)
			return
		}
		// Annex B: \k is a named reference if the pattern has named groups,
		// and an identity escape if not.
		if self.scanCaptures(); len(self.captureNames) > 0 {
			self.scanUnicodeNamedReference(inClass)
			return
		}
		self.pass()
		self.writeRegexp2(offset-1, self.chrOffset, "k")
		return
	default:
		if self.chr == -1 {
			self.syntaxError("\\ at end of pattern")
			return
		}
		if self.unicode && !isSyntaxCharacter(self.chr) && !(inClass && self.chr == '-') {
			self.syntaxError("Invalid escape")
			return
		}
		// $ is an identifier character, so we have to have
		// a special case for it here
		if self.chr == '$' || self.chr < utf8.RuneSelf && !isIdentifierPart(self.chr) {
			// A non-identifier character needs escaping
			self.passString(offset-1, self.offset)
			self.read()
			return
		}
		// Unescape the character for re2, and for regexp2, which takes
		// some escaped letters for classes or assertions (\A, \Z, \G, \e).
		self.pass()
		self.writeRegexp2(offset-1, self.chrOffset, self.str[offset:self.chrOffset])
		return
	}

	// Otherwise, we're a \u.... or \x...
	valueOffset := self.chrOffset

	if length > 0 {
		for length := length; length > 0; length-- {
			digit := uint32(digitValue(self.chr))
			if digit >= base {
				// Not a valid digit
				goto skip
			}
			self.read()
		}
	} else {
		var value uint32
		for self.chr != '}' && self.chr != -1 {
			digit := uint32(digitValue(self.chr))
			if digit >= base {
				// Not a valid digit
				self.syntaxError("Invalid Unicode escape")
				return
			}
			if value <= 0x10FFFF {
				value = value*16 + digit
			}
			self.read()
		}
		if self.chr != '}' || self.chrOffset == valueOffset || value > 0x10FFFF {
			self.syntaxError("Invalid Unicode escape")
			return
		}
	}

	if length == 4 || length == 0 {
		self.write([]byte{
			'\\',
			'x',
			'{',
		})
		self.passString(valueOffset, self.chrOffset)
		if length != 0 {
			self.writeByte('}')
		} else {
			// The closing brace.
			self.pass()
		}
	} else if length == 2 {
		self.passString(offset-1, valueOffset+2)
	} else {
		// Should never, ever get here...
		self.error(true, "re2: Illegal branch in scanEscape")
		return
	}

	return

skip:
	if self.unicode {
		if self.str[offset] == 'u' {
			self.syntaxError("Invalid Unicode escape")
		} else {
			self.syntaxError("Invalid escape")
		}
		return
	}
	// Annex B: an identity escape of x or u; the digits after it are read
	// again, as themselves.
	self.seek(offset + 1)
	self.passString(offset, offset+1)
	self.writeRegexp2(offset-1, offset+1, self.str[offset:offset+1])
}

// scanLegacyDecimalEscape scans \ and a digit without the u flag (Annex B):
// outside a class, a backreference if its number is at most the number of
// capturing groups; else an identity escape of 8 or 9, or a legacy octal
// escape of up to three digits and at most 0377.
func (self *_RegExp_parser) scanLegacyDecimalEscape(inClass bool) {
	offset := self.chrOffset
	if !inClass && self.chr != '0' {
		value, i := 0, offset
		for i < self.length && self.str[i] >= '0' && self.str[i] <= '9' {
			if value < 1<<30 {
				value = value*10 + int(self.str[i]-'0')
			}
			i++
		}
		self.scanCaptures()
		if value <= self.captureCount {
			self.seek(i)
			self.error(false, "re2: Invalid \\%d <backreference>", value)
			self.backref = true
			self.writeBackref(i, value)
			return
		}
	}
	if self.chr >= '8' {
		self.pass()
		self.writeRegexp2(offset-1, self.chrOffset, self.str[offset:self.chrOffset])
		return
	}
	first := self.chr - '0'
	value := first
	self.read()
	for n := 1; n < 3 && self.chr >= '0' && self.chr <= '7' && (n == 1 || first <= 3); n++ {
		value = value*8 + self.chr - '0'
		self.read()
	}
	self.writeLegacyChar(offset-1, self.chrOffset, value)
}

// writeLegacyChar writes, for both engines, the character that the escape
// from start to end stands for (without the u flag).
func (self *_RegExp_parser) writeLegacyChar(start, end int, r rune) {
	self.writeString(fmt.Sprintf(`\x{%x}`, r))
	self.writeRegexp2(start, end, fmt.Sprintf(`\u%04x`, r))
}

// scanUnicodeDecimalEscape scans \0 and backreferences with the u flag.
func (self *_RegExp_parser) scanUnicodeDecimalEscape(inClass bool) {
	offset := self.chrOffset
	nextIsDigit := self.offset < self.length && self.str[self.offset] >= '0' && self.str[self.offset] <= '9'
	if self.chr == '0' {
		if nextIsDigit {
			if inClass {
				self.syntaxError("Invalid class escape")
			} else {
				self.syntaxError("Invalid decimal escape")
			}
			return
		}
		self.read()
		self.passString(offset-1, self.chrOffset)
		return
	}
	if inClass {
		if self.chr <= '7' {
			self.syntaxError("Invalid class escape")
		} else {
			self.syntaxError("Invalid escape")
		}
		return
	}
	value := 0
	for self.chr >= '0' && self.chr <= '9' {
		if value < 1<<30 {
			value = value*10 + int(self.chr-'0')
		}
		self.read()
	}
	self.scanCaptures()
	if value > self.captureCount {
		self.syntaxError("Invalid escape")
		return
	}
	self.error(false, "re2: Invalid \\%d <backreference>", value)
	self.backref = true
	self.writeBackref(self.chrOffset, value)
}

// openCapture is a capturing group being scanned: its number, and name.
type openCapture struct {
	num  int
	name string
}

// inOwnGroup reports whether a backreference to group num, or to the group
// named name, is in the group. ECMAScript has it match nothing: the group
// has no capture there, as it is reset at each iteration of a quantified
// group it is in, and regexp2 matches it with its capture from the last
// (/((a)\1|.){2}/ on "aab").
func (self *_RegExp_parser) inOwnGroup(num int, name string) bool {
	for _, c := range self.openCaptures {
		if num != 0 && c.num == num || name != "" && c.name == name {
			return true
		}
	}
	return false
}

// writeBackref writes for regexp2 the backreference to group num that ends
// at end: numbered as the groups are once the hidden ones are in (see
// planIterations), and compared ignoring case if the pattern ignores case.
// One in the group matches nothing (see inOwnGroup).
func (self *_RegExp_parser) writeBackref(end, num int) {
	if self.inOwnGroup(num, "") {
		self.writeRegexp2(self.escStart, end, "(?:)")
		return
	}
	text := self.str[self.escStart:end]
	if n := self.groupNumber(num); n != num {
		text = `\` + strconv.Itoa(n)
	}
	if self.canonFold() {
		text = "(?i:" + text + ")"
	}
	if text != self.str[self.escStart:end] {
		self.writeRegexp2(self.escStart, end, text)
	}
}

// scanUnicodeNamedReference scans \k<name> with the u flag.
func (self *_RegExp_parser) scanUnicodeNamedReference(inClass bool) {
	if inClass {
		self.syntaxError("Invalid escape")
		return
	}
	self.read()
	if self.chr != '<' {
		self.syntaxError("Invalid named reference")
		return
	}
	end := strings.IndexByte(self.str[self.offset:], '>')
	if end < 0 {
		self.syntaxError("Invalid capture group name")
		return
	}
	name, valid := decodeGroupName(self.str[self.offset : self.offset+end])
	if !valid {
		self.syntaxError("Invalid capture group name")
		return
	}
	self.scanCaptures()
	found := false
	for _, n := range self.captureNames {
		if n == name {
			found = true
			break
		}
	}
	if !found && self.deferred == "" {
		self.deferred = "Invalid named capture referenced"
	}
	self.backref = true
	if found && self.inOwnGroup(0, name) {
		self.writeRegexp2(self.escStart, self.offset+end+1, "(?:)")
	} else if self.canonFold() {
		// regexp2 compares it ignoring case.
		self.writeRegexp2(self.escStart, self.offset+end+1, `(?i:\k<`+name+`>)`)
	} else if strings.IndexByte(self.str[self.offset:self.offset+end], '\\') >= 0 {
		self.writeRegexp2(self.offset, self.offset+end, name)
	}
	self.seek(self.offset + end + 1)
	self.error(false, "named group back-reference")
}

// scanPropertyEscape scans \p{...} or \P{...} with the u flag.
func (self *_RegExp_parser) scanPropertyEscape(inClass bool) {
	start := self.chrOffset - 1 // the backslash
	negate := self.chr == 'P'
	invalid := "Invalid property name"
	if inClass {
		invalid = "Invalid property name in character class"
	}
	self.read()
	if self.chr != '{' {
		self.syntaxError(invalid)
		return
	}
	end := strings.IndexByte(self.str[self.offset:], '}')
	if end < 0 {
		self.syntaxError(invalid)
		return
	}
	prop, ok := lookupRegexpProperty(self.str[self.offset : self.offset+end])
	if !ok {
		self.syntaxError(invalid)
		return
	}
	end += self.offset + 1
	self.seek(end)

	if !self.regexp2Used {
		self.regexp2.Grow(len(self.str))
		self.regexp2Used = true
	}
	self.expandGo(expansionEscape)
	self.expandRegexp2(expansionEscape)
	if self.refused {
		// The rest of the pattern is only validated.
		return
	}
	if inClass {
		// The class is written when it ends.
		if self.goUsed() {
			self.writeGoProperty(prop, negate, inClass)
		}
		self.classProps = append(self.classProps, classProperty{start: start, end: end, prop: prop, negate: negate})
		return
	}
	self.regexp2.WriteString(self.str[self.regexp2Offset:start])
	self.regexp2Offset = end
	if self.goUsed() {
		self.writeGoProperty(prop, negate, inClass)
	}
	self.writeRegexp2Property(prop, negate)
}

// writeGoProperty writes a property escape for Go's regexp, which knows
// General_Category and Script by name; other sets are written out.
// Ignoring case, the escape is written as its set closed under case folding
// and matched case-sensitively (see canonFold), and a class with a property
// escape is written whole (see writeGoClass).
func (self *_RegExp_parser) writeGoProperty(prop regexpProperty, negate, inClass bool) {
	if self.ignoreCase && inClass {
		// The class is written whole (see writeGoClass).
		return
	}
	name := prop.goRegexpName()
	if self.ignoreCase && !prop.closed(negate) {
		// The set closed under case folding, or the table and what makes
		// up its closure (the case variants of its characters, or for its
		// negation, its characters some of whose case variants are not in
		// it), whichever costs less.
		c := prop.class(negate, true, false)
		extra := prop.foldExtra(false)
		if negate {
			extra = prop.notClosed()
		}
		if units := classUnits([]string{name}, extra, false); name == "" || c.units <= units {
			self.expandGo(c.units)
			self.writeString(c.text)
			return
		}
		if negate {
			self.writeString(`[\P{`)
		} else {
			self.writeString(`[\p{`)
		}
		self.writeString(name)
		self.writeByte('}')
		writeRanges(&self.goRegexp, extra, `\x`)
		self.writeByte(']')
		self.expandGo(classUnits([]string{name}, extra, false))
		return
	}
	if name != "" {
		if inClass {
			self.expandGo(expansionClassTable * prop.ranges())
		} else {
			self.expandGo(2 * prop.ranges() / 5)
		}
		if negate {
			self.writeString(`\P{`)
		} else {
			self.writeString(`\p{`)
		}
		self.writeString(name)
		self.writeByte('}')
		return
	}
	var c propertyClass
	if inClass {
		c = prop.classItems(negate, false)
	} else {
		c = prop.class(negate, false, false)
	}
	self.expandGo(c.units)
	self.writeString(c.text)
}

// writeRegexp2Property writes a property escape outside a class for regexp2.
//
// regexp2 stops searching a set's categories at the first negated one that
// contains the character, and gets alternations of sets with different
// options wrong, so it gets no negated categories: \P{L} is written [^\p{L}].
// It ignores case by lower-casing, which gets some sets wrong (around U+0130
// and the circled letters) and is slow for large ones, so ignoring case it
// gets sets closed under case folding, matched case-sensitively: \P{L} is
// then written (?-i:[^\p{L}]|[letters with case variants that are not]).
// A property with no name is written as a class (see writeClass).
func (self *_RegExp_parser) writeRegexp2Property(prop regexpProperty, negate bool) {
	b := &self.regexp2
	name := prop.goName()
	switch {
	case name != "" && !negate && !self.ignoreCase:
		b.WriteString(`\p{` + name + `}`)
	case name != "":
		self.expandRegexp2(expansionName)
		var extra runeSet
		if self.ignoreCase {
			extra = prop.foldExtra(negate)
			if c := prop.class(negate, true, true); c.units < regexp2TextUnits(len(extra)/2) {
				// The set written as a class (with the tables it contains
				// by name) is cheaper than the name and its variants.
				self.expandRegexp2(c.units)
				b.WriteString(`(?-i:`)
				b.WriteString(c.text)
				b.WriteByte(')')
				return
			}
			self.expandRegexp2(regexp2TextUnits(len(extra) / 2))
			b.WriteString(`(?-i:`)
		}
		switch {
		case negate:
			b.WriteString(`[^\p{` + name + `}]`)
			if len(extra) > 0 {
				b.WriteString(`|[`)
				writeRanges(b, extra, `\u`)
				b.WriteByte(']')
			}
		case len(extra) > 0:
			b.WriteString(`[\p{` + name + `}`)
			writeRanges(b, extra, `\u`)
			b.WriteByte(']')
		default:
			b.WriteString(`\p{` + name + `}`)
		}
		if self.ignoreCase {
			b.WriteByte(')')
		}
	default:
		c := prop.class(negate, self.ignoreCase, true)
		self.expandRegexp2(c.units)
		if self.ignoreCase {
			b.WriteString(`(?-i:`)
			b.WriteString(c.text)
			b.WriteByte(')')
		} else {
			b.WriteString(c.text)
		}
	}
}

func (self *_RegExp_parser) pass() {
	if self.passOffset == self.chrOffset {
		self.passOffset = self.offset
	} else {
		if self.passOffset != -1 {
			self.stopPassing()
		}
		if self.chr != -1 {
			self.goRegexp.WriteRune(self.chr)
		}
	}
	self.read()
}

func (self *_RegExp_parser) passString(start, end int) {
	if self.passOffset == start {
		self.passOffset = end
		return
	}
	if self.passOffset != -1 {
		self.stopPassing()
	}
	self.goRegexp.WriteString(self.str[start:end])
}

// fatal reports a syntax error, with V8's message format.
func (self *_RegExp_parser) fatal(msg string) {
	self.syntaxError(msg)
}

// syntaxError reports an error with the message V8 gives.
func (self *_RegExp_parser) syntaxError(msg string) {
	self.error(true, "Invalid regular expression: /%s/: %s", self.str, msg)
}

func (self *_RegExp_parser) error(fatal bool, msg string, msgValues ...interface{}) {
	if self.err != nil {
		return
	}
	e := regexpParseError{
		offset: self.offset,
		err:    fmt.Sprintf(msg, msgValues...),
	}
	if !fatal {
		// Keep scanning, to validate the rest of the pattern and write it
		// for regexp2.
		if self.incompat == nil {
			self.incompat = RegexpErrorIncompatible{e}
		}
		return
	}
	self.err = RegexpSyntaxError{e}
	self.offset = self.length
	self.chr = -1
}
