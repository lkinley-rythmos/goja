package parser

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode"
)

// parsePattern parses a pattern, for what it costs and writes.
func parsePattern(pattern string, unicode, ignoreCase bool) *_RegExp_parser {
	p := &_RegExp_parser{str: pattern, length: len(pattern), unicode: unicode, ignoreCase: ignoreCase}
	p.parse()
	return p
}

// outcome is what the parser makes of a pattern: "go" (both engines),
// "regexp2" (regexp2 alone) or "too large".
func outcome(t *testing.T, pattern string, unicode, ignoreCase bool) string {
	t.Helper()
	_, _, err := TransformRegExp2(pattern, false, unicode, ignoreCase)
	switch err.(type) {
	case nil:
		return "go"
	case RegexpErrorIncompatible:
		return "regexp2"
	}
	if strings.HasSuffix(err.Error(), "/: Regular expression too large") {
		return "too large"
	}
	t.Fatalf("%.40s...: %.200v", pattern, err)
	return ""
}

func TestTransformRegExpCostLimits(t *testing.T) {
	// What any pattern costs the engines is bounded, as its property escapes
	// are, at about 100 ms of compile for each (see termCost): a count under
	// the limit measured for each kind of term is accepted, and one 40%
	// over it refused, or left to regexp2 when it is Go's regexp's program
	// that would be too large. Go's regexp took 1 s for 3.5 million
	// literal characters, and regexp2 0.7 s for 40 classes of 2,000 ranges.
	distinct := func(n, k int) string {
		var b strings.Builder
		for i := 0; i < n; i++ {
			b.WriteByte('[')
			for j := 0; j < k; j++ {
				fmt.Fprintf(&b, `\u{%x}-\u{%x}`, 0x100+4*j+i, 0x101+4*j+i)
			}
			b.WriteByte(']')
		}
		return b.String()
	}
	for _, tc := range []struct {
		term       string
		limit      int
		flags      string
		accepted   string // "go" if empty
		past       string
		repeatWith func(int) string
	}{
		{term: "x", limit: 520000, past: "too large"},
		{term: "é", limit: 520000, past: "too large"},
		{term: "x", flags: "i", limit: 150000, past: "too large"},
		{term: `(x)`, limit: 70000, past: "too large"},
		{term: `(?:x)`, limit: 68000, past: "too large"},
		{term: `x*`, limit: 98000, past: "too large"},
		{term: `[ab]`, limit: 78000, past: "too large"},
		{term: `[^ab]`, limit: 78000, past: "too large"},
		{term: `ab|`, limit: 50000, past: "too large"},
		{term: `(?:ab|cd)`, limit: 28000, past: "too large"},
		{term: `\d`, limit: 140000, past: "too large"},
		{term: `\s`, limit: 56000, past: "too large"},
		{term: `\s`, flags: "i", limit: 27000, past: "too large"},
		{term: `\W`, flags: "i", limit: 30000, past: "too large"},
		{term: `.`, limit: 104000, past: "too large"},
		{term: `.`, flags: "i", limit: 104000, past: "too large"},
		{term: `\b`, limit: 112000, past: "too large"},
		{term: `x{2,5}`, limit: 28000, past: "too large"},
		{term: `\u0041`, limit: 310000, past: "too large"},
		{term: `(?=x)`, limit: 87000, accepted: "regexp2", past: "too large"},
		{term: `[\u0100-\u01ff]`, flags: "i", limit: 10000, past: "too large"},
		{term: `[\u0100-\u01ff]`, flags: "iu", limit: 10000, past: "too large"},
		{term: `[A-\uffef]`, flags: "i", limit: 52000, past: "too large"},
		// Go's regexp expands repeats: a program too large for it is left
		// to regexp2, which does not.
		{term: `x{1000}`, limit: 500, past: "regexp2"},
		{term: `(?:x{1000})`, limit: 500, past: "regexp2"},
		{repeatWith: func(n int) string { return fmt.Sprintf("(?:x{1000}){%d}", n) }, limit: 500, past: "regexp2"},
		{repeatWith: func(n int) string { return "(?:" + strings.Repeat("x", n) + "){20}" }, limit: 20000, past: "regexp2"},
		// regexp2 sorts a class's ranges again for each one it adds.
		{repeatWith: func(n int) string { return distinct(n, 2000) }, flags: "u", limit: 10, past: "too large"},
		{repeatWith: func(n int) string { return distinct(n, 20) }, flags: "u", limit: 12000, past: "too large"},
	} {
		gen := tc.repeatWith
		if gen == nil {
			gen = func(n int) string { return strings.Repeat(tc.term, n) }
		}
		unicode, ignoreCase := strings.Contains(tc.flags, "u"), strings.Contains(tc.flags, "i")
		name := fmt.Sprintf("%s/%s", gen(1), tc.flags)
		accepted := tc.accepted
		if accepted == "" {
			accepted = "go"
		}
		if got := outcome(t, gen(tc.limit), unicode, ignoreCase); got != accepted {
			t.Errorf("%s x%d: %s", name, tc.limit, got)
		}
		if got := outcome(t, gen(tc.limit*7/5), unicode, ignoreCase); got != tc.past {
			t.Errorf("%s x%d: %s, want %s", name, tc.limit*7/5, got, tc.past)
		}
	}
}

