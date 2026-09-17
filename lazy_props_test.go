package goja

import (
	"strings"
	"testing"

	"github.com/dop251/goja/unistring"
)

func runBenchClosure(b *testing.B, setup string) {
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
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := f(nil); err != nil {
			b.Fatal(err)
		}
	}
}

// Array iterator objects never receive an own property; with a lazy map
// their creation should cost only the Object and the iterator struct.
func BenchmarkArrayIterObjectCreate(b *testing.B) {
	runBenchClosure(b, `var a = [1]; (function() { var n = 0; for (var i = 0; i < 1000; i++) { var it = a[Symbol.iterator](); n += it.next().value; } return n; })`)
}

func propMap(t *testing.T, r *Runtime, src string) map[unistring.String]Value {
	t.Helper()
	v, err := r.RunString(src)
	if err != nil {
		t.Fatal(err)
	}
	switch impl := v.(*Object).self.(type) {
	case *baseObject:
		return impl.values
	case *arrayObject:
		return impl.values0()
	case *arrayIterObject:
		return impl.values
	case *dateObject:
		return impl.values
	case *regexpObject:
		return impl.values
	default:
		t.Fatalf("unexpected impl %T", impl)
		return nil
	}
}

func TestLazyPropertyMap(t *testing.T) {
	r := New()
	// RegExp objects are excluded: regexpObject.init stores lastIndex eagerly.
	for _, src := range []string{`({})`, `new Date(0)`, `[][Symbol.iterator]()`} {
		if m := propMap(t, r, src); m != nil {
			t.Errorf("%s: expected nil property map before any write, got len %d", src, len(m))
		}
	}
	if m := propMap(t, r, `(function() { var o = {}; o.x = 1; return o; })()`); m == nil {
		t.Fatal("expected the map to exist after a write")
	}
	// Behaviour on never-written objects must be unchanged.
	r.testScript(`
		var d = new Date(0);
		[Object.keys(d).length, d.hasOwnProperty("x"), delete d.x, JSON.stringify(d), Object.getOwnPropertyNames(d).length,
		 (function() { var k = []; for (var p in d) k.push(p); return k.length; })(),
		 Object.isFrozen(Object.freeze({})), Object.isExtensible(Object.preventExtensions({}))].join(",")`,
		asciiString("0,false,true,\"1970-01-01T00:00:00.000Z\",0,0,true,false"), t)
	// Templated objects (the global object) still materialize and honour the white-hole delete marker.
	r.testScript(`
		var before = typeof parseInt;
		delete globalThis.parseInt;
		var after = typeof parseInt;
		globalThis.parseInt = 42;
		[before, after, parseInt, Object.getOwnPropertyDescriptor(globalThis, "Math").writable].join(",")`,
		asciiString("function,undefined,42,true"), t)
}

// values0 exposes the base property map to tests.
func (a *arrayObject) values0() map[unistring.String]Value { return a.baseObject.values }

func BenchmarkArrayLiteralCreate(b *testing.B) {
	runBenchClosure(b, `(function() { var n = 0; for (var i = 0; i < 1000; i++) { var a = []; n += a.length; } return n; })`)
}

