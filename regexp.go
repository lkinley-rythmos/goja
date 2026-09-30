package goja

import (
	"fmt"
	"io"
	"regexp"
	"regexp/syntax"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"
	"weak"

	"github.com/dlclark/regexp2/v2"
	"github.com/dop251/goja/parser"
	"github.com/dop251/goja/unistring"
)

type regexp2MatchCache struct {
	target String
	runes  []rune
	posMap []int
}

// Not goroutine-safe. Use regexp2Wrapper.clone()
type regexp2Wrapper struct {
	rx    *regexp2.Regexp
	cache *regexp2MatchCache
	// iter is the pattern's loops, whose iterations regexp2 takes and
	// ECMAScript refuses are dropped from its matches (see
	// dropEmptyIterations); nil when it has none.
	iter *parser.Iterations
}

type regexpWrapper regexp.Regexp

type positionMapItem struct {
	src, dst int
}
type positionMap []positionMapItem

func (m positionMap) get(src int) int {
	if src <= 0 {
		return src
	}
	res := sort.Search(len(m), func(n int) bool { return m[n].src >= src })
	if res >= len(m) || m[res].src != src {
		panic("index not found")
	}
	return m[res].dst
}

type arrayRuneReader struct {
	runes []rune
	pos   int
}

func (rd *arrayRuneReader) ReadRune() (r rune, size int, err error) {
	if rd.pos < len(rd.runes) {
		r = rd.runes[rd.pos]
		size = 1
		rd.pos++
	} else {
		err = io.EOF
	}
	return
}

// Not goroutine-safe. Use regexpPattern.clone().
//
// A *regexpPattern may also be a process-global template held by the
// compileRegexp cache (builtin_regexp.go) and shared by every Runtime. The
// only method that may be called on a template is clone(); in particular
// createRegexp2 and the find* methods mutate the receiver and must never
// run on one.
type regexpPattern struct {
	src string

	global, ignoreCase, multiline, dotAll, sticky, unicode bool
	hasIndices                                             bool

	regexpWrapper  *regexpWrapper
	regexp2Wrapper *regexp2Wrapper

	// goStartContext is set when the Go regexp has an assertion that looks
	// before the position it matches at (^, \A, \b or \B), so that it cannot
	// be run from the middle of the input.
	goStartContext bool

	// goEmptyAfterMatch is set when the Go regexp can match both the empty
	// string and a non-empty one, so that a global match may have an empty
	// match right after a non-empty one, which ECMAScript keeps and Go's
	// FindAll drops.
	goEmptyAfterMatch bool

	// goFoldBoundary is set when the Go regexp has \b or \B, with the u and
	// i flags, under which ECMAScript takes U+017F and U+212A for word
	// characters (their case variants are), which Go's regexp does not: a
	// match on an input with them is made with regexp2 (see
	// hasFoldWordChars).
	goFoldBoundary bool
	foldScan       foldScan

	// prefixSkip is the Go regexp anchored at its start, for a large input,
	// which is searched for the pattern's literal prefix and tried at each
	// occurrence (see findStringSubmatchIndex); nil when the pattern has no
	// prefix or has a ^.
	prefixSkip *anchoredRegexp

	// iter is the hidden groups of the engines' patterns, which tell the
	// captures of earlier iterations of quantified groups (see
	// dropEarlierIterations); nil when there are none.
	iter      *parser.Iterations
	iterNames []string // the groups' names without the hidden ones

	// size is the estimated memory the compiled engines hold, charged to the
	// Runtime that compiled the pattern and counted by the cache (with
	// prefixSkip's).
	size int64
}

// anchoredRegexp is a Go regexp anchored at its start, compiled on first use
// (once, shared by the clones of a pattern) from the pattern's source, and
// charged then to the Runtime that compiles it. Its size is the same
// estimate as the pattern's program.
type anchoredRegexp struct {
	mu       sync.Mutex
	compiled atomic.Bool
	src      string
	size     int64
	re       *regexp.Regexp
}

