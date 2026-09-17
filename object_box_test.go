package goja

import "testing"

// coallocGoldenScript pins behaviour that depends on an object's impl and
// its *Object staying consistent across representation changes: dense
// to sparse arrays and back, generators, arrays as prototypes, prototype
// chains, functions/arrows/methods and their own properties, descriptor
// objects, array iterators, for-of, JSON, classes, and the array builtins
// that create arrays.
const coallocGoldenScript = `
var out = [];
var a = [1, 2, 3]; a[1000000] = 'far'; out.push(a.length + '/' + a[1000000] + '/' + a[1] + '/' + Object.keys(a).join(',') + '/' + a.indexOf('far'));
a.length = 3; out.push(a.length + '/' + JSON.stringify(a) + '/' + Object.keys(a).join(','));
delete a[1]; out.push(JSON.stringify(a) + '/' + (1 in a) + '/' + a.length);
var b = []; b[10] = 1; b.length = 0; b.push(5); out.push(JSON.stringify(b));
function* gen() { yield 1; yield 2 } var g = gen(); out.push(g.next().value + '/' + g.next().value + '/' + g.next().done + '/' + Object.prototype.toString.call(g));
var protoArr = [1, 2]; var child = Object.create(protoArr); child.push(3); out.push(child.length + '/' + JSON.stringify(protoArr) + '/' + Array.isArray(child) + '/' + child[0]);
var chain = Object.create(Object.create({deep: 1})); out.push(chain.deep + '/' + Object.getPrototypeOf(Object.getPrototypeOf(chain)).deep);
var f = function(x) { return x * 2 }; var arrow = (x) => x + 1; var obj = { m(x) { return x - 1 } };
out.push(f(2) + '/' + arrow(2) + '/' + obj.m(2) + '/' + typeof f + '/' + f.name + '/' + arrow.name + '/' + obj.m.name + '/' + f.length + '/' + (f instanceof Function) + '/' + Object.prototype.toString.call(arrow) + '/' + (f.prototype.constructor === f));
f.custom = 1; arrow.custom = 2; out.push(f.custom + '/' + arrow.custom + '/' + Object.keys(f).join(',') + '/' + ('prototype' in arrow) + '/' + ('prototype' in obj.m));
var d = Object.getOwnPropertyDescriptor({q: 1}, 'q'); d.extra = 1; out.push(JSON.stringify(d) + '/' + Object.keys(d).join(','));
var it = [1, 2][Symbol.iterator](); out.push(it.next().value + '/' + it.next().value + '/' + it.next().done + '/' + Object.prototype.toString.call(it) + '/' + (it[Symbol.iterator]() === it));
var seen = []; for (var v of [7, 8]) seen.push(v); for (var [k, w] of [['a', 1]]) seen.push(k + w); for (var e of [9].entries()) seen.push(e.join(':')); out.push(seen.join(','));
out.push(JSON.stringify({a: [1, {b: 2}], f: function() {}, u: undefined}), JSON.stringify([1, , 3]), JSON.parse('{"x":[1,2,{"y":3}]}').x[2].y);
var cls = class K { constructor() { this.v = 1 } }; out.push(new cls().v + '/' + (new cls() instanceof cls));
out.push([1, 2, 3].map(function(x) { return x * 2 }).filter(function(x) { return x > 2 }).join(','), Array.from({length: 3}, function(_, i) { return i }).join(','), [3, 1, 2].sort().join(','), [1, 2, 3].slice(1).concat([4]).join(','), Array.isArray(new Array(2)), new Array(2).length);
var ctor = function C() { this.x = 1 }; ctor.prototype.m = function() { return this.x }; var inst = new ctor(); out.push(inst.m() + '/' + (Object.getPrototypeOf(inst) === ctor.prototype) + '/' + inst.constructor.name);
var fr = Object.freeze([1, 2]); try { 'use strict'; (function() { 'use strict'; fr.push(3) })(); out.push('nothrow') } catch (e) { out.push(e.constructor.name) }
out.push(Object.isFrozen(fr) + '/' + Object.isFrozen(Object.freeze({a: 1})) + '/' + Object.isSealed(Object.seal(function() {})));
out.join('|');
`

// coallocGolden was captured from the unmodified engine at e41cf0f.
const coallocGolden = `1000001/far/2/0,1,2,1000000/1000000|3/[1,2,3]/0,1,2|[1,null,3]/false/3|[5]|1/2/true/[object Generator]|3/[1,2]/false/1|1/1|4/3/1/function/f/arrow/m/1/true/[object Function]/true|1/2/custom/false/false|{"value":1,"writable":true,"enumerable":true,"configurable":true,"extra":1}/value,writable,enumerable,configurable,extra|1/2/true/[object Array Iterator]/true|7,8,a1,0:9|{"a":[1,{"b":2}]}|[1,null,3]|3|1/true|4,6|0,1,2|1,2,3|2,3,4|true|2|1/true/C|TypeError|true/true/true`

func TestCoallocCharacterization(t *testing.T) {
	runGolden(t, "coalloc", coallocGoldenScript, coallocGolden) // runGolden: string_coerce_test.go (Task 1)
}

