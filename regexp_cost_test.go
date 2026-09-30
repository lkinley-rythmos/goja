package goja

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/dop251/goja/parser"
)

// foldChars are the characters foldedClassesNode18 tests classes on.
const foldChars = `["k", "K", "\u212a", "s", "S", "\u017f", "\u00b5", "\u03bc", "\u039c", "\u00df", "\u1e9e", "\u0130", "\u0131", "i", "I", "\u01c5", "\u1f80", "\u1f88", "\u2126", "\u03c9", "a", "_", " ", "\u00e9", "\u0101", "\u4e00", "\n", "\u2028", "\ud801\udc00", "\ud801\udc28", "0"]`

// foldedClassesNode18 is whether each of foldChars matches a class with the
// iu flags in Node.js 18.20.3. The classes that ignoring case would cost the
// engines the most are given to them closed under case folding.
var foldedClassesNode18 = []struct{ class, want string }{
	{`[A-\uffef]`, "1111111111111111111111011101000"},
	{`[\u0080-\uffff]`, "1111111111111001111100011101000"},
	{`[^\x00-\x7f]`, "0000001111111001111100011101110"},
	{`[\u0100-\u01ff]`, "0001110000011001000000001000000"},
	{`[\u0400-\u04ff]`, "0000000000000000000000000000000"},
	{`[\u4e00-\u9fa5]`, "0000000000000000000000000100000"},
	{`[\W\d]`, "0000001111111001111100111111111"},
	{`[^\S\r\n]`, "0000000000000000000000100001000"},
	{`[\w\u0100-\u0fff]`, "1111111110011111001111001000001"},
	{`[^A-\uffef]`, "0000000000000000000000100010111"},
	{`[\u0000-\u{10ffff}]`, "1111111111111111111111111111111"},
}

func TestRegexpFoldedClassesMatchNode18(t *testing.T) {
	// With Go's regexp, and with regexp2 (through an empty lookahead), which
	// matched some of these wrongly: [\W\d] matched every letter.
	vm := New()
	for _, tc := range foldedClassesNode18 {
		for _, prefix := range []string{"", "(?=)"} {
			for _, flags := range []string{"iu", "giu"} {
				vm.Set("src", "^"+prefix+tc.class+"$")
				vm.Set("f", flags)
				v, err := vm.RunString(`var re = new RegExp(src, f); ` + foldChars + `.map(function(c) { re.lastIndex = 0; return re.test(c) ? 1 : 0; }).join("")`)
				if err != nil {
					t.Fatal(err)
				}
				if v.String() != tc.want {
					t.Errorf("/%s%s/%s: %s, want %s", prefix, tc.class, flags, v, tc.want)
				}
			}
		}
	}
}

// foldedClassesNonUnicodeNode18 is whether each of foldChars matches a class
// with the i flag in Node.js 18.20.3. Without the u flag Canonicalize makes
// characters equivalent (U+212A and U+017F are not variants of k and s).
var foldedClassesNonUnicodeNode18 = []struct{ class, want string }{
	{`[A-\uffef]`, "1111111111111111111111011101000"},
	{`[\u0080-\uffff]`, "0010011111111001111100011101000"},
	{`[^\x00-\x7f]`, "0010011111111001111100011101000"},
	{`[\u0100-\u01ff]`, "0000010000011001000000001000000"},
	{`[\u0400-\u04ff]`, "0000000000000000000000000000000"},
	{`[\u4e00-\u9fa5]`, "0000000000000000000000000100000"},
	{`[\W\d]`, "0010011111111001111100111111001"},
	{`[^\S\r\n]`, "0000000000000000000000100001000"},
	{`[\w\u0100-\u0fff]`, "1101111110011111000111001000001"},
	{`[^A-\uffef]`, "0000000000000000000000100010001"},
}

func TestRegexpFoldedClassesNonUnicodeMatchNode18(t *testing.T) {
	// With Go's regexp and regexp2 (through an empty lookahead), which
	// folded case otherwise.
	vm := New()
	for _, tc := range foldedClassesNonUnicodeNode18 {
		for _, prefix := range []string{"", "(?=)"} {
			for _, flags := range []string{"i", "gi"} {
				vm.Set("src", "^"+prefix+tc.class+"$")
				vm.Set("f", flags)
				v, err := vm.RunString(`var re = new RegExp(src, f); ` + foldChars + `.map(function(c) { re.lastIndex = 0; return re.test(c) ? 1 : 0; }).join("")`)
				if err != nil {
					t.Fatal(err)
				}
				if v.String() != tc.want {
					t.Errorf("/%s%s/%s: %s, want %s", prefix, tc.class, flags, v, tc.want)
				}
			}
		}
	}
}

