package goja

import "github.com/dop251/goja/unistring"

// descriptorObject is the ordinary object returned by
// Object.getOwnPropertyDescriptor, Object.getOwnPropertyDescriptors and
// Reflect.getOwnPropertyDescriptor. Its four properties live in fields —
// no property map, no key list — until something other than a read or a
// plain assignment to one of them happens (a new key, a define, a delete,
// preventExtensions, an export). Then ensureMaterialized moves them into
// the ordinary map in the same order and the object is a baseObject from
// there on. Nothing observable differs between the two states.
type descriptorObject struct {
	baseObject
	value, get, set                    Value // get/set: *Object or _undefined as constructed; any Value once assigned
	accessor                           bool
	writable, enumerable, configurable bool
	materialized                       bool
}

var (
	descriptorDataKeys     = [...]unistring.String{"value", "writable", "enumerable", "configurable"}
	descriptorAccessorKeys = [...]unistring.String{"get", "set", "enumerable", "configurable"}
)

type descriptorObjectBox struct {
	descriptorObject
	obj Object
}

func (r *Runtime) newDescriptorObject() *descriptorObject {
	b := &descriptorObjectBox{}
	b.obj.runtime = r
	d := &b.descriptorObject
	d.class = classObject
	d.val = &b.obj
	d.extensible = true
	d.prototype = r.global.ObjectPrototype
	b.obj.self = d
	return d
}

// virtualKeys returns the virtual key set in enumeration order (named to
// avoid shadowing objectImpl.keys).
func (d *descriptorObject) virtualKeys() []unistring.String {
	if d.accessor {
		return descriptorAccessorKeys[:]
	}
	return descriptorDataKeys[:]
}

// field returns the value of a virtual property, or nil if name is not one
// of the four keys. Every virtual property is a writable, enumerable,
// configurable data property, so the bare value is the stored form.
func (d *descriptorObject) field(name unistring.String) Value {
	r := d.val.runtime
	if d.accessor {
		switch name {
		case "get":
			return d.get
		case "set":
			return d.set
		}
	} else {
		switch name {
		case "value":
			return d.value
		case "writable":
			return r.toBoolean(d.writable)
		}
	}
	switch name {
	case "enumerable":
		return r.toBoolean(d.enumerable)
	case "configurable":
		return r.toBoolean(d.configurable)
	}
	return nil
}

// setField stores val into a virtual property in place and reports whether
// it did. Flags only hold booleans, so any other value for a flag (or any
// key that is not virtual) is left to the caller, which materializes.
func (d *descriptorObject) setField(name unistring.String, val Value) bool {
	if d.accessor {
		switch name {
		case "get":
			d.get = val
			return true
		case "set":
			d.set = val
			return true
		}
	} else {
		switch name {
		case "value":
			d.value = val
			return true
		case "writable":
			if b, ok := val.(valueBool); ok {
				d.writable = bool(b)
				return true
			}
			return false
		}
	}
	if b, ok := val.(valueBool); ok {
		switch name {
		case "enumerable":
			d.enumerable = bool(b)
			return true
		case "configurable":
			d.configurable = bool(b)
			return true
		}
	}
	return false
}

// ensureMaterialized moves the four properties into the ordinary map, in
// enumeration order, sized for them plus the key that is usually about to
// be added. Afterwards every method delegates to baseObject.
func (d *descriptorObject) ensureMaterialized() {
	if d.materialized {
		return
	}
	keys := d.virtualKeys()
	d.values = make(map[unistring.String]Value, len(keys)+1)
	d.propNames = make([]unistring.String, 0, len(keys)+1)
	for _, k := range keys {
		d.values[k] = d.field(k)
		d.propNames = append(d.propNames, k)
	}
	d.materialized = true
}

func (d *descriptorObject) getStr(name unistring.String, receiver Value) Value {
	if !d.materialized {
		if v := d.field(name); v != nil {
			return v
		}
	}
	return d.baseObject.getStr(name, receiver)
}

