package parser

import (
	"strings"

	"github.com/dop251/goja/file"
	"unicode/utf16"
	"unicode/utf8"
)

// ConvertRegExpToUnicode is a pattern with the u flag as TransformRegExp2
// takes it: the \uXXXX\uXXXX escapes of a surrogate pair are the character.
func ConvertRegExpToUnicode(patternStr string) string {
	var sb strings.Builder
	pos := 0
	for i := 0; i < len(patternStr)-11; {
		r, size := utf8.DecodeRuneInString(patternStr[i:])
		if r == '\\' {
			i++
			if patternStr[i] == 'u' && patternStr[i+5] == '\\' && patternStr[i+6] == 'u' {
				if first, ok := decodeHex4(patternStr[i+1 : i+5]); ok {
					if utf16.IsSurrogate(first) && first < 0xDC00 {
						if second, ok := decodeHex4(patternStr[i+7 : i+11]); ok {
							if second >= 0xDC00 && second <= 0xDFFF {
								r = utf16.DecodeRune(first, second)
								sb.WriteString(patternStr[pos : i-1])
								sb.WriteRune(r)
								i += 11
								pos = i
								continue
							}
						}
					}
				}
			}
			i++
		} else {
			i += size
		}
	}
	if pos > 0 {
		sb.WriteString(patternStr[pos:])
		return sb.String()
	}
	return patternStr
}

// ConvertRegExpToUTF16 is a pattern without the u flag as TransformRegExp2
// takes it: a character above U+FFFF is its surrogate pair, \uXXXX\uXXXX.
func ConvertRegExpToUTF16(patternStr string) string {
	var sb strings.Builder
	pos := 0
	escaped := false
	for i := 0; i < len(patternStr); {
		r, size := utf8.DecodeRuneInString(patternStr[i:])
		if r > 0xFFFF {
			if escaped {
				// An identity escape of the first surrogate: the character
				// itself.
				sb.WriteString(patternStr[pos : i-1])
			} else {
				sb.WriteString(patternStr[pos:i])
			}
			first, second := utf16.EncodeRune(r)
			sb.WriteString(`\u`)
			writeHex4(&sb, first)
			sb.WriteString(`\u`)
			writeHex4(&sb, second)
			pos = i + size
		}
		i += size
		escaped = r == '\\' && !escaped
	}
	if pos > 0 {
		sb.WriteString(patternStr[pos:])
		return sb.String()
	}
	return patternStr
}

func decodeHex4(s string) (rune, bool) {
	var n rune
	for i := 0; i < len(s); i++ {
		d := digitValue(rune(s[i]))
		if d >= 16 {
			return 0, false
		}
		n = n*16 + rune(d)
	}
	return n, true
}

func writeHex4(b *strings.Builder, r rune) {
	const hex = "0123456789abcdef"
	b.WriteByte(hex[r>>12])
	b.WriteByte(hex[(r>>8)&0xF])
	b.WriteByte(hex[(r>>4)&0xF])
	b.WriteByte(hex[r&0xF])
}

// RegExpSourceError is err with the pattern its message shows as source, the
// pattern as written: TransformRegExp2 saw it converted.
func RegExpSourceError(err error, converted, source string) error {
	if converted == source {
		return err
	}
	prefix := "Invalid regular expression: /" + converted + "/: "
	if msg := err.Error(); strings.HasPrefix(msg, prefix) {
		return regexpParseError{err: "Invalid regular expression: /" + source + "/: " + msg[len(prefix):]}
	}
	return err
}

// validateRegExpLiteral reports a syntax error in a regular expression
// literal's pattern when the literal is parsed, as V8 does, before any error
// after it; its flags are valid. Whether the pattern is too large to compile
// is left to the compiler.
func (self *_parser) validateRegExpLiteral(idx file.Idx, pattern, flags string) {
	var dotAll, unicode, ignoreCase bool
	for i := 0; i < len(flags); i++ {
		switch flags[i] {
		case 's':
			dotAll = true
		case 'u':
			unicode = true
		case 'i':
			ignoreCase = true
		}
	}
	converted := pattern
	if unicode {
		converted = ConvertRegExpToUnicode(pattern)
	} else {
		converted = ConvertRegExpToUTF16(pattern)
	}
	if converted == "" {
		return
	}
	p := _RegExp_parser{
		str:        converted,
		length:     len(converted),
		dotAll:     dotAll,
		unicode:    unicode,
		ignoreCase: ignoreCase,
	}
	p.read()
	p.scan()
	if p.err == nil && p.deferred != "" {
		p.syntaxError(p.deferred)
	}
	if p.err != nil {
		self.error(idx, "%s", RegExpSourceError(p.err, converted, pattern).Error())
	}
}
