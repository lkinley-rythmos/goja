package goja

import (
	"errors"
	"fmt"
	"regexp"
	"regexp/syntax"
	"strings"
	"sync"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	"github.com/dop251/goja/parser"
)

func (r *Runtime) newRegexpObject(proto *Object) *regexpObject {
	v := &Object{runtime: r}

	o := &regexpObject{}
	o.class = classRegExp
	o.val = v
	o.extensible = true
	v.self = o
	o.prototype = proto
	o.init()
	return o
}

func (r *Runtime) newRegExpp(pattern *regexpPattern, patternStr String, proto *Object) *regexpObject {
	o := r.newRegexpObject(proto)

	o.pattern = pattern
	o.source = patternStr

	return o
}

func decodeHex(s string) (int, bool) {
	var hex int
	for i := 0; i < len(s); i++ {
		var n byte
		chr := s[i]
		switch {
		case '0' <= chr && chr <= '9':
			n = chr - '0'
		case 'a' <= chr && chr <= 'f':
			n = chr - 'a' + 10
		case 'A' <= chr && chr <= 'F':
			n = chr - 'A' + 10
		default:
			return 0, false
		}
		hex = hex*16 + int(n)
	}
	return hex, true
}

func writeHex4(b *strings.Builder, i int) {
	b.WriteByte(hex[i>>12])
	b.WriteByte(hex[(i>>8)&0xF])
	b.WriteByte(hex[(i>>4)&0xF])
	b.WriteByte(hex[i&0xF])
}

func convertRegexpToUnicode(patternStr string) string {
	return parser.ConvertRegExpToUnicode(patternStr)
}

func convertRegexpToUtf16(patternStr string) string {
	return parser.ConvertRegExpToUTF16(patternStr)
}

// convert any broken UTF-16 surrogate pairs to \uXXXX
func escapeInvalidUtf16(s String) string {
	if imported, ok := s.(*importedString); ok {
		return imported.s
	}
	if ascii, ok := s.(asciiString); ok {
		return ascii.String()
	}
	var sb strings.Builder
	rd := &lenientUtf16Decoder{utf16Reader: s.utf16Reader()}
	pos := 0
	utf8Size := 0
	var utf8Buf [utf8.UTFMax]byte
	for {
		c, size, err := rd.ReadRune()
		if err != nil {
			break
		}
		if utf16.IsSurrogate(c) {
			if sb.Len() == 0 {
				sb.Grow(utf8Size + 7)
				hrd := s.Reader()
				var c rune
				for p := 0; p < pos; {
					var size int
					var err error
					c, size, err = hrd.ReadRune()
					if err != nil {
						// will not happen
						panic(fmt.Errorf("error while reading string head %q, pos: %d: %w", s.String(), pos, err))
					}
					sb.WriteRune(c)
					p += size
				}
				if c == '\\' {
					sb.WriteRune(c)
				}
			}
			sb.WriteString(`\u`)
			writeHex4(&sb, int(c))
		} else {
			if sb.Len() > 0 {
				sb.WriteRune(c)
			} else {
				utf8Size += utf8.EncodeRune(utf8Buf[:], c)
				pos += size
			}
		}
	}
	if sb.Len() > 0 {
		return sb.String()
	}
	return s.String()
}

func (r *Runtime) compileRegexpFromValueString(patternStr String, flags string) (*regexpPattern, error) {
	return compileRegexpFor(r, escapeInvalidUtf16(patternStr), flags)
}

type regexpCacheKey struct {
	src, flags string
}

// regexpCacheMaxEntries is the default bound of the compiled-pattern cache;
// see SetRegexpCacheSize.
const regexpCacheMaxEntries = 1024

// regexpCacheMaxBytes bounds the cache by the estimated size of its
// templates as well (regexpPattern.size): a pattern with property escapes
// can hold hundreds of kilobytes, and the memory is nobody's to charge.
const regexpCacheMaxBytes = 32 << 20

// regexpCache holds immutable compiled pattern templates shared by every
// Runtime in the process. compileRegexp hands out clone()s, never the
// template itself — exactly what the compiler does for regexp literals —
// and the underlying *regexp.Regexp and *regexp2.Regexp are safe for
// concurrent use. Per-object state (lastIndex, the regexp2 match cache)
// lives on the clone or the regexpObject. When either cap is reached the
// whole map is dropped, which keeps memory at O(cap) with no LRU
// bookkeeping.
var regexpCache = struct {
	sync.Mutex
	m     map[regexpCacheKey]*regexpPattern
	max   int
	bytes int64 // the estimated size of the templates in m
}{m: make(map[regexpCacheKey]*regexpPattern), max: regexpCacheMaxEntries}

// SetRegexpCacheSize sets the maximum number of compiled regular expression
// patterns retained in the process-wide cache shared by all Runtimes.
// A value of 0 disables the cache. Any currently cached patterns are
// discarded. The default is 1024.
func SetRegexpCacheSize(n int) {
	if n < 0 {
		n = 0
	}
	regexpCache.Lock()
	regexpCache.max = n
	regexpCache.m = make(map[regexpCacheKey]*regexpPattern)
	regexpCache.bytes = 0
	regexpCache.Unlock()
}

// ResetRegexpCache discards all cached compiled regular expression patterns.
// Patterns held by existing RegExp objects are unaffected.
func ResetRegexpCache() {
	regexpCache.Lock()
	regexpCache.m = make(map[regexpCacheKey]*regexpPattern)
	regexpCache.bytes = 0
	regexpCache.Unlock()
}

func compileRegexp(patternStr, flags string) (*regexpPattern, error) {
	return compileRegexpFor(nil, patternStr, flags)
}

// compileRegexpFor is compileRegexp for a pattern a running script makes: r
// (nil for a literal, compiled with the script) is charged the memory the
// compiled pattern holds when it is not in the cache yet.
func compileRegexpFor(r *Runtime, patternStr, flags string) (*regexpPattern, error) {
	key := regexpCacheKey{src: patternStr, flags: flags}
	regexpCache.Lock()
	max := regexpCache.max
	template := regexpCache.m[key]
	regexpCache.Unlock()
	if template != nil {
		return template.clone(), nil
	}
	template, err := compileRegexpUncached(r, patternStr, flags)
	if err != nil {
		return nil, err
	}
	if max <= 0 {
		return template, nil
	}
	size := template.size
	if template.prefixSkip != nil {
		size += template.prefixSkip.size
	}
	if size > regexpCacheMaxBytes {
		// Too large to share the cache with anything.
		return template.clone(), nil
	}
	regexpCache.Lock()
	if len(regexpCache.m) >= max || regexpCache.bytes+size > regexpCacheMaxBytes {
		regexpCache.m = make(map[regexpCacheKey]*regexpPattern)
		regexpCache.bytes = 0
	}
	regexpCache.m[key] = template
	regexpCache.bytes += size
	regexpCache.Unlock()
	return template.clone(), nil
}

