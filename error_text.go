package goja

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/parser"
	"github.com/dop251/goja/token"
	"github.com/dop251/goja/unistring"
)

// Error texts in V8's wording. Node.js surfaces them to users unchanged,
// so scripts written for it see the same messages.

// noSideEffectsToString renders v as V8 renders the arguments of its error
// messages (Object::NoSideEffectsToString): primitives as String would,
// symbols as Symbol(description), errors as "Name: message", objects whose
// toString is Object.prototype.toString as #<Constructor name>, functions
// as their source, and other objects as [object Class]. It reads only data
// properties, so it runs no script.
func (r *Runtime) noSideEffectsToString(v Value) string {
	switch v := v.(type) {
	case nil:
		return "undefined"
	case *Symbol:
		return v.descriptiveString().String()
	case *Object:
		return r.objectNoSideEffectsToString(v)
	}
	return v.String()
}

func (r *Runtime) objectNoSideEffectsToString(o *Object) string {
	if _, ok := o.self.(*proxyObject); ok {
		return "#<Object>"
	}
	if f, ok := o.self.(funcObjectImpl); ok {
		return f.source().String()
	}
	if o.self.className() == classError {
		name := dataPropertyString(o, "name", "Error")
		msg := dataPropertyString(o, "message", "")
		switch {
		case name == "":
			return msg
		case msg == "":
			return name
		}
		return name + ": " + msg
	}
	if ts, ok := dataProperty(o, "toString").(*Object); ok && ts == r.objectProtoToString() {
		if ctor, ok := dataProperty(o, "constructor").(*Object); ok {
			if _, ok := ctor.self.assertCallable(); ok {
				if name, ok := dataProperty(ctor, "name").(String); ok && name.Length() > 0 {
					return "#<" + name.String() + ">"
				}
			}
		}
	}
	return "[object " + o.self.className() + "]"
}

// objectProtoToString is Object.prototype.toString as the runtime created
// it.
func (r *Runtime) objectProtoToString() *Object {
	if f, ok := dataProperty(r.global.ObjectPrototype, "toString").(*Object); ok {
		return f
	}
	return nil
}

// dataProperty looks name up on o and its prototypes, as a data property:
// an accessor, or a proxy on the way, ends the lookup with nil.
func dataProperty(o *Object, name unistring.String) Value {
	for depth := 0; o != nil && depth < 1000; depth++ {
		if _, ok := o.self.(*proxyObject); ok {
			return nil
		}
		switch p := o.self.getOwnPropStr(name).(type) {
		case nil:
		case *valueProperty:
			if p.accessor {
				return nil
			}
			return p.value
		default:
			return p
		}
		o = o.self.proto()
	}
	return nil
}

func dataPropertyString(o *Object, name unistring.String, def string) string {
	switch v := dataProperty(o, name).(type) {
	case nil, valueUndefined:
		return def
	case String:
		return v.String()
	case *Object, *Symbol:
		return def
	default:
		return v.String()
	}
}

// defaultCallSite renders v as V8 does when it has no expression to name
// (BuildDefaultCallSite): its type, and its value if it is a primitive other
// than a symbol, as in "number 5 is not iterable".
func defaultCallSite(v Value) string {
	switch v := v.(type) {
	case nil, valueUndefined:
		return "undefined"
	case valueNull:
		return "object null"
	case valueBool:
		return "boolean " + v.String()
	case valueInt, valueFloat:
		return "number " + v.String()
	case String:
		return `string "` + v.String() + `"`
	case *Symbol:
		return "symbol"
	case *valueBigInt:
		return "bigint"
	case *Object:
		return v.self.typeOf().String()
	}
	return v.String()
}

func nullishName(v Value) string {
	if v == _null {
		return "null"
	}
	return "undefined"
}

// readNullishError is V8's error for reading a property of null or
// undefined.
func (r *Runtime) readNullishError(base Value, key Value) *Object {
	return r.newTypeErrorString("Cannot read properties of " + nullishName(base) + " (reading '" + r.noSideEffectsToString(key) + "')")
}

// setNullishError is V8's error for setting a property of null or
// undefined.
func (r *Runtime) setNullishError(base Value, key Value) *Object {
	return r.newTypeErrorString("Cannot set properties of " + nullishName(base) + " (setting '" + r.noSideEffectsToString(key) + "')")
}

// newTypeErrorString is a TypeError with msg as its message, which is not
// a format.
func (r *Runtime) newTypeErrorString(msg string) *Object {
	return r.builtin_new(r.getTypeError(), []Value{newStringValue(msg)})
}

