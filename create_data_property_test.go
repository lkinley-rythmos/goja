package goja

import (
	"errors"
	"strings"
	"testing"
)

// TestEngineCreatedObjectsDefineProperties: the properties the engine puts
// on an object it has just made, where ECMAScript creates them
// (CreateDataPropertyOrThrow), are defined, not assigned: with a getter and
// a setter for each of the names on Object.prototype, Array.prototype,
// Error.prototype and TypeError.prototype, making the object runs neither,
// and the object has its own data properties, with the attributes Node.js
// 18.20.3 gives them (run in a fresh vm context). Node.js 18 has no
// Uint8Array.prototype.setFromHex or setFromBase64: their rows follow
// ECMAScript (as Node.js 26.8.1 does); NewGoError has no Node.js
// counterpart and keeps the attributes it had.
func TestEngineCreatedObjectsDefineProperties(t *testing.T) {
	r := New()
	_, err := r.RunString(`
	globalThis.calls = 0;
	for (const proto of [Object.prototype, Array.prototype, Error.prototype, TypeError.prototype]) {
		for (const name of ["value", "done", "index", "input", "read", "written", "code"]) {
			Object.defineProperty(proto, name, {__proto__: null, get() { calls++; return "proto"; }, set(v) { calls++; }, configurable: true});
		}
	}
	globalThis.own = (o, keys) => keys.map(k => {
		const d = Object.getOwnPropertyDescriptor(o, k);
		if (d === undefined) {
			return k + "=absent";
		}
		const v = typeof d.value === "object" && d.value !== null ? "object" : JSON.stringify(d.value);
		return k + "=" + v + "/" + d.writable + "/" + d.enumerable + "/" + d.configurable;
	}).join(" ");
	`)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		label, expr, want, keys string
		before                  func()
	}{
		{"generator next", `(function* () { yield 1; })().next()`, `value=1/true/true/true done=false/true/true/true`, `["value", "done"]`, nil},
		{"array iterator next", `[1][Symbol.iterator]().next()`, `value=1/true/true/true done=false/true/true/true`, `["value", "done"]`, nil},
		{"map iterator next", `new Map([[1, 2]]).keys().next()`, `value=1/true/true/true done=false/true/true/true`, `["value", "done"]`, nil},
		{"set iterator next", `new Set([1]).values().next()`, `value=1/true/true/true done=false/true/true/true`, `["value", "done"]`, nil},
		{"string iterator next", `"a"[Symbol.iterator]().next()`, `value="a"/true/true/true done=false/true/true/true`, `["value", "done"]`, nil},
		{"regexp string iterator next", `"a".matchAll(/a/g).next()`, `value=object/true/true/true done=false/true/true/true`, `["value", "done"]`, nil},
		{"regexp exec", `/b/.exec("abc")`, `index=1/true/true/true input="abc"/true/true/true`, `["index", "input"]`, nil},
		{"string match", `"abc".match(/b/)`, `index=1/true/true/true input="abc"/true/true/true`, `["index", "input"]`, nil},
		{"Uint8Array setFromHex", `new Uint8Array(2).setFromHex("0102")`, `read=4/true/true/true written=2/true/true/true`, `["read", "written"]`, nil},
		{"Uint8Array setFromBase64", `new Uint8Array(2).setFromBase64("AQI=")`, `read=4/true/true/true written=2/true/true/true`, `["read", "written"]`, nil},
		{"dynamic import error", `importError`, `code="ERR_VM_DYNAMIC_IMPORT_CALLBACK_MISSING"/true/true/true`, `["code"]`, func() {
			if _, err := r.RunString(`import("x").catch(e => { globalThis.importError = e; });`); err != nil {
				t.Fatal(err)
			}
		}},
		{"NewGoError", `goError`, `value=object/true/true/true`, `["value"]`, func() {
			r.Set("goError", r.NewGoError(errors.New("boom")))
		}},
	} {
		r.Set("calls", 0)
		if row.before != nil {
			row.before()
		}
		v, err := r.RunString(`own(` + row.expr + `, ` + row.keys + `)`)
		if err != nil {
			t.Fatalf("%s: %v", row.label, err)
		}
		if calls := r.Get("calls").ToInteger(); calls != 0 {
			t.Errorf("%s: %d getters or setters ran", row.label, calls)
		}
		if got := v.String(); got != row.want {
			t.Errorf("%s:\n got %s\nwant %s", row.label, got, row.want)
		}
	}
}

// TestRegExpResultKeyOrder: a RegExp match result has index before input,
// as ECMAScript makes them and Node.js 18.20.3 lists them, in
// Reflect.ownKeys, in Object.keys and in what JSON.stringify gives of a
// copy, for exec, match and matchAll, with named groups and with the d
// flag's indices.
func TestRegExpResultKeyOrder(t *testing.T) {
	const SCRIPT = `
	const show = o => JSON.stringify(Reflect.ownKeys(o)) + " " + JSON.stringify(Object.keys(o)) + " " + JSON.stringify(Object.assign({}, o));
	const cases = [
		[/b/.exec("abc"), '["0","length","index","input","groups"] ["0","index","input","groups"] {"0":"b","index":1,"input":"abc"}'],
		[/(?<x>b)/.exec("abc"), '["0","1","length","index","input","groups"] ["0","1","index","input","groups"] {"0":"b","1":"b","index":1,"input":"abc","groups":{"x":"b"}}'],
		[/(?<x>b)/d.exec("abc"), '["0","1","length","index","input","groups","indices"] ["0","1","index","input","groups","indices"] {"0":"b","1":"b","index":1,"input":"abc","groups":{"x":"b"},"indices":[[1,2],[1,2]]}'],
		[/(?<x>b)/d.exec("abc").indices, '["0","1","length","groups"] ["0","1","groups"] {"0":[1,2],"1":[1,2],"groups":{"x":[1,2]}}'],
		["abc".match(/b/), '["0","length","index","input","groups"] ["0","index","input","groups"] {"0":"b","index":1,"input":"abc"}'],
		[[..."abcb".matchAll(/(?<x>b)/g)][1], '["0","1","length","index","input","groups"] ["0","1","index","input","groups"] {"0":"b","1":"b","index":3,"input":"abcb","groups":{"x":"b"}}'],
	];
	cases.forEach(([result, want], i) => assert.sameValue(show(result), want, "case " + i));
	`
	testScriptWithTestLib(SCRIPT, _undefined, t)
}

// objectLiteralHarness installs, on Object.prototype and Array.prototype,
// counting getters and setters for the keys the rows use, and defines
// describe(o, keys), which gives the own keys, each key's descriptor and
// the prototype.
const objectLiteralHarness = `
globalThis.calls = 0;
globalThis.sym = Symbol("sym");
globalThis.k = "k";
globalThis.one = 1;
globalThis.P = "__proto__";
globalThis.protoObj = {marker: 1};
for (const proto of [Object.prototype, Array.prototype]) {
	for (const name of ["k", "1", "m", "g", "s", "f", "x", "0", "2", sym]) {
		Object.defineProperty(proto, name, {__proto__: null, get() { calls++; return "proto"; }, set(v) { calls++; }, configurable: true});
	}
}
globalThis.describe = (o, ks) => {
	const d = k => {
		const x = Object.getOwnPropertyDescriptor(o, k);
		if (x === undefined) return "absent";
		if (Object.prototype.hasOwnProperty.call(x, "get")) return "accessor/" + typeof x.get + "/" + typeof x.set + "/" + x.enumerable + "/" + x.configurable;
		const v = typeof x.value === "function" ? "fn:" + x.value.name : typeof x.value === "object" && x.value !== null ? "object" : String(x.value);
		return v + "/" + x.writable + "/" + x.enumerable + "/" + x.configurable;
	};
	const proto = Object.getPrototypeOf(o);
	return "keys=" + Reflect.ownKeys(o).map(String).join(",") + " " + ks.map(k => String(k) + "=" + d(k)).join(" ") +
		" proto=" + (proto === Object.prototype ? "Object.prototype" : proto === null ? "null" : "other");
};
`

