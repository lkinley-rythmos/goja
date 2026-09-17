package goja

import (
	"fmt"
	"math"
	"reflect"

	"github.com/dop251/goja/unistring"
)

type resultType uint8

const (
	resultNormal resultType = iota
	resultYield
	resultYieldRes      // a yield that expects a value in return
	resultYieldDelegate // yield*
	resultYieldDelegateRes
	resultAwait
)

// used both as an instruction and as a Value
type yieldMarker struct {
	valueNull
	resultType resultType
}

var (
	await = &yieldMarker{resultType: resultAwait}

	yield            = &yieldMarker{resultType: resultYield}
	yieldRes         = &yieldMarker{resultType: resultYieldRes}
	yieldDelegate    = &yieldMarker{resultType: resultYieldDelegate}
	yieldDelegateRes = &yieldMarker{resultType: resultYieldDelegateRes}
	yieldEmpty       = &yieldMarker{resultType: resultYield}
)

// AsyncContextTracker is a handler that allows to track an async execution context to ensure it remains
// consistent across all callback invocations.
// Whenever a Promise reaction job is scheduled the Grab method is called. It is supposed to return the
// current context. The same context will be supplied to the Resumed method before the reaction job is
// executed. The Exited method is called after the reaction job is finished.
// This means that for each invocation of the Grab method there will be exactly one subsequent invocation
// of Resumed and then Exited methods (assuming the Promise is fulfilled or rejected). Also, the Resumed/Exited
// calls cannot be nested, so Exited can simply clear the current context instead of popping from a stack.
// Note, this works for both async functions and regular Promise.then()/Promise.catch() callbacks.
// See TestAsyncContextTracker for more insight.
//
// To register it call Runtime.SetAsyncContextTracker().
type AsyncContextTracker interface {
	Grab() (trackingObject interface{})
	Resumed(trackingObject interface{})
	Exited()
}

type funcObjectImpl interface {
	source() String
}

type baseFuncObject struct {
	baseObject

	lenProp  valueProperty
	nameProp valueProperty
	// intrinsicsVirtual is true while length and name live only in the
	// fields above. The first define or delete of either materializes both
	// into values/propNames (see materializeIntrinsics) and clears it.
	intrinsicsVirtual bool
}

type baseJsFuncObject struct {
	baseFuncObject

	stash   *stash
	privEnv *privateEnv

	prg    *Program
	src    string
	strict bool

	// callFn caches the method value assertCallable returns. A method
	// value escapes to the heap, so without the cache every toCallable or
	// AssertFunction of a JS function allocated a closure per lookup.
	callFn func(FunctionCall) Value
}

type funcObject struct {
	baseJsFuncObject
}

type generatorFuncObject struct {
	baseJsFuncObject
}

type asyncFuncObject struct {
	baseJsFuncObject
}

type classFuncObject struct {
	baseJsFuncObject
	initFields   *Program
	computedKeys []Value

	privateEnvType *privateEnvType
	privateMethods []Value

	derived bool
}

type methodFuncObject struct {
	baseJsFuncObject
	homeObject *Object
}

type generatorMethodFuncObject struct {
	methodFuncObject
}

type asyncMethodFuncObject struct {
	methodFuncObject
}

type arrowFuncObject struct {
	baseJsFuncObject
	funcObj   *Object
	newTarget Value
}

type asyncArrowFuncObject struct {
	arrowFuncObject
}

type nativeFuncObject struct {
	baseFuncObject

	f         func(FunctionCall) Value
	construct func(args []Value, newTarget *Object) *Object
}

type wrappedFuncObject struct {
	nativeFuncObject
	wrapped reflect.Value
}

type boundFuncObject struct {
	nativeFuncObject
	wrapped *Object
}

type generatorState uint8

const (
	genStateUndefined generatorState = iota
	genStateSuspendedStart
	genStateExecuting
	genStateSuspendedYield
	genStateSuspendedYieldRes
	genStateCompleted
)

type generatorObject struct {
	baseObject
	gen       generator
	delegated *iteratorRecord
	state     generatorState
}

func (f *nativeFuncObject) source() String {
	return newStringValue(fmt.Sprintf("function %s() { [native code] }", nilSafe(f.getStr("name", nil)).toString()))
}

func (f *nativeFuncObject) export(*objectExportCtx) interface{} {
	return f.f
}

func (f *wrappedFuncObject) exportType() reflect.Type {
	return f.wrapped.Type()
}

func (f *wrappedFuncObject) export(*objectExportCtx) interface{} {
	return f.wrapped.Interface()
}

func (f *funcObject) _addProto(n unistring.String) Value {
	if n == "prototype" {
		if _, exists := f.values[n]; !exists {
			return f.addPrototype()
		}
	}
	return nil
}

func (f *funcObject) getStr(p unistring.String, receiver Value) Value {
	return f.getStrWithOwnProp(f.getOwnPropStr(p), p, receiver)
}

func (f *funcObject) getOwnPropStr(name unistring.String) Value {
	if v := f._addProto(name); v != nil {
		return v
	}

	return f.baseFuncObject.getOwnPropStr(name)
}

