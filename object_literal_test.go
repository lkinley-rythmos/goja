package goja

import "testing"

// literalGoldenScript pins the key order, values and enumeration of
// object literals across every property form the compiler counts or
// skips: keyed values, shorthand, computed keys, methods, getter/setter
// pairs, __proto__, spread (before/after/between), duplicate keys, more
// than eight keys, numeric-string keys, nesting, symbol keys and
// function-name inference.
const literalGoldenScript = `
var out = [];
function show(o) { var ks = []; for (var k in o) ks.push(k); return Object.keys(o).join(',') + '/' + JSON.stringify(o) + '/' + Object.getOwnPropertyNames(o).join(',') + '/' + ks.join(','); }
var g = 1;
var c = 'comp';
out.push(show({}), show({a: 1}), show({a: 1, b: 2, c: 3}), show({b: 2, a: 1, 1: 'one', 0: 'zero'}), show({a: 1, a: 2}), show({g, c}), show({[c]: 1, x: 2}), show({x: 1, [c + '2']: 2, y: 3}), show({m() { return 1 }, get q() { return 2 }, set q(v) {}, z: 3}), show({a: 1, ...{b: 2, c: 3}, d: 4}), show({...{a: 1}, a: 2}), show({__proto__: {inh: 1}, own: 2}), show({__proto__: null, x: 1}));
var big = {k1: 1, k2: 2, k3: 3, k4: 4, k5: 5, k6: 6, k7: 7, k8: 8, k9: 9, k10: 10, k11: 11, k12: 12}; out.push(show(big));
var nested = {a: {b: {c: [1, {d: 2}]}}, e: 3}; out.push(show(nested), show(nested.a), show(nested.a.b.c[1]));
var p = {__proto__: {inh: 1}, own: 2}; out.push(p.inh + '/' + Object.getPrototypeOf(p).inh + '/' + ('inh' in p) + '/' + p.hasOwnProperty('inh'));
var n = {x: 1}; n.y = 2; delete n.x; n.x = 3; out.push(show(n));
var desc = Object.getOwnPropertyDescriptors({a: 1, get b() { return 2 }, m() {}}); out.push(Object.keys(desc).join(',') + '/' + JSON.stringify(desc.a) + '/' + typeof desc.b.get + '/' + desc.m.enumerable);
var sym = Symbol('s'); var ws = {[sym]: 1, a: 2}; out.push(show(ws) + '/' + Object.getOwnPropertySymbols(ws).length);
function mk(i) { return {i: i, sq: i * i} } var arr = []; for (var i = 0; i < 20; i++) arr.push(mk(i)); out.push(JSON.stringify(arr.map(function(o) { return o.sq })));
var withMethodName = {f: function() {}, ar: () => 1, [c]: function() {}}; out.push(withMethodName.f.name + '/' + withMethodName.ar.name + '/' + withMethodName.comp.name);
out.push(Object.isExtensible({}), Object.getPrototypeOf({}) === Object.prototype, Object.prototype.toString.call({a: 1}));
out.join('|');
`

// literalGolden was captured from the unmodified engine at e41cf0f.
const literalGolden = `/{}//|a/{"a":1}/a/a|a,b,c/{"a":1,"b":2,"c":3}/a,b,c/a,b,c|0,1,b,a/{"0":"zero","1":"one","b":2,"a":1}/0,1,b,a/0,1,b,a|a/{"a":2}/a/a|g,c/{"g":1,"c":"comp"}/g,c/g,c|comp,x/{"comp":1,"x":2}/comp,x/comp,x|x,comp2,y/{"x":1,"comp2":2,"y":3}/x,comp2,y/x,comp2,y|m,q,z/{"q":2,"z":3}/m,q,z/m,q,z|a,b,c,d/{"a":1,"b":2,"c":3,"d":4}/a,b,c,d/a,b,c,d|a/{"a":2}/a/a|own/{"own":2}/own/own,inh|x/{"x":1}/x/x|k1,k2,k3,k4,k5,k6,k7,k8,k9,k10,k11,k12/{"k1":1,"k2":2,"k3":3,"k4":4,"k5":5,"k6":6,"k7":7,"k8":8,"k9":9,"k10":10,"k11":11,"k12":12}/k1,k2,k3,k4,k5,k6,k7,k8,k9,k10,k11,k12/k1,k2,k3,k4,k5,k6,k7,k8,k9,k10,k11,k12|a,e/{"a":{"b":{"c":[1,{"d":2}]}},"e":3}/a,e/a,e|b/{"b":{"c":[1,{"d":2}]}}/b/b|d/{"d":2}/d/d|1/1/true/false|y,x/{"y":2,"x":3}/y,x/y,x|a,b,m/{"value":1,"writable":true,"enumerable":true,"configurable":true}/function/true|a/{"a":2}/a/a/1|[0,1,4,9,16,25,36,49,64,81,100,121,144,169,196,225,256,289,324,361]|f/ar/comp|true|true|[object Object]`

func TestObjectLiteralCharacterization(t *testing.T) {
	runGolden(t, "literal", literalGoldenScript, literalGolden) // runGolden: string_coerce_test.go (Task 1)
}

// TestObjectLiteralPresized: a literal with k statically-known keys is
// created with room for k, so initialising it neither grows the map nor
// the key list. Before (e41cf0f): {} = 2 (Object + baseObject);
// {a,b,c} = 7 (2 + map header + first group + 3 propNames growths);
// ten keys = 12. After pre-sizing (9686db5, still two allocations per
// object): {} = 2; {a,b,c} = 5 (2 + header + first group on the first
// insert + one propNames allocation); ten keys = 7 (2 + the 4 allocations
// Go makes creating a map hinted above 8 + one propNames allocation).
// After Task 3 boxes the object and its impl into one allocation, each
// case measures one fewer: {} = 1; {a,b,c} = 4; ten keys = 6.
func TestObjectLiteralPresized(t *testing.T) {
	vm := New()
	for _, tc := range []struct {
		src  string
		want float64
	}{
		{"({})", 1},
		{"({a: 1, b: 2, c: 3})", 4},
		{"({a: 1, b: 2, c: 3, d: 4, e: 5, f: 6, g: 7, h: 8, i: 9, j: 10})", 6},
	} {
		if got := measureProgramAllocs(t, vm, tc.src); got != tc.want {
			t.Errorf("%s: %v allocations, want %v", tc.src, got, tc.want)
		}
	}
	// The compiler's count: computed keys and spreads are not counted,
	// __proto__ and shorthand are; getter+setter on one key counts twice.
	// Verified through the opcode the compiler emits.
	for _, tc := range []struct {
		src  string
		want uint32
	}{
		{"({})", 0},
		{"({a: 1, b: 2})", 2},
		{"({a: 1, [k]: 2, b: 3})", 2},
		{"({a, b})", 2},
		{"({...o, a: 1})", 1},
		{"({__proto__: p, a: 1})", 2},
		{"({get x() {}, set x(v) {}, m() {}})", 3},
	} {
		prg, err := Compile("c", tc.src, false)
		if err != nil {
			t.Fatal(err)
		}
		var got uint32
		found := false
		for _, ins := range prg.code {
			if n, ok := ins.(newObject); ok {
				got, found = uint32(n), true
				break
			}
		}
		if !found || got != tc.want {
			t.Errorf("%s: newObject count = %d (found %v), want %d", tc.src, got, found, tc.want)
		}
	}
}
