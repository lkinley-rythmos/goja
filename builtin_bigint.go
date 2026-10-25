package goja

import (
	"fmt"
	"hash/maphash"
	"math"
	"math/big"
	"math/bits"
	"reflect"
	"strconv"
	"sync"

	"github.com/dop251/goja/internal/bigdec"
	"github.com/dop251/goja/unistring"
)

type valueBigInt big.Int

func (v *valueBigInt) ToInteger() int64 {
	v.ToNumber()
	return 0
}

func (v *valueBigInt) toString() String {
	return asciiString((*big.Int)(v).String())
}

func (v *valueBigInt) string() unistring.String {
	return unistring.String(v.String())
}

func (v *valueBigInt) ToString() Value {
	return v
}

func (v *valueBigInt) String() string {
	return (*big.Int)(v).String()
}

func (v *valueBigInt) ToFloat() float64 {
	v.ToNumber()
	return 0
}

func (v *valueBigInt) ToNumber() Value {
	panic(typeError("Cannot convert a BigInt value to a number"))
}

func (v *valueBigInt) ToBoolean() bool {
	return (*big.Int)(v).Sign() != 0
}

func (v *valueBigInt) ToObject(r *Runtime) *Object {
	return r.newPrimitiveObject(v, r.getBigIntPrototype(), classObject)
}

func (v *valueBigInt) SameAs(other Value) bool {
	if o, ok := other.(*valueBigInt); ok {
		return (*big.Int)(v).Cmp((*big.Int)(o)) == 0
	}
	return false
}

func (v *valueBigInt) Equals(other Value) bool {
	switch o := other.(type) {
	case *valueBigInt:
		return (*big.Int)(v).Cmp((*big.Int)(o)) == 0
	case valueInt:
		return (*big.Int)(v).Cmp(big.NewInt(int64(o))) == 0
	case valueFloat:
		if IsInfinity(o) || math.IsNaN(float64(o)) {
			return false
		}
		if f := big.NewFloat(float64(o)); f.IsInt() {
			i, _ := f.Int(nil)
			return (*big.Int)(v).Cmp(i) == 0
		}
		return false
	case String:
		bigInt, err := stringToBigInt(o.toTrimmedUTF8())
		if err != nil {
			return false
		}
		return bigInt.Cmp((*big.Int)(v)) == 0
	case valueBool:
		return (*big.Int)(v).Int64() == o.ToInteger()
	case *Object:
		return v.Equals(o.toPrimitiveNumber())
	}
	return false
}

func (v *valueBigInt) StrictEquals(other Value) bool {
	o, ok := other.(*valueBigInt)
	if ok {
		return (*big.Int)(v).Cmp((*big.Int)(o)) == 0
	}
	return false
}

func (v *valueBigInt) Export() interface{} {
	return new(big.Int).Set((*big.Int)(v))
}

func (v *valueBigInt) ExportType() reflect.Type {
	return typeBigInt
}

func (v *valueBigInt) baseObject(rt *Runtime) *Object {
	return rt.getBigIntPrototype()
}

func (v *valueBigInt) hash(hash *maphash.Hash) uint64 {
	var sign byte
	if (*big.Int)(v).Sign() < 0 {
		sign = 0x01
	} else {
		sign = 0x00
	}
	_ = hash.WriteByte(sign)
	_, _ = hash.Write((*big.Int)(v).Bytes())
	h := hash.Sum64()
	hash.Reset()
	return h
}

