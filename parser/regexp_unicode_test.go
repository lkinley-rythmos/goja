package parser

import (
	"regexp"
	"strings"
	"testing"

	"github.com/dlclark/regexp2/v2"
)

func TestTransformRegExpUnicodeProperties(t *testing.T) {
	for _, tc := range []struct {
		pattern          string
		unicode, icase   bool
		re2, regexp2     string // "" for the input pattern; a trailing "..." matches a prefix
		incompatibleWith string // the incompatibility, if any
	}{
		// Go's regexp and regexp2 know General_Category and Script by name.
		{pattern: `\p{L}`, unicode: true, re2: `\p{L}`},
		{pattern: `\p{Letter}+`, unicode: true, re2: `\p{L}+`, regexp2: `\p{L}+`},
		{pattern: `\p{gc=Lu}\p{sc=Grek}`, unicode: true, re2: `\p{Lu}\p{Greek}`, regexp2: `\p{Lu}\p{Greek}`},
		{pattern: `[\p{L}\d]`, unicode: true, re2: `[\p{L}\d]`, regexp2: `[\p{L}\d]`},
		{pattern: `\P{L}`, unicode: true, re2: `\P{L}`, regexp2: `[^\p{L}]`},
		// regexp2 gets no negated categories: a class with negated
		// properties is written as the set it stands for, with the tables
		// it contains by name, or as the negation of its complement.
		{pattern: `[\P{L}\P{N}x]`, unicode: true, re2: `[\P{L}\P{N}x]`, regexp2: `[\u{0}-\u{10ffff}]`},
		{pattern: `[^\P{L}\p{N}x]`, unicode: true, re2: `[^\P{L}\p{N}x]`, regexp2: `[^\p{C}\p{Mn}\p{S}\p{P}\p{Mc}\p{N}\u{20}\u{78}\u{a0}...`},
		{pattern: `[\P{Lu}\p{L}]`, unicode: true, re2: `[\P{Lu}\p{L}]`, regexp2: `[\u{0}-\u{10ffff}]`},
		{pattern: `[\p{L}\P{Nd}]`, unicode: true, re2: `[\p{L}\P{Nd}]`, regexp2: `[^\p{Nd}]`},
		// Ignoring case, Go's regexp gets the case variants its tables lack
		// (U+0345 folds to U+03B9, in LC), and regexp2 a set matched
		// case-sensitively.
		{pattern: `\p{Lu}`, unicode: true, icase: true, re2: `\p{Lu}`, regexp2: `(?-i:[\u{41}-\u{5a}\u{61}-\u{7a}\u{b5}...`},
		{pattern: `\p{LC}`, unicode: true, icase: true, re2: `[\p{LC}\x{345}]`, regexp2: `(?-i:[\p{LC}\u{345}])`},
		{pattern: `\p{sc=Arabic}`, unicode: true, icase: true, re2: `\p{Arabic}`, regexp2: `(?-i:\p{Arabic})`},
		// Negated, ECMAScript adds the case variants of what is left: the
		// negation of LC, and the letters U+0345 folds to.
		{pattern: `\P{LC}`, unicode: true, icase: true, re2: `(?-i:[\P{LC}\x{399}\x{3b9}\x{1fbe}])`, regexp2: `(?-i:[^\p{LC}]|[\u{345}\u{399}\u{3b9}\u{1fbe}])`},
		{pattern: `[\P{LC}]`, unicode: true, icase: true, re2: `[\P{LC}\x{345}\x{399}\x{3b9}\x{1fbe}]`, regexp2: `(?-i:[^\u{41}-\u{5a}\u{61}-\u{7a}\u{b5}...`},
		{pattern: `x[\p{Lu}a]`, unicode: true, icase: true, re2: `x[\p{Lu}a]`, regexp2: `x(?-i:[\u{41}-\u{5a}\u{61}-\u{7a}\u{b5}\u{c0}-\u{d6}...`},
		// Ignoring case, a class with property escapes is given to regexp2
		// as the set it stands for.
		{pattern: `[^a\P{Any}]`, unicode: true, icase: true, re2: `(?-i:[^\x{41}\x{61}])`, regexp2: `(?-i:[^\u{41}\u{61}])`},
		{pattern: `[\w\p{AHex}]`, unicode: true, icase: true, re2: `(?-i:[\x{30}-\x{39}\x{41}-\x{5a}\x{5f}\x{61}-\x{7a}\x{17f}\x{212a}])`, regexp2: `(?-i:[\u{30}-\u{39}\u{41}-\u{5a}\u{5f}\u{61}-\u{7a}\u{17f}\u{212a}])`},
		{pattern: `[\w\p{Nd}]`, unicode: true, icase: true, re2: `[\w\p{Nd}]`, regexp2: `(?-i:[\p{Nd}\u{41}-\u{5a}\u{5f}\u{61}-\u{7a}\u{17f}\u{212a}])`},
		// Other properties are sets: written with the tables the engine
		// knows by name that they contain (regexp2 knows the binary
		// properties of Go's unicode package too), and the rest as ranges,
		// or as the negation of the same for the complement.
		{pattern: `\p{ASCII_Hex_Digit}`, unicode: true, re2: `[\x{30}-\x{39}\x{41}-\x{46}\x{61}-\x{66}]`, regexp2: `[\u{30}-\u{39}\u{41}-\u{46}\u{61}-\u{66}]`},
		{pattern: `\p{Alphabetic}`, unicode: true, re2: `[\p{L}\x{345}...`, regexp2: `[\p{L}\p{Other_Alphabetic}\u{16ee}-\u{16f0}...`},
		{pattern: `\P{Alphabetic}`, unicode: true, re2: `[^\p{L}\x{345}...`, regexp2: `[^\p{L}\p{Other_Alphabetic}\u{16ee}-\u{16f0}...`},
		{pattern: `\P{Alphabetic}`, unicode: true, icase: true, re2: `(?-i:[^\p{L}\x{345}...`, regexp2: `(?-i:[^\p{L}\p{Other_Alphabetic}\u{16ee}-\u{16f0}...`},
		{pattern: `\p{scx=Latn}`, unicode: true, re2: `[\p{Latin}\x{363}-\x{36f}...`, regexp2: `[\p{Latin}\u{363}-\u{36f}...`},
		{pattern: `[\P{Assigned}]`, unicode: true, re2: `[\p{Cn}]`, regexp2: `[\p{Cn}]`},
		{pattern: `[^\P{Assigned}]`, unicode: true, re2: `[^\p{Cn}]`, regexp2: `[^\p{Cn}]`},
		{pattern: `[\p{AHex}_]`, unicode: true, re2: `[\x{30}-\x{39}\x{41}-\x{46}\x{61}-\x{66}_]`, regexp2: `[\u{30}-\u{39}\u{41}-\u{46}\u{61}-\u{66}_]`},
		{pattern: `[^\P{AHex}_]`, unicode: true, re2: `[^\x{0}-\x{2f}\x{3a}-\x{40}\x{47}-\x{60}\x{67}-\x{10ffff}_]`, regexp2: `[\u{30}-\u{39}\u{41}-\u{46}\u{61}-\u{66}]`},
		{pattern: `\p{AHex}`, unicode: true, icase: true, re2: `(?-i:[\x{30}-\x{39}\x{41}-\x{46}\x{61}-\x{66}])`, regexp2: `(?-i:[\u{30}-\u{39}\u{41}-\u{46}\u{61}-\u{66}])`},
		{pattern: `\P{Any}[\P{Any}]`, unicode: true, re2: `\P{Any}[\P{Any}]`, regexp2: `[][]`},
		{pattern: `\p{Any}`, unicode: true, re2: `[\x{0}-\x{10ffff}]`, regexp2: `[\u{0}-\u{10ffff}]`},
		// With a construct Go's regexp lacks, only the regexp2 pattern is made.
		{pattern: `(?=a)\p{Lu}\P{sc=Grek}`, unicode: true, regexp2: `(?=a)\p{Lu}[^\p{Greek}]`, incompatibleWith: "re2: Invalid (?=) <lookahead>"},
		// Without the u flag, \p is the letter p.
		{pattern: `\p{L}`, re2: `p{L}`},
		{pattern: `[\P]`, re2: `[P]`},
	} {
		re2, r2, err := TransformRegExp2(tc.pattern, false, tc.unicode, tc.icase)
		if tc.incompatibleWith != "" {
			if _, ok := err.(RegexpErrorIncompatible); !ok || err.Error() != tc.incompatibleWith {
				t.Errorf("%s: err %v", tc.pattern, err)
			}
		} else if err != nil {
			t.Errorf("%s: %v", tc.pattern, err)
			continue
		}
		match := func(what, got, want string) {
			if want == "" {
				want = tc.pattern
			}
			if prefix, ok := strings.CutSuffix(want, "..."); ok && strings.HasPrefix(got, prefix) || got == want {
				return
			}
			if len(got) > 80 {
				got = got[:80] + "..."
			}
			t.Errorf("%s (u: %v, i: %v): %s pattern %s, want %s", tc.pattern, tc.unicode, tc.icase, what, got, want)
		}
		if tc.incompatibleWith == "" {
			match("Go", re2, tc.re2)
			flags := ""
			if tc.icase {
				flags = "(?i)"
			}
			if _, err := regexp.Compile(flags + re2); err != nil {
				t.Errorf("%s: Go's regexp: %v", tc.pattern, err)
			}
		}
		match("regexp2", r2, tc.regexp2)
		if !tc.unicode {
			// regexp2 has its own ideas about \P without the u flag.
			continue
		}
		opts := regexp2.ECMAScript
		opts |= regexp2.Unicode
		if tc.icase {
			opts |= regexp2.IgnoreCase
		}
		if _, err := regexp2.Compile(r2, opts); err != nil {
			t.Errorf("%s: regexp2: %v", tc.pattern, err)
		}
	}
}

