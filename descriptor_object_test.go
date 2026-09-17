package goja

import (
	"reflect"
	"testing"
)

// descriptorGoldenScript observes descriptor objects every way the engine
// exposes them (keys, JSON, in/hasOwnProperty, entries, for-in, toString,
// prototype, extensibility) across data/accessor/partial/absent/index/
// symbol/Proxy sources, then mutates them in every way that must
// materialize (assign, add, delete, defineProperty, freeze, seal,
// preventExtensions, setPrototypeOf, symbol keys, assign/spread, for-in
// with mid-iteration mutation, self-reference, descriptor-of-descriptor).
const descriptorGoldenScript = `
var out = [];
function show(d) {
  if (d === undefined) return 'undef';
  var ks = []; for (var k in d) ks.push(k);
  return Object.keys(d).join(',') + '/' + JSON.stringify(d) + '/' + Object.getOwnPropertyNames(d).join(',') + '/' + Object.prototype.toString.call(d) + '/' + (Object.getPrototypeOf(d) === Object.prototype) + '/' + Object.isExtensible(d) + '/' + ('value' in d) + ',' + ('get' in d) + ',' + d.hasOwnProperty('enumerable') + ',' + d.hasOwnProperty('toString') + '/' + Object.entries(d).map(function(e){ return e[0] + '=' + (typeof e[1] === 'function' ? 'fn' : String(e[1])) }).join(';') + '/' + ks.join(',');
}
var gopd = Object.getOwnPropertyDescriptor;
var o = {a: 1};
Object.defineProperty(o, 'b', {value: 2, writable: false, enumerable: false, configurable: false});
Object.defineProperty(o, 'c', {get: function g(){ return 3 }, set: function s(v){}, enumerable: true, configurable: true});
Object.defineProperty(o, 'd', {get: function g2(){ return 4 }});
Object.defineProperty(o, 'e', {set: function s2(v){}});
var sym = Symbol('s'); o[sym] = 5;
var arr = [1, 2, 3];
out.push(show(gopd(o, 'a')), show(gopd(o, 'b')), show(gopd(o, 'c')), show(gopd(o, 'd')), show(gopd(o, 'e')), show(gopd(o, sym)), show(gopd(o, 'zz')), show(gopd(arr, '1')), show(gopd(arr, 'length')), show(gopd(function f(x){}, 'name')), show(Reflect.getOwnPropertyDescriptor(o, 'a')));
var all = Object.getOwnPropertyDescriptors(o);
out.push(Object.keys(all).join(',') + '/' + JSON.stringify(all));
var p = new Proxy({x: 1}, {getOwnPropertyDescriptor: function(t, k) { out.push('trap:' + String(k)); return Reflect.getOwnPropertyDescriptor(t, k) }});
out.push(show(gopd(p, 'x')));
var d = gopd(o, 'a');
d.value = 9; out.push(show(d));
d.writable = false; out.push(show(d));
d.extra = 'x'; out.push(show(d));
delete d.value; out.push(show(d));
d.value = 10; out.push(show(d));
var d2 = gopd(o, 'a');
delete d2.enumerable; out.push(show(d2));
Object.defineProperty(d2, 'writable', {enumerable: false}); out.push(show(d2) + '/' + JSON.stringify(gopd(d2, 'writable')));
var d3 = gopd(o, 'a');
Object.defineProperty(d3, 'value', {get: function(){ return 'g' }}); out.push(show(d3) + '/' + JSON.stringify(gopd(d3, 'value')));
var d4 = gopd(o, 'c');
d4.get = null; out.push(show(d4));
d4[0] = 'zero'; out.push(show(d4));
var d5 = gopd(o, 'a');
Object.freeze(d5); out.push(show(d5) + '/' + Object.isFrozen(d5) + '/' + JSON.stringify(gopd(d5, 'value')));
try { (function(){ 'use strict'; d5.value = 1 })(); out.push('nothrow') } catch (e) { out.push(e.constructor.name) }
var d6 = gopd(o, 'a');
Object.seal(d6); out.push(show(d6) + '/' + Object.isSealed(d6) + '/' + (delete d6.value));
var d7 = gopd(o, 'a');
Object.preventExtensions(d7); d7.zz = 1; out.push(show(d7));
var d8 = gopd(o, 'a');
Object.setPrototypeOf(d8, {inherited: true}); out.push(show(d8) + '/' + d8.inherited + '/' + ('inherited' in d8) + '/' + d8.hasOwnProperty('inherited'));
var d9 = gopd(o, 'a');
d9[Symbol.iterator] = 1; out.push(show(d9) + '/' + Object.getOwnPropertySymbols(d9).length);
var d10 = gopd(o, 'a');
out.push(JSON.stringify(Object.assign({}, d10)) + '/' + JSON.stringify(Object.assign(d10, {value: 'assigned'})));
var d11 = gopd(o, 'a');
var spread = {...d11}; out.push(JSON.stringify(spread));
var d12 = gopd(o, 'a');
out.push(Object.getOwnPropertyNames(d12).join(',') + '/' + Reflect.ownKeys(d12).join(','));
var d13 = gopd(o, 'a'); var seen = [];
for (var k in d13) { seen.push(k); if (k === 'value') { delete d13.enumerable; d13.late = 1 } }
out.push(seen.join(','));
var t = {}; Object.defineProperty(t, 'q', gopd(o, 'b')); out.push(JSON.stringify(gopd(t, 'q')));
var t2 = {}; Object.defineProperty(t2, 'q', gopd(o, 'c')); out.push(typeof gopd(t2, 'q').get + '/' + gopd(t2, 'q').enumerable);
var da = gopd(o, 'a'), db = gopd(o, 'a'); out.push((da === db) + '/' + (da === da) + '/' + (da == db));
out.push(show(gopd(gopd(o, 'a'), 'value')) + '/' + show(gopd(gopd(o, 'a'), 'nope')));
var d14 = gopd(o, 'a'); d14.value = d14; out.push(Object.keys(d14).join(',') + '/' + (d14.value === d14));
var d15 = gopd(o, 'a'); out.push(Object.getOwnPropertyDescriptors(d15).value.value + '/' + Object.keys(Object.getOwnPropertyDescriptors(d15)).join(','));
out.join('|');
`