// get returns the regexp, or nil if it cannot be compiled. A Runtime whose
// allocation limit refuses the charge (which panics) leaves it uncompiled,
// for the next Runtime to compile.
func (a *anchoredRegexp) get(vm *vm) *regexp.Regexp {
	if a.compiled.Load() {
		return a.re
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.compiled.Load() {
		vm.r.countAlloc(a.size)
		a.re, _ = regexp.Compile(`\A(?:` + a.src + `)`)
		a.compiled.Store(true)
	}
	return a.re
}

type regexpResult struct {
	indexes []int
	groups  []string
}

var allocSizeRegexpResult = int64(unsafe.Sizeof(regexpResult{}))

// appendResult appends a match to a list and charges the list's growth.
func appendResult(vm *vm, results []regexpResult, result regexpResult) []regexpResult {
	c := cap(results)
	results = append(results, result)
	if c1 := cap(results); c1 != c {
		vm.r.countAlloc(int64(c1) * allocSizeRegexpResult)
	}
	return results
}

// chargeResults charges the matches Go's regexp found all at once. Unlike
// regexp2's, which are charged one by one, they can only be charged after
// they have been allocated.
func chargeResults(vm *vm, results []regexpResult) []regexpResult {
	n := int64(cap(results)) * allocSizeRegexpResult
	for i := range results {
		n += results[i].allocSize() - allocSizeRegexpResult
	}
	vm.r.countAlloc(n)
	return results
}

// allocSize estimates the memory a match result holds, for the allocation
// counter: a global match or split can collect one per character of input.
func (result *regexpResult) allocSize() int64 {
	return allocSizeRegexpResult + int64(cap(result.indexes))*int64(unsafe.Sizeof(0)) +
		int64(cap(result.groups))*int64(unsafe.Sizeof(""))
}

func (result *regexpResult) appendIndexesAndGroup(index1, index2 int, name string) {
	result.indexes = append(result.indexes, index1, index2)
	result.groups = append(result.groups, name)
}

func submatchesToRegexpResults(submatches [][]int, groups []string) []regexpResult {
	results := make([]regexpResult, 0, len(submatches))
	for _, val := range submatches {
		results = append(results, regexpResult{indexes: val, groups: groups})
	}
	return results
}

// maxRegexpNesting bounds how deeply groups may nest in a pattern compiled
// with regexp2. Its parser and compiler recurse per level (a pattern nested
// 1e6 deep overflows the Go stack, which aborts the process) and are
// quadratic in the depth of nested lookarounds. Go's regexp/syntax, which
// compiles the patterns it supports, refuses nesting past the same depth.
const maxRegexpNesting = 1000

// regexpNestingTooDeep reports whether src nests groups deeper than
// maxRegexpNesting, counting unescaped parentheses outside character classes.
func regexpNestingTooDeep(src string) bool {
	depth := 0
	inClass := false
	for i := 0; i < len(src); i++ {
		switch src[i] {
		case '\\':
			i++
		case '[':
			inClass = true
		case ']':
			inClass = false
		case '(':
			if !inClass {
				depth++
				if depth > maxRegexpNesting {
					return true
				}
			}
		case ')':
			if !inClass && depth > 0 {
				depth--
			}
		}
	}
	return false
}

func compileRegexp2(src string, multiline, dotAll, unicode bool) (*regexp2Wrapper, error) {
	if regexpNestingTooDeep(src) {
		return nil, fmt.Errorf("Invalid regular expression (regexp2): %s (expression nests too deeply)", src)
	}
	var opts = regexp2.ECMAScript
	if multiline {
		opts |= regexp2.Multiline
	}
	if dotAll {
		opts |= regexp2.Singleline
	}
	if unicode {
		opts |= regexp2.Unicode
	}
	regexp2Pattern, err1 := regexp2.Compile(src, opts)
	if err1 != nil {
		return nil, fmt.Errorf("Invalid regular expression (regexp2): %s (%v)", src, err1)
	}
	regexp2Pattern.MatchTimeout = regexp2MatchSlice

	return &regexp2Wrapper{rx: regexp2Pattern}, nil
}

// maxRegexp2Units bounds regexp2's compile time for a pattern whose cost
// the parser has not bounded (see regexp2Estimate), in units of 100 ns:
// about 100 ms, as the parser's bound (parser.TransformRegExp2).
const maxRegexp2Units = 1 << 20

// regexp2Estimate estimates, as upper bounds, the memory regexp2 holds for a
// pattern, from its text, and its compile time in units of about 100 ns.
// Measured on a 64-bit platform (a 32-bit one holds about half as much,
// but for runs of literal runes): a literal character in a run holds about
// 5 bytes, a node (an escape, an operator, the first literal of a run) 30 to
// 60, a lookaround 64, a capturing group 125; a class is stored once
// however often it is used, in under 2 bytes of its text. Compiling takes
// under 0.5 µs a byte, and a class's ranges more (see
// parser.regexp2TextUnits). regexp2 is given every pattern case-sensitively
// (see parser.TransformRegExp2).
func regexp2Estimate(src string) (size int64, units int) {
	const (
		word      = int64(unsafe.Sizeof(uintptr(0)))
		base      = 512 * word
		node      = 5 * word / 2 // an operator, the first literal of a run
		escape    = 5 * word     // an escape
		capture   = 25 * word    // a capturing group
		group     = 5 * word / 2 // a non-capturing group
		around    = 8 * word     // a lookaround
		classUse  = 5 * word     // a class where it is used
		classByte = 2            // a byte of each distinct class's text
		classNode = 24 * word    // each distinct class
	)
	const (
		run       = int64(10) // a further literal in a run
		unitsByte = 5         // compile time, a byte outside classes
	)
	size = base
	var classes map[string]bool
	inRun := false
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == '\\':
			size += escape
			units += 2 * unitsByte
			inRun = false
			i += 2
			if i < len(src) && src[i] == '{' && strings.IndexByte("pPu", src[i-1]) >= 0 {
				if j := strings.IndexByte(src[i:], '}'); j >= 0 {
					units += (j + 1) * unitsByte
					i += j + 1
				}
			}
			continue
		case c == '[':
			// The class's items: an escape or a character, a range
			// joining two.
			j, items := i+1, 0
			for j < len(src) && src[j] != ']' {
				if src[j] == '\\' {
					j++
					if j+1 < len(src) && src[j+1] == '{' {
						if k := strings.IndexByte(src[j:], '}'); k >= 0 {
							j += k
						}
					}
				} else if src[j] == '-' {
					items--
				}
				items++
				j++
			}
			cls := src[i:min(j+1, len(src))]
			size += classUse
			if classes == nil {
				classes = make(map[string]bool)
			}
			if !classes[cls] {
				classes[cls] = true
				size += classNode + classByte*int64(len(cls))
			}
			n := max(items, 0)
			units += 10 + 5*n + n*n/30
			inRun = false
			i = j + 1
			continue
		case c == '(':
			// The group, and its syntax up to its contents.
			k := 1
			switch rest := src[i:]; {
			case strings.HasPrefix(rest, "(?=") || strings.HasPrefix(rest, "(?!"):
				size, k = size+around, 3
			case strings.HasPrefix(rest, "(?<=") || strings.HasPrefix(rest, "(?<!"):
				size, k = size+around, 4
			case strings.HasPrefix(rest, "(?<"):
				size, k = size+capture, max(strings.IndexByte(rest, '>')+1, 1)
			case strings.HasPrefix(rest, "(?"):
				size, k = size+group, max(strings.IndexByte(rest, ':')+1, 1)
			default:
				size += capture
			}
			units += k * unitsByte
			inRun = false
			i += k
			continue
		case strings.IndexByte("|)*+?{}.^$", c) >= 0:
			size += node
			inRun = false
		default:
			if inRun {
				size += run
			} else {
				size += node
			}
			inRun = true
			_, w := utf8.DecodeRuneInString(src[i:])
			units += w * unitsByte
			i += w
			continue
		}
		units += unitsByte
		i++
	}
	return
}

// createRegexp2 compiles regexp2 for a match Go's regexp cannot make, for
// a pattern that has Go's regexp. The parser bounds what regexp2's compile
// costs for every pattern (see parser.TransformRegExp2), so it takes as
// long as it would have when the pattern was made; the memory it holds is
// charged to vm, as it is then (see compileRegexpUncached).
func (p *regexpPattern) createRegexp2(vm *vm) {
	if p.regexp2Wrapper != nil {
		return
	}
	size, _ := regexp2Estimate(p.src)
	vm.r.countAlloc(size)
	rx, err := compileRegexp2(p.src, p.multiline, p.dotAll, p.unicode)
	if err != nil {
		// At this point the regexp should have been successfully converted to re2, if it fails now, it's a bug.
		panic(err)
	}
	rx.iter = p.iter
	p.regexp2Wrapper = rx
}

func buildUTF8PosMap(s unicodeString) (positionMap, string) {
	pm := make(positionMap, 0, s.Length())
	rd := s.Reader()
	sPos, utf8Pos := 0, 0
	var sb strings.Builder
	for {
		r, size, err := rd.ReadRune()
		if err == io.EOF {
			break
		}
		if err != nil {
			// the string contains invalid UTF-16, bailing out
			return nil, ""
		}
		utf8Size, _ := sb.WriteRune(r)
		sPos += size
		utf8Pos += utf8Size
		pm = append(pm, positionMapItem{src: utf8Pos, dst: sPos})
	}
	return pm, sb.String()
}

// foldScan is the last input hasFoldWordChars scanned, and what it found.
// The input is held by a weak pointer: a pattern (a pooled Runtime's, or a
// literal's) would otherwise keep its last input, possibly megabytes, alive.
type foldScan struct {
	start weak.Pointer[uint16]
	n     int
	has   bool
}

// hasFoldWordChars reports whether a pattern with goFoldBoundary is to be
// matched on s with regexp2: when s has U+017F or U+212A. The last input
// scanned is remembered, for the matches of a loop over one input.
func (p *regexpPattern) hasFoldWordChars(s String) bool {
	if !p.goFoldBoundary {
		return false
	}
	_, u := devirtualizeString(s)
	if len(u) == 0 {
		return false
	}
	// A weak pointer's value is nil once its input is reclaimed, before
	// another can take its place.
	if p.foldScan.start.Value() == &u[0] && p.foldScan.n == len(u) {
		return p.foldScan.has
	}
	has := false
	for _, c := range u[1:] {
		if c == 0x17f || c == 0x212a {
			has = true
			break
		}
	}
	p.foldScan = foldScan{start: weak.Make(&u[0]), n: len(u), has: has}
	return has
}

func (p *regexpPattern) findSubmatchIndex(vm *vm, s String, start int) regexpResult {
	res, goRegexp := p.findSubmatchIndexEngines(vm, s, start)
	if p.iter != nil && p.iter.Drop {
		res = p.dropEarlierIterations(res, goRegexp)
	}
	return res
}