func TestTransformRegExpUnicodeSyntax(t *testing.T) {
	for _, tc := range []struct {
		pattern, err string // err "" for a valid pattern
	}{
		{`\p{Foo}`, "Invalid property name"},
		{`[\p{Foo}]`, "Invalid property name in character class"},
		{`\p{Script=Hrkt}`, "Invalid property name"},
		{`\q`, "Invalid escape"},
		{`\-`, "Invalid escape"},
		{`[\-]`, ""},
		{`a{`, "Incomplete quantifier"},
		{`]`, "Lone quantifier brackets"},
		{`(?=a)\q`, "Invalid escape"},
		{`(?=a)*`, "Invalid quantifier"},
		{`\1(a)`, ""},
		{`\2(a)`, "Invalid escape"},
		{`(?<a>x)\k<a>`, ""},
		{`(?<a>x)\k<b>`, "Invalid named capture referenced"},
		{`[\d-z]`, "Invalid character class"},
		{`[\u{5a}-\u{41}]`, "Range out of order in character class"},
		{`\u{110000}`, "Invalid Unicode escape"},
	} {
		_, _, err := TransformRegExp2(tc.pattern, false, true, false)
		if tc.err == "" {
			if _, ok := err.(RegexpSyntaxError); ok {
				t.Errorf("%s: %v", tc.pattern, err)
			}
			continue
		}
		want := "Invalid regular expression: /" + tc.pattern + "/: " + tc.err
		if _, ok := err.(RegexpSyntaxError); !ok || err.Error() != want {
			t.Errorf("%s: err %v, want %s", tc.pattern, err, want)
		}
		// Without the u flag, none of these is an error, except the ranges.
		if _, _, err := TransformRegExp2(tc.pattern, false, false, false); err != nil && !strings.Contains(tc.err, "class") {
			if _, ok := err.(RegexpSyntaxError); ok {
				t.Errorf("%s without u: %v", tc.pattern, err)
			}
		}
	}
}

