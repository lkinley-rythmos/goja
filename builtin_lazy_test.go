package goja

import (
	"fmt"
	"strings"
	"testing"
)

// lazyBuiltinTraps installs, on Object.prototype and Function.prototype, a
// getter and a setter that count their calls, for the names built-in
// objects' lazy initialization assigned before it defined them (size,
// description, RegExp.prototype's flag accessors and
// Array.prototype[Symbol.unscopables]' names) and for constructor,
// prototype, name and length, so that an initialization that assigned a
// property, or read one, instead of defining it would run them.
const lazyBuiltinTraps = `(function () {
	const names = ["size", "description", "source", "global", "hasIndices", "multiline", "dotAll",
		"ignoreCase", "unicode", "sticky", "flags", "constructor", "prototype", "name", "length",
		"copyWithin", "entries", "fill", "find", "findIndex", "findLast", "findLastIndex", "flat",
		"flatMap", "includes", "keys", "values", "toReversed", "toSorted", "toSpliced"];
	let calls = 0;
	for (const proto of [Object.prototype, Function.prototype]) {
		for (const name of names) {
			if (Object.getOwnPropertyDescriptor(proto, name)) {
				continue;
			}
			Object.defineProperty(proto, name, {get() { calls++; }, set(v) { calls++; }, configurable: true});
		}
	}
	return [() => calls, names];
})()`

// lazyShapeGo describes, through the own-property APIs, the own properties
// of o: each key, whether it is a data property, its attributes and the
// kind of its value.
func lazyShapeGo(o *Object, skip map[string]bool) string {
	var b strings.Builder
	names, symbols, ok := o.AppendOwnPropertyKeys(nil, nil)
	if !ok {
		return "not ok"
	}
	kind := func(v Value) string {
		if obj, isObj := v.(*Object); isObj {
			return obj.self.className()
		}
		if v == nil {
			return "-"
		}
		return v.String()
	}
	for _, name := range names {
		if skip[name] {
			continue
		}
		v, w, e, c, ok := o.GetOwnDataProperty(name)
		fmt.Fprintf(&b, "%s=%v/%v/%v/%v/%s;", name, ok, w, e, c, kind(v))
	}
	for _, sym := range symbols {
		v, w, e, c, ok := o.GetOwnDataPropertySymbol(sym)
		fmt.Fprintf(&b, "%s=%v/%v/%v/%v/%s;", sym.descriptiveString(), ok, w, e, c, kind(v))
	}
	return b.String()
}

// lazyWalkGo describes, from Go only, the global object, each of its
// object values and their prototype objects, and
// Array.prototype[Symbol.unscopables]. The names in skip are left out of
// Object.prototype and Function.prototype, which hold the traps.
func lazyWalkGo(r *Runtime, skip map[string]bool) map[string]string {
	shapes := map[string]string{}
	global := r.GlobalObject()
	shapes["globalThis"] = lazyShapeGo(global, nil)
	names, _, _ := global.AppendOwnPropertyKeys(nil, nil)
	for _, name := range names {
		v, _, _, _, ok := global.GetOwnDataProperty(name)
		obj, isObj := v.(*Object)
		if !ok || !isObj {
			continue
		}
		shapes[name] = lazyShapeGo(obj, nil)
		if p, _, _, _, ok := obj.GetOwnDataProperty("prototype"); ok {
			if proto, isObj := p.(*Object); isObj {
				var protoSkip map[string]bool
				if name == "Object" || name == "Function" {
					protoSkip = skip
				}
				shapes[name+".prototype"] = lazyShapeGo(proto, protoSkip)
			}
		}
	}
	arrayCtor, _, _, _, _ := global.GetOwnDataProperty("Array")
	arrayProto, _, _, _, _ := arrayCtor.(*Object).GetOwnDataProperty("prototype")
	unscopables, _, _, _, ok := arrayProto.(*Object).GetOwnDataPropertySymbol(SymUnscopables)
	if !ok {
		shapes["unscopables"] = "missing"
	} else {
		shapes["unscopables"] = lazyShapeGo(unscopables.(*Object), skip)
	}
	return shapes
}

