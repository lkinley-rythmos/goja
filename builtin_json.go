package goja

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const hex = "0123456789abcdef"

func (r *Runtime) builtinJSON_parse(call FunctionCall) Value {
	value := r.builtinJSON_parseSource(call.Argument(0).toString())

	var reviver func(FunctionCall) Value

	if arg1 := call.Argument(1); arg1 != _undefined {
		reviver, _ = arg1.ToObject(r).self.assertCallable()
	}

	if reviver != nil {
		root := r.NewObject()
		createDataPropertyOrThrow(root, stringEmpty, value)
		return r.builtinJSON_reviveWalk(reviver, root, stringEmpty)
	}

	return value
}

type jsonReviver struct {
	r       *Runtime
	reviver func(FunctionCall) Value
	n       int
}

func (r *Runtime) builtinJSON_reviveWalk(reviver func(FunctionCall) Value, holder *Object, name Value) Value {
	w := jsonReviver{r: r, reviver: reviver}
	return w.walk(holder, name)
}

func (w *jsonReviver) walk(holder *Object, name Value) Value {
	r := w.r
	if w.n%interruptCheckInterval == 0 {
		r.checkInterrupt()
	}
	w.n++
	value := nilSafe(holder.get(name, nil))

	if object, ok := value.(*Object); ok {
		r.enterNative()
		if isArray(object) {
			length := toLength(object.self.getStr("length", nil))
			for index := int64(0); index < length; index++ {
				name := asciiString(strconv.FormatInt(index, 10))
				value := w.walk(object, name)
				if value == _undefined {
					object.delete(name, false)
				} else {
					createDataProperty(object, name, value)
				}
			}
		} else {
			for _, name := range object.self.stringKeys(false, nil) {
				value := w.walk(object, name)
				if value == _undefined {
					object.self.deleteStr(name.string(), false)
				} else {
					createDataProperty(object, name, value)
				}
			}
		}
		r.leaveNative()
	}
	return w.reviver(FunctionCall{
		This:      holder,
		Arguments: []Value{name, value},
	})
}

type _builtinJSON_stringifyContext struct {
	r                *Runtime
	stack            objectStack
	n                int   // values visited, for interrupt checks
	charged          int64 // output bytes charged to the allocation counter
	propertyList     []Value
	replacerFunction func(FunctionCall) Value
	gap, indent      string
	buf              bytes.Buffer
	allAscii         bool
}

func (r *Runtime) builtinJSON_stringify(call FunctionCall) Value {
	ctx := _builtinJSON_stringifyContext{
		r:        r,
		allAscii: true,
	}

	replacer, _ := call.Argument(1).(*Object)
	if replacer != nil {
		if isArray(replacer) {
			length := toLength(replacer.self.getStr("length", nil))
			seen := map[string]bool{}
			propertyList := make([]Value, length)
			length = 0
			for index := range propertyList {
				var name string
				value := replacer.self.getIdx(valueInt(int64(index)), nil)
				switch v := value.(type) {
				case valueFloat, valueInt, String:
					name = value.String()
				case *Object:
					switch v.self.className() {
					case classNumber, classString:
						name = value.String()
					default:
						continue
					}
				default:
					continue
				}
				if seen[name] {
					continue
				}
				seen[name] = true
				propertyList[length] = newStringValue(name)
				length += 1
			}
			ctx.propertyList = propertyList[0:length]
		} else if c, ok := replacer.self.assertCallable(); ok {
			ctx.replacerFunction = c
		}
	}
	if spaceValue := call.Argument(2); spaceValue != _undefined {
		if o, ok := spaceValue.(*Object); ok {
			switch oImpl := o.self.(type) {
			case *primitiveValueObject:
				switch oImpl.pValue.(type) {
				case valueInt, valueFloat:
					spaceValue = o.ToNumber()
				}
			case *stringObject:
				spaceValue = o.ToString()
			}
		}
		isNum := false
		var num int64
		if i, ok := spaceValue.(valueInt); ok {
			num = int64(i)
			isNum = true
		} else if f, ok := spaceValue.(valueFloat); ok {
			num = int64(f)
			isNum = true
		}
		if isNum {
			if num > 0 {
				if num > 10 {
					num = 10
				}
				ctx.gap = strings.Repeat(" ", int(num))
			}
		} else {
			if s, ok := spaceValue.(String); ok {
				str := s.String()
				if len(str) > 10 {
					ctx.gap = str[:10]
				} else {
					ctx.gap = str
				}
			}
		}
	}

	if ctx.do(call.Argument(0)) {
		ctx.checkpoint()
		// The result is a copy of the buffer.
		r.countAlloc(int64(ctx.buf.Len()))
		if ctx.allAscii {
			return asciiString(ctx.buf.String())
		} else {
			return &importedString{
				s: ctx.buf.String(),
			}
		}
	}
	return _undefined
}