func (f *funcObject) setOwnStr(name unistring.String, val Value, throw bool) bool {
	f._addProto(name)
	return f.baseFuncObject.setOwnStr(name, val, throw)
}

func (f *funcObject) setForeignStr(name unistring.String, val, receiver Value, throw bool) (bool, bool) {
	return f._setForeignStr(name, f.getOwnPropStr(name), val, receiver, throw)
}

func (f *funcObject) defineOwnPropertyStr(name unistring.String, descr PropertyDescriptor, throw bool) bool {
	f._addProto(name)
	return f.baseFuncObject.defineOwnPropertyStr(name, descr, throw)
}

func (f *funcObject) deleteStr(name unistring.String, throw bool) bool {
	f._addProto(name)
	return f.baseFuncObject.deleteStr(name, throw)
}

func (f *funcObject) addPrototype() Value {
	proto := f.val.runtime.NewObject()
	proto.self._putProp("constructor", f.val, true, false, true)
	return f._putProp("prototype", proto, true, false, false)
}

func (f *funcObject) hasOwnPropertyStr(name unistring.String) bool {
	if f.baseFuncObject.hasOwnPropertyStr(name) {
		return true
	}

	if name == "prototype" {
		return true
	}
	return false
}

func (f *funcObject) stringKeys(all bool, accum []Value) []Value {
	if all {
		if _, exists := f.values["prototype"]; !exists {
			accum = append(accum, asciiString("prototype"))
		}
	}
	return f.baseFuncObject.stringKeys(all, accum)
}

func (f *funcObject) iterateStringKeys() iterNextFunc {
	if _, exists := f.values["prototype"]; !exists {
		f.addPrototype()
	}
	return f.baseFuncObject.iterateStringKeys()
}

func (f *baseFuncObject) createInstance(newTarget *Object) *Object {
	r := f.val.runtime
	if newTarget == nil {
		newTarget = f.val
	}
	proto := r.getPrototypeFromCtor(newTarget, nil, r.global.ObjectPrototype)

	return f.val.runtime.newBaseObject(proto, classObject).val
}

func (f *baseJsFuncObject) source() String {
	return newStringValue(f.src)
}

func (f *baseJsFuncObject) construct(args []Value, newTarget *Object) *Object {
	if newTarget == nil {
		newTarget = f.val
	}
	proto := newTarget.self.getStr("prototype", nil)
	var protoObj *Object
	if p, ok := proto.(*Object); ok {
		protoObj = p
	} else {
		protoObj = f.val.runtime.global.ObjectPrototype
	}

	obj := f.val.runtime.newBaseObject(protoObj, classObject).val
	ret := f.call(FunctionCall{
		This:      obj,
		Arguments: args,
	}, newTarget)

	if ret, ok := ret.(*Object); ok {
		return ret
	}
	return obj
}

func (f *classFuncObject) Call(FunctionCall) Value {
	panic(f.val.runtime.NewTypeError("Class constructor cannot be invoked without 'new'"))
}

func (f *classFuncObject) assertCallable() (func(FunctionCall) Value, bool) {
	if f.callFn == nil {
		f.callFn = f.Call
	}
	return f.callFn, true
}

func (f *classFuncObject) vmCall(vm *vm, n int) {
	f.Call(FunctionCall{})
}

func (f *classFuncObject) exportType() reflect.Type {
	return reflectTypeCtor
}

func (f *classFuncObject) Construct(ccall ConstructorCall) *Object {
	return f.construct(ccall.Arguments, ccall.NewTarget)
}

func (f *classFuncObject) export(*objectExportCtx) interface{} {
	return f.Construct
}

func (f *classFuncObject) createInstance(args []Value, newTarget *Object) (instance *Object) {
	if f.derived {
		if ctor := f.prototype.self.assertConstructor(); ctor != nil {
			instance = ctor(args, newTarget)
		} else {
			panic(f.val.runtime.NewTypeError("Super constructor is not a constructor"))
		}
	} else {
		instance = f.baseFuncObject.createInstance(newTarget)
	}
	return
}

func (f *classFuncObject) _initFields(instance *Object) {
	if f.privateEnvType != nil {
		penv := instance.self.getPrivateEnv(f.privateEnvType, true)
		penv.methods = f.privateMethods
	}
	if f.initFields != nil {
		vm := f.val.runtime.vm
		vm.pushCtx()
		vm.prg = f.initFields
		vm.stash = f.stash
		vm.privEnv = f.privEnv
		vm.newTarget = nil
		// enterFunc derives the stack base from vm.args, which otherwise still
		// holds the argument count of whichever function is executing 'new'.
		vm.args = 0

		// so that 'super' base could be correctly resolved (including from direct eval())
		vm.push(f.val)

		vm.sb = vm.sp
		vm.push(instance)
		vm.pc = 0
		ex := vm.runTry()
		vm.popCtx()
		if ex != nil {
			panic(ex)
		}
		vm.sp -= 2
	}
}

