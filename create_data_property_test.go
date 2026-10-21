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
