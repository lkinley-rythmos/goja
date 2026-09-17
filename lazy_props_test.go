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
