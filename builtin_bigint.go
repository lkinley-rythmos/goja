package goja

import (
	"fmt"
	"hash/maphash"
	"math"
	"math/big"
	"math/bits"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
	"unsafe"

	"github.com/dop251/goja/internal/bigdec"
	"github.com/dop251/goja/unistring"
)

type valueBigInt big.Int

func (v *valueBigInt) ToInteger() int64 {
	v.ToNumber()
	return 0
}

// toString, string and String write v with big.Int.Text, neither
// interruptible nor charged (bigIntTextHost): they are for the host, as
// Value.String called from Go outside a run. A script's conversions go
// through the runtime (Runtime.toStringOf, toStringValue, toPropertyKey,
// noSideEffectsToString: bigString).
func (v *valueBigInt) toString() String {
	return asciiString(bigIntTextHost(v))
}

func (v *valueBigInt) string() unistring.String {
	return unistring.String(bigIntTextHost(v))
}

func (v *valueBigInt) ToString() Value {
	return v
}

func (v *valueBigInt) String() string {
	return bigIntTextHost(v)
}

// bigIntTextHost is v in decimal for the host (see toString).
func bigIntTextHost(v *valueBigInt) string {
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
		bigInt, err := stringToBigIntHost(o.toTrimmedUTF8())
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
	// The words themselves, not a copy of them (Bytes): a BigInt near V8's
	// limit was copied, 128 MB, uncharged, at each Map or Set operation.
	if w := (*big.Int)(v).Bits(); len(w) > 0 {
		_, _ = hash.Write(unsafe.Slice((*byte)(unsafe.Pointer(&w[0])), len(w)*bits.UintSize/8))
	}
	h := hash.Sum64()
	hash.Reset()
	return h
}

func (r *Runtime) toBigInt(value Value) *valueBigInt {
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
		// Charged by stringToBigInt, before it is made.
		bigInt, err := r.stringToBigIntOf(prim)
		if err != nil {
			panic(r.bigIntConversionError(prim))
		}
		return (*valueBigInt)(bigInt)
	case valueBool:
		return r.newBigInt(big.NewInt(prim.ToInteger()))
	case *Symbol:
		panic(r.symbolToBigIntError(prim))
	case *Object:
		return r.toBigInt(prim.toPrimitiveNumber())
	default:
		panic(typeError(fmt.Sprintf("Cannot convert %s to a BigInt", prim)))
	}
}

// numberToBigInt is BigInt(v): ToPrimitive with hint number, then
// NumberToBigInt for a number and ToBigInt for anything else, a string
// included (an object's string was converted through Number).
func (r *Runtime) numberToBigInt(v Value) *valueBigInt {
	if o, ok := v.(*Object); ok {
		v = o.toPrimitiveNumber()
	}
	switch v := v.(type) {
	case valueInt:
		return r.newBigInt(big.NewInt(v.ToInteger()))
	case valueFloat:
		if IsInfinity(v) || math.IsNaN(float64(v)) {
			panic(rangeError(fmt.Sprintf("The number %s cannot be converted to a BigInt because it is not an integer", v)))
		}
		if f := big.NewFloat(float64(v)); f.IsInt() {
			n, _ := f.Int(nil)
			return r.newBigInt(n)
		}
		panic(rangeError(fmt.Sprintf("The number %s cannot be converted to a BigInt because it is not an integer", v)))
	}
	return r.toBigInt(v)
}

// bigFloatCmp is f.Cmp(y), f a number that is not NaN, without copying y
// into a big.Float when y is longer than any finite number: a number of
// at most 1,024 bits compares below a positive y and above a negative one.
func bigFloatCmp(f *big.Float, y *big.Int) int {
	if f.IsInf() {
		return f.Sign()
	}
	if y.BitLen() > 1100 {
		return -y.Sign()
	}
	return f.Cmp(new(big.Float).SetInt(y))
}

// newBigInt is z as a BigInt value, charged to the allocation counter (its
// digits and its header): for the small BigInts made from a boolean, a
// number or 64 bits, the ones an operation makes being charged before they
// are made (chargeBigInt).
func (r *Runtime) newBigInt(z *big.Int) *valueBigInt {
	r.chargeBigIntDigits(int64(bigDigits(z)))
	return (*valueBigInt)(z)
}

// stringToBigInt is StringToBigInt (ECMA-262 7.1.14) of str, trimmed of
// white space: an optionally signed decimal integer, with leading zeros if
// any, or a 0x, 0o or 0b prefixed unsigned one, "" being 0n. A string of
// more digits than V8 takes for a BigInt (bigdec.TooLong) is refused, before
// it is converted, as one that is not a number is. A long decimal string is
// converted with r's interrupt checked between the parts, and the result
// is charged before it is made. Every conversion a script makes goes
// through it.
func (r *Runtime) stringToBigInt(str string) (*big.Int, error) {
	return stringToBigIntWith(str, r)
}

// stringToBigIntOf is stringToBigInt of s trimmed of white space
// (bigIntSource).
func (r *Runtime) stringToBigIntOf(s String) (*big.Int, error) {
	src, ok := r.bigIntSource(s)
	if !ok {
		return nil, strconv.ErrSyntax
	}
	return r.stringToBigInt(src)
}

