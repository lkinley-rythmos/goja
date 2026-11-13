package goja

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"testing"

	"github.com/dop251/goja/unistring"
)

// dataTemplateProbes are scripts run against `o`, an object of properties
// a (1), b ("two"), c ({x: 1, y: [1, 2]}), d (null), then e (5) defined
// after: each must give the same result for an object made from a template
// as for one defined eagerly, as the first thing done to a fresh object.
var dataTemplateProbes = []string{
	`JSON.stringify(o)`,
	`Object.keys(o).join()`,
	`Reflect.ownKeys(o).join()`,
	`Object.getOwnPropertyNames(o).join()`,
	`JSON.stringify(Object.getOwnPropertyDescriptor(o, "b"))`,
	`JSON.stringify(Object.getOwnPropertyDescriptors(o))`,
	`o.a + o.b`,
	`"c" in o && !("z" in o) && o.hasOwnProperty("d") && !o.hasOwnProperty("toString")`,
	`delete o.b; JSON.stringify(o) + Object.keys(o).join()`,
	`delete o.b; o.b = 9; JSON.stringify(o) + Object.keys(o).join()`,
	`delete o.e; delete o.a; o.a = 1; Reflect.ownKeys(o).join()`,
	`o.z = 1; o[5] = "i"; o[1] = "j"; Reflect.ownKeys(o).join() + JSON.stringify(o)`,
	`Object.defineProperty(o, "a", {enumerable: false}); Object.keys(o).join() + JSON.stringify(Object.getOwnPropertyDescriptor(o, "a"))`,
	`Object.defineProperty(o, "c", {get() { return 7; }}); JSON.stringify(o)`,
	`Object.freeze(o); Object.isFrozen(o) + "" + (() => { "use strict"; try { o.a = 2; } catch (e) { return e.constructor.name; } })() + o.a`,
	`Object.seal(o); delete o.a; Object.isSealed(o) + JSON.stringify(o)`,
	`Object.preventExtensions(o); o.q = 1; Object.isExtensible(o) + JSON.stringify(o)`,
	`var out = []; for (var k in o) out.push(k); out.join()`,
	`JSON.stringify({...o, a: 2})`,
	`JSON.stringify(Object.assign({}, o))`,
	`JSON.stringify(Object.entries(o))`,
	`Object.getPrototypeOf(o) === Object.prototype && o.constructor === Object && String(o) + o.toString()`,
	`Object.setPrototypeOf(o, null); typeof o.toString + JSON.stringify(o)`,
	`o.c.x = 5; o.c.y.push(3); JSON.stringify(o.c) + (o.c === o.c)`,
	`o.a = 10; o.b += "!"; JSON.stringify(o)`,
	`var {a, ...rest} = o; a + JSON.stringify(rest)`,
	`o instanceof Object && typeof o + Object.prototype.toString.call(o)`,
	`var p = new Proxy(o, {}); JSON.stringify(p) + Object.keys(p).join()`,
	`Object.prototype.propertyIsEnumerable.call(o, "a") + "" + Object.prototype.propertyIsEnumerable.call(o, "nope")`,
	`o.__proto__ === Object.prototype`,
	`Object.values(o).length + "" + structuredCloneLike(o)`,
}

