package goja

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// Native code must not recurse in Go without a bound: Go aborts the whole
// process with "fatal error: stack overflow" when a goroutine's stack passes
// its limit, which no recover() can catch. Each test below drives one such
// recursion from a script, past maxNativeDepth, and expects the catchable
// RangeError V8 throws. On the unfixed runtime the deep cases either abort the
// test binary or run without throwing.

// expectStackOverflow runs script, which must evaluate to the caught error's
// "name: message", and checks it is the stack overflow RangeError.
func expectStackOverflow(t *testing.T, script string) {
	t.Helper()
	vm := New()
	v, err := vm.RunString(script)
	if err != nil {
		t.Fatalf("uncaught: %v", err)
	}
	if got, want := v.String(), "RangeError: "+errMsgStackOverflow; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if d := nativeDepthOf(vm); d != 0 {
		t.Fatalf("nativeDepth after the run: %d", d)
	}
}

const deepDepth = 2 * maxNativeDepth

func deepScript(build, use string) string {
	return `
	var N = ` + itoa(deepDepth) + `;
	` + build + `
	var res;
	try {
		` + use + `;
		res = "no error";
	} catch (e) {
		res = e.name + ": " + e.message;
	}
	res;
	`
}

func itoa(i int) string {
	return valueInt(i).String()
}

const nestedArray = `var a = []; for (var i = 0; i < N; i++) a = [a];`
const nestedObject = `var a = {}; for (var i = 0; i < N; i++) a = {a: a};`

func TestNativeDepth_JSONStringify(t *testing.T) {
	t.Run("array", func(t *testing.T) { expectStackOverflow(t, deepScript(nestedArray, `JSON.stringify(a)`)) })
	t.Run("object", func(t *testing.T) { expectStackOverflow(t, deepScript(nestedObject, `JSON.stringify(a)`)) })
	t.Run("gap", func(t *testing.T) { expectStackOverflow(t, deepScript(nestedObject, `JSON.stringify(a, null, 2)`)) })
	t.Run("replacer", func(t *testing.T) {
		expectStackOverflow(t, deepScript(nestedObject, `JSON.stringify(a, function(k, v) { return v })`))
	})
	t.Run("MarshalJSON", func(t *testing.T) {
		vm := New()
		v, err := vm.RunString(`var N = ` + itoa(deepDepth) + `; ` + nestedObject + ` a`)
		if err != nil {
			t.Fatal(err)
		}
		_, err = json.Marshal(v)
		var ex *Exception
		if !errors.As(err, &ex) || !strings.Contains(ex.Error(), "RangeError: "+errMsgStackOverflow) {
			t.Fatalf("err: %v", err)
		}
	})
}

func TestNativeDepth_JSONStringifyBelowTheBound(t *testing.T) {
	const script = `
	var N = 5000;
	` + nestedObject + `
	var s = JSON.stringify(a);
	s.length === 6 * N + 2 && JSON.stringify([a, a]).length === 2 * s.length + 3;
	`
	testScript(script, valueTrue, t)
}

func TestNativeDepth_JSONStringifyStillDetectsCycles(t *testing.T) {
	const script = `
	var N = 100;
	var root = {}, a = root;
	for (var i = 0; i < N; i++) a = a.next = {};
	a.back = root;
	var res;
	try { JSON.stringify(root); res = "no error" } catch (e) { res = e.name }
	// A repeated but acyclic reference is not a cycle, at any depth.
	var shared = {x: 1}, b = {s: shared};
	for (var i = 0; i < N; i++) b = {s: shared, n: b};
	res + " " + JSON.stringify(b).length;
	`
	testScript(script, asciiString("TypeError 1813"), t)
}