func TestTransformRegExpCostAfterIncompatible(t *testing.T) {
	// Without the u flag as with it, a pattern is scanned to its end after a
	// construct Go's regexp lacks, so what the rest costs regexp2 is bounded
	// and it gets the rest written for it.
	for _, prefix := range []string{"(?=)", "(?<=a)", `(a)\1`, `\k<a>(?<a>)`, `[\S]`} {
		if got := outcome(t, prefix+strings.Repeat("(x)", 200000), false, false); got != "too large" {
			t.Errorf("%s(x)...: %s", prefix, got)
		}
		_, r2, err := TransformRegExp2(prefix+`\W.`, false, false, true)
		if _, ok := err.(RegexpErrorIncompatible); !ok || !strings.HasSuffix(r2, regexp2FoldNonWord+`(?-i:.)`) {
			t.Errorf("%s: %q, %v", prefix, r2, err)
		}
	}
}

func TestTransformRegExpRegexp2FoldEscapes(t *testing.T) {
	// Ignoring case, regexp2 adds the case variants of every code point of
	// a class escape standing for most of Unicode (\W took it 50 ms, . 25
	// ms), and of \w (13 µs), so it gets them matched case-sensitively as
	// the sets they stand for, which they are closed under.
	for _, unicode := range []bool{false, true} {
		for _, tc := range []struct{ pattern, want string }{
			{`a.b`, `a(?-i:.)b`},
			{`\w+\W*`, regexp2FoldWord + `+` + regexp2FoldNonWord + `*`},
			{`\S\D\s\d`, `(?-i:\S)(?-i:\D)\s\d`},
			{`[\W]\w`, `(?-i:[^\u0030-\u0039\u0041-\u005a\u005f\u0061-\u007a\u017f\u212a])` + regexp2FoldWord},
		} {
			_, r2, err := TransformRegExp2(tc.pattern, false, unicode, true)
			want := tc.want
			if unicode {
				want = strings.ReplaceAll(want, `\u0030-\u0039\u0041-\u005a\u005f\u0061-\u007a\u017f\u212a`, `\u{30}-\u{39}\u{41}-\u{5a}\u{5f}\u{61}-\u{7a}\u{17f}\u{212a}`)
			}
			if err != nil || r2 != want {
				t.Errorf("/%s/ (u: %v): %q, %v; want %q", tc.pattern, unicode, r2, err, want)
			}
			// Matching case-sensitively, nothing changes.
			if _, r2, _ := TransformRegExp2(tc.pattern, false, unicode, false); r2 != tc.pattern {
				t.Errorf("/%s/ (u: %v) without i: %q", tc.pattern, unicode, r2)
			}
		}
	}
}

