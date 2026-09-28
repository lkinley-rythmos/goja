package goja

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
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
