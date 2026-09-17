package goja

import (
	"strings"
	"testing"
)

// callApplyGoldenScript pins this-binding, argument counts, arguments
// objects, new.target, apply's array-like handling and error text, bound
// functions (0-2 bound args, bound-of-bound, bound call/apply, new),
// non-callable targets, class/generator/method/arrow/proxy targets, and
// exception propagation. Stack traces are deliberately absent: the
// intrinsic frame disappearing is the one intended change (see
// TestCallIntrinsicStackShape).
const callApplyGoldenScript = `
var out = [];
function sloppy(a, b, c) { return [typeof this, this === undefined ? 'U' : (this === globalThis ? 'G' : (typeof this === 'object' ? 'O' : String(this))), arguments.length, a, b, c, new.target === undefined].join(':') }
function strict(a, b, c) { 'use strict'; return [typeof this, this === undefined ? 'U' : (this === globalThis ? 'G' : (typeof this === 'object' ? 'O' : String(this))), arguments.length, a, b, c, new.target === undefined].join(':') }
var arrow = (a, b) => [typeof this, a, b].join(':');
var native = Object.prototype.hasOwnProperty;
var obj = {k: 1};
var fs = {sloppy: sloppy, strict: strict};
for (var name in fs) {
  var f = fs[name];
  out.push(f.call(), f.call(undefined), f.call(null), f.call(7), f.call('s'), f.call(obj), f.call(obj, 1), f.call(obj, 1, 2), f.call(obj, 1, 2, 3), f.call(obj, 1, 2, 3, 4));
  out.push(f.apply(), f.apply(obj), f.apply(obj, undefined), f.apply(obj, null), f.apply(obj, []), f.apply(obj, [1]), f.apply(obj, [1, 2, 3, 4]), f.apply(obj, [1, , 3]), f.apply(obj, {length: 2, 0: 'x', 1: 'y'}), f.apply(obj, (function(){ return arguments })('p', 'q')), f.apply(obj, new Array(3)), f.apply(obj, [1, 2, 3], 'extra'));
  var sparse = [1, 2, 3]; sparse[10] = 11; out.push(f.apply(obj, sparse));
  try { f.apply(obj, 5) } catch (e) { out.push(e.constructor.name + ':' + e.message) }
  try { f.apply(obj, 'str') } catch (e) { out.push(e.constructor.name + ':' + e.message) }
  var b0 = f.bind(), b1 = f.bind(obj), b2 = f.bind(obj, 1), b3 = f.bind(obj, 1, 2), bb = f.bind(obj, 1).bind({other: 1}, 2);
  out.push(b0(), b0(9), b1(), b1(9), b2(), b2(9), b3(), b3(9), bb(), bb(9), b2.call({x: 1}, 8), b2.apply({x: 1}, [8, 7]), b2.length, b2.name);
  out.push(typeof new b1(1, 2), (new b1(1, 2)) instanceof f);
}
out.push(arrow.call(obj, 1, 2), arrow.apply(obj, [1, 2]), arrow.bind(obj, 1)(2));
out.push(native.call(obj, 'k'), native.call(obj, 'z'), native.apply(obj, ['k']), Function.prototype.call.bind(native)(obj, 'k'), Function.prototype.call.bind(native)(obj, 'z'), Function.prototype.apply.bind(native)(obj, ['k']));
var has = Function.prototype.call.bind(native); out.push(has(obj, 'k'), has.call(null, obj, 'k'), has.apply(null, [obj, 'k']));
function thrower() { throw new RangeError('boom') }
try { thrower.call(obj) } catch (e) { out.push(e.constructor.name + ':' + e.message) }
try { thrower.apply(obj, []) } catch (e) { out.push(e.constructor.name + ':' + e.message) }
try { thrower.bind(obj)() } catch (e) { out.push(e.constructor.name + ':' + e.message) }
var bads = [undefined, null, 1, 'x', {}, Symbol.iterator];
for (var i = 0; i < bads.length; i++) {
  try { Function.prototype.call.call(bads[i], obj) } catch (e) { out.push(e.constructor.name + ':' + e.message) }
  try { Function.prototype.apply.call(bads[i], obj, []) } catch (e) { out.push(e.constructor.name + ':' + e.message) }
}
class K { constructor(x) { this.x = x } static s(a) { return 'static:' + a + ':' + (this === K) } }
out.push(K.s.call(K, 1), K.s.call(null, 2));
try { K.call(obj, 1) } catch (e) { out.push(e.constructor.name + ':' + e.message) }
function* gen(a) { yield a; yield this === obj }
var g = gen.call(obj, 'g'); out.push(g.next().value + ':' + g.next().value);
var meth = {m(a) { return [this === obj, a, new.target === undefined].join(':') }}.m;
out.push(meth.call(obj, 'm'), meth.apply(obj, ['m2']), meth.bind(obj)('m3'));
var asy = async function(a) { return this === obj ? 'async:' + a : 'bad' };
asy.call(obj, 1).then(function(v) { out.push(v) });
out.push(String.prototype.toUpperCase.call('abc'), Array.prototype.join.call('abc', '-'), Math.max.apply(null, [1, 5, 3]), Math.max.apply(Math, {length: 2, 0: 4, 1: 9}));
function rec(n) { return n === 0 ? 0 : 1 + rec.call(null, n - 1) }
out.push(rec(500));
function mapped(a) { arguments[0] = 'changed'; return a }
out.push(mapped.call(null, 'orig'), mapped.apply(null, ['orig']), mapped.bind(null)('orig'));
function who() { return this === obj ? 'obj' : (this === undefined ? 'undef' : 'other') }
out.push(who.bind(obj).call({}), who.bind(obj).bind({})(), who.call(), (function(){ 'use strict'; return who.bind()() })());
var callee = null; function cap() { callee = arguments.callee; return arguments.length } out.push(cap.call(obj, 1, 2), callee === cap);
var proxyFn = new Proxy(function(a) { return 'px:' + a + ':' + (this === obj) }, {}); out.push(proxyFn.call(obj, 1), proxyFn.apply(obj, [2]), proxyFn.bind(obj)(3));
out.join('|');
`