func (f *classFuncObject) construct(args []Value, newTarget *Object) *Object {
	if newTarget == nil {
		newTarget = f.val
	}
	if f.prg == nil {
		instance := f.createInstance(args, newTarget)
		f._initFields(instance)
		return instance
	} else {
		var instance *Object
		var thisVal Value
		if !f.derived {
			instance = f.createInstance(args, newTarget)
			f._initFields(instance)
			thisVal = instance
		}
		ret := f._call(args, newTarget, thisVal)

		if ret, ok := ret.(*Object); ok {
			return ret
		}
		if f.derived {
			r := f.val.runtime
			if ret != _undefined {
				panic(r.NewTypeError("Derived constructors may only return object or undefined"))
			}
			if v := r.vm.stack[r.vm.sp+1]; v != nil { // using residual 'this' value (a bit hacky)
				instance = r.toObject(v)
			} else {
				panic(r.newError(r.getReferenceError(), "Must call super constructor in derived class before returning from derived constructor"))
			}
		}
		return instance
	}
}

func (f *classFuncObject) assertConstructor() func(args []Value, newTarget *Object) *Object {
	return f.construct
}

func (f *baseJsFuncObject) Call(call FunctionCall) Value {
	return f.call(call, nil)
}

func (f *arrowFuncObject) Call(call FunctionCall) Value {
	return f._call(call.Arguments, f.newTarget, nil)
}

func (f *baseJsFuncObject) __call(args []Value, newTarget, this Value) (Value, *Exception) {
	vm := f.val.runtime.vm

	vm.stack.expand(vm.sp + len(args) + 1)
	vm.stack[vm.sp] = f.val
	vm.sp++
	vm.stack[vm.sp] = this
	vm.sp++
	for _, arg := range args {
		if arg != nil {
			vm.stack[vm.sp] = arg
		} else {
			vm.stack[vm.sp] = _undefined
		}
		vm.sp++
	}

	vm.pushTryFrame(tryPanicMarker, -1)
	defer vm.popTryFrame()

	var needPop bool
	if vm.prg != nil {
		vm.pushCtx()
		vm.callStack = append(vm.callStack, context{pc: -2}) // extra frame so that run() halts after ret
		needPop = true
	} else {
		vm.pc = -2
		vm.pushCtx()
	}

	vm.args = len(args)
	vm.prg = f.prg
	vm.stash = f.stash
	vm.privEnv = f.privEnv
	vm.newTarget = newTarget
	vm.pc = 0
	for {
		ex := vm.runTryInner()
		if ex != nil {
			return nil, ex
		}
		if vm.halted() {
			break
		}
	}
	if needPop {
		vm.popCtx()
	}

	return vm.pop(), nil
}

func (f *baseJsFuncObject) _call(args []Value, newTarget, this Value) Value {
	res, ex := f.__call(args, newTarget, this)
	if ex != nil {
		panic(ex)
	}
	return res
}

func (f *baseJsFuncObject) call(call FunctionCall, newTarget Value) Value {
	return f._call(call.Arguments, newTarget, nilSafe(call.This))
}

func (f *baseJsFuncObject) export(*objectExportCtx) interface{} {
	return f.Call
}

func (f *baseFuncObject) exportType() reflect.Type {
	return reflectTypeFunc
}

func (f *baseFuncObject) typeOf() String {
	return stringFunction
}

func (f *baseJsFuncObject) assertCallable() (func(FunctionCall) Value, bool) {
	if f.callFn == nil {
		f.callFn = f.Call
	}
	return f.callFn, true
}

func (f *funcObject) assertConstructor() func(args []Value, newTarget *Object) *Object {
	return f.construct
}

func (f *baseJsFuncObject) vmCall(vm *vm, n int) {
	vm.pushCtx()
	vm.args = n
	vm.prg = f.prg
	vm.stash = f.stash
	vm.privEnv = f.privEnv
	vm.pc = 0
	vm.stack[vm.sp-n-1], vm.stack[vm.sp-n-2] = vm.stack[vm.sp-n-2], vm.stack[vm.sp-n-1]
}

func (f *arrowFuncObject) assertCallable() (func(FunctionCall) Value, bool) {
	if f.callFn == nil {
		f.callFn = f.Call
	}
	return f.callFn, true
}

func (f *arrowFuncObject) vmCall(vm *vm, n int) {
	vm.pushCtx()
	vm.args = n
	vm.prg = f.prg
	vm.stash = f.stash
	vm.privEnv = f.privEnv
	vm.pc = 0
	vm.stack[vm.sp-n-1], vm.stack[vm.sp-n-2] = nil, vm.stack[vm.sp-n-1]
	vm.newTarget = f.newTarget
}

func (f *arrowFuncObject) export(*objectExportCtx) interface{} {
	return f.Call
}

func (f *baseFuncObject) init(name unistring.String, length Value) {
	f.baseObject.init()

	f.lenProp.configurable = true
	f.lenProp.value = length

	f.nameProp.configurable = true
	f.nameProp.value = stringValueFromRaw(name)

	f.intrinsicsVirtual = true
}