// dataTemplateAuditProbes cover the objectImpl methods a templated object
// inherits from baseObject (see the audit in object_data_template.go) and
// the built-ins that reach them, with the same `o` as dataTemplateProbes.
var dataTemplateAuditProbes = []string{
	// isExtensible, preventExtensions and the integrity checks before
	// anything else has read the object.
	`Object.isExtensible(o) + "" + Object.isFrozen(o) + Object.isSealed(o)`,
	`Object.preventExtensions(o); Object.isFrozen(o) + "" + Object.isSealed(o) + JSON.stringify(Object.getOwnPropertyDescriptors(o))`,
	`Object.freeze(o); delete o.c; o.c = 1; o.z = 2; JSON.stringify(Object.getOwnPropertyDescriptors(o)) + Object.isFrozen(o)`,
	`Object.seal(o); o.a = 3; delete o.b; JSON.stringify(Object.getOwnPropertyDescriptors(o)) + Object.isSealed(o) + Object.isFrozen(o)`,
	`"use strict"; Object.seal(o); try { delete o.a; } catch (e) { e.constructor.name + ":" + e.message }`,
	`"use strict"; Object.preventExtensions(o); try { o.q = 1; } catch (e) { e.constructor.name + ":" + e.message }`,
	`"use strict"; Object.freeze(o); try { Object.setPrototypeOf(o, {}); } catch (e) { e.constructor.name + ":" + e.message }`,
	// The index variants (getIdx, getOwnPropIdx, setOwnIdx, setForeignIdx,
	// hasPropertyIdx, hasOwnPropertyIdx, defineOwnPropertyIdx, deleteIdx).
	`o[0] = "x"; o[3] = "y"; Object.defineProperty(o, 2, {value: "z", enumerable: true}); delete o[3]; (0 in o) + "" + o.hasOwnProperty(2) + o[0] + o[2] + Reflect.ownKeys(o).join()`,
	`Object.freeze(o); o[0] = 1; Reflect.ownKeys(o).join() + (0 in o)`,
	// Partial redefinition keeps the template value and attributes.
	`Object.defineProperty(o, "b", {writable: false}); o.b = 1; JSON.stringify(Object.getOwnPropertyDescriptor(o, "b")) + Object.keys(o).join()`,
	`Object.defineProperty(o, "b", {value: 3}); JSON.stringify(Object.getOwnPropertyDescriptor(o, "b"))`,
	`Object.defineProperty(o, "a", {configurable: false}); Reflect.deleteProperty(o, "a") + "" + Reflect.defineProperty(o, "a", {enumerable: false}) + JSON.stringify(o)`,
	`Object.getOwnPropertyDescriptor(o, "c").value === o.c`,
	// Reflect, which reaches the objectImpl methods with no conversion.
	`Reflect.has(o, "b") + "" + Reflect.has(o, "toString") + Reflect.get(o, "b") + Reflect.set(o, "b", 4) + o.b + Reflect.deleteProperty(o, "b") + Reflect.has(o, "b")`,
	`JSON.stringify(Reflect.getOwnPropertyDescriptor(o, "d")) + Reflect.isExtensible(o) + Reflect.preventExtensions(o) + Reflect.isExtensible(o) + Reflect.set(o, "q", 1)`,
	`Reflect.getPrototypeOf(o) === Object.prototype && Reflect.setPrototypeOf(o, Array.prototype) && typeof o.push`,
	// An object inheriting from the templated one (protoGetStr,
	// protoSetForeignStr, protoHasPropertyStr through the template).
	`var ch = Object.create(o); ch.a = 3; ch.a + "" + o.a + ("c" in ch) + Object.keys(ch).join() + ch.b`,
	`Object.freeze(o); var ch = Object.create(o); ch.a = 3; ch.a + "" + Object.keys(ch).join()`,
	`Object.defineProperty(o, "a", {set(v) { this.seen = v; }}); var ch = Object.create(o); ch.a = 3; ch.seen + "" + Object.keys(ch).join()`,
	`delete o.a; var ch = Object.create(o); ch.a = 3; ch.a + "" + o.a + Object.keys(ch).join()`,
	// Symbols on an object whose template has none.
	`var s = Symbol("s"); o[s] = 1; Object.defineProperty(o, Symbol.toStringTag, {value: "T"}); String(o) + Reflect.ownKeys(o).length + Object.getOwnPropertySymbols(o).length + delete o[s] + Reflect.ownKeys(o).length`,
	// Enumeration with changes on the way.
	`var out = []; for (var k in o) { delete o.c; o.z = 1; out.push(k); } out.join() + "|" + Object.keys(o).join()`,
	`var out = []; for (var k in o) { if (k === "a") delete o.b; out.push(k); } out.join()`,
	`var out = []; Object.keys(o).forEach(function(k) { delete o[k]; out.push(k); }); out.join() + JSON.stringify(o)`,
	// JSON.stringify with replacers and indentation, JSON round trips.
	`JSON.stringify(o, null, 1)`,
	`JSON.stringify(o, ["d", "a", "zz"])`,
	`JSON.stringify(o, function(k, v) { return k === "b" ? undefined : v; })`,
	`JSON.stringify({o: o, p: [o]})`,
	`o.toJSON = function() { return Object.keys(this); }; JSON.stringify(o)`,
	// The other Object built-ins.
	`JSON.stringify(Object.fromEntries(Object.entries(o)))`,
	`Object.hasOwn(o, "a") + "" + Object.hasOwn(o, "zz")`,
	`JSON.stringify(Object.assign(o, {a: 3, f: 4})) + Object.keys(o).join()`,
	`o.__lookupGetter__("a") === undefined && Object.prototype.__lookupSetter__.call(o, "a") === undefined`,
	`o.__proto__ = null; Object.getPrototypeOf(o) === null && Object.keys(o).join()`,
	`o.valueOf() === o && o.toLocaleString()`,
	`Array.prototype.join.call(o) + Array.prototype.slice.call(o).length`,
	`o.length = 2; o[0] = "p"; o[1] = "q"; Array.prototype.reverse.call(o); Array.prototype.sort.call(o); JSON.stringify(o)`,
	`with (o) { a + b + typeof c + d }`,
	// Proxies over the object, with traps that check the target's
	// invariants (non-configurable and non-extensible targets).
	`var p = new Proxy(o, {ownKeys: function(t) { return Reflect.ownKeys(t); }, getOwnPropertyDescriptor: function(t, k) { return Reflect.getOwnPropertyDescriptor(t, k); }}); Reflect.ownKeys(p).join() + JSON.stringify(Object.getOwnPropertyDescriptors(p))`,
	`Object.freeze(o); var p = new Proxy(o, {ownKeys: function() { return ["a"]; }}); try { Reflect.ownKeys(p); } catch (e) { e.constructor.name + ":" + e.message }`,
	`Object.preventExtensions(o); var p = new Proxy(o, {ownKeys: function() { return ["a", "b", "c", "d", "e", "x"]; }}); try { Reflect.ownKeys(p); } catch (e) { e.constructor.name + ":" + e.message }`,
	`Object.freeze(o); var p = new Proxy(o, {get: function() { return 5; }}); try { p.a; } catch (e) { e.constructor.name + ":" + e.message }`,
	`Object.freeze(o); var p = new Proxy(o, {getOwnPropertyDescriptor: function() { return undefined; }}); try { Object.getOwnPropertyDescriptor(p, "a"); } catch (e) { e.constructor.name + ":" + e.message }`,
	`var p = new Proxy(o, {deleteProperty: function(t, k) { return delete t[k]; }}); delete p.a; delete p.zz; Reflect.ownKeys(o).join()`,
	`var p = new Proxy(o, {defineProperty: function(t, k, d) { return Reflect.defineProperty(t, k, d); }}); p.a = 2; p.z = 3; JSON.stringify(o)`,
	`var p = new Proxy(o, {has: function(t, k) { return k in t; }}); ("a" in p) + "" + ("zz" in p) + ("toString" in p)`,
	`var r = Proxy.revocable(o, {}); var k = Object.keys(r.proxy).join(); r.revoke(); k + JSON.stringify(o)`,
	// Destructuring and spread of an object holding the templated one.
	`var {c: {y: [, second]}, ...rest} = o; second + JSON.stringify(rest)`,
	`JSON.stringify({...{...o}})`,
	`var m = new Map(Object.entries(o)); m.size + "" + m.get("b")`,
}

