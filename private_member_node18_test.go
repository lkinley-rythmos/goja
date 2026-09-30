package goja

import "testing"

// These scripts' results were recorded with Node.js 18.20.3.

func TestPrivateMemberMessagesNode18(t *testing.T) {
	// A private member read or written before it is initialized is reported
	// as V8 reports it, as one of an object whose class did not declare it
	// ("Private member #b is accessed before it is initialized" before), and
	// a write is "to an object" ("from" before).
	const script = `
(function () {
	var srcs = [
		'class C { #a = this.#b; #b = 1 } new C()',
		'class C { #a = this.#b(); #b() {} } new C(); "ok"',
		'class C { a = this.#b; #b = 1 } new C()',
		'class C { #a = (this.#b = 2); #b = 1 } new C()',
		'class C { static #a = C.#b; static #b = 1 }',
		'class C { static a = C.#b; static #b = 1 }',
		'class C { #a = 1; static m(o) { return o.#a } } C.m({})',
		'class C { #a = 1; static m(o) { o.#a = 2 } } C.m({})',
		'class C { #a() {} static m(o) { o.#a = 1 } } C.m(new C())',
		'class C { set #a(v) {} static m(o) { return o.#a } } C.m(new C())',
		'class C { static #a = 1; static m(o) { return o.#a } } C.m({})',
		'class C { #a = 1; m() { return this.#a } } C.prototype.m.call({})',
		'class C { #a = this.#c; get #c() { return 1 } } new C(); "ok"',
		'class C { #a = this.#b; static #b = 1 } new C()',
	];
	var out = [];
	for (var i = 0; i < srcs.length; i++) {
		try { out.push([srcs[i], String((0, eval)(srcs[i]))]); } catch (e) { out.push([srcs[i], e.name + ": " + e.message]); }
	}
	return JSON.stringify(out);
})()
`
	testScript(script, newStringValue(`[["class C { #a = this.#b; #b = 1 } new C()","TypeError: Cannot read private member #b from an object whose class did not declare it"],["class C { #a = this.#b(); #b() {} } new C(); \"ok\"","ok"],["class C { a = this.#b; #b = 1 } new C()","TypeError: Cannot read private member #b from an object whose class did not declare it"],["class C { #a = (this.#b = 2); #b = 1 } new C()","TypeError: Cannot write private member #b to an object whose class did not declare it"],["class C { static #a = C.#b; static #b = 1 }","TypeError: Cannot read private member #b from an object whose class did not declare it"],["class C { static a = C.#b; static #b = 1 }","TypeError: Cannot read private member #b from an object whose class did not declare it"],["class C { #a = 1; static m(o) { return o.#a } } C.m({})","TypeError: Cannot read private member #a from an object whose class did not declare it"],["class C { #a = 1; static m(o) { o.#a = 2 } } C.m({})","TypeError: Cannot write private member #a to an object whose class did not declare it"],["class C { #a() {} static m(o) { o.#a = 1 } } C.m(new C())","TypeError: Private method '#a' is not writable"],["class C { set #a(v) {} static m(o) { return o.#a } } C.m(new C())","TypeError: '#a' was defined without a getter"],["class C { static #a = 1; static m(o) { return o.#a } } C.m({})","TypeError: Cannot read private member #a from an object whose class did not declare it"],["class C { #a = 1; m() { return this.#a } } C.prototype.m.call({})","TypeError: Cannot read private member #a from an object whose class did not declare it"],["class C { #a = this.#c; get #c() { return 1 } } new C(); \"ok\"","ok"],["class C { #a = this.#b; static #b = 1 } new C()","TypeError: Cannot read private member #b from an object whose class did not declare it"]]`), t)
}
