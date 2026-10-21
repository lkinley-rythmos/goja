package goja

import "github.com/dop251/goja/unistring"

// AppendOwnPropertyKeys appends the object's own property keys, the ones
// Reflect.ownKeys lists and in the same order, without running any
// JavaScript: every string key, hidden (non-enumerable) ones included, to
// names (integer-like keys first in ascending order, then the others in
// creation order), and every symbol key, enumerable or not, to symbols (in
// creation order). Reflect.ownKeys(o) is names followed by symbols.
//
// ok is false, and names and symbols are returned unchanged, for a Proxy,
// as only its ownKeys trap can give its keys, and the trap is never run;
// and for any other object whose keys this package cannot read without
// running JavaScript or reading another object, such as the source an
// object rest destructuring passes a getter as this.
//
// For an ordinary object (an object literal, new Object(), Object.create
// and the like) the call allocates nothing when names and symbols have
// capacity for the keys and every string key is ASCII; a non-ASCII key is
// converted to a Go string, which allocates. Every other object (arrays,
// functions, typed arrays, String and arguments objects, Go-backed and
// dynamic objects) lists its keys as Reflect.ownKeys would, which may
// allocate; a Go-backed or dynamic object's keys come from Go code, never
// from JavaScript.
// This method will panic with an *Exception if a JavaScript exception is thrown in the process. Use Runtime.Try to catch these.
func (o *Object) AppendOwnPropertyKeys(names []string, symbols []*Symbol) (namesOut []string, symbolsOut []*Symbol, ok bool) {
	switch impl := o.self.(type) {
	case *baseObject:
		impl.ensurePropOrder()
		for _, k := range impl.propNames {
			names = append(names, k.String())
		}
		if impl.symValues != nil {
			for e := impl.symValues.iterFirst; e != nil; e = e.iterNext {
				if s, isSym := e.key.(*Symbol); isSym {
					symbols = append(symbols, s)
				}
			}
		}
		return names, symbols, true
	}
	if !ownPropsReadable(o.self) {
		return names, symbols, false
	}
	for _, k := range o.self.stringKeys(true, nil) {
		names = append(names, k.String())
	}
	for _, k := range o.self.symbols(true, nil) {
		if s, isSym := k.(*Symbol); isSym {
			symbols = append(symbols, s)
		}
	}
	return names, symbols, true
}

// GetOwnDataProperty returns the object's own data property name, as
// Object.getOwnPropertyDescriptor describes it: its value and its
// writable, enumerable and configurable attributes. ok is false when the
// object has no own property name, when the property is an accessor, for
// a Proxy, whose own properties only its getOwnPropertyDescriptor trap can
// describe, and for any other object whose properties this package cannot
// read without running JavaScript or reading another object, such as the
// source an object rest destructuring passes a getter as this. It never
// runs JavaScript: no getter, setter or Proxy trap.
//
// For an ordinary object and an ASCII name the lookup allocates nothing.
// Other objects describe the property as Object.getOwnPropertyDescriptor
// would, which may allocate; a Go-backed or dynamic object's property
// comes from Go code, never from JavaScript.
// This method will panic with an *Exception if a JavaScript exception is thrown in the process. Use Runtime.Try to catch these.
func (o *Object) GetOwnDataProperty(name string) (v Value, writable, enumerable, configurable, ok bool) {
	var prop Value
	switch impl := o.self.(type) {
	case *baseObject:
		prop = impl.values[unistring.NewFromString(name)]
	default:
		if !ownPropsReadable(o.self) {
			return nil, false, false, false, false
		}
		prop = o.self.getOwnPropStr(unistring.NewFromString(name))
	}
	return ownDataProperty(prop)
}

// GetOwnDataPropertySymbol is GetOwnDataProperty for a symbol-keyed
// property: it returns the object's own data property sym, as
// Object.getOwnPropertyDescriptor describes it. ok is false when the
// object has no own property sym (an inherited one does not count), when
// the property is an accessor, and for a Proxy and any other object
// GetOwnDataProperty cannot read. It never runs JavaScript: no getter,
// setter or Proxy trap.
//
// For an ordinary object the lookup allocates nothing, and neither does
// it for other objects that keep their symbol properties as ordinary ones
// do (arrays, functions, typed arrays and the like). A built-in object
// (a prototype, a constructor, Math and the like) may build its symbol
// properties in Go on its first lookup, which allocates once.
// This method will panic with an *Exception if a JavaScript exception is thrown in the process. Use Runtime.Try to catch these.
func (o *Object) GetOwnDataPropertySymbol(sym *Symbol) (v Value, writable, enumerable, configurable, ok bool) {
	var prop Value
	switch impl := o.self.(type) {
	case *baseObject:
		if impl.symValues != nil {
			prop = impl.symValues.get(sym)
		}
	default:
		if !ownPropsReadable(o.self) {
			return nil, false, false, false, false
		}
		prop = o.self.getOwnPropSym(sym)
	}
	return ownDataProperty(prop)
}

// ownPropsReadable reports whether impl's own properties and keys can be
// read, with getOwnPropStr, getOwnPropSym, stringKeys and symbols, without
// running JavaScript and without reading another object: the object keeps
// them itself, or Go code gives them (a Go-backed or dynamic object's, or
// a built-in object's that builds them on first use). It lists the
// implementations known to be so, and every other one is not: among them
// a Proxy, whose traps would run, and a destructKeyedSource (the source
// an object rest destructuring passes a getter as this), which reads the
// object it wraps, perhaps a Proxy, and records the keys read from it.
func ownPropsReadable(impl objectImpl) bool {
	switch impl.(type) {
	case *baseObject, *guardedObject, *templatedObject, *templatedFuncObject, *templatedArrayObject,
		*arrayObject, *sparseArrayObject, *taggedTemplateArray, *argumentsObject, *stringObject,
		*primitiveValueObject, *errorObject, *dateObject, *regexpObject, *descriptorObject,
		*mapObject, *setObject, *weakMapObject, *weakSetObject, *Promise,
		*arrayBufferObject, *dataViewObject, *typedArrayObject,
		*arrayIterObject, *mapIterObject, *setIterObject, *stringIterObject, *regExpStringIterObject, *generatorObject,
		*funcObject, *classFuncObject, *methodFuncObject, *arrowFuncObject,
		*generatorFuncObject, *generatorMethodFuncObject, *asyncFuncObject, *asyncMethodFuncObject, *asyncArrowFuncObject,
		*nativeFuncObject, *wrappedFuncObject, *boundFuncObject, *funcProtoCallObject, *funcProtoApplyObject,
		*objectGoReflect, *objectGoMapReflect, *objectGoArrayReflect, *objectGoSliceReflect,
		*objectGoMapSimple, *objectGoSlice, *dynamicObject, *dynamicArray:
		return true
	}
	return false
}

// ownDataProperty describes an own property as GetOwnDataProperty returns
// it: ok is false when prop is nil (no such property) or an accessor.
func ownDataProperty(prop Value) (v Value, writable, enumerable, configurable, ok bool) {
	switch p := prop.(type) {
	case nil:
		return nil, false, false, false, false
	case *valueProperty:
		if p.accessor {
			return nil, false, false, false, false
		}
		if p.value == nil {
			return _undefined, p.writable, p.enumerable, p.configurable, true
		}
		return p.value, p.writable, p.enumerable, p.configurable, true
	default:
		return p, true, true, true, true
	}
}