// materializeIntrinsics moves length and name into the property map, at
// the front of propNames where init used to put them, so enumeration order
// is unchanged whether or not the intrinsics were ever materialized.
func (f *baseFuncObject) materializeIntrinsics() {
	if !f.intrinsicsVirtual {
		return
	}
	f.intrinsicsVirtual = false
	f.ensureValues()
	f.values["length"] = &f.lenProp
	f.values["name"] = &f.nameProp
	names := f.propNames
	if namesMarkedForCopy(names) || cap(names) < len(names)+2 {
		// An iterator may be walking the current array: never shift it in place.
		newNames := make([]unistring.String, len(names)+2, growCap(len(names)+2, len(names), cap(names)))
		copy(newNames[2:], names)
		names = newNames
	} else {
		names = append(names, "", "")
		copy(names[2:], names[:len(names)-2])
	}
	names[0] = "length"
	names[1] = "name"
	f.propNames = names
	// Integer-like keys must still sort first; force fixPropOrder to
	// re-scan from the beginning on the next enumeration.
	f.lastSortedPropLen = 0
	f.idxPropCount = 0
}

func (f *baseFuncObject) intrinsicProp(name unistring.String) *valueProperty {
	if !f.intrinsicsVirtual {
		return nil
	}
	switch name {
	case "length":
		return &f.lenProp
	case "name":
		return &f.nameProp
	}
	return nil
}

func (f *baseFuncObject) getStr(p unistring.String, receiver Value) Value {
	return f.getStrWithOwnProp(f.getOwnPropStr(p), p, receiver)
}

func (f *baseFuncObject) getOwnPropStr(name unistring.String) Value {
	if p := f.intrinsicProp(name); p != nil {
		return p
	}
	return f.baseObject.getOwnPropStr(name)
}

func (f *baseFuncObject) hasOwnPropertyStr(name unistring.String) bool {
	if f.intrinsicProp(name) != nil {
		return true
	}
	return f.baseObject.hasOwnPropertyStr(name)
}

func (f *baseFuncObject) setOwnStr(name unistring.String, val Value, throw bool) bool {
	if p := f.intrinsicProp(name); p != nil {
		if p.accessor || p.writable {
			f.materializeIntrinsics()
			return f.baseObject.setOwnStr(name, val, throw)
		}
		f.val.runtime.typeErrorResult(throw, "Cannot assign to read only property '%s'", name)
		return false
	}
	return f.baseObject.setOwnStr(name, val, throw)
}

func (f *baseFuncObject) setForeignStr(name unistring.String, val, receiver Value, throw bool) (bool, bool) {
	return f._setForeignStr(name, f.getOwnPropStr(name), val, receiver, throw)
}

func (f *baseFuncObject) defineOwnPropertyStr(name unistring.String, descr PropertyDescriptor, throw bool) bool {
	if p := f.intrinsicProp(name); p != nil {
		v, ok := f._defineOwnProperty(name, p, descr, throw)
		if !ok {
			return false
		}
		if v == Value(p) {
			// Updated in place (the SetFunctionName case and every other
			// descriptor except the all-true data form): still virtual.
			return true
		}
		f.materializeIntrinsics()
		f.values[name] = v
		return true
	}
	return f.baseObject.defineOwnPropertyStr(name, descr, throw)
}

func (f *baseFuncObject) deleteStr(name unistring.String, throw bool) bool {
	if p := f.intrinsicProp(name); p != nil {
		if !p.configurable {
			// Rejected without materializing, so frozen functions stay map-free.
			return f.checkDeleteProp(name, p, throw)
		}
		f.materializeIntrinsics()
	}
	return f.baseObject.deleteStr(name, throw)
}

func (f *baseFuncObject) stringKeys(all bool, accum []Value) []Value {
	if !f.intrinsicsVirtual {
		return f.baseObject.stringKeys(all, accum)
	}
	// Integer-like keys sort before every other string key, including the
	// intrinsics; ensurePropOrder leaves them as the first idxPropCount
	// entries of propNames.
	f.ensurePropOrder()
	n := f.idxPropCount
	accum = f.appendStringKeys(all, accum, f.propNames[:n])
	// A virtual intrinsic's flags are live: a define can flip enumerable,
	// writable or configurable in place. Every consumer reads the field,
	// never the init defaults. (iterateStringKeys yields the field pointers,
	// so enumerableIter reads the same flags.)
	for _, p := range [...]struct {
		name string
		prop *valueProperty
	}{{"length", &f.lenProp}, {"name", &f.nameProp}} {
		if all || p.prop.enumerable {
			accum = append(accum, asciiString(p.name))
		}
	}
	return f.appendStringKeys(all, accum, f.propNames[n:])
}

const (
	funcIterIndexKeys = iota // pass through the sorted integer-like keys
	funcIterLength
	funcIterName
	funcIterRest
)

// funcIntrinsicsIter yields a function's own string keys in enumeration
// order: the sorted integer-like keys first, then length and name, then
// the remaining keys. The intrinsics are read from live state on each
// step: while virtual they are the fields (so Object.freeze mutates the
// real storage); once materialized they come from the map, so an
// intrinsic deleted or redefined during the iteration is treated exactly
// as any other property is.
type funcIntrinsicsIter struct {
	f          *baseFuncObject
	wrapped    iterNextFunc
	phase      int
	pending    propIterItem
	hasPending bool
}

