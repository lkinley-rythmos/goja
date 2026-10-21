package goja

import (
	"errors"
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
		{"object literal computed key, static block fixes name", `() => { let inBlock; const c = ({[k]: class { static { Object.defineProperty(this, "name", {value: "fixed", configurable: false}); inBlock = this.name; } }})[k]; return inBlock + " " + desc(c, "name"); }`, `"fixed {\"value\":\"fixed\",\"writable\":false,\"enumerable\":false,\"configurable\":false}"`},
		{"object literal computed key, static block deletes name", `() => { const c = ({[k]: class { static { delete this.name; } }})[k]; return desc(c, "name") + " " + Reflect.ownKeys(c).join(","); }`, `"{\"value\":\"x\",\"writable\":false,\"enumerable\":false,\"configurable\":true} length,prototype,name"`},
		// Not Node.js 18's, which throws a TypeError adding the name
		// back: a class made non-extensible gets no name it lacks.
		{"object literal computed key, static block deletes name and prevents extensions", `() => { const c = ({[k]: class { static { delete this.name; Object.preventExtensions(this); } }})[k]; return desc(c, "name") + " extensible=" + Object.isExtensible(c); }`, `"absent extensible=false"`},
		// Not Node.js 18's (configurable true): the frozen class stays frozen.
		{"object literal computed key, static block freezes", `() => { const c = ({[k]: class { static { Object.freeze(this); } }})[k]; return desc(c, "name") + " frozen=" + Object.isFrozen(c); }`, `"{\"value\":\"x\",\"writable\":false,\"enumerable\":false,\"configurable\":false} frozen=true"`},
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
