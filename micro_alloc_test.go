package goja

import (
	"testing"
)

// microGoldenScript exercises property-key coercion, defineProperty on new
// and existing keys, and array builtins with indices above 255 and above
// 65536 (the bounds of Go's convT64 cache and goja's int cache).
const microGoldenScript = `
var out = [];
var o = {1: 'one', x: 'ex'}; var keyObj = {toString: function(){ return 'x' }}; var sym = Symbol('q'); o[sym] = 'symv';
out.push(JSON.stringify(Object.getOwnPropertyDescriptor(o, 'x')), JSON.stringify(Object.getOwnPropertyDescriptor(o, 1)), JSON.stringify(Object.getOwnPropertyDescriptor(o, '1')), JSON.stringify(Object.getOwnPropertyDescriptor(o, keyObj)), JSON.stringify(Object.getOwnPropertyDescriptor(o, sym)), o.hasOwnProperty(keyObj), o.hasOwnProperty(sym), o.propertyIsEnumerable(1), Reflect.has(o, keyObj), Reflect.get(o, 1), Reflect.deleteProperty(o, keyObj), Object.keys(o).join(','));
var t = {};
Object.defineProperty(t, 'a', {value: 1, writable: true, enumerable: true, configurable: true});
Object.defineProperty(t, 'b', {value: 2});
Object.defineProperty(t, 'c', {value: 3, writable: true});
Object.defineProperty(t, 'a', {value: 11});
Object.defineProperty(t, 'a', {enumerable: false});
Object.defineProperty(t, 'd', {get: function(){ return 'd' }});
Object.defineProperty(t, 'e', {});
Object.defineProperty(t, 'f', {value: undefined, writable: true, enumerable: true, configurable: true});
out.push(JSON.stringify(Object.getOwnPropertyDescriptors(t)), Object.keys(t).join(','), t.f === undefined && t.hasOwnProperty('f'));
try { Object.defineProperty(t, 'b', {value: 22}) } catch (e) { out.push(e.constructor.name + ':' + e.message) }
try { Object.defineProperty(Object.preventExtensions({}), 'n', {value: 1, writable: true, enumerable: true, configurable: true}) } catch (e) { out.push(e.constructor.name + ':' + e.message) }
out.push(Reflect.defineProperty(Object.preventExtensions({}), 'n', {value: 1, writable: true, enumerable: true, configurable: true}));
var big = new Array(70000); for (var i = 0; i < 70000; i += 7000) big[i] = i;
var idxs = []; big.forEach(function(v, i) { idxs.push(i) }); out.push(idxs.join(','));
out.push(big.findIndex(function(v, i) { return i > 65536 }), big.map(function(v, i) { return i }).filter(function(v) { return v !== undefined }).join(','), big.reduce(function(acc, v, i) { return acc + i }, 0), big.some(function(v, i) { return i === 63000 }), big.every(function(v, i) { return i <= 70000 }), big.findLast(function(v, i) { return true }), big.findLastIndex(function(v, i) { return i < 300 }), big.find(function(v, i) { return i > 255 }));
var dense = []; for (var j = 0; j < 300; j++) dense.push(j);
out.push(dense.filter(function(v, i) { return i >= 255 && i < 258 }).join(','), dense.map(function(v, i) { return i * 2 })[299], dense.indexOf(299), dense.lastIndexOf(256), dense.findIndex(function(v, i) { return i === 256 }));
out.push(Array.from({length: 300}, function(v, i) { return i })[299], [1, 2, 3].flatMap(function(v, i) { return [i] }).join(','), dense.entries().next().value.join(','), Array.prototype.copyWithin.call({length: 3, 0: 'a', 1: 'b', 2: 'c'}, 0, 1)[0]);
var f = function(a, b) { return a - b }; var arr = [3, 1, 2];
out.push(arr.sort(f).join(','), arr.sort(f).join(','), [5, 4].sort(f).join(','));
out.join('|');
`

