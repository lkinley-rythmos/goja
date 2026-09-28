package goja

import (
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
)

// syntaxErrorMessage is err's message as V8 words it, as far as the
// format goes: Goja reports where the error is ("<file>: Line 1:5 ...",
// "... at 1:5") and how many more it found, and does not quote the token
// of "Unexpected token x".
func syntaxErrorMessage(err error) string {
	var syntaxErr *CompilerSyntaxError
	msg := err.Error()
	if errors.As(err, &syntaxErr) {
		msg = syntaxErr.Message
	}
	msg = syntaxLocation.ReplaceAllString(msg, "")
	msg = syntaxCount.ReplaceAllString(msg, "")
	msg = syntaxAt.ReplaceAllString(msg, "")
	if token, ok := strings.CutPrefix(msg, "Unexpected token "); ok {
		switch token {
		case "ILLEGAL":
			return "Invalid or unexpected token"
		case "EOF":
			return "Unexpected end of input"
		}
		return "Unexpected token '" + token + "'"
	}
	return msg
}

var (
	syntaxLocation = regexp.MustCompile(`(?s)^.*?Line \d+:\d+ `)
	syntaxCount    = regexp.MustCompile(` \(and \d+ more errors?\)$`)
	syntaxAt       = regexp.MustCompile(` at \d+:\d+$`)
)

// TestV8SyntaxErrors compiles each script of testdata/v8_syntax_errors.json
// and compares its SyntaxError with the one Node.js 18.20.3 gives compiling
// it as a script (vm.Script), recorded in the file.
func TestV8SyntaxErrors(t *testing.T) {
	data, err := os.ReadFile("testdata/v8_syntax_errors.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases [][2]string
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		got := "ok"
		if _, err := Compile("", c[0], false); err != nil {
			got = "SyntaxError: " + syntaxErrorMessage(err)
		}
		if got != c[1] {
			t.Errorf("%q\n got: %s\nwant: %s", c[0], got, c[1])
		}
	}
}

// TestHTMLLikeComments: in scripts, <!-- starts a comment anywhere, and -->
// at the start of a line (after white space and comments only). The values
// are Node.js 18.20.3's.
func TestHTMLLikeComments(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"var y = 5, x = 1 <!--y\n; [x, y].join()", "1,5"},
		{"<!-- a comment\nvar a = 1;\n--> another\na + 1", "2"},
		{"var y = 3; var x = y-->1; [x, y].join()", "true,2"},
		{"var y = 3; y\n-->1\n; y", "3"},
		{"var z = 2 /*\n*/ --> not code\n; z", "2"},
		{"  --> at the start\n7", "7"},
		{"/* */ --> at the start\n8", "8"},
		{"var s = '<!--'; s.length", "4"},
		{"var t = 1; t\n  --> t = 9\n; t", "1"},
		{"(function(){ <!-- hidden\n return 4 })()", "4"},
		{"'use strict'; var u = 6 <!-- strict too\n; u", "6"},
		{"var n = 0; n <!--\n++n; n", "1"},
		{"var a = [1]; a[0]-->0", "true"},
	} {
		vm := New()
		v, err := vm.RunString(tc.src)
		if err != nil {
			t.Errorf("%q: %v", tc.src, err)
			continue
		}
		if got := v.String(); got != tc.want {
			t.Errorf("%q: %s, want %s", tc.src, got, tc.want)
		}
	}
}