// goRegexpSize estimates, as an upper bound, the memory Go's regexp holds
// for a simplified parsed pattern (what regexp.Compile builds from) and its
// source, and for the copy anchored at its start (see anchoredRegexp). The
// program has an instruction per literal character, class or operator, in
// a slice that append may have left slack in, and a capture is two. An
// instruction for a class or literal points into the parsed pattern: to a
// node, which it keeps, for up to two runes, or to the node's runes, in a
// slice of the capacity parsing grew it to (case folding grows [A-\uffef]
// to over a thousand runes, and keeps two). A repeat is simplified into copies of its operand,
// which are walked as many times but kept once. The Regexp holds the source
// and its literal prefix, twice. A program of under 1,000 instructions
// anchored at its start is also compiled one-pass, which copies each
// instruction with a table and each class's runes.
func goRegexpSize(re *syntax.Regexp, src string) (prog, anchored int64) {
	const (
		inst = int64(unsafe.Sizeof(syntax.Inst{}))
		node = int64(unsafe.Sizeof(syntax.Regexp{}))
		// A rune of a class's or literal's capacity: 4 bytes, and a margin.
		runeBytes = 6
		// A one-pass instruction: the instruction, its table and slack.
		onePassInst = 100
		// A rune one-pass copies, with its share of the table.
		onePassRune = 8
	)
	seen := make(map[*syntax.Regexp]bool)
	var insts, nodes, runes, onePassRunes int64
	var walk func(re *syntax.Regexp)
	walk = func(re *syntax.Regexp) {
		kept := false
		switch re.Op {
		case syntax.OpLiteral:
			insts += int64(len(re.Rune))
			kept = true
		case syntax.OpCharClass:
			insts++
			kept = true
		case syntax.OpCapture:
			insts += 2
			nodes += int64(unsafe.Sizeof("")) // its name
		default:
			insts++
		}
		if kept && !seen[re] {
			seen[re] = true
			if cap(re.Rune) <= len(re.Rune0) {
				nodes += node
			} else {
				// The slice keeps what case folding grew it to.
				runes += int64(cap(re.Rune))
				onePassRunes += int64(len(re.Rune))
			}
		}
		for _, sub := range re.Sub {
			walk(sub)
		}
	}
	walk(re)
	first := re
	for (first.Op == syntax.OpConcat || first.Op == syntax.OpCapture) && len(first.Sub) > 0 {
		first = first.Sub[0]
	}
	var prefix int64
	if first.Op == syntax.OpLiteral {
		prefix = int64(len(string(first.Rune)))
	}
	prog = insts*inst*3/2 + nodes + runeBytes*runes + int64(len(src)) + 2*prefix + 1024
	anchored = prog
	if insts < 1000 {
		anchored += onePassInst*insts + onePassRune*onePassRunes
	}
	return
}

// hasBeginText reports whether a simplified pattern has ^ (BeginText, or
// BeginLine with the m flag) anywhere. A match tried with the pattern
// anchored at a position of the input (see findStringSubmatchIndex) would
// take that position for the beginning of the text.
func hasBeginText(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpBeginText, syntax.OpBeginLine:
		return true
	}
	for _, sub := range re.Sub {
		if hasBeginText(sub) {
			return true
		}
	}
	return false
}

// matchesEmpty reports whether a simplified pattern may match the empty
// string (its assertions taken to hold).
func matchesEmpty(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpNoMatch, syntax.OpCharClass, syntax.OpAnyCharNotNL, syntax.OpAnyChar:
		return false
	case syntax.OpLiteral:
		return len(re.Rune) == 0
	case syntax.OpCapture, syntax.OpPlus:
		return matchesEmpty(re.Sub[0])
	case syntax.OpStar, syntax.OpQuest:
		return true
	case syntax.OpRepeat:
		return re.Min == 0 || matchesEmpty(re.Sub[0])
	case syntax.OpConcat:
		for _, sub := range re.Sub {
			if !matchesEmpty(sub) {
				return false
			}
		}
		return true
	case syntax.OpAlternate:
		for _, sub := range re.Sub {
			if matchesEmpty(sub) {
				return true
			}
		}
		return false
	}
	return true // the empty match and the assertions
}

// hasWordBoundary reports whether a simplified pattern has \b or \B.
func hasWordBoundary(re *syntax.Regexp) bool {
	if re.Op == syntax.OpWordBoundary || re.Op == syntax.OpNoWordBoundary {
		return true
	}
	for _, sub := range re.Sub {
		if hasWordBoundary(sub) {
			return true
		}
	}
	return false
}

// onlyEmpty reports whether a simplified pattern matches only the empty
// string.
func onlyEmpty(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpCharClass, syntax.OpAnyCharNotNL, syntax.OpAnyChar:
		return false
	case syntax.OpLiteral:
		return len(re.Rune) == 0
	}
	for _, sub := range re.Sub {
		if !onlyEmpty(sub) {
			return false
		}
	}
	return true
}

// chargeRegexp charges r, if any, memory a pattern's compilation is about
// to hold.
func chargeRegexp(r *Runtime, n int64) {
	if r != nil {
		r.countAlloc(n)
	}
}

