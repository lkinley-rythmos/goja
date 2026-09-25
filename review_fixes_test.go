package goja

import (
	"testing"
)

// Fixes from the review of 4b740fd..a23f718.

func TestListFromArrayLikeChargesItsSize(t *testing.T) {
	// A wrapper forwarding its arguments object is called a great many
	// times; the lists built for it are a few values each and must be
	// charged as such (a flat 16 KiB per call exhausted a 512 MB limit
	// after about 40,000 calls).
	const calls = 10000
	for _, call := range []string{
		`w(i, 1)`, `x(i, 1)`,
		`Array.prototype.toSorted.call({length: 2, 0: 2, 1: 1})`,
		`Array.from({length: 2})`,
	} {
		vm := New()
		if _, err := vm.RunString(`
		function f(a, b) { return a + b }
		function w() { return f.apply(this, arguments) }
		function x() { return Reflect.apply(f, this, arguments) }`); err != nil {
			t.Fatal(err)
		}
		vm.SetAllocationLimit(0)
		if _, err := vm.RunString(`for (var i = 0; i < ` + itoa(calls) + `; i++) ` + call); err != nil {
			t.Fatal(err)
		}
		if per := vm.AllocatedBytes() / calls; per > 1024 {
			t.Errorf("%s: %d bytes counted per call", call, per)
		}
	}
}

func TestNativeDepth_GoFunctionThatRecovers(t *testing.T) {
	// Host code called from JS may recover a goja panic itself and return
	// normally; the levels that panic unwound must not stay counted.
	vm := New()
	vm.Set("safeString", func(call FunctionCall) (ret Value) {
		defer func() {
			if recover() != nil {
				ret = stringEmpty
			}
		}()
		return call.Argument(0).ToString()
	})
	vm.Set("SafeCtor", func(call ConstructorCall) (ret *Object) {
		defer func() {
			if recover() != nil {
				ret = call.This
			}
		}()
		call.Argument(0).ToString()
		return call.This
	})
	v, err := vm.RunString(`
	var N = ` + itoa(deepDepth) + `;
	` + nestedArray + `
	// Each call overflows a nested array's toString and recovers in Go;
	// the getter one leaks a few prototype levels per call.
	var g = Object.create({get toString() { throw new Error("x") }});
	for (var i = 0; i < 6; i++) g = Object.create(g);
	for (var j = 0; j < 3; j++) { safeString(a); new SafeCtor(a); }
	for (var j = 0; j < 12000; j++) { safeString(g); new SafeCtor(g); [0].map(function() { safeString(g) }); }
	String([1, [2, [3]]]);
	`)
	if err != nil {
		t.Fatal(err)
	}
	if v.String() != "1,2,3" {
		t.Fatal(v)
	}
	if d := nativeDepthOf(vm); d != 0 {
		t.Fatalf("nativeDepth after the run: %d", d)
	}
}

func TestNativeDepth_BoundInstanceof(t *testing.T) {
	// With no @@hasInstance of their own to call (which would count), bound
	// functions test instanceof by recursing on their target in Go.
	expectStackOverflow(t, deepScript(`var f = function() {}; for (var i = 0; i < N; i++) { f = f.bind(null); Object.defineProperty(f, "name", {value: 0}); Object.defineProperty(f, Symbol.hasInstance, {value: undefined}) }`, `({}) instanceof f`))
}

func TestGlobalMatch_LargeASCIIInputWithLookbehindAssertions(t *testing.T) {
	// ASCII input above the threshold goes to Go's regexp; patterns that
	// look before their position must not be resumed mid-input.
	const script = `
	var s = "ab cd ".repeat(20000);
	[s.match(/\b\w/g).length, s.match(/\B\w/g).length, ("x\n".repeat(40000)).match(/^x/gm).length,
		s.match(/^a/g).length, s.replace(/\b/g, "|").length].join();
	`
	testScript(script, asciiString("40000,40000,40000,1,200000"), t)
}

func TestJSONParseLoneSurrogateInError(t *testing.T) {
	// Node 18 shows the code unit itself. (Go strings cannot hold a lone
	// surrogate, so the sources are built in JS.)
	vm := New()
	for _, tc := range []struct {
		src  string
		unit uint16
		pos  string
	}{
		{`String.fromCharCode(0xd800)`, 0xd800, "0"},
		{`"[" + String.fromCharCode(0xdc00) + "]"`, 0xdc00, "1"},
		{`"é"`, 0xe9, "0"},
	} {
		v, err := vm.RunString(`var r; try { JSON.parse(` + tc.src + `) } catch (e) { r = e.message } r`)
		if err != nil {
			t.Fatal(err)
		}
		m := v.(String)
		if m.Length() != 40 || m.CharAt(17) != tc.unit || m.Substring(18, m.Length()).String() != " in JSON at position "+tc.pos {
			t.Errorf("%s: %q", tc.src, m.String())
		}
	}
}
