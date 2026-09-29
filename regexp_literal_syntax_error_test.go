package goja

import "testing"

// A regular expression literal's SyntaxError is V8's: a literal left open,
// even in a class ("/[/"), is "missing /"; its flags are the identifier
// characters that follow it ("/x/in"), and invalid ones are "Invalid regular
// expression flags", before any error in its pattern; an error in its
// pattern has the prefix "Invalid regular expression: /<pattern>/: ", with
// or without u, and is reported when the literal is parsed, before an error
// after it. Node.js 18.20.3 recorded the messages.
func TestRegExpLiteralSyntaxErrorNode18(t *testing.T) {
	r := New()
	v, err := r.RunString(`({
		ev(src) { try { return "ok " + String((0, eval)(src)); } catch (e) { return e.name + ": " + e.message; } },
		fn(src) { try { new Function("return " + src); return "ok"; } catch (e) { return e.name + ": " + e.message; } },
		ctor(flags) { try { return "ok " + String(new RegExp("x", flags)); } catch (e) { return e.name + ": " + e.message; } },
	})`)
	if err != nil {
		t.Fatal(err)
	}
	o := v.ToObject(r)
	check := func(how string, cases []struct{ src, want string }) {
		fn, _ := AssertFunction(o.Get(how))
		for _, tc := range cases {
			res, err := fn(o, r.ToValue(tc.src))
			if err != nil {
				t.Fatal(err)
			}
			if got := res.String(); got != tc.want {
				t.Errorf("%s %q: %q, want %q", how, tc.src, got, tc.want)
			}
		}
	}
	check("ev", []struct{ src, want string }{
		{"/[/", "SyntaxError: Invalid regular expression: missing /"},
		{"/[/\n]/", "SyntaxError: Invalid regular expression: missing /"},
		{"/[", "SyntaxError: Invalid regular expression: missing /"},
		{"/[]/", "ok /[]/"},
		{"/]/", "ok /]/"},
		{"/[\\/", "SyntaxError: Invalid regular expression: missing /"},
		{"/a[/", "SyntaxError: Invalid regular expression: missing /"},
		{"/(/gg", "SyntaxError: Invalid regular expression flags"},
		{"/x/in", "SyntaxError: Invalid regular expression flags"},
		{"/x/\\u0067", "SyntaxError: Invalid regular expression flags"},
		{"/x/gim", "ok /x/gim"},
		{"/x/v", "SyntaxError: Invalid regular expression flags"},
		{"/x/uu", "SyntaxError: Invalid regular expression flags"},
		{"/x/ gg", "SyntaxError: Unexpected identifier"},
		{"/x/g.source", "ok x"},
		{"/x/gi.flags", "ok gi"},
		{"/x/y1", "SyntaxError: Invalid regular expression flags"},
		{"/x/g$", "SyntaxError: Invalid regular expression flags"},
		{"/x/g\u00e9", "SyntaxError: Invalid regular expression flags"},
		{"/x/instanceof RegExp", "SyntaxError: Invalid regular expression flags"},
		{"/(/ instanceof RegExp", "SyntaxError: Invalid regular expression: /(/: Unterminated group"},
		{"/x/iu\\u0067", "SyntaxError: Invalid regular expression flags"},
		{"/x/_", "SyntaxError: Invalid regular expression flags"},
		{"/[/ + 1", "SyntaxError: Invalid regular expression: missing /"},
		{"/[a/", "SyntaxError: Invalid regular expression: missing /"},
		{"/[a", "SyntaxError: Invalid regular expression: missing /"},
		{"/a\\", "SyntaxError: Invalid regular expression: missing /"},
		{"/a\\\n/", "SyntaxError: Invalid regular expression: missing /"},
		{"/x/gg; )", "SyntaxError: Invalid regular expression flags"},
		{"a = /(/", "SyntaxError: Invalid regular expression: /(/: Unterminated group"},
		{"x = /[/]/g.test('/')", "ok true"},
		{"/\\u{110000}/uu", "SyntaxError: Invalid regular expression flags"},
		{"/(?<a>)(?<a>)/uu", "SyntaxError: Invalid regular expression flags"},
		{"/x/Ii", "SyntaxError: Invalid regular expression flags"},
		{"/x/G", "SyntaxError: Invalid regular expression flags"},
		{"/(/", "SyntaxError: Invalid regular expression: /(/: Unterminated group"},
		{"/(/u", "SyntaxError: Invalid regular expression: /(/: Unterminated group"},
		{"/)/", "SyntaxError: Invalid regular expression: /)/: Unmatched ')'"},
		{"/a**/", "SyntaxError: Invalid regular expression: /a**/: Nothing to repeat"},
		{"/\\p{Foo}/u", "SyntaxError: Invalid regular expression: /\\p{Foo}/: Invalid property name"},
		{"/(?<a>x)(?<a>y)/", "SyntaxError: Invalid regular expression: /(?<a>x)(?<a>y)/: Duplicate capture group name"},
		{"/a{2,1}/", "SyntaxError: Invalid regular expression: /a{2,1}/: numbers out of order in {} quantifier"},
		{"/x/gg", "SyntaxError: Invalid regular expression flags"},
		{"/x/z", "SyntaxError: Invalid regular expression flags"},
		{"/\\k<b>(?<a>x)/", "SyntaxError: Invalid regular expression: /\\k<b>(?<a>x)/: Invalid named capture referenced"},
		{"/[b-a]/", "SyntaxError: Invalid regular expression: /[b-a]/: Range out of order in character class"},
		{"/(?<=a)*/", "SyntaxError: Invalid regular expression: /(?<=a)*/: Invalid quantifier"},
		{"/\\u{110000}/u", "SyntaxError: Invalid regular expression: /\\u{110000}/: Invalid Unicode escape"},
		{"/{1}/u", "SyntaxError: Invalid regular expression: /{1}/: Nothing to repeat"},
		{"/\\/", "SyntaxError: Invalid regular expression: missing /"},
		{"/a/uu", "SyntaxError: Invalid regular expression flags"},
		{"/\\1/u", "SyntaxError: Invalid regular expression: /\\1/: Invalid escape"},
		{"/(?)/", "SyntaxError: Invalid regular expression: /(?)/: Invalid group"},
		{"/[\\d-z]/u", "SyntaxError: Invalid regular expression: /[\\d-z]/: Invalid character class"},
		{"/(/; )", "SyntaxError: Invalid regular expression: /(/: Unterminated group"},
		{"/(/u; )", "SyntaxError: Invalid regular expression: /(/: Unterminated group"},
		{"/(?<=/+\\k<a>/imsu", "SyntaxError: Invalid regular expression: /(?<=/: Unterminated group"},
		{"/[😀-😁]/; )", "SyntaxError: Invalid regular expression: /[😀-😁]/: Range out of order in character class"},
		{"/[😀-😁]/u; )", "SyntaxError: Unexpected token ')'"},
		{"/[\\ud83d\\ude00-\\ud83d\\ude01]/u; )", "SyntaxError: Unexpected token ')'"},
		{"/[\\ud83d\\ude01-\\ud83d\\ude00]/u; )", "SyntaxError: Invalid regular expression: /[\\ud83d\\ude01-\\ud83d\\ude00]/: Range out of order in character class"},
		{"/\\ud83d\\ude00{/u; )", "SyntaxError: Invalid regular expression: /\\ud83d\\ude00{/: Incomplete quantifier"},
		{"/(/d", "SyntaxError: Invalid regular expression: /(/: Unterminated group"},
		{"/(/dg; )", "SyntaxError: Invalid regular expression: /(/: Unterminated group"},
		{"/a{2,1}/; )", "SyntaxError: Invalid regular expression: /a{2,1}/: numbers out of order in {} quantifier"},
		{"/\\p{Foo}/u; )", "SyntaxError: Invalid regular expression: /\\p{Foo}/: Invalid property name"},
		{"/\\p{Foo}/; )", "SyntaxError: Unexpected token ')'"},
		{"function f() { return /)/ } )", "SyntaxError: Invalid regular expression: /)/: Unmatched ')'"},
		{"/(?<a>)\\k<b>/; )", "SyntaxError: Invalid regular expression: /(?<a>)\\k<b>/: Invalid named capture referenced"},
		{"/\\k<b>/; )", "SyntaxError: Unexpected token ')'"},
		{"x = /[/; )", "SyntaxError: Invalid regular expression: missing /"},
		{"/x/; )", "SyntaxError: Unexpected token ')'"},
		{"/(?:x{200000}){20}/; )", "SyntaxError: Unexpected token ')'"},
		{"/(?<a>)(?<a>)/; )", "SyntaxError: Invalid regular expression: /(?<a>)(?<a>)/: Duplicate capture group name"},
		{"/(/gg; )", "SyntaxError: Invalid regular expression flags"},
	})
	check("fn", []struct{ src, want string }{
		{"/[/", "SyntaxError: Invalid regular expression: missing /"},
		{"/[/\n]/", "SyntaxError: Invalid regular expression: missing /"},
		{"/[", "SyntaxError: Invalid regular expression: missing /"},
		{"/[]/", "ok"},
		{"/]/", "ok"},
		{"/[\\/", "SyntaxError: Invalid regular expression: missing /"},
		{"/a[/", "SyntaxError: Invalid regular expression: missing /"},
		{"/(/gg", "SyntaxError: Invalid regular expression flags"},
		{"/x/in", "SyntaxError: Invalid regular expression flags"},
		{"/x/\\u0067", "SyntaxError: Invalid regular expression flags"},
		{"/x/gim", "ok"},
		{"/x/v", "SyntaxError: Invalid regular expression flags"},
		{"/x/uu", "SyntaxError: Invalid regular expression flags"},
		{"/x/ gg", "SyntaxError: Unexpected identifier"},
		{"/x/g.source", "ok"},
		{"/x/gi.flags", "ok"},
		{"/x/y1", "SyntaxError: Invalid regular expression flags"},
		{"/x/g$", "SyntaxError: Invalid regular expression flags"},
		{"/x/g\u00e9", "SyntaxError: Invalid regular expression flags"},
		{"/x/instanceof RegExp", "SyntaxError: Invalid regular expression flags"},
		{"/(/ instanceof RegExp", "SyntaxError: Invalid regular expression: /(/: Unterminated group"},
		{"/x/iu\\u0067", "SyntaxError: Invalid regular expression flags"},
		{"/x/_", "SyntaxError: Invalid regular expression flags"},
		{"/[/ + 1", "SyntaxError: Invalid regular expression: missing /"},
		{"/[a/", "SyntaxError: Invalid regular expression: missing /"},
		{"/[a", "SyntaxError: Invalid regular expression: missing /"},
		{"/a\\", "SyntaxError: Invalid regular expression: missing /"},
		{"/a\\\n/", "SyntaxError: Invalid regular expression: missing /"},
		{"/x/gg; )", "SyntaxError: Invalid regular expression flags"},
		{"a = /(/", "SyntaxError: Invalid regular expression: /(/: Unterminated group"},
		{"x = /[/]/g.test('/')", "ok"},
		{"/\\u{110000}/uu", "SyntaxError: Invalid regular expression flags"},
		{"/(?<a>)(?<a>)/uu", "SyntaxError: Invalid regular expression flags"},
		{"/x/Ii", "SyntaxError: Invalid regular expression flags"},
		{"/x/G", "SyntaxError: Invalid regular expression flags"},
		{"/(/", "SyntaxError: Invalid regular expression: /(/: Unterminated group"},
		{"/(/u", "SyntaxError: Invalid regular expression: /(/: Unterminated group"},
		{"/)/", "SyntaxError: Invalid regular expression: /)/: Unmatched ')'"},
		{"/a**/", "SyntaxError: Invalid regular expression: /a**/: Nothing to repeat"},
		{"/\\p{Foo}/u", "SyntaxError: Invalid regular expression: /\\p{Foo}/: Invalid property name"},
		{"/(?<a>x)(?<a>y)/", "SyntaxError: Invalid regular expression: /(?<a>x)(?<a>y)/: Duplicate capture group name"},
		{"/a{2,1}/", "SyntaxError: Invalid regular expression: /a{2,1}/: numbers out of order in {} quantifier"},
		{"/x/gg", "SyntaxError: Invalid regular expression flags"},
		{"/x/z", "SyntaxError: Invalid regular expression flags"},
		{"/\\k<b>(?<a>x)/", "SyntaxError: Invalid regular expression: /\\k<b>(?<a>x)/: Invalid named capture referenced"},
		{"/[b-a]/", "SyntaxError: Invalid regular expression: /[b-a]/: Range out of order in character class"},
		{"/(?<=a)*/", "SyntaxError: Invalid regular expression: /(?<=a)*/: Invalid quantifier"},
		{"/\\u{110000}/u", "SyntaxError: Invalid regular expression: /\\u{110000}/: Invalid Unicode escape"},
		{"/{1}/u", "SyntaxError: Invalid regular expression: /{1}/: Nothing to repeat"},
		{"/\\/", "SyntaxError: Invalid regular expression: missing /"},
		{"/a/uu", "SyntaxError: Invalid regular expression flags"},
		{"/\\1/u", "SyntaxError: Invalid regular expression: /\\1/: Invalid escape"},
		{"/(?)/", "SyntaxError: Invalid regular expression: /(?)/: Invalid group"},
		{"/[\\d-z]/u", "SyntaxError: Invalid regular expression: /[\\d-z]/: Invalid character class"},
	})
	check("ctor", []struct{ src, want string }{
		{"uu", "SyntaxError: Invalid flags supplied to RegExp constructor 'uu'"},
		{"uiu", "SyntaxError: Invalid flags supplied to RegExp constructor 'uiu'"},
		{"gu", "ok /x/gu"},
		{"u", "ok /x/u"},
		{"iuu", "SyntaxError: Invalid flags supplied to RegExp constructor 'iuu'"},
		{"yy", "SyntaxError: Invalid flags supplied to RegExp constructor 'yy'"},
		{"ss", "SyntaxError: Invalid flags supplied to RegExp constructor 'ss'"},
		{"mm", "SyntaxError: Invalid flags supplied to RegExp constructor 'mm'"},
		{"ii", "SyntaxError: Invalid flags supplied to RegExp constructor 'ii'"},
		{"gg", "SyntaxError: Invalid flags supplied to RegExp constructor 'gg'"},
	})
}