func compileRegexpUncached(r *Runtime, patternStr, flags string) (p *regexpPattern, err error) {
	var global, ignoreCase, multiline, dotAll, sticky, unicode bool
	var wrapper *regexpWrapper
	var wrapper2 *regexp2Wrapper
	var goStartContext, goEmptyAfterMatch, goFoldBoundary bool
	var prefixSkip *anchoredRegexp
	var size int64
	source := patternStr

	if flags != "" {
		invalidFlags := func() {
			err = fmt.Errorf("Invalid flags supplied to RegExp constructor '%s'", flags)
		}
		for _, chr := range flags {
			switch chr {
			case 'g':
				if global {
					invalidFlags()
					return
				}
				global = true
			case 'm':
				if multiline {
					invalidFlags()
					return
				}
				multiline = true
			case 's':
				if dotAll {
					invalidFlags()
					return
				}
				dotAll = true
			case 'i':
				if ignoreCase {
					invalidFlags()
					return
				}
				ignoreCase = true
			case 'y':
				if sticky {
					invalidFlags()
					return
				}
				sticky = true
			case 'u':
				if unicode {
					invalidFlags()
					return
				}
				unicode = true
			default:
				invalidFlags()
				return
			}
		}
	}

	if unicode {
		patternStr = convertRegexpToUnicode(patternStr)
	} else {
		patternStr = convertRegexpToUtf16(patternStr)
	}

	re2Str, regexp2Str, err := parser.TransformRegExp2(patternStr, dotAll, unicode, ignoreCase)
	// Each step of a compile takes up to about 100 ms, which cannot be
	// interrupted (see parser.TransformRegExp2); an Interrupt is raised
	// between them.
	if r != nil {
		r.checkInterrupt()
	}
	if err == nil {
		re2flags := ""
		if multiline {
			re2flags += "m"
		}
		if dotAll {
			re2flags += "s"
		}
		if ignoreCase && unicode {
			// Without the u flag the text is written for case already
			// (see parser.TransformRegExp2).
			re2flags += "i"
		}
		if len(re2flags) > 0 {
			re2Str = fmt.Sprintf("(?%s:%s)", re2flags, re2Str)
		}

		parsed, err1 := syntax.Parse(re2Str, syntax.Perl)
		if err1 == nil {
			// The program is charged before it is built.
			simplified := parsed.Simplify()
			var anchoredSize int64
			size, anchoredSize = goRegexpSize(simplified, re2Str)
			chargeRegexp(r, size)
			var pattern *regexp.Regexp
			pattern, err1 = regexp.Compile(re2Str)
			if err1 == nil {
				wrapper = (*regexpWrapper)(pattern)
				goStartContext = hasStartContext(parsed)
				goEmptyAfterMatch = matchesEmpty(simplified) && !onlyEmpty(simplified)
				goFoldBoundary = unicode && ignoreCase && hasWordBoundary(simplified)
				// LiteralPrefix leaves out a ^ the pattern begins with, so
				// a pattern with ^ cannot be tried at an occurrence of its
				// prefix.
				if prefix, _ := pattern.LiteralPrefix(); prefix != "" && !hasBeginText(simplified) {
					prefixSkip = &anchoredRegexp{src: re2Str, size: anchoredSize}
				}
			}
		}
		if err1 != nil {
			var syntaxError *syntax.Error
			if errors.As(err1, &syntaxError) && syntaxError.Code == syntax.ErrLarge {
				// Go's regexp expands repeats, which regexp2 does not: a
				// pattern too large for Go's regexp may be small for
				// regexp2, which then compiles it (see below), as V8 does.
				if _, units := regexp2Estimate(regexp2Str, ignoreCase && unicode, unicode); units > maxRegexp2Units {
					err = errors.New("Invalid regular expression: /" + source + "/: Regular expression too large")
					return
				}
			} else if !errors.As(err1, &syntaxError) || syntaxError.Code != syntax.ErrInvalidRepeatSize {
				err = fmt.Errorf("Invalid regular expression (re2): %s (%v)", re2Str, err1)
				return
			}
		}
	} else {
		var incompat parser.RegexpErrorIncompatible
		if !errors.As(err, &incompat) {
			err = parser.RegExpSourceError(err, patternStr, source)
			return
		}
	}

	if wrapper == nil || global || sticky {
		if r != nil {
			r.checkInterrupt()
		}
		// regexp2 is the only engine for patterns Go's regexp rejects, and
		// the engine used for any match starting at lastIndex > 0 (global or
		// sticky). Build it once here, on the template, so clones share it.
		size2, _ := regexp2Estimate(regexp2Str, ignoreCase && unicode, unicode)
		chargeRegexp(r, size2)
		size += size2
		wrapper2, err = compileRegexp2(regexp2Str, multiline, dotAll, ignoreCase && unicode, unicode)
		if err != nil {
			err = fmt.Errorf("Invalid regular expression (regexp2): %s (%v)", source, err)
			return
		}
	}

	// The template holds regexp2's pattern.
	size += int64(len(regexp2Str))
	chargeRegexp(r, int64(len(regexp2Str)))
	p = &regexpPattern{
		src:               regexp2Str,
		regexpWrapper:     wrapper,
		goStartContext:    goStartContext,
		goEmptyAfterMatch: goEmptyAfterMatch,
		goFoldBoundary:    goFoldBoundary,
		prefixSkip:        prefixSkip,
		regexp2Wrapper:    wrapper2,
		global:            global,
		ignoreCase:        ignoreCase,
		multiline:         multiline,
		dotAll:            dotAll,
		sticky:            sticky,
		unicode:           unicode,
		size:              size,
	}
	return
}

func (r *Runtime) _newRegExp(patternStr String, flags string, proto *Object) *regexpObject {
	pattern, err := r.compileRegexpFromValueString(patternStr, flags)
	if err != nil {
		panic(r.newSyntaxError(err.Error()))
	}
	return r.newRegExpp(pattern, patternStr, proto)
}

func (r *Runtime) builtin_newRegExp(args []Value, proto *Object) *Object {
	var patternVal, flagsVal Value
	if len(args) > 0 {
		patternVal = args[0]
	}
	if len(args) > 1 {
		flagsVal = args[1]
	}
	return r.newRegExp(patternVal, flagsVal, proto).val
}

func (r *Runtime) newRegExp(patternVal, flagsVal Value, proto *Object) *regexpObject {
	var pattern String
	var flags string
	if isRegexp(patternVal) { // this may have side effects so need to call it anyway
		if obj, ok := patternVal.(*Object); ok {
			if rx, ok := obj.self.(*regexpObject); ok {
				if flagsVal == nil || flagsVal == _undefined {
					return rx.clone()
				} else {
					return r._newRegExp(rx.source, toStringValue(flagsVal).String(), proto)
				}
			} else {
				pattern = toStringValue(nilSafe(obj.self.getStr("source", nil)))
				if flagsVal == nil || flagsVal == _undefined {
					flags = toStringValue(nilSafe(obj.self.getStr("flags", nil))).String()
				} else {
					flags = toStringValue(flagsVal).String()
				}
				goto exit
			}
		}
	}

	if patternVal != nil && patternVal != _undefined {
		pattern = toStringValue(patternVal)
	}
	if flagsVal != nil && flagsVal != _undefined {
		flags = toStringValue(flagsVal).String()
	}

	if pattern == nil {
		pattern = stringEmpty
	}
exit:
	return r._newRegExp(pattern, flags, proto)
}

func (r *Runtime) builtin_RegExp(call FunctionCall) Value {
	pattern := call.Argument(0)
	patternIsRegExp := isRegexp(pattern)
	flags := call.Argument(1)
	if patternIsRegExp && flags == _undefined {
		if obj, ok := call.Argument(0).(*Object); ok {
			patternConstructor := obj.self.getStr("constructor", nil)
			if patternConstructor == r.global.RegExp {
				return pattern
			}
		}
	}
	return r.newRegExp(pattern, flags, r.getRegExpPrototype()).val
}