func (i *funcIntrinsicsIter) intrinsic(name unistring.String, p *valueProperty) (propIterItem, bool) {
	var v Value = p
	if !i.f.intrinsicsVirtual {
		if v = i.f.values[name]; v == nil {
			return propIterItem{}, false // deleted while the iteration was in flight
		}
	}
	return propIterItem{name: asciiString(name), value: v}, true
}

func (i *funcIntrinsicsIter) next() (propIterItem, iterNextFunc) {
	for {
		switch i.phase {
		case funcIterIndexKeys:
			var item propIterItem
			item, i.wrapped = i.wrapped()
			if i.wrapped != nil && strToArrayIdx(item.name.string()) != math.MaxUint32 {
				return item, i.next
			}
			if i.wrapped != nil {
				i.pending, i.hasPending = item, true
			}
			i.phase = funcIterLength
		case funcIterLength:
			i.phase = funcIterName
			if it, ok := i.intrinsic("length", &i.f.lenProp); ok {
				return it, i.next
			}
		case funcIterName:
			i.phase = funcIterRest
			if it, ok := i.intrinsic("name", &i.f.nameProp); ok {
				return it, i.next
			}
		default:
			if i.hasPending {
				i.hasPending = false
				return i.pending, i.next
			}
			if i.wrapped == nil {
				return propIterItem{}, nil
			}
			return i.wrapped()
		}
	}
}

func (f *baseFuncObject) iterateStringKeys() iterNextFunc {
	if f.intrinsicsVirtual {
		return (&funcIntrinsicsIter{f: f, wrapped: f.baseObject.iterateStringKeys()}).next
	}
	return f.baseObject.iterateStringKeys()
}

func hasInstance(val *Object, v Value) bool {
	if v, ok := v.(*Object); ok {
		o := val.self.getStr("prototype", nil)
		if o1, ok := o.(*Object); ok {
			for {
				v = v.self.proto()
				if v == nil {
					return false
				}
				if o1 == v {
					return true
				}
			}
		} else {
			panic(val.runtime.NewTypeError("prototype is not an object"))
		}
	}

	return false
}

func (f *baseFuncObject) hasInstance(v Value) bool {
	return hasInstance(f.val, v)
}

func (f *nativeFuncObject) defaultConstruct(ccall func(ConstructorCall) *Object, args []Value, newTarget *Object) *Object {
	obj := f.createInstance(newTarget)
	ret := ccall(ConstructorCall{
		This:      obj,
		Arguments: args,
		NewTarget: newTarget,
	})

	if ret != nil {
		return ret
	}
	return obj
}

func (f *nativeFuncObject) assertCallable() (func(FunctionCall) Value, bool) {
	if f.f != nil {
		return f.f, true
	}
	return nil, false
}

func (f *nativeFuncObject) vmCall(vm *vm, n int) {
	if f.f != nil {
		vm.pushCtx()
		vm.prg = nil
		vm.sb = vm.sp - n // so that [sb-1] points to the callee
		ret := f.f(FunctionCall{
			Arguments: vm.stack[vm.sp-n : vm.sp],
			This:      vm.stack[vm.sp-n-2],
		})
		if ret == nil {
			ret = _undefined
		}
		vm.stack[vm.sp-n-2] = ret
		vm.popCtx()
	} else {
		vm.stack[vm.sp-n-2] = _undefined
	}
	vm.sp -= n + 1
	vm.pc++
}

func (f *nativeFuncObject) assertConstructor() func(args []Value, newTarget *Object) *Object {
	return f.construct
}

func (f *boundFuncObject) hasInstance(v Value) bool {
	return instanceOfOperator(v, f.wrapped)
}

func (f *baseJsFuncObject) prepareForVmCall(call FunctionCall) {
	vm := f.val.runtime.vm
	args := call.Arguments
	vm.stack.expand(vm.sp + len(args) + 1)
	vm.stack[vm.sp] = call.This
	vm.sp++
	vm.stack[vm.sp] = f.val
	vm.sp++
	for _, arg := range args {
		if arg != nil {
			vm.stack[vm.sp] = arg
		} else {
			vm.stack[vm.sp] = _undefined
		}
		vm.sp++
	}
}

func (f *baseJsFuncObject) asyncCall(call FunctionCall, vmCall func(*vm, int)) Value {
	f.prepareForVmCall(call)
	ar := &asyncRunner{
		f:      f.val,
		vmCall: vmCall,
	}
	ar.start(len(call.Arguments))
	return ar.promiseCap.promise
}

func (f *asyncFuncObject) Call(call FunctionCall) Value {
	return f.asyncCall(call, f.baseJsFuncObject.vmCall)
}

func (f *asyncFuncObject) assertCallable() (func(FunctionCall) Value, bool) {
	if f.callFn == nil {
		f.callFn = f.Call
	}
	return f.callFn, true
}

func (f *asyncFuncObject) export(*objectExportCtx) interface{} {
	return f.Call
}

func (f *asyncArrowFuncObject) Call(call FunctionCall) Value {
	return f.asyncCall(call, f.arrowFuncObject.vmCall)
}

func (f *asyncArrowFuncObject) assertCallable() (func(FunctionCall) Value, bool) {
	if f.callFn == nil {
		f.callFn = f.Call
	}
	return f.callFn, true
}