func dataTemplateNested(r *Runtime) Value {
	o := r.NewObject()
	if err := o.Set("x", 1); err != nil {
		panic(err)
	}
	if err := o.Set("y", r.NewArray(1, 2)); err != nil {
		panic(err)
	}
	return o
}

// dataTemplateObjects returns, in r, the eagerly defined object and the one
// made from a template of the probes' `o` with the hint of one property
// more, and the count of the template's factory calls.
func dataTemplateObjects(t testing.TB, r *Runtime) (eager, lazy *Object, made *int) {
	return dataTemplateObjectsExtra(t, r, 1)
}

// dataTemplateObjectsExtra is dataTemplateObjects with the hint extra.
func dataTemplateObjectsExtra(t testing.TB, r *Runtime, extra int) (eager, lazy *Object, made *int) {
	t.Helper()
	eager = r.NewObject()
	for _, p := range []struct {
		k string
		v Value
	}{{"a", r.ToValue(1)}, {"b", r.ToValue("two")}, {"c", dataTemplateNested(r)}, {"d", Null()}, {"e", r.ToValue(5)}} {
		if err := eager.DefineDataProperty(p.k, p.v, FLAG_TRUE, FLAG_TRUE, FLAG_TRUE); err != nil {
			t.Fatal(err)
		}
	}
	made = new(int)
	tmpl := NewObjectTemplate()
	count := func(f func(*Runtime) Value) func(*Runtime) Value {
		return func(r *Runtime) Value { *made++; return f(r) }
	}
	for _, ok := range []bool{
		tmpl.Put("a", count(func(r *Runtime) Value { return r.ToValue(1) })),
		tmpl.Put("b", count(func(r *Runtime) Value { return r.ToValue("two") })),
		tmpl.Put("c", count(dataTemplateNested)),
		tmpl.Put("d", count(func(*Runtime) Value { return Null() })),
	} {
		if !ok {
			t.Fatal("Put refused a property")
		}
	}
	if tmpl.Put("a", func(*Runtime) Value { return Null() }) || tmpl.Put("7", func(*Runtime) Value { return Null() }) {
		t.Fatal("Put took a duplicate or an index")
	}
	lazy = r.NewObjectFromTemplate(tmpl, extra)
	if err := lazy.DefineDataProperty("e", r.ToValue(5), FLAG_TRUE, FLAG_TRUE, FLAG_TRUE); err != nil {
		t.Fatal(err)
	}
	return eager, lazy, made
}

