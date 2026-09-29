package goja

import "testing"

func TestEvalSyntaxErrorMessageNode18(t *testing.T) {
	// A SyntaxError thrown by eval, indirect eval or new Function has V8's
	// message, which Node.js 18.20.3 recorded here, and so the first line
	// of its stack: Goja's has where it found the error ("<eval>: Line 1:3
	// ..." or "... at <eval>:1:15"), how many more it found, and the token
	// of "Unexpected token x" unquoted.
	script := "(function () {\n" +
		"var srcs = [\"a b\", \"var x = @\", \"'use strict'; 010\", \"(\", \"var 1\", \"let let = 1\", \"\\\"\\\\u{110000}\\\"\", \"return\", \"a\\n  b c\", \"08n\", \"`\\\\01`\", \"x = 'abc\", \"{\", \"function(){}\", \"class { }\", \"let a; let a;\", \"'use strict'; with(a){}\", \"super()\", \"yield 1\", \"for (let x of []) label: function f(){}\", \"var \\\\u0030abc\"];\n" +
		"var out = [];\n" +
		"for (var s of srcs) {\n" +
		"  for (var how of [\"eval\", \"Function\", \"indirect\"]) {\n" +
		"    try { if (how === \"eval\") eval(s); else if (how === \"Function\") new Function(s); else (0, eval)(s); out.push(how + \" \" + JSON.stringify(s) + \": ok\"); }\n" +
		"    catch (e) { out.push(how + \" \" + JSON.stringify(s) + \": \" + e.name + \" | \" + JSON.stringify(e.message) + \" | \" + JSON.stringify(String(e.stack).split(\"\\n\")[0]) + \" | \" + String(e)); }\n" +
		"  }\n" +
		"}\n" +
		"return out.join(\"\\n\");\n" +
		"})()\n"
	testScript(script, newStringValue("eval \"a b\": SyntaxError | \"Unexpected identifier\" | \"SyntaxError: Unexpected identifier\" | SyntaxError: Unexpected identifier\nFunction \"a b\": SyntaxError | \"Unexpected identifier\" | \"SyntaxError: Unexpected identifier\" | SyntaxError: Unexpected identifier\nindirect \"a b\": SyntaxError | \"Unexpected identifier\" | \"SyntaxError: Unexpected identifier\" | SyntaxError: Unexpected identifier\neval \"var x = @\": SyntaxError | \"Invalid or unexpected token\" | \"SyntaxError: Invalid or unexpected token\" | SyntaxError: Invalid or unexpected token\nFunction \"var x = @\": SyntaxError | \"Invalid or unexpected token\" | \"SyntaxError: Invalid or unexpected token\" | SyntaxError: Invalid or unexpected token\nindirect \"var x = @\": SyntaxError | \"Invalid or unexpected token\" | \"SyntaxError: Invalid or unexpected token\" | SyntaxError: Invalid or unexpected token\neval \"'use strict'; 010\": SyntaxError | \"Octal literals are not allowed in strict mode.\" | \"SyntaxError: Octal literals are not allowed in strict mode.\" | SyntaxError: Octal literals are not allowed in strict mode.\nFunction \"'use strict'; 010\": SyntaxError | \"Octal literals are not allowed in strict mode.\" | \"SyntaxError: Octal literals are not allowed in strict mode.\" | SyntaxError: Octal literals are not allowed in strict mode.\nindirect \"'use strict'; 010\": SyntaxError | \"Octal literals are not allowed in strict mode.\" | \"SyntaxError: Octal literals are not allowed in strict mode.\" | SyntaxError: Octal literals are not allowed in strict mode.\neval \"(\": SyntaxError | \"Unexpected end of input\" | \"SyntaxError: Unexpected end of input\" | SyntaxError: Unexpected end of input\nFunction \"(\": SyntaxError | \"Unexpected token '}'\" | \"SyntaxError: Unexpected token '}'\" | SyntaxError: Unexpected token '}'\nindirect \"(\": SyntaxError | \"Unexpected end of input\" | \"SyntaxError: Unexpected end of input\" | SyntaxError: Unexpected end of input\neval \"var 1\": SyntaxError | \"Unexpected number\" | \"SyntaxError: Unexpected number\" | SyntaxError: Unexpected number\nFunction \"var 1\": SyntaxError | \"Unexpected number\" | \"SyntaxError: Unexpected number\" | SyntaxError: Unexpected number\nindirect \"var 1\": SyntaxError | \"Unexpected number\" | \"SyntaxError: Unexpected number\" | SyntaxError: Unexpected number\neval \"let let = 1\": SyntaxError | \"let is disallowed as a lexically bound name\" | \"SyntaxError: let is disallowed as a lexically bound name\" | SyntaxError: let is disallowed as a lexically bound name\nFunction \"let let = 1\": SyntaxError | \"let is disallowed as a lexically bound name\" | \"SyntaxError: let is disallowed as a lexically bound name\" | SyntaxError: let is disallowed as a lexically bound name\nindirect \"let let = 1\": SyntaxError | \"let is disallowed as a lexically bound name\" | \"SyntaxError: let is disallowed as a lexically bound name\" | SyntaxError: let is disallowed as a lexically bound name\neval \"\\\"\\\\u{110000}\\\"\": SyntaxError | \"Undefined Unicode code-point\" | \"SyntaxError: Undefined Unicode code-point\" | SyntaxError: Undefined Unicode code-point\nFunction \"\\\"\\\\u{110000}\\\"\": SyntaxError | \"Undefined Unicode code-point\" | \"SyntaxError: Undefined Unicode code-point\" | SyntaxError: Undefined Unicode code-point\nindirect \"\\\"\\\\u{110000}\\\"\": SyntaxError | \"Undefined Unicode code-point\" | \"SyntaxError: Undefined Unicode code-point\" | SyntaxError: Undefined Unicode code-point\neval \"return\": SyntaxError | \"Illegal return statement\" | \"SyntaxError: Illegal return statement\" | SyntaxError: Illegal return statement\nFunction \"return\": ok\nindirect \"return\": SyntaxError | \"Illegal return statement\" | \"SyntaxError: Illegal return statement\" | SyntaxError: Illegal return statement\neval \"a\\n  b c\": SyntaxError | \"Unexpected identifier\" | \"SyntaxError: Unexpected identifier\" | SyntaxError: Unexpected identifier\nFunction \"a\\n  b c\": SyntaxError | \"Unexpected identifier\" | \"SyntaxError: Unexpected identifier\" | SyntaxError: Unexpected identifier\nindirect \"a\\n  b c\": SyntaxError | \"Unexpected identifier\" | \"SyntaxError: Unexpected identifier\" | SyntaxError: Unexpected identifier\neval \"08n\": SyntaxError | \"Invalid or unexpected token\" | \"SyntaxError: Invalid or unexpected token\" | SyntaxError: Invalid or unexpected token\nFunction \"08n\": SyntaxError | \"Invalid or unexpected token\" | \"SyntaxError: Invalid or unexpected token\" | SyntaxError: Invalid or unexpected token\nindirect \"08n\": SyntaxError | \"Invalid or unexpected token\" | \"SyntaxError: Invalid or unexpected token\" | SyntaxError: Invalid or unexpected token\neval \"`\\\\01`\": SyntaxError | \"Octal escape sequences are not allowed in template strings.\" | \"SyntaxError: Octal escape sequences are not allowed in template strings.\" | SyntaxError: Octal escape sequences are not allowed in template strings.\nFunction \"`\\\\01`\": SyntaxError | \"Octal escape sequences are not allowed in template strings.\" | \"SyntaxError: Octal escape sequences are not allowed in template strings.\" | SyntaxError: Octal escape sequences are not allowed in template strings.\nindirect \"`\\\\01`\": SyntaxError | \"Octal escape sequences are not allowed in template strings.\" | \"SyntaxError: Octal escape sequences are not allowed in template strings.\" | SyntaxError: Octal escape sequences are not allowed in template strings.\neval \"x = 'abc\": SyntaxError | \"Invalid or unexpected token\" | \"SyntaxError: Invalid or unexpected token\" | SyntaxError: Invalid or unexpected token\nFunction \"x = 'abc\": SyntaxError | \"Invalid or unexpected token\" | \"SyntaxError: Invalid or unexpected token\" | SyntaxError: Invalid or unexpected token\nindirect \"x = 'abc\": SyntaxError | \"Invalid or unexpected token\" | \"SyntaxError: Invalid or unexpected token\" | SyntaxError: Invalid or unexpected token\neval \"{\": SyntaxError | \"Unexpected end of input\" | \"SyntaxError: Unexpected end of input\" | SyntaxError: Unexpected end of input\nFunction \"{\": SyntaxError | \"Unexpected token ')'\" | \"SyntaxError: Unexpected token ')'\" | SyntaxError: Unexpected token ')'\nindirect \"{\": SyntaxError | \"Unexpected end of input\" | \"SyntaxError: Unexpected end of input\" | SyntaxError: Unexpected end of input\neval \"function(){}\": SyntaxError | \"Function statements require a function name\" | \"SyntaxError: Function statements require a function name\" | SyntaxError: Function statements require a function name\nFunction \"function(){}\": SyntaxError | \"Function statements require a function name\" | \"SyntaxError: Function statements require a function name\" | SyntaxError: Function statements require a function name\nindirect \"function(){}\": SyntaxError | \"Function statements require a function name\" | \"SyntaxError: Function statements require a function name\" | SyntaxError: Function statements require a function name\neval \"class { }\": SyntaxError | \"Unexpected token '{'\" | \"SyntaxError: Unexpected token '{'\" | SyntaxError: Unexpected token '{'\nFunction \"class { }\": SyntaxError | \"Unexpected token '{'\" | \"SyntaxError: Unexpected token '{'\" | SyntaxError: Unexpected token '{'\nindirect \"class { }\": SyntaxError | \"Unexpected token '{'\" | \"SyntaxError: Unexpected token '{'\" | SyntaxError: Unexpected token '{'\neval \"let a; let a;\": SyntaxError | \"Identifier 'a' has already been declared\" | \"SyntaxError: Identifier 'a' has already been declared\" | SyntaxError: Identifier 'a' has already been declared\nFunction \"let a; let a;\": SyntaxError | \"Identifier 'a' has already been declared\" | \"SyntaxError: Identifier 'a' has already been declared\" | SyntaxError: Identifier 'a' has already been declared\nindirect \"let a; let a;\": SyntaxError | \"Identifier 'a' has already been declared\" | \"SyntaxError: Identifier 'a' has already been declared\" | SyntaxError: Identifier 'a' has already been declared\neval \"'use strict'; with(a){}\": SyntaxError | \"Strict mode code may not include a with statement\" | \"SyntaxError: Strict mode code may not include a with statement\" | SyntaxError: Strict mode code may not include a with statement\nFunction \"'use strict'; with(a){}\": SyntaxError | \"Strict mode code may not include a with statement\" | \"SyntaxError: Strict mode code may not include a with statement\" | SyntaxError: Strict mode code may not include a with statement\nindirect \"'use strict'; with(a){}\": SyntaxError | \"Strict mode code may not include a with statement\" | \"SyntaxError: Strict mode code may not include a with statement\" | SyntaxError: Strict mode code may not include a with statement\neval \"super()\": SyntaxError | \"'super' keyword unexpected here\" | \"SyntaxError: 'super' keyword unexpected here\" | SyntaxError: 'super' keyword unexpected here\nFunction \"super()\": SyntaxError | \"'super' keyword unexpected here\" | \"SyntaxError: 'super' keyword unexpected here\" | SyntaxError: 'super' keyword unexpected here\nindirect \"super()\": SyntaxError | \"'super' keyword unexpected here\" | \"SyntaxError: 'super' keyword unexpected here\" | SyntaxError: 'super' keyword unexpected here\neval \"yield 1\": SyntaxError | \"Unexpected number\" | \"SyntaxError: Unexpected number\" | SyntaxError: Unexpected number\nFunction \"yield 1\": SyntaxError | \"Unexpected number\" | \"SyntaxError: Unexpected number\" | SyntaxError: Unexpected number\nindirect \"yield 1\": SyntaxError | \"Unexpected number\" | \"SyntaxError: Unexpected number\" | SyntaxError: Unexpected number\neval \"for (let x of []) label: function f(){}\": SyntaxError | \"In non-strict mode code, functions can only be declared at top level, inside a block, or as the body of an if statement.\" | \"SyntaxError: In non-strict mode code, functions can only be declared at top level, inside a block, or as the body of an if statement.\" | SyntaxError: In non-strict mode code, functions can only be declared at top level, inside a block, or as the body of an if statement.\nFunction \"for (let x of []) label: function f(){}\": SyntaxError | \"In non-strict mode code, functions can only be declared at top level, inside a block, or as the body of an if statement.\" | \"SyntaxError: In non-strict mode code, functions can only be declared at top level, inside a block, or as the body of an if statement.\" | SyntaxError: In non-strict mode code, functions can only be declared at top level, inside a block, or as the body of an if statement.\nindirect \"for (let x of []) label: function f(){}\": SyntaxError | \"In non-strict mode code, functions can only be declared at top level, inside a block, or as the body of an if statement.\" | \"SyntaxError: In non-strict mode code, functions can only be declared at top level, inside a block, or as the body of an if statement.\" | SyntaxError: In non-strict mode code, functions can only be declared at top level, inside a block, or as the body of an if statement.\neval \"var \\\\u0030abc\": SyntaxError | \"Invalid or unexpected token\" | \"SyntaxError: Invalid or unexpected token\" | SyntaxError: Invalid or unexpected token\nFunction \"var \\\\u0030abc\": SyntaxError | \"Invalid or unexpected token\" | \"SyntaxError: Invalid or unexpected token\" | SyntaxError: Invalid or unexpected token\nindirect \"var \\\\u0030abc\": SyntaxError | \"Invalid or unexpected token\" | \"SyntaxError: Invalid or unexpected token\" | SyntaxError: Invalid or unexpected token"), t)
}