func (r *Runtime) regexpproto_compile(call FunctionCall) Value {
	if this, ok := r.toObject(call.This).self.(*regexpObject); ok {
		var (
			pattern *regexpPattern
			source  String
			flags   string
			err     error
		)
		patternVal := call.Argument(0)
		flagsVal := call.Argument(1)
		if o, ok := patternVal.(*Object); ok {
			if p, ok := o.self.(*regexpObject); ok {
				if flagsVal != _undefined {
					panic(r.NewTypeError("Cannot supply flags when constructing one RegExp from another"))
				}
				this.pattern = p.pattern
				this.source = p.source
				goto exit
			}
		}
		if patternVal != _undefined {
			source = toStringValue(patternVal)
		} else {
			source = stringEmpty
		}
		if flagsVal != _undefined {
			flags = toStringValue(flagsVal).String()
		}
		pattern, err = r.compileRegexpFromValueString(source, flags)
		if err != nil {
			panic(r.newSyntaxError(err.Error()))
		}
		this.pattern = pattern
		this.source = source
	exit:
		this.setOwnStr("lastIndex", intToValue(0), true)
		return call.This
	}

	panic(r.NewTypeError("Method RegExp.prototype.compile called on incompatible receiver %s", r.objectproto_toString(FunctionCall{This: call.This})))
}

func (r *Runtime) regexpproto_exec(call FunctionCall) Value {
	if this, ok := r.toObject(call.This).self.(*regexpObject); ok {
		return this.exec(toStringValue(call.Argument(0)))
	} else {
		r.typeErrorResult(true, "Method RegExp.prototype.exec called on incompatible receiver %s", r.objectproto_toString(FunctionCall{This: call.This}))
		return nil
	}
}

func (r *Runtime) regexpproto_test(call FunctionCall) Value {
	if this, ok := r.toObject(call.This).self.(*regexpObject); ok {
		if this.test(toStringValue(call.Argument(0))) {
			return valueTrue
		} else {
			return valueFalse
		}
	} else {
		panic(r.NewTypeError("Method RegExp.prototype.test called on incompatible receiver %s", r.objectproto_toString(FunctionCall{This: call.This})))
	}
}

func (r *Runtime) regexpproto_toString(call FunctionCall) Value {
	obj := r.toObject(call.This)
	if this := r.checkStdRegexp(obj); this != nil {
		var sb StringBuilder
		sb.WriteRune('/')
		if !this.writeEscapedSource(&sb) {
			sb.WriteString(this.source)
		}
		sb.WriteRune('/')
		if this.pattern.global {
			sb.WriteRune('g')
		}
		if this.pattern.ignoreCase {
			sb.WriteRune('i')
		}
		if this.pattern.multiline {
			sb.WriteRune('m')
		}
		if this.pattern.dotAll {
			sb.WriteRune('s')
		}
		if this.pattern.unicode {
			sb.WriteRune('u')
		}
		if this.pattern.sticky {
			sb.WriteRune('y')
		}
		return sb.String()
	}
	pattern := toStringValue(nilSafe(obj.self.getStr("source", nil)))
	flags := toStringValue(nilSafe(obj.self.getStr("flags", nil)))
	var sb StringBuilder
	sb.WriteRune('/')
	sb.WriteString(pattern)
	sb.WriteRune('/')
	sb.WriteString(flags)
	return sb.String()
}

func (r *regexpObject) writeEscapedSource(sb *StringBuilder) bool {
	if r.source.Length() == 0 {
		sb.WriteString(asciiString("(?:)"))
		return true
	}
	pos := 0
	lastPos := 0
	rd := &lenientUtf16Decoder{utf16Reader: r.source.utf16Reader()}
L:
	for {
		c, size, err := rd.ReadRune()
		if err != nil {
			break
		}
		switch c {
		case '\\':
			pos++
			_, size, err = rd.ReadRune()
			if err != nil {
				break L
			}
		case '/', '\u000a', '\u000d', '\u2028', '\u2029':
			sb.WriteSubstring(r.source, lastPos, pos)
			sb.WriteRune('\\')
			switch c {
			case '\u000a':
				sb.WriteRune('n')
			case '\u000d':
				sb.WriteRune('r')
			default:
				sb.WriteRune('u')
				sb.WriteRune(rune(hex[c>>12]))
				sb.WriteRune(rune(hex[(c>>8)&0xF]))
				sb.WriteRune(rune(hex[(c>>4)&0xF]))
				sb.WriteRune(rune(hex[c&0xF]))
			}
			lastPos = pos + size
		}
		pos += size
	}
	if lastPos > 0 {
		sb.WriteSubstring(r.source, lastPos, r.source.Length())
		return true
	}
	return false
}

func (r *Runtime) regexpproto_getSource(call FunctionCall) Value {
	if this, ok := r.toObject(call.This).self.(*regexpObject); ok {
		var sb StringBuilder
		if this.writeEscapedSource(&sb) {
			return sb.String()
		}
		return this.source
	} else if call.This == r.global.RegExpPrototype {
		return asciiString("(?:)")
	} else {
		panic(r.NewTypeError("Method RegExp.prototype.source getter called on incompatible receiver"))
	}
}

func (r *Runtime) regexpproto_getGlobal(call FunctionCall) Value {
	if this, ok := r.toObject(call.This).self.(*regexpObject); ok {
		if this.pattern.global {
			return valueTrue
		} else {
			return valueFalse
		}
	} else if call.This == r.global.RegExpPrototype {
		return _undefined
	} else {
		panic(r.NewTypeError("Method RegExp.prototype.global getter called on incompatible receiver %s", r.objectproto_toString(FunctionCall{This: call.This})))
	}
}

func (r *Runtime) regexpproto_getMultiline(call FunctionCall) Value {
	if this, ok := r.toObject(call.This).self.(*regexpObject); ok {
		if this.pattern.multiline {
			return valueTrue
		} else {
			return valueFalse
		}
	} else if call.This == r.global.RegExpPrototype {
		return _undefined
	} else {
		panic(r.NewTypeError("Method RegExp.prototype.multiline getter called on incompatible receiver %s", r.objectproto_toString(FunctionCall{This: call.This})))
	}
}

func (r *Runtime) regexpproto_getDotAll(call FunctionCall) Value {
	if this, ok := r.toObject(call.This).self.(*regexpObject); ok {
		if this.pattern.dotAll {
			return valueTrue
		} else {
			return valueFalse
		}
	} else if call.This == r.global.RegExpPrototype {
		return _undefined
	} else {
		panic(r.NewTypeError("Method RegExp.prototype.dotAll getter called on incompatible receiver %s", r.objectproto_toString(FunctionCall{This: call.This})))
	}
}

func (r *Runtime) regexpproto_getIgnoreCase(call FunctionCall) Value {
	if this, ok := r.toObject(call.This).self.(*regexpObject); ok {
		if this.pattern.ignoreCase {
			return valueTrue
		} else {
			return valueFalse
		}
	} else if call.This == r.global.RegExpPrototype {
		return _undefined
	} else {
		panic(r.NewTypeError("Method RegExp.prototype.ignoreCase getter called on incompatible receiver %s", r.objectproto_toString(FunctionCall{This: call.This})))
	}
}