func TestRuneSet(t *testing.T) {
	a := runeSet{'a', 'c', 'x', 'x'}
	b := runeSet{'d', 'f', 'w', 'z'}
	if got := a.union(b); !got.equal(runeSet{'a', 'f', 'w', 'z'}) {
		t.Errorf("union: %q", got)
	}
	if got := a.complement(); !got.equal(runeSet{0, 'a' - 1, 'd', 'w', 'y', 0x10ffff}) {
		t.Errorf("complement: %q", got)
	}
	if got := a.union(b).minus(runeSet{'b', 'e'}); !got.equal(runeSet{'a', 'a', 'f', 'f', 'w', 'z'}) {
		t.Errorf("minus: %q", got)
	}
	if got := (runeSet{0, 10, 20, 30}).minus(runeSet{0, 0, 2, 3, 5, 5, 10, 22, 30, 40}); !got.equal(runeSet{1, 1, 4, 4, 6, 9, 23, 29}) {
		t.Errorf("minus: %d", got)
	}
	if got := (runeSet{0, 10}).minus(runeSet{0, 10}); len(got) != 0 {
		t.Errorf("minus: %d", got)
	}
	if got := (runeSet{5, 10}).minus(runeSet{0, 1, 20, 30}); !got.equal(runeSet{5, 10}) {
		t.Errorf("minus: %d", got)
	}
	if !a.contains('b') || a.contains('d') || !a.contains('x') || a.contains(-1) {
		t.Error("contains")
	}
	// Simple case folding: k, K and KELVIN SIGN; s, S and LONG S; the
	// dotted capital I has no simple folding.
	if got := caseClosure(runeSet{'k', 'k', 's', 's', 0x130, 0x130}); !got.equal(runeSet{'K', 'K', 'S', 'S', 'k', 'k', 's', 's', 0x130, 0x130, 0x17f, 0x17f, 0x212a, 0x212a}) {
		t.Errorf("caseClosure: %x", got)
	}
	// The closure of everything but K is everything.
	if got := complementClosure(runeSet{'K', 'K'}); !got.equal(runeSet{0, 0x10ffff}) {
		t.Errorf("complementClosure: %x", got)
	}
	// The closure of everything but the dotted I lacks it.
	if got := complementClosure(runeSet{0x130, 0x130}); !got.equal(runeSet{0, 0x12f, 0x131, 0x10ffff}) {
		t.Errorf("complementClosure: %x", got)
	}
}