func (ctx *_builtinJSON_stringifyContext) do(v Value) bool {
	holder := ctx.r.NewObject()
	createDataPropertyOrThrow(holder, stringEmpty, v)
	return ctx.str(stringEmpty, holder)
}

func (ctx *_builtinJSON_stringifyContext) str(key Value, holder *Object) bool {
	if ctx.n%interruptCheckInterval == 0 {
		ctx.checkpoint()
	}
	ctx.n++
	value := nilSafe(holder.get(key, nil))

	switch value.(type) {
	case *Object, *valueBigInt:
		if toJSON, ok := ctx.r.getVStr(value, "toJSON").(*Object); ok {
			if c, ok := toJSON.self.assertCallable(); ok {
				value = c(FunctionCall{
					This:      value,
					Arguments: []Value{key},
				})
			}
		}
	}

	if ctx.replacerFunction != nil {
		value = ctx.replacerFunction(FunctionCall{
			This:      holder,
			Arguments: []Value{key, value},
		})
	}

	if o, ok := value.(*Object); ok {
		switch o1 := o.self.(type) {
		case *primitiveValueObject:
			switch pValue := o1.pValue.(type) {
			case valueInt, valueFloat:
				value = o.ToNumber()
			default:
				value = pValue
			}
		case *stringObject:
			value = o.toString()
		case *objectGoReflect:
			if o1.toJson != nil {
				value = ctx.r.ToValue(o1.toJson())
			} else if v, ok := o1.origValue.Interface().(json.Marshaler); ok {
				b, err := v.MarshalJSON()
				if err != nil {
					panic(ctx.r.NewGoError(err))
				}
				ctx.buf.Write(b)
				ctx.allAscii = false
				return true
			} else {
				switch o1.className() {
				case classNumber:
					value = o1.val.ordinaryToPrimitiveNumber()
				case classString:
					value = o1.val.ordinaryToPrimitiveString()
				case classBoolean:
					if o.ToInteger() != 0 {
						value = valueTrue
					} else {
						value = valueFalse
					}
				}
				if o1.exportType() == typeBigInt {
					value = o1.val.ordinaryToPrimitiveNumber()
				}
			}
		}
	}

	switch value1 := value.(type) {
	case valueBool:
		if value1 {
			ctx.buf.WriteString("true")
		} else {
			ctx.buf.WriteString("false")
		}
	case String:
		// Charge a string before copying it into the output: one value can
		// be as long as the longest string.
		if n := int64(value1.Length()) + 2; n > 1024 {
			ctx.r.countAlloc(n)
			ctx.charged += n
			if int64(ctx.buf.Len())+n > maxStringLength {
				ctx.r.throwInvalidStringLength()
			}
		}
		ctx.quote(value1)
	case valueInt:
		ctx.buf.WriteString(value.String())
	case valueFloat:
		if !math.IsNaN(float64(value1)) && !math.IsInf(float64(value1), 0) {
			ctx.buf.WriteString(value.String())
		} else {
			ctx.buf.WriteString("null")
		}
	case valueNull:
		ctx.buf.WriteString("null")
	case *valueBigInt:
		ctx.r.typeErrorResult(true, "Do not know how to serialize a BigInt")
	case *Object:
		if value1.self.className() == classRawJSON {
			ctx.buf.WriteString(value1.self.getStr("rawJSON", nil).String())
			return true
		}
		if _, ok := value1.self.assertCallable(); ok {
			return false
		}
		if !ctx.stack.push(value1) {
			ctx.r.typeErrorResult(true, "Converting circular structure to JSON")
		}
		ctx.r.enterNative()
		if isArray(value1) {
			ctx.ja(value1)
		} else {
			ctx.jo(value1)
		}
		ctx.r.leaveNative()
		ctx.stack.pop()
	default:
		return false
	}
	return true
}

