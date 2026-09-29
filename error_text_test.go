package goja

import (
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// v8ErrorCases runs each script of testdata/v8_error_messages.json in a
// new runtime and compares the error it throws, as "Constructor: message",
// with the one Node.js 18.20.3 throws, recorded in the file, each script
// run in a new context. The scripts cover reading and setting properties
// of null and undefined, calling what is not a function or a constructor,
// iterating what is not iterable, destructuring null and undefined, in and
// instanceof, the callbacks of the built-ins, and bindings read before
// their declaration.
func TestV8ErrorMessages(t *testing.T) {
	data, err := os.ReadFile("testdata/v8_error_messages.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases [][2]string
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		src, _ := json.Marshal(c[0])
		vm := New()
		v, err := vm.RunString(`(function(src) {
			try { (0, eval)(src); return "no error" }
			catch (e) { return (e && e.constructor && e.constructor.name) + ": " + (e && e.message) }
		})(` + string(src) + `)`)
		if err != nil {
			t.Errorf("%s: %v", c[0], err)
			continue
		}
		if got := v.String(); got != c[1] {
			t.Errorf("%s\n got: %s\nwant: %s", c[0], got, c[1])
		}
	}
}

// TestV8ErrorMessages_NonCallableNext: an iterator whose next is not a
// function throws a TypeError from every consumer, instead of calling a nil
// Go function.
func TestV8ErrorMessages_NonCallableNext(t *testing.T) {
	for _, src := range []string{
		`for (var x of {[Symbol.iterator]() { return {} }}) {}`,
		`var [a] = {[Symbol.iterator]() { return {next: 5} }}`,
		`[...{[Symbol.iterator]() { return {} }}]`,
		`function* g() { yield* {[Symbol.iterator]() { return {} }} } g().next()`,
		`new Set({[Symbol.iterator]() { return {} }})`,
	} {
		vm := New()
		_, err := vm.RunString(src)
		ex, ok := err.(*Exception)
		if !ok || !strings.HasPrefix(ex.Error(), "TypeError: ") || !strings.Contains(ex.Error(), "is not a function") {
			t.Errorf("%s: %v", src, err)
		}
	}
}

// TestCallSitePrinter pins V8's printing of callees, from their source.
func TestCallSitePrinter(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`o.f`, `o.f`},
		{`o['a b']`, `o.a b`},
		{`o[0]`, `o[0]`},
		{`o[k]`, `o[k]`},
		{`o[1.50]`, `o[1.5]`},
		{`o[-1]`, `o[-1]`},
		{`o.x?.y`, `o.x?.y`},
		{`o.a?.[k]`, `o.a?.[k]`},
		{`o.a?.["k"]`, `o.a?.k`},
		{`g().h`, `g(...).h`},
		{`this.f.g`, `this.f.g`},
		{`(function(){})()`, `(intermediate value)(...)`},
		{`new Object().x`, `(intermediate value).x`},
		{`a + b + a`, `(a + b + a)`},
		{`a, b`, `(a , b)`},
		{`typeof x`, `(typeof x)`},
		{`x++`, `(x++)`},
		{`x ? 1 : 2`, `(intermediate value)(intermediate value)(intermediate value)`},
		{`x = 5`, `(intermediate value)(intermediate value)`},
		{`{a: 1, b: 2}`, `{(intermediate value)(intermediate value)}`},
		{`[o.a, 2]`, `[o.a,2]`},
		{`"s"`, `"s"`},
		{`/x/g`, `/x/g`},
		{`1n`, `(intermediate value)`},
		{`super.m`, `(intermediate value).m`},
		{`o.f.g(`, ``},
	} {
		var got string
		if e := parseCallSite(tc.src); e != nil {
			got = printCallSite(e)
		}
		if got != tc.want {
			t.Errorf("%s: %q, want %q", tc.src, got, tc.want)
		}
	}
}