// runEagerAndLazy runs probe against a fresh eager object and fresh ones
// made from a template with and without a capacity hint, each in its own
// runtime, and reports any difference.
func runEagerAndLazy(t *testing.T, name string, probe func(r *Runtime, o *Object) string) {
	t.Helper()
	var got [3]string
	for j := range got {
		r := New()
		if _, err := r.RunString(`function structuredCloneLike(v) { return JSON.stringify(JSON.parse(JSON.stringify(v))); }`); err != nil {
			t.Fatal(err)
		}
		eager, lazy, _ := dataTemplateObjectsExtra(t, r, j-1)
		o := eager
		if j > 0 {
			o = lazy
		}
		got[j] = probe(r, o)
	}
	if got[0] != got[1] || got[0] != got[2] {
		t.Errorf("%s\n           eager %s\n            lazy %s\n lazy, with hint %s", name, got[0], got[1], got[2])
	}
}

func runScriptProbe(probe string) func(r *Runtime, o *Object) string {
	return func(r *Runtime, o *Object) string {
		if err := r.Set("o", o); err != nil {
			return "set: " + err.Error()
		}
		v, err := r.RunString(probe)
		if err != nil {
			return "throws " + err.Error()
		}
		return v.String()
	}
}

func TestObjectTemplateAsEagerObject(t *testing.T) {
	for i, probe := range dataTemplateProbes {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			runEagerAndLazy(t, probe, runScriptProbe(probe))
		})
	}
}

func TestObjectTemplateAuditAsEagerObject(t *testing.T) {
	for i, probe := range dataTemplateAuditProbes {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			runEagerAndLazy(t, probe, runScriptProbe(probe))
		})
	}
}

type dataTemplateStruct struct {
	A int64                  `json:"a"`
	B string                 `json:"b"`
	C map[string]interface{} `json:"c"`
	D interface{}            `json:"d"`
	E int                    `json:"e"`
}

// dataTemplateGoProbes reach the object through this package's Go API.
var dataTemplateGoProbes = []struct {
	name  string
	probe func(r *Runtime, o *Object) string
}{
	{"Export", func(_ *Runtime, o *Object) string { return fmt.Sprint(o.Export()) }},
	{"ExportType", func(_ *Runtime, o *Object) string { return fmt.Sprint(o.ExportType()) }},
	{"ExportTo map", func(r *Runtime, o *Object) string {
		var m map[string]interface{}
		err := r.ExportTo(o, &m)
		return fmt.Sprint(m, err)
	}},
	{"ExportTo map of strings", func(r *Runtime, o *Object) string {
		var m map[string]string
		err := r.ExportTo(o, &m)
		return fmt.Sprint(m, err)
	}},
	{"ExportTo struct", func(r *Runtime, o *Object) string {
		r.SetFieldNameMapper(TagFieldNameMapper("json", true))
		var s dataTemplateStruct
		err := r.ExportTo(o, &s)
		return fmt.Sprintf("%+v %v", s, err)
	}},
	{"ExportTo slice", func(r *Runtime, o *Object) string {
		var s []interface{}
		err := r.ExportTo(o, &s)
		return fmt.Sprint(s, err)
	}},
	{"ExportTo after delete", func(r *Runtime, o *Object) string {
		_ = o.Delete("c")
		var m map[string]interface{}
		err := r.ExportTo(o, &m)
		return fmt.Sprint(m, err, o.Export())
	}},
	{"json.Marshal", func(_ *Runtime, o *Object) string {
		b, err := json.Marshal(o)
		return fmt.Sprint(string(b), err)
	}},
	{"Keys", func(_ *Runtime, o *Object) string { return fmt.Sprint(o.Keys()) }},
	{"AppendKeys", func(_ *Runtime, o *Object) string { return fmt.Sprint(o.AppendKeys(make([]string, 0, 8))) }},
	{"GetOwnPropertyNames", func(_ *Runtime, o *Object) string { return fmt.Sprint(o.GetOwnPropertyNames()) }},
	{"Symbols", func(_ *Runtime, o *Object) string { return fmt.Sprint(len(o.Symbols())) }},
	{"AppendOwnPropertyKeys", func(_ *Runtime, o *Object) string {
		names, syms, ok := o.AppendOwnPropertyKeys(nil, nil)
		return fmt.Sprint(names, syms, ok)
	}},
	{"GetOwnDataProperty", func(_ *Runtime, o *Object) string {
		s := ""
		for _, k := range []string{"a", "b", "c", "d", "e", "zz", "toString"} {
			v, w, e, c, ok := o.GetOwnDataProperty(k)
			if v != nil {
				s += fmt.Sprintf("%s=%v %v%v%v%v;", k, v.Export(), w, e, c, ok)
			} else {
				s += fmt.Sprintf("%s=nil %v%v%v%v;", k, w, e, c, ok)
			}
		}
		return s
	}},
	{"GetOwnDataValue", func(_ *Runtime, o *Object) string {
		s := ""
		for _, k := range []string{"b", "d", "zz"} {
			v, ok := o.GetOwnDataValue(k)
			s += fmt.Sprint(k, v, ok, ";")
		}
		return s
	}},
	{"GetOwnDataPropertySymbol", func(_ *Runtime, o *Object) string {
		v, _, _, _, ok := o.GetOwnDataPropertySymbol(SymIterator)
		return fmt.Sprint(v, ok)
	}},
	{"Get", func(_ *Runtime, o *Object) string {
		return fmt.Sprint(o.Get("a"), o.Get("c").Export(), o.Get("zz"), o.Get("hasOwnProperty") != nil)
	}},
	{"Set existing", func(_ *Runtime, o *Object) string {
		err := o.Set("b", 3)
		return fmt.Sprint(err, o.Export(), o.Keys())
	}},
	{"Set new", func(_ *Runtime, o *Object) string {
		err := o.Set("z", 1)
		return fmt.Sprint(err, o.Keys())
	}},
	{"Delete", func(_ *Runtime, o *Object) string {
		err := o.Delete("b")
		err2 := o.Delete("zz")
		return fmt.Sprint(err, err2, o.Keys(), o.Get("b"))
	}},
	{"Delete then Set", func(_ *Runtime, o *Object) string {
		_ = o.Delete("a")
		err := o.Set("a", 2)
		return fmt.Sprint(err, o.Keys(), o.Export())
	}},
	{"DefineAccessorProperty", func(r *Runtime, o *Object) string {
		err := o.DefineAccessorProperty("b", r.ToValue(func() string { return "g" }), nil, FLAG_TRUE, FLAG_FALSE)
		return fmt.Sprint(err, o.Keys(), o.Get("b"), o.GetOwnPropertyNames())
	}},
	{"DefineDataProperty readonly", func(r *Runtime, o *Object) string {
		err := o.DefineDataProperty("a", r.ToValue(4), FLAG_FALSE, FLAG_FALSE, FLAG_TRUE)
		err2 := o.Set("a", 6)
		err3 := o.Delete("a")
		return fmt.Sprint(err, err2 != nil, err3 != nil, o.Get("a"))
	}},
	{"SetSymbol", func(_ *Runtime, o *Object) string {
		err := o.SetSymbol(SymToStringTag, "T")
		return fmt.Sprint(err, o.String(), len(o.Symbols()))
	}},
	{"Prototype", func(r *Runtime, o *Object) string {
		p := o.Prototype()
		err := o.SetPrototype(nil)
		return fmt.Sprint(p == r.global.ObjectPrototype, err, o.Prototype() == nil, o.Get("toString") == nil)
	}},
	{"ClassName and String", func(_ *Runtime, o *Object) string {
		return fmt.Sprint(o.ClassName(), o.String(), o.ToBoolean(), o.ToObject(nil) == o)
	}},
	{"ToValue", func(r *Runtime, o *Object) string {
		keys := r.ToValue(o.Export()).(*Object).Keys()
		sort.Strings(keys)
		return fmt.Sprint(r.ToValue(o) == o, keys)
	}},
	{"preventExtensions then Set", func(_ *Runtime, o *Object) string {
		o.self.preventExtensions(true)
		err1 := o.Set("q", 1)
		err2 := o.Set("a", 7)
		return fmt.Sprint(err1 != nil, err2, o.Keys(), o.Get("a"))
	}},
}

