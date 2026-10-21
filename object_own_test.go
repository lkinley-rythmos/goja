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
	const tag = s => s; const sparse = []; sparse[100000] = 1; sparse.p = 2;
	const methods = {m() {}, *g() {}, async a() {}};
	function* gen() {}
	out.push(tag` + "`a${1}b`" + `, tag` + "`a${1}b`" + `.raw, sparse, new Promise(() => {}), gen(), gen, async function () {}, async () => {}, () => {},
		methods.m, methods.g, methods.a, new Set([1]), new WeakMap(), new WeakSet(), new ArrayBuffer(2), new DataView(new ArrayBuffer(2)),
		[1][Symbol.iterator](), new Map().entries(), new Set().values(), "ab"[Symbol.iterator](), "ab".matchAll(/a/g),
		Object(Symbol("w")), new Number(1), Function.prototype, Function.prototype.call, Function.prototype.apply, RegExp.prototype,
		Array, Object, Reflect, Symbol, Promise, Uint8Array, Error.prototype, globalThis);
	return out;
})()`

// ownLookup is what GetOwnDataProperty or GetOwnDataPropertySymbol gave
// for a key. unbuilt is set for an Error's stack the lookup found not yet
// built, which it reports not ok.
type ownLookup struct {
	v                        Value
	writable, enum, conf, ok bool
	unbuilt                  bool
}

// checkOwnAPIs makes the Go lookups of the properties first, on the
// object as the fixture left it, and only then asks
// Object.getOwnPropertyDescriptor, so that a property built on first read
// is built by the Go lookup, not before it by JavaScript. Reflect.ownKeys,
// which builds none, is asked before, so that the keys are compared on the
// object as the fixture left it too: a function lists its prototype in
// another place once it is built.
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
	names, symbols, ok := o.AppendOwnPropertyKeys(nil, nil)
	if !ok {
		t.Fatalf("%s: ok false", label)
	}
	want, err := ownKeysFn(nil, o)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]ownLookup, 0, len(names)+len(symbols))
	for _, name := range names {
		var l ownLookup
		if e, isErr := o.self.(*errorObject); isErr && name == propNameStack && !e.stackPropAdded {
			l.unbuilt = true
		}
		l.v, l.writable, l.enum, l.conf, l.ok = o.GetOwnDataProperty(name)
		got = append(got, l)
	}
	for _, sym := range symbols {
		var l ownLookup
		l.v, l.writable, l.enum, l.conf, l.ok = o.GetOwnDataPropertySymbol(sym)
		got = append(got, l)
	}
	var missing []bool
	for _, name := range []string{"nope", "\u0000", ""} {
		v, _, _, _, ok := o.GetOwnDataProperty(name)
		missing = append(missing, ok || v != nil)
	}

	wantObj := want.(*Object)
	n := int(wantObj.Get("length").ToInteger())
	if n != len(names)+len(symbols) {
		t.Fatalf("%s: %d keys, want %d (%v %v)", label, len(names)+len(symbols), n, names, symbols)
	}
	for i := 0; i < n; i++ {
		k := wantObj.Get(fmt.Sprint(i))
		var key any
		if i < len(names) {
			if _, isSym := k.(*Symbol); isSym || k.String() != names[i] {
				t.Fatalf("%s: key %d = %q, want %v", label, i, names[i], k)
			}
			key = names[i]
		} else if s := symbols[i-len(names)]; k != s {
			t.Fatalf("%s: symbol %d = %v, want %v", label, i, s, k)
		} else {
			key = s
		}
		l := got[i]
		if l.unbuilt {
			if l.ok || l.v != nil {
				t.Fatalf("%s: unbuilt stack = %v %v", label, l.v, l.ok)
			}
			continue
		}
		d, err := describeFn(nil, o, k)
		if err != nil {
			t.Fatal(err)
		}
		dObj := d.(*Object)
		if dObj.Get("0").ToBoolean() {
			if l.ok || l.v != nil {
				t.Fatalf("%s: %v is an accessor; got %v %v", label, key, l.v, l.ok)
			}
			continue
		}
		if !l.ok || !l.v.SameAs(dObj.Get("1")) || l.writable != dObj.Get("2").ToBoolean() || l.enum != dObj.Get("3").ToBoolean() || l.conf != dObj.Get("4").ToBoolean() {
			t.Fatalf("%s: %v = %v %v/%v/%v %v, want %v", label, key, l.v, l.writable, l.enum, l.conf, l.ok, d)
		}
	}
	for i, name := range []string{"nope", "\u0000", ""} {
		if missing[i] && !o.hasOwnProperty(newStringValue(name)) {
			t.Fatalf("%s: missing %q found", label, name)
		}
	}
}

// TestOwnAPIs_MatchReflect: AppendOwnPropertyKeys lists exactly
// Reflect.ownKeys, and GetOwnDataProperty and GetOwnDataPropertySymbol
// exactly Object.getOwnPropertyDescriptor's data descriptors (accessors and
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
		"go map":           map[string]any{"k": 1},
		"go struct":        &goStruct{A: 1, B: "x"},
		"go slice":         []any{1, "a"},
		"go typed map":     map[string]int{"k": 1},
		"go typed slice":   []int{1, 2},
		"go array pointer": &[2]int{1, 2},
		"go func":          func(a int) int { return a },
	} {
		checkOwnAPIs(t, r, label, r.ToValue(value).(*Object))
	}
	checkOwnAPIs(t, r, "dynamic", r.NewDynamicObject(&testDynObject{r: r, m: map[string]Value{"only": valueInt(1)}}))
	checkOwnAPIs(t, r, "dynamic array", r.NewDynamicArray(&testDynArray{r: r, a: []Value{valueInt(1)}}))
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

// TestGetOwnDataPropertySymbol: the symbol-keyed lookup finds an own data
// property with its attributes, also on a frozen object, and is not ok
// for an accessor (running neither getter nor setter), for an inherited
// or missing symbol, and for a Proxy (running no trap).
func TestGetOwnDataPropertySymbol(t *testing.T) {
	r := New()
	v, err := r.RunString(`(function () {
		globalThis.calls = 0;
		const s = Symbol("s"), acc = Symbol("acc"), inh = Symbol("inh"), u = Symbol("u"), missing = Symbol("missing");
		const proto = {[inh]: 1};
		const o = Object.create(proto);
		o[s] = 1;
		Object.defineProperty(o, u, {value: undefined, writable: false, enumerable: false, configurable: true});
		Object.defineProperty(o, acc, {get() { globalThis.calls++; return 1; }, set(v) { globalThis.calls++; }, configurable: true});
		const frozen = Object.freeze({[s]: 2});
		const handler = {};
		for (const trap of ["getOwnPropertyDescriptor", "get", "has", "ownKeys", "getPrototypeOf"]) {
			handler[trap] = function () { globalThis.calls++; return Reflect[trap].apply(null, arguments); };
		}
		const p = new Proxy({[s]: 3}, handler);
		const arr = [1]; arr[s] = 4;
		return {s, acc, inh, u, missing, o, frozen, p, pp: new Proxy(p, handler), arr, fn: Object.assign(function () {}, {[s]: 5})};
	})()`)
	if err != nil {
		t.Fatal(err)
	}
	root := v.(*Object)
	sym := func(name string) *Symbol { return root.Get(name).(*Symbol) }
	obj := func(name string) *Object { return root.Get(name).(*Object) }
	s := sym("s")

	check := func(label string, o *Object, key *Symbol, wantOK bool, want Value, wantW, wantE, wantC bool) {
		t.Helper()
		v, w, e, c, ok := o.GetOwnDataPropertySymbol(key)
		if ok != wantOK {
			t.Fatalf("%s: ok = %v, want %v (%v)", label, ok, wantOK, v)
		}
		if !wantOK {
			if v != nil || w || e || c {
				t.Fatalf("%s: not ok must return zero values, got %v %v/%v/%v", label, v, w, e, c)
			}
			return
		}
		if !v.SameAs(want) || w != wantW || e != wantE || c != wantC {
			t.Fatalf("%s: got %v %v/%v/%v, want %v %v/%v/%v", label, v, w, e, c, want, wantW, wantE, wantC)
		}
	}
	o := obj("o")
	check("data", o, s, true, intToValue(1), true, true, true)
	check("undefined value", o, sym("u"), true, _undefined, false, false, true)
	check("accessor", o, sym("acc"), false, nil, false, false, false)
	check("inherited", o, sym("inh"), false, nil, false, false, false)
	check("missing", o, sym("missing"), false, nil, false, false, false)
	check("frozen", obj("frozen"), s, true, intToValue(2), false, true, false)
	check("proxy", obj("p"), s, false, nil, false, false, false)
	check("proxy of proxy", obj("pp"), s, false, nil, false, false, false)
	check("array", obj("arr"), s, true, intToValue(4), true, true, true)
	check("function", obj("fn"), s, true, intToValue(5), true, true, true)
	check("well-known on ordinary object", o, SymIterator, false, nil, false, false, false)
	if calls := r.Get("calls").ToInteger(); calls != 0 {
		t.Fatalf("%d traps or accessors ran", calls)
	}

	builtin := r.Get("Array").(*Object).Get("prototype").(*Object)
	if v, _, _, _, ok := builtin.GetOwnDataPropertySymbol(SymIterator); !ok || v == nil {
		t.Fatalf("Array.prototype[Symbol.iterator] = %v %v", v, ok)
	}
	if v, _, _, _, ok := r.ToValue(map[string]any{"k": 1}).(*Object).GetOwnDataPropertySymbol(s); ok || v != nil {
		t.Fatalf("go map = %v %v", v, ok)
	}
	dyn := r.NewDynamicObject(&testDynObject{r: r, m: map[string]Value{"only": valueInt(1)}})
	if v, _, _, _, ok := dyn.GetOwnDataPropertySymbol(s); ok || v != nil {
		t.Fatalf("dynamic = %v %v", v, ok)
	}

	missing, frozen, arr := sym("missing"), obj("frozen"), obj("arr")
	if allocs := testing.AllocsPerRun(100, func() {
		o.GetOwnDataPropertySymbol(s)
		o.GetOwnDataPropertySymbol(missing)
		frozen.GetOwnDataPropertySymbol(s)
		arr.GetOwnDataPropertySymbol(s)
	}); allocs != 0 {
		t.Fatalf("GetOwnDataPropertySymbol: %v allocs", allocs)
	}
}

// TestOwnAPIs_DestructuringSource: the source an object rest destructuring
// reads through, which forwards to the destructured value and records the
// keys read from it, never reaches script: a getter run by the pattern
// gets the value itself as this, on which the APIs read as on any other
// object, and the rest keeps the keys read. Built from Go, the source is
// not ok for the APIs, as a Proxy is, which it may wrap: no trap runs and
// no key is recorded.
func TestOwnAPIs_DestructuringSource(t *testing.T) {
	r := New()
	var probed []*Object
	r.Set("probe", func(o *Object) {
		probed = append(probed, o)
		o.GetOwnDataProperty("b")
		o.GetOwnDataPropertySymbol(r.Get("slot").(*Symbol))
	})
	v, err := r.RunString(`(function () {
		globalThis.traps = 0;
		globalThis.slot = Symbol("slot");
		const target = {b: 2, [slot]: 42, get a() { probe(this); return 1; }};
		const handler = {};
		for (const trap of ["getOwnPropertyDescriptor", "ownKeys", "get", "has"]) {
			handler[trap] = function () { globalThis.traps++; return Reflect[trap].apply(null, arguments); };
		}
		const p = new Proxy(target, handler);
		const {a: a1, ...plainRest} = target;
		const {a: a2, ...proxyRest} = p;
		return [target, p, plainRest, proxyRest];
	})()`)
	if err != nil {
		t.Fatal(err)
	}
	results := v.(*Object)
	target, p := results.Get("0").(*Object), results.Get("1").(*Object)
	if len(probed) != 2 || probed[0] != target || probed[1] != p {
		t.Fatalf("getters got %v as this, want the target and the proxy", probed)
	}
	slot := r.Get("slot").(*Symbol)
	for i := 2; i < 4; i++ {
		rest := results.Get(fmt.Sprint(i)).(*Object)
		if v, _, _, _, ok := rest.GetOwnDataProperty("b"); !ok || v.ToInteger() != 2 {
			t.Fatalf("rest %d: b = %v %v", i, v, ok)
		}
		if v, _, _, _, ok := rest.GetOwnDataPropertySymbol(slot); !ok || v.ToInteger() != 42 {
			t.Fatalf("rest %d: slot = %v %v", i, v, ok)
		}
	}

	for _, wrapped := range []*Object{target, p} {
		src := r.newDestructKeyedSource(wrapped)
		r.Set("traps", 0)
		if v, _, _, _, ok := src.GetOwnDataProperty("b"); ok || v != nil {
			t.Fatalf("source of %v: b = %v %v", wrapped, v, ok)
		}
		if v, _, _, _, ok := src.GetOwnDataPropertySymbol(slot); ok || v != nil {
			t.Fatalf("source of %v: slot = %v %v", wrapped, v, ok)
		}
		if names, symbols, ok := src.AppendOwnPropertyKeys(nil, nil); ok || names != nil || symbols != nil {
			t.Fatalf("source of %v: keys %v %v %v", wrapped, names, symbols, ok)
		}
		if traps := r.Get("traps").ToInteger(); traps != 0 {
			t.Fatalf("source of %v: %d traps ran", wrapped, traps)
		}
		if d := src.self.(*destructKeyedSource); len(d.usedKeys.stringProps) != 0 || len(d.usedKeys.symbolProps) != 0 {
			t.Fatalf("source of %v: keys recorded", wrapped)
		}
	}
}

// TestOwnAPIs_ErrorStack: an Error builds its stack on first read, from
// its name and message, which a getter, a conversion or a Proxy trap may
// give. Until script has read it GetOwnDataProperty reports the stack not
// ok and runs none of them, and AppendOwnPropertyKeys lists it without
// building it; once read, it is an own data property like any other.
func TestOwnAPIs_ErrorStack(t *testing.T) {
	r := New()
	v, err := r.RunString(`(function () {
		globalThis.calls = 0;
		const named = new Error("message");
		Object.defineProperty(named, "name", {get() { calls++; return "Error"; }});
		const messaged = new Error();
		messaged.message = {toString() { calls++; return "m"; }};
		const proxied = new Error("m");
		Object.setPrototypeOf(proxied, new Proxy(Error.prototype, {get(t, k, r) { calls++; return Reflect.get(t, k, r); }}));
		return [named, messaged, proxied];
	})()`)
	if err != nil {
		t.Fatal(err)
	}
	errs := v.(*Object)
	for i := 0; i < 3; i++ {
		e := errs.Get(fmt.Sprint(i)).(*Object)
		if v, _, _, _, ok := e.GetOwnDataProperty("stack"); ok || v != nil {
			t.Fatalf("error %d: unbuilt stack = %v %v", i, v, ok)
		}
		if names, _, ok := e.AppendOwnPropertyKeys(nil, nil); !ok || len(names) == 0 || names[0] != "stack" {
			t.Fatalf("error %d: keys %v %v", i, names, ok)
		}
	}
	if calls := r.Get("calls").ToInteger(); calls != 0 {
		t.Fatalf("%d getters, conversions or traps ran", calls)
	}
	r.Set("errs", errs)
	stacks, err := r.RunString(`[0, 1, 2].map(i => errs[i].stack)`)
	if err != nil {
		t.Fatal(err)
	}
	if calls := r.Get("calls").ToInteger(); calls == 0 {
		t.Fatal("reading the stacks from script ran nothing")
	}
	for i := 0; i < 3; i++ {
		e := errs.Get(fmt.Sprint(i)).(*Object)
		v, w, en, c, ok := e.GetOwnDataProperty("stack")
		if want := stacks.(*Object).Get(fmt.Sprint(i)); !ok || !v.SameAs(want) || !w || en || !c {
			t.Fatalf("error %d: built stack = %v %v/%v/%v %v, want %v", i, v, w, en, c, ok, want)
		}
	}
}
