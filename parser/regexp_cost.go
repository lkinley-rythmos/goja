package parser

import (
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// What a pattern costs the engines to compile besides its property escapes,
// in the units of maxRegexpExpansion (about 100 ns of compile time),
// measured term by term on patterns of tens of thousands of terms.
//
// Go's regexp parses a term in about 0.3 to 1.5 µs (goUnits), and compiles
// each instruction of the program, in which a repeat is expanded into
// copies of what it repeats, in about 0.3 µs (goInstUnits): a literal
// character is one instruction and nothing more. regexp2 parses and
// compiles the text it is given, in 0.05 to 3 µs a term, and more ignoring
// case, when it adds the case variants of every literal and class (r2 and
// r2Fold); a class of n items costs it more per item the more it has (see
// regexp2TextUnits).
//
// Ignoring case, Go's regexp adds the case variants of a class by trying
// every code point of its ranges between A and U+1E943 (about 50 ns each:
// [A-\uffef] takes 3 ms), and regexp2 of every code point of its ranges
// and class escapes (about 40 ns each; \W, \S and \D stand for over a
// million, and take 50 ms each, . 25 ms). So a class whose case variants
// would cost more than rewriting it (classFoldUnits) is given to both as
// the set it stands for, closed under case folding and matched
// case-sensitively (see foldClass); and regexp2 gets the escapes that stand
// for most of Unicode, and \w, matched case-sensitively as the sets they
// stand for ignoring case, which they are closed under.
type termCost struct {
	goUnits, goInsts int
	r2, r2Fold       int
}

var (
	costLiteral   = termCost{goInsts: 1, r2: 1, r2Fold: 10}
	costEscape    = termCost{goUnits: 1, goInsts: 1, r2: 5, r2Fold: 10}  // an escape standing for a character
	costClassEsc  = termCost{goUnits: 8, goInsts: 1, r2: 7, r2Fold: 30}  // \d \D \w \W
	costSpaceEsc  = termCost{goUnits: 25, goInsts: 1, r2: 7, r2Fold: 45} // \s \S
	costDot       = termCost{goUnits: 12, goInsts: 1, r2: 3, r2Fold: 15}
	costAssertion = termCost{goUnits: 11, goInsts: 1, r2: 6, r2Fold: 6}
	costBoundary  = termCost{goUnits: 11, goInsts: 1, r2: 90, r2Fold: 100} // \b \B (see regexp2Boundary)
	costCapture   = termCost{goUnits: 13, goInsts: 2, r2: 15, r2Fold: 25}
	costNamed     = termCost{goUnits: 13, goInsts: 2, r2: 45, r2Fold: 65}
	costGroup     = termCost{goUnits: 20, r2: 12, r2Fold: 20}
	costAround    = termCost{r2: 17, r2Fold: 25}
	costBackref   = termCost{r2: 10, r2Fold: 15}
	costAlternate = termCost{goUnits: 20, goInsts: 1, r2: 15, r2Fold: 30}
	costQuantity  = termCost{goUnits: 10, goInsts: 1, r2: 3, r2Fold: 8}
	costRepeat    = termCost{goUnits: 30, r2: 5, r2Fold: 9}
)

const (
	goInstUnits = 3
	// A class costs Go's regexp goClassUnits, and goClassItemUnits an item.
	goClassUnits     = 5
	goClassItemUnits = 3
	// Ignoring case, Go's regexp adds the case variants of \w and \W in
	// about 4 µs, and of \s and \S (written as a class of 25) in 3 µs.
	goFoldWordUnits  = 40
	goFoldSpaceUnits = 30
	// A negated class costs Go's regexp goNegatedUnits more.
	goNegatedUnits = 5
	// Case variants are added to ranges between these (Go's regexp/syntax
	// minFold and maxFold).
	goMinFold = 0x41
	goMaxFold = 0x1e943
	// regexp2 adds the case variants of a class ignoring case in about 4 µs,
	// 1 µs an item, 30 ns a code point and 200 ns one with case variants
	// (Go's regexp in 50 ns a code point and 50 ns one with case variants).
	r2FoldClassUnits = 40
	r2FoldItemUnits  = 10
	// classFoldUnits is what rewriting a class as its set closed under case
	// folding costs, for both engines; a class is rewritten when adding its
	// case variants would cost them more. The closure takes one pass over
	// the set and the table of case variants, however wide the set is:
	// about 7 µs with Canonicalize (canonClosure) and 14 µs with simple case
	// folding (caseClosure), which the units round up; writing the set is
	// counted by its ranges.
	classFoldUnits = 400
)

// count counts a term's cost.
func (self *_RegExp_parser) count(c termCost) {
	if self.goUsed() {
		self.goExpansion += c.goUnits
		self.goInsts += int64(c.goInsts)
	}
	if self.ignoreCase {
		self.expandRegexp2(c.r2Fold)
	} else {
		self.expandRegexp2(c.r2)
	}
}

// goCost is what the pattern costs Go's regexp so far.
func (self *_RegExp_parser) goCost() int64 {
	return int64(self.goExpansion) + goInstUnits*self.goInsts
}

// startAtom notes where the instructions of the atom being scanned start,
// for a quantifier after it.
func (self *_RegExp_parser) startAtom() {
	self.atomInsts = self.goInsts
}

// countQuantifier counts a quantifier with its bounds (max is -1 for none)
// after an atom. Go's regexp expands a repeat into copies of the atom: max
// of them, the last max-min optional, or min and a loop. A count over 1,000,
// which Go's regexp refuses, or a program that would take Go's regexp too
// long to compile, leaves the pattern to regexp2, as the constructs Go's
// regexp lacks do (regexp2 does not expand repeats).
func (self *_RegExp_parser) countQuantifier(min, max int) {
	if min > maxGoRepeat || max > maxGoRepeat {
		self.error(false, "re2: invalid repeat count")
	}
	if max >= 0 && max < min {
		max = min
	}
	if min == 0 && (max == 1 || max == -1) || min == 1 && max == -1 {
		self.count(costQuantity)
		return
	}
	self.count(costRepeat)
	if !self.goUsed() {
		return
	}
	atom := self.goInsts - self.atomInsts
	if max < 0 {
		self.goInsts += atom*int64(min-1) + 1
	} else if max > 0 {
		self.goInsts += atom*int64(max-1) + int64(max-min)
	} else {
		self.goInsts -= atom
	}
	if self.goCost() > maxRegexpExpansion {
		self.error(false, "re2: expression too large")
	}
}

// classItem counts an item of a class ignoring case: what adding its case
// variants costs each engine, from its set; wide is a class escape standing
// for most of Unicode, and word \w or \W.
func (self *_RegExp_parser) classItem(set runeSet, wide, word bool) {
	self.classItems++
	if !self.ignoreCase {
		return
	}
	if !self.unicode {
		// With the u flag the class's set is kept anyway.
		self.classSet = self.classSet.union(set)
	}
	if word {
		self.classGoFold += goFoldWordUnits
	}
	if wide {
		self.classR2Span += unicode.MaxRune + 1
		return
	}
	for i := 0; i < len(set); i += 2 {
		lo, hi := set[i], set[i+1]
		cased := int64(casedIn(lo, hi))
		self.classR2Span += int64(hi-lo) + 1 + 6*cased
		if lo <= goMinFold && hi >= goMaxFold || hi < goMinFold || lo > goMaxFold {
			continue
		}
		self.classGoFold += int(min(hi, goMaxFold)-max(lo, goMinFold)+1)/2 + int(cased)/2
	}
}

// CasedIn returns how many code points from lo to hi have case variants
// (under simple case folding).
func CasedIn(lo, hi rune) int {
	return casedIn(lo, hi)
}

func casedIn(lo, hi rune) int {
	runes, _ := caseFoldOrbits()
	i := sort.Search(len(runes), func(i int) bool { return runes[i] >= lo })
	j := sort.Search(len(runes), func(i int) bool { return runes[i] > hi })
	return j - i
}

// foldClass counts what adding the case variants of the class just scanned
// costs the engines, or rewrites it for both when that would cost them more
// than its set (see termCost). The set is closed under simple case folding,
// which is what Go's regexp matches ignoring case, and ECMAScript with the u
// flag.
func (self *_RegExp_parser) foldClass(negated bool) {
	r2Fold := r2FoldClassUnits + r2FoldItemUnits*self.classItems + int(min(self.classR2Span/3, maxRegexpExpansion+1))
	if self.classGoFold+r2Fold <= classFoldUnits {
		if self.goUsed() {
			self.goExpansion += self.classGoFold
		}
		self.expandRegexp2(r2Fold)
		return
	}
	end := self.chrOffset // after the ]
	text := self.str[self.classStart:end]
	rw, ok := self.foldedClasses[text]
	if !ok {
		set := caseClosure(self.classSet)
		if negated {
			set = set.complement()
		}
		var goText, r2Text strings.Builder
		goText.WriteString(`(?-i:`)
		rw.goUnits = writeClass(&goText, set, false)
		goText.WriteByte(')')
		r2Text.WriteString(`(?-i:`)
		if self.unicode {
			rw.r2Units = writeClass(&r2Text, set, true)
		} else {
			rw.r2Units = writeBMPClass(&r2Text, set)
		}
		r2Text.WriteByte(')')
		rw.goText, rw.r2Text = goText.String(), r2Text.String()
		if self.foldedClasses == nil {
			self.foldedClasses = make(map[string]foldedClass)
		}
		self.foldedClasses[text] = rw
		self.expandGo(classFoldUnits)
		self.expandRegexp2(classFoldUnits)
	}
	if self.goUsed() && self.classGoStart >= 0 {
		self.goRegexp.Truncate(self.classGoStart)
		self.goRegexp.WriteString(rw.goText)
		self.expandGo(rw.goUnits)
	}
	self.restartRegexp2Class()
	self.writeRegexp2(self.classStart, end, rw.r2Text)
	self.expandRegexp2(rw.r2Units)
}

// foldedClass is a class rewritten for both engines as its set closed under
// case folding, and what that costs them.
type foldedClass struct {
	goText, r2Text   string
	goUnits, r2Units int
}

// writeRegexp2 writes text for regexp2 in place of the input from start to
// end.
func (self *_RegExp_parser) writeRegexp2(start, end int, text string) {
	if self.refused {
		return
	}
	if !self.regexp2Used {
		self.regexp2.Grow(len(self.str))
		self.regexp2Used = true
	}
	self.regexp2.WriteString(self.str[self.regexp2Offset:start])
	self.regexp2.WriteString(text)
	self.regexp2Offset = end
}

// writeBMPClass writes a class standing for the code units of s, for
// regexp2 without the u flag, which matches UTF-16 code units and takes no
// \u{...} escapes. It returns what the class costs regexp2.
func writeBMPClass(b *strings.Builder, s runeSet) int {
	var bmp runeSet
	for i := 0; i < len(s) && s[i] <= 0xffff; i += 2 {
		bmp = append(bmp, s[i], min(s[i+1], 0xffff))
	}
	negated := false
	if comp := bmp.complement(); len(comp) > 0 && comp[len(comp)-1] == unicode.MaxRune {
		comp[len(comp)-1] = 0xffff
		if comp[len(comp)-2] > 0xffff {
			comp = comp[:len(comp)-2]
		}
		if len(comp) < len(bmp) {
			bmp, negated = comp, true
		}
	}
	b.WriteByte('[')
	if negated {
		b.WriteByte('^')
	}
	var buf [8]byte
	hex4 := func(r rune) {
		b.WriteString(`\u`)
		h := strconv.AppendUint(buf[:0], uint64(r)|0x10000, 16)
		b.Write(h[1:])
	}
	for i := 0; i < len(bmp); i += 2 {
		hex4(bmp[i])
		if bmp[i+1] != bmp[i] {
			b.WriteByte('-')
			hex4(bmp[i+1])
		}
	}
	b.WriteByte(']')
	return regexp2TextUnits(len(bmp) / 2)
}

// Case-insensitive regexp2 is given these for the class escapes \w and \W,
// and those standing for most of Unicode: the sets they stand for ignoring
// case, which they are closed under (\w and \W include the characters whose
// case variants are word characters, U+017F and U+212A, as Go's regexp and ECMAScript
// with the u flag have them).
const (
	regexp2FoldWord    = `(?-i:[\w\u017f\u212a])`
	regexp2FoldNonWord = `(?-i:[^\w\u017f\u212a])`
)

// regexp2Boundary returns what regexp2 is given for \b (or \B, negated):
// its own takes letters, marks, digits and connector punctuation of any
// script for word characters, where ECMAScript takes those of \w, and with
// the u and i flags U+017F and U+212A, whose case variants are.
func (self *_RegExp_parser) regexp2Boundary(negated bool) string {
	word := `[0-9A-Z_a-z]`
	if self.unicode && self.ignoreCase {
		word = `[0-9A-Z_a-z\u017f\u212a]`
	}
	var b strings.Builder
	if self.ignoreCase {
		b.WriteString(`(?-i:`)
	}
	b.WriteString(`(?:(?<=` + word + `)`)
	if negated {
		b.WriteString(`(?=` + word + `)|(?<!` + word + `)(?!` + word + `))`)
	} else {
		b.WriteString(`(?!` + word + `)|(?<!` + word + `)(?=` + word + `))`)
	}
	if self.ignoreCase {
		b.WriteByte(')')
	}
	return b.String()
}

// nonUnicodeClassAtom returns the set a class atom stands for without the u
// flag (with Annex B's escapes), and whether it is a class escape.
func nonUnicodeClassAtom(atom string, ignoreCase bool) (set runeSet, isClass bool) {
	single := func(r rune) (runeSet, bool) { return runeSet{r, r}, false }
	if atom[0] != '\\' || len(atom) < 2 {
		r, _ := utf8.DecodeRuneInString(atom)
		return single(r)
	}
	switch c := atom[1]; c {
	case 'd', 'D', 's', 'S', 'w', 'W':
		var s runeSet
		switch c {
		case 'd', 'D':
			s = runeSet{'0', '9'}
		case 's', 'S':
			for _, r := range WhitespaceChars {
				s = s.union(runeSet{r, r})
			}
		default:
			s = runeSet{'0', '9', 'A', 'Z', '_', '_', 'a', 'z'}
			if ignoreCase {
				s = s.union(runeSet{0x17f, 0x17f, 0x212a, 0x212a})
			}
		}
		if c >= 'A' && c <= 'Z' {
			s = s.complement()
		}
		return s, true
	case 'b':
		return single('\b')
	case 'f':
		return single('\f')
	case 'n':
		return single('\n')
	case 'r':
		return single('\r')
	case 't':
		return single('\t')
	case 'v':
		return single('\v')
	case 'c':
		if len(atom) > 2 {
			return single(rune(atom[2] % 32))
		}
		return single('\\')
	case 'x', 'u':
		n := 2
		if c == 'u' {
			n = 4
		}
		if len(atom) == 2+n {
			if v, err := strconv.ParseUint(atom[2:], 16, 32); err == nil {
				return single(rune(v))
			}
		}
		return single(rune(c))
	}
	if atom[1] >= '0' && atom[1] <= '7' {
		v, _ := strconv.ParseUint(atom[1:], 8, 32)
		return single(rune(v))
	}
	r, _ := utf8.DecodeRuneInString(atom[1:])
	return single(r)
}
