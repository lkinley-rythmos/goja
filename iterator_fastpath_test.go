package goja

import (
	"strconv"
	"testing"
)

// fastPathEngaged evaluates src, which must yield an iterable, and reports
// whether getIterator recorded a Go-level nexter for it.
func fastPathEngaged(t *testing.T, r *Runtime, src string) bool {
	t.Helper()
	v, err := r.RunString(src)
	if err != nil {
		t.Fatal(err)
	}
	return r.getIterator(v, nil).fast != nil
}

// benchCallable evaluates setup, which must yield a zero-argument function,
// and returns it as a Callable so the benchmark loop measures only the call.
func benchCallable(b *testing.B, setup string) Callable {
	b.Helper()
	r := New()
	v, err := r.RunString(setup)
	if err != nil {
		b.Fatal(err)
	}
	f, ok := AssertFunction(v)
	if !ok {
		b.Fatal("setup did not return a function")
	}
	return f
}

func runBench(b *testing.B, setup string) {
	f := benchCallable(b, setup)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := f(nil); err != nil {
			b.Fatal(err)
		}
	}
}

const benchArraySetup = `var a = []; for (var i = 0; i < 1000; i++) a.push(i);`

func BenchmarkForOfArray(b *testing.B) {
	runBench(b, benchArraySetup+`(function() { var n = 0; for (var v of a) n++; return n; })`)
}

func BenchmarkArraySpread(b *testing.B) {
	runBench(b, benchArraySetup+`(function() { return [...a].length; })`)
}

func BenchmarkArrayDestructure(b *testing.B) {
	runBench(b, benchArraySetup+`(function() { var t = 0; for (var i = 0; i < 100; i++) { var [x, y, z] = a; t += x + y + z; } return t; })`)
}

func BenchmarkArrayFrom(b *testing.B) {
	runBench(b, benchArraySetup+`(function() { return Array.from(a).length; })`)
}

func TestIteratorFastPathGate(t *testing.T) {
	engaged := []string{
		`[1, 2, 3]`,
		`[1, , 3]`,
		`new Uint8Array([1, 2])`,
		`[1, 2].keys()`,
		`[1, 2].entries()`,
		`Array.prototype.values.call({length: 2, 0: "a", 1: "b"})`,
		`(function() { var it = [1, 2][Symbol.iterator](); Object.setPrototypeOf(it, { next: Object.getPrototypeOf([][Symbol.iterator]()).next, [Symbol.iterator]() { return this; } }); return it; })()`,
	}
	for _, src := range engaged {
		if !fastPathEngaged(t, New(), src) {
			t.Errorf("expected fast path for %s", src)
		}
	}
	notEngaged := []string{
		`new Map([[1, 2]])`,
		`new Set([1])`,
		`new Map([[1, 2]]).keys()`,
		`"abc"`,
		`(function*() { yield 1; })()`,
		`({ [Symbol.iterator]() { return { next() { return { done: true }; } }; } })`,
		`(function() { var a = [1]; a[Symbol.iterator] = function() { return { next() { return { done: true }; } }; }; return a; })()`,
		`(function() { var it = [1][Symbol.iterator](); it.next = function() { return { done: true }; }; return it; })()`,
		`(function() { var it = new Map([[1, 2]]).keys(); Object.setPrototypeOf(it, Object.getPrototypeOf([][Symbol.iterator]())); return it; })()`,
	}
	for _, src := range notEngaged {
		if fastPathEngaged(t, New(), src) {
			t.Errorf("expected generic path for %s", src)
		}
	}

	// Replacing %ArrayIteratorPrototype%.next disables the fast path for every array in that runtime.
	r := New()
	if !fastPathEngaged(t, r, `[1]`) {
		t.Fatal("precondition: fast path engaged on a fresh runtime")
	}
	if _, err := r.RunString(`var proto = Object.getPrototypeOf([][Symbol.iterator]()); var origNext = proto.next; proto.next = function() { return origNext.call(this); };`); err != nil {
		t.Fatal(err)
	}
	if fastPathEngaged(t, r, `[1]`) {
		t.Fatal("replaced %ArrayIteratorPrototype%.next must disable the fast path")
	}

	// An accessor for next disables it too.
	r = New()
	if _, err := r.RunString(`Object.defineProperty(Object.getPrototypeOf([][Symbol.iterator]()), "next", { configurable: true, get() { return function() { return { done: true }; }; } });`); err != nil {
		t.Fatal(err)
	}
	if fastPathEngaged(t, r, `[1]`) {
		t.Fatal("accessor next must disable the fast path")
	}

	// Replacing Array.prototype[Symbol.iterator] disables it.
	r = New()
	if _, err := r.RunString(`Array.prototype[Symbol.iterator] = function() { return { next() { return { done: true }; } }; };`); err != nil {
		t.Fatal(err)
	}
	if fastPathEngaged(t, r, `[1]`) {
		t.Fatal("replaced Array.prototype[Symbol.iterator] must disable the fast path")
	}

	// An iterator created by another Runtime never matches this runtime's anchor.
	other := New()
	v, err := other.RunString(`[1, 2]`)
	if err != nil {
		t.Fatal(err)
	}
	if New().getIterator(v, nil).fast != nil {
		t.Fatal("cross-runtime iterator must not take the fast path")
	}
}

