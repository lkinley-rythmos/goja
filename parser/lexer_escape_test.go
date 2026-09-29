package parser

import (
	"fmt"
	"testing"
	"unicode/utf16"

	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/unistring"
)

func TestLexerCodePointEscapes(t *testing.T) {
	// Every code point escape up to U+10FFFF is one character (two UTF-16
	// units above U+FFFF), with or without leading zeros, in a string and a
	// template: the scan counted "\u{10FFFF}" as three and the decode
	// panicked out of the parser. A regexp literal's body is kept as written.
	for cp := rune(0); cp <= 0x10ffff; cp += 0x101 {
		for _, cp := range []rune{cp, 0x10ffff, 0xffff, 0x10000} {
			units := []uint16{unistring.BOM, uint16(cp)}
			if cp > 0xffff {
				hi, lo := utf16.EncodeRune(cp)
				units = []uint16{unistring.BOM, uint16(hi), uint16(lo)}
			}
			want := unistring.FromUtf16(units)
			if cp < 0x80 {
				want = unistring.String(string(cp))
			}
			for _, esc := range []string{fmt.Sprintf(`\u{%x}`, cp), fmt.Sprintf(`\u{000%X}`, cp)} {
				for _, src := range []string{`"` + esc + `"`, "`" + esc + "`"} {
					program, err := ParseFile(nil, "", src, 0)
					if err != nil {
						t.Fatalf("%s: %v", src, err)
					}
					var got unistring.String
					switch e := program.Body[0].(*ast.ExpressionStatement).Expression.(type) {
					case *ast.StringLiteral:
						got = e.Value
					case *ast.TemplateLiteral:
						got = e.Elements[0].Parsed
					}
					if got != want {
						t.Fatalf("%s: %q, want %q", src, got, want)
					}
				}
			}
		}
	}
	if _, err := ParseFile(nil, "", `/\u{10FFFF}[\u{10FFFF}]/u; /\u{110000}/`, 0); err != nil {
		t.Fatal(err)
	}
}

func TestParseStringLiteralUncountedUnicode(t *testing.T) {
	// A literal decoded as ASCII that turns out to have a character that is
	// not is decoded again as UTF-16, whatever the scan counted.
	for _, tc := range []struct {
		literal string
		want    unistring.String
	}{
		{`é`, unistring.FromUtf16([]uint16{unistring.BOM, 0xe9})},
		{`a\u{1F600}`, unistring.FromUtf16([]uint16{unistring.BOM, 'a', 0xd83d, 0xde00})},
		{`\x41`, "A"},
	} {
		got, err := parseStringLiteral(tc.literal, 1, false, false)
		if err != "" || got != tc.want {
			t.Errorf("%s: %q, %q", tc.literal, got, err)
		}
	}
}