// descriptorGolden was captured from the unmodified engine at 7f360ba.
const descriptorGolden = `value,writable,enumerable,configurable/{"value":1,"writable":true,"enumerable":true,"configurable":true}/value,writable,enumerable,configurable/[object Object]/true/true/true,false,true,false/value=1;writable=true;enumerable=true;configurable=true/value,writable,enumerable,configurable|value,writable,enumerable,configurable/{"value":2,"writable":false,"enumerable":false,"configurable":false}/value,writable,enumerable,configurable/[object Object]/true/true/true,false,true,false/value=2;writable=false;enumerable=false;configurable=false/value,writable,enumerable,configurable|get,set,enumerable,configurable/{"enumerable":true,"configurable":true}/get,set,enumerable,configurable/[object Object]/true/true/false,true,true,false/get=fn;set=fn;enumerable=true;configurable=true/get,set,enumerable,configurable|get,set,enumerable,configurable/{"enumerable":false,"configurable":false}/get,set,enumerable,configurable/[object Object]/true/true/false,true,true,false/get=fn;set=undefined;enumerable=false;configurable=false/get,set,enumerable,configurable|get,set,enumerable,configurable/{"enumerable":false,"configurable":false}/get,set,enumerable,configurable/[object Object]/true/true/false,true,true,false/get=undefined;set=fn;enumerable=false;configurable=false/get,set,enumerable,configurable|value,writable,enumerable,configurable/{"value":5,"writable":true,"enumerable":true,"configurable":true}/value,writable,enumerable,configurable/[object Object]/true/true/true,false,true,false/value=5;writable=true;enumerable=true;configurable=true/value,writable,enumerable,configurable|undef|value,writable,enumerable,configurable/{"value":2,"writable":true,"enumerable":true,"configurable":true}/value,writable,enumerable,configurable/[object Object]/true/true/true,false,true,false/value=2;writable=true;enumerable=true;configurable=true/value,writable,enumerable,configurable|value,writable,enumerable,configurable/{"value":3,"writable":true,"enumerable":false,"configurable":false}/value,writable,enumerable,configurable/[object Object]/true/true/true,false,true,false/value=3;writable=true;enumerable=false;configurable=false/value,writable,enumerable,configurable|value,writable,enumerable,configurable/{"value":"f","writable":false,"enumerable":false,"configurable":true}/value,writable,enumerable,configurable/[object Object]/true/true/true,false,true,false/value=f;writable=false;enumerable=false;configurable=true/value,writable,enumerable,configurable|value,writable,enumerable,configurable/{"value":1,"writable":true,"enumerable":true,"configurable":true}/value,writable,enumerable,configurable/[object Object]/true/true/true,false,true,false/value=1;writable=true;enumerable=true;configurable=true/value,writable,enumerable,configurable|a,b,c,d,e/{"a":{"value":1,"writable":true,"enumerable":true,"configurable":true},"b":{"value":2,"writable":false,"enumerable":false,"configurable":false},"c":{"enumerable":true,"configurable":true},"d":{"enumerable":false,"configurable":false},"e":{"enumerable":false,"configurable":false}}|trap:x|value,writable,enumerable,configurable/{"value":1,"writable":true,"enumerable":true,"configurable":true}/value,writable,enumerable,configurable/[object Object]/true/true/true,false,true,false/value=1;writable=true;enumerable=true;configurable=true/value,writable,enumerable,configurable|value,writable,enumerable,configurable/{"value":9,"writable":true,"enumerable":true,"configurable":true}/value,writable,enumerable,configurable/[object Object]/true/true/true,false,true,false/value=9;writable=true;enumerable=true;configurable=true/value,writable,enumerable,configurable|value,writable,enumerable,configurable/{"value":9,"writable":false,"enumerable":true,"configurable":true}/value,writable,enumerable,configurable/[object Object]/true/true/true,false,true,false/value=9;writable=false;enumerable=true;configurable=true/value,writable,enumerable,configurable|value,writable,enumerable,configurable,extra/{"value":9,"writable":false,"enumerable":true,"configurable":true,"extra":"x"}/value,writable,enumerable,configurable,extra/[object Object]/true/true/true,false,true,false/value=9;writable=false;enumerable=true;configurable=true;extra=x/value,writable,enumerable,configurable,extra|writable,enumerable,configurable,extra/{"writable":false,"enumerable":true,"configurable":true,"extra":"x"}/writable,enumerable,configurable,extra/[object Object]/true/true/false,false,true,false/writable=false;enumerable=true;configurable=true;extra=x/writable,enumerable,configurable,extra|writable,enumerable,configurable,extra,value/{"writable":false,"enumerable":true,"configurable":true,"extra":"x","value":10}/writable,enumerable,configurable,extra,value/[object Object]/true/true/true,false,true,false/writable=false;enumerable=true;configurable=true;extra=x;value=10/writable,enumerable,configurable,extra,value|value,writable,configurable/{"value":1,"writable":true,"configurable":true}/value,writable,configurable/[object Object]/true/true/true,false,false,false/value=1;writable=true;configurable=true/value,writable,configurable|value,configurable/{"value":1,"configurable":true}/value,writable,configurable/[object Object]/true/true/true,false,false,false/value=1;configurable=true/value,configurable/{"value":true,"writable":true,"enumerable":false,"configurable":true}|value,writable,enumerable,configurable/{"value":"g","writable":true,"enumerable":true,"configurable":true}/value,writable,enumerable,configurable/[object Object]/true/true/true,false,true,false/value=g;writable=true;enumerable=true;configurable=true/value,writable,enumerable,configurable/{"enumerable":true,"configurable":true}|get,set,enumerable,configurable/{"get":null,"enumerable":true,"configurable":true}/get,set,enumerable,configurable/[object Object]/true/true/false,true,true,false/get=null;set=fn;enumerable=true;configurable=true/get,set,enumerable,configurable|0,get,set,enumerable,configurable/{"0":"zero","get":null,"enumerable":true,"configurable":true}/0,get,set,enumerable,configurable/[object Object]/true/true/false,true,true,false/0=zero;get=null;set=fn;enumerable=true;configurable=true/0,get,set,enumerable,configurable|value,writable,enumerable,configurable/{"value":1,"writable":true,"enumerable":true,"configurable":true}/value,writable,enumerable,configurable/[object Object]/true/false/true,false,true,false/value=1;writable=true;enumerable=true;configurable=true/value,writable,enumerable,configurable/true/{"value":1,"writable":false,"enumerable":true,"configurable":false}|TypeError|value,writable,enumerable,configurable/{"value":1,"writable":true,"enumerable":true,"configurable":true}/value,writable,enumerable,configurable/[object Object]/true/false/true,false,true,false/value=1;writable=true;enumerable=true;configurable=true/value,writable,enumerable,configurable/true/false|value,writable,enumerable,configurable/{"value":1,"writable":true,"enumerable":true,"configurable":true}/value,writable,enumerable,configurable/[object Object]/true/false/true,false,true,false/value=1;writable=true;enumerable=true;configurable=true/value,writable,enumerable,configurable|value,writable,enumerable,configurable/{"value":1,"writable":true,"enumerable":true,"configurable":true}/value,writable,enumerable,configurable/[object Object]/false/true/true,false,true,false/value=1;writable=true;enumerable=true;configurable=true/value,writable,enumerable,configurable,inherited/true/true/false|value,writable,enumerable,configurable/{"value":1,"writable":true,"enumerable":true,"configurable":true}/value,writable,enumerable,configurable/[object Object]/true/true/true,false,true,false/value=1;writable=true;enumerable=true;configurable=true/value,writable,enumerable,configurable/1|{"value":1,"writable":true,"enumerable":true,"configurable":true}/{"value":"assigned","writable":true,"enumerable":true,"configurable":true}|{"value":1,"writable":true,"enumerable":true,"configurable":true}|value,writable,enumerable,configurable/value,writable,enumerable,configurable|value,writable,configurable|{"value":2,"writable":false,"enumerable":false,"configurable":false}|function/true|false/true/false|value,writable,enumerable,configurable/{"value":1,"writable":true,"enumerable":true,"configurable":true}/value,writable,enumerable,configurable/[object Object]/true/true/true,false,true,false/value=1;writable=true;enumerable=true;configurable=true/value,writable,enumerable,configurable/undef|value,writable,enumerable,configurable/true|1/value,writable,enumerable,configurable`

