package goja

import (
	"strings"

	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/parser"
	"github.com/dop251/goja/token"
)

// V8 names the expression an error is about ("o.f is not a function",
// "o.a is not iterable") by printing the expression's syntax tree with its
// CallPrinter: names, member accesses, calls as "(...)", literals, and
// "(intermediate value)" for what it does not print (functions, object
// literal values, conditionals, assignments, new, ...). The compiler records
// where such an expression is in the source (Program.exprMap); when the
// error happens, the expression is parsed again from there and printed the
// same way.

// parseCallSite parses the source of a recorded expression. It returns nil
// if the source does not parse on its own.
func parseCallSite(src string) ast.Expression {
	if src == "" {
		return nil
	}
	if e := parseCallSiteIn("(", src, ")"); e != nil {
		return e
	}
	// super is only valid in a method.
	if strings.Contains(src, "super") {
		if e := parseCallSiteIn("({m(){return (", src, ")}})"); e != nil {
			if obj, ok := e.(*ast.ObjectLiteral); ok && len(obj.Value) == 1 {
				if p, ok := obj.Value[0].(*ast.PropertyKeyed); ok {
					if fn, ok := p.Value.(*ast.FunctionLiteral); ok && fn.Body != nil && len(fn.Body.List) == 1 {
						if ret, ok := fn.Body.List[0].(*ast.ReturnStatement); ok {
							return ret.Argument
						}
					}
				}
			}
		}
	}
	return nil
}

func parseCallSiteIn(prefix, src, suffix string) ast.Expression {
	prg, err := parser.ParseFile(nil, "", prefix+src+suffix, 0, parser.WithDisableSourceMaps)
	if err != nil || len(prg.Body) != 1 {
		return nil
	}
	if st, ok := prg.Body[0].(*ast.ExpressionStatement); ok {
		return st.Expression
	}
	return nil
}

type callPrinter struct {
	sb     strings.Builder
	prints int
}

// printCallSite prints e as V8's CallPrinter prints the expression it
// finds.
func printCallSite(e ast.Expression) string {
	var p callPrinter
	p.find(e, true)
	return p.sb.String()
}

func (p *callPrinter) print(s string) {
	p.prints++
	p.sb.WriteString(s)
}

func (p *callPrinter) find(e ast.Expression, print bool) {
	if print {
		prev := p.prints
		p.visit(e)
		if prev != p.prints {
			return
		}
	}
	p.print("(intermediate value)")
}