func TestObjectTemplateGoAPIAsEagerObject(t *testing.T) {
	for _, p := range dataTemplateGoProbes {
		t.Run(p.name, func(t *testing.T) {
			runEagerAndLazy(t, p.name, p.probe)
		})
	}
}

// _putProp, which built-ins and the VM call on objects they make, keeps
// the template's names and gives a deleted property a new place at the
// end, as on an eagerly defined object.
func TestObjectTemplatePutPropAsEagerObject(t *testing.T) {
	for _, tc := range []struct {
		name string
		do   func(o *Object)
	}{
		{"new name", func(o *Object) { o.self._putProp("z", intToValue(1), true, true, true) }},
		{"template name not yet read", func(o *Object) { o.self._putProp("b", intToValue(9), true, true, true) }},
		{"template name read", func(o *Object) { o.Get("b"); o.self._putProp("b", intToValue(9), true, false, true) }},
		{"template name deleted", func(o *Object) { _ = o.Delete("a"); o.self._putProp("a", intToValue(9), true, true, true) }},
		{"later name", func(o *Object) { o.self._putProp("e", intToValue(9), false, true, true) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runEagerAndLazy(t, tc.name, func(r *Runtime, o *Object) string {
				tc.do(o)
				return runScriptProbe(`Reflect.ownKeys(o).join() + JSON.stringify(Object.getOwnPropertyDescriptors(o))`)(r, o)
			})
		})
	}
}

