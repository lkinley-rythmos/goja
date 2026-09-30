package parser

import (
	"fmt"
	"sort"

	"github.com/dop251/goja/file"
	"github.com/dop251/goja/token"
)

const (
	err_UnexpectedToken      = "Unexpected token %v"
	err_UnexpectedEndOfInput = "Unexpected end of input"
	err_UnexpectedEscape     = "Unexpected escape"
)

//    UnexpectedNumber:  'Unexpected number',
//    UnexpectedString:  'Unexpected string',
//    UnexpectedIdentifier:  'Unexpected identifier',
//    UnexpectedReserved:  'Unexpected reserved word',
//    NewlineAfterThrow:  'Illegal newline after throw',
//    InvalidRegExp: 'Invalid regular expression',
//    UnterminatedRegExp:  'Invalid regular expression: missing /',
//    InvalidLHSInAssignment:  'Invalid left-hand side in assignment',
//    InvalidLHSInForIn:  'Invalid left-hand side in for-in',
//    MultipleDefaultsInSwitch: 'More than one default clause in switch statement',
//    NoCatchOrFinally:  'Missing catch or finally after try',
//    UnknownLabel: 'Undefined label \'%0\'',
//    Redeclaration: '%0 \'%1\' has already been declared',
//    IllegalContinue: 'Illegal continue statement',
//    IllegalBreak: 'Illegal break statement',
//    IllegalReturn: 'Illegal return statement',
//    StrictModeWith:  'Strict mode code may not include a with statement',
//    StrictCatchVariable:  'Catch variable may not be eval or arguments in strict mode',
//    StrictVarName:  'Variable name may not be eval or arguments in strict mode',
//    StrictParamName:  'Parameter name eval or arguments is not allowed in strict mode',
//    StrictParamDupe: 'Strict mode function may not have duplicate parameter names',
//    StrictFunctionName:  'Function name may not be eval or arguments in strict mode',
//    StrictOctalLiteral:  'Octal literals are not allowed in strict mode.',
//    StrictDelete:  'Delete of an unqualified identifier in strict mode.',
//    StrictDuplicateProperty:  'Duplicate data property in object literal not allowed in strict mode',
//    AccessorDataProperty:  'Object literal may not have data and accessor property with the same name',
//    AccessorGetSet:  'Object literal may not have multiple get/set accessors with the same name',
//    StrictLHSAssignment:  'Assignment to eval or arguments is not allowed in strict mode',
//    StrictLHSPostfix:  'Postfix increment/decrement may not have eval or arguments operand in strict mode',
//    StrictLHSPrefix:  'Prefix increment/decrement may not have eval or arguments operand in strict mode',
//    StrictReservedWord:  'Use of future reserved word in strict mode'

// A SyntaxError is a description of an ECMAScript syntax error.

// An Error represents a parsing error. It includes the position where the error occurred and a message/description.
type Error struct {
	Position file.Position
	Message  string

	// An error V8 reports as it parses that is no error of the grammar
	// (see earlyError), and where it is in the source.
	early  bool
	offset int
}

// FIXME Should this be "SyntaxError"?

func (self Error) Error() string {
	filename := self.Position.Filename
	if filename == "" {
		filename = "(anonymous)"
	}
	return fmt.Sprintf("%s: Line %d:%d %s",
		filename,
		self.Position.Line,
		self.Position.Column,
		self.Message,
	)
}

func (self *_parser) error(place interface{}, msg string, msgValues ...interface{}) *Error {
	idx := file.Idx(0)
	switch place := place.(type) {
	case int:
		idx = self.idxOf(place)
	case file.Idx:
		if place == 0 {
			idx = self.idxOf(self.chrOffset)
		} else {
			idx = place
		}
	default:
		panic(fmt.Errorf("error(%T, ...)", place))
	}

	position := self.position(idx)
	msg = fmt.Sprintf(msg, msgValues...)
	self.errors.Add(position, msg)
	return self.errors[len(self.errors)-1]
}