// TestObjectLiteralDefinesProperties: an object literal's properties, with
// computed keys too, a class's fields and methods, computed or not, and
// array literals are created, not assigned: no inherited setter or getter
// runs, an own accessor is replaced, a computed "__proto__" is an own
// property while a literal __proto__ sets the prototype, and an anonymous
// function, arrow function or class value of an object literal's computed
// key is named after the key (a class field's is not, see
// TestComputedKeyClassName). Each row is as Node.js 18.20.3 has it (run in
// a fresh vm context); the static block's assignment runs the inherited
// setter there too.
func TestObjectLiteralDefinesProperties(t *testing.T) {
	r := New()
	if _, err := r.RunString(objectLiteralHarness); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ label, expr, keys, want string }{
		{"computed string", `{[k]: 1}`, `["k"]`, `keys=k k=1/true/true/true proto=Object.prototype calls=0`},
		{"computed number", `{[one]: 1}`, `["1"]`, `keys=1 1=1/true/true/true proto=Object.prototype calls=0`},
		{"computed symbol", `{[sym]: 1}`, `[sym]`, `keys=Symbol(sym) Symbol(sym)=1/true/true/true proto=Object.prototype calls=0`},
		{"computed anonymous function", `{[k]: function () {}}`, `["k"]`, `keys=k k=fn:k/true/true/true proto=Object.prototype calls=0`},
		{"computed anonymous arrow", `{[k]: () => {}}`, `["k"]`, `keys=k k=fn:k/true/true/true proto=Object.prototype calls=0`},
		{"computed anonymous class", `{[k]: class {}}`, `["k"]`, `keys=k k=fn:k/true/true/true proto=Object.prototype calls=0`},
		{"computed named class", `{[k]: class A {}}`, `["k"]`, `keys=k k=fn:A/true/true/true proto=Object.prototype calls=0`},
		{"computed named function", `{[k]: function f() {}}`, `["k"]`, `keys=k k=fn:f/true/true/true proto=Object.prototype calls=0`},
		{"computed parenthesised sequence", `{[k]: (0, function () {})}`, `["k"]`, `keys=k k=fn:/true/true/true proto=Object.prototype calls=0`},
		{"computed symbol anonymous function", `{[sym]: function () {}}`, `[sym]`, `keys=Symbol(sym) Symbol(sym)=fn:[sym]/true/true/true proto=Object.prototype calls=0`},
		{"computed __proto__", `{[P]: protoObj}`, `["__proto__"]`, `keys=__proto__ __proto__=object/true/true/true proto=Object.prototype calls=0`},
		{"literal __proto__", `{__proto__: protoObj}`, `["__proto__"]`, `keys= __proto__=absent proto=other calls=0`},
		{"string literal __proto__", `{"__proto__": protoObj}`, `["__proto__"]`, `keys= __proto__=absent proto=other calls=0`},
		{"shorthand __proto__", `(function (__proto__) { return {__proto__}; })(protoObj)`, `["__proto__"]`, `keys=__proto__ __proto__=object/true/true/true proto=Object.prototype calls=0`},
		{"computed method", `{[k]() {}}`, `["k"]`, `keys=k k=fn:k/true/true/true proto=Object.prototype calls=0`},
		{"computed getter", `{get [k]() { return 1; }}`, `["k"]`, `keys=k k=accessor/function/undefined/true/true proto=Object.prototype calls=0`},
		{"computed setter", `{set [k](v) {}}`, `["k"]`, `keys=k k=accessor/undefined/function/true/true proto=Object.prototype calls=0`},
		{"getter then computed value", `{get [k]() { return 1; }, [k]: 2}`, `["k"]`, `keys=k k=2/true/true/true proto=Object.prototype calls=0`},
		{"key order", `{b: 1, [k]: 2, a: 3, [one]: 4}`, `["b", "k", "a", "1"]`, `keys=1,b,k,a b=1/true/true/true k=2/true/true/true a=3/true/true/true 1=4/true/true/true proto=Object.prototype calls=0`},
		{"spread", `{...{k: 1, [sym]: 2}}`, `["k", sym]`, `keys=k,Symbol(sym) k=1/true/true/true Symbol(sym)=2/true/true/true proto=Object.prototype calls=0`},
		{"spread then computed", `{...{x: 1}, [k]: 2}`, `["x", "k"]`, `keys=x,k x=1/true/true/true k=2/true/true/true proto=Object.prototype calls=0`},
		{"array with a hole", `[1, , 3]`, `["0", "1", "2"]`, `keys=0,2,length 0=1/true/true/true 1=absent 2=3/true/true/true proto=other calls=0`},
		{"array spread", `[...[1, 2]]`, `["0", "1"]`, `keys=0,1,length 0=1/true/true/true 1=2/true/true/true proto=other calls=0`},
		{"class computed fields", `new (class { [k] = 1; [sym] = function () {}; })()`, `["k", sym]`, `keys=k,Symbol(sym) k=1/true/true/true Symbol(sym)=fn:undefined/true/true/true proto=other calls=0`},
		{"class computed arrow field", `new (class { [k] = () => {}; })()`, `["k"]`, `keys=k k=fn:undefined/true/true/true proto=other calls=0`},
		{"class computed named class field", `new (class { [k] = class B {}; })()`, `["k"]`, `keys=k k=fn:B/true/true/true proto=other calls=0`},
		{"class static computed field", `(class C { static [k] = 2; })`, `["k"]`, `keys=length,name,prototype,k k=2/true/true/true proto=other calls=0`},
		{"class computed methods", `(class { [k]() {} get [one]() { return 1; } }).prototype`, `["k", "1"]`, `keys=1,constructor,k k=fn:k/true/false/true 1=accessor/function/undefined/false/true proto=Object.prototype calls=0`},
		{"class static computed method", `(class C { static [k]() {} })`, `["k"]`, `keys=length,name,prototype,k k=fn:k/true/false/true proto=other calls=0`},
		{"class static block assignment", `(class C { static { this.x = 1; } })`, `["x"]`, `keys=length,name,prototype x=absent proto=other calls=1`},
	} {
		r.Set("calls", 0)
		v, err := r.RunString(`describe(` + row.expr + `, ` + row.keys + `) + " calls=" + calls`)
		if err != nil {
			t.Errorf("%s: %v", row.label, err)
			continue
		}
		if got := v.String(); got != row.want {
			t.Errorf("%s:\n got %s\nwant %s", row.label, got, row.want)
		}
	}
}

// TestArrayUnscopables: Array.prototype[Symbol.unscopables] has Node.js
// 18.20.3's names, in its order, each an own data property that is true
// and writable, enumerable and configurable, on an object with no
// prototype; a with statement on an array leaves each of them, and no
// other array method, to the outer scope. (Node.js 18 has no toReversed,
// toSorted or toSpliced, which goja has; they are not unscopable.)
func TestArrayUnscopables(t *testing.T) {
	const SCRIPT = `
	const names = ["copyWithin", "entries", "fill", "find", "findIndex", "flat", "flatMap", "includes", "keys", "values", "at", "findLast", "findLastIndex"];
	const u = Array.prototype[Symbol.unscopables];
	assert.sameValue(JSON.stringify(Reflect.ownKeys(u)), JSON.stringify(names), "names");
	assert.sameValue(Object.getPrototypeOf(u), null, "prototype");
	const d = Object.getOwnPropertyDescriptor(Array.prototype, Symbol.unscopables);
	assert.sameValue(d.value === u && !d.writable && !d.enumerable && d.configurable, true, "Array.prototype[Symbol.unscopables]");
	for (const name of names) {
		assert.sameValue(JSON.stringify(Object.getOwnPropertyDescriptor(u, name)), '{"value":true,"writable":true,"enumerable":true,"configurable":true}', name);
		const seen = new Function("arr", "var " + name + ' = "outer"; with (arr) { return ' + name + "; }")([]);
		assert.sameValue(seen, "outer", "with: " + name);
	}
	for (const name of ["concat", "join", "map", "push", "slice"]) {
		const seen = new Function("arr", "var " + name + ' = "outer"; with (arr) { return typeof ' + name + "; }")([]);
		assert.sameValue(seen, "function", "with: " + name);
	}
	`
	testScriptWithTestLib(SCRIPT, _undefined, t)
}