// findSubmatchIndexEngines finds a match with Go's regexp or regexp2, and
// reports whether it is Go's.
func (p *regexpPattern) findSubmatchIndexEngines(vm *vm, s String, start int) (regexpResult, bool) {
	if p.regexpWrapper == nil {
		return p.regexp2Wrapper.findSubmatchIndex(vm, s, start, p.unicode, p.global || p.sticky), false
	}
	if start != 0 || p.hasFoldWordChars(s) {
		// Unfortunately Go's regexp library does not allow starting from an arbitrary position.
		// If we just drop the first _start_ characters of the string the assertions (^, $, \b and \B) will not
		// work correctly.
		p.createRegexp2(vm)
		return p.regexp2Wrapper.findSubmatchIndex(vm, s, start, p.unicode, p.global || p.sticky), false
	}
	return p.regexpWrapper.findSubmatchIndex(vm, s, p.unicode, p.prefixSkip), true
}

// goFindAllIncrementalAbove is the input length (in bytes) above which a
// global match with Go's regexp collects its matches one at a time, charging
// and checking Interrupt as it goes, rather than with one
// FindAllStringSubmatchIndex call. That call is not interruptible, and holds
// every match (about 80 bytes each) before any can be charged: a /./g over
// 10 MB allocated about 800 MB. Below the threshold it is at most a few MB.
const goFindAllIncrementalAbove = 64 << 10

func (p *regexpPattern) findAllSubmatchIndex(vm *vm, s String, start int, limit int, sticky bool) []regexpResult {
	results, goRegexp := p.findAllSubmatchIndexEngines(vm, s, start, limit, sticky)
	if p.iter != nil && p.iter.Drop {
		for i := range results {
			results[i] = p.dropEarlierIterations(results[i], goRegexp)
		}
	}
	return results
}

// dropEarlierIterations drops from a match the captures made in earlier
// iterations of a loop than its last, which ECMAScript resets at each (see
// parser.Iterations), and the hidden groups that tell them. regexp2's
// matches have had theirs dropped already (see dropIterations); goRegexp
// reports that it is Go's regexp's.
func (p *regexpPattern) dropEarlierIterations(res regexpResult, goRegexp bool) regexpResult {
	idx := res.indexes
	if len(idx) == 0 {
		return res
	}
	if goRegexp {
		dropIterations(p.iter, idx, skipGoBounded, nil)
	}
	if len(p.iter.Hidden) == 0 {
		return res
	}
	// The hidden groups are dropped, and their names (the same for every
	// match, and made once).
	kept := idx[:0]
	h := 0
	for i := 0; i < len(idx)/2; i++ {
		if h < len(p.iter.Hidden) && p.iter.Hidden[h] == i {
			h++
			continue
		}
		kept = append(kept, idx[2*i], idx[2*i+1])
	}
	if p.iterNames == nil && res.groups != nil {
		names := make([]string, 0, len(res.groups))
		h = 0
		for i, name := range res.groups {
			if h < len(p.iter.Hidden) && p.iter.Hidden[h] == i {
				h++
				continue
			}
			names = append(names, name)
		}
		p.iterNames = names
	}
	return regexpResult{indexes: kept, groups: p.iterNames}
}

func (p *regexpPattern) findAllSubmatchIndexEngines(vm *vm, s String, start int, limit int, sticky bool) ([]regexpResult, bool) {
	if p.regexpWrapper == nil {
		return p.regexp2Wrapper.findAllSubmatchIndex(vm, s, start, limit, sticky, p.unicode), false
	}
	// A pattern that looks before its match position cannot be resumed
	// mid-input with Go's regexp; for a large input, regexp2 (which resumes
	// with the whole input as context) collects the matches instead.
	//
	// Matches are collected one at a time (see findAllIncremental) when an
	// empty one may follow a non-empty one; with ^, \b or \B that cannot be
	// done on Go's regexp, which regexp2 collects instead.
	if start == 0 && !(p.goStartContext && limit != 1 && (s.Length() > goFindAllIncrementalAbove/3 || p.goEmptyAfterMatch)) && !p.hasFoldWordChars(s) {
		a, u := devirtualizeString(s)
		if u == nil {
			return p.regexpWrapper.findAllSubmatchIndex(vm, string(a), limit, sticky, p.prefixSkip, p.goEmptyAfterMatch), true
		}
		if limit == 1 {
			result := p.regexpWrapper.findSubmatchIndexUnicode(vm, u, p.unicode)
			// A sticky match must start at the start (the leftmost one
			// starts after it if none does).
			if result.indexes == nil || sticky && result.indexes[0] != 0 {
				return nil, true
			}
			return []regexpResult{result}, true
		}
		// Unfortunately Go's regexp library lacks FindAllReaderSubmatchIndex(), so we have to use a UTF-8 string as an
		// input.
		if p.unicode {
			// Try to convert s to UTF-8. If it does not contain any invalid UTF-16 we can do the matching in UTF-8.
			// The position map and the UTF-8 copy are as large as the
			// input several times over.
			vm.r.countAlloc(int64(u.Length()) * (int64(unsafe.Sizeof(positionMapItem{})) + 3))
			pm, str := buildUTF8PosMap(u)
			if pm != nil {
				res := p.regexpWrapper.findAllSubmatchIndex(vm, str, limit, sticky, p.prefixSkip, p.goEmptyAfterMatch)
				for _, result := range res {
					for i, idx := range result.indexes {
						result.indexes[i] = pm.get(idx)
					}
				}
				return res, true
			}
		}
	}

	p.createRegexp2(vm)
	return p.regexp2Wrapper.findAllSubmatchIndex(vm, s, start, limit, sticky, p.unicode), false
}

// clone creates a copy of the regexpPattern which can be used concurrently.
//
// clone returns a pattern that shares the compiled engines with p but has
// its own mutable match state. It must not mutate p: p may be a cache
// template read concurrently from other goroutines.
func (p *regexpPattern) clone() *regexpPattern {
	ret := &regexpPattern{
		src:        p.src,
		global:     p.global,
		ignoreCase: p.ignoreCase,
		multiline:  p.multiline,
		dotAll:     p.dotAll,
		sticky:     p.sticky,
		unicode:    p.unicode,
		hasIndices: p.hasIndices,
	}
	if p.regexpWrapper != nil {
		ret.regexpWrapper = p.regexpWrapper.clone()
	}
	if p.regexp2Wrapper != nil {
		ret.regexp2Wrapper = p.regexp2Wrapper.clone()
	}
	ret.goStartContext = p.goStartContext
	ret.goEmptyAfterMatch = p.goEmptyAfterMatch
	ret.goFoldBoundary = p.goFoldBoundary
	ret.prefixSkip = p.prefixSkip
	ret.iter = p.iter
	ret.size = p.size
	return ret
}

type regexpObject struct {
	baseObject
	pattern *regexpPattern
	source  String

	standard bool
}

// dropIterations drops from a match (idx) the captures made in earlier
// iterations of a loop than its last, which ECMAScript resets at each (see
// parser.Iterations), but those of the loops skip reports. A capture is
// checked against each loop it is in, but for those whose every iteration
// has it (without alternatives), and those it is in a lookaround within.
func dropIterations(it *parser.Iterations, idx []int, skip func(it *parser.Iterations, idx []int, li int, refused []int) bool, refused []int) {
	for _, c := range it.Captures {
		gs, ge := idx[2*c.Group], idx[2*c.Group+1]
		if gs < 0 {
			continue
		}
		d := it.Groups[c.Group]
		for li := c.Loop; li >= 0; li = it.Loops[li].Parent {
			l := &it.Loops[li]
			if l.Depth < d.Around {
				break
			}
			if !l.Reset || !l.Alternatives && l.Depth >= c.Missing || skip(it, idx, li, refused) {
				continue
			}
			// Where the last iteration starts, a capture is from it if it
			// is not empty, or if the body need not consume anything
			// before the group.
			if hs := idx[2*l.Group]; hs < 0 || gs < hs || gs == hs && ge == gs && l.Depth <= d.Consumed {
				idx[2*c.Group], idx[2*c.Group+1] = -1, 0
				break
			}
		}
	}
}