func TestDescriptorCharacterization(t *testing.T) {
	testScript(descriptorGoldenScript, asciiString(descriptorGolden), t)
}

func gopdCallable(t *testing.T, vm *Runtime) Callable {
	t.Helper()
	fn, ok := AssertFunction(vm.Get("Object").ToObject(vm).Get("getOwnPropertyDescriptor"))
	if !ok {
		t.Fatal("Object.getOwnPropertyDescriptor is not callable")
	}
	return fn
}

// TestDescriptorObjectAllocs: a descriptor costs exactly two allocations
// (the Object and the descriptorObject) beyond what a miss (undefined)
// costs, which is the Callable wrapper's own overhead.
func TestDescriptorObjectAllocs(t *testing.T) {
	vm := New()
	gopd := gopdCallable(t, vm)
	target := vm.NewObject()
	if err := target.Set("a", 1); err != nil {
		t.Fatal(err)
	}
	if err := target.DefineAccessorProperty("acc", vm.ToValue(func(FunctionCall) Value { return valueTrue }), nil, FLAG_TRUE, FLAG_TRUE); err != nil {
		t.Fatal(err)
	}
	hit, acc, miss := vm.ToValue("a"), vm.ToValue("acc"), vm.ToValue("nope")
	run := func(key Value) float64 {
		return testing.AllocsPerRun(100, func() {
			if _, err := gopd(Undefined(), target, key); err != nil {
				panic(err)
			}
		})
	}
	base := run(miss)
	for _, key := range []Value{hit, acc} {
		if got := run(key) - base; got != 2 {
			t.Errorf("descriptor for %v: %v allocations beyond a miss, want 2", key, got)
		}
	}
}

