package parser

import (
	"fmt"
	"math/rand"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"time"
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
		// Ignoring case a character with case variants is a class.
		{term: "x", flags: "iu", limit: 100000, past: "too large"},
		{term: "x", flags: "i", limit: 100000, past: "too large"},
		{term: `(x)`, limit: 60000, past: "too large"},
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
		{term: `\b`, limit: 14000, past: "too large"},
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
		{repeatWith: func(n int) string { return distinct(n, 20) }, flags: "u", limit: 10000, past: "too large"},
		// Distinct classes cost regexp2 more than one class repeated.
		{repeatWith: distinctRanges, limit: 40000, past: "too large"},
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

// distinctRanges returns n distinct negated classes of a range.
func distinctRanges(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		lo := 0x100 + i%40000
		fmt.Fprintf(&b, `[^\u%04x-\u%04x]`, lo, lo+1+i/40000)
	}
	return b.String()
}

func TestTransformRegExpCostAfterIncompatible(t *testing.T) {
	// Without the u flag as with it, a pattern is scanned to its end after a
	// construct Go's regexp lacks, so what the rest costs regexp2 is bounded
	// and it gets the rest written for it.
	for _, prefix := range []string{"(?=)", "(?<=a)", `(a)\1`, `\k<a>(?<a>)`, `[\S]`} {
		if got := outcome(t, prefix+strings.Repeat("(x)", 200000), false, false); got != "too large" {
			t.Errorf("%s(x)...: %s", prefix, got)
		}
		_, r2, err := TransformRegExp2(prefix+`\W.`, false, true, true)
		if prefix == `[\S]` {
			// Invalid with the u flag.
			_, r2, err = TransformRegExp2(prefix+`\W.`, false, false, false)
			if _, ok := err.(RegexpErrorIncompatible); !ok || !strings.HasSuffix(r2, `\W.`) {
				t.Errorf("%s: %q, %v", prefix, r2, err)
			}
			continue
		}
		if _, ok := err.(RegexpErrorIncompatible); !ok || !strings.HasSuffix(r2, regexp2FoldNonWord+`.`) {
			t.Errorf("%s: %q, %v", prefix, r2, err)
		}
	}
}

func TestTransformRegExpRegexp2FoldEscapes(t *testing.T) {
	// Ignoring case, regexp2 matches case-sensitively, and gets each
	// character and class as the set case folding makes it equivalent to
	// (see regexp_canon.go); \w and \W as the sets they stand for, which
	// have U+017F and U+212A with the u flag, and the other escapes as
	// they are, which they are closed under.
	for _, tc := range []struct{ pattern, want string }{
		{`a.b`, `[\u{41}\u{61}].[\u{42}\u{62}]`},
		{`\w+\W*`, regexp2FoldWord + `+` + regexp2FoldNonWord + `*`},
		{`\S\D\s\d`, `\S\D\s\d`},
		{`[\W]\w`, `[^\u{30}-\u{39}\u{41}-\u{5a}\u{5f}\u{61}-\u{7a}\u{17f}\u{212a}]` + regexp2FoldWord},
		{`(k)\1\k<a>(?<a>)`, `([\u{4b}\u{6b}\u{212a}])(?i:\1)(?i:\k<a>)(?<a>)`},
	} {
		_, r2, err := TransformRegExp2(tc.pattern, false, true, true)
		if _, incompat := err.(RegexpErrorIncompatible); err != nil && !incompat || r2 != tc.want {
			t.Errorf("/%s/iu: %q, %v; want %q", tc.pattern, r2, err, tc.want)
		}
		// Matching case-sensitively, nothing changes.
		if _, r2, _ := TransformRegExp2(tc.pattern, false, true, false); r2 != tc.pattern {
			t.Errorf("/%s/u: %q", tc.pattern, r2)
		}
	}
	// Without the u flag regexp2 matches case-sensitively, and gets each
	// character and class as the set Canonicalize makes it equivalent to
	// (see regexp_canon.go); the escapes are closed under it.
	for _, tc := range []struct{ pattern, want string }{
		{`a.b`, `[\u0041\u0061].[\u0042\u0062]`},
		{`\w+\W*`, `\w+\W*`},
		{`\S\D\s\d`, `\S\D\s\d`},
		{`[\W]\w`, `[^\u0030-\u0039\u0041-\u005a\u005f\u0061-\u007a]\w`},
		{`(k)\1`, `([\u004b\u006b])(?i:\1)`},
	} {
		if _, r2, err := TransformRegExp2(tc.pattern, false, false, true); r2 != tc.want {
			t.Errorf("/%s/i: %q, %v; want %q", tc.pattern, r2, err, tc.want)
		}
	}
}