// TestComputedKeyClassName: how a function, arrow function or class
// without a name of its own is named after where it is written, and how
// calling a class without new names it, as Node.js 18.20.3 (production
// rle-api) has it, which differs from ECMAScript and later Node.js:
//   - an object literal's computed key, constant ones (["x"]) too, names
//     the value; a class is named before its static fields and blocks run
//     and again once made, whatever name they gave it (a static name
//     field, method or getter, a deleted name), except that, unlike
//     Node.js 18, a non-configurable name is kept and a frozen class stays
//     frozen (the rows marked so);
//   - a class's field with a computed key, constant ones too, does not
//     name its value: a class keeps "", a function or arrow function gets
//     an undefined name;
//   - calling a class without new names it only by a name it was given
//     where it is written (its own, or its binding's, property key's,
//     field's or parameter's), never by a computed key.
//
// Each expected value is Node.js 18.20.3's, run in a fresh vm context, but
// for the rows marked otherwise.
func TestComputedKeyClassName(t *testing.T) {
	r := New()
	if _, err := r.RunString(`globalThis.k = "x";
globalThis.sym = Symbol("s");
globalThis.desc = (o, p) => { const d = Object.getOwnPropertyDescriptor(o, p); return d === undefined ? "absent" : JSON.stringify(d); };
globalThis.msg = f => { try { f(); return "no throw"; } catch (e) { return e.constructor.name + ": " + e.message; } };
globalThis.nameOf = f => {
	try {
		const n = f();
		return typeof n === "string" ? JSON.stringify(n) : typeof n;
	} catch (e) {
		return e.constructor.name;
	}
};
`); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ label, fn, want string }{
		{"instance computed field, static name field", `() => new (class { [k] = class { static name = "kept"; }; })()[k].name`, `"kept"`},
		{"instance computed field, static name method", `() => typeof new (class { [k] = class { static name() {} }; })()[k].name`, `"function"`},
		{"instance computed field, static name getter", `() => new (class { [k] = class { static get name() { return "got"; } }; })()[k].name`, `"got"`},
		{"instance computed field, static block non-configurable", `() => new (class { [k] = class { static { Object.defineProperty(this, "name", {value: "fixed", configurable: false}); } }; })()[k].name`, `"fixed"`},
		{"instance computed field, static block reads name", `() => { let seen; new (class { [k] = class { static { seen = this.name; } }; })(); return seen; }`, `""`},
		{"instance computed field, plain class", `() => new (class { [k] = class {}; })()[k].name`, `""`},
		{"instance computed symbol field, plain class", `() => new (class { [sym] = class {}; })()[sym].name`, `""`},
		{"instance computed field, function", `() => new (class { [k] = function () {}; })()[k].name`, `undefined`},
		{"instance computed field, arrow", `() => new (class { [k] = () => {}; })()[k].name`, `undefined`},
		{"static computed field, static name field", `() => (class { static [k] = class { static name = "kept"; }; })[k].name`, `"kept"`},
		{"static computed field, plain class", `() => (class { static [k] = class {}; })[k].name`, `""`},
		{"static computed field, static block reads name", `() => { let seen; (class { static [k] = class { static { seen = this.name; } }; }); return seen; }`, `""`},
		{"static computed field, derived plain class", `() => (class { static [k] = class extends Object {}; })[k].name`, `""`},
		{"object literal computed, static name field", `() => ({[k]: class { static name = "kept"; }})[k].name`, `"x"`},
		{"object literal computed, static name getter", `() => ({[k]: class { static get name() { return "got"; } }})[k].name`, `"x"`},
		// Not Node.js 18's ("x"): a non-configurable name is kept.
		{"object literal computed, static block non-configurable", `() => ({[k]: class { static { Object.defineProperty(this, "name", {value: "fixed", configurable: false}); } }})[k].name`, `"fixed"`},
		{"object literal computed, static block reads name", `() => { let seen; ({[k]: class { static { seen = this.name; } }}); return seen; }`, `"x"`},
		{"object literal computed, derived static block reads name", `() => { let seen; ({[k]: class extends Object { static { seen = this.name; } }}); return seen; }`, `"x"`},
		{"object literal computed symbol, plain class", `() => ({[sym]: class {}})[sym].name`, `"[s]"`},
		{"object literal computed, function", `() => ({[k]: function () {}})[k].name`, `"x"`},
		{"plain field, static name field", `() => new (class { f = class { static name = "kept"; }; })().f.name`, `"kept"`},
		{"plain field, static block reads name", `() => { let seen; new (class { f = class { static { seen = this.name; } }; })(); return seen; }`, `"f"`},
		{"object literal plain, static block reads name", `() => { let seen; ({f: class { static { seen = this.name; } }}); return seen; }`, `"f"`},
		{"variable, static name field", `() => { const v = class { static name = "kept"; }; return v.name; }`, `"kept"`},
		{"variable, static block reads name", `() => { let seen; const v = class { static { seen = this.name; } }; return seen; }`, `"v"`},
		{"assignment, static block reads name", `() => { let seen, v; v = class { static { seen = this.name; } }; return seen; }`, `"v"`},
		{"default value, static block reads name", `() => { let seen; (function (d = class { static { seen = this.name; } }) {})(); return seen; }`, `"d"`},
		{"destructuring default, static block reads name", `() => { let seen; const {d = class { static { seen = this.name; } }} = {}; return seen; }`, `"d"`},
		{"object literal computed, static name method", `() => typeof ({[k]: class { static name() {} }})[k].name`, `"string"`},
		{"variable, static name method", `() => { const v = class { static name() {} }; return typeof v.name; }`, `"function"`},
		{"variable, static name getter", `() => { const v = class { static get name() { return "got"; } }; return v.name; }`, `"got"`},
		{"object literal constant computed key, static name field", `() => desc(({["x"]: class { static name = "kept"; }})["x"], "name")`, `"{\"value\":\"x\",\"writable\":false,\"enumerable\":false,\"configurable\":true}"`},
		{"object literal constant computed key, static block reads name", `() => { let s; ({["x"]: class { static { s = this.name; } }}); return s; }`, `"x"`},
		{"object literal constant computed key, static name method", `() => typeof ({["x"]: class { static name() {} }})["x"].name`, `"string"`},
		{"object literal constant computed number key, class", `() => desc(({[1]: class {}})[1], "name")`, `"{\"value\":\"1\",\"writable\":false,\"enumerable\":false,\"configurable\":true}"`},
		{"object literal constant computed key, function", `() => desc(({["x"]: function () {}})["x"], "name")`, `"{\"value\":\"x\",\"writable\":false,\"enumerable\":false,\"configurable\":true}"`},
		{"object literal string key, class with static name field", `() => desc(({"s": class { static name = "kept"; }}).s, "name")`, `"{\"value\":\"kept\",\"writable\":true,\"enumerable\":true,\"configurable\":true}"`},
		{"object literal computed key, class name descriptor", `() => { const c = ({[k]: class {}})[k]; return desc(c, "name") + " " + Reflect.ownKeys(c).join(","); }`, `"{\"value\":\"x\",\"writable\":false,\"enumerable\":false,\"configurable\":true} length,name,prototype"`},
		{"object literal computed key, static name field and another", `() => { const c = ({[k]: class { static name = "kept"; static y = 1; }})[k]; return desc(c, "name") + " " + Reflect.ownKeys(c).join(","); }`, `"{\"value\":\"x\",\"writable\":false,\"enumerable\":false,\"configurable\":true} length,name,prototype,y"`},
		// Not Node.js 18's ("fixed", then "x" configurable): a non-configurable
		// name is kept, with no error.
		{"object literal computed key, static block fixes name", `() => { let inBlock; const c = ({[k]: class { static { Object.defineProperty(this, "name", {value: "fixed", configurable: false}); inBlock = this.name; } }})[k]; return inBlock + " " + desc(c, "name"); }`, `"fixed {\"value\":\"fixed\",\"writable\":true,\"enumerable\":true,\"configurable\":false}"`},
		{"object literal computed key, static block deletes name", `() => { const c = ({[k]: class { static { delete this.name; } }})[k]; return desc(c, "name") + " " + Reflect.ownKeys(c).join(","); }`, `"{\"value\":\"x\",\"writable\":false,\"enumerable\":false,\"configurable\":true} length,prototype,name"`},
		// Not Node.js 18's, which throws a TypeError adding the name
		// back: a class made non-extensible gets no name it lacks.
		{"object literal computed key, static block deletes name and prevents extensions", `() => { const c = ({[k]: class { static { delete this.name; Object.preventExtensions(this); } }})[k]; return desc(c, "name") + " extensible=" + Object.isExtensible(c); }`, `"absent extensible=false"`},
		// Not Node.js 18's (configurable true): the frozen class stays frozen.
		{"object literal computed key, static block freezes", `() => { const c = ({[k]: class { static { Object.freeze(this); } }})[k]; return desc(c, "name") + " frozen=" + Object.isFrozen(c); }`, `"{\"value\":\"x\",\"writable\":false,\"enumerable\":true,\"configurable\":false} frozen=true"`},
		{"object literal computed key, static name method descriptor", `() => desc(({[k]: class { static name() {} }})[k], "name")`, `"{\"value\":\"x\",\"writable\":false,\"enumerable\":false,\"configurable\":true}"`},
		{"class computed field, function name descriptor", `() => { const f = new (class { [k] = function () {}; })()[k]; return desc(f, "name"); }`, `"{\"writable\":false,\"enumerable\":false,\"configurable\":true}"`},
		{"class computed field, arrow name descriptor", `() => desc(new (class { [k] = () => {}; })()[k], "name")`, `"{\"writable\":false,\"enumerable\":false,\"configurable\":true}"`},
		{"class computed field, class name descriptor", `() => desc(new (class { [k] = class {}; })()[k], "name")`, `"{\"value\":\"\",\"writable\":false,\"enumerable\":false,\"configurable\":true}"`},
		{"class static computed symbol field, function", `() => desc((class { static [sym] = function () {}; })[sym], "name")`, `"{\"writable\":false,\"enumerable\":false,\"configurable\":true}"`},
		{"class constant computed field, class", `() => desc(new (class { ["x"] = class {}; })().x, "name")`, `"{\"value\":\"\",\"writable\":false,\"enumerable\":false,\"configurable\":true}"`},
		{"class constant computed field, function", `() => desc(new (class { ["x"] = function () {}; })().x, "name")`, `"{\"writable\":false,\"enumerable\":false,\"configurable\":true}"`},
		{"class constant computed field, arrow", `() => desc(new (class { ["x"] = () => {}; })().x, "name")`, `"{\"writable\":false,\"enumerable\":false,\"configurable\":true}"`},
		{"class static constant computed field, static block reads name", `() => { let s; (class { static ["x"] = class { static { s = this.name; } }; }); return JSON.stringify(s); }`, `"\"\""`},
		{"class string key field, class", `() => desc(new (class { "s" = class {}; })().s, "name")`, `"{\"value\":\"s\",\"writable\":false,\"enumerable\":false,\"configurable\":true}"`},
		{"class numeric key field, function", `() => desc(new (class { 1 = function () {}; })()[1], "name")`, `"{\"value\":\"1\",\"writable\":false,\"enumerable\":false,\"configurable\":true}"`},
		{"without new: named", `() => msg(() => { class A {} A(); })`, `"TypeError: Class constructor A cannot be invoked without 'new'"`},
		{"without new: derived named", `() => msg(() => { class A {} class B extends A {} B(); })`, `"TypeError: Class constructor B cannot be invoked without 'new'"`},
		{"without new: anonymous", `() => msg(() => (0, class {})())`, `"TypeError: Class constructors cannot be invoked without 'new'"`},
		{"without new: anonymous derived", `() => msg(() => (0, class extends Object {})())`, `"TypeError: Class constructors cannot be invoked without 'new'"`},
		{"without new: variable", `() => msg(() => { const v = class {}; v(); })`, `"TypeError: Class constructor v cannot be invoked without 'new'"`},
		{"without new: variable derived", `() => msg(() => { const vd = class extends Object {}; vd(); })`, `"TypeError: Class constructor vd cannot be invoked without 'new'"`},
		{"without new: object literal key", `() => msg(() => ({p: class {}}).p())`, `"TypeError: Class constructor p cannot be invoked without 'new'"`},
		{"without new: object literal key derived", `() => msg(() => ({q: class extends Object {}}).q())`, `"TypeError: Class constructor q cannot be invoked without 'new'"`},
		{"without new: object literal computed key", `() => msg(() => ({[k]: class {}})[k]())`, `"TypeError: Class constructors cannot be invoked without 'new'"`},
		{"without new: object literal constant computed key", `() => msg(() => ({["x"]: class {}})["x"]())`, `"TypeError: Class constructors cannot be invoked without 'new'"`},
		{"without new: object literal constant computed number key", `() => msg(() => ({[1]: class {}})[1]())`, `"TypeError: Class constructors cannot be invoked without 'new'"`},
		{"without new: object literal numeric key", `() => msg(() => ({1: class {}})[1]())`, `"TypeError: Class constructor 1 cannot be invoked without 'new'"`},
		{"without new: anonymous with static name field", `() => msg(() => (class { static name = "z"; })())`, `"TypeError: Class constructors cannot be invoked without 'new'"`},
		{"without new: named with static name field", `() => msg(() => (class N { static name = "z"; })())`, `"TypeError: Class constructor N cannot be invoked without 'new'"`},
		{"without new: class field", `() => msg(() => new (class { f = class {}; })().f())`, `"TypeError: Class constructor f cannot be invoked without 'new'"`},
		{"without new: class computed field", `() => msg(() => new (class { [k] = class {}; })()[k]())`, `"TypeError: Class constructors cannot be invoked without 'new'"`},
		{"without new: class constant computed field", `() => msg(() => new (class { ["x"] = class {}; })().x())`, `"TypeError: Class constructors cannot be invoked without 'new'"`},
		{"without new: call", `() => msg(() => { class A {} A.call({}); })`, `"TypeError: Class constructor A cannot be invoked without 'new'"`},
		{"without new: parameter default", `() => msg(() => (function (d = class {}) { return d; })()())`, `"TypeError: Class constructor d cannot be invoked without 'new'"`},
		{"without new: destructuring default", `() => msg(() => { const {d = class {}} = {}; d(); })`, `"TypeError: Class constructor d cannot be invoked without 'new'"`},
		{"without new: assignment", `() => msg(() => { let a; a = class {}; a(); })`, `"TypeError: Class constructor a cannot be invoked without 'new'"`},
	} {
		v, err := r.RunString("nameOf(" + row.fn + ")")
		if err != nil {
			t.Errorf("%s: %v", row.label, err)
			continue
		}
		if got := v.String(); got != row.want {
			t.Errorf("%s: got %s, want %s", row.label, got, row.want)
		}
	}
}

