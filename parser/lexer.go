package parser

import (
	"errors"
	"math/big"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/unicode/rangetable"

	"github.com/dop251/goja/file"
	"github.com/dop251/goja/token"
	"github.com/dop251/goja/unistring"
)

var (
	unicodeRangeIdNeg      = rangetable.Merge(unicode.Pattern_Syntax, unicode.Pattern_White_Space)
	unicodeRangeIdStartPos = rangetable.Merge(unicode.Letter, unicode.Nl, unicode.Other_ID_Start)
	unicodeRangeIdContPos  = rangetable.Merge(unicodeRangeIdStartPos, unicode.Mn, unicode.Mc, unicode.Nd, unicode.Pc, unicode.Other_ID_Continue)
)

func isDecimalDigit(chr rune) bool {
	return '0' <= chr && chr <= '9'
}

func IsIdentifier(s string) bool {
	if s == "" {
		return false
	}
	r, size := utf8.DecodeRuneInString(s)
	if !isIdentifierStart(r) {
		return false
	}
	for _, r := range s[size:] {
		if !isIdentifierPart(r) {
			return false
		}
	}
	return true
}

func digitValue(chr rune) int {
	switch {
	case '0' <= chr && chr <= '9':
		return int(chr - '0')
	case 'a' <= chr && chr <= 'f':
		return int(chr - 'a' + 10)
	case 'A' <= chr && chr <= 'F':
		return int(chr - 'A' + 10)
	}
	return 16 // Larger than any legal digit value
}

func isDigit(chr rune, base int) bool {
	return digitValue(chr) < base
}

func isIdStartUnicode(r rune) bool {
	return unicode.Is(unicodeRangeIdStartPos, r) && !unicode.Is(unicodeRangeIdNeg, r)
}

func isIdPartUnicode(r rune) bool {
	return unicode.Is(unicodeRangeIdContPos, r) && !unicode.Is(unicodeRangeIdNeg, r) || r == '\u200C' || r == '\u200D'
}

func isIdentifierStart(chr rune) bool {
	return chr == '$' || chr == '_' || chr == '\\' ||
		'a' <= chr && chr <= 'z' || 'A' <= chr && chr <= 'Z' ||
		chr >= utf8.RuneSelf && isIdStartUnicode(chr)
}

func isIdentifierPart(chr rune) bool {
	return chr == '$' || chr == '_' || chr == '\\' ||
		'a' <= chr && chr <= 'z' || 'A' <= chr && chr <= 'Z' ||
		'0' <= chr && chr <= '9' ||
		chr >= utf8.RuneSelf && isIdPartUnicode(chr)
}

func (self *_parser) scanIdentifier() (string, unistring.String, bool, string) {
	offset := self.chrOffset
	hasEscape := false
	isUnicode := false
	length := 0
	for isIdentifierPart(self.chr) {
		r := self.chr
		length++
		if r == '\\' {
			hasEscape = true
			value, n, err := identifierEscape(self.str[self.chrOffset:])
			if err != "" {
				self.read() // always make progress
				return "", "", false, err
			}
			if n == 0 || value == '\\' || self.chrOffset == offset && !isIdentifierStart(value) || self.chrOffset > offset && !isIdentifierPart(value) {
				// The token ends here, illegal. V8 has scanned the next one
				// by the time it reports it, and reports a malformed escape
				// there instead.
				next := self.chrOffset + max(n, 1)
				self.offset = next
				self.read() // always make progress
				if next < len(self.str) {
					if err := identifierEscapeError(self.str[next:]); err != "" {
						return "", "", false, err
					}
				}
				return "", "", false, errInvalidToken
			}
			self.offset = self.chrOffset + n - 1
			self.read()
			r = value
		}
		if r >= utf8.RuneSelf {
			isUnicode = true
			if r > 0xFFFF {
				length++
			}
		}
		self.read()
	}

	literal := self.str[offset:self.chrOffset]
	var parsed unistring.String
	if hasEscape || isUnicode {
		var err string
		parsed, err = parseStringLiteral(literal, length, isUnicode, false)
		if err != "" {
			return "", "", false, err
		}
	} else {
		parsed = unistring.String(literal)
	}

	return literal, parsed, hasEscape, ""
}