// foldedClassCases are classes the engines get, ignoring case, as their
// sets closed under case folding (\W, which then has neither U+017F nor
// U+212A, is checked against Node.js 18 in goja's regexp_cost_test.go).
var foldedClassCases = []string{
	`[A-\uffef]`, `[^A-\uffef]`, `[\u0080-\uffff]`, `[\u0100-\u01ff]`, `[\u0400-\u04ff\u1e00-\u1eff]`,
	`[\u4e00-\u9fa5]`, `[\w\u0100-\u0fff]`, `[\u0000-\u{10ffff}]`, `[a-z\u212a]`,
	`[\u0100-\u013f]`, `[^a-z]`, `[\w-]`, `[\u1f00-\u1fff]`, `[^\u0380-\u03ff]`, `[\ufb00-\ufb06]`,
	`[\u0180-\u01ff]`, `[\u{10400}-\u{1044f}]`,
}

func TestTransformRegExpFoldedClasses(t *testing.T) {
	// With the u and i flags, a class is written for both engines as the
	// set of the characters one of whose case variants (see caseClass) it
	// has, or for a negated class, the others, matched case-sensitively.
	for _, class := range foldedClassCases {
		out, r2, err := TransformRegExp2(class, false, true, true)
		if err != nil {
			t.Fatalf("%s: %v", class, err)
		}
		plain, _, err := TransformRegExp2(strings.Replace(class, "[^", "[", 1), false, true, false)
		if err != nil {
			t.Fatal(err)
		}
		negated := strings.HasPrefix(class, "[^")
		in, got := regexp.MustCompile(`^(?:`+plain+`)$`), regexp.MustCompile(`^(?:`+out+`)$`)
		for r := rune(0); r <= unicode.MaxRune; r++ {
			if r >= 0xd800 && r <= 0xdfff || r > 0x1ffff && r < 0xe0000 {
				continue
			}
			want := false
			set := caseClass(r)
			for k := 0; k < len(set) && !want; k += 2 {
				for m := set[k]; m <= set[k+1] && !want; m++ {
					want = in.MatchString(string(m))
				}
			}
			if s := string(r); got.MatchString(s) != (want != negated) {
				t.Errorf("%s: %U: %v", class, r, got.MatchString(s))
				break
			}
		}
		if strings.Contains(r2, "(?i") {
			t.Errorf("%s: regexp2 %q", class, r2)
		}
	}
}

func TestTransformRegExpFoldedClassesCost(t *testing.T) {
	// A class ignoring case is rewritten as its closed set once however
	// often the pattern has it, and costs the engines its set's text: [A-\uffef]
	// with the i flag took Go's regexp 3 ms and regexp2 2.5 ms a copy when
	// they added its case variants themselves.
	for _, unicode := range []bool{false, true} {
		for _, class := range []string{`[A-\uffef]`, `[\u0100-\u013f]`} {
			p := parsePattern(strings.Repeat(class, 1000), unicode, true)
			if p.err != nil || p.incompat != nil {
				t.Fatalf("u: %v: %v %v", unicode, p.err, p.incompat)
			}
			if n := len(p.foldedClasses); n != 1 {
				t.Errorf("u: %v: %s: %d classes rewritten", unicode, class, n)
			}
			if c := p.goCost(); c > 100000 || p.regexp2Expansion > 100000 {
				t.Errorf("u: %v: %s: Go's regexp %d units, regexp2 %d", unicode, class, c, p.regexp2Expansion)
			}
		}
		// Distinct classes are each charged their closure.
		if p := parsePattern(distinctWideClasses(100), unicode, true); p.goCost() < 100*classFoldUnits || p.regexp2Expansion < 100*classFoldUnits {
			t.Errorf("u: %v: 100 distinct classes cost Go's regexp %d units, regexp2 %d", unicode, p.goCost(), p.regexp2Expansion)
		}
	}
}

