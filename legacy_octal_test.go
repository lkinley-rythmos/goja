package goja

import (
	"strings"
	"testing"
)

// V8's scanner keeps only the last legacy octal escape or literal (or \8,
// \9, or decimal with a leading zero) it read, and reports it when a strict
// function or script ends if it is in it: the last one in the function that
// ends first. Node.js 18.20.3 recorded the messages.
func TestLegacyOctalLastNode18(t *testing.T) {
	r := New()
	fn, err := r.RunString(`(function (src) { try { return "ok " + String((0, eval)(src)); } catch (e) { return e.name + ": " + e.message; } })`)
	if err != nil {
		t.Fatal(err)
	}
	ev, _ := AssertFunction(fn)
	for _, tc := range []struct{ src, want string }{
		{"'use strict'; '\\01\\9'", "SyntaxError: \\8 and \\9 are not allowed in strict mode."},
		{"'use strict'; '\\9\\01'", "SyntaxError: Octal escape sequences are not allowed in strict mode."},
		{"'use strict'; '\\8\\9'", "SyntaxError: \\8 and \\9 are not allowed in strict mode."},
		{"'use strict'; '\\01\\02'", "SyntaxError: Octal escape sequences are not allowed in strict mode."},
		{"'use strict'; '\\01' + '\\9'", "SyntaxError: \\8 and \\9 are not allowed in strict mode."},
		{"'use strict'; '\\9' + '\\01'", "SyntaxError: Octal escape sequences are not allowed in strict mode."},
		{"'use strict'; ({'\\01\\8': 1})", "SyntaxError: \\8 and \\9 are not allowed in strict mode."},
		{"'use strict'; ({'\\8': 1, '\\01': 2})", "SyntaxError: Octal escape sequences are not allowed in strict mode."},
		{"function f() { '\\01\\9'; 'use strict' }", "SyntaxError: \\8 and \\9 are not allowed in strict mode."},
		{"function f() { '\\9\\01'; 'use strict' }", "SyntaxError: Octal escape sequences are not allowed in strict mode."},
		{"function f() { '\\9'; '\\01'; 'use strict' }", "SyntaxError: Octal escape sequences are not allowed in strict mode."},
		{"function f() { '\\01'; '\\9'; 'use strict' }", "SyntaxError: \\8 and \\9 are not allowed in strict mode."},
		{"'use strict'; '\\08'", "SyntaxError: Octal escape sequences are not allowed in strict mode."},
		{"'use strict'; '\\0\\8'", "SyntaxError: \\8 and \\9 are not allowed in strict mode."},
		{"'use strict'; '\\00'", "SyntaxError: Octal escape sequences are not allowed in strict mode."},
		{"'use strict'; '\\0'", "ok \u0000"},
		{"'use strict'; 010; '\\9'", "SyntaxError: \\8 and \\9 are not allowed in strict mode."},
		{"'use strict'; '\\9'; 010", "SyntaxError: Octal literals are not allowed in strict mode."},
		{"'use strict'; `\\9`", "SyntaxError: \\8 and \\9 are not allowed in template strings."},
		{"'use strict'; '\\1' + 010", "SyntaxError: Octal literals are not allowed in strict mode."},
		{"'use strict'; '\\9'; function f(){ '\\01' }", "SyntaxError: Octal escape sequences are not allowed in strict mode."},
		{"'use strict'; var x = function(){ '\\9' }; function f(){ '\\01' }", "SyntaxError: \\8 and \\9 are not allowed in strict mode."},
		{"'use strict'; let a; let a; function f(){ '\\01' }", "SyntaxError: Identifier 'a' has already been declared"},
		{"'use strict'; '\\01'; let a; let a;", "SyntaxError: Identifier 'a' has already been declared"},
		{"function f() { '\\01'; function g() { 'use strict'; } }", "ok undefined"},
		{"function f() { 'use strict'; var g = () => '\\9'; return '\\01' }", "SyntaxError: \\8 and \\9 are not allowed in strict mode."},
		{"class C { m() { return '\\01' } }", "SyntaxError: Octal escape sequences are not allowed in strict mode."},
		{"class C { x = '\\01' }", "ok undefined"},
		{"class C { static { '\\01' } }", "ok undefined"},
		{"class C { ['\\01'] = 1 }", "ok undefined"},
		{"class C { m() { '\\9' } n() { '\\01' } }", "SyntaxError: \\8 and \\9 are not allowed in strict mode."},
		{"'use strict'; class C { x = '\\01' }", "SyntaxError: Octal escape sequences are not allowed in strict mode."},
		{"'use strict'; class C { x = '\\9' } '\\01'", "SyntaxError: Octal escape sequences are not allowed in strict mode."},
		{"'use strict'; ({ m() { '\\9' }, n: '\\01' })", "SyntaxError: \\8 and \\9 are not allowed in strict mode."},
		{"'use strict'; `${'\\01'}`", "SyntaxError: Octal escape sequences are not allowed in strict mode."},
		{"'use strict'; 010 + '\\9' + 08", "SyntaxError: Decimals with leading zeros are not allowed in strict mode."},
		{"'use strict'; 08; 010", "SyntaxError: Octal literals are not allowed in strict mode."},
		{"'use strict'; 010; 08", "SyntaxError: Decimals with leading zeros are not allowed in strict mode."},
		{"function f(a = '\\01') { 'use strict' }", "SyntaxError: Illegal 'use strict' directive in function with non-simple parameter list"},
		{"function f(a = '\\01') { 'use strict'; '\\9' }", "SyntaxError: Illegal 'use strict' directive in function with non-simple parameter list"},
		{"'use strict'; if (1) { '\\01' } else { '\\9' }", "SyntaxError: \\8 and \\9 are not allowed in strict mode."},
		{"'use strict'; function f() { '\\01' } x = { a: 1, a: 2 }; with (x) {}", "SyntaxError: Octal escape sequences are not allowed in strict mode."},
		{"'use strict'; with (x) {} function f() { '\\01' }", "SyntaxError: Strict mode code may not include a with statement"},
		{"'use strict'; function f() { '\\01' } delete x;", "SyntaxError: Octal escape sequences are not allowed in strict mode."},
		{"'use strict'; delete x; function f() { '\\01' }", "SyntaxError: Delete of an unqualified identifier in strict mode."},
	} {
		res, err := ev(_undefined, r.ToValue(tc.src))
		if err != nil {
			t.Fatal(err)
		}
		if got := res.String(); got != tc.want {
			t.Errorf("%q: %q, want %q", tc.src, got, tc.want)
		}
	}
}

func TestLegacyOctalBeforeLaterErrorScript(t *testing.T) {
	// In a script, Goja compiles the function before the declarations, so
	// the octal escape V8 reports at the end of f comes before the
	// redeclaration after it (Node.js 18.20.3: "Octal escape sequences are
	// not allowed in strict mode.").
	_, err := New().RunString(`'use strict'; function f() { '\01' } let a; let a;`)
	if err == nil || !strings.Contains(err.Error(), "Octal escape sequences are not allowed in strict mode.") {
		t.Fatal(err)
	}
}