// identifierEscape decodes the escape of an identifier s starts with (a
// backslash), and returns its length: 0 if it is not a \u escape, with the
// error V8 reports if it is a malformed one.
func identifierEscape(s string) (value rune, n int, err string) {
	if len(s) < 2 || s[1] != 'u' {
		return 0, 0, ""
	}
	if len(s) > 2 && s[2] == '{' {
		i := 3
		for ; i < len(s) && s[i] != '}'; i++ {
			decimal, ok := hex2decimal(s[i])
			if !ok {
				return 0, 0, errUnicodeEscape
			}
			value = value<<4 | decimal
			if value > utf8.MaxRune {
				return 0, 0, errUndefinedCodePoint
			}
		}
		if i == 3 || i == len(s) {
			return 0, 0, errUnicodeEscape
		}
		return value, i + 1, ""
	}
	if len(s) < 6 {
		return 0, 0, errUnicodeEscape
	}
	for i := 2; i < 6; i++ {
		decimal, ok := hex2decimal(s[i])
		if !ok {
			return 0, 0, errUnicodeEscape
		}
		value = value<<4 | decimal
	}
	return value, 6, ""
}

// identifierEscapeError returns the error V8 reports for a malformed escape
// in the identifier s starts with, if any, before the identifier ends.
func identifierEscapeError(s string) string {
	for i := 0; i < len(s); {
		if s[i] == '\\' {
			value, n, err := identifierEscape(s[i:])
			if err != "" {
				return err
			}
			if n == 0 || value == '\\' || i == 0 && !isIdentifierStart(value) || i > 0 && !isIdentifierPart(value) {
				return ""
			}
			i += n
			continue
		}
		r, w := utf8.DecodeRuneInString(s[i:])
		if i == 0 && !isIdentifierStart(r) || i > 0 && !isIdentifierPart(r) {
			return ""
		}
		i += w
	}
	return ""
}

// 7.2
func isLineWhiteSpace(chr rune) bool {
	switch chr {
	case '\u0009', '\u000b', '\u000c', '\u0020', '\u00a0', '\ufeff':
		return true
	case '\u000a', '\u000d', '\u2028', '\u2029':
		return false
	case '\u0085':
		return false
	}
	return unicode.IsSpace(chr)
}

// 7.3
func isLineTerminator(chr rune) bool {
	switch chr {
	case '\u000a', '\u000d', '\u2028', '\u2029':
		return true
	}
	return false
}

type parserState struct {
	idx                                file.Idx
	tok                                token.Token
	literal                            string
	parsedLiteral                      unistring.String
	implicitSemicolon, insertSemicolon bool
	chr                                rune
	chrOffset, offset                  int
	errorCount                         int
}

func (self *_parser) mark(state *parserState) *parserState {
	if state == nil {
		state = &parserState{}
	}
	state.idx, state.tok, state.literal, state.parsedLiteral, state.implicitSemicolon, state.insertSemicolon, state.chr, state.chrOffset, state.offset =
		self.idx, self.token, self.literal, self.parsedLiteral, self.implicitSemicolon, self.insertSemicolon, self.chr, self.chrOffset, self.offset

	state.errorCount = len(self.errors)
	return state
}

func (self *_parser) restore(state *parserState) {
	self.idx, self.token, self.literal, self.parsedLiteral, self.implicitSemicolon, self.insertSemicolon, self.chr, self.chrOffset, self.offset =
		state.idx, state.tok, state.literal, state.parsedLiteral, state.implicitSemicolon, state.insertSemicolon, state.chr, state.chrOffset, state.offset
	self.errors = self.errors[:state.errorCount]
}

func (self *_parser) peek() token.Token {
	implicitSemicolon, insertSemicolon, chr, chrOffset, offset := self.implicitSemicolon, self.insertSemicolon, self.chr, self.chrOffset, self.offset
	tok, _, _, _ := self.scan()
	self.implicitSemicolon, self.insertSemicolon, self.chr, self.chrOffset, self.offset = implicitSemicolon, insertSemicolon, chr, chrOffset, offset
	return tok
}

