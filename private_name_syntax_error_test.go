package goja

import "testing"

// Private names that no class declares, or that are where no expression may
// be one, are SyntaxErrors with V8's messages, reported where V8 reports them:
// a private name outside any class as it is read, one a class does not
// declare when the class ends. Node.js 18.20.3 recorded the messages.
func TestPrivateNameSyntaxErrorNode18(t *testing.T) {
	r := New()
	v, err := r.RunString(`({
		top(src) { try { (0, eval)(src); return "ok"; } catch (e) { return e.name + ": " + e.message; } },
		k: new (class K { #x; static #s;
			m(src) { try { eval(src); return "ok"; } catch (e) { return e.name + ": " + e.message; } }
			f(src) { try { new Function(src); return "ok"; } catch (e) { return e.name + ": " + e.message; } } })(),
	})`)
	if err != nil {
		t.Fatal(err)
	}
	o := v.ToObject(r)
	k := o.Get("k").ToObject(r)
	check := func(how string, this Value, fnVal Value, cases []struct{ src, want string }) {
		fn, _ := AssertFunction(fnVal)
		for _, tc := range cases {
			res, err := fn(this, r.ToValue(tc.src))
			if err != nil {
				t.Fatal(err)
			}
			if got := res.String(); got != tc.want {
				t.Errorf("%s %q: %q, want %q", how, tc.src, got, tc.want)
			}
		}
	}
	check("indirect eval", o, o.Get("top"), []struct{ src, want string }{
		{"var a = #x", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"#x", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"#x in o", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"class C { m() { return this.#x } }", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"class C { m() { return #x in this } }", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"class C { #y; m() { return this.#x } }", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"class C { m() { class D { #x } return this.#x } }", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"class C { #x; m() { class D { m() { return this.#x } } } }", "ok"},
		{"this.#x", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"o?.#x", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"class C { m() { return o?.#x } }", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"class C { m() { delete this.#x } }", "SyntaxError: Private fields can not be deleted"},
		{"class C { #x; m() { delete this.#x } }", "SyntaxError: Private fields can not be deleted"},
		{"class C { #x; #x }", "SyntaxError: Identifier '#x' has already been declared"},
		{"class C { m() { this.#x = 1 } }", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"class C { static m() { return C.#x } }", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"function f() { return this.#x }", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"class C { #constructor }", "SyntaxError: Classes may not have a field named 'constructor'"},
		{"class C { m() { return #x } }", "SyntaxError: Unexpected identifier"},
		{"class C { #x; m() { return #x } }", "SyntaxError: Unexpected identifier"},
		{"class C { #x; m() { return #x + 1 } }", "SyntaxError: Unexpected identifier"},
		{"#x + 1", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"a.#x", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"class C { m() { a.#x.#y } }", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"class C { [this.#x] }", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"class C extends (class { m() { this.#x } }) { #x }", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"`${this.#x}`", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"class C { m() { `${this.#x}` } }", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"class C { #x; static { this.#y } }", "SyntaxError: Private field '#y' must be declared in an enclosing class"},
		{"({ m() { this.#x } })", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"class C { m() { super.#x } }", "SyntaxError: Unexpected private field"},
		{"class C { #x; m() { super.#x } }", "SyntaxError: Unexpected private field"},
		{"class C { m() { for (#x in o); } }", "SyntaxError: Unexpected identifier"},
		{"class C { #x; m() { #x in #x in this } }", "SyntaxError: Unexpected identifier"},
		{"!#x", "SyntaxError: Unexpected identifier"},
		{"a + #x", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"-#x", "SyntaxError: Unexpected identifier"},
		{"typeof #x", "SyntaxError: Unexpected identifier"},
		{"#x++", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"++#x", "SyntaxError: Unexpected identifier"},
		{"new #x", "SyntaxError: Unexpected identifier"},
		{"#x.y", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"(#x)", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"[#x]", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"f(#x)", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"({#x: 1})", "SyntaxError: Unexpected identifier"},
		{"({#x})", "SyntaxError: Unexpected identifier"},
		{"let #x", "SyntaxError: Unexpected identifier"},
		{"function #x(){}", "SyntaxError: Unexpected identifier"},
		{"#x => 1", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"a ** #x", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"#x ** 2", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"a ? #x : b", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"a || #x", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"class C { #x; m() { return !#x } }", "SyntaxError: Unexpected identifier"},
		{"class C { #x; m() { return a + #x } }", "SyntaxError: Unexpected identifier"},
		{"class C { #x; m() { return #x.y } }", "SyntaxError: Unexpected identifier"},
		{"class C { #x; m() { return ({#x: 1}) } }", "SyntaxError: Unexpected identifier"},
		{"class C { #x; m() { return (#x) } }", "SyntaxError: Unexpected identifier"},
		{"class C { #x; m(o) { return #x in o in p } }", "ok"},
		{"class C { #x; m(o) { return #x in o < 1 } }", "ok"},
		{"class C { #x; m(o) { return 1 < #x in o } }", "SyntaxError: Unexpected identifier"},
		{"class C { #x; m(o) { return a + #x in o } }", "SyntaxError: Unexpected identifier"},
		{"class C { #x; m(o) { return #x in o ? 1 : 2 } }", "ok"},
		{"class C { #x; m(o) { return !(#x in o) } }", "ok"},
		{"class C { #x; m(o) { for (var i = #x in o; ;); } }", "SyntaxError: Unexpected identifier"},
		{"class C { #x; m(o) { for (#x in o;;); } }", "SyntaxError: Unexpected identifier"},
		{"class C { m() { this.#x } } )", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"this.#x; )", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"#x in o; )", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"class C extends (#x in o, Object) { #x }", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"class D { #x; m() { class C extends (this.#x, Object) {} } }", "ok"},
		{"class C { m() { this.#a; this.#b } #b }", "SyntaxError: Private field '#a' must be declared in an enclosing class"},
		{"class C { m() { this.#b; this.#a } #b }", "SyntaxError: Private field '#a' must be declared in an enclosing class"},
		{"class C { [#x in o] = 1; #x }", "ReferenceError: o is not defined"},
		{"class C { #x; static { #x in this } }", "ok"},
		{"class C { m() { class D { n() { this.#y } } } #y }", "ok"},
		{"class C { m() { class D { #z; n() { this.#y } } } }", "SyntaxError: Private field '#y' must be declared in an enclosing class"},
		{"class C { static #x; m() { return C.#x } }", "ok"},
		{"class C { #x() {} get #y() {} set #y(v) {} m() { this.#x; this.#y } }", "ok"},
		{"class C { m() { this.#x.#y } #x }", "SyntaxError: Private field '#y' must be declared in an enclosing class"},
		{"class C { #x; m() { a?.b.#x } }", "ok"},
		{"class C { m() { a?.b.#x } }", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"class C { m() { new.target.#x } #x }", "ok"},
		{"`${#x}`", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"class C { #x; m() { return `${#x}` } }", "SyntaxError: Unexpected identifier"},
		{"class C { #x; m() { return #x in #x } }", "SyntaxError: Unexpected identifier"},
		{"x = #x in {}", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"class C { #x; m() { for (const a of #x in o); } }", "ok"},
		{"class C { #x; m() { delete this?.#x } }", "SyntaxError: Private fields can not be deleted"},
		{"class C { #x; m() { delete (this.#x) } }", "SyntaxError: Private fields can not be deleted"},
		{"class C { #x; m() { delete this.#x.y } }", "ok"},
		{"class C { #x; m() { delete this?.a.#x } }", "SyntaxError: Private fields can not be deleted"},
		{"class C { #x; m() { delete ((this.#x)) } }", "SyntaxError: Private fields can not be deleted"},
		{"class C { m() { delete this.#x } ) }", "SyntaxError: Private fields can not be deleted"},
		{"class C { m() { delete this.#x; this.#y } }", "SyntaxError: Private fields can not be deleted"},
		{"class C { #x; m() { delete this.#x; ) } }", "SyntaxError: Private fields can not be deleted"},
		{"delete this.#x", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"class C { #x; m() { delete this?.#x.y } }", "ok"},
		{"class C { #x; m() { delete (0, this.#x) } }", "ok"},
		{"class C { #x; #x; m() { this.#y } }", "SyntaxError: Identifier '#x' has already been declared"},
		{"class C { #x; #x } )", "SyntaxError: Identifier '#x' has already been declared"},
		{"class C { get #x() {} set #x(v) {} }", "ok"},
		{"class C { set #x(v) {} get #x() {} }", "ok"},
		{"class C { get #x() {} get #x() {} }", "SyntaxError: Identifier '#x' has already been declared"},
		{"class C { static get #x() {} set #x(v) {} }", "SyntaxError: Identifier '#x' has already been declared"},
		{"class C { #x() {} get #x() {} }", "SyntaxError: Identifier '#x' has already been declared"},
		{"class C { get #x() {} #x }", "SyntaxError: Identifier '#x' has already been declared"},
		{"class C { static #x; #x }", "SyntaxError: Identifier '#x' has already been declared"},
		{"class C { #x = 1; m() { )} #x }", "SyntaxError: Unexpected token ')'"},
		{"class C { #x; #x() { ) } }", "SyntaxError: Unexpected token ')'"},
		{"class C { get #x() {} set #x(v) {} set #x(v) {} }", "SyntaxError: Identifier '#x' has already been declared"},
		{"class C { static #x() {} static #x() {} }", "SyntaxError: Identifier '#x' has already been declared"},
		{"class C { #x; m() { this.#y } #x }", "SyntaxError: Identifier '#x' has already been declared"},
		{"class C { m() { this.#y } #x; #x }", "SyntaxError: Identifier '#x' has already been declared"},
		{"class C { static async *#x() {} #x }", "SyntaxError: Identifier '#x' has already been declared"},
		{"class C { #constructor() {} }", "SyntaxError: Class constructor may not be a private method"},
		{"class C { #x; static { this.#x } }", "TypeError: Cannot read private member #x from an object whose class did not declare it"},
		{"class C { m() { class D { #x; #x } this.#q } }", "SyntaxError: Identifier '#x' has already been declared"},
		{"class C { m() { this.#q; class D { #x; #x } } }", "SyntaxError: Identifier '#x' has already been declared"},
		{"class C { #a; m() { return #a in this && #b in this } }", "SyntaxError: Private field '#b' must be declared in an enclosing class"},
		{"class C { #x = this.#x; }", "ok"},
		{"class C { #x = #x in this; }", "ok"},
		{"class C { static #constructor() {} }", "SyntaxError: Class constructor may not be a private method"},
		{"class C { get #constructor() {} }", "SyntaxError: Class constructor may not be a private method"},
		{"class C { async #constructor() {} }", "SyntaxError: Class constructor may not be a private method"},
		{"class C { *#constructor() {} }", "SyntaxError: Class constructor may not be a private method"},
		{"class C { static #constructor }", "SyntaxError: Classes may not have a field named 'constructor'"},
		{"class C { static get #constructor() {} }", "SyntaxError: Class constructor may not be a private method"},
		{"class C { m() { #y in this } } )", "SyntaxError: Private field '#y' must be declared in an enclosing class"},
		{"class C { m() { this?.#y } } )", "SyntaxError: Private field '#y' must be declared in an enclosing class"},
		{"class C { m() { (a = this.#y) => a } } )", "SyntaxError: Private field '#y' must be declared in an enclosing class"},
		{"class C { *g() { yield this.#y } } )", "SyntaxError: Private field '#y' must be declared in an enclosing class"},
		{"class C { #y; m() { (a = this.#y) => a } } )", "SyntaxError: Unexpected token ')'"},
		{"var f = (a = class { m() { this.#y } }) => 1; )", "SyntaxError: Private field '#y' must be declared in an enclosing class"},
		{"class C { static get #x() {} set #x(v) {} m() { this.#y } }", "SyntaxError: Identifier '#x' has already been declared"},
		{"class C { get #x() {} static set #x(v) {} m() { this.#y } }", "SyntaxError: Identifier '#x' has already been declared"},
		{"class C { m() { class D { n() { this.#y } } } } )", "SyntaxError: Private field '#y' must be declared in an enclosing class"},
		{"class C { m() { class D { #y; n() { this.#y } } } } )", "SyntaxError: Unexpected token ')'"},
		{"class C { #y; m() { class D { n() { this.#y } } } } )", "SyntaxError: Unexpected token ')'"},
		{"class C extends (#x in o, Object) { #x } )", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
	})
	check("direct eval in a class", k, k.Get("m"), []struct{ src, want string }{
		{"#x", "SyntaxError: Unexpected identifier"},
		{"this.#x", "ok"},
		{"this.#y", "SyntaxError: Private field '#y' must be declared in an enclosing class"},
		{"#x in this", "ok"},
		{"#y in this", "SyntaxError: Private field '#y' must be declared in an enclosing class"},
		{"!#x", "SyntaxError: Unexpected identifier"},
		{"a + #x", "SyntaxError: Unexpected identifier"},
		{"K.#s", "ok"},
		{"this.#x; )", "SyntaxError: Unexpected token ')'"},
		{"this.#y; )", "SyntaxError: Unexpected token ')'"},
		{"#y", "SyntaxError: Unexpected identifier"},
		{"class D { m() { this.#x } }", "ok"},
		{"class D { m() { this.#q } }", "SyntaxError: Private field '#q' must be declared in an enclosing class"},
		{"(() => this.#x)()", "ok"},
	})
	check("new Function in a class", k, k.Get("f"), []struct{ src, want string }{
		{"#x", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"this.#x", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"this.#y", "SyntaxError: Private field '#y' must be declared in an enclosing class"},
		{"#x in this", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"#y in this", "SyntaxError: Private field '#y' must be declared in an enclosing class"},
		{"!#x", "SyntaxError: Unexpected identifier"},
		{"a + #x", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"K.#s", "SyntaxError: Private field '#s' must be declared in an enclosing class"},
		{"this.#x; )", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"this.#y; )", "SyntaxError: Private field '#y' must be declared in an enclosing class"},
		{"#y", "SyntaxError: Private field '#y' must be declared in an enclosing class"},
		{"class D { m() { this.#x } }", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
		{"class D { m() { this.#q } }", "SyntaxError: Private field '#q' must be declared in an enclosing class"},
		{"(() => this.#x)()", "SyntaxError: Private field '#x' must be declared in an enclosing class"},
	})
}

func TestPrivateInChainNode18(t *testing.T) {
	// "#x in o" is a relational expression's first operand, so another
	// relational operator may follow it (Node.js 18.20.3's results).
	const script = `
	class C { #x; static t(o) { return [#x in o in { true: 1 }, #x in o in { false: 1 }, #x in o < 1, true == #x in o, #x in o === false, !(#x in o)]; } }
	JSON.stringify([C.t(new C()), C.t({})]);
	`
	testScript(script, asciiString("[[true,false,false,true,false,false],[false,true,true,false,true,true]]"), t)
}