// skipGoBounded reports whether a loop's captures in a match of Go's
// regexp are left to the loops it is in: its last iteration matches
// nothing, and so does the last of it or a loop it is in with a maximum
// over its minimum, at the same place. Go's regexp takes the optional
// copies of a bounded repeat's body that match nothing, where ECMAScript
// refuses an iteration past the minimum that does and ends the loop before
// it: which of the iterations it was cannot be told, nor which of the
// captures of the loops in it were made in it.
func skipGoBounded(it *parser.Iterations, idx []int, li int, _ []int) bool {
	l := &it.Loops[li]
	hs := idx[2*l.Group]
	if hs < 0 || idx[2*l.Group+1] != hs {
		return false
	}
	for a := li; a >= 0; a = it.Loops[a].Parent {
		outer := &it.Loops[a]
		if outer.Max >= 0 && outer.Max > outer.Min && idx[2*outer.Group] == hs && idx[2*outer.Group+1] == hs {
			return true
		}
	}
	return false
}

// skipRefused reports whether a loop's captures in a match of regexp2 are
// left to the loops it is in: its last iteration matches nothing, at where
// a loop it is in had one refused (1 + its position in refused, see
// dropEmptyIterations), so that it may have been in the refused one, whose
// captures cannot all be told from those before it.
func skipRefused(it *parser.Iterations, idx []int, li int, refused []int) bool {
	if refused == nil {
		return false
	}
	l := &it.Loops[li]
	hs := idx[2*l.Group]
	if hs < 0 || idx[2*l.Group+1] != hs {
		return false
	}
	for a := l.Parent; a >= 0; a = it.Loops[a].Parent {
		if refused[a] == hs+1 {
			return true
		}
	}
	return false
}

// appendGroups appends the groups of a regexp2 match to result: the last
// capture of each, at posMap's positions (the runes' if nil), once the
// iterations ECMAScript refuses, and the captures of earlier iterations
// than a loop's last, are dropped (see dropEmptyIterations and
// dropIterations).
func (r *regexp2Wrapper) appendGroups(result *regexpResult, groups []regexp2.Group, posMap []int) {
	it := r.iter
	var kept, refused []int
	if it != nil && len(it.Loops) > 0 {
		kept, refused = dropEmptyIterations(it, groups)
	}
	for i := range groups {
		group := &groups[i]
		n := len(group.Captures)
		if kept != nil {
			n = kept[i]
		}
		if n == 0 {
			result.appendIndexesAndGroup(-1, 0, group.Name)
			continue
		}
		c := &group.Captures[n-1]
		result.appendIndexesAndGroup(c.RuneIndex, c.RuneIndex+c.RuneLength, group.Name)
	}
	idx := result.indexes
	if it != nil && it.Drop {
		// A loop whose last iteration is an empty one at where a loop it
		// is in had one refused may have had it in the refused one, whose
		// captures cannot all be told from those before it: its
		// captures are left to the loops it is in.
		dropIterations(it, idx, skipRefused, refused)
	}
	if posMap != nil {
		for i, pos := range idx {
			if pos >= 0 {
				idx[i] = posMap[pos]
			}
		}
	}
}

// dropEmptyIterations drops from a regexp2 match the iterations of its
// loops that ECMAScript refuses, and returns how many captures of each group
// are left, and where each loop had one refused (1 + its position; 0 for
// none), or nil if none was. regexp2 ends a loop with an iteration that
// matches nothing, which ECMAScript refuses past the loop's minimum
// (RepeatMatcher, ECMA-262 22.2.2.3.1), ending the loop before it: an
// iteration after the minimum's (counted over the loop's iterations that
// follow one another in the last iteration of the loop it is in) whose
// capture is empty. The empty captures made in it
// (at where it is, of a capturing group the body need not consume anything
// before, not in a lookaround in it) are dropped with it, one of each, a
// loop before those in it, but for those in a loop in it that did not run
// there (see ranRefused). The hidden groups of the loops in it are left:
// how many of their iterations it had cannot be told (see appendGroups).
func dropEmptyIterations(it *parser.Iterations, groups []regexp2.Group) (kept, refused []int) {
	// last is the last iteration of a loop, once those refused are
	// dropped.
	last := func(q int) []regexp2.Capture {
		caps := groups[it.Loops[q].Group].Captures
		if kept != nil {
			caps = caps[:kept[it.Loops[q].Group]]
		}
		return caps
	}
	for i := range it.Loops {
		l := &it.Loops[i]
		caps := last(i)
		k := len(caps)
		if k == 0 || caps[k-1].RuneLength != 0 {
			continue
		}
		// An iteration that starts before the last iteration of the loop
		// it is in is in an earlier iteration of that, and so of another
		// run of the loop.
		from := -1
		if l.Parent >= 0 {
			if p := last(l.Parent); len(p) > 0 {
				from = p[len(p)-1].RuneIndex
			}
		}
		n := 1
		for j := k - 1; j > 0 && n <= l.Min && caps[j-1].RuneIndex >= from && caps[j-1].RuneIndex+caps[j-1].RuneLength == caps[j].RuneIndex; j-- {
			n++
		}
		if n <= l.Min {
			continue
		}
		if kept == nil {
			kept = make([]int, len(groups))
			for g := range groups {
				kept[g] = len(groups[g].Captures)
			}
			refused = make([]int, len(it.Loops))
		}
		at := caps[k-1].RuneIndex
		refused[i] = at + 1
		kept[l.Group]--
		// Last to first: the loops a group is in are told by groups before
		// it, whose iterations ranRefused counts before they are dropped.
		for g := l.Last; g > l.Group; g-- {
			if kept[g] == 0 || it.Groups[g].Hidden {
				continue
			}
			c := &groups[g].Captures[kept[g]-1]
			d := it.Groups[g]
			if c.RuneIndex == at && c.RuneLength == 0 && l.Depth > d.Consumed && l.Depth >= d.Around && !refusedAround(it, refused, l, d, at) &&
				ranRefused(it, groups, kept, g, i, at) {
				kept[g]--
			}
		}
	}
	return
}

// ranRefused reports whether group g may have been captured in the
// iteration of loop refused at at: none of the loops between them (nor
// the one g tells, if it does) has fewer iterations at at than a run of it
// there has, the minimum's or one (an iteration that matches nothing ends
// a loop, past its minimum), and the capturing groups it is in, and the one
// always captured before it, were captured there.
func ranRefused(it *parser.Iterations, groups []regexp2.Group, kept []int, g, refused, at int) bool {
	ran := func(q int) bool {
		l := &it.Loops[q]
		caps := groups[l.Group].Captures[:kept[l.Group]]
		n := 0
		for k := len(caps) - 1; k >= 0 && caps[k].RuneIndex == at && caps[k].RuneLength == 0; k-- {
			n++
		}
		return n >= max(l.Min, 1)
	}
	if q := it.Groups[g].Marks; q >= 0 && !ran(q) {
		return false
	}
	// The capturing groups it is in, and one always captured before it,
	// are captured in the iteration too, at at.
	emptyAt := func(h int) bool {
		if kept[h] == 0 {
			return false
		}
		c := &groups[h].Captures[kept[h]-1]
		return c.RuneIndex == at && c.RuneLength == 0
	}
	first := it.Loops[refused].Group
	if h := it.Groups[g].Before; h > first && !emptyAt(h) {
		return false
	}
	for h := it.Groups[g].Parent; h > first; h = it.Groups[h].Parent {
		if !emptyAt(h) {
			return false
		}
	}
	for q := it.Groups[g].Loop; q >= 0 && q != refused; q = it.Loops[q].Parent {
		if !ran(q) {
			return false
		}
	}
	return true
}