func TestTransformRegExpNestingBounded(t *testing.T) {
	// The scan recurses per group; a million nested groups are refused at
	// the bound, before the stack grows past a few hundred kilobytes (it
	// overflowed the 250 MB a goroutine may have on 386). Node.js 18 takes
	// 2,000 (and refuses 100,000 as "Too many captures"); neither of goja's
	// engines takes over 1,000.
	defer debug.SetMaxStack(debug.SetMaxStack(16 << 20))
	for _, unicode := range []bool{false, true} {
		for _, n := range []int{maxGroupNesting + 1, 1000000} {
			pattern := strings.Repeat("(", n) + "a" + strings.Repeat(")", n)
			if got := outcome(t, pattern, unicode, false); got != "too large" {
				t.Errorf("%d groups (u: %v): %s", n, unicode, got)
			}
			if got := outcome(t, strings.Repeat("(?=", n)+"a"+strings.Repeat(")", n), unicode, false); got != "too large" {
				t.Errorf("%d lookaheads (u: %v): %s", n, unicode, got)
			}
		}
		if got := outcome(t, strings.Repeat("(", maxGroupNesting)+"a"+strings.Repeat(")", maxGroupNesting), unicode, false); got != "go" {
			t.Errorf("%d groups (u: %v): %s", maxGroupNesting, unicode, got)
		}
	}
}