func (d *descriptorObject) getOwnPropStr(name unistring.String) Value {
	if !d.materialized {
		return d.field(name) // nil when absent: there are no other own string keys yet
	}
	return d.baseObject.getOwnPropStr(name)
}

func (d *descriptorObject) hasOwnPropertyStr(name unistring.String) bool {
	if !d.materialized {
		return d.field(name) != nil
	}
	return d.baseObject.hasOwnPropertyStr(name)
}

func (d *descriptorObject) setOwnStr(name unistring.String, val Value, throw bool) bool {
	if !d.materialized {
		if d.setField(name, val) {
			return true
		}
		d.ensureMaterialized()
	}
	return d.baseObject.setOwnStr(name, val, throw)
}

func (d *descriptorObject) setForeignStr(name unistring.String, val, receiver Value, throw bool) (bool, bool) {
	return d._setForeignStr(name, d.getOwnPropStr(name), val, receiver, throw)
}

func (d *descriptorObject) defineOwnPropertyStr(name unistring.String, descr PropertyDescriptor, throw bool) bool {
	d.ensureMaterialized()
	return d.baseObject.defineOwnPropertyStr(name, descr, throw)
}

func (d *descriptorObject) deleteStr(name unistring.String, throw bool) bool {
	if !d.materialized {
		if d.field(name) == nil {
			return true // absent: nothing to delete, stay virtual
		}
		d.ensureMaterialized()
	}
	return d.baseObject.deleteStr(name, throw)
}

func (d *descriptorObject) preventExtensions(throw bool) bool {
	d.ensureMaterialized()
	return d.baseObject.preventExtensions(throw)
}

func (d *descriptorObject) _putProp(name unistring.String, value Value, writable, enumerable, configurable bool) Value {
	d.ensureMaterialized()
	return d.baseObject._putProp(name, value, writable, enumerable, configurable)
}

func (d *descriptorObject) stringKeys(all bool, accum []Value) []Value {
	if d.materialized {
		return d.baseObject.stringKeys(all, accum)
	}
	for _, k := range d.virtualKeys() {
		accum = append(accum, stringValueFromRaw(k))
	}
	return accum
}

// appendEnumerableStringKeys serves Object.AppendKeys/Keys.
func (d *descriptorObject) appendEnumerableStringKeys(dst []string) []string {
	if d.materialized {
		return d.baseObject.appendEnumerableStringKeys(dst)
	}
	keys := d.virtualKeys()
	if dst == nil {
		dst = make([]string, 0, len(keys))
	}
	for _, k := range keys {
		dst = append(dst, k.String())
	}
	return dst
}

// descriptorPropIter walks the four keys. Like objectPropIter it sees the
// key list as it was when the iteration started (keys added meanwhile are
// not visited) and skips keys deleted meanwhile; if the object
// materializes mid-iteration the remaining keys are read from the map.
type descriptorPropIter struct {
	d   *descriptorObject
	idx int
}

func (i *descriptorPropIter) next() (propIterItem, iterNextFunc) {
	d := i.d
	keys := d.virtualKeys()
	for i.idx < len(keys) {
		name := keys[i.idx]
		i.idx++
		var v Value
		if d.materialized {
			v = d.values[name]
		} else {
			v = d.field(name)
		}
		if v != nil {
			return propIterItem{name: stringValueFromRaw(name), value: v}, i.next
		}
	}
	return propIterItem{}, nil
}

func (d *descriptorObject) iterateStringKeys() iterNextFunc {
	if d.materialized {
		return d.baseObject.iterateStringKeys()
	}
	return (&descriptorPropIter{d: d}).next
}

// export walks baseObject's own key list, so materialize first.
func (d *descriptorObject) export(ctx *objectExportCtx) interface{} {
	d.ensureMaterialized()
	return d.baseObject.export(ctx)
}