func TestCompilerSyntaxErrorV8Message(t *testing.T) {
	// What Compile returns, for an embedder that shows the message as
	// Node.js would (Node.js 18.20.3's messages).
	for _, tc := range []struct{ src, want string }{
		{"a b", "Unexpected identifier"},
		{"(", "Unexpected end of input"},
		{"var x = @", "Invalid or unexpected token"},
		{"var x = {;", "Unexpected token ';'"},
		{"'use strict'; 010", "Octal literals are not allowed in strict mode."},
		{`"\u{110000}"`, "Undefined Unicode code-point"},
		{"/(/", "Invalid regular expression: /(/: Unterminated group"},
		{"/(/u; )", "Invalid regular expression: /(/: Unterminated group"},
		{"x = /[/", "Invalid regular expression: missing /"},
		{"/x/gg", "Invalid regular expression flags"},
	} {
		_, err := Compile("rule.js", tc.src, false)
		se, ok := err.(*CompilerSyntaxError)
		if !ok {
			t.Fatalf("%s: %v", tc.src, err)
		}
		if got := se.V8Message(); got != tc.want {
			t.Errorf("%s: %q, want %q (%s)", tc.src, got, tc.want, se.Error())
		}
	}
}

func TestOpenCommentSyntaxErrorNode18(t *testing.T) {
	// A comment left open is an illegal token in V8 (Node.js 18.20.3's
	// messages), not the end of the input.
	r := New()
	v, err := r.RunString(`({
		ev(src) { try { return "ok " + String((0, eval)(src)); } catch (e) { return e.name + ": " + e.message; } },
		fn(src) { try { new Function(src); return "ok"; } catch (e) { return e.name + ": " + e.message; } },
	})`)
	if err != nil {
		t.Fatal(err)
	}
	o := v.ToObject(r)
	for _, tc := range []struct{ how, src, want string }{
		{"ev", "/* abc", "SyntaxError: Invalid or unexpected token"},
		{"fn", "/* abc", "SyntaxError: Invalid or unexpected token"},
		{"ev", "a /* x", "SyntaxError: Invalid or unexpected token"},
		{"fn", "a /* x", "SyntaxError: Invalid or unexpected token"},
		{"ev", "/*\n", "SyntaxError: Invalid or unexpected token"},
		{"fn", "/*\n", "SyntaxError: Invalid or unexpected token"},
		{"ev", "/*\n*", "SyntaxError: Invalid or unexpected token"},
		{"fn", "/*\n*", "SyntaxError: Invalid or unexpected token"},
		{"ev", "x = 1 /*", "SyntaxError: Invalid or unexpected token"},
		{"fn", "x = 1 /*", "SyntaxError: Invalid or unexpected token"},
		{"ev", "/**", "SyntaxError: Invalid or unexpected token"},
		{"fn", "/**", "SyntaxError: Invalid or unexpected token"},
		{"ev", "/*/", "SyntaxError: Invalid or unexpected token"},
		{"fn", "/*/", "SyntaxError: Invalid or unexpected token"},
		{"ev", "/* */ /*", "SyntaxError: Invalid or unexpected token"},
		{"fn", "/* */ /*", "SyntaxError: Invalid or unexpected token"},
		{"ev", "`${/*`", "SyntaxError: Invalid or unexpected token"},
		{"fn", "`${/*`", "SyntaxError: Invalid or unexpected token"},
		{"ev", "a\n/* x\n y", "SyntaxError: Invalid or unexpected token"},
		{"fn", "a\n/* x\n y", "SyntaxError: Invalid or unexpected token"},
		{"ev", "/* x */ 1", "ok 1"},
		{"fn", "/* x */ 1", "ok"},
		{"ev", "--> x", "ok undefined"},
		{"fn", "--> x", "ok"},
		{"ev", "/*\n*/ --> x", "ok undefined"},
		{"fn", "/*\n*/ --> x", "ok"},
		{"ev", "<!-- x", "ok undefined"},
		{"fn", "<!-- x", "ok"},
		{"ev", "x\n<!-- /*", "ReferenceError: x is not defined"},
		{"fn", "x\n<!-- /*", "ok"},
	} {
		fn, _ := AssertFunction(o.Get(tc.how))
		res, err := fn(o, r.ToValue(tc.src))
		if err != nil {
			t.Fatal(err)
		}
		if got := res.String(); got != tc.want {
			t.Errorf("%s %q: %q, want %q", tc.how, tc.src, got, tc.want)
		}
	}
}