func TestIteratorFastPathObservable(t *testing.T) {
	// Every observable effect must be identical on both paths. Each script
	// runs once with the intrinsic next (fast path) and once after wrapping
	// %ArrayIteratorPrototype%.next (generic path); results must match.
	const wrapNext = `var __p = Object.getPrototypeOf([][Symbol.iterator]()); var __n = __p.next; __p.next = function() { return __n.call(this); };`
	cases := []struct {
		name, script, want string
	}{
		{"for-of/spread/destructure/from/map/fromEntries", `
			var out = [];
			var s = 0; for (var v of [1, 2, 3]) s += v; out.push(s);
			out.push([...[4, 5]].join("-"));
			var [a, b] = [6, 7, 8]; out.push(a + ":" + b);
			out.push(Array.from([1, , 3]).length, Array.from([1, , 3])[1] === undefined);
			out.push(new Map([[1, "x"], [2, "y"]]).get(2));
			out.push(Object.fromEntries([["k", 9]]).k);
			out.push([...[1, , 3]].length);
			var early = 0; for (var w of [1, 2, 3]) { early++; break; } out.push(early);
			out.join("|");`, "6|4-5|6:7|3|true|y|9|3|1"},
		{"throwing length getter surfaces identically", `
			var it = Array.prototype.values.call({ get length() { throw new Error("boom"); } });
			var msg = ""; try { for (var v of it) {} } catch (e) { msg = e.message; }
			msg;`, "boom"},
		{"proxy trap order", `
			var log = [];
			var p = new Proxy([10, 20], { get(t, k, r) { log.push(String(k)); return Reflect.get(t, k, r); } });
			var out = []; for (var v of p) out.push(v);
			log.join(",") + " => " + out.join(",");`, "Symbol(Symbol.iterator),length,0,length,1,length => 10,20"},
		{"direct next() still returns result objects", `
			var it = [1][Symbol.iterator](); var r1 = it.next(); var r2 = it.next(); var r3 = it.next();
			[r1.value, r1.done, r2.value, r2.done, r3.done].join(",");`, "1,false,,true,true"},
		{"incompatible receiver", `
			var msg = ""; try { Object.getPrototypeOf([][Symbol.iterator]()).next.call(new Map().keys()); } catch (e) { msg = e.constructor.name; } msg;`, "TypeError"},
		{"transplanted prototypes", `
			var proto = Object.getPrototypeOf([][Symbol.iterator]());
			var a = [1, 2][Symbol.iterator](); Object.setPrototypeOf(a, { next: proto.next, [Symbol.iterator]() { return this; } });
			var out = [...a].join(",");
			var m = new Map([[1, 2]]).keys(); Object.setPrototypeOf(m, proto);
			var msg = ""; try { for (var v of m) {} } catch (e) { msg = e.constructor.name; }
			out + "|" + msg;`, "1,2|TypeError"},
	}
	for _, c := range cases {
		for _, generic := range []bool{false, true} {
			r := New()
			src := c.script
			if generic {
				src = wrapNext + src
			}
			v, err := r.RunString(src)
			if err != nil {
				t.Fatalf("%s (generic=%v): %v", c.name, generic, err)
			}
			if got := v.String(); got != c.want {
				t.Fatalf("%s (generic=%v): got %q, want %q", c.name, generic, got, c.want)
			}
		}
	}

	// Tampered next is called exactly once per step on the generic path.
	r := New()
	v, err := r.RunString(`
		var proto = Object.getPrototypeOf([][Symbol.iterator]()); var orig = proto.next; var calls = 0;
		proto.next = function() { calls++; return orig.call(this); };
		var s = 0; for (var v of [1, 2, 3]) s += v; var t = [...[4, 5]];
		calls + ":" + s + ":" + t.length;`)
	if err != nil {
		t.Fatal(err)
	}
	if got := v.String(); got != "7:6:2" {
		t.Fatalf("tampered next call count: got %q, want %q", got, "7:6:2")
	}
}