func TestRegexpCanonicalizeClasses(t *testing.T) {
	// Every class of code units Canonicalize makes equivalent (recorded
	// with Node.js 18.20.3, see parser/regexp_canon_gen.js) is matched by a
	// character of it ignoring case without the u flag, and no neighbour
	// outside it is, with both engines.
	vm := New()
	if _, err := vm.RunString(`function check(members, others) {
		var res = [];
		for (var i = 0; i < members.length; i++) {
			var src = "\\u" + ("000" + members[i].toString(16)).slice(-4);
			var res1 = [new RegExp("^" + src + "$", "i"), new RegExp("^(?=)" + src + "$", "i"), new RegExp("^[" + src + "]$", "i")];
			for (var k = 0; k < res1.length; k++) {
				for (var j = 0; j < members.length; j++) if (!res1[k].test(String.fromCharCode(members[j]))) res.push(res1[k].source + " misses " + members[j]);
				for (var j = 0; j < others.length; j++) if (res1[k].test(String.fromCharCode(others[j]))) res.push(res1[k].source + " matches " + others[j]);
			}
		}
		return res.join("; ");
	}`); err != nil {
		t.Fatal(err)
	}
	check, _ := AssertFunction(vm.Get("check"))
	classes := parser.CanonClasses()
	for _, members := range classes {
		in := make(map[uint16]bool)
		for _, m := range members {
			in[m] = true
		}
		var others []uint16
		for _, m := range members {
			for _, d := range []int{-2, -1, 1, 2, 0x20, -0x20} {
				if o := int(m) + d; o >= 0 && o <= 0xffff && !in[uint16(o)] {
					others = append(others, uint16(o))
				}
			}
		}
		v, err := check(nil, vm.ToValue(members), vm.ToValue(others))
		if err != nil {
			t.Fatal(err)
		}
		if v.String() != "" {
			t.Errorf("%v: %s", members, v)
		}
	}
	if len(classes) < 1000 {
		t.Errorf("%d classes", len(classes))
	}
}

func TestRegexpCaseInsensitiveEscapesMatchNode18(t *testing.T) {
	// Ignoring case, regexp2 matched \W, [\W] and [^\w] with every
	// character, and [^\W] with none; they match as in Node.js 18.20.3,
	// which without the u flag takes U+017F and U+212A for no variants of s
	// and k (Go's regexp did).
	vm := New()
	const chars = `["s", "S", "\u017f", "k", "K", "\u212a", "\u00e9", " "]`
	for _, tc := range []struct{ pattern, want, wantNonUnicode string }{
		{`\W`, "00000011", "00100111"}, {`\w`, "11111100", "11011000"}, {`[\W]`, "00000011", "00100111"}, {`[\w]`, "11111100", "11011000"},
		{`[^\w]`, "00000011", "00100111"}, {`[^\W]`, "11111100", "11011000"}, {`\S`, "11111110", "11111110"}, {`\D`, "11111111", "11111111"}, {`.`, "11111111", "11111111"},
	} {
		for _, prefix := range []string{"", "(?=)"} {
			for _, flags := range []string{"iu", "i"} {
				want := tc.want
				if flags == "i" {
					want = tc.wantNonUnicode
				}
				vm.Set("src", prefix+tc.pattern)
				vm.Set("f", flags)
				v, err := vm.RunString(`var re = new RegExp(src, f); ` + chars + `.map(function(c) { return re.test(c) ? 1 : 0; }).join("")`)
				if err != nil {
					t.Fatal(err)
				}
				if v.String() != want {
					t.Errorf("/%s%s/%s: %s, want %s", prefix, tc.pattern, flags, v, want)
				}
			}
		}
	}
}