// foldedClassCases are classes whose case variants would cost the engines
// more than their sets closed under case folding (see foldClass), and some
// that would not.
var foldedClassCases = []struct {
	class    string // the class, without the u flag
	uClass   string // with it, if different
	rewrite  bool
	nonASCII bool // the class has non-ASCII characters
}{
	{class: `[A-\uffef]`, rewrite: true},
	{class: `[^A-\uffef]`, rewrite: true},
	{class: `[\u0080-\uffff]`, rewrite: true},
	{class: `[\u0100-\u01ff]`, rewrite: true},
	{class: `[\u0400-\u04ff\u1e00-\u1eff]`, rewrite: true},
	{class: `[\u4e00-\u9fa5]`, rewrite: true},
	{class: `[\W\d]`, rewrite: true},
	{class: `[^\W\d]`, rewrite: true},
	{class: `[\w\u0100-\u0fff]`, rewrite: true},
	{class: `[\u0000-\uffff]`, uClass: `[\u0000-\u{10ffff}]`, rewrite: true},
	{class: `[a-z\u212a]`},
	{class: `[\u0100-\u013f]`},
	{class: `[^a-z]`},
	{class: `[\w-]`},
}

// inFoldOrbits reports whether simple case folding relates r to no other
// code point, or caseFoldOrbits has its orbit: Go's unicode tables since
// Unicode 15.1 (Go 1.27) have orbits none of whose members has a case
// mapping (U+0390 and U+1FD3), which Node.js 18 (Unicode 15.0) does not.
func inFoldOrbits(r rune) bool {
	if unicode.SimpleFold(r) == r {
		return true
	}
	runes, _ := caseFoldOrbits()
	i := sort.Search(len(runes), func(i int) bool { return runes[i] >= r })
	return i < len(runes) && runes[i] == r
}

func TestTransformRegExpFoldedClasses(t *testing.T) {
	// A class rewritten as its set closed under case folding stands for
	// what Go's regexp matches ignoring case, with or without the u flag.
	for _, tc := range foldedClassCases {
		for _, unicode := range []bool{false, true} {
			class := tc.class
			if unicode && tc.uClass != "" {
				class = tc.uClass
			}
			out, _, err := TransformRegExp2(class, false, unicode, true)
			if err != nil {
				t.Fatalf("%s (u: %v): %v", class, unicode, err)
			}
			if got := strings.HasPrefix(out, "(?-i:"); got != tc.rewrite {
				t.Errorf("%s (u: %v): %q", class, unicode, out)
			}
			// The class as it would be written unfolded, for Go's regexp to
			// fold.
			plain, _, err := TransformRegExp2(class, false, unicode, false)
			if err != nil {
				t.Fatal(err)
			}
			want, got := regexp.MustCompile(`^(?i:`+plain+`)$`), regexp.MustCompile(`^(?i:`+out+`)$`)
			for r := rune(0); r <= 0x1ffff; r++ {
				if r >= 0xd800 && r <= 0xdfff || !inFoldOrbits(r) {
					continue
				}
				if s := string(r); got.MatchString(s) != want.MatchString(s) {
					t.Errorf("%s (u: %v): %U: %v", class, unicode, r, got.MatchString(s))
					break
				}
			}
		}
	}
}

func TestTransformRegExpFoldedClassesCost(t *testing.T) {
	// A rewritten class costs the engines its set's text, and is rewritten
	// once however often the pattern has it: [A-\uffef] with the i flag
	// took Go's regexp 3 ms and regexp2 2.5 ms a copy.
	for _, unicode := range []bool{false, true} {
		p := parsePattern(strings.Repeat(`[A-\uffef]`, 1000), unicode, true)
		if p.err != nil || p.incompat != nil {
			t.Fatalf("u: %v: %v %v", unicode, p.err, p.incompat)
		}
		if n := len(p.foldedClasses); n != 1 {
			t.Errorf("u: %v: %d classes rewritten", unicode, n)
		}
		if c := p.goCost(); c > 100000 || p.regexp2Expansion > 100000 {
			t.Errorf("u: %v: Go's regexp %d units, regexp2 %d", unicode, c, p.regexp2Expansion)
		}
		// What adding the case variants of classes not rewritten costs is
		// counted.
		if plain, folded := parsePattern(strings.Repeat(`[\u0100-\u013f]`, 1000), unicode, false), parsePattern(strings.Repeat(`[\u0100-\u013f]`, 1000), unicode, true); folded.goCost() < plain.goCost()+50000 || folded.regexp2Expansion < plain.regexp2Expansion+150000 {
			t.Errorf("u: %v: [\\u0100-\\u013f] costs Go's regexp %d units ignoring case, %d not; regexp2 %d, %d", unicode, folded.goCost(), plain.goCost(), folded.regexp2Expansion, plain.regexp2Expansion)
		}
	}
}