// bigIntSource is s trimmed of white space, as StringToBigInt reads it, or
// false if what is left has a character that is not ASCII, which no BigInt
// has. A long string is read a block of bigIntScanBlock characters at a
// time, its trimming included, with the interrupt checked between them:
// toTrimmedUTF8 made a UTF-8 copy of the whole of a string that is not
// ASCII first, uninterruptibly, and trimmed an ASCII one in one step. A
// string imported from Go is read in its UTF-8, without making its code
// units (which scanned all of it at once).
func (r *Runtime) bigIntSource(s String) (string, bool) {
	switch s := s.(type) {
	case asciiString:
		return r.trimSpaceUTF8(string(s)), true
	case *importedString:
		// What is left is checked by the conversion, which refuses any
		// character but a digit, a sign or a prefix.
		return r.trimSpaceUTF8(s.s), true
	}
	i, j := 0, s.Length()
	for i < j && isWhiteSpaceUnit(s.CharAt(i)) {
		i++
		if i%bigIntScanBlock == 0 {
			r.vm.checkInterrupt()
		}
	}
	for j > i && isWhiteSpaceUnit(s.CharAt(j-1)) {
		j--
		if j%bigIntScanBlock == 0 {
			r.vm.checkInterrupt()
		}
	}
	for k := i; k < j; k++ {
		if (k-i)%bigIntScanBlock == 0 {
			r.vm.checkInterrupt()
		}
		if s.CharAt(k) >= utf8.RuneSelf {
			return "", false
		}
	}
	// The copy is charged before it is made, as the + operator charges a
	// string, and made at once: grown a block at a time, it took up to
	// twice its length more.
	r.countAlloc(allocSize(int64(j - i)))
	var b strings.Builder
	b.Grow(j - i)
	for k := i; k < j; k++ {
		if (k-i)%bigIntScanBlock == 0 {
			r.vm.checkInterrupt()
		}
		b.WriteByte(byte(s.CharAt(k)))
	}
	return b.String(), true
}

// bigIntScanBlock is how many characters bigIntSource and the conversion
// read between two interrupt checks (about 1 ms).
const bigIntScanBlock = 1 << 20

// trimSpaceUTF8 is s, UTF-8, trimmed of ECMAScript white space, a block of
// characters at a time, with the interrupt checked between them.
func (r *Runtime) trimSpaceUTF8(s string) string {
	i, j, n := 0, len(s), 0
	for i < j {
		c, size := rune(s[i]), 1
		if c >= utf8.RuneSelf {
			c, size = utf8.DecodeRuneInString(s[i:])
		}
		if c > 0xffff || !isWhiteSpaceUnit(uint16(c)) {
			break
		}
		i += size
		if n++; n%bigIntScanBlock == 0 {
			r.vm.checkInterrupt()
		}
	}
	for j > i {
		c, size := rune(s[j-1]), 1
		if c >= utf8.RuneSelf {
			c, size = utf8.DecodeLastRuneInString(s[i:j])
		}
		if c > 0xffff || !isWhiteSpaceUnit(uint16(c)) {
			break
		}
		j -= size
		if n++; n%bigIntScanBlock == 0 {
			r.vm.checkInterrupt()
		}
	}
	return s[i:j]
}

// isWhiteSpaceUnit reports whether c is ECMAScript WhiteSpace or a
// LineTerminator (parser.WhitespaceChars).
func isWhiteSpaceUnit(c uint16) bool {
	switch c {
	case ' ', '\f', '\n', '\r', '\t', '\v', 0x00a0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return c >= 0x2000 && c <= 0x200a
}

// stringToBigIntHost is stringToBigInt for Value.Equals called by the host
// outside a run, where there is neither an interrupt to check nor a counter
// to charge: a script's == goes through Runtime.looseEquals, and its other
// conversions through Runtime.stringToBigInt.
func stringToBigIntHost(str string) (*big.Int, error) {
	return stringToBigIntWith(str, nil)
}

// stringToBigIntWith is the conversion of stringToBigInt and
// stringToBigIntHost, with r nil for the latter.
func stringToBigIntWith(str string, r *Runtime) (*big.Int, error) {
	var check func()
	if r != nil {
		check = r.vm.checkInterrupt
	}
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
			if !allDigits(digits, base, check) || bigdec.TooLongWith(digits, base, check) {
				return nil, strconv.ErrSyntax
			}
			if r != nil {
				r.chargeBigIntDigits(int64(bitsToDigits(int64(len(digits)) * int64(bits.TrailingZeros(uint(base))))))
			}
			z, _ := bigdec.ParsePow2With(digits, base, check)
			return z, nil
		}
	}
	digits := str
	if str[0] == '+' || str[0] == '-' {
		digits = str[1:]
	}
	if digits == "" || !allDigits(digits, 10, check) || bigdec.TooLongWith(digits, 10, check) {
		return nil, strconv.ErrSyntax
	}
	if r != nil {
		// log2(10) < 3.33 bits a digit. A long string is converted in
		// parts, as many words as the result in all (bigdec), and their
		// products are charged as they are made.
		n := int64(bitsToDigits(int64(len(digits)) * 333 / 100))
		r.startBigOp()
		r.chargeBigIntDigits(n)
		if len(digits) > 18 {
			r.chargeBigIntDigits(n)
		}
	}
	var z *big.Int
	switch {
	case len(digits) <= 18:
		n, _ := strconv.ParseInt(digits, 10, 64)
		z = big.NewInt(n)
	case r == nil:
		z, _ = bigdec.Parse(digits)
	default:
		z, _ = bigdec.ParseWith(digits, r.bigMulPart, r.vm.checkInterrupt)
	}
	if str[0] == '-' {
		z.Neg(z)
	}
	return z, nil
}