func TestRegexpPropertyNames(t *testing.T) {
	for _, tc := range []struct {
		name string
		ok   bool
	}{
		{"L", true}, {"Letter", true}, {"gc=L", true}, {"General_Category=Letter", true}, {"LC", true}, {"Cased_Letter", true},
		{"digit", true}, {"punct", true}, {"Combining_Mark", true}, {"Script=Greek", true}, {"sc=Grek", true},
		{"scx=Grek", true}, {"Script_Extensions=Greek", true}, {"sc=Qaai", true}, {"sc=Zzzz", true}, {"scx=Unknown", true},
		{"Any", true}, {"ASCII", true}, {"Assigned", true}, {"Alpha", true}, {"space", true}, {"Emoji", true}, {"ExtPict", true},
		{"letter", false}, {"Lu ", false}, {"gc=Greek", false}, {"sc=L", false}, {"General_Category", false}, {"Script", false},
		{"Hyphen", false}, {"Other_Alphabetic", false}, {"ASCII=Y", false}, {"sc=Hrkt", false}, {"IsLatin", false},
		{"Block=Basic_Latin", false}, {"RGI_Emoji", false}, {"any", false}, {"L=L", false}, {"=L", false}, {"", false},
	} {
		if _, ok := lookupRegexpProperty(tc.name); ok != tc.ok {
			t.Errorf("%q: %v", tc.name, ok)
		}
	}
}

// transformUnicode transforms a pattern with the u flag, for both engines.
func transformUnicode(pattern string, ignoreCase bool) (re2, regexp2 string, err error) {
	return TransformRegExp2(pattern, false, true, ignoreCase)
}

// lookupProperty returns a property by name.
func lookupProperty(t *testing.T, name string) regexpProperty {
	t.Helper()
	p, ok := lookupRegexpProperty(name)
	if !ok {
		t.Fatalf("no property %s", name)
	}
	return p
}

// parseUnicode parses a pattern with the u flag, for what it costs and
// writes.
func parseUnicode(pattern string, ignoreCase bool) *_RegExp_parser {
	p := &_RegExp_parser{str: pattern, length: len(pattern), unicode: true, ignoreCase: ignoreCase}
	p.parse()
	return p
}