func (r *Runtime) regexpproto_getUnicode(call FunctionCall) Value {
	if this, ok := r.toObject(call.This).self.(*regexpObject); ok {
		if this.pattern.unicode {
			return valueTrue
		} else {
			return valueFalse
		}
	} else if call.This == r.global.RegExpPrototype {
		return _undefined
	} else {
		panic(r.NewTypeError("Method RegExp.prototype.unicode getter called on incompatible receiver %s", r.objectproto_toString(FunctionCall{This: call.This})))
	}
}

func (r *Runtime) regexpproto_getSticky(call FunctionCall) Value {
	if this, ok := r.toObject(call.This).self.(*regexpObject); ok {
		if this.pattern.sticky {
			return valueTrue
		} else {
			return valueFalse
		}
	} else if call.This == r.global.RegExpPrototype {
		return _undefined
	} else {
		panic(r.NewTypeError("Method RegExp.prototype.sticky getter called on incompatible receiver %s", r.objectproto_toString(FunctionCall{This: call.This})))
	}
}

func (r *Runtime) regexpproto_getFlags(call FunctionCall) Value {
	var global, ignoreCase, multiline, dotAll, sticky, unicode bool

	thisObj := r.toObject(call.This)
	size := 0
	if v := thisObj.self.getStr("global", nil); v != nil {
		global = v.ToBoolean()
		if global {
			size++
		}
	}
	if v := thisObj.self.getStr("ignoreCase", nil); v != nil {
		ignoreCase = v.ToBoolean()
		if ignoreCase {
			size++
		}
	}
	if v := thisObj.self.getStr("multiline", nil); v != nil {
		multiline = v.ToBoolean()
		if multiline {
			size++
		}
	}
	if v := thisObj.self.getStr("dotAll", nil); v != nil {
		dotAll = v.ToBoolean()
		if dotAll {
			size++
		}
	}
	if v := thisObj.self.getStr("sticky", nil); v != nil {
		sticky = v.ToBoolean()
		if sticky {
			size++
		}
	}
	if v := thisObj.self.getStr("unicode", nil); v != nil {
		unicode = v.ToBoolean()
		if unicode {
			size++
		}
	}

	var sb strings.Builder
	sb.Grow(size)
	if global {
		sb.WriteByte('g')
	}
	if ignoreCase {
		sb.WriteByte('i')
	}
	if multiline {
		sb.WriteByte('m')
	}
	if dotAll {
		sb.WriteByte('s')
	}
	if unicode {
		sb.WriteByte('u')
	}
	if sticky {
		sb.WriteByte('y')
	}

	return asciiString(sb.String())
}

func (r *Runtime) regExpExec(execFn func(FunctionCall) Value, rxObj *Object, arg Value) Value {
	res := execFn(FunctionCall{
		This:      rxObj,
		Arguments: []Value{arg},
	})

	if res != _null {
		if _, ok := res.(*Object); !ok {
			panic(r.NewTypeError("RegExp exec method returned something other than an Object or null"))
		}
	}

	return res
}

func (r *Runtime) getGlobalRegexpMatches(rxObj *Object, s String, fullUnicode bool) []Value {
	rxObj.self.setOwnStr("lastIndex", intToValue(0), true)
	execFn, ok := r.toObject(rxObj.self.getStr("exec", nil)).self.assertCallable()
	if !ok {
		panic(r.NewTypeError("exec is not a function"))
	}
	var a []Value
	for {
		res := r.regExpExec(execFn, rxObj, s)
		if res == _null {
			break
		}
		a = append(a, res)
		matchStr := toStringValue(nilSafe(r.toObject(res).self.getIdx(valueInt(0), nil)))
		if matchStr.Length() == 0 {
			thisIndex := toLength(rxObj.self.getStr("lastIndex", nil))
			rxObj.self.setOwnStr("lastIndex", valueInt(advanceStringIndex64(s, thisIndex, fullUnicode)), true)
		}
	}

	return a
}

func (r *Runtime) regexpproto_stdMatcherGeneric(rxObj *Object, s String) Value {
	rx := rxObj.self
	flags := nilSafe(rx.getStr("flags", nil)).String()
	global := strings.ContainsRune(flags, 'g')
	if global {
		a := r.getGlobalRegexpMatches(rxObj, s, strings.ContainsRune(flags, 'u'))
		if len(a) == 0 {
			return _null
		}
		ar := make([]Value, 0, len(a))
		for _, result := range a {
			obj := r.toObject(result)
			matchStr := nilSafe(obj.self.getIdx(valueInt(0), nil)).ToString()
			ar = append(ar, matchStr)
		}
		return r.newArrayValues(ar)
	}

	execFn, ok := r.toObject(rx.getStr("exec", nil)).self.assertCallable()
	if !ok {
		panic(r.NewTypeError("exec is not a function"))
	}

	return r.regExpExec(execFn, rxObj, s)
}

func (r *Runtime) checkStdRegexp(rxObj *Object) *regexpObject {
	if deoptimiseRegexp {
		return nil
	}

	rx, ok := rxObj.self.(*regexpObject)
	if !ok {
		return nil
	}

	if !rx.standard || rx.prototype == nil || rx.prototype.self != r.global.stdRegexpProto {
		return nil
	}

	return rx
}

func (r *Runtime) regexpproto_stdMatcher(call FunctionCall) Value {
	thisObj := r.toObject(call.This)
	s := toStringValue(call.Argument(0))
	rx := r.checkStdRegexp(thisObj)
	if rx == nil {
		return r.regexpproto_stdMatcherGeneric(thisObj, s)
	}
	if rx.pattern.global {
		rx.setOwnStr("lastIndex", intToValue(0), true)
		results := rx.pattern.findAllSubmatchIndex(r.vm, s, 0, -1, rx.pattern.sticky)
		if len(results) == 0 {
			return _null
		}
		a := make([]Value, 0, len(results))
		for _, result := range results {
			a = append(a, s.Substring(result.indexes[0], result.indexes[1]))
		}
		return r.newArrayValues(a)
	} else {
		return rx.exec(s)
	}
}

func (r *Runtime) regexpproto_stdSearchGeneric(rxObj *Object, arg String) Value {
	rx := rxObj.self
	previousLastIndex := nilSafe(rx.getStr("lastIndex", nil))
	zero := intToValue(0)
	if !previousLastIndex.SameAs(zero) {
		rx.setOwnStr("lastIndex", zero, true)
	}
	execFn, ok := r.toObject(rx.getStr("exec", nil)).self.assertCallable()
	if !ok {
		panic(r.NewTypeError("exec is not a function"))
	}

	result := r.regExpExec(execFn, rxObj, arg)
	currentLastIndex := nilSafe(rx.getStr("lastIndex", nil))
	if !currentLastIndex.SameAs(previousLastIndex) {
		rx.setOwnStr("lastIndex", previousLastIndex, true)
	}

	if result == _null {
		return intToValue(-1)
	}

	return r.toObject(result).self.getStr("index", nil)
}