// refusedAround reports whether a loop that l is in had an iteration
// refused at at, with which a group's empty capture there was dropped
// already (d is where the group is).
func refusedAround(it *parser.Iterations, refused []int, l *parser.IterationLoop, d parser.IterationGroup, at int) bool {
	for a := l.Parent; a >= 0; a = it.Loops[a].Parent {
		outer := &it.Loops[a]
		if refused[a] == at+1 && outer.Depth > d.Consumed && outer.Depth >= d.Around {
			return true
		}
	}
	return false
}

func (r *regexp2Wrapper) findSubmatchIndex(vm *vm, s String, start int, fullUnicode, doCache bool) regexpResult {
	if fullUnicode {
		return r.findSubmatchIndexUnicode(vm, s, start, doCache)
	}
	return r.findSubmatchIndexUTF16(vm, s, start, doCache)
}

func (r *regexp2Wrapper) findUTF16Cached(vm *vm, s String, start int, doCache bool) (match *regexp2.Match, runes []rune, err error) {
	cache := r.cache
	if cache != nil && cache.posMap == nil && cache.target.SameAs(s) {
		runes = cache.runes
	} else {
		vm.r.countAlloc(int64(s.Length()) * int64(unsafe.Sizeof(rune(0))))
		runes = s.utf16Runes()
		cache = nil
	}
	match, err = r.findRunesAt(vm, runes, start)
	if doCache && match != nil && err == nil {
		if cache == nil {
			if r.cache == nil {
				r.cache = new(regexp2MatchCache)
			}
			*r.cache = regexp2MatchCache{
				target: s,
				runes:  runes,
			}
		}
	} else {
		r.cache = nil
	}
	return
}

func (r *regexp2Wrapper) findSubmatchIndexUTF16(vm *vm, s String, start int, doCache bool) regexpResult {
	match, _, err := r.findUTF16Cached(vm, s, start, doCache)
	if err != nil {
		return regexpResult{}
	}

	if match == nil {
		return regexpResult{}
	}
	groups := match.Groups()

	result := regexpResult{
		indexes: make([]int, 0, len(groups)<<1),
		groups:  make([]string, 0, len(groups)),
	}
	r.appendGroups(&result, groups, nil)
	return result
}

func (r *regexp2Wrapper) findUnicodeCached(vm *vm, s String, start int, doCache bool) (match *regexp2.Match, posMap []int, err error) {
	var (
		runes       []rune
		mappedStart int
		splitPair   bool
		savedRune   rune
	)
	cache := r.cache
	if cache != nil && cache.posMap != nil && cache.target.SameAs(s) {
		runes, posMap = cache.runes, cache.posMap
		mappedStart, splitPair = posMapReverseLookup(posMap, start)
	} else {
		vm.r.countAlloc(int64(s.Length()) * int64(unsafe.Sizeof(0)+unsafe.Sizeof(rune(0))))
		posMap, runes, mappedStart, splitPair = buildPosMap(&lenientUtf16Decoder{utf16Reader: s.utf16Reader()}, s.Length(), start)
		cache = nil
	}
	if splitPair {
		// temporarily set the rune at mappedStart to the second code point of the pair
		_, second := utf16.EncodeRune(runes[mappedStart])
		savedRune, runes[mappedStart] = runes[mappedStart], second
	}
	match, err = r.findRunesAt(vm, runes, mappedStart)
	if doCache && match != nil && err == nil {
		if splitPair {
			runes[mappedStart] = savedRune
		}
		if cache == nil {
			if r.cache == nil {
				r.cache = new(regexp2MatchCache)
			}
			*r.cache = regexp2MatchCache{
				target: s,
				runes:  runes,
				posMap: posMap,
			}
		}
	} else {
		r.cache = nil
	}

	return
}

func (r *regexp2Wrapper) findSubmatchIndexUnicode(vm *vm, s String, start int, doCache bool) regexpResult {
	match, posMap, err := r.findUnicodeCached(vm, s, start, doCache)
	if match == nil || err != nil {
		return regexpResult{}
	}

	groups := match.Groups()

	result := regexpResult{
		indexes: make([]int, 0, len(groups)<<1),
		groups:  make([]string, 0, len(groups)),
	}
	r.appendGroups(&result, groups, posMap)
	return result
}

func (r *regexp2Wrapper) findAllSubmatchIndexUTF16(vm *vm, s String, start, limit int, sticky bool) []regexpResult {
	match, runes, err := r.findUTF16Cached(vm, s, start, false)
	if match == nil || err != nil {
		return nil
	}
	if limit < 0 {
		limit = len(runes) + 1
	}
	// limit is the input length for a global match; most inputs have far
	// fewer matches.
	results := make([]regexpResult, 0, lengthCapHint(int64(limit)))
	for n := 1; match != nil; n++ {
		if n%interruptCheckInterval == 0 {
			vm.checkInterrupt()
		}
		groups := match.Groups()

		result := regexpResult{
			indexes: make([]int, 0, len(groups)<<1),
			groups:  make([]string, 0, len(groups)),
		}

		r.appendGroups(&result, groups, nil)

		if sticky && len(result.indexes) > 1 {
			if result.indexes[0] != start {
				break
			}
			start = advanceStringIndex(s, result.indexes[1], false)
			if result.indexes[1] != result.indexes[0] {
				start = result.indexes[1]
			}
		}

		vm.r.countAlloc(result.allocSize() - allocSizeRegexpResult)
		results = appendResult(vm, results, result)
		limit--
		if limit <= 0 {
			break
		}
		match, err = r.findNext(vm, match)
		if err != nil {
			return nil
		}
	}
	return results
}

func buildPosMap(rd io.RuneReader, l, start int) (posMap []int, runes []rune, mappedStart int, splitPair bool) {
	posMap = make([]int, 0, l+1)
	curPos := 0
	runes = make([]rune, 0, l)
	startFound := false
	for {
		if !startFound {
			if curPos == start {
				mappedStart = len(runes)
				startFound = true
			}
			if curPos > start {
				// start position splits a surrogate pair
				mappedStart = len(runes) - 1
				splitPair = true
				startFound = true
			}
		}
		rn, size, err := rd.ReadRune()
		if err != nil {
			break
		}
		runes = append(runes, rn)
		posMap = append(posMap, curPos)
		curPos += size
	}
	posMap = append(posMap, curPos)
	return
}

func posMapReverseLookup(posMap []int, pos int) (int, bool) {
	mapped := sort.SearchInts(posMap, pos)
	if mapped < len(posMap) && posMap[mapped] != pos {
		return mapped - 1, true
	}
	return mapped, false
}

func (r *regexp2Wrapper) findAllSubmatchIndexUnicode(vm *vm, s unicodeString, start, limit int, sticky bool) []regexpResult {
	if limit < 0 {
		limit = len(s) + 1
	}
	// limit is the input length for a global match; most inputs have far
	// fewer matches.
	results := make([]regexpResult, 0, lengthCapHint(int64(limit)))
	match, posMap, err := r.findUnicodeCached(vm, s, start, false)
	if err != nil {
		return nil
	}
	for n := 1; match != nil; n++ {
		if n%interruptCheckInterval == 0 {
			vm.checkInterrupt()
		}
		groups := match.Groups()

		result := regexpResult{
			indexes: make([]int, 0, len(groups)<<1),
			groups:  make([]string, 0, len(groups)),
		}

		r.appendGroups(&result, groups, posMap)

		if sticky && len(result.indexes) > 1 {
			if result.indexes[0] != start {
				break
			}
			start = advanceStringIndex(s, result.indexes[1], true)
			if result.indexes[1] != result.indexes[0] {
				start = result.indexes[1]
			}
		}

		vm.r.countAlloc(result.allocSize() - allocSizeRegexpResult)
		results = appendResult(vm, results, result)
		// A replace without the g flag asks for one match.
		limit--
		if limit <= 0 {
			break
		}
		match, err = r.findNext(vm, match)
		if err != nil {
			return nil
		}
	}
	return results
}