func TestTransformRegExpExpansionLimit(t *testing.T) {
	// A pattern whose property escapes cost an engine that compiles it more
	// than maxRegexpExpansion (about 100 ms of compile) is refused, whatever
	// its flags: regexp2 compiles any pattern a match needs it for (a split
	// of a large input with \b, or of a string with a lone surrogate); Go's
	// regexp compiles any pattern without a construct it lacks, wherever
	// that construct is. \P{Alphabetic} is a large set with no name Go's
	// regexp knows, written as \p{L} and ranges; \p{L} costs Go's regexp its
	// table and regexp2 its name; \p{Dia} costs Go's regexp its ranges
	// written out and regexp2 its name (Diacritic); \p{Lu} with the i flag
	// costs Go's regexp its table and case variants, and regexp2 its closure
	// under case folding, written as ranges;
	// \p{Changes_When_NFKC_Casefolded} is ranges for both, which cost
	// regexp2 more per range the more a class has.
	tooLarge := func(pattern string, ignoreCase bool) bool {
		t.Helper()
		_, _, err := transformUnicode(pattern, ignoreCase)
		if err == nil {
			return false
		}
		if _, incompat := err.(RegexpErrorIncompatible); incompat {
			return false
		}
		want := "Invalid regular expression: /" + pattern + "/: Regular expression too large"
		if _, ok := err.(RegexpSyntaxError); !ok || err.Error() != want {
			t.Fatalf("%.40s...: err %.200v", pattern, err)
		}
		return true
	}
	for _, tc := range []struct {
		prefix, escape, suffix string
		n                      int
		ignoreCase             bool
		want                   bool
	}{
		{"", `\P{Alphabetic}`, "", 100, true, false},
		{"", `\P{Alphabetic}`, "", 1000, true, true},
		{"", `\P{Alphabetic}`, "", 1000, false, true},
		{"", `[\P{L}\p{sc=Greek}]`, "", 2000, true, true},
		{"", `\p{L}`, "", 4000, false, false},
		{"", `\p{L}`, "", 6000, false, true},
		{"", `\p{Dia}`, "", 2000, false, false},
		{"", `\p{Dia}`, "", 3000, false, true},
		{"", `\p{Lu}`, "", 400, true, false},
		{"", `\p{Lu}`, "", 800, true, true},
		{"", `\p{Changes_When_NFKC_Casefolded}`, "", 30, false, false},
		// Not refused early: these compile in about 50 ms.
		{"", `\p{Assigned}`, "", 2000, false, false},
		{"", `\p{L}`, "", 1500, true, false},
		{"", `[\p{L}\p{N}_]`, "", 300, false, false},
		{"", `\p{Emoji}`, "", 500, false, false},
		{"", `\p{Changes_When_NFKC_Casefolded}`, "", 80, false, true},
		{`\b`, `\p{Changes_When_NFKC_Casefolded}`, "", 80, false, true},
		// A pattern only regexp2 compiles costs what it costs regexp2.
		{"(?=)", `\p{Dia}`, "", 3000, false, false},
		{"", `\p{Dia}`, "(?=)", 3000, false, false},
		{"(", `\p{Dia}`, `)\1`, 3000, false, false},
		{"(?=)", `\p{Lu}`, "", 1000, true, true},
		// So is one with a count Go's regexp refuses.
		{"", `\p{L}`, "x{1001}", 6000, false, false},
		{"x{1001}", `\p{L}`, "", 6000, false, false},
		{"", `\p{L}`, "x{2,1001}", 6000, false, false},
		{"", `\p{L}`, "x{1000}", 6000, false, true},
		{"x{1001}", `\p{Changes_When_NFKC_Casefolded}`, "", 80, false, true},
		// A class of a single named table costs its ranges (\p{Assigned} is
		// [^\p{Cn}] for Go's regexp): 8,000 copies take about 250 ms.
		{"", `\p{Assigned}`, "", 8000, false, true},
		{"(?=)", `\p{Changes_When_NFKC_Casefolded}`, "", 80, false, true},
	} {
		pattern := tc.prefix + strings.Repeat(tc.escape, tc.n) + tc.suffix
		if got := tooLarge(pattern, tc.ignoreCase); got != tc.want {
			t.Errorf("%s(%s x%d)%s (i: %v): too large %v", tc.prefix, tc.escape, tc.n, tc.suffix, tc.ignoreCase, got)
		}
	}
}

func TestTransformRegExpExpansionRegexp2Only(t *testing.T) {
	// A pattern with a construct Go's regexp lacks is compiled by regexp2
	// alone: what its escapes would cost Go's regexp is neither counted nor
	// written, before the construct or after it.
	for _, tc := range []struct{ prefix, suffix string }{{"(?=)", ""}, {"", "(?=)"}, {"(?<=a)", ""}, {"(", `)\1`}, {"a{1001}", ""}, {"", "a{0,1001}"}} {
		pattern := tc.prefix + strings.Repeat(`\p{Dia}`, 3000) + tc.suffix
		p := parseUnicode(pattern, false)
		if p.err != nil || p.incompat == nil {
			t.Fatalf("%s...%s: err %.100v, incompatible %v", tc.prefix, tc.suffix, p.err, p.incompat)
		}
		// \p{Dia} is 3 KB for Go's regexp: 3000 copies would be 9 MB.
		if n := p.goRegexp.Len(); n > 8<<20 {
			t.Errorf("%s...%s: %d bytes written for Go's regexp", tc.prefix, tc.suffix, n)
		}
		if tc.suffix == "" && (p.goRegexp.Len() > 64 || p.goExpansion > 0) {
			t.Errorf("%s...: %d bytes written for Go's regexp after the construct, cost %d", tc.prefix, p.goRegexp.Len(), p.goExpansion)
		}
		if p.regexp2Expansion < 3000*expansionEscape {
			t.Errorf("%s...%s: regexp2 cost %d", tc.prefix, tc.suffix, p.regexp2Expansion)
		}
	}
}