func toBigInt(value Value) *valueBigInt {
	// Undefined	Throw a TypeError exception.
	// Null			Throw a TypeError exception.
	// Boolean		Return 1n if prim is true and 0n if prim is false.
	// BigInt		Return prim.
	// Number		Throw a TypeError exception.
	// String		1. Let n be StringToBigInt(prim).
	//				2. If n is undefined, throw a SyntaxError exception.
	//				3. Return n.
	// Symbol		Throw a TypeError exception.
	switch prim := value.(type) {
	case *valueBigInt:
		return prim
	case String:
		bigInt, err := stringToBigInt(prim.toTrimmedUTF8())
		if err != nil {
			panic(bigIntConversionError(prim))
		}
		return (*valueBigInt)(bigInt)
	case valueBool:
		return (*valueBigInt)(big.NewInt(prim.ToInteger()))
	case *Symbol:
		panic(typeError("Cannot convert Symbol to a BigInt"))
	case *Object:
		return toBigInt(prim.toPrimitiveNumber())
	default:
		panic(typeError(fmt.Sprintf("Cannot convert %s to a BigInt", prim)))
	}
}

func numberToBigInt(v Value) *valueBigInt {
	switch v := toNumeric(v).(type) {
	case *valueBigInt:
		return v
	case valueInt:
		return (*valueBigInt)(big.NewInt(v.ToInteger()))
	case valueFloat:
		if IsInfinity(v) || math.IsNaN(float64(v)) {
			panic(rangeError(fmt.Sprintf("The number %s cannot be converted to a BigInt because it is not an integer", v)))
		}
		if f := big.NewFloat(float64(v)); f.IsInt() {
			n, _ := f.Int(nil)
			return (*valueBigInt)(n)
		}
		panic(rangeError(fmt.Sprintf("The number %s cannot be converted to a BigInt because it is not an integer", v)))
	case *Object:
		prim := v.toPrimitiveNumber()
		switch prim.(type) {
		case valueInt, valueFloat:
			return numberToBigInt(prim)
		default:
			return toBigInt(prim)
		}
	default:
		panic(newTypeError("Cannot convert %s to a BigInt", v))
	}
}

// stringToBigInt is StringToBigInt (ECMA-262 7.1.14) of str, trimmed of
// white space: an optionally signed decimal integer, with leading zeros if
// any, or a 0x, 0o or 0b prefixed unsigned one, "" being 0n. A string of
// more digits than V8 takes for a BigInt (bigdec.TooLong) is refused, before
// it is converted, as one that is not a number is.
func stringToBigInt(str string) (*big.Int, error) {
	return stringToBigIntWith(str, nil)
}

// stringToBigIntWith is stringToBigInt, with r's interrupt checked while a
// long decimal string is converted and the result charged before it is
// made, if r is not nil.
func stringToBigIntWith(str string, r *Runtime) (*big.Int, error) {
	if str == "" {
		if r != nil {
			r.chargeBigIntDigits(0)
		}
		return new(big.Int), nil
	}
	if len(str) > 2 && str[0] == '0' {
		base := 0
		switch str[1] {
		case 'x', 'X':
			base = 16
		case 'o', 'O':
			base = 8
		case 'b', 'B':
			base = 2
		}
		if base != 0 {
			digits := str[2:]
			if !allDigits(digits, base) || bigdec.TooLong(digits, base) {
				return nil, strconv.ErrSyntax
			}
			if r != nil {
				r.chargeBigIntDigits(int64(bitsToDigits(int64(len(digits)) * int64(bits.TrailingZeros(uint(base))))))
			}
			z, _ := bigdec.ParsePow2(digits, base)
			return z, nil
		}
	}
	digits := str
	if str[0] == '+' || str[0] == '-' {
		digits = str[1:]
	}
	if digits == "" || !allDigits(digits, 10) || bigdec.TooLong(digits, 10) {
		return nil, strconv.ErrSyntax
	}
	if r != nil {
		// log2(10) < 3.33 bits a digit.
		r.chargeBigIntDigits(int64(bitsToDigits(int64(len(digits)) * 333 / 100)))
	}
	var z *big.Int
	switch {
	case len(digits) <= 18:
		n, _ := strconv.ParseInt(digits, 10, 64)
		z = big.NewInt(n)
	case r == nil:
		z, _ = bigdec.Parse(digits)
	default:
		z, _ = bigdec.ParseWith(digits, r.mulSigned, r.vm.checkInterrupt)
	}
	if str[0] == '-' {
		z.Neg(z)
	}
	return z, nil
}