func (self *_parser) scan() (tkn token.Token, literal string, parsedLiteral unistring.String, idx file.Idx) {

	self.implicitSemicolon = false
	// At the start of the input, or after a line terminator, --> starts a
	// comment.
	self.lineStart = self.chrOffset == 0

	for {
		self.skipWhiteSpace()

		idx = self.idxOf(self.chrOffset)
		insertSemicolon := false

		switch chr := self.chr; {
		case isIdentifierStart(chr):
			var err string
			var hasEscape bool
			literal, parsedLiteral, hasEscape, err = self.scanIdentifier()
			if err != "" {
				self.error(idx, err)
				tkn = token.ILLEGAL
				break
			}
			if len(parsedLiteral) > 1 {
				// Keywords are longer than 1 character, avoid lookup otherwise
				var strict bool
				tkn, strict = token.IsKeyword(string(parsedLiteral))
				if hasEscape {
					self.insertSemicolon = true
					if tkn == 0 || self.isBindingId(tkn) {
						tkn = token.IDENTIFIER
					} else {
						tkn = token.ESCAPED_RESERVED_WORD
					}
					return
				}
				switch tkn {
				case 0: // Not a keyword
					// no-op
				case token.KEYWORD:
					if strict {
						// TODO If strict and in strict mode, then this is not a break
						break
					}
					return

				case
					token.BOOLEAN,
					token.NULL,
					token.THIS,
					token.BREAK,
					token.THROW, // A newline after a throw is not allowed, but we need to detect it
					token.YIELD,
					token.RETURN,
					token.CONTINUE,
					token.DEBUGGER:
					self.insertSemicolon = true
					return

				case token.ASYNC:
					// async only has special meaning if not followed by a LineTerminator
					if self.skipWhiteSpaceCheckLineTerminator() {
						self.insertSemicolon = true
						tkn = token.IDENTIFIER
					}
					return
				default:
					return

				}
			}
			self.insertSemicolon = true
			tkn = token.IDENTIFIER
			return
		case '0' <= chr && chr <= '9':
			self.insertSemicolon = true
			tkn, literal = self.scanNumericLiteral(false)
			return
		default:
			self.read()
			switch chr {
			case -1:
				if self.insertSemicolon {
					self.insertSemicolon = false
					self.implicitSemicolon = true
				}
				tkn = token.EOF
			case '\r', '\n', '\u2028', '\u2029':
				self.insertSemicolon = false
				self.implicitSemicolon = true
				self.lineStart = true
				continue
			case ':':
				tkn = token.COLON
			case '.':
				if digitValue(self.chr) < 10 {
					insertSemicolon = true
					tkn, literal = self.scanNumericLiteral(true)
				} else {
					if self.chr == '.' {
						self.read()
						if self.chr == '.' {
							self.read()
							tkn = token.ELLIPSIS
						} else {
							tkn = token.ILLEGAL
						}
					} else {
						tkn = token.PERIOD
					}
				}
			case ',':
				tkn = token.COMMA
			case ';':
				tkn = token.SEMICOLON
			case '(':
				tkn = token.LEFT_PARENTHESIS
			case ')':
				tkn = token.RIGHT_PARENTHESIS
				insertSemicolon = true
			case '[':
				tkn = token.LEFT_BRACKET
			case ']':
				tkn = token.RIGHT_BRACKET
				insertSemicolon = true
			case '{':
				tkn = token.LEFT_BRACE
			case '}':
				tkn = token.RIGHT_BRACE
				insertSemicolon = true
			case '+':
				tkn = self.switch3(token.PLUS, token.ADD_ASSIGN, '+', token.INCREMENT)
				if tkn == token.INCREMENT {
					insertSemicolon = true
				}
			case '-':
				if self.lineStart && self.chr == '-' && self._peek() == '>' {
					// A SingleLineHTMLCloseComment (Annex B.1.1).
					self.skipSingleLineComment()
					continue
				}
				tkn = self.switch3(token.MINUS, token.SUBTRACT_ASSIGN, '-', token.DECREMENT)
				if tkn == token.DECREMENT {
					insertSemicolon = true
				}
			case '*':
				if self.chr == '*' {
					self.read()
					tkn = self.switch2(token.EXPONENT, token.EXPONENT_ASSIGN)
				} else {
					tkn = self.switch2(token.MULTIPLY, token.MULTIPLY_ASSIGN)
				}
			case '/':
				if self.chr == '/' {
					self.skipSingleLineComment()
					continue
				} else if self.chr == '*' {
					if self.skipMultiLineComment() {
						self.insertSemicolon = false
						self.implicitSemicolon = true
						self.lineStart = true
					}
					continue
				} else {
					// Could be division, could be RegExp literal
					tkn = self.switch2(token.SLASH, token.QUOTIENT_ASSIGN)
					insertSemicolon = true
				}
			case '%':
				tkn = self.switch2(token.REMAINDER, token.REMAINDER_ASSIGN)
			case '^':
				tkn = self.switch2(token.EXCLUSIVE_OR, token.EXCLUSIVE_OR_ASSIGN)
			case '<':
				if self.chr == '!' && strings.HasPrefix(self.str[self.offset:], "--") {
					// A SingleLineHTMLOpenComment (Annex B.1.1).
					self.skipSingleLineComment()
					continue
				}
				tkn = self.switch4(token.LESS, token.LESS_OR_EQUAL, '<', token.SHIFT_LEFT, token.SHIFT_LEFT_ASSIGN)
			case '>':
				tkn = self.switch6(token.GREATER, token.GREATER_OR_EQUAL, '>', token.SHIFT_RIGHT, token.SHIFT_RIGHT_ASSIGN, '>', token.UNSIGNED_SHIFT_RIGHT, token.UNSIGNED_SHIFT_RIGHT_ASSIGN)
			case '=':
				if self.chr == '>' {
					self.read()
					if self.implicitSemicolon {
						tkn = token.ILLEGAL
					} else {
						tkn = token.ARROW
					}
				} else {
					tkn = self.switch2(token.ASSIGN, token.EQUAL)
					if tkn == token.EQUAL && self.chr == '=' {
						self.read()
						tkn = token.STRICT_EQUAL
					}
				}
			case '!':
				tkn = self.switch2(token.NOT, token.NOT_EQUAL)
				if tkn == token.NOT_EQUAL && self.chr == '=' {
					self.read()
					tkn = token.STRICT_NOT_EQUAL
				}
			case '&':
				tkn = self.switch4(token.AND, token.AND_ASSIGN, '&', token.LOGICAL_AND, token.LOGICAL_AND_ASSIGN)
			case '|':
				tkn = self.switch4(token.OR, token.OR_ASSIGN, '|', token.LOGICAL_OR, token.LOGICAL_OR_ASSIGN)
			case '~':
				tkn = token.BITWISE_NOT
			case '?':
				if self.chr == '.' && !isDecimalDigit(self._peek()) {
					self.read()
					tkn = token.QUESTION_DOT
				} else if self.chr == '?' {
					self.read()
					if self.chr == '=' {
						self.read()
						tkn = token.COALESCE_ASSIGN
					} else {
						tkn = token.COALESCE
					}
				} else {
					tkn = token.QUESTION_MARK
				}
			case '"', '\'':
				insertSemicolon = true
				tkn = token.STRING
				var err string
				literal, parsedLiteral, err = self.scanString(self.chrOffset-1, true)
				if err != "" {
					// scanString has reported the error in V8's words.
					tkn = token.ILLEGAL
				}
			case '`':
				tkn = token.BACKTICK
			case '#':
				if self.chrOffset == 1 && self.chr == '!' {
					self.skipSingleLineComment()
					continue
				}

				var err string
				literal, parsedLiteral, _, err = self.scanIdentifier()
				if err != "" || literal == "" {
					tkn = token.ILLEGAL
					break
				}
				self.insertSemicolon = true
				tkn = token.PRIVATE_IDENTIFIER
				return
			default:
				self.errorUnexpected(idx, chr)
				tkn = token.ILLEGAL
			}
		}
		self.insertSemicolon = insertSemicolon
		return
	}
}