// TestCallSiteParseBounded: naming a callee costs at most one parse of at
// most maxCallSiteSource bytes per throw, whatever the callee's source and
// however many parentheses it is written in. (It took 20 throws 24 s and
// 7.4 GB with a 1 MB function in the callee, and 16.5 s with 10 MB.)
func TestCallSiteParseBounded(t *testing.T) {
	throws := func(expr string) string {
		return `var msg; for (var i = 0; i < 20; i++) { try { ` + expr + ` } catch (e) { msg = e.message } } msg`
	}
	sizes := []int{1 << 20, 10 << 20}
	if testing.Short() || raceDetectorEnabled {
		sizes = sizes[:1]
	}
	for _, size := range sizes {
		body := strings.Repeat("var super_ = 1; ", size/16)
		for _, tc := range []struct{ name, src, want string }{
			{"parenthesized super", throws(`((((((((0 || function(){` + body + `})))))))).x()`), "undefined is not a function"},
			{"sequence", throws(`(function(){` + body + `}, 5)()`), "number 5 is not a function"},
			{"for-of", `for (var x of (0, function(){` + body + `})) {}`, "function is not iterable"},
		} {
			prg := MustCompile("", tc.src, false)
			vm := New()
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			start := time.Now()
			v, err := vm.RunProgram(prg)
			elapsed := time.Since(start)
			runtime.ReadMemStats(&after)
			if tc.name == "for-of" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("%d %s: %v", size, tc.name, err)
				}
			} else if err != nil || v.String() != tc.want {
				t.Fatalf("%d %s: %v, %v", size, tc.name, v, err)
			}
			if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 4<<20 || elapsed > 2*time.Second {
				t.Fatalf("%d %s: %d bytes and %v for the throws", size, tc.name, alloc, elapsed)
			}
		}
	}

	// Just under the bound, a callee is still named, parsed once a throw.
	body := strings.Repeat("var q = 1; ", 1400)
	vm := New()
	prg := MustCompile("", throws(`(((0 || function(){`+body+`}))).x()`), false)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	v, err := vm.RunProgram(prg)
	runtime.ReadMemStats(&after)
	if err != nil || v.String() != "(0 || (intermediate value)).x is not a function" {
		t.Fatalf("%v, %v", v, err)
	}
	if alloc := (after.TotalAlloc - before.TotalAlloc) / 20; alloc > 2<<20 {
		t.Fatalf("%d bytes a throw", alloc)
	}
}

// TestCallSiteSuper: a callee on super is printed as V8 prints it, with
// super as "(intermediate value)" (recorded from Node.js 18.20.3).
func TestCallSiteSuper(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`class A { m() { return super.nope() } } new A().m()`, "(intermediate value).nope is not a function"},
		{`class A { static m() { return super['no' + 'pe']() } } A.m()`, `(intermediate value)[("no" + "pe")] is not a function`},
		{`class B { x() { return 1 } } class A extends B { m() { return super.x.y() } } new A().m()`, "(intermediate value).x.y is not a function"},
		{`var o = { m() { return (super.nope)() } }; o.m()`, "(intermediate value).nope is not a function"},
	} {
		vm := New()
		_, err := vm.RunString(tc.src)
		ex, ok := err.(*Exception)
		if !ok || ex.Value().(*Object).Get("message").String() != tc.want {
			t.Errorf("%s: %v, want %s", tc.src, err, tc.want)
		}
	}
}

// TestCallSiteParentheses: callees whose first or last operand is
// parenthesized, and parentheses inside strings, templates and regular
// expressions (recorded from Node.js 18.20.3).
func TestCallSiteParentheses(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"var a = 1, b = 2; (a + (b))()", "(a + b) is not a function"},
		{"var a = 1, b = 2; ((a) + (b))()", "(a + b) is not a function"},
		{"var a = 1; (a, (a))()", "(a , a) is not a function"},
		{"var o = {}; ((o).f)()", "o.f is not a function"},
		{"var o = {}; (o[(1)])()", "o[1] is not a function"},
		{"var o = {f: {}}; (o.f[')'])()", "o.f.) is not a function"},
		{"var o = {}; (o[`(${1}`])()", "o[1] is not a function"},
		{"var o = {}; (o[/\\(/.source])()", "o[/\\(/.source] is not a function"},
	} {
		vm := New()
		_, err := vm.RunString(tc.src)
		ex, ok := err.(*Exception)
		if !ok || ex.Value().(*Object).Get("message").String() != tc.want {
			t.Errorf("%s: %v, want %s", tc.src, err, tc.want)
		}
	}
}