func (r *Runtime) regexpproto_stdMatcherAll(call FunctionCall) Value {
	thisObj := r.toObject(call.This)
	s := toStringValue(call.Argument(0))
	flags := toStringValue(nilSafe(thisObj.self.getStr("flags", nil)))
	c := r.speciesConstructorObj(call.This.(*Object), r.getRegExp())
	matcher := r.toConstructor(c)([]Value{call.This, flags}, nil)
	matcher.self.setOwnStr("lastIndex", valueInt(toLength(thisObj.self.getStr("lastIndex", nil))), true)
	flagsStr := flags.String()
	global := strings.Contains(flagsStr, "g")
	fullUnicode := strings.Contains(flagsStr, "u")
	return r.createRegExpStringIterator(matcher, s, global, fullUnicode)
}

func (r *Runtime) createRegExpStringIterator(matcher *Object, s String, global, fullUnicode bool) Value {
	o := &Object{runtime: r}

	ri := &regExpStringIterObject{
		matcher:     matcher,
		s:           s,
		global:      global,
		fullUnicode: fullUnicode,
	}
	ri.class = classObject
	ri.val = o
	ri.extensible = true
	o.self = ri
	ri.prototype = r.getRegExpStringIteratorPrototype()
	ri.init()

	return o
}

type regExpStringIterObject struct {
	baseObject
	matcher                   *Object
	s                         String
	global, fullUnicode, done bool
}

// RegExpExec as defined in 21.2.5.2.1
func regExpExec(r *Object, s String) Value {
	exec := r.self.getStr("exec", nil)
	if execObject, ok := exec.(*Object); ok {
		if execFn, ok := execObject.self.assertCallable(); ok {
			return r.runtime.regExpExec(execFn, r, s)
		}
	}
	if rx, ok := r.self.(*regexpObject); ok {
		return rx.exec(s)
	}
	panic(r.runtime.NewTypeError("no RegExpMatcher internal slot"))
}

// nextValue is the body of %RegExpStringIteratorPrototype%.next without the
// result object: same regExpExec call, same done latch, same lastIndex
// advance on empty matches.
func (ri *regExpStringIterObject) nextValue() (Value, bool) {
	if ri.done {
		return _undefined, true
	}

	match := regExpExec(ri.matcher, ri.s)
	if IsNull(match) {
		ri.done = true
		return _undefined, true
	}
	if !ri.global {
		ri.done = true
		return match, false
	}

	matchStr := toStringValue(nilSafe(ri.val.runtime.toObject(match).self.getIdx(valueInt(0), nil)))
	if matchStr.Length() == 0 {
		thisIndex := toLength(ri.matcher.self.getStr("lastIndex", nil))
		ri.matcher.self.setOwnStr("lastIndex", valueInt(advanceStringIndex64(ri.s, thisIndex, ri.fullUnicode)), true)
	}
	return match, false
}

func (ri *regExpStringIterObject) next() Value {
	v, done := ri.nextValue()
	return ri.val.runtime.createIterResultObject(v, done)
}

func (r *Runtime) regexpproto_stdSearch(call FunctionCall) Value {
	thisObj := r.toObject(call.This)
	s := toStringValue(call.Argument(0))
	rx := r.checkStdRegexp(thisObj)
	if rx == nil {
		return r.regexpproto_stdSearchGeneric(thisObj, s)
	}

	previousLastIndex := rx.getStr("lastIndex", nil)
	rx.setOwnStr("lastIndex", intToValue(0), true)

	match, result := rx.execRegexp(s)
	rx.setOwnStr("lastIndex", previousLastIndex, true)

	if !match {
		return intToValue(-1)
	}
	return intToValue(int64(result.indexes[0]))
}

func (r *Runtime) regexpproto_stdSplitterGeneric(splitter *Object, s String, limit Value, unicodeMatching bool) Value {
	var a []Value
	var lim int64
	if limit == nil || limit == _undefined {
		lim = maxInt - 1
	} else {
		lim = toLength(limit)
	}
	if lim == 0 {
		return r.newArrayValues(a)
	}
	size := s.Length()
	p := 0
	execFn := toMethod(splitter.ToObject(r).self.getStr("exec", nil)) // must be non-nil

	if size == 0 {
		if r.regExpExec(execFn, splitter, s) == _null {
			a = append(a, s)
		}
		return r.newArrayValues(a)
	}

	q := p
	for q < size {
		splitter.self.setOwnStr("lastIndex", intToValue(int64(q)), true)
		z := r.regExpExec(execFn, splitter, s)
		if z == _null {
			q = advanceStringIndex(s, q, unicodeMatching)
		} else {
			z := r.toObject(z)
			e := toLength(splitter.self.getStr("lastIndex", nil))
			if e == int64(p) {
				q = advanceStringIndex(s, q, unicodeMatching)
			} else {
				a = append(a, s.Substring(p, q))
				if int64(len(a)) == lim {
					return r.newArrayValues(a)
				}
				if e > int64(size) {
					p = size
				} else {
					p = int(e)
				}
				numberOfCaptures := max(toLength(z.self.getStr("length", nil))-1, 0)
				for i := int64(1); i <= numberOfCaptures; i++ {
					a = append(a, nilSafe(z.self.getIdx(valueInt(i), nil)))
					if int64(len(a)) == lim {
						return r.newArrayValues(a)
					}
				}
				q = p
			}
		}
	}
	a = append(a, s.Substring(p, size))
	return r.newArrayValues(a)
}

func advanceStringIndex(s String, pos int, unicode bool) int {
	next := pos + 1
	if !unicode {
		return next
	}
	l := s.Length()
	if next >= l {
		return next
	}
	if !isUTF16FirstSurrogate(s.CharAt(pos)) {
		return next
	}
	if !isUTF16SecondSurrogate(s.CharAt(next)) {
		return next
	}
	return next + 1
}

func advanceStringIndex64(s String, pos int64, unicode bool) int64 {
	next := pos + 1
	if !unicode {
		return next
	}
	l := int64(s.Length())
	if next >= l {
		return next
	}
	if !isUTF16FirstSurrogate(s.CharAt(int(pos))) {
		return next
	}
	if !isUTF16SecondSurrogate(s.CharAt(int(next))) {
		return next
	}
	return next + 1
}