func TestDescriptorObjectGoAPI(t *testing.T) {
	vm := New()
	gopd := gopdCallable(t, vm)
	target := vm.NewObject()
	_ = target.Set("a", 1)
	dv, err := gopd(Undefined(), target, vm.ToValue("a"))
	if err != nil {
		t.Fatal(err)
	}
	d := dv.(*Object)
	impl, ok := d.self.(*descriptorObject)
	if !ok {
		t.Fatalf("descriptor is a %T, want *descriptorObject", d.self)
	}
	want := []string{"value", "writable", "enumerable", "configurable"}
	check := func(stage string) {
		t.Helper()
		if keys := d.Keys(); !reflect.DeepEqual(keys, want) {
			t.Fatalf("%s: Keys() = %v", stage, keys)
		}
		if keys := d.AppendKeys(nil); !reflect.DeepEqual(keys, want) {
			t.Fatalf("%s: AppendKeys() = %v", stage, keys)
		}
		if names := d.GetOwnPropertyNames(); !reflect.DeepEqual(names, want) {
			t.Fatalf("%s: GetOwnPropertyNames() = %v", stage, names)
		}
		if v, ok := d.GetOwnDataValue("value"); !ok || v.ToInteger() != 1 {
			t.Fatalf("%s: GetOwnDataValue(value) = %v, %v", stage, v, ok)
		}
		if v, ok := d.GetOwnDataValue("get"); ok || v != nil {
			t.Fatalf("%s: GetOwnDataValue(get) = %v, %v", stage, v, ok)
		}
		m, ok := d.Export().(map[string]interface{})
		if !ok || len(m) != 4 || m["value"] != int64(1) || m["writable"] != true {
			t.Fatalf("%s: Export() = %#v", stage, d.Export())
		}
		if d.ClassName() != classObject || d.Prototype() != vm.global.ObjectPrototype {
			t.Fatalf("%s: class %q proto ok=%v", stage, d.ClassName(), d.Prototype() == vm.global.ObjectPrototype)
		}
	}
	check("virtual")
	// Export materializes (it walks the map); everything still agrees.
	if !impl.materialized {
		t.Fatal("Export must materialize the descriptor object")
	}
	check("materialized")
	if len(impl.values) != 4 || len(impl.propNames) != 4 || cap(impl.propNames) != 5 {
		t.Fatalf("materialized storage: values=%d names=%d cap=%d", len(impl.values), len(impl.propNames), cap(impl.propNames))
	}

	// A fresh descriptor stays virtual through reads and plain assignment
	// to its own keys, and materializes on a new key.
	dv, _ = gopd(Undefined(), target, vm.ToValue("a"))
	d = dv.(*Object)
	impl = d.self.(*descriptorObject)
	_ = d.Get("value")
	_ = d.Set("value", 7)
	_ = d.Set("writable", false)
	if impl.materialized {
		t.Fatal("reads and assignments to the four keys must not materialize")
	}
	if v, _ := d.GetOwnDataValue("value"); v.ToInteger() != 7 {
		t.Fatalf("value after assignment: %v", v)
	}
	if v := d.Get("writable"); v != valueFalse {
		t.Fatalf("writable after assignment: %v", v)
	}
	_ = d.Set("writable", "yes") // non-boolean flag value: materializes
	if !impl.materialized || d.Get("writable").String() != "yes" {
		t.Fatalf("non-boolean flag assignment: materialized=%v writable=%v", impl.materialized, d.Get("writable"))
	}
	dv, _ = gopd(Undefined(), target, vm.ToValue("a"))
	d = dv.(*Object)
	impl = d.self.(*descriptorObject)
	_ = d.Set("extra", 1)
	if !impl.materialized {
		t.Fatal("a new key must materialize")
	}
	if keys := d.Keys(); !reflect.DeepEqual(keys, append(want, "extra")) {
		t.Fatalf("keys after new key: %v", keys)
	}

	// Descriptor objects of accessor properties.
	_ = target.DefineAccessorProperty("acc", vm.ToValue(func(FunctionCall) Value { return valueTrue }), nil, FLAG_TRUE, FLAG_TRUE)
	dv, _ = gopd(Undefined(), target, vm.ToValue("acc"))
	d = dv.(*Object)
	if keys := d.Keys(); !reflect.DeepEqual(keys, []string{"get", "set", "enumerable", "configurable"}) {
		t.Fatalf("accessor descriptor keys: %v", keys)
	}
	if d.Get("set") != _undefined || d.Get("get").ExportType() == nil {
		t.Fatalf("accessor descriptor get/set: %v / %v", d.Get("get"), d.Get("set"))
	}
}

func BenchmarkGetOwnPropertyDescriptor(b *testing.B) {
	vm := New()
	if _, err := vm.RunString(`var o = {a: 1}; var gopd = Object.getOwnPropertyDescriptor`); err != nil {
		b.Fatal(err)
	}
	prg, _ := Compile("b", "gopd(o, 'a').value", false)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := vm.RunProgram(prg); err != nil {
			b.Fatal(err)
		}
	}
}