// classNameStaticsProbe builds, for an anonymous class that is the value of
// an object literal's computed key (a string, number or symbol variable, a
// constant string or number), of a class's computed field (instance or
// static) and of an identifier key, each with one of a set of members
// (static name fields, methods and accessors, static blocks that write,
// delete or define name or freeze, seal or prevent extensions of the
// class, a static field that reads name, an instance field, a heritage),
// the name's descriptor and the class's keys when its statics start, when
// they end and once the class is made.
const classNameStaticsProbe = `(function () {
	const sym = Symbol("s");
	const keyExprs = {
		"string var": 'k', "number var": 'n', "symbol": 'sym', "constant string": '"x"', "constant number": '1',
	};
	const variants = {
		"plain": '',
		"static name field": 'static name = "kept";',
		"static name field undefined": 'static name;',
		"static name method": 'static name() {}',
		"static name getter": 'static get name() { return "got"; }',
		"static name setter": 'static set name(v) {}',
		"static computed name method": 'static ["name"]() {}',
		"block writes name": 'static { this.name = "changed"; }',
		"block deletes name": 'static { delete this.name; }',
		"block defines non-configurable": 'static { Object.defineProperty(this, "name", {value: "fixed", configurable: false}); }',
		"block defines accessor": 'static { Object.defineProperty(this, "name", {get() { return "acc"; }, configurable: true}); }',
		"block freezes": 'static { Object.freeze(this); }',
		"block seals": 'static { Object.seal(this); }',
		"block prevents extensions": 'static { Object.preventExtensions(this); }',
		"block deletes, prevents extensions": 'static { delete this.name; Object.preventExtensions(this); }',
		"static field reads name": 'static seen = typeof this.name === "symbol" ? "sym" : this.name;',
		"instance field": 'f = 1;',
		"derived": 'extends Object',
	};
	const show = (o, key) => {
		let d;
		try { d = Object.getOwnPropertyDescriptor(o, "name"); } catch (e) { return "gopd throws " + e.constructor.name; }
		let s;
		if (d === undefined) s = "absent";
		else if ("get" in d || "set" in d) s = "accessor/" + typeof d.get + "/" + typeof d.set + "/" + d.enumerable + "/" + d.configurable;
		else s = (typeof d.value === "symbol" ? "symbol" + (d.value === key ? "(key)" : "") : JSON.stringify(d.value)) + "/" + d.writable + "/" + d.enumerable + "/" + d.configurable;
		return s + " keys=" + Reflect.ownKeys(o).map(String).filter(x => x !== "seen").join(",");
	};
	const out = [];
	for (const [kl, ke] of Object.entries(keyExprs)) {
		for (const [vl, vs] of Object.entries(variants)) {
			const ext = vl === "derived" ? "extends Object " : "";
			const body = vl === "derived" ? "" : vs;
			const src = ` + "`" + `(function (k, n, sym, show, out) {
				const key = ${ke};
				let start, end;
				const v = ({[${ke}]: class ${ext}{ static { start = show(this, key); } ${body} static { end = show(this, key); } }})[${ke}];
				return "start=" + start + " | end=" + end + " | final=" + show(v, key) + (v.seen !== undefined ? " | seen=" + String(v.seen) : "");
			})` + "`" + `;
			let r;
			try { r = (0, eval)(src)("x", 1, sym, show); } catch (e) { r = "throws " + e.constructor.name + ": " + e.message; }
			out.push("ol " + kl + ", " + vl + ": " + r);
		}
	}
	// controls: class computed field (instance and static) and non-computed key
	for (const [vl, vs] of Object.entries(variants)) {
		if (vl === "derived") continue;
		const srcs = {
			"class computed field": ` + "`" + `(function (k, show) { let start, end; const v = new (class { [k] = class { static { start = show(this); } ${vs} static { end = show(this); } }; })()[k]; return "start=" + start + " | end=" + end + " | final=" + show(v); })` + "`" + `,
			"class static computed field": ` + "`" + `(function (k, show) { let start, end; const v = (class { static [k] = class { static { start = show(this); } ${vs} static { end = show(this); } }; })[k]; return "start=" + start + " | end=" + end + " | final=" + show(v); })` + "`" + `,
			"ol identifier key": ` + "`" + `(function (k, show) { let start, end; const v = ({x: class { static { start = show(this); } ${vs} static { end = show(this); } }}).x; return "start=" + start + " | end=" + end + " | final=" + show(v); })` + "`" + `,
		};
		for (const [pl, src] of Object.entries(srcs)) {
			let r;
			try { r = (0, eval)(src)("x", show); } catch (e) { r = "throws " + e.constructor.name + ": " + e.message; }
			out.push(pl + ", " + vl + ": " + r);
		}
	}
	// the review's three findings
	const f = [
		["computed write", () => { let k = "x"; const C = ({[k]: class { static { this.name = "changed"; this.seen = this.name; } }}).x; return C.seen; }],
		["computed constant getter", () => { let seen; ({["x"]: class { static get name() { throw Error("getter called"); } static { seen = this.name; } }}); return seen; }],
		["computed symbol read", () => { let s = Symbol("s"), seen; ({[s]: class { static { seen = typeof this.name + "/" + (this.name === s); } }}); return seen; }],
	];
	for (const [l, fn] of f) { let r; try { r = String(fn()); } catch (e) { r = e.name + ": " + e.message; } out.push("finding " + l + ": " + r); }
	return out.join("\n");
})()
`

