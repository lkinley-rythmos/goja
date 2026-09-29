package goja

import "testing"

func TestLexerEscapesNode18(t *testing.T) {
	// Escapes in string literals, templates and identifiers, recorded with
	// Node.js 18.20.3: every code point up to U+10FFFF (a string with
	// "\u{10FFFF}" panicked out of RunString), legacy octal escapes (\611 is
	// "11"), and V8's messages for malformed escapes, octal escapes in strict
	// mode and templates, escapes in identifiers and keywords. goja's own
	// format around a message (its location, " (and N more errors)") is left
	// out.
	script := "(function () {\n" +
		"\tvar srcs = ['\"\\\\u{10FFFF}\".length', '\"a\\\\u{10FFFF}b\".charCodeAt(2)', '\"\\\\u{0010FFFF}\".codePointAt(0)', '\"\\\\u{0000000041}\"', '`\\\\u{10FFFF}`.length', 'var \\\\u{2F800}x = 1; \\\\u{2F800}x', 'var a\\\\u{61} = 2; aa',\n" +
		"\t\t'\"\\\\611\"', '\"\\\\411\"', '\"\\\\3777\"', '\"\\\\08\"', '\"\\\\8\\\\9\"', '\"\\\\x41\\\\u0042\\\\u{43}\"', 'String.raw`\\\\u{110000}\\\\01`', '(function (s) { return s[0] === undefined && s.raw[0]; })`\\\\u{}\\\\x4`',\n" +
		"\t\t'\"\\\\u{110000}\"', '\"\\\\u{}\"', '\"\\\\u{12\"', '\"\\\\u{FFFFFFFFFFFF}\"', '\"\\\\u{1g}\"', '\"\\\\x4\"', '\"\\\\u12\"', '\"\\\\u\"', '\"\\\\x4\\\\\"', '\"a\\\\u{110000}\\n\"', '\"unterminated', '\"\\\\',\n" +
		"\t\t'\"use strict\"; \"\\\\01\"', '\"use strict\"; \"\\\\8\"', '\"use strict\"; \"\\\\9\\\\1\"', '\"use strict\"; ({\"\\\\02\": 1})', '(function () { \"use strict\"; return \"\\\\7\"; })()',\n" +
		"\t\t'`\\\\01`', '`\\\\8`', '`\\\\1\\\\8`', '`\\\\x4`', '`\\\\u{110000}`', '`\\\\u006141\\\\01\\\\`', '`a${1}\\\\u{}`',\n" +
		"\t\t'a\\\\u{110000}', 'var \\\\u0030abc', 'v\\\\u0061r x = 1', 'var \\\\u{10FFFF}', 'var x\\\\u{1F600}\\\\u{110000}', 'var \\\\u{10FFFF}\\\\u12', 'var x\\\\u005c', 'x\\\\', '\\\\u0xyz', 'var _\\\\n\\\\u8'];\n" +
		"\tvar out = [];\n" +
		"\tfor (var i = 0; i < srcs.length; i++) {\n" +
		"\t\tvar r;\n" +
		"\t\ttry { r = \"ok \" + JSON.stringify((0, eval)(srcs[i])); } catch (e) { r = e.name + \": \" + e.message.replace(/^.*Line \\d+:\\d+ /, \"\").replace(/ \\(and \\d+ more errors\\)$/, \"\").replace(/^SyntaxError: /, \"\").replace(/ at \\S+$/, \"\"); }\n" +
		"\t\tout.push(srcs[i] + \" => \" + r);\n" +
		"\t}\n" +
		"\treturn JSON.stringify(out);\n" +
		"})()\n"
	testScript(script, newStringValue("[\"\\\"\\\\u{10FFFF}\\\".length => ok 2\",\"\\\"a\\\\u{10FFFF}b\\\".charCodeAt(2) => ok 57343\",\"\\\"\\\\u{0010FFFF}\\\".codePointAt(0) => ok 1114111\",\"\\\"\\\\u{0000000041}\\\" => ok \\\"A\\\"\",\"`\\\\u{10FFFF}`.length => ok 2\",\"var \\\\u{2F800}x = 1; \\\\u{2F800}x => ok 1\",\"var a\\\\u{61} = 2; aa => ok 2\",\"\\\"\\\\611\\\" => ok \\\"11\\\"\",\"\\\"\\\\411\\\" => ok \\\"!1\\\"\",\"\\\"\\\\3777\\\" => ok \\\"\u00ff7\\\"\",\"\\\"\\\\08\\\" => ok \\\"\\\\u00008\\\"\",\"\\\"\\\\8\\\\9\\\" => ok \\\"89\\\"\",\"\\\"\\\\x41\\\\u0042\\\\u{43}\\\" => ok \\\"ABC\\\"\",\"String.raw`\\\\u{110000}\\\\01` => ok \\\"\\\\\\\\u{110000}\\\\\\\\01\\\"\",\"(function (s) { return s[0] === undefined && s.raw[0]; })`\\\\u{}\\\\x4` => ok \\\"\\\\\\\\u{}\\\\\\\\x4\\\"\",\"\\\"\\\\u{110000}\\\" => SyntaxError: Undefined Unicode code-point\",\"\\\"\\\\u{}\\\" => SyntaxError: Invalid Unicode escape sequence\",\"\\\"\\\\u{12\\\" => SyntaxError: Invalid Unicode escape sequence\",\"\\\"\\\\u{FFFFFFFFFFFF}\\\" => SyntaxError: Undefined Unicode code-point\",\"\\\"\\\\u{1g}\\\" => SyntaxError: Invalid Unicode escape sequence\",\"\\\"\\\\x4\\\" => SyntaxError: Invalid hexadecimal escape sequence\",\"\\\"\\\\u12\\\" => SyntaxError: Invalid Unicode escape sequence\",\"\\\"\\\\u\\\" => SyntaxError: Invalid Unicode escape sequence\",\"\\\"\\\\x4\\\\\\\" => SyntaxError: Invalid hexadecimal escape sequence\",\"\\\"a\\\\u{110000}\\n\\\" => SyntaxError: Undefined Unicode code-point\",\"\\\"unterminated => SyntaxError: Invalid or unexpected token\",\"\\\"\\\\ => SyntaxError: Invalid or unexpected token\",\"\\\"use strict\\\"; \\\"\\\\01\\\" => SyntaxError: Octal escape sequences are not allowed in strict mode.\",\"\\\"use strict\\\"; \\\"\\\\8\\\" => SyntaxError: \\\\8 and \\\\9 are not allowed in strict mode.\",\"\\\"use strict\\\"; \\\"\\\\9\\\\1\\\" => SyntaxError: Octal escape sequences are not allowed in strict mode.\",\"\\\"use strict\\\"; ({\\\"\\\\02\\\": 1}) => SyntaxError: Octal escape sequences are not allowed in strict mode.\",\"(function () { \\\"use strict\\\"; return \\\"\\\\7\\\"; })() => SyntaxError: Octal escape sequences are not allowed in strict mode.\",\"`\\\\01` => SyntaxError: Octal escape sequences are not allowed in template strings.\",\"`\\\\8` => SyntaxError: \\\\8 and \\\\9 are not allowed in template strings.\",\"`\\\\1\\\\8` => SyntaxError: Octal escape sequences are not allowed in template strings.\",\"`\\\\x4` => SyntaxError: Invalid hexadecimal escape sequence\",\"`\\\\u{110000}` => SyntaxError: Undefined Unicode code-point\",\"`\\\\u006141\\\\01\\\\` => SyntaxError: Octal escape sequences are not allowed in template strings.\",\"`a${1}\\\\u{}` => SyntaxError: Invalid Unicode escape sequence\",\"a\\\\u{110000} => SyntaxError: Undefined Unicode code-point\",\"var \\\\u0030abc => SyntaxError: Invalid or unexpected token\",\"v\\\\u0061r x = 1 => SyntaxError: Keyword must not contain escaped characters\",\"var \\\\u{10FFFF} => SyntaxError: Invalid or unexpected token\",\"var x\\\\u{1F600}\\\\u{110000} => SyntaxError: Undefined Unicode code-point\",\"var \\\\u{10FFFF}\\\\u12 => SyntaxError: Invalid Unicode escape sequence\",\"var x\\\\u005c => SyntaxError: Invalid or unexpected token\",\"x\\\\ => SyntaxError: Invalid or unexpected token\",\"\\\\u0xyz => SyntaxError: Invalid Unicode escape sequence\",\"var _\\\\n\\\\u8 => SyntaxError: Invalid Unicode escape sequence\"]"), t)
}