func TestNativeDepth_JSONParseReviver(t *testing.T) {
	// maxNativeDepth levels of JSON are within reach of every encoding/json.
	src := strings.Repeat("[", maxNativeDepth) + strings.Repeat("]", maxNativeDepth)
	vm := New()
	vm.Set("src", src)
	v, err := vm.RunString(`
	var res;
	try {
		JSON.parse(src, function(k, v) { return v });
		res = "no error";
	} catch (e) {
		res = e.name + ": " + e.message;
	}
	res`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := v.String(), "RangeError: "+errMsgStackOverflow; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// jsonDecoderLimitsDepth reports whether this toolchain's encoding/json
// Decoder refuses deep nesting itself (encoding/json v2 does, at 10000).
func jsonDecoderLimitsDepth() bool {
	d := json.NewDecoder(strings.NewReader(strings.Repeat("[", 20000)))
	for {
		if _, err := d.Token(); err != nil {
			return !strings.Contains(err.Error(), "EOF")
		}
	}
}

// TestNativeDepth_JSONParseDeepInput parses 10^7 levels of nesting. Parsing
// used to recurse once per level and aborted the process with a Go stack
// overflow; it is iterative now, like V8's, so the depth is limited only by
// memory (Node 18 parses this input too).
func TestNativeDepth_JSONParseDeepInput(t *testing.T) {
	if testing.Short() {
		t.Skip("parses 10^7 levels")
	}
	const n = 10000000
	vm := New()
	vm.Set("src", strings.Repeat("[", n)+strings.Repeat("]", n))
	v, err := vm.RunString(`
	var res;
	try {
		var v = JSON.parse(src), d = 0;
		while (v.length === 1) { v = v[0]; d++; }
		res = String(d + 1);
	} catch (e) {
		res = e.name;
	}
	res`)
	if err != nil {
		t.Fatal(err)
	}
	want := "10000000"
	if jsonDecoderLimitsDepth() {
		want = "SyntaxError"
	}
	if got := v.String(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestNativeDepth_JSONParseStillParses(t *testing.T) {
	const script = `
	var v = JSON.parse('{"a": [1, "x", true, null, {"b": [], "c": {}}], "d": -1.5e3, "e": "\\u00e9"}');
	JSON.stringify(v) === '{"a":[1,"x",true,null,{"b":[],"c":{}}],"d":-1500,"e":"é"}' &&
		JSON.parse("[]").length === 0 && JSON.parse(" 7 ") === 7 &&
		JSON.stringify(JSON.parse('[[[1]],[[2],[3]]]')) === "[[[1]],[[2],[3]]]";
	`
	testScript(script, valueTrue, t)
	for _, src := range []string{`[1,]`, `{"a":1,}`, `{"a" 1}`, `{1:2}`, `[`, `{"a":`, `]`, `[1}`, `{"a":1]`} {
		vm := New()
		vm.Set("src", src)
		v, err := vm.RunString(`var r; try { JSON.parse(src); r = "parsed" } catch (e) { r = e.name } r`)
		if err != nil {
			t.Fatal(err)
		}
		if v.String() != "SyntaxError" {
			t.Errorf("%s: %s", src, v)
		}
	}
}

func TestNativeDepth_ArrayToString(t *testing.T) {
	t.Run("String", func(t *testing.T) { expectStackOverflow(t, deepScript(nestedArray, `String(a)`)) })
	t.Run("join", func(t *testing.T) { expectStackOverflow(t, deepScript(nestedArray, `a.join()`)) })
	t.Run("toLocaleString", func(t *testing.T) { expectStackOverflow(t, deepScript(nestedArray, `a.toLocaleString()`)) })
	t.Run("sort", func(t *testing.T) { expectStackOverflow(t, deepScript(nestedArray, `[a, a].sort()`)) })
}

func TestNativeDepth_ArrayFlat(t *testing.T) {
	expectStackOverflow(t, deepScript(`var a = [1]; for (var i = 0; i < N; i++) a = [a];`, `a.flat(Infinity)`))
}

func TestNativeDepth_Chains(t *testing.T) {
	t.Run("proxy get", func(t *testing.T) {
		expectStackOverflow(t, deepScript(`var p = {x: 1}; for (var i = 0; i < N; i++) p = new Proxy(p, {});`, `p.x`))
	})
	t.Run("proxy isArray", func(t *testing.T) {
		expectStackOverflow(t, deepScript(`var p = []; for (var i = 0; i < N; i++) p = new Proxy(p, {});`, `Array.isArray(p)`))
	})
	t.Run("proxy call", func(t *testing.T) {
		expectStackOverflow(t, deepScript(`var p = function() {}; for (var i = 0; i < N; i++) p = new Proxy(p, {});`, `p()`))
	})
	t.Run("proxy construct", func(t *testing.T) {
		expectStackOverflow(t, deepScript(`var p = function() {}; for (var i = 0; i < N; i++) p = new Proxy(p, {});`, `new p()`))
	})
	t.Run("bound call", func(t *testing.T) {
		// Renaming each bound function keeps "bound bound ... max" from
		// growing with the chain. The vm unwraps a chain of bound functions
		// in a loop; a call from native code (here map's callback) recurses.
		expectStackOverflow(t, deepScript(`var f = Math.max; for (var i = 0; i < N; i++) { f = f.bind(null); Object.defineProperty(f, "name", {value: 0}) }`, `[0].map(f)`))
	})
	t.Run("bound construct", func(t *testing.T) {
		expectStackOverflow(t, deepScript(`var f = function() {}; for (var i = 0; i < N; i++) { f = f.bind(null); Object.defineProperty(f, "name", {value: 0}) }`, `new f()`))
	})
	t.Run("prototype get", func(t *testing.T) {
		expectStackOverflow(t, deepScript(`var o = {x: 1}; for (var i = 0; i < N; i++) o = Object.create(o);`, `o.x`))
	})
	t.Run("prototype set", func(t *testing.T) {
		expectStackOverflow(t, deepScript(`var o = {}; for (var i = 0; i < N; i++) o = Object.create(o);`, `o.y = 1`))
	})
	t.Run("prototype has", func(t *testing.T) {
		expectStackOverflow(t, deepScript(`var o = {}; for (var i = 0; i < N; i++) o = Object.create(o);`, `"y" in o`))
	})
	t.Run("array prototype index", func(t *testing.T) {
		expectStackOverflow(t, deepScript(`var o = []; for (var i = 0; i < N; i++) o = Object.setPrototypeOf([], o);`, `o[5]`))
	})
}

// Cycles made only of native functions never pass through the vm's loop, so
// they recurse in Go until the process aborts unless the Go-to-native calls
// are counted.
func TestNativeDepth_NativeCycles(t *testing.T) {
	t.Run("toLocaleString calls toString", func(t *testing.T) {
		expectStackOverflow(t, deepScript(`var o = {}; o.toString = Object.prototype.toLocaleString;`, `String(o)`))
	})
	t.Run("toString of this", func(t *testing.T) {
		expectStackOverflow(t, deepScript(`var o = {}; o.toString = String.prototype.trim;`, `"" + o`))
	})
	t.Run("getter reads itself", func(t *testing.T) {
		expectStackOverflow(t, deepScript(`
			var flags = Object.getOwnPropertyDescriptor(RegExp.prototype, "flags").get;
			var o = {}; Object.defineProperty(o, "global", {get: flags});`, `o.global`))
	})
	t.Run("native to JS reentry", func(t *testing.T) {
		expectStackOverflow(t, deepScript(`function f() { [0].forEach(f) }`, `f()`))
	})
}

func TestNativeDepth_Source(t *testing.T) {
	t.Run("parens", func(t *testing.T) {
		expectStackOverflow(t, deepScript(``, `eval("(".repeat(N) + "1" + ")".repeat(N))`))
	})
	t.Run("arrays", func(t *testing.T) {
		expectStackOverflow(t, deepScript(``, `eval("[".repeat(N) + "]".repeat(N))`))
	})
	t.Run("blocks", func(t *testing.T) {
		expectStackOverflow(t, deepScript(``, `eval("{".repeat(N) + "}".repeat(N))`))
	})
	t.Run("calls", func(t *testing.T) {
		expectStackOverflow(t, deepScript(``, `eval("f(".repeat(N) + ")".repeat(N))`))
	})
	t.Run("unary", func(t *testing.T) {
		expectStackOverflow(t, deepScript(``, `eval("!".repeat(N) + "1")`))
	})
	t.Run("arrows", func(t *testing.T) {
		expectStackOverflow(t, deepScript(``, `eval("x=>".repeat(N) + "1")`))
	})
	t.Run("member chain", func(t *testing.T) {
		// Built by a loop in the parser, so only the compiler sees the depth.
		expectStackOverflow(t, deepScript(``, `eval("a" + ".b".repeat(N))`))
	})
	t.Run("Function", func(t *testing.T) {
		expectStackOverflow(t, deepScript(``, `new Function("return " + "(".repeat(N) + "1" + ")".repeat(N))`))
	})
	t.Run("Compile", func(t *testing.T) {
		_, err := Compile("", strings.Repeat("(", deepDepth)+"1"+strings.Repeat(")", deepDepth), false)
		var se *CompilerSyntaxError
		if !errors.As(err, &se) || !strings.Contains(se.Message, errMsgStackOverflow) {
			t.Fatalf("err: %v", err)
		}
	})
	t.Run("below the bound", func(t *testing.T) {
		testScript(`eval("(".repeat(1000) + "1" + ")".repeat(1000)) + eval("[".repeat(1000) + "]".repeat(1000)).length`, valueInt(2), t)
	})
}

func TestNativeDepth_RegExpNesting(t *testing.T) {
	// Backreferences and lookarounds are compiled by regexp2, whose parser
	// recurses per group.
	const script = `
	var res = [];
	[1e6, 2000].forEach(function(n) {
		try {
			new RegExp("(".repeat(n) + "a" + ")".repeat(n) + "\\1");
			res.push("compiled");
		} catch (e) {
			res.push(e.name);
		}
	});
	res.push(new RegExp("(".repeat(500) + "a" + ")".repeat(500) + "\\1").test("aa"));
	res.push(new RegExp("(?=".repeat(500) + "a" + ")".repeat(500)).test("a"));
	res.join();
	`
	testScript(script, asciiString("SyntaxError,SyntaxError,true,true"), t)
}

func TestNativeDepth_ErrorLeavesDepthBalanced(t *testing.T) {
	// A throw from inside native recursion unwinds levels without their
	// leaveNative; the depth must be back where the catching code runs.
	vm := New()
	vm.Set("depth", func() int { return nativeDepthOf(vm) })
	v, err := vm.RunString(`
	var N = ` + itoa(deepDepth) + `;
	` + nestedArray + `
	var before = depth(), overflows = 0;
	for (var j = 0; j < 50; j++) {
		try { String(a) } catch (e) { overflows++ }
		try { a.toLocaleString() } catch (e) { overflows++ }
		// One level deeper, so that the bound is hit at the other of the
		// two levels each nested array costs.
		[0].forEach(function() { try { String(a) } catch (e) { overflows++ } });
		try { JSON.stringify({get x() { throw new Error("x") }}) } catch (e) {}
		var o = Object.create({get y() { throw new Error("y") }});
		for (var i = 0; i < 100; i++) o = Object.create(o);
		try { o.y } catch (e) {}
		try { [1].map(function() { null.x }) } catch (e) {}
		try { new Proxy({}, {get: function() { throw 1 }}).x } catch (e) {}
	}
	function inner() {
		try { String(a) } catch (e) { return depth() }
	}
	[before, depth(), [0].map(inner)[0], overflows, String([1, [2, [3]]])].join();
	`)
	if err != nil {
		t.Fatal(err)
	}
	// The script runs one level deep (the run itself), and a function
	// called back from map one level deeper.
	if got := v.String(); got != "1,1,2,150,1,2,3" {
		t.Fatal(got)
	}
	if d := nativeDepthOf(vm); d != 0 {
		t.Fatalf("nativeDepth after the run: %d", d)
	}
}

func TestNativeDepth_Generators(t *testing.T) {
	// A generator suspended inside a try block and resumed at another depth.
	const script = `
	function* g() {
		for (;;) {
			try { yield 1; throw new Error("e"); } catch (e) {}
		}
	}
	var it = g();
	it.next();
	function deep(n) { return n === 0 ? it.next().value : [0].map(function() { return deep(n - 1) })[0]; }
	var s = 0;
	for (var i = 0; i < 100; i++) s += deep(i % 20);
	s;
	`
	vm := New()
	v, err := vm.RunString(script)
	if err != nil {
		t.Fatal(err)
	}
	if v.ToInteger() != 100 {
		t.Fatal(v)
	}
	if d := nativeDepthOf(vm); d != 0 {
		t.Fatalf("nativeDepth after the run: %d", d)
	}
}

// runInterrupted runs script, interrupts it after a short delay and expects
// the run to stop promptly with the interrupt.
func runInterrupted(t *testing.T, script string, globals map[string]interface{}) {
	t.Helper()
	vm := New()
	for k, v := range globals {
		vm.Set(k, v)
	}
	timer := time.AfterFunc(50*time.Millisecond, func() {
		vm.Interrupt("halt")
	})
	defer timer.Stop()
	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := vm.RunString(script)
		done <- err
	}()
	select {
	case err := <-done:
		var ie *InterruptedError
		if !errors.As(err, &ie) {
			t.Fatalf("err: %v", err)
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Fatalf("took %v", d)
		}
	case <-time.After(10 * time.Second):
		// The goroutine is left running: nothing can stop it.
		t.Fatal("not interrupted within 10s")
	}
}

func TestNativeLoops_ObserveInterrupt(t *testing.T) {
	sparse := `var a = []; a.length = 4294967295; a[0] = 1;`
	// Built outside the run, so that the interrupt lands inside JSON.parse.
	bigJSON := map[string]interface{}{"s": "[" + strings.Repeat("0,", 1<<25) + "0]"}
	for _, tc := range []struct {
		name, script string
		globals      map[string]interface{}
	}{
		{"JSON.stringify", sparse + `JSON.stringify(a)`, nil},
		{"JSON.parse", `JSON.parse(s)`, bigJSON},
		{"join", sparse + `a.join()`, nil},
		{"toLocaleString", sparse + `a.toLocaleString()`, nil},
		{"indexOf", sparse + `a.indexOf(2)`, nil},
		{"lastIndexOf", sparse + `a.lastIndexOf(2)`, nil},
		{"includes", sparse + `a.includes(2)`, nil},
		{"forEach", sparse + `a.forEach(function() {})`, nil},
		{"every", sparse + `a.every(function() { return true })`, nil},
		{"reduce", sparse + `a.reduce(function(x) { return x })`, nil},
		{"reverse", sparse + `a.reverse()`, nil},
		{"copyWithin", sparse + `a.copyWithin(1, 0)`, nil},
		{"slice", sparse + `a.slice(0)`, nil},
		{"splice", sparse + `a.splice(0, 1)`, nil},
		{"flat", sparse + `a.flat()`, nil},
		{"sort", sparse + `a.sort()`, nil},
		{"toSorted", `Array.prototype.toSorted.call({length: 4294967294})`, nil},
		{"array-like join", `Array.prototype.join.call({length: 2 ** 53 - 1})`, nil},
		{"Array.from", `Array.from({length: 2 ** 53 - 1})`, nil},
		{"apply", `Math.max.apply(null, {length: 2 ** 53 - 1})`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) { runInterrupted(t, tc.script, tc.globals) })
	}
}

func TestNativeDepth_JSONStringifyDetectsDeepGoCycles(t *testing.T) {
	// Every access to a wrapped Go pointer yields a new *Object, so a cycle
	// through Go values is only seen by comparing what they wrap, also once
	// the stack is deep enough to use a set.
	type node struct {
		Next *node
	}
	for _, n := range []int{1, 10, 100} {
		nodes := make([]node, n)
		for i := 0; i < n-1; i++ {
			nodes[i].Next = &nodes[i+1]
		}
		nodes[n-1].Next = &nodes[0]
		vm := New()
		vm.Set("v", &nodes[0])
		_, err := vm.RunString("JSON.stringify(v)")
		if err == nil || !strings.HasPrefix(err.Error(), "TypeError: Converting circular structure to JSON") {
			t.Fatalf("%d: %v", n, err)
		}
	}
}

func TestNativeDepth_Export(t *testing.T) {
	// Export and ExportTo walk nested objects in Go, from host code; past the
	// bound they panic with the RangeError as an *Exception, like any other
	// JavaScript exception they run into.
	vm := New()
	v, err := vm.RunString(`var N = ` + itoa(deepDepth) + `; ` + nestedObject + ` a`)
	if err != nil {
		t.Fatal(err)
	}
	expectPanic := func(name string, f func()) {
		t.Helper()
		defer func() {
			x := recover()
			ex, ok := x.(*Exception)
			if !ok || !strings.HasPrefix(ex.Error(), "RangeError: "+errMsgStackOverflow) {
				t.Fatalf("%s: recovered %v", name, x)
			}
		}()
		f()
	}
	expectPanic("Export", func() { v.Export() })
	expectPanic("ExportTo", func() {
		var m map[string]interface{}
		_ = vm.ExportTo(v, &m)
	})
	if d := nativeDepthOf(vm); d != 0 {
		t.Fatalf("nativeDepth: %d", d)
	}
	shallow, err := vm.RunString(`var N = 1000; ` + nestedObject + ` a`)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	if err := vm.ExportTo(shallow, &m); err != nil || m["a"] == nil || shallow.Export() == nil {
		t.Fatal(err)
	}
}
