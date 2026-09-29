package parser

import (
	"testing"

	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/token"
)

func TestPrivateInOperator(t *testing.T) {
	prg, err := ParseFile(nil, "", "class C { #x; m(o) { return #x in o } }", 0)
	if err != nil {
		t.Fatal(err)
	}
	m := prg.Body[0].(*ast.ClassDeclaration).Class.Body[1].(*ast.MethodDefinition)
	ret := m.Body.Body.List[0].(*ast.ReturnStatement)
	bin, ok := ret.Argument.(*ast.BinaryExpression)
	if !ok {
		t.Fatalf("%T", ret.Argument)
	}
	if bin.Operator != token.IN {
		t.Errorf("operator %v, want in", bin.Operator)
	}
	if _, ok := bin.Left.(*ast.PrivateIdentifier); !ok {
		t.Errorf("left %T", bin.Left)
	}
}

func TestWithPrivateNamesInScope(t *testing.T) {
	// Source that runs inside a class body (a direct eval there) may use the
	// class's private names; the compiler resolves them.
	for _, tc := range []struct {
		src          string
		want, wantIn string
	}{
		{"this.#x", "(anonymous): Line 1:6 Private field '#x' must be declared in an enclosing class", ""},
		{"#x in this", "(anonymous): Line 1:1 Private field '#x' must be declared in an enclosing class", ""},
		{"class D { m() { this.#y } }", "(anonymous): Line 1:22 Private field '#y' must be declared in an enclosing class", ""},
		{"#x", "(anonymous): Line 1:1 Private field '#x' must be declared in an enclosing class", "(anonymous): Line 1:1 Unexpected identifier"},
		{"a + #x", "(anonymous): Line 1:5 Private field '#x' must be declared in an enclosing class", "(anonymous): Line 1:5 Unexpected identifier"},
	} {
		for _, in := range []bool{false, true} {
			var opts []Option
			want := tc.want
			if in {
				opts = append(opts, WithPrivateNamesInScope)
				want = tc.wantIn
			}
			_, err := ParseFile(nil, "", tc.src, 0, opts...)
			got := ""
			if err != nil {
				got = err.Error()
			}
			if got != want {
				t.Errorf("%q (in a class: %v): %q, want %q", tc.src, in, got, want)
			}
		}
	}
}