func (self *_parser) switch2(tkn0, tkn1 token.Token) token.Token {
	if self.chr == '=' {
		self.read()
		return tkn1
	}
	return tkn0
}

func (self *_parser) switch3(tkn0, tkn1 token.Token, chr2 rune, tkn2 token.Token) token.Token {
	if self.chr == '=' {
		self.read()
		return tkn1
	}
	if self.chr == chr2 {
		self.read()
		return tkn2
	}
	return tkn0
}

func (self *_parser) switch4(tkn0, tkn1 token.Token, chr2 rune, tkn2, tkn3 token.Token) token.Token {
	if self.chr == '=' {
		self.read()
		return tkn1
	}
	if self.chr == chr2 {
		self.read()
		if self.chr == '=' {
			self.read()
			return tkn3
		}
		return tkn2
	}
	return tkn0
}

func (self *_parser) switch6(tkn0, tkn1 token.Token, chr2 rune, tkn2, tkn3 token.Token, chr3 rune, tkn4, tkn5 token.Token) token.Token {
	if self.chr == '=' {
		self.read()
		return tkn1
	}
	if self.chr == chr2 {
		self.read()
		if self.chr == '=' {
			self.read()
			return tkn3
		}
		if self.chr == chr3 {
			self.read()
			if self.chr == '=' {
				self.read()
				return tkn5
			}
			return tkn4
		}
		return tkn2
	}
	return tkn0
}

func (self *_parser) _peek() rune {
	if self.offset < self.length {
		return rune(self.str[self.offset])
	}
	return -1
}

func (self *_parser) read() {
	if self.offset < self.length {
		self.chrOffset = self.offset
		chr, width := rune(self.str[self.offset]), 1
		if chr >= utf8.RuneSelf { // !ASCII
			chr, width = utf8.DecodeRuneInString(self.str[self.offset:])
			if chr == utf8.RuneError && width == 1 {
				self.error(self.chrOffset, "Invalid UTF-8 character")
			}
		}
		self.offset += width
		self.chr = chr
	} else {
		self.chrOffset = self.length
		self.chr = -1 // EOF
	}
}

func (self *_parser) skipSingleLineComment() {
	for self.chr != -1 {
		self.read()
		if isLineTerminator(self.chr) {
			return
		}
	}
}

