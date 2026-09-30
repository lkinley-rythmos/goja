package parser

import (
	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/unistring"
)

type _scope struct {
	outer           *_scope
	allowIn         bool
	allowLet        bool
	inIteration     bool
	inSwitch        bool
	inFuncParams    bool
	inFunction      bool
	inAsync         bool
	allowAwait      bool
	allowYield      bool
	this            thisContext // what the code may use of its function's this
	strict          bool        // strict mode code (which a nested function's is too)
	prologue        bool        // the statements next parsed are a function body's
	declarationList []*ast.VariableDeclaration

	labels []unistring.String
}

// thisContext is what the code of a function may use of its this, which V8
// checks as it parses: new.target in any function but an arrow at the top
// level, super.x in methods (and class field initializers and static
// blocks), and super() in derived class constructors. An arrow function has
// its enclosing code's.
type thisContext uint8

const (
	thisNewTarget thisContext = 1 << iota
	thisSuperProperty
	thisSuperCall

	thisMethod = thisNewTarget | thisSuperProperty
)

// useThis makes the code parsed until the function it returns is called
// have this: a function's parameters, and a class field's initializer.
func (self *_parser) useThis(this thisContext) func() {
	outer := self.scope.this
	self.scope.this = this
	return func() { self.scope.this = outer }
}

func (self *_parser) openScope() {
	self.scope = &_scope{
		outer:   self.scope,
		allowIn: true,
		strict:  self.scope != nil && self.scope.strict,
	}
}

func (self *_parser) closeScope() {
	self.scope = self.scope.outer
}

func (self *_scope) declare(declaration *ast.VariableDeclaration) {
	self.declarationList = append(self.declarationList, declaration)
}

func (self *_scope) hasLabel(name unistring.String) bool {
	for _, label := range self.labels {
		if label == name {
			return true
		}
	}
	if self.outer != nil && !self.inFunction {
		// Crossing a function boundary to look for a label is verboten
		return self.outer.hasLabel(name)
	}
	return false
}