// allDigits reports whether s is digits in base (2, 8, 10 or 16) and
// nothing else.
func allDigits(s string, base int) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		var d int
		switch {
		case c >= '0' && c <= '9':
			d = int(c - '0')
		case c >= 'a' && c <= 'f':
			d = int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = int(c-'A') + 10
		default:
			return false
		}
		if d >= base {
			return false
		}
	}
	return true
}

// bigIntConversionError is the SyntaxError for a string that is not a
// BigInt: V8 cuts its text at 1,000 code units.
func bigIntConversionError(s String) syntaxError {
	text := s.String()
	if s.Length() > 1000 {
		text = s.Substring(0, 1000).String() + "\u2026"
	}
	return syntaxError(fmt.Sprintf("Cannot convert %s to a BigInt", text))
}

func (r *Runtime) thisBigIntValue(value Value) Value {
	switch t := value.(type) {
	case *valueBigInt:
		return t
	case *Object:
		switch t := t.self.(type) {
		case *primitiveValueObject:
			return r.thisBigIntValue(t.pValue)
		case *objectGoReflect:
			if t.exportType() == typeBigInt && t.valueOf != nil {
				return t.valueOf()
			}
		}
	}
	panic(r.NewTypeError("requires that 'this' be a BigInt"))
}

func (r *Runtime) bigintproto_valueOf(call FunctionCall) Value {
	return r.thisBigIntValue(call.This)
}

func (r *Runtime) bigintproto_toString(call FunctionCall) Value {
	x := (*big.Int)(r.thisBigIntValue(call.This).(*valueBigInt))
	radix := call.Argument(0)
	var radixMV int

	if radix == _undefined {
		radixMV = 10
	} else {
		radixMV = int(radix.ToInteger())
		if radixMV < 2 || radixMV > 36 {
			panic(r.newError(r.getRangeError(), "radix must be an integer between 2 and 36"))
		}
	}

	return r.bigString(x, radixMV)
}

func (r *Runtime) bigint_asIntN(call FunctionCall) Value {
	bits := r.bigIntBitsIndex(call.Argument(0))
	bigint := toBigInt(call.Argument(1))
	return (*valueBigInt)(r.bigAsIntN(bits, (*big.Int)(bigint)))
}

func (r *Runtime) bigint_asUintN(call FunctionCall) Value {
	bits := r.bigIntBitsIndex(call.Argument(0))
	bigint := toBigInt(call.Argument(1))
	return (*valueBigInt)(r.bigAsUintN(bits, (*big.Int)(bigint)))
}

// bigIntBitsIndex is ToIndex(v) for BigInt.asIntN and asUintN, with V8's
// message for a value out of its range, before the BigInt is converted.
func (r *Runtime) bigIntBitsIndex(v Value) int64 {
	f := v.ToNumber().ToFloat()
	if math.IsNaN(f) {
		return 0
	}
	f = math.Trunc(f)
	if f < 0 || f > maxInt-1 {
		panic(r.newError(r.getRangeError(), "Invalid value: not (convertible to) a safe integer"))
	}
	return int64(f)
}

var bigintTemplate *objectTemplate
var bigintTemplateOnce sync.Once

func getBigIntTemplate() *objectTemplate {
	bigintTemplateOnce.Do(func() {
		bigintTemplate = createBigIntTemplate()
	})
	return bigintTemplate
}