const arrayLengthGoldenScript = `
function dump(a) {
	return [
		Object.getOwnPropertyNames(a).join(","),
		Object.keys(a).join(","),
		(function() { var k = []; for (var p in a) k.push(p); return k.join(","); })(),
		JSON.stringify(a),
		JSON.stringify(Object.getOwnPropertyDescriptor(a, "length")),
		Object.keys(Object.assign({}, a)).join(","),
		[...a].length
	].join("/");
}
var out = [];
var a = [1, 2, 3]; a.x = 9;
out.push(dump(a));
out.push(delete a.length);
out.push((function() { "use strict"; try { delete a.length; return "no"; } catch (e) { return e.constructor.name; } })());
Object.defineProperty(a, "length", { value: 2 }); out.push(dump(a));
Object.defineProperty(a, "length", { writable: false }); out.push(dump(a));
out.push((function() { "use strict"; try { a.push(1); return "no"; } catch (e) { return e.constructor.name; } })());
var b = [1]; Object.freeze(b); out.push(Object.isFrozen(b), JSON.stringify(Object.getOwnPropertyDescriptor(b, "length")));
var c = []; c.foo = 1; c[0] = 2; out.push(dump(c));
var s = []; s[100000] = 1; s.y = 2;
out.push(Object.getOwnPropertyNames(s).join(","), s.length);
for (var i = 0; i < 20000; i++) s[i] = i;
out.push(Object.getOwnPropertyNames(s).length, s.length, Object.keys(s).length, JSON.stringify(Object.getOwnPropertyDescriptor(s, "length")));
var e = []; out.push(Object.getOwnPropertyNames(e).join(","), Object.keys(e).length, e.hasOwnProperty("length"), e.hasOwnProperty("x"), delete e.x);
var g = []; g.x = 1; g[100000] = 2; out.push(Object.getOwnPropertyNames(g).join(","));
var h = []; h.q = 1; h[100000] = 2; Object.freeze(h); out.push(Object.getOwnPropertyNames(h).join(","), Object.isFrozen(h));
out.join("|");
`

// Captured from the unmodified engine at 43617bb. Must pass before and after Task 2.
const arrayLengthGolden = `0,1,2,length,x/0,1,2,x/0,1,2,x/[1,2,3]/{"value":3,"writable":true,"enumerable":false,"configurable":false}/0,1,2,x/3|false|TypeError|0,1,length,x/0,1,x/0,1,x/[1,2]/{"value":2,"writable":true,"enumerable":false,"configurable":false}/0,1,x/2|0,1,length,x/0,1,x/0,1,x/[1,2]/{"value":2,"writable":false,"enumerable":false,"configurable":false}/0,1,x/2|TypeError|true|{"value":1,"writable":false,"enumerable":false,"configurable":false}|0,length,foo/0,foo/0,foo/[2]/{"value":1,"writable":true,"enumerable":false,"configurable":false}/0,foo/1|100000,length,y|100001|20003|100001|20002|{"value":100001,"writable":true,"enumerable":false,"configurable":false}|length|0|true|false|true|100000,length,x|100000,length,q|true`

func TestArrayLengthCharacterization(t *testing.T) {
	testScript(arrayLengthGoldenScript, asciiString(arrayLengthGolden), t)
}

func TestArrayLengthNotInMap(t *testing.T) {
	r := New()
	for _, src := range []string{`[]`, `[1, 2]`, `(function() { var a = [1]; a.x = 1; return a; })()`, `(function() { var a = []; Object.defineProperty(a, "length", { value: 3 }); return a; })()`} {
		v, err := r.RunString(src)
		if err != nil {
			t.Fatal(err)
		}
		a := v.(*Object).self.(*arrayObject)
		if _, ok := a.baseObject.values["length"]; ok {
			t.Errorf("%s: length is in the property map", src)
		}
		for _, n := range a.propNames {
			if n == "length" {
				t.Errorf("%s: length is in propNames", src)
			}
		}
	}
	// A sparse array converted back to dense must not inherit the sparse map entry.
	v, err := r.RunString(`(function() { var s = []; s[100000] = 1; for (var i = 0; i < 20000; i++) s[i] = i; return s; })()`)
	if err != nil {
		t.Fatal(err)
	}
	a, ok := v.(*Object).self.(*arrayObject)
	if !ok {
		t.Fatalf("expected a dense array after conversion, got %T", v.(*Object).self)
	}
	if _, ok := a.baseObject.values["length"]; ok {
		t.Error("converted dense array inherited length from the sparse map")
	}
	for _, n := range a.propNames {
		if n == "length" {
			t.Error("converted dense array has length in propNames")
		}
	}
}