// microGolden was captured from the unmodified engine at 7f360ba.
const microGolden = `{"value":"ex","writable":true,"enumerable":true,"configurable":true}|{"value":"one","writable":true,"enumerable":true,"configurable":true}|{"value":"one","writable":true,"enumerable":true,"configurable":true}|{"value":"ex","writable":true,"enumerable":true,"configurable":true}|{"value":"symv","writable":true,"enumerable":true,"configurable":true}|true|true|true|true|one|true|1|{"a":{"value":11,"writable":true,"enumerable":false,"configurable":true},"b":{"value":2,"writable":false,"enumerable":false,"configurable":false},"c":{"value":3,"writable":true,"enumerable":false,"configurable":false},"d":{"enumerable":false,"configurable":false},"e":{"writable":false,"enumerable":false,"configurable":false},"f":{"writable":true,"enumerable":true,"configurable":true}}|f|true|TypeError:Cannot redefine property: b|TypeError:Cannot define property n, object is not extensible|false|0,7000,14000,21000,28000,35000,42000,49000,56000,63000|65537|0,7000,14000,21000,28000,35000,42000,49000,56000,63000|315000|true|true||299||255,256,257|598|299|256|256|299|0,1,2|0,0|b|1,2,3|1,2,3|4,5`

func TestMicroCharacterization(t *testing.T) {
	testScript(microGoldenScript, asciiString(microGolden), t)
}

func TestToPropertyKeyNoAlloc(t *testing.T) {
	vm := New()
	for _, key := range []Value{asciiString("abc"), vm.ToValue("ünicode"), NewSymbol("s")} {
		var got Value
		allocs := testing.AllocsPerRun(100, func() { got = vm.toPropertyKey(key) })
		// unicodeString is a slice type, so compare with SameAs, not ==.
		if !got.SameAs(key) {
			t.Fatalf("toPropertyKey(%v) returned a different value %v", key, got)
		}
		if allocs != 0 {
			t.Fatalf("toPropertyKey(%v) allocated %v times per call, want 0", key, allocs)
		}
	}
	// Non-key values are still coerced.
	if got := vm.toPropertyKey(valueInt(42)); got.String() != "42" {
		t.Fatalf("toPropertyKey(42) = %v", got)
	}
}

func TestDefineNewAllTrueNoAlloc(t *testing.T) {
	vm := New()
	o := vm.NewObject().self.(*baseObject)
	descr := PropertyDescriptor{Value: valueTrue, Writable: FLAG_TRUE, Enumerable: FLAG_TRUE, Configurable: FLAG_TRUE}
	// Warm up so the map and the names slice already have capacity; the
	// measured loop then contains only the define itself and its undo.
	o.defineOwnPropertyStr("x", descr, true)
	o.deleteStr("x", true)
	allocs := testing.AllocsPerRun(100, func() {
		o.defineOwnPropertyStr("x", descr, true)
		o.deleteStr("x", true)
	})
	if allocs != 0 {
		t.Fatalf("defining a new all-true data property allocated %v times per call, want 0", allocs)
	}
	o.defineOwnPropertyStr("x", descr, true)
	if v := o.values["x"]; v != valueTrue {
		t.Fatalf("all-true data property must be stored as the bare value, got %T", v)
	}
	// A partial descriptor on a new key still gets a valueProperty.
	o.defineOwnPropertyStr("y", PropertyDescriptor{Value: valueTrue}, true)
	if p, ok := o.values["y"].(*valueProperty); !ok || p.writable || p.enumerable || p.configurable {
		t.Fatalf("partial descriptor on a new key stored as %#v", o.values["y"])
	}
}

func TestAssertCallableCached(t *testing.T) {
	vm := New()
	for _, src := range []string{
		"(function(){ return 1 })",
		"(() => 1)",
		"(async function(){})",
		"(async () => 1)",
		"(class {})",
		"({ m() { return 1 } }).m",
	} {
		v, err := vm.RunString(src)
		if err != nil {
			t.Fatal(err)
		}
		fn := v.(*Object)
		first, ok := fn.self.assertCallable()
		if !ok || first == nil {
			t.Fatalf("%s: not callable", src)
		}
		allocs := testing.AllocsPerRun(100, func() { fn.self.assertCallable() })
		if allocs != 0 {
			t.Fatalf("%s: assertCallable allocated %v times per call, want 0", src, allocs)
		}
	}
	// The cached func is the live function: calling it twice works and
	// AssertFunction sees the same behaviour.
	v, _ := vm.RunString("(function(a) { return a * 2 })")
	callable, _ := AssertFunction(v)
	for i := 0; i < 2; i++ {
		res, err := callable(Undefined(), valueInt(21))
		if err != nil || res.ToInteger() != 42 {
			t.Fatalf("call %d: %v, %v", i, res, err)
		}
	}
}