func TestTransformRegExpExpansionGoPastBound(t *testing.T) {
	// Past its bound nothing more is written for Go's regexp: with the i
	// flag, a class of a property with no name Go's regexp knows would be
	// rewritten as its closure under case folding, about 3 KB for \p{Dia}.
	past := strings.Repeat(`\p{Dia}`, 3000)
	base, classes := parseUnicode(past, true), parseUnicode(past+strings.Repeat(`[\p{Dia}a]`, 200), true)
	if base.goExpansion <= maxRegexpExpansion {
		t.Fatalf("Go's regexp costs %d units", base.goExpansion)
	}
	if n := classes.goRegexp.Len() - base.goRegexp.Len(); n > 200*16 {
		t.Errorf("%d bytes written for Go's regexp past its bound", n)
	}
}

func TestTransformRegExpExpansionAbortsEarly(t *testing.T) {
	// A pattern is refused at the escape that takes regexp2 past the bound,
	// and nothing more is written for Go's regexp once it is past it, so
	// what is written for each stays small: \p{Dia} costs regexp2 its name
	// (it knows Diacritic), and Go's regexp a class of about 3 KB, which
	// 100,000 copies would make 300 MB.
	for _, tc := range []struct {
		escape     string
		ignoreCase bool
	}{
		{`\p{Dia}`, false},
		{`[\p{Dia}]`, false},
		{`\p{Term}`, true},
		{`\p{L}`, false},
	} {
		p := parseUnicode(strings.Repeat(tc.escape, 100000), tc.ignoreCase)
		if _, ok := p.err.(RegexpSyntaxError); !ok || !strings.HasSuffix(p.err.Error(), "Regular expression too large") {
			t.Fatalf("%s: err %.100v", tc.escape, p.err)
		}
		if n, n2 := p.goRegexp.Len(), p.regexp2.Len(); n > 16<<20 || n2 > 16<<20 {
			t.Errorf("%s: wrote %d bytes for Go's regexp and %d for regexp2 before refusing", tc.escape, n, n2)
		}
	}
}

// propertyEscapeForms returns an escape of every property in the forms a
// pattern can have it, inside a class and out.
func propertyEscapeForms() []string {
	seen := map[string]bool{}
	var names []string
	add := func(name, canonical string) {
		if !seen[canonical] {
			seen[canonical] = true
			names = append(names, name)
		}
	}
	for k, v := range regexpGeneralCategoryValues {
		add(k, "gc="+v)
	}
	for k, v := range regexpBinaryPropertyNames {
		add(k, v)
	}
	for _, s := range []string{"Latin", "Greek", "Common", "Arabic", "Han", "Cyrillic", "Inherited", "Zzzz"} {
		names = append(names, "sc="+s, "scx="+s)
	}
	var escapes []string
	for _, n := range names {
		for _, form := range []string{`\p{X}`, `\P{X}`, `[\p{X}]`, `[^\p{X}]`, `[\P{X}a]`, `[^\p{X}\p{Lu}]`, `[\p{X}\p{L}\p{N}]`} {
			escapes = append(escapes, strings.Replace(form, "X", n, 1))
		}
	}
	return escapes
}