func BenchmarkFunctionCreate(b *testing.B) {
	runBenchClosure(b, `(function() { var n = 0; for (var i = 0; i < 1000; i++) { var f = function() { return i; }; n += f.length; } return n; })`)
}

// Object-literal methods get their name via defineOwnPropertyStr right after
// creation; that path must stay allocation-free for the intrinsics.
func BenchmarkMethodLiteralCreate(b *testing.B) {
	runBenchClosure(b, `(function() { var n = 0; for (var i = 0; i < 1000; i++) { var o = { m() { return i; } }; n += o.m.length; } return n; })`)
}

const funcIntrinsicsGoldenScript = `
function names(f) { return Object.getOwnPropertyNames(f).join(","); }
var out = [];
function decl(a, b) {}
var expr = function(a) {};
var arrow = (a, b, c) => 0;
var obj = { m(a) {}, get g() { return 1; } };
class K2 { static s(a, b) {} m() {} }
var bound = decl.bind(null, 1);
function* gen(a) {}
async function asy(a, b) {}
var native = Array.prototype.push;
var fns = [decl, expr, arrow, obj.m, K2, K2.s, K2.prototype.m, bound, gen, asy, native, Object.getOwnPropertyDescriptor(obj, "g").get];
for (var i = 0; i < fns.length; i++) {
	var f = fns[i];
	out.push(names(f) + ":" + f.name + ":" + f.length + ":" + JSON.stringify(Object.getOwnPropertyDescriptor(f, "name")) + ":" + JSON.stringify(Object.getOwnPropertyDescriptor(f, "length")));
}
function p1() {} p1.prototype; p1.u = 1; out.push(names(p1));
function p2() {} p2.u = 1; p2.prototype; out.push(names(p2));
function d1() {} out.push(delete d1.name, names(d1), d1.name, Object.prototype.hasOwnProperty.call(d1, "name"));
function d2() {} Object.defineProperty(d2, "name", { value: "renamed" }); out.push(names(d2), d2.name, JSON.stringify(Object.getOwnPropertyDescriptor(d2, "name")));
function d3() {} d3.u = 1; Object.defineProperty(d3, "name", { value: "w", writable: true, enumerable: true, configurable: true }); out.push(names(d3), Object.keys(d3).join(","), d3.name);
function d4() {} d4.name = "x"; out.push(d4.name);
out.push((function() { "use strict"; function d5() {} try { d5.name = "x"; return "no"; } catch (e) { return e.constructor.name; } })());
function d6() {} Object.freeze(d6); out.push(Object.isFrozen(d6), JSON.stringify(Object.getOwnPropertyDescriptor(d6, "name")), JSON.stringify(Object.getOwnPropertyDescriptor(d6, "length")));
class K3 { static name() { return "N"; } } out.push(typeof K3.name, names(K3));
function d7() {} d7[5] = 1; d7.u = 2; out.push(names(d7));
function d8() {} Object.defineProperty(d8, "length", { get() { return 42; } }); out.push(d8.length, names(d8));
function d9() {} out.push(Object.keys(d9).length, JSON.stringify(d9), d9.hasOwnProperty("length"), d9.hasOwnProperty("name"), d9.hasOwnProperty("x"));
function e1(a, b) {} Object.defineProperty(e1, "length", { enumerable: true }); out.push(Object.keys(e1).join(","), JSON.stringify(e1), names(e1));
function e2() {} Object.defineProperty(e2, "name", { enumerable: true }); out.push(Object.keys(e2).join(","), Object.entries(e2).join(";"));
function w1(a, b) {} Object.defineProperty(w1, "length", { writable: true }); w1.length = 99; out.push(w1.length, names(w1));
function w2() {} Object.defineProperty(w2, "name", { writable: true }); w2.name = "zz"; out.push(w2.name);
out.push((function() { "use strict"; function w3(a) {} Object.defineProperty(w3, "length", { writable: true }); w3.length = 99; return w3.length; })());
function w4() {} Object.defineProperty(w4, "length", { value: 7, writable: true, configurable: true }); w4.length = 99; out.push(w4.length, names(w4));
function i1() {} i1.p0 = 0; i1.p1 = 1; i1.p2 = 2; i1.p3 = 3; var seen = []; for (var k in i1) { seen.push(k); if (seen.length === 1) delete i1.name; } out.push(seen.join(","), names(i1));
function i2() {} i2.p0 = 0; i2.p1 = 1; i2.p2 = 2; i2.p3 = 3; var seen2 = []; for (var k2 in i2) { seen2.push(k2); if (seen2.length === 1) Object.defineProperty(i2, "name", { value: "x", writable: true, enumerable: true, configurable: true }); } out.push(seen2.join(","), names(i2));
out.join("|");
`

