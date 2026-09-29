package goja

import (
	"fmt"
	"testing"
)

// ownAPIFixtures builds, with a seeded generator, ordinary objects shaped
// by random sequences of property operations (string, integer-like and
// non-ASCII keys, hidden and read-only members, accessors, symbols, both
// enumerable and hidden, deletions, redefinitions, freezing, sealing),
// then appends exotic objects: arrays, functions, class instances, typed
// arrays, String, arguments, Date, RegExp, Error, Map, bound functions,
// null-prototype objects and a descriptor object.
const ownAPIFixtures = `(function () {
	let seed = 12345;
	const rnd = n => { seed = (seed * 1103515245 + 12345) & 0x7fffffff; return seed % n; };
	const names = ["a", "b", "c", "0", "1", "10", "2", "length", "ключ", "é", "constructor", "x y", "-1", "4294967295", "007"];
	const syms = [Symbol("s1"), Symbol("s2"), Symbol.iterator, Symbol.for("shared")];
	const out = [];
	for (let i = 0; i < 300; i++) {
		const o = rnd(5) === 0 ? Object.create(null) : {};
		const ops = rnd(24);
		for (let j = 0; j < ops; j++) {
			const key = rnd(4) === 0 ? syms[rnd(syms.length)] : names[rnd(names.length)];
			try {
				switch (rnd(9)) {
				case 0: case 1: o[key] = rnd(100); break;
				case 2: Object.defineProperty(o, key, {value: "h" + j, writable: rnd(2) === 0, enumerable: rnd(2) === 0, configurable: rnd(2) === 0}); break;
				case 3: Object.defineProperty(o, key, {get() { return 1; }, set(v) {}, enumerable: rnd(2) === 0, configurable: true}); break;
				case 4: Object.defineProperty(o, key, {get: undefined, configurable: rnd(2) === 0}); break;
				case 5: delete o[key]; break;
				case 6: Object.defineProperty(o, key, {value: undefined, writable: true, enumerable: true, configurable: true}); break;
				case 7: Object.defineProperty(o, key, {writable: false}); break;
				case 8: o[key] = o; break;
				}
			} catch (e) {}
		}
		switch (rnd(10)) {
		case 0: Object.freeze(o); break;
		case 1: Object.seal(o); break;
		case 2: Object.preventExtensions(o); break;
		}
		out.push(o);
	}
	const arr = [1, , 3]; arr.extra = 1; Object.defineProperty(arr, "hidden", {value: 2}); arr[Symbol("as")] = 1;
	const fn = function f(a, b) {}; fn.p = 1; fn[Symbol("fs")] = 2;
	class K { constructor() { this.a = 1; } m() {} }
	const args = (function () { return arguments; })(1, 2);
	const strictArgs = (function () { "use strict"; return arguments; })(1);
	const re = /x/g; re.q = 1;
	const err = new Error("m"); err.code = 1;
	const nul = Object.create(null); nul.z = 1; nul[1] = 2;
	out.push(arr, fn, new K(), K, new Uint8Array(3), new Float64Array([1, 2]), new String("ab"), args, strictArgs, new Date(0), re, err,
		new Map([[1, 2]]), fn.bind(null), nul, Object.getOwnPropertyDescriptor({a: 1}, "a"),
		Object.getOwnPropertyDescriptor({get a() { return 1; }}, "a"), Math, JSON, Object.prototype, Array.prototype, [], new Boolean(true));
	return out;
})()`