func (self *_parser) skipMultiLineComment() (hasLineTerminator bool) {
	self.read()
	for self.chr >= 0 {
		chr := self.chr
		if chr == '\r' || chr == '\n' || chr == '\u2028' || chr == '\u2029' {
			hasLineTerminator = true
			break
		}
		self.read()
		if chr == '*' && self.chr == '/' {
			self.read()
			return
		}
	}
	for self.chr >= 0 {
		chr := self.chr
		self.read()
		if chr == '*' && self.chr == '/' {
			self.read()
			return
		}
	}

	// V8 reports a comment left open as an illegal token.
	self.error(file.Idx(0), errInvalidToken)
	return
}

func (self *_parser) skipWhiteSpaceCheckLineTerminator() bool {
	for {
		switch self.chr {
		case ' ', '\t', '\f', '\v', '\u00a0', '\ufeff':
			self.read()
			continue
		case '\r':
			if self._peek() == '\n' {
				self.read()
			}
			fallthrough
		case '\u2028', '\u2029', '\n':
			return true
		}
		if self.chr >= utf8.RuneSelf {
			if unicode.IsSpace(self.chr) {
				self.read()
				continue
			}
		}
		break
	}
	return false
}

func (self *_parser) skipWhiteSpace() {
	for {
		switch self.chr {
		case ' ', '\t', '\f', '\v', '\u00a0', '\ufeff':
			self.read()
			continue
		case '\r':
			if self._peek() == '\n' {
				self.read()
			}
			fallthrough
		case '\u2028', '\u2029', '\n':
			if self.insertSemicolon {
				return
			}
			self.lineStart = true
			self.read()
			continue
		}
		if self.chr >= utf8.RuneSelf {
			if unicode.IsSpace(self.chr) {
				self.read()
				continue
			}
		}
		break
	}
}

func (self *_parser) scanMantissa(base int, allowSeparator bool) {
	for digitValue(self.chr) < base || (allowSeparator && self.chr == '_') {
		afterUnderscore := self.chr == '_'
		self.read()
		if afterUnderscore && !isDigit(self.chr, base) {
			if self.chr == '_' {
				self.error(self.chrOffset, "Only one underscore is allowed as numeric separator")
			} else {
				self.error(self.chrOffset, "Numeric separators are not allowed at the end of numeric literals")
			}
		}
	}
}

func (self *_parser) scanEscape(quote rune) (int, bool) {

	var length, base uint32
	chr := self.chr
	switch chr {
	case '0', '1', '2', '3', '4', '5', '6', '7':
		//    Octal:
		length, base = 3, 8
	case 'a', 'b', 'f', 'n', 'r', 't', 'v', '\\', '"', '\'':
		self.read()
		return 1, false
	case '\r':
		self.read()
		if self.chr == '\n' {
			self.read()
			return 2, false
		}
		return 1, false
	case '\n':
		self.read()
		return 1, false
	case '\u2028', '\u2029':
		self.read()
		return 1, true
	case 'x':
		self.read()
		length, base = 2, 16
	case 'u':
		self.read()
		if self.chr == '{' {
			self.read()
			length, base = 0, 16
		} else {
			length, base = 4, 16
		}
	default:
		self.read() // Always make progress
	}

	if base > 0 {
		var value uint32
		if length > 0 {
			for ; length > 0 && self.chr != quote && self.chr >= 0; length-- {
				digit := uint32(digitValue(self.chr))
				if digit >= base {
					break
				}
				value = value*base + digit
				self.read()
			}
		} else {
			// \u{...}: every hex digit, and the closing brace. A value past
			// U+10FFFF is an error (see parseStringLiteral), and saturates.
			for {
				digit := uint32(digitValue(self.chr))
				if digit >= base {
					break
				}
				if value <= utf8.MaxRune {
					value = value*base + digit
				}
				self.read()
			}
			if self.chr == '}' {
				self.read()
			}
		}
		chr = rune(value)
	}
	if chr >= utf8.RuneSelf {
		if chr > 0xFFFF {
			return 2, true
		}
		return 1, true
	}
	return 1, false
}