func TestTransformRegExpExpansionBoundsText(t *testing.T) {
	// What a property escape writes for an engine is bounded by what it
	// costs the engine and the escape's length, so that bounding the cost
	// bounds the text (see TestTransformRegExpExpansionAbortsEarly): Go's
	// text is under 7 bytes a unit, regexp2's under 14 (a name costs it one
	// unit), and an escape that costs nothing is written in a few times its
	// length.
	for _, escape := range propertyEscapeForms() {
		for _, ignoreCase := range []bool{false, true} {
			p := parseUnicode(escape, ignoreCase)
			if p.err != nil {
				t.Fatalf("%s: %v", escape, p.err)
			}
			in := len(escape)
			if n := len(p.ResultString()); n > 8*p.goExpansion+8*in {
				t.Errorf("/%s/ (i: %v): %d bytes for Go's regexp cost %d units", escape, ignoreCase, n, p.goExpansion)
			}
			if n := len(p.regexp2Pattern()); n > 16*p.regexp2Expansion+8*in {
				t.Errorf("/%s/ (i: %v): %d bytes for regexp2 cost %d units", escape, ignoreCase, n, p.regexp2Expansion)
			}
		}
	}
}

func TestTransformRegExpExpansionModel(t *testing.T) {
	// The cost model's factors, as relations measured on the engines (see
	// maxRegexpExpansion; times on the reference machine).

	// Go's regexp sorts the ranges of a class's named tables together:
	// [\p{L}\p{N}] takes about 7 times as long to compile as \p{L}\p{N}
	// (about 180 µs against 25 µs), so a named table in a class costs at
	// least twice its ranges, in an escape or in a property written as
	// named tables and ranges (\p{Alphabetic} is \p{L} and ranges for Go's
	// regexp), and a class of tables four times its tables as escapes.
	for _, tc := range []struct {
		pattern string
		names   []string
	}{
		{`[\p{L}\p{N}]`, []string{"L", "N"}},
		{`[\p{Lu}\p{sc=Greek}\p{Nd}]`, []string{"Lu", "Greek", "Nd"}},
		{`\p{Alphabetic}`, []string{"L"}},
		{`\p{Uppercase}`, []string{"Lu"}},
	} {
		p := parseUnicode(tc.pattern, false)
		ranges := 0
		for _, name := range tc.names {
			if !strings.Contains(p.ResultString(), `\p{`+name+`}`) && !strings.Contains(p.ResultString(), `\P{`+name+`}`) {
				t.Fatalf("/%s/ is not written with %s: %.200s", tc.pattern, name, p.ResultString())
			}
			ranges += namedTableRanges(name)
		}
		if p.goExpansion < 2*ranges {
			t.Errorf("/%s/ costs Go's regexp %d units for named tables of %d ranges", tc.pattern, p.goExpansion, ranges)
		}
	}
	class, escapes := parseUnicode(`[\p{L}\p{N}]`, false), parseUnicode(`\p{L}\p{N}`, false)
	if class.goExpansion < 4*escapes.goExpansion {
		t.Errorf("[\\p{L}\\p{N}] costs Go's regexp %d units, \\p{L}\\p{N} %d", class.goExpansion, escapes.goExpansion)
	}
	// An escape of a named table costs Go's regexp its ranges, about 35 ns
	// each (\p{L}, 684 ranges, 23 µs), and with the i flag also the case
	// variants it adds: \p{Lu} then takes about 11 times as long.
	if p := parseUnicode(`\p{L}`, false); p.goExpansion < namedTableRanges("L")/3 {
		t.Errorf("\\p{L} costs Go's regexp %d units", p.goExpansion)
	}
	if fold, plain := parseUnicode(`\p{Lu}`, true), parseUnicode(`\p{Lu}`, false); fold.goExpansion < 4*plain.goExpansion {
		t.Errorf("\\p{Lu} costs Go's regexp %d units with the i flag, %d without", fold.goExpansion, plain.goExpansion)
	}
	// Every escape costs each engine at least 1.5 to 3 µs, whatever it
	// stands for: \p{Any} is one range, and regexp2 knows Diacritic.
	for _, pattern := range []string{`\p{Any}`, `\P{Any}`, `\p{Dia}`, `\p{L}`, `[\p{L}]`} {
		if p := parseUnicode(pattern, false); p.goExpansion < 15 || p.regexp2Expansion < 15 {
			t.Errorf("/%s/: Go's regexp %d units, regexp2 %d", pattern, p.goExpansion, p.regexp2Expansion)
		}
	}

	// regexp2 sorts a class's ranges again for each one it adds, so a class
	// costs it more per range the more ranges it has, and the ranges of a
	// class's properties count together; a name costs it about 1 µs (a
	// class of 8 names about 8 µs).
	rangeClass := func(n int) runeSet {
		s := make(runeSet, 0, 2*n)
		for i := 0; i < n; i++ {
			s = append(s, rune(0x1000+2*i), rune(0x1000+2*i))
		}
		return s
	}
	var b strings.Builder
	small, large := writeClass(&b, rangeClass(300), true), writeClass(&b, rangeClass(1200), true)
	if large < 6*small {
		t.Errorf("a class of 1200 ranges costs regexp2 %d units, one of 300 %d", large, small)
	}
	xidc, cwkcf := lookupProperty(t, "XIDC").classItems(false, true), lookupProperty(t, "CWKCF").classItems(false, true)
	if both := parseUnicode(`[\p{XIDC}\p{CWKCF}]`, false); both.regexp2Expansion < regexp2TextUnits(xidc.ranges+cwkcf.ranges) {
		t.Errorf("a class of %d and %d ranges costs regexp2 %d units", xidc.ranges, cwkcf.ranges, both.regexp2Expansion)
	}
	var tables runeSet
	for _, name := range []string{"Lu", "Ll", "Lt", "Nd", "Nl", "Pd", "Ps", "Pe"} {
		tables = tables.union(lookupProperty(t, name).charSet(false, false))
	}
	var names strings.Builder
	units := writeClass(&names, tables, true)
	named := strings.Count(names.String(), `\p{`)
	ranges := strings.Count(names.String(), `\u{`) - strings.Count(names.String(), `-\u{`)
	if named < 2 || units < regexp2TextUnits(ranges)+7*named {
		t.Errorf("a class of %d names and %d ranges costs regexp2 %d units", named, ranges, units)
	}

	// A class rewritten as the set it stands for is computed by the parser
	// whichever engine compiles it, so it costs both.
	for _, pattern := range []string{`[\P{Any}]`, `[^\P{Any}]`} {
		p := parseUnicode(pattern, false)
		if p.goExpansion < expansionClass || p.regexp2Expansion < expansionClass {
			t.Errorf("/%s/: Go's regexp %d units, regexp2 %d", pattern, p.goExpansion, p.regexp2Expansion)
		}
	}
	for _, pattern := range []string{`[\p{Dia}a]`, `[\p{Dia}\P{Any}]`} {
		p := parseUnicode(pattern, true)
		if p.goExpansion < expansionClass || p.regexp2Expansion < expansionClass {
			t.Errorf("/%s/i: Go's regexp %d units, regexp2 %d", pattern, p.goExpansion, p.regexp2Expansion)
		}
	}
}