// TestComputedKeyClassNameDuringStatics: classNameStaticsProbe gives
// Node.js 18.20.3's result (run in a fresh vm context in the rle-api
// image) on every line but those in classNameStaticsIntegrity, where goja
// keeps the class's integrity: a non-configurable name stays as it is, a
// frozen or sealed class keeps its name, and a class that deleted its name
// and was made non-extensible gets none, with no error. In Node.js 18 the
// object literal's computed key names an anonymous class once made over
// each of them, or throws a TypeError.
func TestComputedKeyClassNameDuringStatics(t *testing.T) {
	v, err := New().RunString(classNameStaticsProbe)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(v.String(), "\n")
	if len(got) != len(classNameStaticsWant) {
		t.Fatalf("%d lines, want %d", len(got), len(classNameStaticsWant))
	}
	integrity := 0
	for i, want := range classNameStaticsWant {
		label := want[:strings.Index(want, ": ")]
		if node18, ok := classNameStaticsIntegrity[label]; ok {
			integrity++
			if node18 == want {
				t.Errorf("%s: listed as differing from Node.js 18 but is the same", label)
			}
		}
		if got[i] != want {
			t.Errorf("line %d:\n got %s\nwant %s", i, got[i], want)
		}
	}
	if integrity != len(classNameStaticsIntegrity) {
		t.Errorf("%d integrity lines found, %d listed", integrity, len(classNameStaticsIntegrity))
	}
}

