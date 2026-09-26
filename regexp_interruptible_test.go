package goja

import (
	"io"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// The uri-reference format of ajv-formats (2.x), the pattern a single match
// over 45 MB of was found to run for 17 s past Runtime.Interrupt.
const ajvURIReference = `^(?:[a-z][a-z0-9+\-.]*:)?(?:\/?\/(?:(?:[a-z0-9\-._~!$&'()*+,;=:]|%[0-9a-f]{2})*@)?(?:\[(?:(?:(?:(?:[0-9a-f]{1,4}:){6}|::(?:[0-9a-f]{1,4}:){5}|(?:[0-9a-f]{1,4})?::(?:[0-9a-f]{1,4}:){4}|(?:(?:[0-9a-f]{1,4}:){0,1}[0-9a-f]{1,4})?::(?:[0-9a-f]{1,4}:){3}|(?:(?:[0-9a-f]{1,4}:){0,2}[0-9a-f]{1,4})?::(?:[0-9a-f]{1,4}:){2}|(?:(?:[0-9a-f]{1,4}:){0,3}[0-9a-f]{1,4})?::[0-9a-f]{1,4}:|(?:(?:[0-9a-f]{1,4}:){0,4}[0-9a-f]{1,4})?::)(?:[0-9a-f]{1,4}:[0-9a-f]{1,4}|(?:(?:25[0-5]|2[0-4]\d|[01]?\d\d?)\.){3}(?:25[0-5]|2[0-4]\d|[01]?\d\d?))|(?:(?:[0-9a-f]{1,4}:){0,5}[0-9a-f]{1,4})?::[0-9a-f]{1,4}|(?:(?:[0-9a-f]{1,4}:){0,6}[0-9a-f]{1,4})?::)|[Vv][0-9a-f]+\.[a-z0-9\-._~!$&'()*+,;=:]+)\]|(?:(?:25[0-5]|2[0-4]\d|[01]?\d\d?)\.){3}(?:25[0-5]|2[0-4]\d|[01]?\d\d?)|(?:[a-z0-9\-._~!$&'"()*+,;=]|%[0-9a-f]{2})*)(?::\d*)?(?:\/(?:[a-z0-9\-._~!$&'"()*+,;=:@]|%[0-9a-f]{2})*)*|\/(?:(?:[a-z0-9\-._~!$&'"()*+,;=:@]|%[0-9a-f]{2})+(?:\/(?:[a-z0-9\-._~!$&'"()*+,;=:@]|%[0-9a-f]{2})*)*)?|(?:[a-z0-9\-._~!$&'"()*+,;=:@]|%[0-9a-f]{2})+(?:\/(?:[a-z0-9\-._~!$&'"()*+,;=:@]|%[0-9a-f]{2})*)*)?(?:\?(?:[a-z0-9\-._~!$&'"()*+,;=:@/?]|%[0-9a-f]{2})*)?(?:#(?:[a-z0-9\-._~!$&'"()*+,;=:@/?]|%[0-9a-f]{2})*)?$`

// Patterns for the differential tests: literal prefixes, assertions that look
// before or after the position, empty matches, groups, and non-ASCII.
var largeInputPatterns = []struct{ src, flags string }{
	{"foo", ""}, {"foo\\d+", ""}, {"foo", "i"}, {"ab*c", ""}, {"^a", ""}, {"a$", ""}, {"^b", "m"}, {"\\bfoo\\b", ""},
	{"\\Bo", ""}, {"x*", ""}, {"(?:)", ""}, {"(a)(b)?c", ""}, {"[^a-z ]", ""}, {"é+", ""}, {"😀|x", "u"}, {"😀|x", ""},
	{"f.o", "s"}, {"foo|bar", ""}, {"\\d{3}-\\d{4}", ""}, {"$", ""}, {"$", "m"}, {"(\\w+)@(\\w+)\\.com", ""},
	{"é\\w", "i"}, {"zz+", ""}, {ajvURIReference, "i"},
	// Literal prefixes tried at each occurrence: assertions after the
	// prefix, a prefix at the start and the match at the end, dense
	// occurrences, a pattern reading far past each failing occurrence, and
	// prefixes that ^ keeps out of the skip.
	{"foo\\d", ""}, {"foo\\b", ""}, {"\\.com\\b", ""}, {"o\\n$", ""}, {"a[^a]", ""}, {"ab*c\\B", ""}, {"a.*z", "s"},
	{"a(?:b|c)*d", ""}, {"(foo)\\d", ""}, {"foo^", ""}, {"^foo", ""}, {"(^foo)", ""}, {"^foo\\d", "m"}, {"abc$", "m"},
	{"é😀x", "u"}, {"😀x", "u"}, {"zzzz z", ""},
}

func largeInputs() []string {
	unit := "abcdefghij klmno\n"
	base := strings.Repeat(unit, 5000) // 85 KB
	return []string{
		base, "foo" + base, base + "foo123", base[:40000] + "foo9 x@y.com" + base, strings.Repeat("a", 90000) + "bc",
		base + "é😀", "é" + base + "😀x", strings.Repeat("é", 50000) + "éa", base + "555-1234\n", strings.Repeat("zzzz ", 20000),
		"http://example.com/" + strings.Repeat("a", 100000), strings.Repeat("a", 100000),
		"foo" + base + "foo1", strings.Repeat("a", 100000) + "z", strings.Repeat("abcabcd ", 15000), strings.Repeat("ab", 50000),
	}
}

// oldFindUnicode is how a match of a UTF-16 string ran before inputs were
// large enough to read through an interruptible reader.
func oldFindUnicode(wrapped *regexp.Regexp, s unicodeString, fullUnicode bool) []int {
	if fullUnicode {
		posMap, runes, _, _ := buildPosMap(&lenientUtf16Decoder{utf16Reader: s.utf16Reader()}, s.Length(), 0)
		indexes := wrapped.FindReaderSubmatchIndex(&arrayRuneReader{runes: runes})
		for i, item := range indexes {
			if item >= 0 {
				indexes[i] = posMap[item]
			}
		}
		return indexes
	}
	return wrapped.FindReaderSubmatchIndex(s.utf16RuneReader())
}

func TestGoRegexpLargeInputSameAsWhole(t *testing.T) {
	vm := New().vm
	inputs := largeInputs()
	for _, pt := range largeInputPatterns {
		p, err := compileRegexp(pt.src, pt.flags)
		if err != nil {
			t.Fatal(err)
		}
		if p.regexpWrapper == nil {
			t.Fatalf("/%s/%s: not a Go regexp", pt.src, pt.flags)
		}
		wrapped := (*regexp.Regexp)(p.regexpWrapper)
		name := pt.src
		if len(name) > 20 {
			name = name[:20] + "..."
		}
		for i, in := range inputs {
			if len(in) <= goInterruptibleAbove {
				t.Fatalf("input %d is not large", i)
			}
			// UTF-8 (ASCII strings, and the UTF-8 copy of a global match).
			want := wrapped.FindStringSubmatchIndex(in)
			if got := p.regexpWrapper.findStringSubmatchIndex(vm, in, p.prefixSkip); !slices.Equal(got, want) {
				t.Errorf("/%s/%s on input %d: %v, want %v", name, pt.flags, i, got, want)
			}
			// UTF-16.
			if u, ok := newStringValue(in).(unicodeString); ok {
				want := oldFindUnicode(wrapped, u, p.unicode)
				if got := p.regexpWrapper.findSubmatchIndexUnicode(vm, u, p.unicode).indexes; !slices.Equal(got, want) {
					t.Errorf("/%s/%s on UTF-16 input %d: %v, want %v", name, pt.flags, i, got, want)
				}
			}
			// Every match, one at a time.
			if !p.goStartContext {
				want := wrapped.FindAllStringSubmatchIndex(in, 50)
				got := p.regexpWrapper.findAllIncremental(vm, in, 50, false, p.prefixSkip)
				if len(got) != len(want) {
					t.Errorf("/%s/%s on input %d: %d matches, want %d", name, pt.flags, i, len(got), len(want))
					continue
				}
				for j := range got {
					if !slices.Equal(got[j].indexes, want[j]) {
						t.Errorf("/%s/%s on input %d: match %d is %v, want %v", name, pt.flags, i, j, got[j].indexes, want[j])
						break
					}
				}
			}
		}
	}
}

func TestIndexInterruptible(t *testing.T) {
	vm := New().vm
	const window = 1 << 20
	for _, at := range []int{0, 1, window - 3, window - 2, window - 1, window, window + 1, 2*window - 2, 3*window + 5} {
		s := strings.Repeat("a", at) + "abc" + strings.Repeat("b", 10)
		if got := indexInterruptible(vm, s, "abc"); got != at {
			t.Errorf("abc at %d: found at %d", at, got)
		}
		if got := indexInterruptible(vm, s, "abd"); got != -1 {
			t.Errorf("abd: found at %d", got)
		}
	}
}

func TestInterruptibleReadersReadAll(t *testing.T) {
	vm := New().vm
	s := strings.Repeat("aé😀", 5000)
	rd := &interruptibleStringReader{vm: vm, s: s}
	var b strings.Builder
	n := 0
	for {
		r, size, err := rd.ReadRune()
		if err == io.EOF {
			break
		}
		b.WriteRune(r)
		n += size
	}
	if b.String() != s || n != len(s) {
		t.Fatal("interruptibleStringReader did not read the string")
	}
}

func TestGoRegexpLargeInputResults(t *testing.T) {
	// Above the threshold, single matches read the input through an
	// interruptible reader. The results are those the patterns have on the
	// small input they are made of.
	const script = `
	var pad = "abcdefghij klmno\n".repeat(10000); // 170 KB
	var upad = "é" + pad;
	var bad = [];
	function check(name, got, want) {
		got = JSON.stringify(got); want = JSON.stringify(want);
		if (got !== want) bad.push(name + ": " + got.slice(0, 60) + " != " + want.slice(0, 60));
	}
	[[pad, 0], [upad, 1]].forEach(function(p) {
		var s = p[0] + "foo123 x@y.com 555-1234 bar";
		var at = p[0].length;
		check("exec", /foo(\d+)/.exec(s).slice(), ["foo123", "123"]);
		check("exec index", /foo(\d+)/.exec(s).index, at);
		check("exec u", /foo(\d+)/u.exec(s).index, at);
		check("test", /\d{3}-\d{4}/.test(s), true);
		check("test none", /\d{5}/.test(s), false);
		check("search", s.search(/bar$/), s.length - 3);
		check("search m", s.search(/^abc/m), p[1] ? 18 : 0);
		check("match", s.match(/(\w+)@(\w+)\.com/).slice(), ["x@y.com", "x", "y"]);
		check("replace", s.replace(/foo\d+/, "X").slice(at), "X x@y.com 555-1234 bar");
		check("replace fn", s.replace(/(\d+)-(\d+)/, function(m, a, b) { return b + a; }).slice(-11), "1234555 bar");
		check("split", s.split(/\s+/, 3).length, 3);
		check("split far", (p[0] + "|x|y").split(/\|/, 2).length, 2);
		check("word boundary", /\bfoo\b/.test(s), false);
		check("word boundary 2", /\bx@/.exec(s).index, at + 7);
		check("anchored", /^foo/.test(s), false);
		check("end", /bar$/.exec(s).index, s.length - 3);
		check("ignore case", /FOO\d/i.exec(s).index, at);
		check("empty", /x*/.exec(s).index, 0);
		// lastIndex and sticky (regexp2).
		var g = /\d+/g;
		g.lastIndex = at;
		check("global from lastIndex", g.exec(s).index, at + 3);
		var y = /foo/y;
		y.lastIndex = at;
		check("sticky", y.test(s), true);
		y.lastIndex = at + 1;
		check("sticky miss", y.test(s), false);
		// Lookarounds (regexp2).
		check("lookahead", /\d+(?=-)/.exec(s).slice(), ["555"]);
		check("lookbehind", /(?<=x@)\w/.exec(s).index, at + 9);
		// Global matches whose next match is far away.
		check("global", s.match(/foo|bar/g), ["foo", "bar"]);
		check("global u", s.match(/foo|bar/gu), ["foo", "bar"]);
		check("replace global", s.replace(/\d{4}/g, "#").slice(-9), "555-# bar");
	});
	bad.join("; ");
	`
	testScript(script, asciiString(""), t)
}

func TestGoRegexpLargeSingleMatchInterrupt(t *testing.T) {
	// A single match with Go's regexp over a large input runs for seconds;
	// it must stop soon after Runtime.Interrupt. uri is the ajv-formats
	// pattern, and its match reads the whole input. The inputs are made
	// before the run, so that the interrupt lands in the match.
	globals := map[string]interface{}{
		"uri":  ajvURIReference,
		"big":  strings.Repeat("a", 1e8),
		"ubig": strings.Repeat("a", 5e7) + "é",
		"ab":   strings.Repeat("ab", 5e7),
		"xbig": "x" + strings.Repeat("a", 1e8),
	}
	for _, tc := range []struct{ name, script string }{
		{"uri test", `new RegExp(uri, "i").test(big)`},
		{"uri exec unicode", `new RegExp(uri, "i").exec(ubig)`},
		{"uri exec u", `new RegExp(uri, "iu").exec(ubig)`},
		{"search", `big.search(/a[^a]/)`},
		{"match", `big.match(/a[^a](x)?/)`},
		{"replace", `big.replace(/a\d/, "")`},
		{"split limit", `big.split(/b|c\d/, 2)`},
		{"literal prefix", `ab.match(/a[^b]/)`},
		{"global, next match far", `xbig.match(/x|a[^a]/g)`},
	} {
		t.Run(tc.name, func(t *testing.T) { runInterrupted(t, tc.script, globals) })
	}
}

func TestGoRegexpPrefixSkip(t *testing.T) {
	// A pattern with a literal prefix that does not begin with ^ is tried at
	// each occurrence of the prefix in a large input.
	for _, tc := range []struct {
		src, flags string
		skip       bool
	}{
		{"foo\\d", "", true}, {"foo\\b", "", true}, {"\\.com\\b", "", true}, {"foo", "", true}, {"(foo)\\d", "", true},
		{"foo\\d", "m", true}, {"foo\\d", "s", true}, {"^foo", "", false}, {"^foo\\d", "", false}, {"(^foo)", "", false},
		{"(?:^foo|fob)", "", false}, {"^foo", "m", false}, {"\\bfoo", "", false}, {"foo", "i", false}, {"foo|bar", "", false},
		{"\\d+", "", false}, {"(?:)foo", "", true},
		// A ^ anywhere, hidden in a repeat or after the prefix, refuses the
		// skip: a try at an occurrence takes it for the beginning.
		{"foo^", "", false}, {"(?:^foo){1}", "", false}, {"(?:^){1}foo", "", false}, {"(?:^){1,1}foo", "", false},
		{"(?:(?:^){1})foo", "", false}, {"(?:^f){1}oo", "", false}, {"foo(?:^|x)", "", false}, {"foo$", "", true},
		{"foo\\n^bar", "m", false}, {"(?:^){0}foo", "", true},
	} {
		p, err := compileRegexp(tc.src, tc.flags)
		if err != nil {
			t.Fatal(err)
		}
		if (p.prefixSkip != nil) != tc.skip {
			t.Errorf("/%s/%s: prefix skip %v", tc.src, tc.flags, p.prefixSkip != nil)
		}
		if p.prefixSkip != nil && p.prefixSkip.get(New().vm) == nil {
			t.Errorf("/%s/%s: no anchored regexp", tc.src, tc.flags)
		}
	}
	// The regexp tried at each occurrence is anchored there.
	vm := New().vm
	if p, err := compileRegexp("foo\\d", ""); err != nil || !p.prefixSkip.get(vm).MatchString("foo1x") || p.prefixSkip.get(vm).MatchString("xfoo1") {
		t.Errorf("anchored regexp: %v", err)
	}
	// A failing try reads little, and the tries stop once they have read
	// the input over: neither dense occurrences nor a pattern that reads to
	// the end at each occurrence make the search read the input over for
	// each occurrence (the latter would take minutes).
	for _, tc := range []struct {
		src, flags, input string
		at                int
	}{
		{"foo\\d", "", strings.Repeat("foo ", 50000) + "foo1", 200000},
		{"a.*z", "s", strings.Repeat("ab", 100000) + "y", -1},
		{"ab*c\\d", "", strings.Repeat("abbbbbbbbb", 20000) + "abc1", 200000},
	} {
		p, err := compileRegexp(tc.src, tc.flags)
		if err != nil || p.prefixSkip == nil {
			t.Fatalf("/%s/%s: %v", tc.src, tc.flags, err)
		}
		start := time.Now()
		for i := 0; i < 3; i++ {
			m := p.regexpWrapper.findStringSubmatchIndex(vm, tc.input, p.prefixSkip)
			if tc.at < 0 && m != nil || tc.at >= 0 && (len(m) != 2 || m[0] != tc.at) {
				t.Fatalf("/%s/%s: match %v", tc.src, tc.flags, m)
			}
		}
		if d := time.Since(start); d > 10*time.Second {
			t.Errorf("/%s/%s: 3 searches took %v", tc.src, tc.flags, d)
		}
	}
}

func TestGoRegexpPrefixSkipBeginText(t *testing.T) {
	// Patterns with ^ in every form, including ones LiteralPrefix gives a
	// prefix for, match a large input as the whole-input search does.
	pats := []string{"^foo", "(?:^)foo", "(^)foo", "(?:^|^)foo", "(?:^){1}foo", "(?:^)+foo", "(?:^)*foo", "(?:^)?foo", "(?:(?:^))foo", "(?:^foo)", "((?:^)foo)",
		"(?:^)(?:)foo", "(?:)^foo", "(?:(?:)|(?:))^foo", "(?:^){2}foo", "(?:^){1,}foo", "(?:^){0,1}foo", "(?:^\\b)foo", "(?:\\b^)foo", "(?:^)(?:^)foo", "(?:^$|^)foo",
		"(?:^)?(?:^)foo", "(?:(^))foo", "(?<n>^)foo", "(?:^){1}(?:)foo", "(?:(?:^)+)foo", "(?:^)foo|(?:^)foo", "^foo|^foo", "(?:^fo)o", "(?:^f)(?:oo)", "a{0}^foo", "(?:a{0})^foo",
		"x{0}(?:^)foo", "(?:^){1,1}foo", "(?:(?:^){1})foo", "(?:^|^){1}foo", "(?:^foo){1}", "(?:^f){1}oo", "(?:^){1}(?:^){1}foo", "(?:x{0}|x{0})^foo", "(?:x?){0}^foo",
		"[]{0}^foo", "(?:a*){0}^foo", "(^){1}foo", "(?:x{0}^)foo", "foo(?:^){1}", "foo(?:^|x)", "(?:^){0}foo"}
	inputs := []string{strings.Repeat("x", 70000) + "foo" + strings.Repeat("y", 100), "foo" + strings.Repeat("x", 70000) + "foo", strings.Repeat("x\n", 35000) + "foo"}
	vm := New().vm
	for _, flags := range []string{"", "i", "s", "m", "u", "g"} {
		for _, pt := range pats {
			p, err := compileRegexp(pt, flags)
			if err != nil || p.regexpWrapper == nil {
				continue
			}
			wrapped := (*regexp.Regexp)(p.regexpWrapper)
			for _, in := range inputs {
				want := wrapped.FindStringSubmatchIndex(in)
				if got := p.regexpWrapper.findStringSubmatchIndex(vm, in, p.prefixSkip); !slices.Equal(got, want) {
					prefix, _ := wrapped.LiteralPrefix()
					t.Errorf("/%s/%s prefix %q skip %v: got %v, want %v", pt, flags, prefix, p.prefixSkip != nil, got, want)
				}
			}
		}
	}
}

func TestGoRegexpPrefixTries(t *testing.T) {
	// The tries at the prefix's occurrences: each occurrence is tried once,
	// a try reads only what the pattern needs, and the tries give up when
	// the occurrences are dense or a try reads past its window.
	vm := New().vm
	compile := func(src string) (*regexp.Regexp, string) {
		p, err := compileRegexp(src, "")
		if err != nil || p.prefixSkip == nil {
			t.Fatalf("/%s/: %v", src, err)
		}
		prefix, _ := (*regexp.Regexp)(p.regexpWrapper).LiteralPrefix()
		return p.prefixSkip.get(vm), prefix
	}
	count := func(s string, prefix string) int {
		return strings.Count(s, prefix)
	}
	// Sparse occurrences: every one tried, then the match.
	anchored, prefix := compile("foo\\d")
	s := strings.Repeat("foo             ", 1000) + "foo1"
	m, next, done := prefixTries(vm, anchored, s, prefix, 0)
	if !done || !slices.Equal(m, []int{len(s) - 4, len(s)}) || next != len(s)-4 {
		t.Errorf("sparse: %v %d %v", m, next, done)
	}
	// No match: every occurrence tried.
	s = strings.Repeat("foo             ", 1000)
	if m, next, done = prefixTries(vm, anchored, s, prefix, 0); !done || m != nil || next != len(s) {
		t.Errorf("no match: %v %d %v", m, next, done)
	}
	// Overlapping occurrences are all tried.
	anchored, prefix = compile("aa\\d")
	s = strings.Repeat("aaa                 ", 100) + "aa1"
	if m, _, done = prefixTries(vm, anchored, s, prefix, 0); !done || !slices.Equal(m, []int{len(s) - 3, len(s)}) {
		t.Errorf("overlapping: %v %v", m, done)
	}
	// Dense occurrences: the tries stop after tryDenseAfter of them, at an
	// occurrence not tried.
	anchored, prefix = compile("a\\d")
	s = strings.Repeat("a", 100000) + "a1"
	if m, next, done = prefixTries(vm, anchored, s, prefix, 0); done || m != nil || next != tryDenseAfter {
		t.Errorf("dense: %v %d %v", m, next, done)
	}
	if n := count(s[:tryDenseAfter], "a"); n != tryDenseAfter {
		t.Errorf("dense: %d occurrences", n)
	}
	// A try reading past its window is dropped, whatever it found.
	anchored, prefix = compile("a.*z")
	s = strings.Repeat("a", 100000) + "z"
	if m, next, done = prefixTries(vm, anchored, s, prefix, 0); done || m != nil || next != 0 {
		t.Errorf("far reading: %v %d %v", m, next, done)
	}
	s = "a" + strings.Repeat("b", 100000) + "z"
	if m, next, done = prefixTries(vm, anchored, s, prefix, 0); done || m != nil || next != 0 {
		t.Errorf("far reading match: %v %d %v", m, next, done)
	}
	// The tries read at most the input once over: a pattern that reads
	// four occurrences ahead at each one gives up a quarter of the way.
	anchored, prefix = compile("a[ab]{200}c")
	s = strings.Repeat("a"+strings.Repeat("b", 49), 8000) + "z"
	m, next, done = prefixTries(vm, anchored, s, prefix, 0)
	if done || m != nil || next < len(s)/5 || next > len(s)/3 {
		t.Errorf("budget: %v %d %v", m, next, done)
	}
	// A reader bounded by end reports reading past it.
	rd := &interruptibleStringReader{vm: vm, s: "abcdef", end: 3}
	for i := 0; i < 3; i++ {
		if _, _, err := rd.ReadRune(); err != nil || rd.truncated {
			t.Fatal("truncated early")
		}
	}
	if _, _, err := rd.ReadRune(); err != io.EOF || !rd.truncated {
		t.Fatal("not truncated")
	}
	rd = &interruptibleStringReader{vm: vm, s: "abc", end: 3}
	for i := 0; i < 4; i++ {
		rd.ReadRune()
	}
	if rd.truncated {
		t.Fatal("truncated at the end of the input")
	}
}
