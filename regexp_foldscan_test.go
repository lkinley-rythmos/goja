package goja

import (
	"runtime"
	"strings"
	"testing"
	"weak"
)

func TestRegexpFoldScanDoesNotRetainInput(t *testing.T) {
	// A pattern with \b and the u and i flags remembers whether the last
	// input it matched has U+017F or U+212A, for a loop of matches over one
	// input; it held that input, so a pooled Runtime kept its last input
	// (possibly megabytes) alive.
	p, err := compileRegexp(`\bk\b`, "giu")
	if err != nil {
		t.Fatal(err)
	}
	if !p.goFoldBoundary {
		t.Fatal("no goFoldBoundary")
	}
	k := newStringValue("\u212a " + strings.Repeat("\u00e9 k ", 10))
	if !p.hasFoldWordChars(k) || !p.hasFoldWordChars(k) {
		t.Fatal("U+212A not found")
	}
	s := newStringValue(strings.Repeat("\u00e9 k ", 100000))
	_, u := devirtualizeString(s)
	w := weak.Make(&u[0])
	if p.hasFoldWordChars(s) || p.hasFoldWordChars(s) {
		t.Fatal("has U+017F or U+212A")
	}
	s, u = nil, nil
	runtime.GC()
	runtime.GC()
	if w.Value() != nil {
		t.Error("the last input scanned is alive")
	}
	runtime.KeepAlive(k)
	runtime.KeepAlive(p)
}