func TestRuneSetCover(t *testing.T) {
	if !(runeSet{'b', 'c', 'x', 'x'}).subsetOf(runeSet{'a', 'c', 'x', 'z'}) || (runeSet{'b', 'd'}).subsetOf(runeSet{'a', 'c', 'x', 'z'}) ||
		(runeSet{0, 0}).subsetOf(nil) || !(runeSet{}).subsetOf(runeSet{'a', 'a'}) {
		t.Error("subsetOf")
	}
	for _, regexp2 := range []bool{false, true} {
		tables := engineNamedTables(regexp2)
		for i := 1; i < len(tables); i++ {
			if len(tables[i].set) > len(tables[i-1].set) {
				t.Fatalf("regexp2 %v: tables not sorted at %d", regexp2, i)
			}
		}
		// The cover of a set is exact: the names and the rest make it up.
		for _, p := range []regexpProperty{{propBinary, "Alphabetic"}, {propBinary, "ID_Continue"}, {propBinary, "Assigned"}, {propScriptExtensions, "Latin"}} {
			for _, negate := range []bool{false, true} {
				set := p.charSet(negate, false)
				names, rest := set.cover(tables)
				if len(names) > coverMaxNames {
					t.Errorf("%v: %d names", p, len(names))
				}
				got := rest
				for _, name := range names {
					var table runeSet
					for _, tb := range tables {
						if tb.name == name {
							table = tb.set
						}
					}
					if table == nil || !table.subsetOf(set) {
						t.Errorf("%v negate=%v: %s is not a subset", p, negate, name)
					}
					got = got.union(table)
				}
				if !got.equal(set) {
					t.Errorf("%v negate=%v: cover %v differs from the set", p, negate, names)
				}
			}
		}
		// A small set is written as it is.
		if names, rest := (runeSet{'0', '9'}).cover(tables); len(names) != 0 || !rest.equal(runeSet{'0', '9'}) {
			t.Errorf("small set: %v %v", names, rest)
		}
	}
}