func (self *_parser) scanString(offset int, parse bool) (literal string, parsed unistring.String, err string) {
	// " ' /
	quote := rune(self.str[offset])
	length := 0
	isUnicode := false
	inClass := false // in a regular expression's class, where a slash does not end it
	for self.chr != quote || inClass {
		chr := self.chr
		if chr == '\n' || chr == '\r' || chr < 0 {
			goto newline
		}
		if quote == '/' && (self.chr == '\u2028' || self.chr == '\u2029') {
			goto newline
		}
		self.read()
		if chr == '\\' {
			if self.chr == '\n' || self.chr == '\r' || self.chr == '\u2028' || self.chr == '\u2029' || self.chr < 0 {
				if quote == '/' {
					goto newline
				}
				self.scanNewline()
			} else {
				l, u := self.scanEscape(quote)
				length += l
				if u {
					isUnicode = true
				}
			}
			continue
		} else if chr == '[' && quote == '/' {
			inClass = true
		} else if chr == ']' && inClass {
			inClass = false
		}
		if chr >= utf8.RuneSelf {
			isUnicode = true
			if chr > 0xFFFF {
				length++
			}
		}
		length++
	}

	// " ' /
	self.read()
	literal = self.str[offset:self.chrOffset]
	if parse {
		// Strict mode's octal escapes are refused by the compiler, which
		// knows the mode.
		parsed, err = parseStringLiteral(literal[1:len(literal)-1], length, isUnicode, false)
		if err != "" {
			self.error(self.idxOf(offset), err)
		}
	}
	return

newline:
	errStr := errInvalidToken
	if quote == '/' {
		self.scanNewline()
		errStr = "Invalid regular expression: missing /"
		self.error(self.idxOf(offset), errStr)
		return "", "", errStr
	}
	// V8 reports a malformed escape before the end of the line.
	if _, e := parseStringLiteral(self.str[offset+1:self.chrOffset], length, true, false); e != "" && e != errInvalidToken {
		errStr = e
	}
	self.scanNewline()
	self.error(self.idxOf(offset), errStr)
	return "", "", errStr
}

func (self *_parser) scanNewline() {
	if self.chr == '\u2028' || self.chr == '\u2029' {
		self.read()
		return
	}
	if self.chr == '\r' {
		self.read()
		if self.chr != '\n' {
			return
		}
	}
	self.read()
}

func (self *_parser) parseTemplateCharacters() (literal string, parsed unistring.String, finished bool, parseErr, err string) {
	offset := self.chrOffset
	var end int
	length := 0
	isUnicode := false
	hasCR := false
	for {
		chr := self.chr
		if chr < 0 {
			goto unterminated
		}
		self.read()
		if chr == '`' {
			finished = true
			end = self.chrOffset - 1
			break
		}
		if chr == '\\' {
			if self.chr == '\n' || self.chr == '\r' || self.chr == '\u2028' || self.chr == '\u2029' || self.chr < 0 {
				if self.chr == '\r' {
					hasCR = true
				}
				self.scanNewline()
			} else {
				l, u := self.scanEscape('`')
				length += l
				if u {
					isUnicode = true
				}
			}
			continue
		}
		if chr == '$' && self.chr == '{' {
			self.read()
			end = self.chrOffset - 2
			break
		}
		if chr >= utf8.RuneSelf {
			isUnicode = true
			if chr > 0xFFFF {
				length++
			}
		} else if chr == '\r' {
			hasCR = true
			if self.chr == '\n' {
				length--
			}
		}
		length++
	}
	literal = self.str[offset:end]
	if hasCR {
		literal = normaliseCRLF(literal)
	}
	if parseErr == "" {
		parsed, parseErr = parseStringLiteral(literal, length, isUnicode, true)
	}
	self.insertSemicolon = true
	return
unterminated:
	// V8 reports an escape an untagged template is not allowed before the
	// end of the input (see parseTemplateLiteral).
	if _, e := parseStringLiteral(self.str[offset:], length, true, true); e != "" && e != errInvalidToken {
		parseErr = e
	}
	err = err_UnexpectedEndOfInput
	finished = true
	return
}

func normaliseCRLF(s string) string {
	var buf strings.Builder
	buf.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\r' {
			buf.WriteByte('\n')
			if i < len(s)-1 && s[i+1] == '\n' {
				i++
			}
		} else {
			buf.WriteByte(s[i])
		}
	}
	return buf.String()
}

func hex2decimal(chr byte) (value rune, ok bool) {
	{
		chr := rune(chr)
		switch {
		case '0' <= chr && chr <= '9':
			return chr - '0', true
		case 'a' <= chr && chr <= 'f':
			return chr - 'a' + 10, true
		case 'A' <= chr && chr <= 'F':
			return chr - 'A' + 10, true
		}
		return
	}
}