// callApplyGolden was captured from the unmodified engine at 7f360ba.
const callApplyGolden = `object:G:0::::true|object:G:0::::true|object:G:0::::true|object:O:0::::true|object:O:0::::true|object:O:0::::true|object:O:1:1:::true|object:O:2:1:2::true|object:O:3:1:2:3:true|object:O:4:1:2:3:true|object:G:0::::true|object:O:0::::true|object:O:0::::true|object:O:0::::true|object:O:0::::true|object:O:1:1:::true|object:O:4:1:2:3:true|object:O:3:1::3:true|object:O:2:x:y::true|object:O:2:p:q::true|object:O:3::::true|object:O:3:1:2:3:true|object:O:11:1:2:3:true|TypeError:Value is not an object: 5|TypeError:Value is not an object: str|object:G:0::::true|object:G:1:9:::true|object:O:0::::true|object:O:1:9:::true|object:O:1:1:::true|object:O:2:1:9::true|object:O:2:1:2::true|object:O:3:1:2:9:true|object:O:2:1:2::true|object:O:3:1:2:9:true|object:O:2:1:8::true|object:O:3:1:8:7:true|2|bound sloppy|object|true|undefined:U:0::::true|undefined:U:0::::true|object:O:0::::true|number:7:0::::true|string:s:0::::true|object:O:0::::true|object:O:1:1:::true|object:O:2:1:2::true|object:O:3:1:2:3:true|object:O:4:1:2:3:true|undefined:U:0::::true|object:O:0::::true|object:O:0::::true|object:O:0::::true|object:O:0::::true|object:O:1:1:::true|object:O:4:1:2:3:true|object:O:3:1::3:true|object:O:2:x:y::true|object:O:2:p:q::true|object:O:3::::true|object:O:3:1:2:3:true|object:O:11:1:2:3:true|TypeError:Value is not an object: 5|TypeError:Value is not an object: str|undefined:U:0::::true|undefined:U:1:9:::true|object:O:0::::true|object:O:1:9:::true|object:O:1:1:::true|object:O:2:1:9::true|object:O:2:1:2::true|object:O:3:1:2:9:true|object:O:2:1:2::true|object:O:3:1:2:9:true|object:O:2:1:8::true|object:O:3:1:8:7:true|2|bound strict|object|true|object:1:2|object:1:2|object:1:2|true|false|true|true|false|true|true|true|true|RangeError:boom|RangeError:boom|RangeError:boom|TypeError:Value is not an object: undefined|TypeError:Value is not an object: undefined|TypeError:Value is not an object: null|TypeError:Value is not an object: null|TypeError:Value is not an object: 1|TypeError:Value is not an object: 1|TypeError:Value is not an object: x|TypeError:Value is not an object: x|TypeError:Value is not callable: [object Object]|TypeError:Value is not callable: [object Object]|TypeError:Value is not an object: Symbol.iterator|TypeError:Value is not an object: Symbol.iterator|static:1:true|static:2:false|TypeError:Class constructor cannot be invoked without 'new'|g:true|true:m:true|true:m2:true|true:m3:true|ABC|a-b-c|5|9|500|changed|changed|changed|obj|obj|other|other|2|true|px:1:true|px:2:true|px:3:true`

