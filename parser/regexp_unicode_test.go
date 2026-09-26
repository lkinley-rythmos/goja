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
		re2, r2, err := TransformRegExp2(tc.pattern, false, tc.unicode, tc.icase, false)
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
		_, _, err := TransformRegExp2(tc.pattern, false, true, false, false)
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
		if _, _, err := TransformRegExp2(tc.pattern, false, false, false, false); err != nil && !strings.Contains(tc.err, "class") {
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

func TestTransformRegExpExpansionLimit(t *testing.T) {
	// A pattern whose property escapes cost an engine that compiles it more
	// than maxRegexpExpansion is refused. \P{Alphabetic} is a large set with
	// no name Go's regexp knows, written as \p{L} and ranges; \p{L} costs
	// Go's regexp its table and regexp2 nothing; \p{Lu} with the i flag
	// costs regexp2 its closure under case folding, written as ranges.
	tooLarge := func(pattern string, ignoreCase, needRegexp2 bool) bool {
		t.Helper()
		_, _, err := TransformRegExp2(pattern, false, true, ignoreCase, needRegexp2)
		if err == nil {
			return false
		}
		want := "Invalid regular expression: /" + pattern + "/: Regular expression too large"
		if _, ok := err.(RegexpSyntaxError); !ok || err.Error() != want {
			t.Fatalf("%.40s...: err %v", pattern, err)
		}
		return true
	}
	for _, tc := range []struct {
		escape              string
		n                   int
		ignoreCase, regexp2 bool
		want                bool
	}{
		{`\P{Alphabetic}`, 100, true, true, false},
		{`\P{Alphabetic}`, 1000, true, true, true},
		{`\P{Alphabetic}`, 1000, false, false, true},
		{`[\P{L}\p{sc=Greek}]`, 2000, true, false, true},
		{`\p{L}`, 1000, false, false, false},
		{`\p{L}`, 2000, false, false, true},
		{`\p{L}`, 2000, false, true, true},
		{`\p{Lu}`, 400, true, false, false},
		{`\p{Lu}`, 400, true, true, true},
		{`(?=)\p{Lu}`, 400, true, false, true},
		{`\p{Lu}`, 50, true, true, false},
	} {
		pattern := strings.Repeat(tc.escape, tc.n)
		if strings.HasPrefix(tc.escape, "(?=)") {
			pattern = "(?=)" + strings.Repeat(tc.escape[4:], tc.n)
		}
		if got := tooLarge(pattern, tc.ignoreCase, tc.regexp2); got != tc.want {
			t.Errorf("%s x%d (i: %v, regexp2: %v): too large %v", tc.escape, tc.n, tc.ignoreCase, tc.regexp2, got)
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