func TestIteratorFastPathAllocationFree(t *testing.T) {
	// Allocation per iteration must not scale with the number of elements:
	// iterating 10 and 1000 elements must cost the same.
	measure := func(n int) float64 {
		r := New()
		v, err := r.RunString(`var a = []; for (var i = 0; i < ` + strconv.Itoa(n) + `; i++) a.push(i); (function() { var c = 0; for (var v of a) c++; return c; })`)
		if err != nil {
			t.Fatal(err)
		}
		f, _ := AssertFunction(v)
		if _, err := f(nil); err != nil {
			t.Fatal(err)
		}
		return testing.AllocsPerRun(50, func() {
			if _, err := f(nil); err != nil {
				t.Fatal(err)
			}
		})
	}
	small, large := measure(10), measure(1000)
	if large-small > 2 {
		t.Fatalf("for-of allocations scale with length: %v allocs for 10 elements, %v for 1000", small, large)
	}
	if small > 8 {
		t.Fatalf("for-of over 10 elements allocated %v times, want <= 8", small)
	}

	// Runtime.ForOf (Go side) goes through the same record.
	r := New()
	arr, err := r.RunString(`var b = []; for (var i = 0; i < 1000; i++) b.push(i); b`)
	if err != nil {
		t.Fatal(err)
	}
	var sum int64
	allocs := testing.AllocsPerRun(50, func() {
		sum = 0
		r.ForOf(arr, func(v Value) bool { sum += v.ToInteger(); return true })
	})
	if sum != 499500 {
		t.Fatalf("ForOf sum = %d", sum)
	}
	if allocs > 8 {
		t.Fatalf("ForOf over 1000 elements allocated %v times, want <= 8", allocs)
	}
}

func BenchmarkMatchAllLoop(b *testing.B) {
	runBench(b, `var s = "ab".repeat(500); var re = /a/g; (function() { var n = 0; for (var m of s.matchAll(re)) n += m[0].length; return n; })`)
}

func TestRegExpStringIteratorFastPath(t *testing.T) {
	if !fastPathEngaged(t, New(), `"abc".matchAll(/b/g)`) {
		t.Fatal("expected fast path for a RegExp String Iterator")
	}
	r := New()
	if _, err := r.RunString(`var __rp = Object.getPrototypeOf("a".matchAll(/a/g)); var __rn = __rp.next; __rp.next = function() { return __rn.call(this); };`); err != nil {
		t.Fatal(err)
	}
	if fastPathEngaged(t, r, `"abc".matchAll(/b/g)`) {
		t.Fatal("replaced %RegExpStringIteratorPrototype%.next must disable the fast path")
	}
	const wrapNext = `var __rp = Object.getPrototypeOf("a".matchAll(/a/g)); var __rn = __rp.next; __rp.next = function() { return __rn.call(this); };`
	cases := []struct{ name, script, want string }{
		{"global", `[..."a1b2c3".matchAll(/\d/g)].map(function(m) { return m[0]; }).join("");`, "123"},
		{"empty matches advance", `[..."ab".matchAll(/(?:)/g)].map(function(m) { return m.index; }).join(",");`, "0,1,2"},
		{"sticky+global", `[..."aab".matchAll(/a/gy)].map(function(m) { return m.index; }).join(",");`, "0,1"},
		{"groups preserved", `var out = []; for (var m of "k=v;x=y".matchAll(/(\w)=(\w)/g)) out.push(m[1] + m[2]); out.join("|");`, "kv|xy"},
		{"direct next()", `var it = "aa".matchAll(/a/g); var r1 = it.next(); var r2 = it.next(); var r3 = it.next(); [r1.value[0], r1.done, r2.done, r3.done, r3.value].join(",");`, "a,false,false,true,"},
		{"incompatible receiver", `var msg = ""; try { Object.getPrototypeOf("a".matchAll(/a/g)).next.call([][Symbol.iterator]()); } catch (e) { msg = e.constructor.name; } msg;`, "TypeError"},
	}
	for _, c := range cases {
		for _, generic := range []bool{false, true} {
			r := New()
			src := c.script
			if generic {
				src = wrapNext + src
			}
			v, err := r.RunString(src)
			if err != nil {
				t.Fatalf("%s (generic=%v): %v", c.name, generic, err)
			}
			if got := v.String(); got != c.want {
				t.Fatalf("%s (generic=%v): got %q, want %q", c.name, generic, got, c.want)
			}
		}
	}
}

func TestIteratorFastPathYieldStarForwardsResultObject(t *testing.T) {
	// yield* must hand the inner iterator's result object through unchanged,
	// so generatorObject.callDelegated bypasses the fast path by design.
	testScript(`
		var marker = { value: 1, done: false, tag: "x" };
		var inner = { [Symbol.iterator]() { return this; }, next() { return marker; } };
		function* g() { yield* inner; }
		var r = g().next();
		function* h() { yield* [1, 2]; }
		var out = [...h()];
		(r === marker) + ":" + r.tag + ":" + out.join(",");
	`, asciiString("true:x:1,2"), t)
}