func (p *callPrinter) visit(e ast.Expression) {
	switch e := e.(type) {
	case *ast.Identifier:
		p.print(e.Name.String())
	case *ast.ThisExpression:
		p.print("this")
	case *ast.NullLiteral:
		p.print("null")
	case *ast.BooleanLiteral:
		if e.Value {
			p.print("true")
		} else {
			p.print("false")
		}
	case *ast.NumberLiteral:
		if s, ok := numberLiteralText(e); ok {
			p.print(s)
		}
	case *ast.StringLiteral:
		p.print(`"` + e.Value.String() + `"`)
	case *ast.TemplateLiteral:
		if e.Tag != nil {
			p.find(e.Tag, true)
			p.print("(...)")
			return
		}
		if len(e.Expressions) == 0 {
			if len(e.Elements) == 1 {
				p.print(`"` + e.Elements[0].Parsed.String() + `"`)
			}
			return
		}
		// V8 prints what it makes of the substitutions.
		for _, sub := range e.Expressions {
			p.find(sub, true)
		}
	case *ast.RegExpLiteral:
		p.print("/" + e.Pattern + "/" + e.Flags)
	case *ast.ArrayLiteral:
		p.print("[")
		for i, v := range e.Value {
			if i > 0 {
				p.print(",")
			}
			if v != nil {
				p.find(v, true)
			}
		}
		p.print("]")
	case *ast.ObjectLiteral:
		p.print("{")
		for range e.Value {
			p.find(nil, false)
		}
		p.print("}")
	case *ast.SpreadElement:
		p.print("(...")
		p.find(e.Expression, true)
		p.print(")")
	case *ast.DotExpression:
		p.printObject(e.Left)
		p.print(".")
		p.print(e.Identifier.Name.String())
	case *ast.PrivateDotExpression:
		p.printObject(e.Left)
		p.print(".#")
		p.print(e.Identifier.Name.String())
	case *ast.BracketExpression:
		if key, ok := literalKeyText(e.Member); ok {
			p.printObject(e.Left)
			p.print(".")
			p.print(key)
			return
		}
		p.printObject(e.Left)
		if _, ok := e.Left.(*ast.Optional); ok {
			p.print(".")
		}
		p.print("[")
		p.find(e.Member, true)
		p.print("]")
	case *ast.OptionalChain:
		p.find(e.Expression, true)
	case *ast.Optional:
		p.find(e.Expression, true)
	case *ast.CallExpression:
		p.find(e.Callee, true)
		p.print("(...)")
	case *ast.BinaryExpression:
		p.print("(")
		operands := flattenBinary(e, nil)
		for i, o := range operands {
			if i > 0 {
				p.print(" " + e.Operator.String() + " ")
			}
			p.find(o, true)
		}
		p.print(")")
	case *ast.SequenceExpression:
		p.print("(")
		for i, o := range e.Sequence {
			if i > 0 {
				p.print(" , ")
			}
			p.find(o, true)
		}
		p.print(")")
	case *ast.UnaryExpression:
		switch e.Operator {
		case token.INCREMENT, token.DECREMENT:
			p.print("(")
			if !e.Postfix {
				p.print(e.Operator.String())
			}
			p.find(e.Operand, true)
			if e.Postfix {
				p.print(e.Operator.String())
			}
			p.print(")")
		case token.MINUS:
			// V8's parser folds a minus sign into a number literal.
			if n, ok := e.Operand.(*ast.NumberLiteral); ok {
				if s, ok := numberLiteralText(n); ok {
					if s == "0" {
						p.print("0")
					} else {
						p.print("-" + s)
					}
					return
				}
			}
			fallthrough
		default:
			p.print("(" + e.Operator.String())
			switch e.Operator {
			case token.DELETE, token.TYPEOF, token.VOID:
				p.print(" ")
			}
			p.find(e.Operand, true)
			p.print(")")
		}
	case *ast.ConditionalExpression:
		p.find(e.Test, false)
		p.find(e.Consequent, false)
		p.find(e.Alternate, false)
	case *ast.AssignExpression:
		p.find(e.Left, false)
		p.find(e.Right, false)
	case *ast.NewExpression:
		p.find(e.Callee, false)
	case *ast.AwaitExpression:
		p.find(e.Argument, false)
	case *ast.YieldExpression:
		if e.Argument != nil {
			p.find(e.Argument, false)
		}
	}
	// Functions, classes, super, new.target and the like print nothing.
}

// printObject prints the object of a member access, and the "?" of an
// optional one.
func (p *callPrinter) printObject(e ast.Expression) {
	if opt, ok := e.(*ast.Optional); ok {
		p.find(opt.Expression, true)
		p.print("?")
		return
	}
	p.find(e, true)
}

// literalKeyText is the key of a member access written as a string (or a
// template without substitutions), which V8 prints after a dot.
func literalKeyText(e ast.Expression) (string, bool) {
	switch e := e.(type) {
	case *ast.StringLiteral:
		return e.Value.String(), true
	case *ast.TemplateLiteral:
		if e.Tag == nil && len(e.Expressions) == 0 && len(e.Elements) == 1 {
			return e.Elements[0].Parsed.String(), true
		}
	}
	return "", false
}

func numberLiteralText(e *ast.NumberLiteral) (string, bool) {
	switch v := e.Value.(type) {
	case int64:
		return valueInt(v).String(), true
	case float64:
		return floatToValue(v).String(), true
	}
	// V8 prints no BigInt literal.
	return "", false
}

// flattenBinary lists the operands of a chain of the same operator, which
// V8's parser makes one n-ary operation of: a + b + c prints (a + b + c).
func flattenBinary(e *ast.BinaryExpression, acc []ast.Expression) []ast.Expression {
	if l, ok := e.Left.(*ast.BinaryExpression); ok && l.Operator == e.Operator {
		acc = flattenBinary(l, acc)
	} else {
		acc = append(acc, e.Left)
	}
	return append(acc, e.Right)
}