// Captured from the unmodified engine at 43617bb. Must pass before and after Task 3.
const funcIntrinsicsGolden = `prototype,length,name:decl:2:{"value":"decl","writable":false,"enumerable":false,"configurable":true}:{"value":2,"writable":false,"enumerable":false,"configurable":true}|prototype,length,name:expr:1:{"value":"expr","writable":false,"enumerable":false,"configurable":true}:{"value":1,"writable":false,"enumerable":false,"configurable":true}|length,name:arrow:3:{"value":"arrow","writable":false,"enumerable":false,"configurable":true}:{"value":3,"writable":false,"enumerable":false,"configurable":true}|length,name:m:1:{"value":"m","writable":false,"enumerable":false,"configurable":true}:{"value":1,"writable":false,"enumerable":false,"configurable":true}|length,name,prototype,s:K2:0:{"value":"K2","writable":false,"enumerable":false,"configurable":true}:{"value":0,"writable":false,"enumerable":false,"configurable":true}|length,name:s:2:{"value":"s","writable":false,"enumerable":false,"configurable":true}:{"value":2,"writable":false,"enumerable":false,"configurable":true}|length,name:m:0:{"value":"m","writable":false,"enumerable":false,"configurable":true}:{"value":0,"writable":false,"enumerable":false,"configurable":true}|length,name:bound decl:1:{"value":"bound decl","writable":false,"enumerable":false,"configurable":true}:{"value":1,"writable":false,"enumerable":false,"configurable":true}|length,name,prototype:gen:1:{"value":"gen","writable":false,"enumerable":false,"configurable":true}:{"value":1,"writable":false,"enumerable":false,"configurable":true}|length,name:asy:2:{"value":"asy","writable":false,"enumerable":false,"configurable":true}:{"value":2,"writable":false,"enumerable":false,"configurable":true}|length,name:push:1:{"value":"push","writable":false,"enumerable":false,"configurable":true}:{"value":1,"writable":false,"enumerable":false,"configurable":true}|length,name:get g:0:{"value":"get g","writable":false,"enumerable":false,"configurable":true}:{"value":0,"writable":false,"enumerable":false,"configurable":true}|length,name,prototype,u|length,name,u,prototype|true|prototype,length||false|prototype,length,name|renamed|{"value":"renamed","writable":false,"enumerable":false,"configurable":true}|prototype,length,name,u|name,u|w|d4|TypeError|true|{"value":"d6","writable":false,"enumerable":false,"configurable":false}|{"value":0,"writable":false,"enumerable":false,"configurable":false}|function|length,name,prototype|prototype,5,length,name,u|42|prototype,length,name|0||true|true|false|length||prototype,length,name|name|name,e2|99|prototype,length,name|zz|99|99|prototype,length,name|p0,p1,p2,p3|length,p0,p1,p2,p3,prototype|p0,p1,p2,p3|length,name,p0,p1,p2,p3,prototype`

func TestFuncIntrinsicsCharacterization(t *testing.T) {
	testScript(funcIntrinsicsGoldenScript, asciiString(funcIntrinsicsGolden), t)
}