// notAFunctionError is the TypeError for calling v, a value that is not a
// function: V8 names the callee as written if the call site is known,
// else by its type and value.
func (vm *vm) notAFunctionError(v Value) *Object {
	return vm.r.newTypeErrorString(vm.callSiteName(v) + " is not a function")
}

func (vm *vm) notAConstructorError(v Value) *Object {
	return vm.r.newTypeErrorString(vm.callSiteName(v) + " is not a constructor")
}

// callSiteName prints the expression recorded for the current instruction,
// or, if there is none, v as V8's default call site.
func (vm *vm) callSiteName(v Value) string {
	if e := vm.callSiteExpr(); e != nil {
		if s := printCallSite(e); s != "" {
			return s
		}
	}
	return defaultCallSite(v)
}

// callSiteExpr parses the expression recorded for the current instruction.
func (vm *vm) callSiteExpr() ast.Expression {
	if vm.prg == nil || vm.prg.src == nil {
		return nil
	}
	start, end, ok := vm.prg.exprRange(vm.pc)
	if !ok {
		return nil
	}
	return parseCallSiteAt(vm.prg.src.Source(), start, end)
}

// parseCallSiteAt parses the expression at src[start:end], at most once. A
// node's range leaves out the parentheses its first or last operand is
// written in, as in (function(){})() or a + (b), so as many as the source
// lacks are added around it. An expression longer than
// maxCallSiteSource is not parsed: the error names the value instead.
func parseCallSiteAt(src string, start, end int) ast.Expression {
	if end-start > maxCallSiteSource {
		return nil
	}
	text := src[start:end]
	open, closing, ok := parenBalance(text)
	if !ok {
		return nil
	}
	if open > 0 || closing > 0 {
		text = strings.Repeat("(", open) + text + strings.Repeat(")", closing)
	}
	return parseCallSite(text)
}

// maxCallSiteSource bounds the source an error parses to name an
// expression: V8 prints expressions of any length, but a throw must not
// cost a parse of a megabyte.
const maxCallSiteSource = 16 << 10

// parenBalance returns how many opening parentheses src lacks before its
// first unmatched closing one, and how many closing ones it lacks at its
// end. It skips strings, templates, comments and (by the usual heuristic)
// regular expressions; ok is false if it cannot tell.
func parenBalance(src string) (open, closing int, ok bool) {
	depth, minDepth := 0, 0
	// prev is the last significant character, which tells a regular
	// expression from a division.
	prev := byte(0)
	braces := []int{} // brace depths of the template substitutions open
	braceDepth := 0
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch c {
		case ' ', '\t', '\n', '\r':
			continue
		case '(':
			depth++
		case ')':
			depth--
			if depth < minDepth {
				minDepth = depth
			}
		case '{':
			braceDepth++
		case '}':
			if len(braces) > 0 && braces[len(braces)-1] == braceDepth {
				// The end of a substitution: the template goes on.
				braces = braces[:len(braces)-1]
				j, subst, ok := skipTemplate(src, i+1)
				if !ok {
					return 0, 0, false
				}
				if subst {
					braces = append(braces, braceDepth)
				}
				i = j
				prev = '`'
				continue
			}
			braceDepth--
		case '"', '\'':
			j := i + 1
			for ; j < len(src) && src[j] != c; j++ {
				if src[j] == '\\' {
					j++
				}
			}
			if j >= len(src) {
				return 0, 0, false
			}
			i = j
		case '`':
			j, subst, ok := skipTemplate(src, i+1)
			if !ok {
				return 0, 0, false
			}
			if subst {
				braces = append(braces, braceDepth)
			}
			i = j
			prev = '`'
			continue
		case '/':
			if i+1 < len(src) && src[i+1] == '/' {
				for i < len(src) && src[i] != '\n' {
					i++
				}
				continue
			}
			if i+1 < len(src) && src[i+1] == '*' {
				j := strings.Index(src[i+2:], "*/")
				if j < 0 {
					return 0, 0, false
				}
				i += j + 3
				continue
			}
			if prev == 0 || strings.IndexByte("(,=:[!&|?{};+-*%<>~^", prev) >= 0 {
				j, ok := skipRegExp(src, i+1)
				if !ok {
					return 0, 0, false
				}
				i = j
				c = 'r'
			}
		}
		prev = c
	}
	if len(braces) > 0 {
		return 0, 0, false
	}
	open = -minDepth
	return open, depth + open, true
}

// skipTemplate skips the characters of a template from i, just past its
// opening backtick or the closing brace of a substitution, and returns the
// index of the backtick that ends it or of the brace of the substitution
// that interrupts it (subst).
func skipTemplate(src string, i int) (int, bool, bool) {
	for ; i < len(src); i++ {
		switch src[i] {
		case '\\':
			i++
		case '`':
			return i, false, true
		case '$':
			if i+1 < len(src) && src[i+1] == '{' {
				return i + 1, true, true
			}
		}
	}
	return 0, false, false
}