func TestRegexpCompileTimeBounded(t *testing.T) {
	// Compiling cannot be interrupted, so what a pattern costs the engines
	// is bounded at about 100 ms each: before, [A-\uffef] with the i flag
	// took Go's regexp 4 ms a copy to fold (1,000 copies 4 s), (?=)\W with
	// the i flag regexp2 50 ms, 3.5 million literal characters Go's regexp
	// 1 s, and 40 classes of 2,000 ranges regexp2 0.7 s. Node.js 18 compiles
	// the first two in under 150 ms, and refuses the literal characters as
	// too large when the pattern is first used.
	for _, tc := range []struct {
		name, pattern, flags string
		tooLarge             bool
	}{
		{"wide classes", strings.Repeat(`[A-\uffef]`, 1000), "i", false},
		{"wide classes g", strings.Repeat(`[A-\uffef]`, 1000), "gi", false},
		{"wide classes u", strings.Repeat(`[A-\uffef]`, 1000), "giu", false},
		{"negated wide classes", strings.Repeat(`[^\u0100-\uffff]`, 1000), "gi", false},
		{"non-word", "(?=)" + strings.Repeat(`\W.\S\D`, 200), "i", false},
		{"non-word u", "(?=)" + strings.Repeat(`\W.\S\D`, 200), "iu", false},
		{"literal", strings.Repeat("x", 3500000), "", true},
		{"repeated literal", "(?:" + strings.Repeat("x", 200000) + "){20}", "", false},
		{"classes", distinctClasses(40, 2000), "u", true},
		{"nested alternations", "a" + strings.Repeat("(?:ab|cd|", 999) + strings.Repeat(")", 999), "", false},
		{"backreferences in their groups", strings.Repeat("(", 999) + strings.Repeat(`\999`, 150000) + strings.Repeat(")", 999), "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vm := New()
			vm.Set("p", tc.pattern)
			vm.Set("f", tc.flags)
			start := time.Now()
			_, err := vm.RunString(`new RegExp(p, f).test("x")`)
			elapsed := time.Since(start)
			if tc.tooLarge != regexpTooLarge(err) || !tc.tooLarge && err != nil {
				t.Fatalf("err %.200v", err)
			}
			if elapsed > time.Second && !raceDetectorEnabled {
				t.Errorf("took %v", elapsed)
			}
		})
	}
}

func TestRegexpCompileInterrupt(t *testing.T) {
	// A script compiling such patterns without end stops soon after
	// Runtime.Interrupt. The patterns differ so that none is served from the
	// cache.
	for _, tc := range []struct{ name, script string }{
		{"wide classes", `for (var i = 0; ; i++) new RegExp("[A-\\uffef]".repeat(1000) + i, "i")`},
		{"non-word", `for (var i = 0; ; i++) new RegExp("(?=)" + "\\W".repeat(100) + i, "gi")`},
		{"too large", `for (var i = 0; ; i++) { try { new RegExp("x".repeat(3500000) + i); } catch (e) { if (!(e instanceof SyntaxError)) throw e; } }`},
	} {
		t.Run(tc.name, func(t *testing.T) { runInterrupted(t, tc.script, nil) })
	}
}

func TestRegexpCompileInterruptedBetweenSteps(t *testing.T) {
	// An Interrupt during one step of a compile is raised before the next:
	// the pattern is not compiled, and so not cached. Here it comes during
	// the transform of the pattern (about 100 ms), or during Go's regexp's
	// compile (about 100 ms, after a transform of about 5 ms), before
	// regexp2's for the g flag.
	for _, tc := range []struct{ name, src, flags string }{
		{"transform", distinctClasses(10000, 20), "u"},
		{"Go's regexp", strings.Repeat("[ab]", 70000), "g"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetRegexpCache()
			defer resetRegexpCache()
			vm := New()
			vm.Set("p", tc.src)
			vm.Set("f", tc.flags)
			timer := time.AfterFunc(40*time.Millisecond, func() { vm.Interrupt("halt") })
			defer timer.Stop()
			_, err := vm.RunString(`new RegExp(p, f)`)
			var ie *InterruptedError
			if !errors.As(err, &ie) {
				t.Fatalf("err %v", err)
			}
			regexpCache.Lock()
			_, cached := regexpCache.m[regexpCacheKey{src: tc.src, flags: tc.flags}]
			regexpCache.Unlock()
			if cached {
				t.Error("compiled after the Interrupt")
			}
		})
	}
}

// distinctWideClasses returns n distinct classes [\u0100-\uffff<digit><CJK>].
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