// checkpoint observes a pending Runtime.Interrupt and charges the output
// written since the last checkpoint to the allocation counter.
func (ctx *_builtinJSON_stringifyContext) checkpoint() {
	r := ctx.r
	if l := int64(ctx.buf.Len()); l > ctx.charged {
		// The buffer holds UTF-8: while it is ASCII its length is the
		// result's; otherwise it is at most 3 bytes per code unit.
		if l > maxStringLength && (ctx.allAscii || l > 3*maxStringLength) {
			r.throwInvalidStringLength()
		}
		r.countAlloc(l - ctx.charged)
		ctx.charged = l
	}
	r.checkInterrupt()
}

func (ctx *_builtinJSON_stringifyContext) ja(array *Object) {
	var stepback string
	if ctx.gap != "" {
		stepback = ctx.indent
		ctx.indent += ctx.gap
	}
	length := toLength(array.self.getStr("length", nil))
	if length == 0 {
		ctx.buf.WriteString("[]")
		return
	}

	ctx.buf.WriteByte('[')
	var separator string
	if ctx.gap != "" {
		ctx.buf.WriteByte('\n')
		ctx.buf.WriteString(ctx.indent)
		separator = ",\n" + ctx.indent
	} else {
		separator = ","
	}

	for i := int64(0); i < length; i++ {
		if !ctx.str(asciiString(strconv.FormatInt(i, 10)), array) {
			ctx.buf.WriteString("null")
		}
		if i < length-1 {
			ctx.buf.WriteString(separator)
		}
	}
	if ctx.gap != "" {
		ctx.buf.WriteByte('\n')
		ctx.buf.WriteString(stepback)
		ctx.indent = stepback
	}
	ctx.buf.WriteByte(']')
}

func (ctx *_builtinJSON_stringifyContext) jo(object *Object) {
	var stepback string
	if ctx.gap != "" {
		stepback = ctx.indent
		ctx.indent += ctx.gap
	}

	ctx.buf.WriteByte('{')
	mark := ctx.buf.Len()
	var separator string
	if ctx.gap != "" {
		ctx.buf.WriteByte('\n')
		ctx.buf.WriteString(ctx.indent)
		separator = ",\n" + ctx.indent
	} else {
		separator = ","
	}

	var props []Value
	if ctx.propertyList == nil {
		props = object.self.stringKeys(false, nil)
	} else {
		props = ctx.propertyList
	}

	empty := true
	for _, name := range props {
		off := ctx.buf.Len()
		if !empty {
			ctx.buf.WriteString(separator)
		}
		ctx.quote(name.toString())
		if ctx.gap != "" {
			ctx.buf.WriteString(": ")
		} else {
			ctx.buf.WriteByte(':')
		}
		if ctx.str(name, object) {
			if empty {
				empty = false
			}
		} else {
			ctx.buf.Truncate(off)
		}
	}

	if empty {
		ctx.buf.Truncate(mark)
	} else {
		if ctx.gap != "" {
			ctx.buf.WriteByte('\n')
			ctx.buf.WriteString(stepback)
			ctx.indent = stepback
		}
	}
	ctx.buf.WriteByte('}')
}

