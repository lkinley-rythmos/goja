package goja

import (
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
out.join("|");
`

// Captured from the unmodified engine at 43617bb. Must pass before and after Task 2.
const arrayLengthGolden = `0,1,2,length,x/0,1,2,x/0,1,2,x/[1,2,3]/{"value":3,"writable":true,"enumerable":false,"configurable":false}/0,1,2,x/3|false|TypeError|0,1,length,x/0,1,x/0,1,x/[1,2]/{"value":2,"writable":true,"enumerable":false,"configurable":false}/0,1,x/2|0,1,length,x/0,1,x/0,1,x/[1,2]/{"value":2,"writable":false,"enumerable":false,"configurable":false}/0,1,x/2|TypeError|true|{"value":1,"writable":false,"enumerable":false,"configurable":false}|0,length,foo/0,foo/0,foo/[2]/{"value":1,"writable":true,"enumerable":false,"configurable":false}/0,foo/1|100000,length,y|100001|20003|100001|20002|{"value":100001,"writable":true,"enumerable":false,"configurable":false}|length|0|true|false|true`

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
}