func BenchmarkArrayFindJSCallback(b *testing.B) {
	vm := New()
	if _, err := vm.RunString(`var arr = []; for (var i = 0; i < 10000; i++) arr.push(i); var pred = function(x) { return x === -1 }`); err != nil {
		b.Fatal(err)
	}
	prg, _ := Compile("b", "arr.find(pred)", false)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := vm.RunProgram(prg); err != nil {
			b.Fatal(err)
		}
	}
}

// TestArrayBuiltinIndexBoxing checks that array builtins do not allocate
// per element when handing the index to a callback: allocations must not
// grow with the array length. Both a native callback (no VM re-entry) and
// a JS callback are used; 300 vs 3000 elements straddles the 256-entry
// convT64 cache so the per-element boxing shows up as ~2700 extra allocs.
func TestArrayBuiltinIndexBoxing(t *testing.T) {
	vm := New()
	mk := func(n int) *Object {
		vals := make([]interface{}, n)
		for i := range vals {
			vals[i] = i
		}
		return vm.NewArray(vals...)
	}
	small, large := mk(300), mk(3000)
	nativeFalse := vm.ToValue(func(call FunctionCall) Value { return valueFalse })
	nativeTrue := vm.ToValue(func(call FunctionCall) Value { return valueTrue })
	nativeAcc := vm.ToValue(func(call FunctionCall) Value { return call.Argument(0) })
	jsFalseV, _ := vm.RunString("(function(v, i) { return false })")
	jsTrueV, _ := vm.RunString("(function(v, i) { return true })")
	jsAccV, _ := vm.RunString("(function(acc, v, i) { return acc })")
	jsFalse, jsTrue, jsAcc := jsFalseV.(*Object), jsTrueV.(*Object), jsAccV.(*Object)
	// every stops at the first false and some at the first true, so each
	// gets the callback that makes it visit every element.
	proto := vm.Get("Array").ToObject(vm).Get("prototype").ToObject(vm)
	for _, tc := range []struct {
		method string
		cb     Value
	}{
		{"forEach", nativeFalse}, {"filter", nativeFalse}, {"some", nativeFalse}, {"every", nativeTrue},
		{"find", nativeFalse}, {"findIndex", nativeFalse}, {"findLast", nativeFalse}, {"findLastIndex", nativeFalse},
		{"reduce", nativeAcc}, {"reduceRight", nativeAcc},
		{"forEach", jsFalse}, {"filter", jsFalse}, {"some", jsFalse}, {"every", jsTrue},
		{"find", jsFalse}, {"findIndex", jsFalse}, {"findLast", jsFalse}, {"findLastIndex", jsFalse},
		{"reduce", jsAcc}, {"reduceRight", jsAcc},
	} {
		fn, ok := AssertFunction(proto.Get(tc.method))
		if !ok {
			t.Fatalf("%s: not a function", tc.method)
		}
		run := func(arr *Object) float64 {
			return testing.AllocsPerRun(10, func() {
				if _, err := fn(arr, tc.cb, valueInt(0)); err != nil {
					panic(err)
				}
			})
		}
		if a, b := run(small), run(large); a != b {
			t.Errorf("%s (%T callback): allocations grow with length: %v for 300 elements, %v for 3000", tc.method, tc.cb.(*Object).self, a, b)
		}
	}
	// map's result array grows its backing slice as it fills (a handful of
	// allocations that scale with log(n)), so it is checked separately: the
	// per-element boxing would add at least 2444 allocations between the
	// two sizes; slice growth adds fewer than 16.
	mapFn, _ := AssertFunction(proto.Get("map"))
	runMap := func(arr *Object, cb Value) float64 {
		return testing.AllocsPerRun(10, func() {
			if _, err := mapFn(arr, cb); err != nil {
				panic(err)
			}
		})
	}
	for _, cb := range []Value{nativeFalse, jsFalse} {
		if a, b := runMap(small, cb), runMap(large, cb); b-a >= 16 {
			t.Errorf("map (%T callback): %v allocations for 300 elements, %v for 3000", cb.(*Object).self, a, b)
		}
	}
}