// earlyError reports an error that V8 reports as it parses, where goja's
// compiler finds it only once the whole source has parsed: super where the
// code may not use it, new.target outside functions, and let as a name in
// strict mode code. It is no error of the grammar, and the parse goes on as
// if it were not one; a parse error after it comes after it, and one of
// goja's compiler before it in the source may come first (see
// EarlyErrors).
func (self *_parser) earlyError(idx file.Idx, msg string) {
	e := self.error(idx, msg)
	e.early = true
	e.offset = int(idx) - self.base
}

// EarlyErrors reports whether err, an error from ParseFile, has only errors
// V8 reports as it parses that are no errors of the grammar (see
// earlyError), and where the first is in the source: the program parsed,
// and goja's compiler may find an error before it that V8 reports first
// ("use strict"; eval = 1; super.x is "Unexpected eval or arguments in
// strict mode").
func EarlyErrors(err error) (offset int, ok bool) {
	list, isList := err.(ErrorList)
	if !isList || len(list) == 0 {
		return 0, false
	}
	for _, e := range list {
		if !e.early {
			return 0, false
		}
	}
	return list[0].offset, true
}

func (self *_parser) errorUnexpected(idx file.Idx, chr rune) error {
	if chr == -1 {
		return self.error(idx, err_UnexpectedEndOfInput)
	}
	return self.error(idx, errInvalidToken)
}

func (self *_parser) errorUnexpectedToken(tkn token.Token) error {
	switch tkn {
	case token.EOF:
		return self.error(file.Idx(0), err_UnexpectedEndOfInput)
	}
	value := tkn.String()
	switch tkn {
	case token.BOOLEAN, token.NULL:
		value = self.literal
	case token.IDENTIFIER, token.PRIVATE_IDENTIFIER:
		return self.error(self.idx, "Unexpected identifier")
	case token.KEYWORD:
		// TODO Might be a future reserved word
		switch self.literal {
		case "import", "export":
			return self.error(self.idx, err_UnexpectedToken, self.literal)
		}
		return self.error(self.idx, "Unexpected reserved word")
	case token.AWAIT:
		return self.error(self.idx, "Unexpected reserved word")
	case token.YIELD:
		return self.error(self.idx, "Unexpected identifier")
	case token.ESCAPED_RESERVED_WORD:
		return self.error(self.idx, "Keyword must not contain escaped characters")
	case token.NUMBER:
		return self.error(self.idx, "Unexpected number")
	case token.STRING:
		return self.error(self.idx, "Unexpected string")
	case token.ILLEGAL:
		// V8's message.
		return self.error(self.idx, errInvalidToken)
	}
	return self.error(self.idx, err_UnexpectedToken, value)
}

// ErrorList is a list of *Errors.
type ErrorList []*Error

// Add adds an Error with given position and message to an ErrorList.
func (self *ErrorList) Add(position file.Position, msg string) {
	*self = append(*self, &Error{Position: position, Message: msg})
}

// Reset resets an ErrorList to no errors.
func (self *ErrorList) Reset() { *self = (*self)[0:0] }

func (self ErrorList) Len() int      { return len(self) }
func (self ErrorList) Swap(i, j int) { self[i], self[j] = self[j], self[i] }
func (self ErrorList) Less(i, j int) bool {
	x := &self[i].Position
	y := &self[j].Position
	if x.Filename < y.Filename {
		return true
	}
	if x.Filename == y.Filename {
		if x.Line < y.Line {
			return true
		}
		if x.Line == y.Line {
			return x.Column < y.Column
		}
	}
	return false
}

func (self ErrorList) Sort() {
	sort.Sort(self)
}

// Error implements the Error interface.
func (self ErrorList) Error() string {
	switch len(self) {
	case 0:
		return "no errors"
	case 1:
		return self[0].Error()
	}
	return fmt.Sprintf("%s (and %d more errors)", self[0].Error(), len(self)-1)
}

// Err returns an error equivalent to this ErrorList.
// If the list is empty, Err returns nil.
func (self ErrorList) Err() error {
	if len(self) == 0 {
		return nil
	}
	return self
}
