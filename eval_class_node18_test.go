package goja

import "testing"

func TestDirectEvalInClassExpressionNode18(t *testing.T) {
	// A class expression without a name has a scope without bindings, which
	// a direct eval() in its methods makes dynamic: the scopes in it counted
	// it when they reached the stashes around them by level, but it had no
	// stash, so that a write to a variable one level up went to the
	// function around it (K, 1, became 2), and one two levels up was out of
	// range (a panic in stash.getByIdx). So did a function's scope for its body, after
	// parameters with expressions, with a direct eval() in an arrow
	// function. Such a scope now has a stash. Methods, constructors (also
	// derived), static blocks, field initializers, getters, setters, arrow
	// functions in them, computed keys, the heritage, private names and
	// nested functions, recorded with Node.js 18.20.3.
	const script = `
(function () {
	var out = [];
	var cases = [
		function () { var K = 1; (function () { var v; new (class { constructor() { v = eval("2"); } })(); })(); return K; },
		function () { var K = 1; var v; new (class { constructor() { eval(""); v = 2; } })(); return [K, v]; },
		function () { var K = 1, v; new (class { m() { v = eval("K + 2"); } constructor() { this.m(); } })(); return [K, v]; },
		function () { var K = 1, v; (class { static { v = eval("K + 3"); } }); return [K, v]; },
		function () { var K = 1, v; (class { static s = (v = eval("K + 4")); }); return [K, v]; },
		function () { var K = 1, v; new (class { x = (v = eval("K + 5")); })(); return [K, v]; },
		function () { var K = 1, v; new (class { get g() { return v = eval("K + 6"); } })().g; return [K, v]; },
		function () { var K = 1, v; new (class { set s(a) { v = eval("K + a"); } })().s = 7; return [K, v]; },
		function () { var K = 1, v; new (class { constructor() { (() => { v = eval("K + 8"); })(); } })(); return [K, v]; },
		function () { var K = 1, v; new (class { m() { return () => eval("v = K + 9"); } })().m()(); return [K, v]; },
		function () { var K = 1, v; new (class { static { (() => { v = eval("K + 10"); })(); } }); return [K, v]; },
		function () { var K = 1, v; var o = new (class { [eval("'m'")]() { v = K + 11; } })(); o.m(); return [K, v]; },
		function () { var K = 1, v; class B { constructor() { this.b = K; } } new (class extends B { constructor() { super(); v = eval("this.b + 11"); } })(); return [K, v]; },
		function () { var K = 1, v; new (class extends (eval("(class { })")) { constructor() { super(); v = K + 12; } })(); return [K, v]; },
		function () { var K = 1, v; new (class { #p = 13; m() { v = eval("this.#p"); } constructor() { this.m(); } })(); return [K, v]; },
		function () { var K = 1, v; (function () { var w; (function () { new (class { constructor() { w = eval("K + 14"); v = w; } })(); })(); })(); return [K, v]; },
		function () { var K = 1, v; (function (a = 1) { (() => { v = eval("K + a + 14"); })(); })(); return [K, v]; },
		function () { var K = 1, v; var C = class N { m() { v = eval("typeof N + K"); } }; new C().m(); return [K, v]; },
		function () { var K = 1, v; new (class { m() { var K = 20; v = eval("K"); } constructor() { this.m(); } })(); return [K, v]; },
		function () { "use strict"; var K = 1, v; new (class { constructor() { v = eval("var z = 21; z + K"); } })(); return [K, v, typeof z]; },
		function () { var K = 1, v; for (let i = 0; i < 2; i++) { new (class { constructor() { v = eval("i + K"); } })(); } return [K, v]; },
		function () { var K = 1, v; { let L = 30; new (class { constructor() { v = eval("L + K"); } })(); } return [K, v]; },
	];
	for (var i = 0; i < cases.length; i++) {
		try { out.push(cases[i]()); } catch (e) { out.push(String(e)); }
	}
	return JSON.stringify(out);
})()
`
	testScript(script, newStringValue(`[1,[1,2],[1,3],[1,4],[1,5],[1,6],[1,7],[1,8],[1,9],[1,10],[1,11],[1,12],[1,12],[1,13],[1,13],[1,15],[1,16],[1,"function1"],[1,20],[1,22,"undefined"],[1,2],[1,31]]`), t)
}