func parseNumberLiteral(literal string) (value interface{}, err error) {
	// TODO Is Uint okay? What about -MAX_UINT
	value, err = strconv.ParseInt(literal, 0, 64)
	if err == nil {
		return
	}

	parseIntErr := err // Save this first error, just in case

	value, err = strconv.ParseFloat(literal, 64)
	if err == nil {
		return
	} else if err.(*strconv.NumError).Err == strconv.ErrRange {
		// Infinity, etc.
		return value, nil
	}

	err = parseIntErr

	if err.(*strconv.NumError).Err == strconv.ErrRange {
		if len(literal) > 2 &&
			literal[0] == '0' && (literal[1] == 'X' || literal[1] == 'x') &&
			literal[len(literal)-1] != 'n' {
			// Could just be a very large number (e.g. 0x8000000000000000)
			var value float64
			literal = literal[2:]
			for _, chr := range literal {
				digit := digitValue(chr)
				if digit >= 16 {
					goto error
				}
				value = value*16 + float64(digit)
			}
			return value, nil
		}
	}

	if len(literal) > 1 && literal[len(literal)-1] == 'n' {
		if literal[0] == '0' {
			if len(literal) > 2 && isDecimalDigit(rune(literal[1])) {
				goto error
			}
		}
		// Parse as big.Int
		bigInt := new(big.Int)
		_, ok := bigInt.SetString(literal[:len(literal)-1], 0)
		if !ok {
			goto error
		}
		return bigInt, nil
	}

error:
	return nil, errors.New("Illegal numeric literal")
}

// V8's messages for the escapes of string literals, templates and
// identifiers.
const (
	errInvalidToken       = "Invalid or unexpected token"
	errUnicodeEscape      = "Invalid Unicode escape sequence"
	errHexEscape          = "Invalid hexadecimal escape sequence"
	errUndefinedCodePoint = "Undefined Unicode code-point"
	errTemplateOctal      = "Octal escape sequences are not allowed in template strings."
	errTemplate89         = "\\8 and \\9 are not allowed in template strings."
)

// parseStringLiteral decodes the escapes of a literal (inTemplate: a
// template's characters, where octal escapes are not allowed). length is
// the number of UTF-16 code units the scan counted, and only sizes the
// result; unicode is whether it found a character that is not ASCII (if it
// missed one the literal is decoded again as unicode).
func parseStringLiteral(literal string, length int, unicode, inTemplate bool) (unistring.String, string) {
	var sb strings.Builder
	var chars []uint16
	if unicode {
		chars = make([]uint16, 1, length+1)
		chars[0] = unistring.BOM
	} else {
		sb.Grow(length)
	}
	str := literal
	for len(str) > 0 {
		switch chr := str[0]; {
		// We do not explicitly handle the case of the quote
		// value, which can be: " ' /
		// This assumes we're already passed a partially well-formed literal
		case chr >= utf8.RuneSelf:
			if !unicode {
				return parseStringLiteral(literal, length, true, inTemplate)
			}
			chr, size := utf8.DecodeRuneInString(str)
			if chr <= 0xFFFF {
				chars = append(chars, uint16(chr))
			} else {
				first, second := utf16.EncodeRune(chr)
				chars = append(chars, uint16(first), uint16(second))
			}
			str = str[size:]
			continue
		case chr != '\\':
			if unicode {
				chars = append(chars, uint16(chr))
			} else {
				sb.WriteByte(chr)
			}
			str = str[1:]
			continue
		}

		if len(str) <= 1 {
			return "", errInvalidToken
		}
		chr := str[1]
		var value rune
		if chr >= utf8.RuneSelf {
			str = str[1:]
			var size int
			value, size = utf8.DecodeRuneInString(str)
			str = str[size:] // \ + <character>
			if value == '\u2028' || value == '\u2029' {
				continue
			}
		} else {
			str = str[2:] // \<character>
			switch chr {
			case 'b':
				value = '\b'
			case 'f':
				value = '\f'
			case 'n':
				value = '\n'
			case 'r':
				value = '\r'
			case 't':
				value = '\t'
			case 'v':
				value = '\v'
			case 'x', 'u':
				if chr == 'x' || str == "" || str[0] != '{' {
					size, msg := 2, errHexEscape
					if chr == 'u' {
						size, msg = 4, errUnicodeEscape
					}
					if len(str) < size {
						return "", msg
					}
					for j := 0; j < size; j++ {
						decimal, ok := hex2decimal(str[j])
						if !ok {
							return "", msg
						}
						value = value<<4 | decimal
					}
					str = str[size:]
					break
				}
				// \u{...}: past U+10FFFF is refused as soon as it is.
				str = str[1:]
				j := 0
				for ; j < len(str); j++ {
					decimal, ok := hex2decimal(str[j])
					if !ok {
						break
					}
					value = value<<4 | decimal
					if value > utf8.MaxRune {
						return "", errUndefinedCodePoint
					}
				}
				if j == 0 || j == len(str) || str[j] != '}' {
					return "", errUnicodeEscape
				}
				str = str[j+1:]
			case '0':
				if len(str) == 0 || '0' > str[0] || str[0] > '9' {
					value = 0
					break
				}
				fallthrough
			case '1', '2', '3', '4', '5', '6', '7':
				if inTemplate {
					return "", errTemplateOctal
				}
				value = rune(chr) - '0'
				// Up to three digits from 0 to 3, two from 4 to 7.
				j, digits := 0, 2
				if chr >= '4' {
					digits = 1
				}
				for ; j < digits; j++ {
					if len(str) < j+1 {
						break
					}
					chr := str[j]
					if '0' > chr || chr > '7' {
						break
					}
					decimal := rune(str[j]) - '0'
					value = (value << 3) | decimal
				}
				str = str[j:]
			case '8', '9':
				if inTemplate {
					return "", errTemplate89
				}
				value = rune(chr)
			case '\\':
				value = '\\'
			case '\'', '"':
				value = rune(chr)
			case '\r':
				if len(str) > 0 {
					if str[0] == '\n' {
						str = str[1:]
					}
				}
				fallthrough
			case '\n':
				continue
			default:
				value = rune(chr)
			}
		}
		if unicode {
			if value <= 0xFFFF {
				chars = append(chars, uint16(value))
			} else {
				first, second := utf16.EncodeRune(value)
				chars = append(chars, uint16(first), uint16(second))
			}
		} else {
			if value >= utf8.RuneSelf {
				return parseStringLiteral(literal, length, true, inTemplate)
			}
			sb.WriteByte(byte(value))
		}
	}

	if unicode {
		return unistring.FromUtf16(chars), ""
	}
	return unistring.String(sb.String()), ""
}

