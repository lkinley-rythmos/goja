package goja

import (
	"strings"
	"testing"

	"github.com/dop251/goja/parser"
)

// FuzzCompile parses arbitrary sources, as a script and as the source of a
// direct eval in a class body, and compiles them, sloppy and strict: none
// may panic or report a compiler bug, nor may writing a syntax error in
// V8's words. Its seeds run with the other tests.
func FuzzCompile(f *testing.F) {
	for _, s := range []string{
		`"\u{10FFFF}"`, `"\u{0010FFFF}"`, "`\\u{10FFFF}${a}\\x41`", `var \u{61} = 1; a`, `'use strict'; "\01\9"; 010`,
		`/(?<=a)[\u0080-￿]\1(b)/giu`, `/[/]/y.test("/")`, `/x/in`, `/\c/`, `/[\c_]/`, `/a{/`, `/(?<a>.)\k<a>/`,
		`class C { #x; static #y() {} m() { return #x in this && this.#x } }`, `var a = #x`, `class A { #a; m() { class B { #b; f() { return this.#a } } } }`,
		`/* abc`, `a => { 'use strict'; return "\08" }`, `function f() { function g() { 'use strict'; '\9' } '\01' }`,
		`f(...a, ...b)`, "tag`\\u{` + x", `({ #x: 1 })`, `super.#x`, `async function* g() { yield* await x }`,
		`label: for (;;) { break label }`, `(a, b) => ({...a, [b]: 1n})`, `x ??= y?.z?.[0]`, `/[\p{L}--\p{Lu}]/v`,
		"'\\\r\n'", `" "`, `0x_1`, `1_000n`, `08.5`, `.5e-3`, `import.meta`, `new.target`,
		`super(a b)`, `({ m() { return super.x } })`, `class A extends B { constructor() { super() } }`,
		`function f() { return new.target }`, `"use strict"; let 1 = 2`, `for (async of x);`, `for (1 of a);`,
		`1nx`, `0b2_`, `1._5`, `1.5n`, `/(?:(a)|b)+/d.exec("ab")`, `/[^\w]*?[ab]{1,2}b/`, `/[a\Dx]/g`,
	} {
		f.Add(s, false)
		f.Add(s, true)
	}
	f.Fuzz(func(t *testing.T, src string, strict bool) {
		_, _ = parser.ParseFile(nil, "", src, 0)
		_, _ = parser.ParseFile(nil, "", src, 0, parser.WithPrivateNamesInScope)
		_, err := Compile("", src, strict)
		if err != nil && strings.Contains(err.Error(), "Compiler bug") {
			t.Fatalf("%q: %v", src, err)
		}
		if cerr, ok := err.(*CompilerSyntaxError); ok {
			_ = cerr.V8Message()
		}
	})
}
