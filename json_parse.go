package goja

import (
	"strconv"
	"unsafe"

	"github.com/dop251/goja/unistring"
)

// JSON.parse is a scanner of its own over the source's code units rather
// than encoding/json, for three reasons:
//
//   - Nesting depth. It is iterative, as V8's parser is, so the depth of the
//     input is limited only by memory; encoding/json's Decoder either has no
//     limit but made goja recurse (v1) or refuses more than 10000 levels (the
//     v2-based encoding/json of newer toolchains), so the result depended on
//     the Go version.
//   - Fidelity. It reads the code units the script holds, so lone surrogates
//     (in the source or from \u escapes) survive as V8's do, and a number too
//     large for a float64 is Infinity, where encoding/json reported an error.
//   - Errors. They are V8's (Node 18): "Unexpected end of JSON input",
//     "Unexpected token x in JSON at position n", and "Unexpected number" or
//     "Unexpected string" when the unexpected character starts one.

// jsonParseFrame is an array or object that JSON.parse has opened and not
// yet closed.
type jsonParseFrame struct {
	object  *Object
	array   []Value
	key     unistring.String
	isArray bool
}

var allocSizeJSONParseFrame = int64(unsafe.Sizeof(jsonParseFrame{}))

type jsonCodeUnit interface {
	byte | uint16
}

type jsonParser[T jsonCodeUnit] struct {
	r   *Runtime
	src []T
	pos int
	buf []uint16 // scratch for strings with escapes or non-ASCII characters
}

// jsonSyntaxError is the panic value of a failed parse, recovered by
// builtinJSON_parse.
type jsonSyntaxError struct {
	msg string
}

func (r *Runtime) builtinJSON_parseSource(s String) Value {
	a, u := devirtualizeString(s)
	if u == nil {
		p := jsonParser[byte]{r: r, src: unsafe.Slice(unsafe.StringData(string(a)), len(a))}
		return p.parse()
	}
	p := jsonParser[uint16]{r: r, src: u[1:]}
	return p.parse()
}

func (p *jsonParser[T]) parse() (v Value) {
	defer func() {
		if x := recover(); x != nil {
			if e, ok := x.(jsonSyntaxError); ok {
				panic(p.r.newError(p.r.getSyntaxError(), e.msg))
			}
			panic(x)
		}
	}()
	v = p.value()
	p.skipSpace()
	if p.pos < len(p.src) {
		p.unexpected()
	}
	return v
}

// unexpected reports the code unit at the current position, as V8 does.
func (p *jsonParser[T]) unexpected() {
	if p.pos >= len(p.src) {
		panic(jsonSyntaxError{"Unexpected end of JSON input"})
	}
	pos := strconv.Itoa(p.pos)
	switch c := p.src[p.pos]; {
	case c == '"':
		panic(jsonSyntaxError{"Unexpected string in JSON at position " + pos})
	case c == '-' || c >= '0' && c <= '9':
		panic(jsonSyntaxError{"Unexpected number in JSON at position " + pos})
	default:
		panic(jsonSyntaxError{"Unexpected token " + string(rune(c)) + " in JSON at position " + pos})
	}
}