// checkWiring asserts the invariant every boxed constructor must satisfy:
// the impl's val is the Object and the Object's self is the impl.
func checkWiring(t *testing.T, what string, obj *Object, impl objectImpl) {
	t.Helper()
	if obj.self != impl {
		t.Fatalf("%s: obj.self is %T, want the impl", what, obj.self)
	}
	switch i := impl.(type) {
	case *baseObject:
		if i.val != obj {
			t.Fatalf("%s: impl.val != obj", what)
		}
	case *arrayObject:
		if i.val != obj {
			t.Fatalf("%s: impl.val != obj", what)
		}
	case *funcObject:
		if i.val != obj {
			t.Fatalf("%s: impl.val != obj", what)
		}
	case *arrowFuncObject:
		if i.val != obj {
			t.Fatalf("%s: impl.val != obj", what)
		}
	case *methodFuncObject:
		if i.val != obj {
			t.Fatalf("%s: impl.val != obj", what)
		}
	case *descriptorObject:
		if i.val != obj {
			t.Fatalf("%s: impl.val != obj", what)
		}
	case *arrayIterObject:
		if i.val != obj {
			t.Fatalf("%s: impl.val != obj", what)
		}
	default:
		t.Fatalf("%s: unexpected impl type %T", what, impl)
	}
}

func TestPlainObjectBox(t *testing.T) {
	vm := New()
	obj := vm.NewObject()
	checkWiring(t, "NewObject", obj, obj.self)
	if allocs := testing.AllocsPerRun(100, func() { vm.NewObject() }); allocs != 1 {
		t.Fatalf("NewObject allocated %v times, want 1", allocs)
	}
	sized := vm.NewObjectWithCapacity(4)
	checkWiring(t, "NewObjectWithCapacity", sized, sized.self)
	created := vm.CreateObject(obj)
	checkWiring(t, "CreateObject", created, created.self)
	if created.Prototype() != obj {
		t.Fatal("CreateObject prototype")
	}
	// Object identity is stable and the impl stays reachable through it.
	_ = obj.Set("a", 1)
	if v, ok := obj.GetOwnDataValue("a"); !ok || v.ToInteger() != 1 {
		t.Fatalf("Set/GetOwnDataValue through the box: %v %v", v, ok)
	}
	for _, tc := range []struct {
		src  string
		want float64
	}{
		{"({})", 1},
		{"({a: 1, b: 2, c: 3})", 4},
		{"Object.create(null)", 1},
	} {
		if got := measureProgramAllocs(t, vm, tc.src); got != tc.want {
			t.Errorf("%s: %v allocations, want %v", tc.src, got, tc.want)
		}
	}
}

func TestArrayBox(t *testing.T) {
	vm := New()
	arr := vm.NewArray(1, 2)
	checkWiring(t, "NewArray", arr, arr.self)
	if allocs := testing.AllocsPerRun(100, func() { vm.NewArray() }); allocs != 1 {
		t.Fatalf("NewArray() allocated %v times, want 1", allocs)
	}
	// Identity survives the dense -> sparse swap; the box's original impl
	// is simply replaced in obj.self.
	v, err := vm.RunString(`var sw = [1, 2, 3]; sw`)
	if err != nil {
		t.Fatal(err)
	}
	sw := v.(*Object)
	if _, ok := sw.self.(*arrayObject); !ok {
		t.Fatalf("fresh array impl is %T", sw.self)
	}
	if _, err := vm.RunString(`sw[1000000] = 1`); err != nil {
		t.Fatal(err)
	}
	if _, ok := sw.self.(*sparseArrayObject); !ok {
		t.Fatalf("after sparse conversion impl is %T", sw.self)
	}
	// Shrinking does not convert back (that needs >= 1024 items), but the
	// Object identity and contents must be intact behind the swapped impl.
	if _, err := vm.RunString(`sw.length = 3`); err != nil {
		t.Fatal(err)
	}
	if got := vm.Get("sw"); got != Value(sw) {
		t.Fatal("array identity changed across the representation swap")
	}
	if keys := sw.Keys(); len(keys) != 3 || keys[0] != "0" {
		t.Fatalf("keys after the swap: %v", keys)
	}
	if v, ok := sw.GetOwnDataValue("2"); !ok || v.ToInteger() != 3 {
		t.Fatalf("element after the swap: %v %v", v, ok)
	}
	if _, err := vm.RunString(`var arr2 = [1, 2]`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		src  string
		want float64
	}{
		{"[]", 1},
		{"[1, 2, 3]", 2},
		{"for (var x of arr2) {}", 4},
		{"arr2[Symbol.iterator]()", 1},
	} {
		if got := measureProgramAllocs(t, vm, tc.src); got != tc.want {
			t.Errorf("%s: %v allocations, want %v", tc.src, got, tc.want)
		}
	}
	it, _ := vm.RunString(`arr2[Symbol.iterator]()`)
	checkWiring(t, "array iterator", it.(*Object), it.(*Object).self)
}