func TestRegexpDistinctCanonClassesCompileBounded(t *testing.T) {
	// Without the u flag, ignoring case, each distinct class is closed under
	// Canonicalize, which cannot be interrupted: 3,520 distinct wide classes
	// took 1.5 s with an Interrupt at 100 ms, and a literal of them 3.7 s to
	// compile. They are refused as too large, or compile, within the bound
	// (about 50 ms), from a string or a literal.
	for _, tc := range []struct {
		name, pattern, flags string
	}{
		{"wide", distinctWideClasses(3520), "i"},
		{"wide under the bound", distinctWideClasses(3000), "gi"},
		{"negated", strings.Replace(distinctWideClasses(3264), "[", "[^", -1), "i"},
		{"narrow", distinctClassesFrom(3904, 'a'), "i"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			check := func(what string, elapsed time.Duration, err error) {
				t.Helper()
				if err != nil && !regexpTooLarge(err) && !strings.Contains(err.Error(), "/: Regular expression too large at ") {
					msg := err.Error()
					t.Fatalf("%s: %s...%s", what, msg[:min(len(msg), 60)], msg[max(len(msg)-100, 0):])
				}
				if elapsed > 500*time.Millisecond && !raceDetectorEnabled {
					t.Errorf("%s took %v", what, elapsed)
				}
			}
			resetRegexpCache()
			defer resetRegexpCache()
			vm := New()
			vm.Set("p", tc.pattern)
			vm.Set("f", tc.flags)
			start := time.Now()
			_, err := vm.RunString(`new RegExp(p, f).test("\u4e01")`)
			check("new RegExp", time.Since(start), err)
			resetRegexpCache()
			start = time.Now()
			_, err = Compile("", "x = /"+tc.pattern+"/"+tc.flags, false)
			check("Compile", time.Since(start), err)
		})
	}
}

// distinctClassesFrom returns n distinct classes [<lo>-<c>] of characters from
// U+0100.
func distinctClassesFrom(n int, lo rune) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteByte('[')
		b.WriteRune(lo)
		b.WriteByte('-')
		b.WriteRune(rune(0x100 + i))
		b.WriteByte(']')
	}
	return b.String()
}

func TestRegexpFoldOrbitsMatchNode18(t *testing.T) {
	// With the u and i flags a character matches the characters of its case
	// folding orbit as V8 relates them (recorded with Node.js 18.20.3, see
	// parser/regexp_fold_gen.js), whatever the Unicode version of the Go
	// toolchain: as a literal, an escape, a class and a negated class, with
	// both engines. Every character Go's unicode tables fold is checked too:
	// Go 1.27 (Unicode 17) folds U+019B and U+A7DC, which Node.js 18 does
	// not, and Go 1.26 (Unicode 15.0) does not relate U+0390 and U+1FD3,
	// which V8 does (their full case foldings are the same).
	vm := New()
	if _, err := vm.RunString(`function check(c, members, others) {
		var res = [];
		var ch = String.fromCodePoint(c), esc = "\\u{" + c.toString(16) + "}";
		var lit = ch.replace(/[\\^$.*+?()[\]{}|\/-]/g, "\\$&");
		var res1 = [new RegExp("^" + lit + "$", "iu"), new RegExp("^" + esc + "$", "iu"), new RegExp("^(?=)" + esc + "$", "iu"),
			new RegExp("^[" + esc + "]$", "iu"), new RegExp("^(?=)[" + lit + "]$", "iu")];
		var neg = [new RegExp("^[^" + esc + "]$", "iu"), new RegExp("^(?=)[^" + lit + "]$", "iu")];
		for (var k = 0; k < res1.length; k++) {
			for (var j = 0; j < members.length; j++) if (!res1[k].test(String.fromCodePoint(members[j]))) res.push(res1[k].source + " misses " + members[j].toString(16));
			for (var j = 0; j < others.length; j++) if (res1[k].test(String.fromCodePoint(others[j]))) res.push(res1[k].source + " matches " + others[j].toString(16));
		}
		for (var k = 0; k < neg.length; k++) {
			for (var j = 0; j < members.length; j++) if (neg[k].test(String.fromCodePoint(members[j]))) res.push(neg[k].source + " matches " + members[j].toString(16));
			for (var j = 0; j < others.length; j++) if (!neg[k].test(String.fromCodePoint(others[j]))) res.push(neg[k].source + " misses " + others[j].toString(16));
		}
		return res.join("; ");
	}`); err != nil {
		t.Fatal(err)
	}
	check, _ := AssertFunction(vm.Get("check"))
	orbitOf := make(map[rune][]rune)
	for _, o := range parser.FoldOrbits() {
		for _, r := range o {
			orbitOf[r] = o
		}
	}
	chars := make(map[rune]bool)
	for r := range orbitOf {
		chars[r] = true
	}
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if unicode.SimpleFold(r) != r {
			chars[r] = true
		}
	}
	checked := 0
	for r := range chars {
		members := orbitOf[r]
		if members == nil {
			members = []rune{r}
		}
		in := make(map[rune]bool)
		for _, m := range members {
			in[m] = true
		}
		// The characters Go's tables fold r to, and neighbours.
		var others []rune
		add := func(o rune) {
			if o >= 0 && o <= unicode.MaxRune && !(o >= 0xd800 && o <= 0xdfff) && !in[o] {
				in[o] = true
				others = append(others, o)
			}
		}
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			add(f)
		}
		for _, d := range []rune{-1, 1, 0x20, -0x20} {
			add(r + d)
		}
		v, err := check(nil, vm.ToValue(r), vm.ToValue(members), vm.ToValue(others))
		if err != nil {
			t.Fatal(err)
		}
		if v.String() != "" {
			t.Errorf("%U: %s", r, v)
		}
		checked++
	}
	if checked < 2800 {
		t.Errorf("%d characters checked", checked)
	}
	// And no character outside the orbits matches one of them.
	var all, cls strings.Builder
	n := 0
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if r < 0xd800 || r > 0xdfff {
			all.WriteRune(r)
		}
	}
	for r := range orbitOf {
		fmt.Fprintf(&cls, `\u{%x}`, r)
		n++
	}
	vm.Set("all", all.String())
	vm.Set("cls", cls.String())
	for _, prefix := range []string{"", "(?=)"} {
		v, err := vm.RunString(`all.match(new RegExp("` + prefix + `[" + cls + "]", "giu")).length`)
		if err != nil {
			t.Fatal(err)
		}
		if v.ToInteger() != int64(n) {
			t.Errorf("%q: %d characters match the orbits' %d", prefix, v.ToInteger(), n)
		}
	}
}

