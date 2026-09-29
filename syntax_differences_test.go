package goja

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestNodeSyntaxDifferences: what Node.js 18.20.3 does, compiling each
// script as a vm.Script and running it in a new context, where goja did
// otherwise: strict octal escapes are SyntaxErrors, sloppy 08 and 09.5 are
// decimals, a call assigned to is a ReferenceError when it runs, a var of
// eval code must not redeclare a lexical binding around it, and import()
// parses (and rejects, TestDynamicImport).
func TestNodeSyntaxDifferences(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"'use strict'; '\\1'", "SyntaxError: Octal escape sequences are not allowed in strict mode."},
		{"'use strict'; '\\08'", "SyntaxError: Octal escape sequences are not allowed in strict mode."},
		{"'use strict'; '\\8'", "SyntaxError: \\8 and \\9 are not allowed in strict mode."},
		{"'use strict'; '\\9'", "SyntaxError: \\8 and \\9 are not allowed in strict mode."},
		{"'use strict'; '\\0'", "ok: \"\\u0000\""},
		{"'use strict'; '\\00'", "SyntaxError: Octal escape sequences are not allowed in strict mode."},
		{"'use strict'; '\\0a'", "ok: \"\\u0000a\""},
		{"'use strict'; '\\7'", "SyntaxError: Octal escape sequences are not allowed in strict mode."},
		{"function f(){ '\\1'; 'use strict' }", "SyntaxError: Octal escape sequences are not allowed in strict mode."},
		{"function f(){ 'use strict'; return {'\\1': 1} }", "SyntaxError: Octal escape sequences are not allowed in strict mode."},
		{"'use strict'; ({'\\1': 1})", "SyntaxError: Octal escape sequences are not allowed in strict mode."},
		{"'use strict'; var o = {a: '\\01'}", "SyntaxError: Octal escape sequences are not allowed in strict mode."},
		{"function f(){ 'use strict'; return '\\8' }", "SyntaxError: \\8 and \\9 are not allowed in strict mode."},
		{"'\\1'.charCodeAt(0)", "ok: 1"},
		{"'\\8'", "ok: \"8\""},
		{"'\\08'.length", "ok: 2"},
		{"08", "ok: 8"},
		{"09.5", "ok: 9.5"},
		{"08.5", "ok: 8.5"},
		{"089", "ok: 89"},
		{"07.5", "SyntaxError: Unexpected number"},
		{"08e1", "ok: 80"},
		{"09n", "SyntaxError: Invalid or unexpected token"},
		{"0_8", "SyntaxError: Numeric separator can not be used after leading 0."},
		{"08_1", "SyntaxError: Invalid or unexpected token"},
		{"009", "ok: 9"},
		{"0009.5", "ok: 9.5"},
		{"019", "ok: 19"},
		{"018", "ok: 18"},
		{"017", "ok: 15"},
		{"08.toString()", "SyntaxError: Invalid or unexpected token"},
		{"08 .toString()", "ok: \"8\""},
		{"'use strict'; 08", "SyntaxError: Decimals with leading zeros are not allowed in strict mode."},
		{"'use strict'; 09.5", "SyntaxError: Decimals with leading zeros are not allowed in strict mode."},
		{"function f(){ 'use strict'; return 08 }", "SyntaxError: Decimals with leading zeros are not allowed in strict mode."},
		{"'use strict'; 00", "SyntaxError: Octal literals are not allowed in strict mode."},
		{"08.5e2", "ok: 850"},
		{"09.", "ok: 9"},
		{"09.e1", "ok: 90"},
		{"function f(){ return 1 } f() = 1", "ReferenceError: Invalid left-hand side in assignment"},
		{"var n = 0; function f(){ n++ } try { f() = 1 } catch (e) {} n", "ok: 1"},
		{"var n = 0; function f(){ n++ } try { f() = (n += 10) } catch (e) {} n", "ok: 1"},
		{"function f(){} f() += 1", "ReferenceError: Invalid left-hand side in assignment"},
		{"var n = 0; function f(){ n++; return 1 } try { f() += 1 } catch (e) {} n", "ok: 1"},
		{"function f(){} f()++", "ReferenceError: Invalid left-hand side expression in postfix operation"},
		{"function f(){} ++f()", "ReferenceError: Invalid left-hand side expression in prefix operation"},
		{"function f(){} for (f() in {a: 1}) {}", "ReferenceError: Invalid left-hand side in for-loop"},
		{"function f(){} for (f() of [1]) {}", "ReferenceError: Invalid left-hand side in for-loop"},
		{"function f(){} for (f() in {}) {} 'done'", "ok: \"done\""},
		{"function f(){} for (f() of []) {} 'done'", "ok: \"done\""},
		{"'use strict'; function f(){} f() = 1", "ReferenceError: Invalid left-hand side in assignment"},
		{"function f(){} [f()] = [1]", "SyntaxError: Invalid destructuring assignment target"},
		{"function f(){} ({a: f()} = {})", "SyntaxError: Invalid destructuring assignment target"},
		{"function f(){} f() &&= 1", "SyntaxError: Invalid left-hand side in assignment"},
		{"function f(){} f() ??= 1", "SyntaxError: Invalid left-hand side in assignment"},
		{"var o = {f(){}}; o.f() = 1", "ReferenceError: Invalid left-hand side in assignment"},
		{"new Date() = 1", "SyntaxError: Invalid left-hand side in assignment"},
		{"function f(){} (f()) = 1", "ReferenceError: Invalid left-hand side in assignment"},
		{"function f(){} f()--", "ReferenceError: Invalid left-hand side expression in postfix operation"},
		{"let x = 1; eval('var x')", "SyntaxError: Identifier 'x' has already been declared"},
		{"let x = 1; eval('var y'); typeof y", "ok: \"undefined\""},
		{"function g(){ let x; eval('var x') } g()", "SyntaxError: Identifier 'x' has already been declared"},
		{"{ let x; eval('var x') }", "SyntaxError: Identifier 'x' has already been declared"},
		{"let x; eval('function x(){}')", "SyntaxError: Identifier 'x' has already been declared"},
		{"try { throw 1 } catch (x) { eval('var x = 2') } 'ok'", "ok: \"ok\""},
		{"try { throw 1 } catch ([x]) { eval('var x') }", "TypeError: number 1 is not iterable (cannot read property Symbol(Symbol.iterator))"},
		{"for (let x of [1]) { eval('var x') }", "SyntaxError: Identifier 'x' has already been declared"},
		{"'use strict'; let x; eval('var x'); 'ok'", "ok: \"ok\""},
		{"let x; eval('\"use strict\"; var x'); 'ok'", "ok: \"ok\""},
		{"function g(){ let x; { eval('var x') } } g()", "SyntaxError: Identifier 'x' has already been declared"},
		{"function g(){ { let x; eval('var x') } } g()", "SyntaxError: Identifier 'x' has already been declared"},
		{"function g(a){ let x; (0, eval)('var x'); return 'ok' } g()", "ok: \"ok\""},
		{"let x; (0, eval)('var x')", "SyntaxError: Identifier 'x' has already been declared"},
		{"const c = 1; eval('var c')", "SyntaxError: Identifier 'c' has already been declared"},
		{"class C {} eval('var C')", "SyntaxError: Identifier 'C' has already been declared"},
		{"function g(){ let x; function h(){ eval('var x') } h() } g(); 'ok'", "ok: \"ok\""},
		{"let x; eval('var x = 1; var q = 2')", "SyntaxError: Identifier 'x' has already been declared"},
		{"let x; eval('if (false) { var x }')", "SyntaxError: Identifier 'x' has already been declared"},
		{"let x; eval('for (var x of []) {}')", "SyntaxError: Identifier 'x' has already been declared"},
		{"import('x')", "compiles"},
		{"new import('x')", "SyntaxError: Cannot use new with import"},
		{"import()", "SyntaxError: import() requires a specifier"},
		{"import('a', 'b', 'c')", "SyntaxError: Unexpected string"},
		{"import(...a)", "SyntaxError: Unexpected token '...'"},
		{"var i = import; ", "SyntaxError: Cannot use import statement outside a module"},
		{"import('x').then", "compiles"},
		{"(function(){ return import('x') })()", "compiles"},
	} {
		vm := New()
		got := "compiles"
		if strings.HasPrefix(tc.want, "compiles") || tc.want == "compiles" {
			if _, err := Compile("", tc.src, false); err != nil {
				got = "SyntaxError: " + syntaxErrorMessage(err)
			}
		} else {
			v, err := vm.RunString(tc.src)
			switch err := err.(type) {
			case nil:
				if s, ok := v.Export().(string); ok {
					b, _ := json.Marshal(s)
					got = "ok: " + string(b)
				} else {
					got = "ok: " + v.String()
				}
			case *CompilerSyntaxError:
				got = "SyntaxError: " + syntaxErrorMessage(err)
			case *Exception:
				o := err.Value().(*Object)
				name, msg := o.Get("constructor").(*Object).Get("name").String(), o.Get("message").String()
				if name == "SyntaxError" {
					msg = syntaxErrorMessage(errors.New(strings.TrimPrefix(msg, "SyntaxError: ")))
				}
				got = name + ": " + msg
			default:
				got = err.Error()
			}
		}
		if got != tc.want {
			t.Errorf("%s\n got: %s\nwant: %s", tc.src, got, tc.want)
		}
	}
}