func TestTransformRegExpInvalidUTF8(t *testing.T) {
	// Invalid UTF-8 ends the scan, even after an error, when the scan went
	// back (\c without a control letter is a backslash, and the c is read
	// again): "\c\xff" looped for ever. A Go string given to RunString may
	// have such bytes.
	for _, pattern := range []string{"k8a]\\c\xff", "\\c\xff", "[\\c\xff]", "a\xff", "(?<\xff>)"} {
		for _, unicode := range []bool{false, true} {
			done := make(chan error, 1)
			go func() {
				_, _, err := TransformRegExp2(pattern, false, unicode, false)
				done <- err
			}()
			select {
			case err := <-done:
				if _, ok := err.(RegexpSyntaxError); !ok {
					t.Errorf("%q (u: %v): %v", pattern, unicode, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("%q (u: %v): no result", pattern, unicode)
			}
		}
	}
}

func TestRegExpReadInvalidUTF8AfterError(t *testing.T) {
	// Reading invalid UTF-8 ends the scan even when an error was reported
	// already, so that a scan that went back cannot loop.
	p := &_RegExp_parser{str: "a\xff", length: 2}
	p.error(true, "earlier")
	p.chr = 'a' // as a scan that went back has it
	p.seek(1)
	if p.chr != -1 {
		t.Fatalf("read %q", p.chr)
	}
}

// distinctWideClasses returns n distinct classes [\u0100-\uffff<digit><CJK>]
// (the i flag rewrites each as its set closed under Canonicalize).
func distinctWideClasses(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString("[\u0100-\uffff")
		b.WriteRune(rune('0' + i%10))
		b.WriteRune(rune(0x4e00 + i))
		b.WriteByte(']')
	}
	return b.String()
}

func TestTransformRegExpDistinctCanonClassesBounded(t *testing.T) {
	// Each distinct class of a pattern that ignores case without the u flag
	// is closed under Canonicalize in one pass over the table: 3,520
	// distinct wide classes took 1.76 s and allocated 2.2 GB, a closure a
	// union per case class, where they are charged 40 µs each (a closure
	// takes about 7 µs). The pattern is refused as too large, or compiles;
	// what it allocates stands for the time, which regexp_cost_test.go in
	// goja bounds.
	for _, tc := range []struct {
		name    string
		pattern string
	}{
		{"wide", distinctWideClasses(3520)},
		{"negated", strings.Replace(distinctWideClasses(3264), "[", "[^", -1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			_, _, err := TransformRegExp2(tc.pattern, false, false, true)
			runtime.ReadMemStats(&after)
			if err != nil && !strings.HasSuffix(err.Error(), "/: Regular expression too large") {
				t.Fatalf("%.200v", err)
			}
			if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 100<<20 {
				t.Errorf("allocated %d MB", alloc>>20)
			}
		})
	}
}

func TestCanonClosure(t *testing.T) {
	// canonClosure is the union of the classes of the code units in s.
	units, _, _ := canonClasses()
	for _, s := range []runeSet{
		nil, {'a', 'a'}, {'A', 'Z'}, {0x100, 0xffff}, {0, 0x10ffff}, {'0', '0', 0x100, 0xffff},
		{0x17f, 0x17f}, {0x212a, 0x212a}, {0x3a3, 0x3a3, 0x1e9e, 0x1e9e}, {0x10000, 0x10ffff},
	} {
		want := s
		for _, u := range units {
			if s.contains(rune(u)) {
				want = want.union(canonClass(rune(u)))
			}
		}
		if got := canonClosure(s); !got.equal(want) {
			t.Errorf("%x: %x, want %x", s, got, want)
		}
	}
}

func TestValidateRegExpLiteralOnlyValidates(t *testing.T) {
	// A literal's pattern is validated when it is parsed, and transformed
	// when it is compiled: the validation neither folds case nor writes the
	// pattern, which made the worst literal cost two transforms (3.66 s
	// against 1.76 s before canonClosure was linear).
	src := "x = /" + distinctWideClasses(3000) + "/i"
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	if _, err := ParseFile(nil, "", src, 0); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	parsed := after.TotalAlloc - before.TotalAlloc
	runtime.GC()
	runtime.ReadMemStats(&before)
	if _, _, err := TransformRegExp2(distinctWideClasses(3000), false, false, true); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	if transformed := after.TotalAlloc - before.TotalAlloc; parsed > transformed/4 {
		t.Errorf("parsing allocated %d KB, the transform %d KB", parsed>>10, transformed>>10)
	}
	// Its syntax errors are found with the i flag as without it.
	for _, pattern := range []string{`[b-a]`, `(?<a>x)(?<a>y)`, `\k<b>(?<a>x)`, `[\p{Foo}]`, `a{2,1}`, `(`} {
		for _, flags := range []string{"i", "iu", "", "u"} {
			_, err := ParseFile(nil, "", "/"+pattern+"/"+flags, 0)
			_, _, terr := TransformRegExp2(pattern, false, strings.Contains(flags, "u"), strings.Contains(flags, "i"))
			if _, incompat := terr.(RegexpErrorIncompatible); incompat {
				terr = nil
			}
			if (err == nil) != (terr == nil) {
				t.Errorf("/%s/%s: parse %v, transform %v", pattern, flags, err, terr)
			}
		}
	}
}

func TestTransformRegExpReviewFamilies(t *testing.T) {
	// Families of patterns that compiled in 170 to 240 ms, over the bound:
	// they now take the engines up to about 120 ms, or are refused. A class
	// of thousands of items took the transform itself 120 ms and 190 MB, a
	// union per item.
	items := func(n int, negated bool) string {
		var b strings.Builder
		b.WriteByte('[')
		if negated {
			b.WriteByte('^')
		}
		for i := 0; i < n; i++ {
			if negated {
				fmt.Fprintf(&b, `\u%04x-\u%04x`, 0x100+3*i, 0x101+3*i)
			} else {
				b.WriteRune(rune(0x100 + 2*i))
			}
		}
		b.WriteByte(']')
		return b.String()
	}
	wideNeg := func(n int) string {
		var b strings.Builder
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, "[^A-\u2fff%c]", rune(0x4e00+i%20000)+rune(i/20000)*0x10000)
		}
		return b.String()
	}
	for _, tc := range []struct {
		name, pattern string
		unicode, i    bool
		want          string
	}{
		{"6,784 items", items(6784, false), true, false, "go"},
		{"6,784 negated ranges", items(6784, true), true, false, "go"},
		{"75,000 distinct negated classes", wideNeg(75000), false, false, "too large"},
		{"15,616 boundaries", strings.Repeat(`\b`, 15616), true, true, "too large"},
		{"70,000 groups", strings.Repeat(`(a)`, 70000), true, false, "too large"},
	} {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		got := outcome(t, tc.pattern, tc.unicode, tc.i)
		runtime.ReadMemStats(&after)
		if got != tc.want {
			t.Errorf("%s: %s", tc.name, got)
		}
		if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 64<<20 {
			t.Errorf("%s: allocated %d MB", tc.name, alloc>>20)
		}
	}
}

func TestAddClassSet(t *testing.T) {
	// The class's set, appended range by range and sorted at its end, is
	// the union of its items' sets.
	rng := rand.New(rand.NewSource(1))
	for n := 0; n < 200; n++ {
		p := &_RegExp_parser{}
		var want runeSet
		for k := rng.Intn(30); k > 0; k-- {
			var set runeSet
			for j := rng.Intn(4); j >= 0; j-- {
				lo := rune(rng.Intn(300))
				set = set.union(runeSet{lo, lo + rune(rng.Intn(20))})
			}
			if rng.Intn(10) == 0 {
				set = set.complement()
			}
			want = want.union(set)
			p.addClassSet(set)
		}
		p.sortClassSet()
		if !p.classSet.equal(want) && !(len(p.classSet) == 0 && len(want) == 0) {
			t.Fatalf("%x, want %x", p.classSet, want)
		}
	}
}