func (ctx *_builtinJSON_stringifyContext) quote(str String) {
	ctx.buf.WriteByte('"')
	reader := &lenientUtf16Decoder{utf16Reader: str.utf16Reader()}
	for {
		r, _, err := reader.ReadRune()
		if err != nil {
			break
		}
		switch r {
		case '"', '\\':
			ctx.buf.WriteByte('\\')
			ctx.buf.WriteByte(byte(r))
		case 0x08:
			ctx.buf.WriteString(`\b`)
		case 0x09:
			ctx.buf.WriteString(`\t`)
		case 0x0A:
			ctx.buf.WriteString(`\n`)
		case 0x0C:
			ctx.buf.WriteString(`\f`)
		case 0x0D:
			ctx.buf.WriteString(`\r`)
		default:
			if r < 0x20 {
				ctx.buf.WriteString(`\u00`)
				ctx.buf.WriteByte(hex[r>>4])
				ctx.buf.WriteByte(hex[r&0xF])
			} else {
				if utf16.IsSurrogate(r) {
					ctx.buf.WriteString(`\u`)
					ctx.buf.WriteByte(hex[r>>12])
					ctx.buf.WriteByte(hex[(r>>8)&0xF])
					ctx.buf.WriteByte(hex[(r>>4)&0xF])
					ctx.buf.WriteByte(hex[r&0xF])
				} else {
					ctx.buf.WriteRune(r)
					if ctx.allAscii && r >= utf8.RuneSelf {
						ctx.allAscii = false
					}
				}
			}
		}
	}
	ctx.buf.WriteByte('"')
}

func (r *Runtime) builtinJSON_rawJSON(call FunctionCall) Value {
	arg := call.Argument(0)

	var jsonString String
	switch dd := arg.(type) {
	case String:
		jsonString = dd
		if jsonString.Length() == 0 {
			panic(r.newSyntaxError("\"\" is unacceptable as raw JSON"))
		}
		first := jsonString.CharAt(0)
		last := jsonString.CharAt(jsonString.Length() - 1)
		if first == '{' || first == '[' ||
			first == ' ' || first == '\n' || first == '\r' || first == '\t' ||
			last == ' ' || last == '\n' || last == '\r' || last == '\t' {
			panic(r.newSyntaxError("\"" + arg.String() + "\" is unacceptable as raw JSON"))
		}
		if !json.Valid([]byte(jsonString.String())) {
			panic(r.newSyntaxError("\"" + arg.String() + "\" is not a valid JSON"))
		}
	case valueBool, valueInt, *valueBigInt, valueFloat, valueNull:
		jsonString = dd.toString()
	case *Symbol:
		panic(r.NewTypeError("Cannot convert a Symbol value to a string"))
	default:
		panic(r.newSyntaxError(arg.String() + " is not a valid JSON"))
	}

	o := r.newBaseObject(nil, classRawJSON)
	o._putProp("rawJSON", jsonString, false, true, false)
	o.preventExtensions(true)
	return o.val
}

func (r *Runtime) builtinJSON_isRawJSON(call FunctionCall) Value {
	arg := call.Argument(0)
	if o, ok := arg.(*Object); ok {
		if o.self.className() == classRawJSON {
			return valueTrue
		}
	}
	return valueFalse
}

func (r *Runtime) getJSON() *Object {
	ret := r.global.JSON
	if ret == nil {
		JSON := r.newBaseObject(r.global.ObjectPrototype, classObject)
		ret = JSON.val
		r.global.JSON = ret
		JSON._putProp("parse", r.newNativeFunc(r.builtinJSON_parse, "parse", 2), true, false, true)
		JSON._putProp("stringify", r.newNativeFunc(r.builtinJSON_stringify, "stringify", 3), true, false, true)
		JSON._putProp("rawJSON", r.newNativeFunc(r.builtinJSON_rawJSON, "rawJSON", 1), true, false, true)
		JSON._putProp("isRawJSON", r.newNativeFunc(r.builtinJSON_isRawJSON, "isRawJSON", 1), true, false, true)
		JSON._putSym(SymToStringTag, valueProp(asciiString(classJSON), false, false, true))
	}
	return ret
}