func (r *regexp2Wrapper) findAllSubmatchIndex(vm *vm, s String, start, limit int, sticky, fullUnicode bool) []regexpResult {
	a, u := devirtualizeString(s)
	if u != nil {
		if fullUnicode {
			return r.findAllSubmatchIndexUnicode(vm, u, start, limit, sticky)
		}
		return r.findAllSubmatchIndexUTF16(vm, u, start, limit, sticky)
	}
	return r.findAllSubmatchIndexUTF16(vm, a, start, limit, sticky)
}

// regexp2MatchSlice is the MatchTimeout every regexp2 pattern is compiled
// with. regexp2 cannot be cancelled, and a backtracking match (such as
// /^(a+)+\1$/ on a few dozen characters) can run for hours, so a match runs
// in time slices: when one expires, the pending Runtime.Interrupt is raised
// if there is one, and otherwise the match is run again from the start with
// twice the time. The interrupt is therefore seen within about twice the time
// the match had run for when it arrived, and a match that legitimately needs
// long costs at most about twice its time. regexp2's clock ticks every 100ms,
// which adds to each slice.
const regexp2MatchSlice = 50 * time.Millisecond

// regexp2MaxSlice is the slice after which a match runs without a timeout.
const regexp2MaxSlice = time.Hour

func isRegexp2Timeout(err error) bool {
	// regexp2 reports an expired MatchTimeout only by this message.
	return err != nil && strings.HasPrefix(err.Error(), "match timeout after ")
}

// retrySlice returns a shallow copy of rx with twice its MatchTimeout. The
// copy shares everything compiled with rx; MatchTimeout is the only field a
// match reads from the Regexp it is called on that the copy changes.
func retrySlice(rx *regexp2.Regexp) *regexp2.Regexp {
	c := *rx
	if c.MatchTimeout >= regexp2MaxSlice {
		c.MatchTimeout = regexp2.DefaultMatchTimeout
	} else {
		c.MatchTimeout *= 2
	}
	return &c
}

// allocSizeRegexp2Match is what regexp2 allocates for a match beyond the
// regexpResult goja makes of it (its Match, groups and captures), measured
// at about 350 bytes for a single-group pattern.
const allocSizeRegexp2Match = 352

func (r *regexp2Wrapper) findRunesAt(vm *vm, runes []rune, start int) (*regexp2.Match, error) {
	rx := r.rx
	for {
		m, err := rx.FindRunesMatchStartingAt(runes, start)
		if !isRegexp2Timeout(err) {
			if m != nil {
				vm.r.countAlloc(allocSizeRegexp2Match)
			}
			return m, err
		}
		vm.checkInterrupt()
		rx = retrySlice(rx)
	}
}

func (r *regexp2Wrapper) findNext(vm *vm, m *regexp2.Match) (*regexp2.Match, error) {
	rx := r.rx
	for {
		next, err := rx.FindNextMatch(m)
		if !isRegexp2Timeout(err) {
			if next != nil {
				vm.r.countAlloc(allocSizeRegexp2Match)
			}
			return next, err
		}
		vm.checkInterrupt()
		rx = retrySlice(rx)
	}
}

func (r *regexp2Wrapper) clone() *regexp2Wrapper {
	return &regexp2Wrapper{
		rx:   r.rx,
		iter: r.iter,
	}
}

// findAllSubmatchIndex collects the matches of a global match. Go's
// FindAll collects the matches ECMAScript does, but for an empty match right
// after a non-empty one, which it drops: when there may be one
// (emptyAfterMatch), the matches are collected one at a time.
func (r *regexpWrapper) findAllSubmatchIndex(vm *vm, s string, limit int, sticky bool, prefixSkip *anchoredRegexp, emptyAfterMatch bool) []regexpResult {
	wrapped := (*regexp.Regexp)(r)
	if len(s) > goFindAllIncrementalAbove || emptyAfterMatch {
		return r.findAllIncremental(vm, s, limit, sticky, prefixSkip)
	}
	results := chargeResults(vm, submatchesToRegexpResults(wrapped.FindAllStringSubmatchIndex(s, limit), wrapped.SubexpNames()))
	pos := 0
	if sticky {
		for i, result := range results {
			if len(result.indexes) > 1 {
				if result.indexes[0] != pos {
					return results[:i]
				}
				pos = nextStickyPos(s, result.indexes)
			}
		}
	}
	return results
}

// nextStickyPos returns where the next match of a sticky global match must
// start, after the match m: where it ends, or the character after an empty
// one.
func nextStickyPos(s string, m []int) int {
	if m[1] != m[0] {
		return m[1]
	}
	if _, width := utf8.DecodeRuneInString(s[m[1]:]); width > 0 {
		return m[1] + width
	}
	return m[1] + 1
}

// findAllIncremental returns what findAllSubmatchIndex returns for a pattern
// without goStartContext, finding one match at a time: it is
// FindAllStringSubmatchIndex's loop (regexp.(*Regexp).allMatches), with each
// search run on the rest of the input, which for such a pattern matches
// exactly as a search from that position in the whole input does, and an
// empty match right after a non-empty one kept, as ECMAScript keeps it.
func (r *regexpWrapper) findAllIncremental(vm *vm, s string, limit int, sticky bool, prefixSkip *anchoredRegexp) []regexpResult {
	wrapped := (*regexp.Regexp)(r)
	names := wrapped.SubexpNames()
	var results []regexpResult
	end := len(s)
	stickyPos := 0
	for pos, n := 0, 1; pos <= end && limit != 0; n++ {
		if n%interruptCheckInterval == 0 {
			vm.checkInterrupt()
		}
		m := r.findStringSubmatchIndex(vm, s[pos:], prefixSkip)
		if m == nil {
			break
		}
		for i := range m {
			if m[i] >= 0 {
				m[i] += pos
			}
		}
		if m[1] == m[0] {
			// An empty match: move on by a character.
			pos = m[1]
			if _, width := utf8.DecodeRuneInString(s[pos:]); width > 0 {
				pos += width
			} else {
				pos = end + 1
			}
		} else {
			pos = m[1]
		}
		if sticky {
			if m[0] != stickyPos {
				break
			}
			stickyPos = nextStickyPos(s, m)
		}
		vm.r.countAlloc(int64(cap(m)) * int64(unsafe.Sizeof(0)))
		results = appendResult(vm, results, regexpResult{indexes: m, groups: names})
		if limit > 0 {
			limit--
		}
	}
	return results
}

// hasStartContext reports whether a Go regexp pattern has an assertion that
// looks before the position it matches at.
func hasStartContext(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpBeginLine, syntax.OpBeginText, syntax.OpWordBoundary, syntax.OpNoWordBoundary:
		return true
	}
	for _, sub := range re.Sub {
		if hasStartContext(sub) {
			return true
		}
	}
	return false
}

func (r *regexpWrapper) findSubmatchIndex(vm *vm, s String, fullUnicode bool, prefixSkip *anchoredRegexp) regexpResult {
	a, u := devirtualizeString(s)
	if u != nil {
		return r.findSubmatchIndexUnicode(vm, u, fullUnicode)
	}
	return r.findSubmatchIndexASCII(vm, string(a), prefixSkip)
}

func (r *regexpWrapper) findSubmatchIndexASCII(vm *vm, s string, prefixSkip *anchoredRegexp) regexpResult {
	wrapped := (*regexp.Regexp)(r)
	return regexpResult{indexes: r.findStringSubmatchIndex(vm, s, prefixSkip), groups: wrapped.SubexpNames()}
}