func (r *Runtime) regexpproto_stdSplitter(call FunctionCall) Value {
	rxObj := r.toObject(call.This)
	s := toStringValue(call.Argument(0))
	limitValue := call.Argument(1)
	var splitter *Object
	search := r.checkStdRegexp(rxObj)
	c := r.speciesConstructorObj(rxObj, r.getRegExp())
	if search == nil || c != r.global.RegExp {
		flags := toStringValue(nilSafe(rxObj.self.getStr("flags", nil)))
		flagsStr := flags.String()

		// Add 'y' flag if missing
		if !strings.Contains(flagsStr, "y") {
			flags = flags.Concat(asciiString("y"))
		}
		splitter = r.toConstructor(c)([]Value{rxObj, flags}, nil)
		search = r.checkStdRegexp(splitter)
		if search == nil {
			return r.regexpproto_stdSplitterGeneric(splitter, s, limitValue, strings.Contains(flagsStr, "u"))
		}
	}

	pattern := search.pattern // toUint32() may recompile the pattern, but we still need to use the original
	limit := -1
	if limitValue != _undefined {
		limit = int(toUint32(limitValue))
	}

	if limit == 0 {
		return r.newArrayValues(nil)
	}

	targetLength := s.Length()
	var valueArray []Value
	lastIndex := 0
	found := 0

	results := pattern.findAllSubmatchIndex(r.vm, s, 0, -1, false)
	if targetLength == 0 {
		if len(results) == 0 {
			valueArray = append(valueArray, s)
		}
		goto RETURN
	}

	for _, result := range results {
		// ECMAScript's split tries a match at each position before the end
		// of the input, and takes none that ends where the last piece ended
		// (an empty match there); the matches it takes are those of a global
		// match.
		if result.indexes[0] >= targetLength || result.indexes[1] == lastIndex {
			continue
		}
		valueArray = append(valueArray, s.Substring(lastIndex, result.indexes[0]))
		found++
		lastIndex = result.indexes[1]
		if found == limit {
			goto RETURN
		}

		captureCount := len(result.indexes) / 2
		for index := 1; index < captureCount; index++ {
			offset := index * 2
			var value Value
			if result.indexes[offset] != -1 {
				value = s.Substring(result.indexes[offset], result.indexes[offset+1])
			} else {
				value = _undefined
			}
			valueArray = append(valueArray, value)
			found++
			if found == limit {
				goto RETURN
			}
		}
	}

	if found != limit {
		valueArray = append(valueArray, s.Substring(lastIndex, targetLength))
	}

RETURN:
	return r.newArrayValues(valueArray)
}

func (r *Runtime) regexpproto_stdReplacerGeneric(rxObj *Object, s, replaceStr String, rcall func(FunctionCall) Value) Value {
	var results []Value
	flags := nilSafe(rxObj.self.getStr("flags", nil)).String()
	isGlobal := strings.ContainsRune(flags, 'g')
	isUnicode := strings.ContainsRune(flags, 'u')
	if isGlobal {
		results = r.getGlobalRegexpMatches(rxObj, s, isUnicode)
	} else {
		execFn := toMethod(rxObj.self.getStr("exec", nil)) // must be non-nil
		result := r.regExpExec(execFn, rxObj, s)
		if result != _null {
			results = append(results, result)
		}
	}
	lengthS := s.Length()
	nextSourcePosition := 0
	var resultBuf StringBuilder
	charged := 0
	for i, result := range results {
		if i%interruptCheckInterval == 0 {
			r.checkInterrupt()
		}
		r.chargeStringGrowth(&charged, resultBuf.Len())
		obj := r.toObject(result)
		nCaptures := max(toLength(obj.self.getStr("length", nil))-1, 0)
		matched := toStringValue(nilSafe(obj.self.getIdx(valueInt(0), nil)))
		matchLength := matched.Length()
		position := toIntStrict(max(min(nilSafe(obj.self.getStr("index", nil)).ToInteger(), int64(lengthS)), 0))
		var captures []Value
		if rcall != nil {
			captures = make([]Value, 0, nCaptures+3)
		} else {
			captures = make([]Value, 0, nCaptures+1)
		}
		captures = append(captures, matched)
		for n := int64(1); n <= nCaptures; n++ {
			capN := nilSafe(obj.self.getIdx(valueInt(n), nil))
			if capN != _undefined {
				capN = capN.ToString()
			}
			captures = append(captures, capN)
		}
		var replacement String
		if rcall != nil {
			captures = append(captures, intToValue(int64(position)), s)
			if namedCaptures := nilSafe(obj.self.getStr("groups", nil)); namedCaptures != _undefined {
				captures = append(captures, namedCaptures)
			}
			replacement = toStringValue(rcall(FunctionCall{
				This:      _undefined,
				Arguments: captures,
			}))
			if position >= nextSourcePosition {
				resultBuf.WriteString(s.Substring(nextSourcePosition, position))
				resultBuf.WriteString(replacement)
				nextSourcePosition = position + matchLength
			}
		} else {
			var namedCaptures *Object
			if c := nilSafe(obj.self.getStr("groups", nil)); c != _undefined {
				namedCaptures = c.ToObject(r)
			}
			if position >= nextSourcePosition {
				resultBuf.WriteString(s.Substring(nextSourcePosition, position))
				writeSubstitution(s, position, len(captures), func(idx int) String {
					capture := captures[idx]
					if capture != _undefined {
						return toStringValue(capture)
					}
					return stringEmpty
				}, func(ref String) String {
					if namedCaptures != nil {
						capture := nilSafe(namedCaptures.self.getStr(ref.string(), nil))
						if capture != _undefined {
							return toStringValue(capture)
						}
						return stringEmpty
					}
					return nil
				}, replaceStr, &resultBuf)
				nextSourcePosition = position + matchLength
			}
		}
	}
	if nextSourcePosition < lengthS {
		resultBuf.WriteString(s.Substring(nextSourcePosition, lengthS))
	}
	r.chargeStringGrowth(&charged, resultBuf.Len())
	return resultBuf.String()
}

func writeSubstitution(s String, position int, numCaptures int, getCapture func(int) String, getNamedCapture func(String) String, replaceStr String, buf *StringBuilder) {
	l := s.Length()
	rl := replaceStr.Length()
	matched := getCapture(0)
	tailPos := position + matched.Length()

	for i := 0; i < rl; i++ {
		c := replaceStr.CharAt(i)
		if c == '$' && i < rl-1 {
			ch := replaceStr.CharAt(i + 1)
			switch ch {
			case '$':
				buf.WriteRune('$')
			case '`':
				buf.WriteString(s.Substring(0, position))
			case '\'':
				if tailPos < l {
					buf.WriteString(s.Substring(tailPos, l))
				}
			case '&':
				buf.WriteString(matched)
			case '<':
				var ref String
				j := i + 2
				for ; j < rl; j++ {
					ch := replaceStr.CharAt(j)
					if ch == '>' {
						ref = replaceStr.Substring(i+2, j)
						break
					}
				}
				if ref != nil {
					capture := getNamedCapture(ref)
					if capture != nil {
						buf.WriteString(capture)
						i = j
						continue
					}
				}
				buf.WriteRune('$')
				buf.WriteRune('<')
			default:
				index := 0
				j := i + 1
				lim := min(j+2, rl)

				for ; j < lim; j++ {
					ch := replaceStr.CharAt(j)
					if ch >= '0' && ch <= '9' {
						m := index*10 + int(ch-'0')
						if m >= numCaptures {
							break
						}
						index = m
					} else {
						break
					}
				}
				if index > 0 {
					buf.WriteString(getCapture(index))
					i = j - 1
					continue
				} else {
					buf.WriteRune('$')
					buf.WriteRune(rune(ch))
				}
			}
			i++
		} else {
			buf.WriteRune(rune(c))
		}
	}
}