func (f *asyncArrowFuncObject) export(*objectExportCtx) interface{} {
	return f.Call
}

func (f *asyncArrowFuncObject) vmCall(vm *vm, n int) {
	f.asyncVmCall(vm, n, f.arrowFuncObject.vmCall)
}

func (f *asyncMethodFuncObject) Call(call FunctionCall) Value {
	return f.asyncCall(call, f.methodFuncObject.vmCall)
}

func (f *asyncMethodFuncObject) assertCallable() (func(FunctionCall) Value, bool) {
	return f.Call, true
}

func (f *asyncMethodFuncObject) export(ctx *objectExportCtx) interface{} {
	return f.Call
}

func (f *asyncMethodFuncObject) vmCall(vm *vm, n int) {
	f.asyncVmCall(vm, n, f.methodFuncObject.vmCall)
}

func (f *baseJsFuncObject) asyncVmCall(vm *vm, n int, vmCall func(*vm, int)) {
	ar := &asyncRunner{
		f:      f.val,
		vmCall: vmCall,
	}
	ar.start(n)
	vm.push(ar.promiseCap.promise)
	vm.pc++
}

func (f *asyncFuncObject) vmCall(vm *vm, n int) {
	f.asyncVmCall(vm, n, f.baseJsFuncObject.vmCall)
}

type asyncRunner struct {
	gen        generator
	promiseCap *promiseCapability
	f          *Object
	vmCall     func(*vm, int)
}

func (ar *asyncRunner) onFulfilled(call FunctionCall) Value {
	ar.gen.vm.curAsyncRunner = ar
	defer func() {
		ar.gen.vm.curAsyncRunner = nil
	}()
	arg := call.Argument(0)
	res, resType, ex := ar.gen.next(arg)
	ar.step(res, resType == resultNormal, ex)
	return _undefined
}

func (ar *asyncRunner) onRejected(call FunctionCall) Value {
	ar.gen.vm.curAsyncRunner = ar
	defer func() {
		ar.gen.vm.curAsyncRunner = nil
	}()
	reason := call.Argument(0)
	res, resType, ex := ar.gen.nextThrow(reason)
	ar.step(res, resType == resultNormal, ex)
	return _undefined
}

func (ar *asyncRunner) step(res Value, done bool, ex *Exception) {
	r := ar.f.runtime
	if done || ex != nil {
		if ex == nil {
			ar.promiseCap.resolve(res)
		} else {
			ar.promiseCap.reject(ex.val)
		}
		return
	}

	// await
	promise := r.promiseResolve(r.getPromise(), res)
	promise.self.(*Promise).addReactions(&promiseReaction{
		typ:         promiseReactionFulfill,
		handler:     &jobCallback{callback: ar.onFulfilled},
		asyncRunner: ar,
	}, &promiseReaction{
		typ:         promiseReactionReject,
		handler:     &jobCallback{callback: ar.onRejected},
		asyncRunner: ar,
	})
}

func (ar *asyncRunner) start(nArgs int) {
	r := ar.f.runtime
	ar.gen.vm = r.vm
	ar.promiseCap = r.newPromiseCapability(r.getPromise())
	sp := r.vm.sp
	ar.gen.enter()
	ar.vmCall(r.vm, nArgs)
	res, resType, ex := ar.gen.step()
	ar.step(res, resType == resultNormal, ex)
	if ex != nil {
		r.vm.sp = sp - nArgs - 2
	}
	r.vm.popTryFrame()
	r.vm.popCtx()
}

type generator struct {
	ctx       execCtx
	vm        *vm
	returning Value

	tryStackLen, iterStackLen, refStackLen uint32
}

func (g *generator) storeLengths() {
	g.tryStackLen, g.iterStackLen, g.refStackLen = uint32(len(g.vm.tryStack)), uint32(len(g.vm.iterStack)), uint32(len(g.vm.refStack))
}

func (g *generator) enter() {
	g.vm.pushCtx()
	g.vm.pushTryFrame(tryPanicMarker, -1)
	g.vm.prg, g.vm.sb, g.vm.pc = nil, -1, -2 // so that vm.run() halts after ret
	g.storeLengths()
}

func (g *generator) enterNextFinallyFrame() (canContinue bool) {
	vm := g.vm
	callStackLen := len(vm.callStack)

	for len(vm.tryStack) > 0 {
		tf := &vm.tryStack[len(vm.tryStack)-1]
		if int(tf.callStackLen) != callStackLen { // have we breached the function boundary?
			break
		}
		ex := vm.restoreStacks(tf.iterLen, tf.refLen)
		if ex != nil {
			vm.throw(ex)
			return true
		}
		if tf.finallyPos >= 0 {
			vm.sp = int(tf.sp)
			vm.stash = tf.stash
			vm.privEnv = tf.privEnv
			vm.pc = int(tf.finallyPos)
			tf.catchPos = tryPanicMarker
			tf.finallyPos = -1
			tf.finallyRet = -2 // -1 would cause it to continue after leaveFinally
			return true
		}
		vm.popTryFrame()
	}
	return
}