func funcImpl(t *testing.T, r *Runtime, src string) *baseFuncObject {
	t.Helper()
	v, err := r.RunString(src)
	if err != nil {
		t.Fatal(err)
	}
	switch f := v.(*Object).self.(type) {
	case *funcObject:
		return &f.baseFuncObject
	case *arrowFuncObject:
		return &f.baseFuncObject
	case *methodFuncObject:
		return &f.baseFuncObject
	case *classFuncObject:
		return &f.baseFuncObject
	case *nativeFuncObject:
		return &f.baseFuncObject
	case *boundFuncObject:
		return &f.baseFuncObject
	case *generatorFuncObject:
		return &f.baseFuncObject
	case *asyncFuncObject:
		return &f.baseFuncObject
	default:
		t.Fatalf("unexpected function impl %T", f)
		return nil
	}
}

func TestFuncIntrinsicsVirtual(t *testing.T) {
	r := New()
	virtual := []string{
		`(function f(a) {})`,
		`(() => 0)`,
		`({ m() {} }).m`,
		`Array.prototype.push`,
		`(function() { function f() {} f.u = 1; return f; })()`,
		`(function() { function f() {} Object.defineProperty(f, "name", { value: "renamed" }); return f; })()`,
		`(function() { class K { static name() {} } return K; })()`,
		`(function() { function f() {} return f.bind(null); })()`,
		`(function*() {})`,
		`(async function() {})`,
	}
	for _, src := range virtual {
		f := funcImpl(t, r, src)
		if !f.intrinsicsVirtual {
			t.Errorf("%s: intrinsics unexpectedly materialized", src)
		}
		if _, ok := f.values["length"]; ok {
			t.Errorf("%s: length is in the property map", src)
		}
	}
	materialized := []string{
		`(function() { function f() {} delete f.name; return f; })()`,
		`(function() { function f() {} Object.defineProperty(f, "name", { value: "w", writable: true, enumerable: true, configurable: true }); return f; })()`,
	}
	for _, src := range materialized {
		f := funcImpl(t, r, src)
		if f.intrinsicsVirtual {
			t.Errorf("%s: expected intrinsics to be materialized", src)
		}
	}
	// Materialization places length, name at the front of propNames.
	f := funcImpl(t, r, `(function() { function f() {} f.u = 1; f.prototype; Object.defineProperty(f, "name", { value: "w", writable: true, enumerable: true, configurable: true }); return f; })()`)
	if len(f.propNames) < 2 || f.propNames[0] != "length" || f.propNames[1] != "name" {
		t.Errorf("propNames after materialization = %v, want length, name first", f.propNames)
	}

	f = funcImpl(t, r, `(function() { function f() {} f[5] = 1; f.u = 2; return f; })()`)
	var order []string
	for item, next := f.iterateStringKeys()(); next != nil; item, next = next() {
		order = append(order, item.name.String())
	}
	if got := strings.Join(order, ","); got != "5,length,name,u" {
		t.Errorf("virtual iterator order = %q, want 5,length,name,u", got)
	}

	// funcImpl returns the embedded *baseFuncObject directly, so these calls
	// dispatch to baseFuncObject.iterateStringKeys, not funcObject's override
	// that injects "prototype" (that only happens through the objectImpl
	// interface, e.g. Object.getOwnPropertyNames). No "prototype" is expected
	// here for that reason.
	iterOrderCases := []struct {
		src  string
		want string
	}{
		{`(function() { function f() {} return f; })()`, "length,name"},
		{`(function() { function f() {} f[7] = 1; return f; })()`, "7,length,name"},
		{`(function() { function f() {} f[3] = 1; f[1] = 2; return f; })()`, "1,3,length,name"},
	}
	for _, c := range iterOrderCases {
		f := funcImpl(t, r, c.src)
		var order []string
		for item, next := f.iterateStringKeys()(); next != nil; item, next = next() {
			order = append(order, item.name.String())
		}
		if got := strings.Join(order, ","); got != c.want {
			t.Errorf("%s: virtual iterator order = %q, want %q", c.src, got, c.want)
		}
	}
}