// A property's value is made when it is first needed, once, and the Go
// API's native readers see the object as the eager one.
func TestObjectTemplateMakesValuesOnDemand(t *testing.T) {
	r := New()
	eager, lazy, made := dataTemplateObjects(t, r)
	if *made != 0 {
		t.Fatalf("%d values made before any read", *made)
	}
	if err := r.Set("o", lazy); err != nil {
		t.Fatal(err)
	}
	if v, err := r.RunString(`o.e + o.a + o.a`); err != nil || v.ToInteger() != 7 || *made != 1 {
		t.Fatalf("o.e + o.a + o.a = %v, %v; %d values made, want 1", v, err, *made)
	}
	if v, ok := lazy.GetOwnDataValue("b"); !ok || v.String() != "two" || *made != 2 {
		t.Fatalf("GetOwnDataValue(b) = %v, %v; %d made", v, ok, *made)
	}
	names, symbols, ok := lazy.AppendOwnPropertyKeys(nil, nil)
	want, _, _ := eager.AppendOwnPropertyKeys(nil, nil)
	if !ok || len(symbols) != 0 || fmt.Sprint(names) != fmt.Sprint(want) || *made != 2 {
		t.Fatalf("own keys %v, %v (want %v); %d made", names, ok, want, *made)
	}
	// Listing the enumerable keys (Object.keys) and checking for a
	// property makes nothing.
	if v, err := r.RunString(`Object.keys(o).join() + ("c" in o) + o.hasOwnProperty("d") + Reflect.ownKeys(o).length`); err != nil || v.String() != "a,b,c,d,etruetrue5" || *made != 2 {
		t.Fatalf("Object.keys(o) ... = %v, %v; %d made", v, err, *made)
	}
	if fmt.Sprint(lazy.Keys()) != fmt.Sprint(eager.Keys()) || *made != 4 {
		t.Fatalf("Keys %v, want %v; %d made", lazy.Keys(), eager.Keys(), *made)
	}
	if got, want := fmt.Sprint(lazy.Export()), fmt.Sprint(eager.Export()); got != want {
		t.Fatalf("Export %s, want %s", got, want)
	}
	if *made != 4 {
		t.Fatalf("%d values made, want each made once", *made)
	}
	r2 := New()
	_, lazy2, made2 := dataTemplateObjects(t, r2)
	if got, want := fmt.Sprint(lazy2.Export()), fmt.Sprint(eager.Export()); got != want || *made2 != 4 {
		t.Fatalf("Export of an untouched object %s, want %s; %d made", got, want, *made2)
	}
	if v, w, e, c, ok := lazy2.GetOwnDataProperty("d"); !ok || !IsNull(v) || !w || !e || !c {
		t.Fatalf("GetOwnDataProperty(d) = %v %v %v %v %v", v, w, e, c, ok)
	}
	// A deleted property is not made again: Keys makes a, b and d.
	r3 := New()
	_, lazy3, made3 := dataTemplateObjects(t, r3)
	if err := lazy3.Delete("c"); err != nil {
		t.Fatal(err)
	}
	if *made3 != 1 {
		t.Fatalf("deleting made %d values, want 1 (to check it may be deleted)", *made3)
	}
	if fmt.Sprint(lazy3.Keys()) != "[a b d e]" || lazy3.Get("c") != nil || *made3 != 4 {
		t.Fatalf("after delete: keys %v, c %v, %d made", lazy3.Keys(), lazy3.Get("c"), *made3)
	}
}

// Names that are special to an ordinary object, or near the integer
// indices Put refuses, behave as on an eagerly defined object.
func TestObjectTemplateSpecialNames(t *testing.T) {
	names := []string{"__proto__", "", "é", "日本", "4294967295", "-0", "01", "1.5", "-1", "constructor", "toString", "length"}
	for _, idx := range []string{"0", "1", "4294967294"} {
		if NewObjectTemplate().Put(idx, func(*Runtime) Value { return Null() }) {
			t.Fatalf("Put took the index %q", idx)
		}
	}
	tmpl := NewObjectTemplate()
	for i, n := range names {
		v := int64(i)
		if !tmpl.Put(n, func(*Runtime) Value { return intToValue(v) }) {
			t.Fatalf("Put refused %q", n)
		}
	}
	if tmpl.Len() != len(names) {
		t.Fatalf("Len %d, want %d", tmpl.Len(), len(names))
	}
	for i, probe := range []string{
		`Reflect.ownKeys(o).join("|") + JSON.stringify(o)`,
		`Object.getPrototypeOf(o) === Object.prototype && o.__proto__ + "" + o.constructor + o.toString + o.length`,
		`o.__proto__ = null; Object.getPrototypeOf(o) === Object.prototype && JSON.stringify(o)`,
		`o[4294967295] = "x"; o[4294967294] = "y"; Reflect.ownKeys(o).join("|")`,
		`delete o.__proto__; o.__proto__ = null; (Object.getPrototypeOf(o) === null) + Reflect.ownKeys(o).join("|")`,
		`var out = []; for (var k in o) out.push(k); out.join("|") + o["é"] + o["日本"] + o[""]`,
		`JSON.stringify({...o})`,
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			var got [2]string
			for j := range got {
				r := New()
				var o *Object
				if j == 0 {
					o = r.NewObject()
					for i, n := range names {
						if err := o.DefineDataProperty(n, intToValue(int64(i)), FLAG_TRUE, FLAG_TRUE, FLAG_TRUE); err != nil {
							t.Fatal(err)
						}
					}
				} else {
					o = r.NewObjectFromTemplate(tmpl, 0)
				}
				got[j] = runScriptProbe(probe)(r, o)
			}
			if got[0] != got[1] {
				t.Errorf("%s\n eager %s\n  lazy %s", probe, got[0], got[1])
			}
		})
	}
}