func TestCallApplyBindCharacterization(t *testing.T) {
	testScript(callApplyGoldenScript, asciiString(callApplyGolden), t)
}

// TestCallIntrinsicOverflow: unbounded recursion through call/apply/bind
// surfaces as *StackOverflowError exactly as direct recursion does, before
// and after the change (the depth at which it triggers differs; the type
// does not).
func TestCallIntrinsicOverflow(t *testing.T) {
	for _, src := range []string{
		`function d() { return d() } d()`,
		`function inf() { return inf.call(null) } inf()`,
		`function infA() { return infA.apply(null, []) } infA()`,
		`var infB = function() { return infB.bind(null)() }; infB()`,
	} {
		vm := New()
		vm.SetMaxCallStackSize(200)
		_, err := vm.RunString(src)
		if _, ok := err.(*StackOverflowError); !ok {
			t.Errorf("%s: got %T (%v), want *StackOverflowError", src, err, err)
		}
	}
}

// TestCallIntrinsicStackShape pins the one intended observable change:
// call/apply/bound no longer appear as "(native)" frames because the
// target runs in the caller's VM loop, exactly as a direct call does. The
// scripts are byte-identical to the ones the pre-change traces were taken
// from, so the remaining frames' positions are unchanged. A builtin that
// re-enters the VM (map) keeps its frame — the control case.
func TestCallIntrinsicStackShape(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want []string
	}{
		{
			`function inner() { return new Error('x').stack } function outer() { return inner.call(null, 1) } outer()`,
			[]string{"Error: x", "\tat inner (<eval>:1:27(3))", "\tat outer (<eval>:1:86(5))", "\tat <eval>:1:103(4)"},
		},
		{
			`function inner() { return new Error('x').stack } function outer() { return inner.apply(null, [1]) } outer()`,
			[]string{"Error: x", "\tat inner (<eval>:1:27(3))", "\tat outer (<eval>:1:87(7))", "\tat <eval>:1:106(4)"},
		},
		{
			`function inner() { return new Error('x').stack } function outer() { return inner.bind(null)(1) } outer()`,
			[]string{"Error: x", "\tat inner (<eval>:1:27(3))", "\tat outer (<eval>:1:92(7))", "\tat <eval>:1:103(4)"},
		},
		{
			`function inner() { return new Error('x').stack } var b = Function.prototype.call.bind(inner); function outer() { return b(null, 1) } outer()`,
			[]string{"Error: x", "\tat inner (<eval>:1:27(3))", "\tat outer (<eval>:1:122(4))", "\tat <eval>:1:139(12)"},
		},
		{
			`function inner() { return new Error('x').stack } function outer() { return [1].map(inner)[0] } outer()`,
			[]string{"Error: x", "\tat inner (<eval>:1:27(3))", "\tat map (native)", "\tat outer (<eval>:1:83(6))", "\tat <eval>:1:101(4)"},
		},
		{
			`function f() {} function outer() { try { f.apply({}, 5) } catch (e) { return e.stack } } outer()`,
			[]string{"TypeError: Value is not an object: 5", "\tat outer (<eval>:1:49(6))", "\tat <eval>:1:95(4)"},
		},
	} {
		vm := New()
		v, err := vm.RunString(tc.src)
		if err != nil {
			t.Fatal(err)
		}
		got := strings.Split(strings.TrimRight(v.String(), "\n"), "\n")
		if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
			t.Errorf("%s:\n got %q\nwant %q", tc.src, got, tc.want)
		}
	}
}