// skipRegExp skips a regular expression's body from i, just past its
// opening slash, and returns the index of its closing slash.
func skipRegExp(src string, i int) (int, bool) {
	inClass := false
	for ; i < len(src); i++ {
		switch src[i] {
		case '\\':
			i++
		case '[':
			inClass = true
		case ']':
			inClass = false
		case '/':
			if !inClass {
				return i, true
			}
		case '\n':
			return 0, false
		}
	}
	return 0, false
}

// toSetBase is v as the object a property key is set on, or V8's error for
// setting a property of null or undefined.
func (vm *vm) toSetBase(v, key Value) *Object {
	if o, ok := v.(*Object); ok {
		return o
	}
	switch v.(type) {
	case valueUndefined, valueNull:
		panic(vm.r.setNullishError(v, key))
	}
	return v.ToObject(vm.r)
}

func (vm *vm) toSetBaseName(v Value, name unistring.String) *Object {
	if o, ok := v.(*Object); ok {
		return o
	}
	switch v.(type) {
	case valueUndefined, valueNull:
		panic(vm.r.setNullishError(v, stringValueFromRaw(name)))
	}
	return v.ToObject(vm.r)
}

// toRefBase is v as the object of a reference that is read before it is
// written (o.x += 1, o.x++), or V8's error for reading a property of null
// or undefined.
func (vm *vm) toRefBase(v, key Value) *Object {
	if o, ok := v.(*Object); ok {
		return o
	}
	switch v.(type) {
	case valueUndefined, valueNull:
		panic(vm.r.readNullishError(v, key))
	}
	return v.ToObject(vm.r)
}

func (vm *vm) toRefBaseName(v Value, name unistring.String) *Object {
	if o, ok := v.(*Object); ok {
		return o
	}
	switch v.(type) {
	case valueUndefined, valueNull:
		panic(vm.r.readNullishError(v, stringValueFromRaw(name)))
	}
	return v.ToObject(vm.r)
}

// iterSite is where the VM gets an iterator, for V8's error if the value
// is not iterable.
type iterSite uint8

const (
	iterSiteForOf       iterSite = iota // for-of, and spread into an array
	iterSiteDestructure                 // an array pattern
	iterSiteCallSpread                  // spread into arguments
)

// getIterator is GetIterator(v) for the VM's own operations.
func (vm *vm) getIterator(v Value, site iterSite) *iteratorRecord {
	method := toCallableOrNil(vm.r.iteratorMethod(v))
	if method == nil {
		panic(vm.notIterableError(v, site))
	}
	return vm.r.iteratorFrom(v, method)
}

func (vm *vm) notIterableError(v Value, site iterSite) *Object {
	e := vm.callSiteExpr()
	name := func() string {
		if e != nil {
			if s := printCallSite(e); s != "" {
				return s
			}
		}
		return defaultCallSite(v)
	}
	switch site {
	case iterSiteCallSpread:
		switch v.(type) {
		case nil, valueUndefined, valueNull:
			return vm.r.newTypeErrorString(name() + " is not iterable (cannot read property " + nullishName(v) + ")")
		}
		return vm.r.newTypeErrorString("Found non-callable @@iterator")
	case iterSiteDestructure:
		// V8 names only some initializers of a pattern.
		switch e.(type) {
		case *ast.Identifier, *ast.NullLiteral, *ast.BooleanLiteral, *ast.NumberLiteral, *ast.StringLiteral,
			*ast.ObjectLiteral, *ast.ArrayLiteral, *ast.FunctionLiteral, *ast.ArrowFunctionLiteral, *ast.ClassLiteral,
			*ast.CallExpression, *ast.NewExpression:
		default:
			return vm.r.newTypeErrorString(defaultCallSite(v) + " is not iterable (cannot read property Symbol(Symbol.iterator))")
		}
	}
	switch e := e.(type) {
	case *ast.CallExpression:
		return vm.r.newTypeErrorString(printCallSite(e.Callee) + " is not a function or its return value is not iterable")
	case *ast.NewExpression:
		return vm.r.newTypeErrorString(printCallSite(e.Callee) + " is not a function or its return value is not iterable")
	}
	return vm.r.newTypeErrorString(name() + " is not iterable")
}