func (g *generator) step() (res Value, resultType resultType, ex *Exception) {
	vm := g.vm
	if g.returning == nil {
		for {
			ex = vm.runTryInner()
			if ex != nil {
				return
			}
			if vm.halted() {
				break
			}
		}
		res = vm.pop()
	} else {
		for {
			ex = vm.runTryInner()
			if ex != nil {
				if vm.prg != nil || vm.pc != -2 {
					// The exception was thrown in the outermost finally block, it never got to leaveFinally
					// which does popTryFrame()
					vm.popTryFrame()
				}
				return
			}

			if vm.prg != nil && vm.pc == -2 { // normal exit from finally
				if g.enterNextFinallyFrame() {
					continue
				}

				// All finally blocks have exited without result
				res, g.returning = g.returning, nil
				ex = vm.restoreStacks(g.iterStackLen, g.refStackLen)
				if ex != nil {
					return
				}
				vm.sp = vm.sb - 1
				vm.callStack = vm.callStack[:len(vm.callStack)-1]

				return
			}
			res = vm.pop()
			if vm.prg == nil { // It was a return, not a yield
				return
			}
			break
		}
	}

	if ym, ok := res.(*yieldMarker); ok {
		resultType = ym.resultType
		g.ctx = execCtx{}
		vm.pc = -vm.pc + 1
		if res != yieldEmpty {
			res = vm.pop()
		} else {
			res = nil
		}
		vm.suspend(&g.ctx, g.tryStackLen, g.iterStackLen, g.refStackLen)
		vm.sp = vm.sb - 1
		vm.callStack = vm.callStack[:len(vm.callStack)-1] // remove the frame with pc == -2, as ret would do
	}
	return
}

func (g *generator) enterNext() {
	g.vm.pushCtx()
	g.vm.pushTryFrame(tryPanicMarker, -1)
	g.vm.callStack = append(g.vm.callStack, context{pc: -2}) // extra frame so that vm.run() halts after ret
	g.storeLengths()
	g.vm.resume(&g.ctx)
}

func (g *generator) next(v Value) (Value, resultType, *Exception) {
	g.enterNext()
	if v != nil {
		g.vm.push(v)
	}
	res, done, ex := g.step()
	g.vm.popTryFrame()
	g.vm.popCtx()
	return res, done, ex
}

func (g *generator) nextThrow(v interface{}) (Value, resultType, *Exception) {
	g.enterNext()
	ex := g.vm.handleThrow(v)
	if ex != nil {
		g.vm.popTryFrame()
		g.vm.popCtx()
		return nil, resultNormal, ex
	}

	res, resType, ex := g.step()
	g.vm.popTryFrame()
	g.vm.popCtx()
	return res, resType, ex
}

func (g *generatorObject) init(vmCall func(*vm, int), nArgs int) {
	g.baseObject.init()
	vm := g.val.runtime.vm
	g.gen.vm = vm

	g.gen.enter()
	vmCall(vm, nArgs)

	_, _, ex := g.gen.step()

	vm.popTryFrame()
	if ex != nil {
		panic(ex)
	}

	g.state = genStateSuspendedStart
	vm.popCtx()
}

func (g *generatorObject) validate() {
	if g.state == genStateExecuting {
		panic(g.val.runtime.NewTypeError("Illegal generator state"))
	}
}

func (g *generatorObject) step(res Value, resType resultType, ex *Exception) Value {
	if ex != nil {
		g.delegated = nil
		g.state = genStateCompleted
		panic(ex)
	}
	switch resType {
	case resultYield:
		g.state = genStateSuspendedYield
		return g.val.runtime.createIterResultObject(res, false)
	case resultYieldDelegate:
		g.state = genStateSuspendedYield
		return g.delegate(res)
	case resultYieldRes:
		g.state = genStateSuspendedYieldRes
		return g.val.runtime.createIterResultObject(res, false)
	case resultYieldDelegateRes:
		g.state = genStateSuspendedYieldRes
		return g.delegate(res)
	case resultNormal:
		g.state = genStateCompleted
		return g.val.runtime.createIterResultObject(res, true)
	default:
		panic(g.val.runtime.NewTypeError("Runtime bug: unexpected result type: %v", resType))
	}
}

func (g *generatorObject) delegate(v Value) Value {
	ex := g.val.runtime.try(func() {
		g.delegated = g.val.runtime.getIterator(v, nil)
	})
	if ex != nil {
		g.delegated = nil
		g.state = genStateCompleted
		return g.step(g.gen.nextThrow(ex))
	}
	return g.next(_undefined)
}

func (g *generatorObject) tryCallDelegated(fn func() (Value, bool)) (ret Value, done bool) {
	ex := g.val.runtime.try(func() {
		ret, done = fn()
	})
	if ex != nil {
		g.delegated = nil
		g.state = genStateExecuting
		return g.step(g.gen.nextThrow(ex)), false
	}
	return
}

func (g *generatorObject) callDelegated(method func(FunctionCall) Value, v Value) (Value, bool) {
	res := g.val.runtime.toObject(method(FunctionCall{This: g.delegated.iterator, Arguments: []Value{v}}))
	if iteratorComplete(res) {
		g.delegated = nil
		return iteratorValue(res), true
	}
	return res, false
}