// TestCallIntrinsicsNoAlloc: f.call(t, a), f.apply(t, arr) with a dense
// array, and bound(a) allocate nothing beyond the direct call f(a).
func TestCallIntrinsicsNoAlloc(t *testing.T) {
	vm := New()
	if _, err := vm.RunString(`
		var f = function(a, b) { return a };
		var n = Object.prototype.hasOwnProperty;
		var o = {k: 1};
		var args = [1, 2];
		var bf = f.bind(o, 1);
		var bn = Function.prototype.call.bind(n);
	`); err != nil {
		t.Fatal(err)
	}
	measure := func(src string) float64 {
		prg, err := Compile("m", src, false)
		if err != nil {
			t.Fatal(err)
		}
		return testing.AllocsPerRun(100, func() {
			if _, err := vm.RunProgram(prg); err != nil {
				panic(err)
			}
		})
	}
	direct := measure("f(1, 2)")
	directNative := measure("o.hasOwnProperty('k')")
	for _, tc := range []struct {
		src  string
		base float64
	}{
		{"f.call(o, 1, 2)", direct},
		{"f.call(o)", direct},
		{"f.apply(o, args)", direct},
		{"f.apply(o)", direct},
		{"bf(2)", direct},
		{"n.call(o, 'k')", directNative},
		{"bn(o, 'k')", directNative},
	} {
		if got := measure(tc.src); got != tc.base {
			t.Errorf("%s: %v allocations per run, direct call %v", tc.src, got, tc.base)
		}
	}
}

func benchmarkProgram(b *testing.B, setup, body string) {
	vm := New()
	if _, err := vm.RunString(setup); err != nil {
		b.Fatal(err)
	}
	prg, err := Compile("b", body, false)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := vm.RunProgram(prg); err != nil {
			b.Fatal(err)
		}
	}
}

const callBenchSetup = `var f = function(s, x) { return (s + x) & 1023 }; var n = Object.prototype.hasOwnProperty; var o = {k: 1};`

func BenchmarkDirectCall(b *testing.B) {
	benchmarkProgram(b, callBenchSetup, `(function(){ var s = 0; for (var i = 0; i < 1000; i++) s = f(s, i); return s })()`)
}
func BenchmarkFuncProtoCall(b *testing.B) {
	benchmarkProgram(b, callBenchSetup, `(function(){ var s = 0; for (var i = 0; i < 1000; i++) s = f.call(null, s, i); return s })()`)
}
func BenchmarkFuncProtoApply(b *testing.B) {
	benchmarkProgram(b, callBenchSetup, `(function(){ var s = 0, a = [0, 0]; for (var i = 0; i < 1000; i++) { a[0] = s; a[1] = i; s = f.apply(null, a) } return s })()`)
}
func BenchmarkNativeCall(b *testing.B) {
	benchmarkProgram(b, callBenchSetup, `(function(){ var s = 0; for (var i = 0; i < 1000; i++) if (n.call(o, 'k')) s++; return s })()`)
}

func BenchmarkBoundCall(b *testing.B) {
	benchmarkProgram(b, callBenchSetup+` var bf = f.bind(null, 0); var has = Function.prototype.call.bind(n);`,
		`(function(){ var s = 0; for (var i = 0; i < 1000; i++) { s = bf(i); if (has(o, 'k')) s++ } return s })()`)
}