// destructureError is V8's error for destructuring null or undefined with
// an object pattern. A nested pattern, or one whose first property has a
// default, gets the error for reading that property instead.
func (vm *vm) destructureError(v Value, d *destructCheck) *Object {
	what := nullishName(v)
	if d.hasFirstKey && (d.nested || d.firstDefault) {
		return vm.r.readNullishError(v, newStringValue(d.firstKey))
	}
	name := what
	if e := vm.callSiteExpr(); e != nil {
		if s := printCallSite(e); s != "" {
			name = s
		}
	}
	if d.hasFirstKey {
		return vm.r.newTypeErrorString("Cannot destructure property '" + d.firstKey + "' of '" + name + "' as it is " + what + ".")
	}
	return vm.r.newTypeErrorString("Cannot destructure '" + name + "' as it is " + what + ".")
}

// typeOfString is typeof v.
func typeOfString(v Value) string {
	switch v := v.(type) {
	case nil, valueUndefined:
		return "undefined"
	case valueNull:
		return "object"
	case valueBool:
		return "boolean"
	case valueInt, valueFloat:
		return "number"
	case String:
		return "string"
	case *Symbol:
		return "symbol"
	case *valueBigInt:
		return "bigint"
	case *Object:
		return v.self.typeOf().String()
	}
	return "object"
}

// readOnlyError is V8's error for assigning, in strict mode, to the
// property key of receiver that prop describes: read only, or an accessor
// without a setter.
func (r *Runtime) readOnlyError(prop *valueProperty, key, receiver Value) *Object {
	if prop != nil && prop.accessor {
		return r.newTypeErrorString("Cannot set property " + r.noSideEffectsToString(key) + " of " + r.noSideEffectsToString(receiver) + " which has only a getter")
	}
	return r.newTypeErrorString("Cannot assign to read only property '" + r.noSideEffectsToString(key) + "' of " + typeOfString(receiver) + " '" + r.noSideEffectsToString(receiver) + "'")
}

// cannotCreatePropertyError is V8's error for creating, in strict mode, the
// property key on receiver, a primitive.
func (r *Runtime) cannotCreatePropertyError(key, receiver Value) *Object {
	return r.newTypeErrorString("Cannot create property '" + r.noSideEffectsToString(key) + "' on " + typeOfString(receiver) + " '" + r.noSideEffectsToString(receiver) + "'")
}

const errMsgDerivedThis = "Must call super constructor in derived class before accessing 'this' or returning from derived constructor"

// accessBeforeInitError is the ReferenceError for reading or writing a
// let, const or class binding before its declaration has run. V8 names the
// binding; it is the identifier the current instruction's source position
// is at, when that is one.
func (vm *vm) accessBeforeInitError() Value {
	if vm.prg != nil && vm.prg.src != nil {
		if name := identifierAt(vm.prg.src.Source(), vm.prg.sourceOffset(vm.pc)); name != "" {
			return vm.r.newError(vm.r.getReferenceError(), "Cannot access '"+name+"' before initialization")
		}
	}
	return vm.r.newError(vm.r.getReferenceError(), string(errAccessBeforeInit))
}

// identifierAt returns the identifier that starts at src[offset], if it is
// not a keyword and is not a property name.
func identifierAt(src string, offset int) string {
	if offset < 0 || offset >= len(src) {
		return ""
	}
	if offset > 0 {
		if prev, _ := utf8.DecodeLastRuneInString(src[:offset]); prev == '.' || prev == '#' || isIdentPart(prev) {
			return ""
		}
	}
	end := offset
	for end < len(src) {
		r, size := utf8.DecodeRuneInString(src[end:])
		if !isIdentPart(r) {
			break
		}
		end += size
	}
	name := src[offset:end]
	if !parser.IsIdentifier(name) {
		return ""
	}
	if _, isKeyword := token.IsKeyword(name); isKeyword {
		return ""
	}
	return name
}

func isIdentPart(r rune) bool {
	return r == '$' || r == '_' || 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9' ||
		r >= utf8.RuneSelf && (unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) || unicode.Is(unicode.Pc, r))
}

// toEntryObject is item, an entry of a Map, WeakMap or Object.fromEntries
// iterable, as an object.
func (r *Runtime) toEntryObject(item Value) *Object {
	if o, ok := item.(*Object); ok {
		return o
	}
	panic(r.newTypeErrorString("Iterator value " + r.noSideEffectsToString(item) + " is not an entry object"))
}

// applyNonFunctionError is V8's error for Function.prototype.apply called
// on v, which is not a function.
func (r *Runtime) applyNonFunctionError(v Value) *Object {
	return r.newTypeErrorString("Function.prototype.apply was called on " + r.noSideEffectsToString(v) + ", which is a " + typeOfString(v) + " and not a function")
}