func checkOwnAPIs(t *testing.T, r *Runtime, label string, o *Object) {
	t.Helper()
	ownKeys, err := r.RunString(`(o => Reflect.ownKeys(o))`)
	if err != nil {
		t.Fatal(err)
	}
	describe, err := r.RunString(`((o, k) => { const d = Object.getOwnPropertyDescriptor(o, k); return d === undefined ? null : ["get" in d, d.value, d.writable, d.enumerable, d.configurable]; })`)
	if err != nil {
		t.Fatal(err)
	}
	ownKeysFn, _ := AssertFunction(ownKeys)
	describeFn, _ := AssertFunction(describe)
	want, err := ownKeysFn(nil, o)
	if err != nil {
		t.Fatal(err)
	}
	names, symbols, ok := o.AppendOwnPropertyKeys(nil, nil)
	if !ok {
		t.Fatalf("%s: ok false", label)
	}
	wantObj := want.(*Object)
	n := int(wantObj.Get("length").ToInteger())
	if n != len(names)+len(symbols) {
		t.Fatalf("%s: %d keys, want %d (%v %v)", label, len(names)+len(symbols), n, names, symbols)
	}
	for i := 0; i < n; i++ {
		k := wantObj.Get(fmt.Sprint(i))
		if i < len(names) {
			if _, isSym := k.(*Symbol); isSym || k.String() != names[i] {
				t.Fatalf("%s: key %d = %q, want %v", label, i, names[i], k)
			}
			d, err := describeFn(nil, o, k)
			if err != nil {
				t.Fatal(err)
			}
			v, w, e, c, ok := o.GetOwnDataProperty(names[i])
			dObj := d.(*Object)
			accessor := dObj.Get("0").ToBoolean()
			if accessor {
				if ok || v != nil {
					t.Fatalf("%s: %q is an accessor; got %v %v", label, names[i], v, ok)
				}
				continue
			}
			if !ok || !v.SameAs(dObj.Get("1")) || w != dObj.Get("2").ToBoolean() || e != dObj.Get("3").ToBoolean() || c != dObj.Get("4").ToBoolean() {
				t.Fatalf("%s: %q = %v %v/%v/%v %v, want %v", label, names[i], v, w, e, c, ok, d)
			}
		} else if s := symbols[i-len(names)]; k != s {
			t.Fatalf("%s: symbol %d = %v, want %v", label, i, s, k)
		}
	}
	for _, missing := range []string{"nope", "\u0000", ""} {
		if has := o.hasOwnProperty(newStringValue(missing)); !has {
			if v, _, _, _, ok := o.GetOwnDataProperty(missing); ok || v != nil {
				t.Fatalf("%s: missing %q = %v %v", label, missing, v, ok)
			}
		}
	}
}

// TestOwnAPIs_MatchReflect: AppendOwnPropertyKeys lists exactly
// Reflect.ownKeys, and GetOwnDataProperty exactly
// Object.getOwnPropertyDescriptor's data descriptors (accessors and
// missing properties are not ok), over the fuzzed ordinary objects and
// the exotic ones, and over Go-backed and dynamic objects.
func TestOwnAPIs_MatchReflect(t *testing.T) {
	r := New()
	v, err := r.RunString(ownAPIFixtures)
	if err != nil {
		t.Fatal(err)
	}
	arr := v.(*Object)
	n := int(arr.Get("length").ToInteger())
	for i := 0; i < n; i++ {
		checkOwnAPIs(t, r, fmt.Sprintf("fixture %d", i), arr.Get(fmt.Sprint(i)).(*Object))
	}
	type goStruct struct {
		A int
		B string
	}
	for label, value := range map[string]any{
		"go map":    map[string]any{"k": 1},
		"go struct": &goStruct{A: 1, B: "x"},
		"go slice":  []any{1, "a"},
	} {
		checkOwnAPIs(t, r, label, r.ToValue(value).(*Object))
	}
	checkOwnAPIs(t, r, "dynamic", r.NewDynamicObject(&testDynObject{r: r, m: map[string]Value{"only": valueInt(1)}}))
}