func TestRegexpFoldOrbitsNode18Cases(t *testing.T) {
	// Recorded with Node.js 18.20.3.
	for _, tc := range []struct {
		script string
		want   bool
	}{
		{`/[\u1f00-\u1fff]/iu.test("\u0390")`, true},
		{`/[\u0380-\u03ff]/iu.test("\u1fd3")`, true},
		{`/[^\u1f00-\u1fff]/iu.test("\u0390")`, false},
		{`/[\u3000-\ufb05]/iu.test("\ufb06")`, true},
		{`/(?=[^])[\u13f7-\u5bb4]/iu.test("\u03b0")`, true},
		{`/\u0390/iu.test("\u1fd3")`, true},
		{`/\u03b0/iu.test("\u1fe3")`, true},
		{`/\ufb05/iu.test("\ufb06")`, true},
		{`/(?=)\ufb05/iu.test("\ufb06")`, true},
		{`/\u019b/iu.test("\ua7dc")`, false},
		{`/\ua7dc/iu.test("\u019b")`, false},
		{`/[\u0180-\u01ff]/iu.test("\ua7dc")`, false},
		{`/\u0264/iu.test("\ua7cb")`, false},
		{`/\u1c89/iu.test("\u1c8a")`, false},
		{`/\u{16ea0}/iu.test("\u{16ebb}")`, false},
		{`/\u{10d50}/iu.test("\u{10d70}")`, false},
		{`/\u2160/iu.test("\u2170")`, true},
		{`/\u0345/iu.test("\u1fbe")`, true},
		{`/\u00df/iu.test("\u1e9e")`, true},
		{`/\u0130/iu.test("i")`, false},
		{`/\u017f/iu.test("S")`, true},
		{`/\u212a/iu.test("k")`, true},
		{`/(a)\1/iu.test("aA")`, true},
		{`/(?<n>\u00e9)\k<n>/iu.test("\u00e9\u00c9")`, true},
	} {
		v, err := New().RunString(tc.script)
		if err != nil {
			t.Fatalf("%s: %v", tc.script, err)
		}
		if v.ToBoolean() != tc.want {
			t.Errorf("%s: %v", tc.script, v)
		}
	}
}
