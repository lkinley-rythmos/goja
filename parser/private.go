package parser

import (
	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/file"
	"github.com/dop251/goja/unistring"
)

// _privateScope is the body of a class being parsed: the private names it
// declares and those referred to in it that it or a class inside it may not
// declare. A reference is resolved when the class body ends, since a private
// name may be used before its declaration.
type _privateScope struct {
	outer      *_privateScope
	declared   map[unistring.String]*privateDecl
	unresolved []privateRef
}

type privateDecl struct {
	static, getter, setter bool
}

type privateRef struct {
	name unistring.String
	idx  file.Idx
}

const errPrivateNotDeclared = "Private field '#%s' must be declared in an enclosing class"

// noEnclosingClass tells whether no class body encloses the current position,
// in which V8 reports a private name when it reads it.
func (self *_parser) noEnclosingClass() bool {
	return self.privateScope == nil && !self.opts.privateNamesInScope
}

// referPrivateName records a reference to a private name ("o.#x", "#x in o"),
// which a class enclosing it must declare.
func (self *_parser) referPrivateName(name unistring.String, idx file.Idx) {
	if self.privateScope == nil {
		if !self.opts.privateNamesInScope {
			self.error(idx, errPrivateNotDeclared, name)
		}
		// Otherwise it may be the enclosing class's; the compiler resolves it.
		return
	}
	self.privateScope.unresolved = append(self.privateScope.unresolved, privateRef{name: name, idx: idx})
}

// unexpectedPrivateName reports a private name where no expression may be one
// ("#x + 1", "(#x)"), as V8 does: first as a private name that must be
// declared if no class encloses it, then as an unexpected token.
func (self *_parser) unexpectedPrivateName(name unistring.String, idx file.Idx) {
	if self.noEnclosingClass() {
		self.error(idx, errPrivateNotDeclared, name)
		return
	}
	self.error(idx, "Unexpected identifier")
}

func (self *_parser) openPrivateScope() {
	self.privateScope = &_privateScope{outer: self.privateScope}
}

// declarePrivateName declares a class element's private name once the element
// is parsed, as V8 does. Only a getter and a setter, both static or both not,
// may share one.
func (self *_parser) declarePrivateName(id *ast.PrivateIdentifier, kind ast.PropertyKind, static bool) {
	ps := self.privateScope
	if d := ps.declared[id.Name]; d != nil {
		if d.static == static {
			switch {
			case kind == ast.PropertyKindGet && d.setter && !d.getter:
				d.getter = true
				return
			case kind == ast.PropertyKindSet && d.getter && !d.setter:
				d.setter = true
				return
			}
		}
		self.error(id.Idx, "Identifier '#%s' has already been declared", id.Name)
		return
	}
	if ps.declared == nil {
		ps.declared = make(map[unistring.String]*privateDecl)
	}
	ps.declared[id.Name] = &privateDecl{
		static: static,
		getter: kind == ast.PropertyKindGet,
		setter: kind == ast.PropertyKindSet,
	}
}

// closePrivateScope ends a class body. A private name it does not declare is
// left to the class enclosing it, and V8 reports the first one no class
// declares when the outermost class ends.
func (self *_parser) closePrivateScope() {
	ps := self.privateScope
	self.privateScope = ps.outer
	for _, ref := range ps.unresolved {
		if _, ok := ps.declared[ref.name]; ok {
			continue
		}
		if outer := ps.outer; outer != nil {
			outer.unresolved = append(outer.unresolved, ref)
			continue
		}
		if !self.opts.privateNamesInScope {
			self.error(ref.idx, errPrivateNotDeclared, ref.name)
			return
		}
	}
}
