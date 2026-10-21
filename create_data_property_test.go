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
// 26.8.1 gives them (run in a fresh vm context; for the dynamic import
// error, in Node.js's own realm; NewGoError has no Node.js counterpart and
// keeps the attributes it had).
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
// as ECMAScript makes them and Node.js 26.8.1 lists them, in
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
// function, arrow function or class value is named after its key. Each row
// is as Node.js 26.8.1 has it (run in a fresh vm context); the static
// block's assignment runs the inherited setter in both.
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
		{"class computed fields", `new (class { [k] = 1; [sym] = function () {}; })()`, `["k", sym]`, `keys=k,Symbol(sym) k=1/true/true/true Symbol(sym)=fn:[sym]/true/true/true proto=other calls=0`},
		{"class computed arrow field", `new (class { [k] = () => {}; })()`, `["k"]`, `keys=k k=fn:k/true/true/true proto=other calls=0`},
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
// 26.8.1's (and ECMAScript's) names, in its order, each an own data
// property that is true and writable, enumerable and configurable, on an
// object with no prototype; a with statement on an array leaves each of
// them, and no other array method, to the outer scope.
func TestArrayUnscopables(t *testing.T) {
	const SCRIPT = `
	const names = ["at", "copyWithin", "entries", "fill", "find", "findIndex", "findLast", "findLastIndex", "flat", "flatMap", "includes", "keys", "toReversed", "toSorted", "toSpliced", "values"];
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

// TestComputedKeyClassName: an anonymous class that is the value of a
// property with a computed key, in an object literal or a class's instance
// or static field, is named after the key once its methods and accessors
// are defined (a static name method or getter does not stay) and before its
// static fields and blocks run (a static name field, or a name a static
// block defines, stays, and a static block reads the key's name);
// anonymous functions and arrow functions are named after the key, and a
// class in a variable, assignment, field, parameter default or destructuring
// default is named after its target. Each row is as Node.js 26.8.1 has it
// (run in a fresh vm context).
func TestComputedKeyClassName(t *testing.T) {
	r := New()
	if _, err := r.RunString(`
	globalThis.k = "x";
	globalThis.sym = Symbol("s");
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
		{"instance computed field, static name method", `() => typeof new (class { [k] = class { static name() {} }; })()[k].name`, `"string"`},
		{"instance computed field, static name getter", `() => new (class { [k] = class { static get name() { return "got"; } }; })()[k].name`, `"x"`},
		{"instance computed field, static block non-configurable", `() => new (class { [k] = class { static { Object.defineProperty(this, "name", {value: "fixed", configurable: false}); } }; })()[k].name`, `"fixed"`},
		{"instance computed field, static block reads name", `() => { let seen; new (class { [k] = class { static { seen = this.name; } }; })(); return seen; }`, `"x"`},
		{"instance computed field, plain class", `() => new (class { [k] = class {}; })()[k].name`, `"x"`},
		{"instance computed symbol field, plain class", `() => new (class { [sym] = class {}; })()[sym].name`, `"[s]"`},
		{"instance computed field, function", `() => new (class { [k] = function () {}; })()[k].name`, `"x"`},
		{"instance computed field, arrow", `() => new (class { [k] = () => {}; })()[k].name`, `"x"`},
		{"static computed field, static name field", `() => (class { static [k] = class { static name = "kept"; }; })[k].name`, `"kept"`},
		{"static computed field, plain class", `() => (class { static [k] = class {}; })[k].name`, `"x"`},
		{"static computed field, static block reads name", `() => { let seen; (class { static [k] = class { static { seen = this.name; } }; }); return seen; }`, `"x"`},
		{"static computed field, derived plain class", `() => (class { static [k] = class extends Object {}; })[k].name`, `"x"`},
		{"object literal computed, static name field", `() => ({[k]: class { static name = "kept"; }})[k].name`, `"kept"`},
		{"object literal computed, static name getter", `() => ({[k]: class { static get name() { return "got"; } }})[k].name`, `"x"`},
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