// goInterruptibleAbove is the input length (in bytes, or UTF-16 code units)
// above which a search with Go's regexp reads its input through a reader
// that checks for Runtime.Interrupt. A search is linear in its input, but
// cannot be stopped, and a large input can keep one running for many
// seconds (a URI pattern over 45 MB took 17 s). Go's regexp matches a
// reader exactly as it does a string (it is the same machine, fed the same
// characters); what it cannot do with a reader is skip ahead to the next
// occurrence of the pattern's literal prefix, which findStringSubmatchIndex
// does itself.
const goInterruptibleAbove = goFindAllIncrementalAbove

// interruptReadInterval is the number of characters a reader returns
// between checks for Runtime.Interrupt.
const interruptReadInterval = 1 << 12

// findStringSubmatchIndex is FindStringSubmatchIndex, interruptible on large
// inputs. prefixSkip is the pattern's prefixSkip.
func (r *regexpWrapper) findStringSubmatchIndex(vm *vm, s string, prefixSkip *anchoredRegexp) []int {
	wrapped := (*regexp.Regexp)(r)
	if len(s) <= goInterruptibleAbove {
		return wrapped.FindStringSubmatchIndex(s)
	}
	pos := 0
	if prefixSkip != nil {
		// Every match starts with the prefix, so none starts before its
		// first occurrence; and a pattern that does not look before its
		// position matches the input from there on as it does the whole.
		prefix, _ := wrapped.LiteralPrefix()
		if pos = indexInterruptible(vm, s, prefix); pos < 0 {
			return nil
		}
		// Go's regexp, given a string, jumps to the next occurrence of the
		// prefix whenever a match fails, which it cannot with a reader: it
		// would read every character from here on. So the match is tried at
		// the occurrences with the pattern anchored there, which stops
		// reading as soon as it fails, as long as that pays.
		if anchored := prefixSkip.get(vm); anchored != nil {
			var m []int
			var done bool
			if m, pos, done = prefixTries(vm, anchored, s, prefix, pos); done {
				return m
			}
		}
	}
	var m []int
	if len(s)-pos <= goInterruptibleAbove {
		m = wrapped.FindStringSubmatchIndex(s[pos:])
	} else {
		m = wrapped.FindReaderSubmatchIndex(&interruptibleStringReader{vm: vm, s: s[pos:]})
	}
	return shiftIndexes(m, pos)
}

// The bounds of the tries at the occurrences of the prefix (prefixTries),
// past which the reader reads the rest of the input at once, as for a
// pattern without a prefix. A try reads at most tryWindow bytes: one that
// reads further is dropped, since a pattern that reads far past a failing
// occurrence would read the input over at every occurrence. The tries read
// at most the input once over in all. And they stop when the occurrences
// are dense, tryDenseGap bytes apart on average over tryDenseAfter tries,
// where the reader is as fast.
const (
	tryWindow     = 1 << 12
	tryDenseGap   = 8
	tryDenseAfter = 64
)

// prefixTries tries the pattern, anchored, at each occurrence of its prefix
// in s from pos (an occurrence), within the bounds above. It returns the
// match and done when a try matched or none is left to try; otherwise pos is
// an occurrence at which no try has been made, for the reader to go on
// from.
func prefixTries(vm *vm, anchored *regexp.Regexp, s, prefix string, pos int) (m []int, next int, done bool) {
	start := pos
	for tries, budget := 0, len(s); budget > 0; tries++ {
		if tries == tryDenseAfter && pos-start < tries*tryDenseGap {
			break
		}
		rd := &interruptibleStringReader{vm: vm, s: s[pos:], end: tryWindow}
		m := anchored.FindReaderSubmatchIndex(rd)
		if rd.truncated {
			// The try read the whole window: its result is not the
			// pattern's on the whole input.
			break
		}
		if m != nil {
			return shiftIndexes(m, pos), pos, true
		}
		budget -= rd.pos
		i := indexInterruptible(vm, s[pos+1:], prefix)
		if i < 0 {
			return nil, len(s), true
		}
		pos += 1 + i
	}
	return nil, pos, false
}

// shiftIndexes moves the indexes of a match found in the input from pos.
func shiftIndexes(m []int, pos int) []int {
	if pos > 0 {
		for i := range m {
			if m[i] >= 0 {
				m[i] += pos
			}
		}
	}
	return m
}

// indexInterruptible is strings.Index, checking for Runtime.Interrupt
// between windows of the input.
func indexInterruptible(vm *vm, s, sub string) int {
	const window = 1 << 20
	for pos := 0; pos < len(s); pos += window {
		end := min(pos+window+len(sub)-1, len(s))
		if i := strings.Index(s[pos:end], sub); i >= 0 {
			return pos + i
		}
		vm.checkInterrupt()
	}
	return -1
}

// interruptibleStringReader reads a UTF-8 string, rune by rune, as Go's
// regexp does, and checks for Runtime.Interrupt as it goes. With end > 0 it
// reads no further than that, and reports truncated if it was asked to.
type interruptibleStringReader struct {
	vm        *vm
	s         string
	pos       int
	left      int
	end       int
	truncated bool
}

func (r *interruptibleStringReader) ReadRune() (rune, int, error) {
	if r.left--; r.left < 0 {
		r.left = interruptReadInterval
		r.vm.checkInterrupt()
	}
	if r.end > 0 && r.pos >= r.end && r.pos < len(r.s) {
		r.truncated = true
		return 0, 0, io.EOF
	}
	if r.pos >= len(r.s) {
		return 0, 0, io.EOF
	}
	if c := r.s[r.pos]; c < utf8.RuneSelf {
		r.pos++
		return rune(c), 1, nil
	}
	c, width := utf8.DecodeRuneInString(r.s[r.pos:])
	r.pos += width
	return c, width, nil
}

// interruptibleRuneReader passes on a reader's runes and checks for
// Runtime.Interrupt as it goes.
type interruptibleRuneReader struct {
	vm   *vm
	rd   io.RuneReader
	left int
}

func (r *interruptibleRuneReader) ReadRune() (rune, int, error) {
	if r.left--; r.left < 0 {
		r.left = interruptReadInterval
		r.vm.checkInterrupt()
	}
	return r.rd.ReadRune()
}

func (r *regexpWrapper) findSubmatchIndexUnicode(vm *vm, s unicodeString, fullUnicode bool) regexpResult {
	wrapped := (*regexp.Regexp)(r)
	if s.Length() > goInterruptibleAbove {
		if fullUnicode {
			// The decoder's sizes are in UTF-16 code units, so the indexes
			// need no mapping (unlike below, where each code point is one).
			return regexpResult{indexes: wrapped.FindReaderSubmatchIndex(&interruptibleRuneReader{
				vm: vm, rd: &lenientUtf16Decoder{utf16Reader: s.utf16Reader()}}), groups: wrapped.SubexpNames()}
		}
		return regexpResult{indexes: wrapped.FindReaderSubmatchIndex(&interruptibleRuneReader{vm: vm, rd: s.utf16RuneReader()}),
			groups: wrapped.SubexpNames()}
	}
	if fullUnicode {
		posMap, runes, _, _ := buildPosMap(&lenientUtf16Decoder{utf16Reader: s.utf16Reader()}, s.Length(), 0)
		result := regexpResult{indexes: wrapped.FindReaderSubmatchIndex(&arrayRuneReader{runes: runes}), groups: wrapped.SubexpNames()}
		for i, item := range result.indexes {
			if item >= 0 {
				result.indexes[i] = posMap[item]
			}
		}
		return result
	}
	return regexpResult{indexes: wrapped.FindReaderSubmatchIndex(s.utf16RuneReader()), groups: wrapped.SubexpNames()}
}