// allDigits reports whether s is digits in base (2, 8, 10 or 16) and
// nothing else, calling check (if not nil) between blocks of characters.
func allDigits(s string, base int, check func()) bool {
	for i := 0; i < len(s); i++ {
		if check != nil && i%(1<<20) == 0 {
			check()
		}
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

// symbolToBigIntError is the TypeError for BigInt(sym), whose message has
// all of sym's description, which may be as long as a string can be: made
// as a String by joinText, so that it is charged, interruptible, and keeps
// the description's code units as they are.
func (r *Runtime) symbolToBigIntError(sym *Symbol) *Object {
	desc := sym.desc
	if desc == nil {
		desc = stringEmpty
	}
	msg := r.joinText(asciiString("Cannot convert Symbol("), desc, asciiString(") to a BigInt"))
	return r.builtin_new(r.getTypeError(), []Value{msg})
}

// bigIntConversionError is the SyntaxError for a string that is not a
// BigInt, whose text V8 cuts at 1,000 UTF-16 code units, a surrogate pair
// split if it is there, and which is made a String, so that the half kept
// stays what it was.
func (r *Runtime) bigIntConversionError(s String) *Object {
	if is, ok := s.(*importedString); ok && !is.scanned {
		// Only the units that can be in the message are made: making
		// them all scanned all of the string at once.
		s = is.prefixUnits(1001)
	}
	text := s
	if s.Length() > 1000 {
		text = s.Substring(0, 1000).Concat(newStringValue("\u2026"))
	}
	msg := asciiString("Cannot convert ").Concat(text).Concat(asciiString(" to a BigInt"))
	return r.builtin_new(r.getSyntaxError(), []Value{msg})
}

// bigIntConversionErrorHost is bigIntConversionError without a runtime, for
// toBigIntConverted.
func bigIntConversionErrorHost(s String) syntaxError {
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
	bigint := r.toBigInt(call.Argument(1))
	return (*valueBigInt)(r.bigAsIntN(bits, (*big.Int)(bigint)))
}

func (r *Runtime) bigint_asUintN(call FunctionCall) Value {
	bits := r.bigIntBitsIndex(call.Argument(0))
	bigint := r.toBigInt(call.Argument(1))
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
		return r.numberToBigInt(call.Arguments[0])
	}
	return r.newBigInt(new(big.Int))
}

func (r *Runtime) builtin_newBigInt(args []Value, newTarget *Object) *Object {
	if newTarget != nil {
		panic(r.NewTypeError("BigInt is not a constructor"))
	}
	var v Value
	if len(args) > 0 {
		v = r.numberToBigInt(args[0])
	} else {
		v = r.newBigInt(new(big.Int))
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

// bigIntStringPair reports whether x == y can compare a BigInt with a
// string, as they are or once an object among them is a primitive: only
// such a pair needs the runtime; any other compares as it did.
func bigIntStringPair(x, y Value) bool {
	switch x.(type) {
	case *valueBigInt, *Object:
		switch y.(type) {
		case *valueBigInt, String, *Object:
			return true
		}
	case String:
		switch y.(type) {
		case *valueBigInt, *Object:
			return true
		}
	}
	return false
}

// looseEqualsConverted is x == y (IsLooselyEqual) for a pair
// bigIntStringPair reports: Value.Equals, but a BigInt and a string, as they
// are or once an object among them is converted to a primitive as Equals
// converts it, are compared with the string converted by r
// (Runtime.stringToBigInt). The == and != instructions call it only for
// such a pair.
func (r *Runtime) looseEqualsConverted(x, y Value) bool {
	for {
		switch xv := x.(type) {
		case *valueBigInt:
			switch yv := y.(type) {
			case String:
				return r.bigIntEqualsString(xv, yv)
			case *Object:
				y = yv.toPrimitiveNumber()
				continue
			}
		case String:
			switch yv := y.(type) {
			case *valueBigInt:
				return r.bigIntEqualsString(yv, xv)
			case *Object:
				y = yv.toPrimitive()
				continue
			}
		case *Object:
			switch y.(type) {
			case *valueBigInt, String:
				x = xv.toPrimitive()
				continue
			}
		}
		return x.Equals(y)
	}
}

func (r *Runtime) bigIntEqualsString(b *valueBigInt, s String) bool {
	z, err := r.stringToBigIntOf(s)
	return err == nil && z.Cmp((*big.Int)(b)) == 0
}