func (p *jsonParser[T]) skipSpace() {
	for p.pos < len(p.src) {
		switch p.src[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

// next skips whitespace and returns the code unit there, which it reports
// as the end of input if there is none.
func (p *jsonParser[T]) next() T {
	p.skipSpace()
	if p.pos >= len(p.src) {
		p.unexpected()
	}
	return p.src[p.pos]
}

// value parses one JSON value. The containers it is inside of are an
// explicit stack, charged to the allocation counter, so the depth of the
// input does not consume the Go stack.
func (p *jsonParser[T]) value() Value {
	r := p.r
	var stackBuf [8]jsonParseFrame
	stack := stackBuf[:0]
	for n := 0; ; n++ {
		if n%interruptCheckInterval == 0 {
			r.checkInterrupt()
		}
		var value Value
		switch c := p.next(); c {
		case '{':
			p.pos++
			r.countAlloc(allocSizeJSONParseFrame)
			frame := jsonParseFrame{object: r.NewObject()}
			if p.next() == '}' {
				p.pos++
				value = frame.object
				break
			}
			frame.key = p.key()
			stack = append(stack, frame)
			continue
		case '[':
			p.pos++
			if p.next() == ']' {
				p.pos++
				value = r.newArrayValues(nil)
				break
			}
			r.countAlloc(allocSizeJSONParseFrame)
			stack = append(stack, jsonParseFrame{isArray: true})
			continue
		case '"':
			value = p.string()
		case 't':
			p.literal("true")
			value = valueTrue
		case 'f':
			p.literal("false")
			value = valueFalse
		case 'n':
			p.literal("null")
			value = _null
		default:
			if c == '-' || c >= '0' && c <= '9' {
				value = p.number()
			} else {
				p.unexpected()
			}
		}

		// Store the value in the enclosing containers, closing each one
		// that ends here.
		for {
			l := len(stack)
			if l == 0 {
				return value
			}
			top := &stack[l-1]
			if top.isArray {
				top.array = append(top.array, value)
			} else {
				top.object.self._putProp(top.key, value, true, true, true)
			}
			c := p.next()
			if c == ',' {
				p.pos++
				if !top.isArray {
					p.next()
					top.key = p.key()
				}
				break
			}
			if top.isArray && c == ']' {
				value = r.newArrayValues(top.array)
			} else if !top.isArray && c == '}' {
				value = top.object
			} else {
				p.unexpected()
			}
			p.pos++
			stack[l-1] = jsonParseFrame{}
			stack = stack[:l-1]
		}
	}
}

// key parses an object's key and the colon after it.
func (p *jsonParser[T]) key() unistring.String {
	if p.src[p.pos] != '"' {
		p.unexpected()
	}
	key := p.string().string()
	if p.next() != ':' {
		p.unexpected()
	}
	p.pos++
	return key
}

func (p *jsonParser[T]) literal(lit string) {
	for i := 0; i < len(lit); i++ {
		if p.pos >= len(p.src) || p.src[p.pos] != T(lit[i]) {
			p.unexpected()
		}
		p.pos++
	}
}

func (p *jsonParser[T]) digits() {
	start := p.pos
	for p.pos < len(p.src) && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' {
		p.pos++
	}
	if p.pos == start {
		p.unexpected()
	}
}

func (p *jsonParser[T]) number() Value {
	start := p.pos
	if p.src[p.pos] == '-' {
		p.pos++
	}
	if p.pos < len(p.src) && p.src[p.pos] == '0' {
		p.pos++
	} else {
		p.digits()
	}
	if p.pos < len(p.src) && p.src[p.pos] == '.' {
		p.pos++
		p.digits()
	}
	if p.pos < len(p.src) && (p.src[p.pos] == 'e' || p.src[p.pos] == 'E') {
		p.pos++
		if p.pos < len(p.src) && (p.src[p.pos] == '+' || p.src[p.pos] == '-') {
			p.pos++
		}
		p.digits()
	}
	var b []byte
	if bs, ok := any(p.src[start:p.pos]).([]byte); ok {
		b = bs
	} else {
		var tmp [32]byte
		b = tmp[:0]
		for _, c := range p.src[start:p.pos] {
			b = append(b, byte(c))
		}
	}
	// Out of range, ParseFloat returns the infinity or zero JSON.parse wants.
	f, _ := strconv.ParseFloat(unsafe.String(unsafe.SliceData(b), len(b)), 64)
	return floatToValue(f)
}

func hexValue[T jsonCodeUnit](c T) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// string parses a string, the current code unit being its opening quote.
func (p *jsonParser[T]) string() String {
	p.pos++
	start := p.pos
	// The common case: no escapes and, for an ASCII source, nothing to
	// convert.
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == '"' {
			s := p.src[start:p.pos]
			p.pos++
			return p.plain(s)
		}
		if c == '\\' || c < 0x20 {
			break
		}
		p.pos++
	}
	buf := p.buf[:0]
	for i := start; i < p.pos; i++ {
		buf = append(buf, uint16(p.src[i]))
	}
	for {
		if p.pos >= len(p.src) {
			p.unexpected()
		}
		c := p.src[p.pos]
		switch {
		case c == '"':
			p.pos++
			p.buf = buf
			return p.fromUnits(buf)
		case c < 0x20:
			p.unexpected()
		case c != '\\':
			buf = append(buf, uint16(c))
			p.pos++
			continue
		}
		p.pos++
		if p.pos >= len(p.src) {
			p.unexpected()
		}
		switch p.src[p.pos] {
		case '"':
			buf = append(buf, '"')
		case '\\':
			buf = append(buf, '\\')
		case '/':
			buf = append(buf, '/')
		case 'b':
			buf = append(buf, '\b')
		case 'f':
			buf = append(buf, '\f')
		case 'n':
			buf = append(buf, '\n')
		case 'r':
			buf = append(buf, '\r')
		case 't':
			buf = append(buf, '\t')
		case 'u':
			v := 0
			for i := 0; i < 4; i++ {
				p.pos++
				if p.pos >= len(p.src) {
					p.unexpected()
				}
				h := hexValue(p.src[p.pos])
				if h < 0 {
					p.unexpected()
				}
				v = v<<4 | h
			}
			buf = append(buf, uint16(v))
		default:
			p.unexpected()
		}
		p.pos++
	}
}

// plain returns the characters of a string that has no escapes.
func (p *jsonParser[T]) plain(s []T) String {
	p.r.countAlloc(int64(len(s)))
	switch s := any(s).(type) {
	case []byte:
		return asciiString(s)
	case []uint16:
		for _, c := range s {
			if c >= 0x80 {
				b := make([]uint16, len(s)+1)
				b[0] = unistring.BOM
				copy(b[1:], s)
				return unicodeString(b)
			}
		}
		b := make([]byte, len(s))
		for i, c := range s {
			b[i] = byte(c)
		}
		return asciiString(b)
	}
	panic("unreachable")
}

// fromUnits returns the string of the code units in buf.
func (p *jsonParser[T]) fromUnits(buf []uint16) String {
	p.r.countAlloc(int64(len(buf)))
	for _, c := range buf {
		if c >= 0x80 {
			b := make([]uint16, len(buf)+1)
			b[0] = unistring.BOM
			copy(b[1:], buf)
			return unicodeString(b)
		}
	}
	b := make([]byte, len(buf))
	for i, c := range buf {
		b[i] = byte(c)
	}
	return asciiString(b)
}