func (r *regexpWrapper) clone() *regexpWrapper {
	return r
}

func (r *Runtime) createRegexpGroupsObj(valueArray []Value, groupNames []string) *Object {
	var groups *baseObject
	for index := 1; index < len(valueArray); index++ {
		if index < len(groupNames) {
			name := groupNames[index]
			if name == "" {
				continue
			} else if groups == nil {
				groups = r.newBaseObject(nil, classObject)
			}
			groups.setOwnStr(unistring.NewFromString(name), valueArray[index], false)
		}
	}
	if groups != nil {
		return groups.val
	}
	return nil
}

func createRegexpGroupsMap(result regexpResult) (groups map[unistring.String]int) {
	if len(result.groups) > 0 {
		groups = make(map[unistring.String]int)
		for i := 1; i < len(result.groups); i++ {
			if group := result.groups[i]; group != "" {
				idx := i * 2
				if idx < len(result.indexes)-1 {
					groups[unistring.NewFromString(group)] = idx
				}
			}
		}
	}
	return
}

func (r *regexpObject) execResultToArray(target String, result regexpResult) Value {
	captureCount := len(result.indexes) >> 1
	valueArray := make([]Value, captureCount)
	matchIndex := result.indexes[0]
	valueArray[0] = target.Substring(result.indexes[0], result.indexes[1])
	lowerBound := 0
	for index := 1; index < captureCount; index++ {
		offset := index << 1
		if result.indexes[offset] >= 0 && result.indexes[offset+1] >= lowerBound {
			valueArray[index] = target.Substring(result.indexes[offset], result.indexes[offset+1])
			lowerBound = result.indexes[offset]
		} else {
			valueArray[index] = _undefined
		}
	}
	match := r.val.runtime.newArrayValues(valueArray)
	match.self.setOwnStr("input", target, false)
	match.self.setOwnStr("index", intToValue(int64(matchIndex)), false)

	groups := r.val.runtime.createRegexpGroupsObj(valueArray, result.groups)

	var groupsVal Value
	if groups != nil {
		groupsVal = groups
	} else {
		groupsVal = _undefined
	}
	match.self.defineOwnPropertyStr("groups", PropertyDescriptor{
		Value:        groupsVal,
		Writable:     FLAG_TRUE,
		Configurable: FLAG_TRUE,
		Enumerable:   FLAG_TRUE,
	}, false)
	if r.pattern.hasIndices {
		match.self.defineOwnPropertyStr("indices", PropertyDescriptor{
			Value:        r.matchIndices(valueArray, result),
			Writable:     FLAG_TRUE,
			Configurable: FLAG_TRUE,
			Enumerable:   FLAG_TRUE,
		}, false)
	}

	return match
}

// matchIndices is a match's indices with the d flag (MakeMatchIndicesIndex
// PairArray): the start and end of the match and of each capture, undefined
// for one that did not participate, and the named ones' in groups (an
// object without a prototype), or undefined without named groups.
func (r *regexpObject) matchIndices(values []Value, result regexpResult) Value {
	rt := r.val.runtime
	pairs := make([]Value, len(values))
	for i := range values {
		if values[i] == _undefined {
			pairs[i] = _undefined
			continue
		}
		pairs[i] = rt.newArrayValues([]Value{intToValue(int64(result.indexes[2*i])), intToValue(int64(result.indexes[2*i+1]))})
	}
	indices := rt.newArrayValues(pairs)
	var groups Value = _undefined
	for i := 1; i < len(values) && i < len(result.groups); i++ {
		if name := result.groups[i]; name != "" {
			if groups == _undefined {
				groups = rt.newBaseObject(nil, classObject).val
			}
			groups.(*Object).self.setOwnStr(unistring.NewFromString(name), pairs[i], false)
		}
	}
	indices.self.defineOwnPropertyStr("groups", PropertyDescriptor{
		Value:        groups,
		Writable:     FLAG_TRUE,
		Configurable: FLAG_TRUE,
		Enumerable:   FLAG_TRUE,
	}, false)
	return indices
}

func (r *regexpObject) getLastIndex() int64 {
	lastIndex := toLength(r.getStr("lastIndex", nil))
	if !r.pattern.global && !r.pattern.sticky {
		return 0
	}
	return lastIndex
}

// matchStart returns where a match on target starts: lastIndex for a global
// or sticky pattern, and with the u flag, as V8 does, the start of the
// surrogate pair lastIndex is in the middle of.
func (r *regexpObject) matchStart(target String) int64 {
	index := r.getLastIndex()
	if r.pattern.unicode && index > 0 && index < int64(target.Length()) &&
		isUTF16SecondSurrogate(target.CharAt(int(index))) && isUTF16FirstSurrogate(target.CharAt(int(index-1))) {
		index--
	}
	return index
}

func (r *regexpObject) execRegexp(target String) (match bool, result regexpResult) {
	index := r.matchStart(target)
	if index >= 0 && index <= int64(target.Length()) {
		result = r.pattern.findSubmatchIndex(r.val.runtime.vm, target, int(index))
	}
	match = len(result.indexes) > 0 && (!r.pattern.sticky || int64(result.indexes[0]) == index)

	if r.pattern.global || r.pattern.sticky {
		var newLastIndex int64
		if match {
			newLastIndex = int64(result.indexes[1])
		}
		r.setOwnStr("lastIndex", intToValue(newLastIndex), true)
	}

	return
}

func (r *regexpObject) exec(target String) Value {
	match, result := r.execRegexp(target)
	if match {
		return r.execResultToArray(target, result)
	}
	return _null
}

func (r *regexpObject) test(target String) bool {
	match, _ := r.execRegexp(target)
	return match
}

func (r *regexpObject) clone() *regexpObject {
	r1 := r.val.runtime.newRegexpObject(r.prototype)
	r1.source = r.source
	r1.pattern = r.pattern

	return r1
}

func (r *regexpObject) init() {
	r.baseObject.init()
	r.standard = true
	r._putProp("lastIndex", intToValue(0), true, false, false)
}

func (r *regexpObject) setProto(proto *Object, throw bool) bool {
	res := r.baseObject.setProto(proto, throw)
	if res {
		r.standard = false
	}
	return res
}

func (r *regexpObject) defineOwnPropertyStr(name unistring.String, desc PropertyDescriptor, throw bool) bool {
	res := r.baseObject.defineOwnPropertyStr(name, desc, throw)
	if res {
		r.standard = false
	}
	return res
}

func (r *regexpObject) defineOwnPropertySym(name *Symbol, desc PropertyDescriptor, throw bool) bool {
	res := r.baseObject.defineOwnPropertySym(name, desc, throw)
	if res && r.standard {
		switch name {
		case SymMatch, SymMatchAll, SymSearch, SymSplit, SymReplace:
			r.standard = false
		}
	}
	return res
}

func (r *regexpObject) deleteStr(name unistring.String, throw bool) bool {
	res := r.baseObject.deleteStr(name, throw)
	if res {
		r.standard = false
	}
	return res
}

func (r *regexpObject) setOwnStr(name unistring.String, value Value, throw bool) bool {
	res := r.baseObject.setOwnStr(name, value, throw)
	if res && r.standard && name == "exec" {
		r.standard = false
	}
	return res
}

func (r *regexpObject) setOwnSym(name *Symbol, value Value, throw bool) bool {
	res := r.baseObject.setOwnSym(name, value, throw)
	if res && r.standard {
		switch name {
		case SymMatch, SymMatchAll, SymSearch, SymSplit, SymReplace:
			r.standard = false
		}
	}
	return res
}