// TestOwnAPIs_ZeroAlloc: on an ordinary object, with capacity for the
// keys and ASCII names, neither API allocates, whatever the object holds:
// integer-like keys, hidden members, accessors, symbols (enumerable and
// hidden), and after deletions.
func TestOwnAPIs_ZeroAlloc(t *testing.T) {
	r := New()
	v, err := r.RunString(`(function () {
		const o = {b: 1, a: 2, 3: 3, 1: 4};
		Object.defineProperty(o, "hidden", {value: 5});
		Object.defineProperty(o, "acc", {get() { return 1; }, enumerable: true, configurable: true});
		Object.defineProperty(o, "ro", {value: 6, writable: false, enumerable: true, configurable: true});
		o[Symbol("e")] = 1;
		Object.defineProperty(o, Symbol("h"), {value: 1});
		o.gone = 1; delete o.gone;
		const s = Symbol("gone"); o[s] = 1; delete o[s];
		return o;
	})()`)
	if err != nil {
		t.Fatal(err)
	}
	o := v.(*Object)
	if _, ok := o.self.(*baseObject); !ok {
		t.Fatalf("fixture is %T, not an ordinary object", o.self)
	}
	names := make([]string, 0, 16)
	symbols := make([]*Symbol, 0, 4)
	if allocs := testing.AllocsPerRun(100, func() {
		names, symbols, _ = o.AppendOwnPropertyKeys(names[:0], symbols[:0])
	}); allocs != 0 {
		t.Fatalf("AppendOwnPropertyKeys: %v allocs", allocs)
	}
	if got := fmt.Sprint(names, len(symbols)); got != "[1 3 b a hidden acc ro] 2" {
		t.Fatalf("keys %s", got)
	}
	if allocs := testing.AllocsPerRun(100, func() {
		for _, name := range names {
			o.GetOwnDataProperty(name)
		}
		o.GetOwnDataProperty("missing")
	}); allocs != 0 {
		t.Fatalf("GetOwnDataProperty: %v allocs", allocs)
	}
	lit, err := r.RunString(`({a: 1, b: 2})`)
	if err != nil {
		t.Fatal(err)
	}
	if allocs := testing.AllocsPerRun(100, func() {
		names, symbols, _ = lit.(*Object).AppendOwnPropertyKeys(names[:0], symbols[:0])
		lit.(*Object).GetOwnDataProperty("a")
	}); allocs != 0 {
		t.Fatalf("object literal: %v allocs", allocs)
	}
}

// TestOwnAPIs_RunNoJavaScript: on a Proxy (and a Proxy of a Proxy) both
// APIs return ok false and run no trap; on an accessor GetOwnDataProperty
// is not ok and runs neither the getter nor the setter.
func TestOwnAPIs_RunNoJavaScript(t *testing.T) {
	r := New()
	v, err := r.RunString(`(function () {
		globalThis.traps = 0;
		const handler = {};
		for (const trap of ["getOwnPropertyDescriptor", "ownKeys", "get", "has", "getPrototypeOf", "defineProperty", "deleteProperty", "isExtensible", "preventExtensions", "set", "setPrototypeOf", "apply", "construct"]) {
			handler[trap] = function () { globalThis.traps++; return Reflect[trap].apply(null, arguments); };
		}
		const target = {a: 1};
		Object.defineProperty(target, "acc", {get() { globalThis.traps++; return 1; }, set(v) { globalThis.traps++; }, configurable: true});
		const p = new Proxy(target, handler);
		return [p, new Proxy(p, handler), target, new Proxy(function () {}, handler)];
	})()`)
	if err != nil {
		t.Fatal(err)
	}
	objs := v.(*Object)
	for i := 0; i < 4; i++ {
		o := objs.Get(fmt.Sprint(i)).(*Object)
		names, symbols, ok := o.AppendOwnPropertyKeys(nil, nil)
		isProxy := i != 2
		if isProxy && (ok || names != nil || symbols != nil) {
			t.Fatalf("object %d: proxy keys %v %v %v", i, names, symbols, ok)
		}
		for _, name := range []string{"a", "acc", "missing"} {
			v, _, _, _, ok := o.GetOwnDataProperty(name)
			if isProxy && (ok || v != nil) {
				t.Fatalf("object %d: proxy %q = %v %v", i, name, v, ok)
			}
			if !isProxy && name == "acc" && (ok || v != nil) {
				t.Fatalf("accessor = %v %v", v, ok)
			}
		}
	}
	if traps := r.Get("traps").ToInteger(); traps != 0 {
		t.Fatalf("%d traps or accessors ran", traps)
	}
}