// lazyShapeJS describes, from script, the own properties of every global
// object value and of its prototype object, and of
// Array.prototype[Symbol.unscopables], touching each first from script.
const lazyShapeJS = `(function (names, skip) {
	const shape = o => Reflect.ownKeys(o).filter(k => !(o === Object.prototype || o === Function.prototype) || !skip.includes(k)).map(k => {
		const d = Reflect.getOwnPropertyDescriptor(o, k);
		const kind = Object.prototype.hasOwnProperty.call(d, "value") ? "data/" + d.writable + "/" + typeof d.value : "accessor/" + typeof d.get + "/" + typeof d.set;
		return String(k) + "=" + kind + "/" + d.enumerable + "/" + d.configurable;
	}).join(";");
	const out = {};
	for (const name of names) {
		const v = globalThis[name];
		if ((typeof v !== "object" && typeof v !== "function") || v === null) {
			continue;
		}
		out[name] = shape(v);
		const p = Object.prototype.hasOwnProperty.call(v, "prototype") ? Object.getOwnPropertyDescriptor(v, "prototype").value : undefined;
		if (p !== null && (typeof p === "object" || typeof p === "function")) {
			out[name + ".prototype"] = shape(p);
		}
	}
	out.unscopables = shape(Array.prototype[Symbol.unscopables]);
	return JSON.stringify(out);
})`

// lazyRuntimeWithTraps returns a fresh runtime with lazyBuiltinTraps
// installed, a function giving the number of trap calls so far, and the
// names trapped.
func lazyRuntimeWithTraps(t *testing.T) (*Runtime, func() int64, map[string]bool) {
	t.Helper()
	r := New()
	v, err := r.RunString(lazyBuiltinTraps)
	if err != nil {
		t.Fatal(err)
	}
	calls, _ := AssertFunction(v.(*Object).Get("0"))
	skip := map[string]bool{}
	for _, name := range v.(*Object).Get("1").Export().([]any) {
		skip[name.(string)] = true
	}
	return r, func() int64 {
		n, err := calls(nil)
		if err != nil {
			t.Fatal(err)
		}
		return n.ToInteger()
	}, skip
}

// TestLazyBuiltinsDefineProperties: a built-in object built on first use
// defines its properties: with getters and setters on Object.prototype
// and Function.prototype for the names they define, building every global
// object and its prototype, from Go through the own-property APIs or from
// script, runs none of them, and every one has the properties, attributes
// and kinds of value it has when built in a runtime without them.
func TestLazyBuiltinsDefineProperties(t *testing.T) {
	r, count, skip := lazyRuntimeWithTraps(t)
	got := lazyWalkGo(r, skip)
	if n := count(); n != 0 {
		t.Errorf("building the globals from Go ran %d getters or setters", n)
	}
	want := lazyWalkGo(New(), skip)
	if len(got) != len(want) {
		t.Errorf("from Go: %d objects, want %d", len(got), len(want))
	}
	for name, w := range want {
		if g := got[name]; g != w {
			t.Errorf("%s from Go:\n got %s\nwant %s", name, g, w)
		}
	}

	names, _, _ := New().GlobalObject().AppendOwnPropertyKeys(nil, nil)
	skipNames := make([]string, 0, len(skip))
	for name := range skip {
		skipNames = append(skipNames, name)
	}
	shapes := func(rt *Runtime) string {
		t.Helper()
		f, err := rt.RunString(lazyShapeJS)
		if err != nil {
			t.Fatal(err)
		}
		fn, _ := AssertFunction(f)
		res, err := fn(nil, rt.ToValue(names), rt.ToValue(skipNames))
		if err != nil {
			t.Fatal(err)
		}
		return res.String()
	}
	r2, count2, _ := lazyRuntimeWithTraps(t)
	got2 := shapes(r2)
	if n := count2(); n != 0 {
		t.Errorf("building the globals from script ran %d getters or setters", n)
	}
	if want2 := shapes(New()); got2 != want2 {
		t.Errorf("from script:\n got %s\nwant %s", got2, want2)
	}
}