func TestObjectTemplatePut(t *testing.T) {
	tmpl := NewObjectTemplate()
	if tmpl.Len() != 0 {
		t.Fatalf("Len of an empty template %d", tmpl.Len())
	}
	if tmpl.Put("a", nil) {
		t.Fatal("Put took a nil factory")
	}
	if !tmpl.Put("a", func(*Runtime) Value { return nil }) || !tmpl.Put("b", func(*Runtime) Value { return _undefined }) {
		t.Fatal("Put refused a property")
	}
	r := New()
	o := r.NewObjectFromTemplate(tmpl, 0)
	if tmpl.Put("c", func(*Runtime) Value { return Null() }) || tmpl.Len() != 2 {
		t.Fatalf("Put took a property after an object was made; Len %d", tmpl.Len())
	}
	// A factory that returns nil makes undefined, as defining a property
	// with a nil value does.
	if err := r.Set("o", o); err != nil {
		t.Fatal(err)
	}
	v, err := r.RunString(`Reflect.ownKeys(o).join() + o.hasOwnProperty("a") + ("a" in o) + typeof o.a + JSON.stringify(Object.getOwnPropertyDescriptor(o, "a")) + Object.keys(o).join()`)
	if err != nil {
		t.Fatal(err)
	}
	if want := `a,btruetrueundefined{"writable":true,"enumerable":true,"configurable":true}a,b`; v.String() != want {
		t.Fatalf("got %s, want %s", v, want)
	}
	if v, ok := r.NewObjectFromTemplate(tmpl, 0).GetOwnDataValue("a"); !ok || v != _undefined {
		t.Fatalf("GetOwnDataValue(a) = %v, %v", v, ok)
	}
	// An empty template makes an empty ordinary object.
	e := r.NewObjectFromTemplate(NewObjectTemplate(), 2)
	if err := e.Set("x", 1); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(e.Keys()) != "[x]" || e.Prototype() != r.global.ObjectPrototype {
		t.Fatalf("empty template: keys %v", e.Keys())
	}
	// Negative extra is no hint. Export, before anything else has read
	// the object, lists every property.
	n := r.NewObjectFromTemplate(tmpl, -5)
	if got := fmt.Sprint(n.Export()); got != "map[a:<nil> b:<nil>]" {
		t.Fatalf("negative extra: Export %s", got)
	}
	if fmt.Sprint(n.Keys()) != "[a b]" {
		t.Fatalf("negative extra: keys %v", n.Keys())
	}
}

// Making an object from a template is one allocation, whatever the
// template's size, as NewObject is.
func TestObjectTemplateOneAllocation(t *testing.T) {
	r := New()
	tmpl := NewObjectTemplate()
	for i := 0; i < 12; i++ {
		tmpl.Put(fmt.Sprintf("k%d", i), func(*Runtime) Value { return intToValue(1) })
	}
	tmpl.Put("x", func(*Runtime) Value { return valueTrue }) // sealed after the first object
	r.NewObjectFromTemplate(tmpl, 0)
	plain := testing.AllocsPerRun(100, func() { r.NewObject() })
	got := testing.AllocsPerRun(100, func() { r.NewObjectFromTemplate(tmpl, 0) })
	if got != plain || got != 1 {
		t.Fatalf("NewObjectFromTemplate %v allocations, NewObject %v; want 1", got, plain)
	}
}