// classNameStaticsWant is classNameStaticsProbe's result: Node.js 18.20.3's
// but on the lines classNameStaticsIntegrity lists.
var classNameStaticsWant = []string{
	`ol string var, plain: start="x"/true/true/true keys=length,name,prototype | end="x"/true/true/true keys=length,name,prototype | final="x"/false/false/true keys=length,name,prototype`,
	`ol string var, static name field: start="x"/true/true/true keys=length,name,prototype | end="kept"/true/true/true keys=length,name,prototype | final="x"/false/false/true keys=length,name,prototype`,
	`ol string var, static name field undefined: start="x"/true/true/true keys=length,name,prototype | end=undefined/true/true/true keys=length,name,prototype | final="x"/false/false/true keys=length,name,prototype`,
	`ol string var, static name method: start="x"/true/true/true keys=length,name,prototype | end="x"/true/true/true keys=length,name,prototype | final="x"/false/false/true keys=length,name,prototype`,
	`ol string var, static name getter: start="x"/true/true/true keys=length,name,prototype | end="x"/true/true/true keys=length,name,prototype | final="x"/false/false/true keys=length,name,prototype`,
	`ol string var, static name setter: start="x"/true/true/true keys=length,name,prototype | end="x"/true/true/true keys=length,name,prototype | final="x"/false/false/true keys=length,name,prototype`,
	`ol string var, static computed name method: start="x"/true/true/true keys=length,name,prototype | end="x"/true/true/true keys=length,name,prototype | final="x"/false/false/true keys=length,name,prototype`,
	`ol string var, block writes name: start="x"/true/true/true keys=length,name,prototype | end="changed"/true/true/true keys=length,name,prototype | final="x"/false/false/true keys=length,name,prototype`,
	`ol string var, block deletes name: start="x"/true/true/true keys=length,name,prototype | end=absent keys=length,prototype | final="x"/false/false/true keys=length,prototype,name`,
	`ol string var, block defines non-configurable: start="x"/true/true/true keys=length,name,prototype | end="fixed"/true/true/false keys=length,name,prototype | final="fixed"/true/true/false keys=length,name,prototype`,
	`ol string var, block defines accessor: start="x"/true/true/true keys=length,name,prototype | end=accessor/function/undefined/true/true keys=length,name,prototype | final="x"/false/false/true keys=length,name,prototype`,
	`ol string var, block freezes: start="x"/true/true/true keys=length,name,prototype | end="x"/false/true/false keys=length,name,prototype | final="x"/false/true/false keys=length,name,prototype`,
	`ol string var, block seals: start="x"/true/true/true keys=length,name,prototype | end="x"/true/true/false keys=length,name,prototype | final="x"/true/true/false keys=length,name,prototype`,
	`ol string var, block prevents extensions: start="x"/true/true/true keys=length,name,prototype | end="x"/true/true/true keys=length,name,prototype | final="x"/false/false/true keys=length,name,prototype`,
	`ol string var, block deletes, prevents extensions: start="x"/true/true/true keys=length,name,prototype | end=absent keys=length,prototype | final=absent keys=length,prototype`,
	`ol string var, static field reads name: start="x"/true/true/true keys=length,name,prototype | end="x"/true/true/true keys=length,name,prototype | final="x"/false/false/true keys=length,name,prototype | seen=x`,
	`ol string var, instance field: start="x"/true/true/true keys=length,name,prototype | end="x"/true/true/true keys=length,name,prototype | final="x"/false/false/true keys=length,name,prototype`,
	`ol string var, derived: start="x"/true/true/true keys=length,name,prototype | end="x"/true/true/true keys=length,name,prototype | final="x"/false/false/true keys=length,name,prototype`,
	`ol number var, plain: start="1"/true/true/true keys=length,name,prototype | end="1"/true/true/true keys=length,name,prototype | final="1"/false/false/true keys=length,name,prototype`,
	`ol number var, static name field: start="1"/true/true/true keys=length,name,prototype | end="kept"/true/true/true keys=length,name,prototype | final="1"/false/false/true keys=length,name,prototype`,
	`ol number var, static name field undefined: start="1"/true/true/true keys=length,name,prototype | end=undefined/true/true/true keys=length,name,prototype | final="1"/false/false/true keys=length,name,prototype`,
	`ol number var, static name method: start="1"/true/true/true keys=length,name,prototype | end="1"/true/true/true keys=length,name,prototype | final="1"/false/false/true keys=length,name,prototype`,
	`ol number var, static name getter: start="1"/true/true/true keys=length,name,prototype | end="1"/true/true/true keys=length,name,prototype | final="1"/false/false/true keys=length,name,prototype`,
	`ol number var, static name setter: start="1"/true/true/true keys=length,name,prototype | end="1"/true/true/true keys=length,name,prototype | final="1"/false/false/true keys=length,name,prototype`,
	`ol number var, static computed name method: start="1"/true/true/true keys=length,name,prototype | end="1"/true/true/true keys=length,name,prototype | final="1"/false/false/true keys=length,name,prototype`,
	`ol number var, block writes name: start="1"/true/true/true keys=length,name,prototype | end="changed"/true/true/true keys=length,name,prototype | final="1"/false/false/true keys=length,name,prototype`,
	`ol number var, block deletes name: start="1"/true/true/true keys=length,name,prototype | end=absent keys=length,prototype | final="1"/false/false/true keys=length,prototype,name`,
	`ol number var, block defines non-configurable: start="1"/true/true/true keys=length,name,prototype | end="fixed"/true/true/false keys=length,name,prototype | final="fixed"/true/true/false keys=length,name,prototype`,
	`ol number var, block defines accessor: start="1"/true/true/true keys=length,name,prototype | end=accessor/function/undefined/true/true keys=length,name,prototype | final="1"/false/false/true keys=length,name,prototype`,
	`ol number var, block freezes: start="1"/true/true/true keys=length,name,prototype | end="1"/false/true/false keys=length,name,prototype | final="1"/false/true/false keys=length,name,prototype`,
	`ol number var, block seals: start="1"/true/true/true keys=length,name,prototype | end="1"/true/true/false keys=length,name,prototype | final="1"/true/true/false keys=length,name,prototype`,
	`ol number var, block prevents extensions: start="1"/true/true/true keys=length,name,prototype | end="1"/true/true/true keys=length,name,prototype | final="1"/false/false/true keys=length,name,prototype`,
	`ol number var, block deletes, prevents extensions: start="1"/true/true/true keys=length,name,prototype | end=absent keys=length,prototype | final=absent keys=length,prototype`,
	`ol number var, static field reads name: start="1"/true/true/true keys=length,name,prototype | end="1"/true/true/true keys=length,name,prototype | final="1"/false/false/true keys=length,name,prototype | seen=1`,
	`ol number var, instance field: start="1"/true/true/true keys=length,name,prototype | end="1"/true/true/true keys=length,name,prototype | final="1"/false/false/true keys=length,name,prototype`,
	`ol number var, derived: start="1"/true/true/true keys=length,name,prototype | end="1"/true/true/true keys=length,name,prototype | final="1"/false/false/true keys=length,name,prototype`,
	`ol symbol, plain: start=symbol(key)/true/true/true keys=length,name,prototype | end=symbol(key)/true/true/true keys=length,name,prototype | final="[s]"/false/false/true keys=length,name,prototype`,
	`ol symbol, static name field: start=symbol(key)/true/true/true keys=length,name,prototype | end="kept"/true/true/true keys=length,name,prototype | final="[s]"/false/false/true keys=length,name,prototype`,
	`ol symbol, static name field undefined: start=symbol(key)/true/true/true keys=length,name,prototype | end=undefined/true/true/true keys=length,name,prototype | final="[s]"/false/false/true keys=length,name,prototype`,
	`ol symbol, static name method: start=symbol(key)/true/true/true keys=length,name,prototype | end=symbol(key)/true/true/true keys=length,name,prototype | final="[s]"/false/false/true keys=length,name,prototype`,
	`ol symbol, static name getter: start=symbol(key)/true/true/true keys=length,name,prototype | end=symbol(key)/true/true/true keys=length,name,prototype | final="[s]"/false/false/true keys=length,name,prototype`,
	`ol symbol, static name setter: start=symbol(key)/true/true/true keys=length,name,prototype | end=symbol(key)/true/true/true keys=length,name,prototype | final="[s]"/false/false/true keys=length,name,prototype`,
	`ol symbol, static computed name method: start=symbol(key)/true/true/true keys=length,name,prototype | end=symbol(key)/true/true/true keys=length,name,prototype | final="[s]"/false/false/true keys=length,name,prototype`,
	`ol symbol, block writes name: start=symbol(key)/true/true/true keys=length,name,prototype | end="changed"/true/true/true keys=length,name,prototype | final="[s]"/false/false/true keys=length,name,prototype`,
	`ol symbol, block deletes name: start=symbol(key)/true/true/true keys=length,name,prototype | end=absent keys=length,prototype | final="[s]"/false/false/true keys=length,prototype,name`,
	`ol symbol, block defines non-configurable: start=symbol(key)/true/true/true keys=length,name,prototype | end="fixed"/true/true/false keys=length,name,prototype | final="fixed"/true/true/false keys=length,name,prototype`,
	`ol symbol, block defines accessor: start=symbol(key)/true/true/true keys=length,name,prototype | end=accessor/function/undefined/true/true keys=length,name,prototype | final="[s]"/false/false/true keys=length,name,prototype`,
	`ol symbol, block freezes: start=symbol(key)/true/true/true keys=length,name,prototype | end=symbol(key)/false/true/false keys=length,name,prototype | final=symbol(key)/false/true/false keys=length,name,prototype`,
	`ol symbol, block seals: start=symbol(key)/true/true/true keys=length,name,prototype | end=symbol(key)/true/true/false keys=length,name,prototype | final=symbol(key)/true/true/false keys=length,name,prototype`,
	`ol symbol, block prevents extensions: start=symbol(key)/true/true/true keys=length,name,prototype | end=symbol(key)/true/true/true keys=length,name,prototype | final="[s]"/false/false/true keys=length,name,prototype`,
	`ol symbol, block deletes, prevents extensions: start=symbol(key)/true/true/true keys=length,name,prototype | end=absent keys=length,prototype | final=absent keys=length,prototype`,
	`ol symbol, static field reads name: start=symbol(key)/true/true/true keys=length,name,prototype | end=symbol(key)/true/true/true keys=length,name,prototype | final="[s]"/false/false/true keys=length,name,prototype | seen=sym`,
	`ol symbol, instance field: start=symbol(key)/true/true/true keys=length,name,prototype | end=symbol(key)/true/true/true keys=length,name,prototype | final="[s]"/false/false/true keys=length,name,prototype`,
	`ol symbol, derived: start=symbol(key)/true/true/true keys=length,name,prototype | end=symbol(key)/true/true/true keys=length,name,prototype | final="[s]"/false/false/true keys=length,name,prototype`,
	`ol constant string, plain: start="x"/true/true/true keys=length,name,prototype | end="x"/true/true/true keys=length,name,prototype | final="x"/false/false/true keys=length,name,prototype`,
	`ol constant string, static name field: start="x"/true/true/true keys=length,name,prototype | end="kept"/true/true/true keys=length,name,prototype | final="x"/false/false/true keys=length,name,prototype`,
	`ol constant string, static name field undefined: start="x"/true/true/true keys=length,name,prototype | end=undefined/true/true/true keys=length,name,prototype | final="x"/false/false/true keys=length,name,prototype`,
	`ol constant string, static name method: start="x"/true/true/true keys=length,name,prototype | end="x"/true/true/true keys=length,name,prototype | final="x"/false/false/true keys=length,name,prototype`,
	`ol constant string, static name getter: start="x"/true/true/true keys=length,name,prototype | end="x"/true/true/true keys=length,name,prototype | final="x"/false/false/true keys=length,name,prototype`,
	`ol constant string, static name setter: start="x"/true/true/true keys=length,name,prototype | end="x"/true/true/true keys=length,name,prototype | final="x"/false/false/true keys=length,name,prototype`,
	`ol constant string, static computed name method: start="x"/true/true/true keys=length,name,prototype | end="x"/true/true/true keys=length,name,prototype | final="x"/false/false/true keys=length,name,prototype`,
	`ol constant string, block writes name: start="x"/true/true/true keys=length,name,prototype | end="changed"/true/true/true keys=length,name,prototype | final="x"/false/false/true keys=length,name,prototype`,
	`ol constant string, block deletes name: start="x"/true/true/true keys=length,name,prototype | end=absent keys=length,prototype | final="x"/false/false/true keys=length,prototype,name`,
	`ol constant string, block defines non-configurable: start="x"/true/true/true keys=length,name,prototype | end="fixed"/true/true/false keys=length,name,prototype | final="fixed"/true/true/false keys=length,name,prototype`,
	`ol constant string, block defines accessor: start="x"/true/true/true keys=length,name,prototype | end=accessor/function/undefined/true/true keys=length,name,prototype | final="x"/false/false/true keys=length,name,prototype`,
	`ol constant string, block freezes: start="x"/true/true/true keys=length,name,prototype | end="x"/false/true/false keys=length,name,prototype | final="x"/false/true/false keys=length,name,prototype`,
	`ol constant string, block seals: start="x"/true/true/true keys=length,name,prototype | end="x"/true/true/false keys=length,name,prototype | final="x"/true/true/false keys=length,name,prototype`,
	`ol constant string, block prevents extensions: start="x"/true/true/true keys=length,name,prototype | end="x"/true/true/true keys=length,name,prototype | final="x"/false/false/true keys=length,name,prototype`,
	`ol constant string, block deletes, prevents extensions: start="x"/true/true/true keys=length,name,prototype | end=absent keys=length,prototype | final=absent keys=length,prototype`,
	`ol constant string, static field reads name: start="x"/true/true/true keys=length,name,prototype | end="x"/true/true/true keys=length,name,prototype | final="x"/false/false/true keys=length,name,prototype | seen=x`,
	`ol constant string, instance field: start="x"/true/true/true keys=length,name,prototype | end="x"/true/true/true keys=length,name,prototype | final="x"/false/false/true keys=length,name,prototype`,
	`ol constant string, derived: start="x"/true/true/true keys=length,name,prototype | end="x"/true/true/true keys=length,name,prototype | final="x"/false/false/true keys=length,name,prototype`,
	`ol constant number, plain: start="1"/true/true/true keys=length,name,prototype | end="1"/true/true/true keys=length,name,prototype | final="1"/false/false/true keys=length,name,prototype`,
	`ol constant number, static name field: start="1"/true/true/true keys=length,name,prototype | end="kept"/true/true/true keys=length,name,prototype | final="1"/false/false/true keys=length,name,prototype`,
	`ol constant number, static name field undefined: start="1"/true/true/true keys=length,name,prototype | end=undefined/true/true/true keys=length,name,prototype | final="1"/false/false/true keys=length,name,prototype`,
	`ol constant number, static name method: start="1"/true/true/true keys=length,name,prototype | end="1"/true/true/true keys=length,name,prototype | final="1"/false/false/true keys=length,name,prototype`,
	`ol constant number, static name getter: start="1"/true/true/true keys=length,name,prototype | end="1"/true/true/true keys=length,name,prototype | final="1"/false/false/true keys=length,name,prototype`,
	`ol constant number, static name setter: start="1"/true/true/true keys=length,name,prototype | end="1"/true/true/true keys=length,name,prototype | final="1"/false/false/true keys=length,name,prototype`,
	`ol constant number, static computed name method: start="1"/true/true/true keys=length,name,prototype | end="1"/true/true/true keys=length,name,prototype | final="1"/false/false/true keys=length,name,prototype`,
	`ol constant number, block writes name: start="1"/true/true/true keys=length,name,prototype | end="changed"/true/true/true keys=length,name,prototype | final="1"/false/false/true keys=length,name,prototype`,
	`ol constant number, block deletes name: start="1"/true/true/true keys=length,name,prototype | end=absent keys=length,prototype | final="1"/false/false/true keys=length,prototype,name`,
	`ol constant number, block defines non-configurable: start="1"/true/true/true keys=length,name,prototype | end="fixed"/true/true/false keys=length,name,prototype | final="fixed"/true/true/false keys=length,name,prototype`,
	`ol constant number, block defines accessor: start="1"/true/true/true keys=length,name,prototype | end=accessor/function/undefined/true/true keys=length,name,prototype | final="1"/false/false/true keys=length,name,prototype`,
	`ol constant number, block freezes: start="1"/true/true/true keys=length,name,prototype | end="1"/false/true/false keys=length,name,prototype | final="1"/false/true/false keys=length,name,prototype`,
	`ol constant number, block seals: start="1"/true/true/true keys=length,name,prototype | end="1"/true/true/false keys=length,name,prototype | final="1"/true/true/false keys=length,name,prototype`,
	`ol constant number, block prevents extensions: start="1"/true/true/true keys=length,name,prototype | end="1"/true/true/true keys=length,name,prototype | final="1"/false/false/true keys=length,name,prototype`,
	`ol constant number, block deletes, prevents extensions: start="1"/true/true/true keys=length,name,prototype | end=absent keys=length,prototype | final=absent keys=length,prototype`,
	`ol constant number, static field reads name: start="1"/true/true/true keys=length,name,prototype | end="1"/true/true/true keys=length,name,prototype | final="1"/false/false/true keys=length,name,prototype | seen=1`,
	`ol constant number, instance field: start="1"/true/true/true keys=length,name,prototype | end="1"/true/true/true keys=length,name,prototype | final="1"/false/false/true keys=length,name,prototype`,
	`ol constant number, derived: start="1"/true/true/true keys=length,name,prototype | end="1"/true/true/true keys=length,name,prototype | final="1"/false/false/true keys=length,name,prototype`,
	`class computed field, plain: start=""/false/false/true keys=length,name,prototype | end=""/false/false/true keys=length,name,prototype | final=""/false/false/true keys=length,name,prototype`,
	`class static computed field, plain: start=""/false/false/true keys=length,name,prototype | end=""/false/false/true keys=length,name,prototype | final=""/false/false/true keys=length,name,prototype`,
	`ol identifier key, plain: start="x"/false/false/true keys=length,name,prototype | end="x"/false/false/true keys=length,name,prototype | final="x"/false/false/true keys=length,name,prototype`,
	`class computed field, static name field: start=""/false/false/true keys=length,name,prototype | end="kept"/true/true/true keys=length,name,prototype | final="kept"/true/true/true keys=length,name,prototype`,
	`class static computed field, static name field: start=""/false/false/true keys=length,name,prototype | end="kept"/true/true/true keys=length,name,prototype | final="kept"/true/true/true keys=length,name,prototype`,
	`ol identifier key, static name field: start="x"/false/false/true keys=length,name,prototype | end="kept"/true/true/true keys=length,name,prototype | final="kept"/true/true/true keys=length,name,prototype`,
	`class computed field, static name field undefined: start=""/false/false/true keys=length,name,prototype | end=undefined/true/true/true keys=length,name,prototype | final=undefined/true/true/true keys=length,name,prototype`,
	`class static computed field, static name field undefined: start=""/false/false/true keys=length,name,prototype | end=undefined/true/true/true keys=length,name,prototype | final=undefined/true/true/true keys=length,name,prototype`,
	`ol identifier key, static name field undefined: start="x"/false/false/true keys=length,name,prototype | end=undefined/true/true/true keys=length,name,prototype | final=undefined/true/true/true keys=length,name,prototype`,
	`class computed field, static name method: start=undefined/true/false/true keys=length,name,prototype | end=undefined/true/false/true keys=length,name,prototype | final=undefined/true/false/true keys=length,name,prototype`,
	`class static computed field, static name method: start=undefined/true/false/true keys=length,name,prototype | end=undefined/true/false/true keys=length,name,prototype | final=undefined/true/false/true keys=length,name,prototype`,
	`ol identifier key, static name method: start=undefined/true/false/true keys=length,name,prototype | end=undefined/true/false/true keys=length,name,prototype | final=undefined/true/false/true keys=length,name,prototype`,
	`class computed field, static name getter: start=accessor/function/undefined/false/true keys=length,name,prototype | end=accessor/function/undefined/false/true keys=length,name,prototype | final=accessor/function/undefined/false/true keys=length,name,prototype`,
	`class static computed field, static name getter: start=accessor/function/undefined/false/true keys=length,name,prototype | end=accessor/function/undefined/false/true keys=length,name,prototype | final=accessor/function/undefined/false/true keys=length,name,prototype`,
	`ol identifier key, static name getter: start=accessor/function/undefined/false/true keys=length,name,prototype | end=accessor/function/undefined/false/true keys=length,name,prototype | final=accessor/function/undefined/false/true keys=length,name,prototype`,
	`class computed field, static name setter: start=accessor/undefined/function/false/true keys=length,name,prototype | end=accessor/undefined/function/false/true keys=length,name,prototype | final=accessor/undefined/function/false/true keys=length,name,prototype`,
	`class static computed field, static name setter: start=accessor/undefined/function/false/true keys=length,name,prototype | end=accessor/undefined/function/false/true keys=length,name,prototype | final=accessor/undefined/function/false/true keys=length,name,prototype`,
	`ol identifier key, static name setter: start=accessor/undefined/function/false/true keys=length,name,prototype | end=accessor/undefined/function/false/true keys=length,name,prototype | final=accessor/undefined/function/false/true keys=length,name,prototype`,
	`class computed field, static computed name method: start=undefined/true/false/true keys=length,name,prototype | end=undefined/true/false/true keys=length,name,prototype | final=undefined/true/false/true keys=length,name,prototype`,
	`class static computed field, static computed name method: start=undefined/true/false/true keys=length,name,prototype | end=undefined/true/false/true keys=length,name,prototype | final=undefined/true/false/true keys=length,name,prototype`,
	`ol identifier key, static computed name method: start=undefined/true/false/true keys=length,name,prototype | end=undefined/true/false/true keys=length,name,prototype | final=undefined/true/false/true keys=length,name,prototype`,
	`class computed field, block writes name: throws TypeError: Cannot assign to read only property 'name' of function 'class { static { start = show(this); } static { this.name = "changed"; } static { end = show(this); } }'`,
	`class static computed field, block writes name: throws TypeError: Cannot assign to read only property 'name' of function 'class { static { start = show(this); } static { this.name = "changed"; } static { end = show(this); } }'`,
	`ol identifier key, block writes name: throws TypeError: Cannot assign to read only property 'name' of function 'class { static { start = show(this); } static { this.name = "changed"; } static { end = show(this); } }'`,
	`class computed field, block deletes name: start=""/false/false/true keys=length,name,prototype | end=absent keys=length,prototype | final=absent keys=length,prototype`,
	`class static computed field, block deletes name: start=""/false/false/true keys=length,name,prototype | end=absent keys=length,prototype | final=absent keys=length,prototype`,
	`ol identifier key, block deletes name: start="x"/false/false/true keys=length,name,prototype | end=absent keys=length,prototype | final=absent keys=length,prototype`,
	`class computed field, block defines non-configurable: start=""/false/false/true keys=length,name,prototype | end="fixed"/false/false/false keys=length,name,prototype | final="fixed"/false/false/false keys=length,name,prototype`,
	`class static computed field, block defines non-configurable: start=""/false/false/true keys=length,name,prototype | end="fixed"/false/false/false keys=length,name,prototype | final="fixed"/false/false/false keys=length,name,prototype`,
	`ol identifier key, block defines non-configurable: start="x"/false/false/true keys=length,name,prototype | end="fixed"/false/false/false keys=length,name,prototype | final="fixed"/false/false/false keys=length,name,prototype`,
	`class computed field, block defines accessor: start=""/false/false/true keys=length,name,prototype | end=accessor/function/undefined/false/true keys=length,name,prototype | final=accessor/function/undefined/false/true keys=length,name,prototype`,
	`class static computed field, block defines accessor: start=""/false/false/true keys=length,name,prototype | end=accessor/function/undefined/false/true keys=length,name,prototype | final=accessor/function/undefined/false/true keys=length,name,prototype`,
	`ol identifier key, block defines accessor: start="x"/false/false/true keys=length,name,prototype | end=accessor/function/undefined/false/true keys=length,name,prototype | final=accessor/function/undefined/false/true keys=length,name,prototype`,
	`class computed field, block freezes: start=""/false/false/true keys=length,name,prototype | end=""/false/false/false keys=length,name,prototype | final=""/false/false/false keys=length,name,prototype`,
	`class static computed field, block freezes: start=""/false/false/true keys=length,name,prototype | end=""/false/false/false keys=length,name,prototype | final=""/false/false/false keys=length,name,prototype`,
	`ol identifier key, block freezes: start="x"/false/false/true keys=length,name,prototype | end="x"/false/false/false keys=length,name,prototype | final="x"/false/false/false keys=length,name,prototype`,
	`class computed field, block seals: start=""/false/false/true keys=length,name,prototype | end=""/false/false/false keys=length,name,prototype | final=""/false/false/false keys=length,name,prototype`,
	`class static computed field, block seals: start=""/false/false/true keys=length,name,prototype | end=""/false/false/false keys=length,name,prototype | final=""/false/false/false keys=length,name,prototype`,
	`ol identifier key, block seals: start="x"/false/false/true keys=length,name,prototype | end="x"/false/false/false keys=length,name,prototype | final="x"/false/false/false keys=length,name,prototype`,
	`class computed field, block prevents extensions: start=""/false/false/true keys=length,name,prototype | end=""/false/false/true keys=length,name,prototype | final=""/false/false/true keys=length,name,prototype`,
	`class static computed field, block prevents extensions: start=""/false/false/true keys=length,name,prototype | end=""/false/false/true keys=length,name,prototype | final=""/false/false/true keys=length,name,prototype`,
	`ol identifier key, block prevents extensions: start="x"/false/false/true keys=length,name,prototype | end="x"/false/false/true keys=length,name,prototype | final="x"/false/false/true keys=length,name,prototype`,
	`class computed field, block deletes, prevents extensions: start=""/false/false/true keys=length,name,prototype | end=absent keys=length,prototype | final=absent keys=length,prototype`,
	`class static computed field, block deletes, prevents extensions: start=""/false/false/true keys=length,name,prototype | end=absent keys=length,prototype | final=absent keys=length,prototype`,
	`ol identifier key, block deletes, prevents extensions: start="x"/false/false/true keys=length,name,prototype | end=absent keys=length,prototype | final=absent keys=length,prototype`,
	`class computed field, static field reads name: start=""/false/false/true keys=length,name,prototype | end=""/false/false/true keys=length,name,prototype | final=""/false/false/true keys=length,name,prototype`,
	`class static computed field, static field reads name: start=""/false/false/true keys=length,name,prototype | end=""/false/false/true keys=length,name,prototype | final=""/false/false/true keys=length,name,prototype`,
	`ol identifier key, static field reads name: start="x"/false/false/true keys=length,name,prototype | end="x"/false/false/true keys=length,name,prototype | final="x"/false/false/true keys=length,name,prototype`,
	`class computed field, instance field: start=""/false/false/true keys=length,name,prototype | end=""/false/false/true keys=length,name,prototype | final=""/false/false/true keys=length,name,prototype`,
	`class static computed field, instance field: start=""/false/false/true keys=length,name,prototype | end=""/false/false/true keys=length,name,prototype | final=""/false/false/true keys=length,name,prototype`,
	`ol identifier key, instance field: start="x"/false/false/true keys=length,name,prototype | end="x"/false/false/true keys=length,name,prototype | final="x"/false/false/true keys=length,name,prototype`,
	`finding computed write: changed`,
	`finding computed constant getter: x`,
	`finding computed symbol read: symbol/true`,
}

