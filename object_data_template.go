package goja

import (
	"math"
	"sync/atomic"

	"github.com/dop251/goja/unistring"
)

// ObjectTemplate describes ordinary objects that start with a fixed list of
// own string-keyed data properties (writable, enumerable, configurable, in
// the order they were put), each of whose values is made the first time it
// is needed. Making an object from a template is one allocation, whatever
// the template's size; a property's value is made when something reads,
// writes, redefines or deletes it, or reads every value (JSON.stringify,
// Object.entries, for-in, Export and the like). Listing the keys
// (Object.keys, Reflect.ownKeys) or checking for one ("in",
// hasOwnProperty) makes nothing. It is the built-in objects' own mechanism
// (objectTemplate), made available to embedders for data they hand to
// scripts whole but of which scripts read little.
//
// Such an object is indistinguishable, from JavaScript and through this
// package's API, from one made with NewObject and one DefineDataProperty
// per property in the template's order, provided each property's factory
// is a pure function of the runtime: it must return a value equal to what
// an eager definition would have stored, and must not run JavaScript or
// read or write anything a script can see. A factory runs at most once per
// object, in the object's runtime, on whatever goroutine is running that
// runtime at the time; when several values are made at once, in no
// particular order. A factory that returns nil makes undefined.
//
// The allocation counter (see Runtime.SetAllocationLimit) charges the
// object what it charges NewObject, and a value what making it charges,
// when it is made: once every value has been made, the object has been
// charged what the eager definition is.
//
// A template is filled with Put before its first use, by one goroutine. It
// is immutable once an object has been made from it and may then be shared
// by any number of runtimes and goroutines.
type ObjectTemplate struct {
	t      *objectTemplate
	sealed atomic.Bool
}

// NewObjectTemplate returns an empty template.
func NewObjectTemplate() *ObjectTemplate {
	return &ObjectTemplate{t: newObjectTemplate()}
}

// Put adds the property name, whose value factory makes when it is first
// needed. It reports false, and adds nothing, when factory is nil, when
// the template already has name, when name is an integer index (an
// ordinary object lists those first, which a template's fixed order does
// not model), or once an object has been made from the template.
func (t *ObjectTemplate) Put(name string, factory func(*Runtime) Value) bool {
	if factory == nil || t.sealed.Load() {
		return false
	}
	key := unistring.NewFromString(name)
	if _, exists := t.t.props[key]; exists {
		return false
	}
	if strToArrayIdx(key) != math.MaxUint32 {
		return false
	}
	// A nil value would be taken for a deleted property (see
	// templatedObject.deleteStr).
	t.t.putStr(key, func(r *Runtime) Value {
		if v := factory(r); v != nil {
			return v
		}
		return _undefined
	})
	return true
}

// Len is the number of properties put.
func (t *ObjectTemplate) Len() int { return len(t.t.propNames) }

// dataTemplatedObject is an ordinary object made from an ObjectTemplate: a
// templatedObject whose prototype is Object.prototype from the start and
// whose template has only string-keyed data properties, each writable,
// enumerable and configurable until a definition changes it.
//
// Of the objectImpl methods it inherits from baseObject unchanged, the
// index ones (getIdx, getOwnPropIdx, setOwnIdx, hasPropertyIdx,
// hasOwnPropertyIdx, defineOwnPropertyIdx, deleteIdx), iterateKeys,
// exportType, exportToMap, exportToArrayOrSlice, sortLen, sortGet and swap
// reach the storage only through o.val.self, and className, typeOf,
// isExtensible, preventExtensions, equal, assertCallable, vmCall,
// assertConstructor, hasInstance, getPrivateEnv and _putSym (the template
// has no symbols) do not reach the string-keyed storage. export and
// _putProp read and write it directly, and are overridden here.
type dataTemplatedObject struct {
	templatedObject
}

// dataTemplatedObjectBox co-allocates the impl with its Object, as
// baseObjectBox does; nothing swaps the impl.
type dataTemplatedObjectBox struct {
	dataTemplatedObject
	obj Object
}

// NewObjectFromTemplate returns a new ordinary object with t's properties
// (see ObjectTemplate). extra is how many further properties the caller is
// about to define, a capacity hint only; extra <= 0 is no hint.
func (r *Runtime) NewObjectFromTemplate(t *ObjectTemplate, extra int) *Object {
	if !t.sealed.Load() {
		t.sealed.Store(true)
	}
	// What NewObject charges: the eager definition's properties are not
	// charged either (see SetAllocationLimit).
	r.countAlloc(allocSizeBaseObject)
	b := &dataTemplatedObjectBox{}
	b.obj.runtime = r
	o := &b.dataTemplatedObject
	o.class = classObject
	o.val = &b.obj
	o.extensible = true
	o.prototype = r.global.ObjectPrototype
	o.tmpl = t.t
	o.protoMaterialised = true
	if extra > 0 {
		// The names a later definition appends go after the template's
		// (materialisePropNames), in one slice made once.
		names := make([]unistring.String, len(t.t.propNames), len(t.t.propNames)+extra)
		copy(names, t.t.propNames)
		o.propNames = names
		o.values = make(map[unistring.String]Value, extra)
	}
	b.obj.self = o
	o.init()
	return &b.obj
}

// export makes every value first: baseObject.export lists the keys with
// its own stringKeys and reads values through o.val.self.
func (o *dataTemplatedObject) export(ctx *objectExportCtx) interface{} {
	o.materialiseProps()
	return o.baseObject.export(ctx)
}

// stringKeys lists the keys without making the values: a name listed but
// with no value yet is a template property, enumerable as every template
// property is until a definition, which makes the value, changes it.
func (o *dataTemplatedObject) stringKeys(all bool, keys []Value) []Value {
	o.materialisePropNames()
	return o.baseObject.stringKeys(all, keys)
}

func (o *dataTemplatedObject) keys(all bool, accum []Value) []Value {
	return o.symbols(all, o.stringKeys(all, accum))
}

// _putProp is baseObject._putProp, which writes the storage directly, for
// a template's names: one not yet made is not listed twice, and one
// deleted is listed again, at the end.
func (o *dataTemplatedObject) _putProp(name unistring.String, value Value, writable, enumerable, configurable bool) Value {
	o.materialisePropNames()
	prop := valueProp(value, writable, enumerable, configurable)
	if !o.hasOwnPropertyStr(name) {
		names := copyNamesIfNeeded(o.propNames, 1)
		o.propNames = append(names, name)
	}
	o.ensureValues()
	o.values[name] = prop
	return prop
}