func createBigIntTemplate() *objectTemplate {
	t := newObjectTemplate()
	t.protoFactory = func(r *Runtime) *Object {
		return r.getFunctionPrototype()
	}

	t.putStr("name", func(r *Runtime) Value { return valueProp(asciiString("BigInt"), false, false, true) })
	t.putStr("length", func(r *Runtime) Value { return valueProp(intToValue(1), false, false, true) })
	t.putStr("prototype", func(r *Runtime) Value { return valueProp(r.getBigIntPrototype(), false, false, false) })

	t.putStr("asIntN", func(r *Runtime) Value { return r.methodProp(r.bigint_asIntN, "asIntN", 2) })
	t.putStr("asUintN", func(r *Runtime) Value { return r.methodProp(r.bigint_asUintN, "asUintN", 2) })

	return t
}

func (r *Runtime) builtin_BigInt(call FunctionCall) Value {
	if len(call.Arguments) > 0 {
		var b *valueBigInt
		switch v := call.Argument(0).(type) {
		case *valueBigInt:
			return v
		case String:
			z, err := stringToBigIntWith(v.toTrimmedUTF8(), r)
			if err != nil {
				panic(bigIntConversionError(v))
			}
			return (*valueBigInt)(z)
		case valueInt, valueFloat, *Object:
			b = numberToBigInt(v)
		default:
			b = toBigInt(v)
		}
		r.chargeBigIntDigits(int64(bigDigits((*big.Int)(b))))
		return b
	}
	r.chargeBigIntDigits(0)
	return (*valueBigInt)(big.NewInt(0))
}

func (r *Runtime) builtin_newBigInt(args []Value, newTarget *Object) *Object {
	if newTarget != nil {
		panic(r.NewTypeError("BigInt is not a constructor"))
	}
	var v Value
	if len(args) > 0 {
		v = numberToBigInt(args[0])
	} else {
		v = (*valueBigInt)(big.NewInt(0))
	}
	return r.newPrimitiveObject(v, newTarget, classObject)
}

func (r *Runtime) getBigInt() *Object {
	ret := r.global.BigInt
	if ret == nil {
		ret = &Object{runtime: r}
		r.global.BigInt = ret
		r.newTemplatedFuncObject(getBigIntTemplate(), ret, r.builtin_BigInt,
			r.wrapNativeConstruct(r.builtin_newBigInt, ret, r.getBigIntPrototype()))
	}
	return ret
}

func createBigIntProtoTemplate() *objectTemplate {
	t := newObjectTemplate()
	t.protoFactory = func(r *Runtime) *Object {
		return r.global.ObjectPrototype
	}

	t.putStr("length", func(r *Runtime) Value { return valueProp(intToValue(0), false, false, true) })
	t.putStr("name", func(r *Runtime) Value { return valueProp(asciiString("BigInt"), false, false, true) })
	t.putStr("constructor", func(r *Runtime) Value { return valueProp(r.getBigInt(), true, false, true) })

	t.putStr("toLocaleString", func(r *Runtime) Value { return r.methodProp(r.bigintproto_toString, "toLocaleString", 0) })
	t.putStr("toString", func(r *Runtime) Value { return r.methodProp(r.bigintproto_toString, "toString", 0) })
	t.putStr("valueOf", func(r *Runtime) Value { return r.methodProp(r.bigintproto_valueOf, "valueOf", 0) })
	t.putSym(SymToStringTag, func(r *Runtime) Value { return valueProp(asciiString("BigInt"), false, false, true) })

	return t
}

var bigintProtoTemplate *objectTemplate
var bigintProtoTemplateOnce sync.Once

func getBigIntProtoTemplate() *objectTemplate {
	bigintProtoTemplateOnce.Do(func() {
		bigintProtoTemplate = createBigIntProtoTemplate()
	})
	return bigintProtoTemplate
}

func (r *Runtime) getBigIntPrototype() *Object {
	ret := r.global.BigIntPrototype
	if ret == nil {
		ret = &Object{runtime: r}
		r.global.BigIntPrototype = ret
		o := r.newTemplatedObject(getBigIntProtoTemplate(), ret)
		o.class = classObject
	}
	return ret
}