func (g *generatorObject) next(v Value) Value {
	g.validate()
	if g.state == genStateCompleted {
		return g.val.runtime.createIterResultObject(_undefined, true)
	}
	if g.delegated != nil {
		res, done := g.tryCallDelegated(func() (Value, bool) {
			// yield* forwards the inner iterator's result object verbatim
			// (GeneratorYield(innerResult)), so its identity and any extra own
			// properties are observable. This call therefore must not use
			// iteratorRecord.fast, which exists only for consumers that read
			// value/done and discard the object.
			return g.callDelegated(g.delegated.next, v)
		})
		if !done {
			return res
		} else {
			v = res
		}
	}
	if g.state != genStateSuspendedYieldRes {
		v = nil
	}
	g.state = genStateExecuting
	return g.step(g.gen.next(v))
}

func (g *generatorObject) throw(v Value) Value {
	g.validate()
	if g.state == genStateSuspendedStart {
		g.state = genStateCompleted
	}
	if g.state == genStateCompleted {
		panic(v)
	}
	if d := g.delegated; d != nil {
		res, done := g.tryCallDelegated(func() (Value, bool) {
			method := toMethod(g.delegated.iterator.self.getStr("throw", nil))
			if method != nil {
				return g.callDelegated(method, v)
			}
			g.delegated = nil
			d.returnIter()
			panic(g.val.runtime.NewTypeError("The iterator does not provide a 'throw' method"))
		})
		if !done {
			return res
		}
		if g.state != genStateSuspendedYieldRes {
			res = nil
		}
		g.state = genStateExecuting
		return g.step(g.gen.next(res))
	}
	g.state = genStateExecuting
	return g.step(g.gen.nextThrow(v))
}

func (g *generatorObject) _return(v Value) Value {
	g.validate()
	if g.state == genStateSuspendedStart {
		g.state = genStateCompleted
	}

	if g.state == genStateCompleted {
		return g.val.runtime.createIterResultObject(v, true)
	}

	if d := g.delegated; d != nil {
		res, done := g.tryCallDelegated(func() (Value, bool) {
			method := toMethod(g.delegated.iterator.self.getStr("return", nil))
			if method != nil {
				return g.callDelegated(method, v)
			}
			g.delegated = nil
			return v, true
		})
		if !done {
			return res
		} else {
			v = res
		}
	}

	g.gen.returning = v
	g.state = genStateExecuting
	g.gen.enterNext()
	canContinue := g.gen.enterNextFinallyFrame()
	if !canContinue {
		vm := g.gen.vm
		g.state = genStateCompleted

		vm.popTryFrame()

		ex := vm.restoreStacks(g.gen.iterStackLen, g.gen.refStackLen)

		if ex != nil {
			panic(ex)
		}

		vm.callStack = vm.callStack[:len(vm.callStack)-1]
		vm.sp = vm.sb - 1
		vm.popCtx()

		return g.val.runtime.createIterResultObject(v, true)
	}
	res, done, ex := g.gen.step()
	vm := g.gen.vm
	vm.popTryFrame()
	vm.popCtx()

	return g.step(res, done, ex)
}

func (f *baseJsFuncObject) generatorCall(vmCall func(*vm, int), nArgs int) Value {
	o := &Object{runtime: f.val.runtime}

	genObj := &generatorObject{
		baseObject: baseObject{
			class:      classObject,
			val:        o,
			extensible: true,
		},
	}
	o.self = genObj
	genObj.init(vmCall, nArgs)
	genObj.prototype = o.runtime.getPrototypeFromCtor(f.val, nil, o.runtime.getGeneratorPrototype())
	return o
}

func (f *baseJsFuncObject) generatorVmCall(vmCall func(*vm, int), nArgs int) {
	vm := f.val.runtime.vm
	vm.push(f.generatorCall(vmCall, nArgs))
	vm.pc++
}

func (f *generatorFuncObject) vmCall(_ *vm, nArgs int) {
	f.generatorVmCall(f.baseJsFuncObject.vmCall, nArgs)
}

func (f *generatorFuncObject) Call(call FunctionCall) Value {
	f.prepareForVmCall(call)
	return f.generatorCall(f.baseJsFuncObject.vmCall, len(call.Arguments))
}

func (f *generatorFuncObject) assertCallable() (func(FunctionCall) Value, bool) {
	return f.Call, true
}

func (f *generatorFuncObject) export(*objectExportCtx) interface{} {
	return f.Call
}

func (f *generatorFuncObject) assertConstructor() func(args []Value, newTarget *Object) *Object {
	return nil
}

func (f *generatorMethodFuncObject) vmCall(_ *vm, nArgs int) {
	f.generatorVmCall(f.methodFuncObject.vmCall, nArgs)
}

func (f *generatorMethodFuncObject) Call(call FunctionCall) Value {
	f.prepareForVmCall(call)
	return f.generatorCall(f.methodFuncObject.vmCall, len(call.Arguments))
}

func (f *generatorMethodFuncObject) assertCallable() (func(FunctionCall) Value, bool) {
	return f.Call, true
}

func (f *generatorMethodFuncObject) export(*objectExportCtx) interface{} {
	return f.Call
}