// The allocation counter charges an object made from a template what it
// charges NewObject: once every value has been made, the total is what an
// eager definition of the same values charges, and before that, no more.
func TestObjectTemplateAllocationCharge(t *testing.T) {
	tmpl := NewObjectTemplate()
	tmpl.Put("a", func(*Runtime) Value { return intToValue(1) })
	tmpl.Put("b", func(*Runtime) Value { return asciiString("two") })
	tmpl.Put("c", dataTemplateNested)
	tmpl.Put("d", func(*Runtime) Value { return _null })
	for _, extra := range []int{0, 1, 4} {
		eagerR, lazyR := New(), New()
		eagerR.SetAllocationLimit(0)
		lazyR.SetAllocationLimit(0)
		eager := eagerR.NewObject()
		for _, k := range []string{"a", "b", "c", "d"} {
			if err := eager.DefineDataProperty(k, tmpl.t.props[unistring.String(k)](eagerR), FLAG_TRUE, FLAG_TRUE, FLAG_TRUE); err != nil {
				t.Fatal(err)
			}
		}
		lazy := lazyR.NewObjectFromTemplate(tmpl, extra)
		if got, want := lazyR.AllocatedBytes(), eagerR.AllocatedBytes(); got > want || got != allocSizeBaseObject {
			t.Fatalf("extra=%d: an unread object charged %d, eager %d, NewObject %d", extra, got, want, allocSizeBaseObject)
		}
		for i := 0; i < extra; i++ {
			k := fmt.Sprintf("x%d", i)
			if err := eager.DefineDataProperty(k, intToValue(1), FLAG_TRUE, FLAG_TRUE, FLAG_TRUE); err != nil {
				t.Fatal(err)
			}
			if err := lazy.DefineDataProperty(k, intToValue(1), FLAG_TRUE, FLAG_TRUE, FLAG_TRUE); err != nil {
				t.Fatal(err)
			}
		}
		lazy.Export()
		if got, want := lazyR.AllocatedBytes(), eagerR.AllocatedBytes(); got != want {
			t.Fatalf("extra=%d: a read object charged %d, eager %d", extra, got, want)
		}
	}
}

// A template made once serves runtimes on several goroutines at a time.
func TestObjectTemplateSharedAcrossRuntimes(t *testing.T) {
	tmpl := NewObjectTemplate()
	for i := 0; i < 8; i++ {
		v := int64(i)
		tmpl.Put(fmt.Sprintf("k%d", i), func(r *Runtime) Value {
			o := r.NewObject()
			_ = o.Set("v", v)
			return o
		})
	}
	const want = `{"k0":{"v":0},"k1":{"v":1},"k2":{"v":2},"k3":{"v":3},"k4":{"v":4},"k5":{"v":5},"k6":{"v":6},"k7":{"v":7}}`
	var wg sync.WaitGroup
	errs := make(chan string, 4)
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := New()
			stringify, _ := AssertFunction(r.Get("JSON").(*Object).Get("stringify"))
			for i := 0; i < 50; i++ {
				v, err := stringify(nil, r.NewObjectFromTemplate(tmpl, 0))
				if err != nil || v.String() != want {
					errs <- fmt.Sprint(v, err)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}

var dataTemplateBenchNames = []string{"id", "name", "type", "priority", "effectiveFrom", "effectiveTo", "status", "tags", "maxPerDay", "points", "currency", "description"}

func dataTemplateBenchValue(i int) func(*Runtime) Value {
	switch i % 3 {
	case 0:
		return func(*Runtime) Value { return intToValue(int64(i)) }
	case 1:
		s := asciiString(fmt.Sprintf("value-%d", i))
		return func(*Runtime) Value { return s }
	default:
		return func(r *Runtime) Value { return r.newArrayValues([]Value{intToValue(1), intToValue(2)}) }
	}
}

// BenchmarkObjectTemplate compares an object of 12 properties made from a
// template with the same object defined eagerly (each value made and
// defined), when nothing reads it, when two properties are read, and when
// every property is.
func BenchmarkObjectTemplate(b *testing.B) {
	tmpl := NewObjectTemplate()
	factories := make([]func(*Runtime) Value, len(dataTemplateBenchNames))
	for i, n := range dataTemplateBenchNames {
		factories[i] = dataTemplateBenchValue(i)
		tmpl.Put(n, factories[i])
	}
	keys := make([]unistring.String, len(dataTemplateBenchNames))
	for i, n := range dataTemplateBenchNames {
		keys[i] = unistring.NewFromString(n)
	}
	r := New()
	eager := func() *Object {
		o := r.NewObject()
		for i, k := range keys {
			o.self.defineOwnPropertyStr(k, PropertyDescriptor{Value: factories[i](r), Writable: FLAG_TRUE, Enumerable: FLAG_TRUE, Configurable: FLAG_TRUE}, true)
		}
		return o
	}
	eagerDefine := func() *Object {
		o := r.NewObject()
		for i, n := range dataTemplateBenchNames {
			_ = o.DefineDataProperty(n, factories[i](r), FLAG_TRUE, FLAG_TRUE, FLAG_TRUE)
		}
		return o
	}
	lazy := func() *Object { return r.NewObjectFromTemplate(tmpl, 0) }
	for _, mk := range []struct {
		name string
		f    func() *Object
	}{{"eager", eager}, {"eagerDefineDataProperty", eagerDefine}, {"template", lazy}} {
		b.Run(mk.name+"/unread", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				mk.f()
			}
		})
		b.Run(mk.name+"/read2", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				o := mk.f()
				o.self.getStr("name", nil)
				o.self.getStr("points", nil)
			}
		})
		b.Run(mk.name+"/readAll", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				o := mk.f()
				for _, k := range keys {
					o.self.getStr(k, nil)
				}
			}
		})
	}
}