// TestDynamicImport: import() converts its specifier to a string, and
// returns a promise rejected as Node.js's vm contexts without
// importModuleDynamically reject it; the log is Node.js 18.20.3's.
func TestDynamicImport(t *testing.T) {
	vm := New()
	if _, err := vm.RunString("var log = [];\nvar p;\ntry { p = import('x'); log.push('returned ' + Object.prototype.toString.call(p)); } catch (e) { log.push('threw ' + e.constructor.name + ': ' + e.message); }\nlog.push('sync after');\nif (p) p.then(function (v) { log.push('fulfilled ' + v) }, function (e) { log.push('rejected ' + e.name + ': ' + e.message + ' code=' + e.code + ' keys=' + Object.keys(e) + ' str=' + String(e)) });\nPromise.resolve().then(function () { log.push('microtask 1') });\nvar order = [];\ntry { import({toString() { order.push('toString'); return 'y' }}).catch(function (e) { log.push('rejected2 ' + e.message) }) } catch (e) { log.push('threw2 ' + e.message) }\nlog.push('order ' + order.join());\ntry { import(Symbol()).catch(function (e) { log.push('rejected3 ' + e.constructor.name + ': ' + e.message) }) } catch (e) { log.push('threw3 ' + e.message) }\ntry { var q = import('z', {assert: {}}); q.catch(function (e) { log.push('rejected4 ' + e.message) }); log.push('two args ok') } catch (e) { log.push('two args threw ' + e.message) }\n(function () { return import('w') })().catch(function (e) { log.push('rejected5 ' + e.code) });\n"); err != nil {
		t.Fatal(err)
	}
	const want = "[\"returned [object Promise]\",\"sync after\",\"order toString\",\"two args ok\",\"rejected TypeError: A dynamic import callback was not specified. code=ERR_VM_DYNAMIC_IMPORT_CALLBACK_MISSING keys=code str=TypeError [ERR_VM_DYNAMIC_IMPORT_CALLBACK_MISSING]: A dynamic import callback was not specified.\",\"microtask 1\",\"rejected2 A dynamic import callback was not specified.\",\"rejected3 TypeError: Cannot convert a Symbol value to a string\",\"rejected4 A dynamic import callback was not specified.\",\"rejected5 ERR_VM_DYNAMIC_IMPORT_CALLBACK_MISSING\"]"
	got, _ := json.Marshal(vm.Get("log").Export())
	if string(got) != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}
