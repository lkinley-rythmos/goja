package goja

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// A named backreference was resolved by comparing its name with every
// group's: 14,000 groups and 94,000 references to the last took 11.3 s to
// compile with the u flag, none of it interruptible. (Node refuses this
// pattern with "Stack overflow"; Goja compiles it.)
func TestRegexpNamedReferencesLinear(t *testing.T) {
	var b strings.Builder
	const groups, refs = 14000, 94000
	for i := 0; i < groups; i++ {
		b.WriteString("(?<g" + strconv.Itoa(i) + ">)")
	}
	last := `\k<g` + strconv.Itoa(groups-1) + `>`
	b.WriteString(strings.Repeat(last, refs))
	pattern := b.String()
	for _, flags := range []string{"u", ""} {
		r := New()
		r.Set("p", pattern)
		start := time.Now()
		_, err := r.RunString(`new RegExp(p, "` + flags + `")`)
		took := time.Since(start)
		if err != nil {
			t.Fatalf("flags %q: %v", flags, err)
		}
		if took > time.Second {
			t.Fatalf("flags %q: took %v", flags, took)
		}
	}
	// What a reference matches is unchanged.
	testScript(`
	var re = /(?<a>x)(?<b>y)\k<b>\k<a>/u;
	var m = re.exec("xyyx");
	m !== null && m.groups.a === "x" && m.groups.b === "y" && /(?<a>x)\k<a>/.test("xx") && !/(?<a>x)\k<a>/.test("xy")
	`, valueTrue, t)
	// Names are still checked: one that no group has is refused, after the
	// whole pattern, and a duplicate name too, with Node's messages.
	r := New()
	for _, c := range []struct{ src, msg string }{
		{`new RegExp("(?<a>x)\\k<b>", "u")`, `/(?<a>x)\k<b>/: Invalid named capture referenced`},
		{`new RegExp("(?<a>x)(?<a>y)", "u")`, `/(?<a>x)(?<a>y)/: Duplicate capture group name`},
		{`new RegExp("(?<a>x)\\k<b>")`, `/(?<a>x)\k<b>/: Invalid named capture referenced`},
	} {
		_, err := r.RunString(c.src)
		if err == nil || !strings.HasPrefix(err.Error(), "SyntaxError: Invalid regular expression: "+c.msg+" at ") {
			t.Fatalf("%s: got %v", c.src, err)
		}
	}
}

// Collecting the group names for a backreference looked for each name's
// end from where it starts, so many unterminated names before one '>' made
// the scan quadratic: 80,000 took 2 minutes. Node refuses these patterns
// at once, with the same message.
func TestRegexpCaptureScanLinear(t *testing.T) {
	names := strings.Repeat("(?<a", 80000) + ">"
	for _, p := range []string{`\1` + names, `\k<a>` + names, `(?<b>)\k<b>` + names} {
		for _, flags := range []string{"u", ""} {
			r := New()
			r.Set("p", p)
			start := time.Now()
			_, err := r.RunString(`new RegExp(p, "` + flags + `")`)
			if took := time.Since(start); took > time.Second {
				t.Fatalf("%.12q %q: took %v", p, flags, took)
			}
			if err == nil || !strings.HasSuffix(strings.SplitN(err.Error(), " at ", 2)[0], "/: Invalid capture group name") {
				t.Fatalf("%.12q %q: got %v", p, flags, err)
			}
		}
	}
	// Names made of escapes and non-ASCII characters are still collected.
	testScript(`/\k<\u{61}b>(?<ab>x)/u.source.length > 0 && /(?<éb>y)\k<éb>/u.test("yy") && /\k<z>(?<z>q)/.test("q")`, valueTrue, t)
}