func (self *_parser) scanNumericLiteral(decimalPoint bool) (token.Token, string) {

	offset := self.chrOffset
	tkn := token.NUMBER
	// A BigInt has neither a decimal point nor an exponent.
	bigIntOK := !decimalPoint

	if decimalPoint {
		offset--
		self.scanFraction()
	} else {
		if self.chr == '0' {
			self.read()
			base := 0
			switch self.chr {
			case 'x', 'X':
				base = 16
			case 'o', 'O':
				base = 8
			case 'b', 'B':
				base = 2
			case '.', 'e', 'E':
				// no-op
			default:
				// A legacy octal literal (017), or, if a digit is 8 or 9, a
				// decimal one with leading zeros (08, 09.5), which sloppy
				// code takes (Annex B.1.1); neither is a BigInt.
				if self.chr == '_' {
					self.error(self.chrOffset, "Numeric separator can not be used after leading 0.")
				}
				legacyDecimal, legacy := false, false
				for isDecimalDigit(self.chr) {
					if self.chr >= '8' {
						legacyDecimal = true
					}
					legacy = true
					self.read()
				}
				if legacy && self.chr == 'n' {
					return token.ILLEGAL, self.str[offset:self.chrOffset]
				}
				if !legacyDecimal {
					goto end
				}
				if self.chr == '.' {
					bigIntOK = false
					self.read()
					self.scanMantissa(10, false)
				}
				goto exponent
			}
			if base > 0 {
				self.read()
				if !isDigit(self.chr, base) {
					return self.illegalNumber(offset)
				}
				self.scanMantissa(base, true)
				goto end
			}
		} else {
			self.scanMantissa(10, true)
		}
		if self.chr == '.' {
			bigIntOK = false
			self.read()
			self.scanFraction()
		}
	}

exponent:
	if self.chr == 'e' || self.chr == 'E' {
		bigIntOK = false
		self.read()
		if self.chr == '-' || self.chr == '+' {
			self.read()
		}
		if isDecimalDigit(self.chr) {
			self.read()
			self.scanMantissa(10, true)
		} else {
			return token.ILLEGAL, self.str[offset:self.chrOffset]
		}
	}
end:
	if self.chr == 'n' && bigIntOK {
		self.read()
	}
	// As in V8, a number followed at once by an identifier or a digit (1nx,
	// 1.5n, 0b1n1) is an invalid token.
	if isIdentifierStart(self.chr) || isDecimalDigit(self.chr) {
		return self.illegalNumber(offset)
	}

	return tkn, self.str[offset:self.chrOffset]
}

// illegalNumber returns the invalid token of a number from offset. V8
// scans the next token before it reports it, and so reports a separator
// misplaced in digits that follow at once (0b2_, 0o18_) rather than the
// invalid token.
func (self *_parser) illegalNumber(offset int) (token.Token, string) {
	end := self.chrOffset
	if isDecimalDigit(self.chr) {
		self.scanMantissa(10, true)
	}
	return token.ILLEGAL, self.str[offset:end]
}

// scanFraction scans the digits after a decimal point: a separator may come
// only between digits, so none may come first (1._5 is 1. and _5).
func (self *_parser) scanFraction() {
	if isDecimalDigit(self.chr) {
		self.scanMantissa(10, true)
	}
}