func (r *Runtime) regexpproto_stdReplacer(call FunctionCall) Value {
	rxObj := r.toObject(call.This)
	s := toStringValue(call.Argument(0))
	replaceStr, rcall := getReplaceValue(call.Argument(1))

	rx := r.checkStdRegexp(rxObj)
	if rx == nil {
		return r.regexpproto_stdReplacerGeneric(rxObj, s, replaceStr, rcall)
	}

	var index int64
	find := 1
	if rx.pattern.global {
		find = -1
	} else {
		index = rx.getLastIndex()
	}
	found := rx.pattern.findAllSubmatchIndex(r.vm, s, toIntStrict(index), find, rx.pattern.sticky)
	if rx.pattern.global || rx.pattern.sticky {
		var newLastIndex int64
		if !rx.pattern.global && len(found) > 0 {
			newLastIndex = int64(found[len(found)-1].indexes[1])
		}
		rx.setOwnStr("lastIndex", intToValue(newLastIndex), true)
	}
	return r.stringReplace(s, found, replaceStr, rcall)
}

func (r *Runtime) regExpStringIteratorProto_next(call FunctionCall) Value {
	thisObj := r.toObject(call.This)
	if iter, ok := thisObj.self.(*regExpStringIterObject); ok {
		return iter.next()
	}
	panic(r.NewTypeError("Method RegExp String Iterator.prototype.next called on incompatible receiver %s", r.objectproto_toString(FunctionCall{This: thisObj})))
}

func (r *Runtime) createRegExpStringIteratorPrototype(val *Object) objectImpl {
	o := newBaseObjectObj(val, r.getIteratorPrototype(), classObject)

	next := r.newNativeFunc(r.regExpStringIteratorProto_next, "next", 0)
	r.global.regExpStringIterProtoNext = next
	o._putProp("next", next, true, false, true)
	o._putSym(SymToStringTag, valueProp(asciiString(classRegExpStringIterator), false, false, true))

	return o
}

func (r *Runtime) getRegExpStringIteratorPrototype() *Object {
	var o *Object
	if o = r.global.RegExpStringIteratorPrototype; o == nil {
		o = &Object{runtime: r}
		r.global.RegExpStringIteratorPrototype = o
		o.self = r.createRegExpStringIteratorPrototype(o)
	}
	return o
}

func (r *Runtime) getRegExp() *Object {
	ret := r.global.RegExp
	if ret == nil {
		ret = &Object{runtime: r}
		r.global.RegExp = ret
		proto := r.getRegExpPrototype()
		r.newNativeFuncAndConstruct(ret, r.builtin_RegExp,
			r.wrapNativeConstruct(r.builtin_newRegExp, ret, proto), proto, "RegExp", intToValue(2))
		rx := ret.self
		r.putSpeciesReturnThis(rx)
	}
	return ret
}

func (r *Runtime) getRegExpPrototype() *Object {
	ret := r.global.RegExpPrototype
	if ret == nil {
		o := r.newGuardedObject(r.global.ObjectPrototype, classObject)
		ret = o.val
		r.global.RegExpPrototype = ret
		r.global.stdRegexpProto = o

		o._putProp("constructor", r.getRegExp(), true, false, true)
		o._putProp("compile", r.newNativeFunc(r.regexpproto_compile, "compile", 2), true, false, true)
		o._putProp("exec", r.newNativeFunc(r.regexpproto_exec, "exec", 1), true, false, true)
		o._putProp("test", r.newNativeFunc(r.regexpproto_test, "test", 1), true, false, true)
		o._putProp("toString", r.newNativeFunc(r.regexpproto_toString, "toString", 0), true, false, true)
		o.setOwnStr("source", &valueProperty{
			configurable: true,
			getterFunc:   r.newNativeFunc(r.regexpproto_getSource, "get source", 0),
			accessor:     true,
		}, false)
		o.setOwnStr("global", &valueProperty{
			configurable: true,
			getterFunc:   r.newNativeFunc(r.regexpproto_getGlobal, "get global", 0),
			accessor:     true,
		}, false)
		o.setOwnStr("multiline", &valueProperty{
			configurable: true,
			getterFunc:   r.newNativeFunc(r.regexpproto_getMultiline, "get multiline", 0),
			accessor:     true,
		}, false)
		o.setOwnStr("dotAll", &valueProperty{
			configurable: true,
			getterFunc:   r.newNativeFunc(r.regexpproto_getDotAll, "get dotAll", 0),
			accessor:     true,
		}, false)
		o.setOwnStr("ignoreCase", &valueProperty{
			configurable: true,
			getterFunc:   r.newNativeFunc(r.regexpproto_getIgnoreCase, "get ignoreCase", 0),
			accessor:     true,
		}, false)
		o.setOwnStr("unicode", &valueProperty{
			configurable: true,
			getterFunc:   r.newNativeFunc(r.regexpproto_getUnicode, "get unicode", 0),
			accessor:     true,
		}, false)
		o.setOwnStr("sticky", &valueProperty{
			configurable: true,
			getterFunc:   r.newNativeFunc(r.regexpproto_getSticky, "get sticky", 0),
			accessor:     true,
		}, false)
		o.setOwnStr("flags", &valueProperty{
			configurable: true,
			getterFunc:   r.newNativeFunc(r.regexpproto_getFlags, "get flags", 0),
			accessor:     true,
		}, false)

		o._putSym(SymMatch, valueProp(r.newNativeFunc(r.regexpproto_stdMatcher, "[Symbol.match]", 1), true, false, true))
		o._putSym(SymMatchAll, valueProp(r.newNativeFunc(r.regexpproto_stdMatcherAll, "[Symbol.matchAll]", 1), true, false, true))
		o._putSym(SymSearch, valueProp(r.newNativeFunc(r.regexpproto_stdSearch, "[Symbol.search]", 1), true, false, true))
		o._putSym(SymSplit, valueProp(r.newNativeFunc(r.regexpproto_stdSplitter, "[Symbol.split]", 2), true, false, true))
		o._putSym(SymReplace, valueProp(r.newNativeFunc(r.regexpproto_stdReplacer, "[Symbol.replace]", 2), true, false, true))
		o.guard("exec", "global", "multiline", "ignoreCase", "unicode", "sticky")
	}
	return ret
}