// classNameStaticsIntegrity lists, by label, the lines where goja keeps
// the class's integrity, with Node.js 18.20.3's line for each.
var classNameStaticsIntegrity = map[string]string{
	`ol string var, block defines non-configurable`:          `ol string var, block defines non-configurable: start="x"/true/true/true keys=length,name,prototype | end="fixed"/true/true/false keys=length,name,prototype | final="x"/false/false/true keys=length,name,prototype`,
	`ol string var, block freezes`:                           `ol string var, block freezes: start="x"/true/true/true keys=length,name,prototype | end="x"/false/true/false keys=length,name,prototype | final="x"/false/false/true keys=length,name,prototype`,
	`ol string var, block seals`:                             `ol string var, block seals: start="x"/true/true/true keys=length,name,prototype | end="x"/true/true/false keys=length,name,prototype | final="x"/false/false/true keys=length,name,prototype`,
	`ol string var, block deletes, prevents extensions`:      `ol string var, block deletes, prevents extensions: throws TypeError: Cannot add property name, object is not extensible`,
	`ol number var, block defines non-configurable`:          `ol number var, block defines non-configurable: start="1"/true/true/true keys=length,name,prototype | end="fixed"/true/true/false keys=length,name,prototype | final="1"/false/false/true keys=length,name,prototype`,
	`ol number var, block freezes`:                           `ol number var, block freezes: start="1"/true/true/true keys=length,name,prototype | end="1"/false/true/false keys=length,name,prototype | final="1"/false/false/true keys=length,name,prototype`,
	`ol number var, block seals`:                             `ol number var, block seals: start="1"/true/true/true keys=length,name,prototype | end="1"/true/true/false keys=length,name,prototype | final="1"/false/false/true keys=length,name,prototype`,
	`ol number var, block deletes, prevents extensions`:      `ol number var, block deletes, prevents extensions: throws TypeError: Cannot add property name, object is not extensible`,
	`ol symbol, block defines non-configurable`:              `ol symbol, block defines non-configurable: start=symbol(key)/true/true/true keys=length,name,prototype | end="fixed"/true/true/false keys=length,name,prototype | final="[s]"/false/false/true keys=length,name,prototype`,
	`ol symbol, block freezes`:                               `ol symbol, block freezes: start=symbol(key)/true/true/true keys=length,name,prototype | end=symbol(key)/false/true/false keys=length,name,prototype | final="[s]"/false/false/true keys=length,name,prototype`,
	`ol symbol, block seals`:                                 `ol symbol, block seals: start=symbol(key)/true/true/true keys=length,name,prototype | end=symbol(key)/true/true/false keys=length,name,prototype | final="[s]"/false/false/true keys=length,name,prototype`,
	`ol symbol, block deletes, prevents extensions`:          `ol symbol, block deletes, prevents extensions: throws TypeError: Cannot add property name, object is not extensible`,
	`ol constant string, block defines non-configurable`:     `ol constant string, block defines non-configurable: start="x"/true/true/true keys=length,name,prototype | end="fixed"/true/true/false keys=length,name,prototype | final="x"/false/false/true keys=length,name,prototype`,
	`ol constant string, block freezes`:                      `ol constant string, block freezes: start="x"/true/true/true keys=length,name,prototype | end="x"/false/true/false keys=length,name,prototype | final="x"/false/false/true keys=length,name,prototype`,
	`ol constant string, block seals`:                        `ol constant string, block seals: start="x"/true/true/true keys=length,name,prototype | end="x"/true/true/false keys=length,name,prototype | final="x"/false/false/true keys=length,name,prototype`,
	`ol constant string, block deletes, prevents extensions`: `ol constant string, block deletes, prevents extensions: throws TypeError: Cannot add property name, object is not extensible`,
	`ol constant number, block defines non-configurable`:     `ol constant number, block defines non-configurable: start="1"/true/true/true keys=length,name,prototype | end="fixed"/true/true/false keys=length,name,prototype | final="1"/false/false/true keys=length,name,prototype`,
	`ol constant number, block freezes`:                      `ol constant number, block freezes: start="1"/true/true/true keys=length,name,prototype | end="1"/false/true/false keys=length,name,prototype | final="1"/false/false/true keys=length,name,prototype`,
	`ol constant number, block seals`:                        `ol constant number, block seals: start="1"/true/true/true keys=length,name,prototype | end="1"/true/true/false keys=length,name,prototype | final="1"/false/false/true keys=length,name,prototype`,
	`ol constant number, block deletes, prevents extensions`: `ol constant number, block deletes, prevents extensions: throws TypeError: Cannot add property name, object is not extensible`,
}
