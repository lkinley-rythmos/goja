package goja

import (
	"errors"
	"runtime"
	"strings"
	"testing"
)

// Built-ins that produce one large string must refuse it before allocating
// it: past V8's maximum string length with V8's RangeError, and past the
// allocation limit with the limit's interrupt.

func TestStringResults_PastMaxLengthThrowRangeError(t *testing.T) {
	const script = `
	var s = "a".repeat(2 ** 28); // 268M units, half the maximum
	var cases = {
		repeat: function() { return "a".repeat(2 ** 29) },
		repeatHuge: function() { return "ab".repeat(2 ** 40) },
		padStart: function() { return "".padStart(2 ** 30) },
		padEnd: function() { return "x".padEnd(2 ** 53 - 1, "yz") },
		plus: function() { return s + s + s },
		template: function() { return ` + "`${s}${s}${s}`" + ` },
		concat: function() { return s.concat(s, s) },
		join: function() { return [s, s, s].join("") },
		joinSeparator: function() { return [1, 2, 3].join(s) },
		stringify: function() { return JSON.stringify([s, s]) },
	};
	var res = [];
	for (var k in cases) {
		try {
			var v = cases[k]();
			res.push(k + ": no error, length " + v.length);
		} catch (e) {
			if (!(e instanceof RangeError) || e.message !== "Invalid string length") {
				res.push(k + ": " + e);
			}
		}
	}
	res.join("; ");
	`
	testScript(script, stringEmpty, t)
}

func TestStringResults_AtMaxLength(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates 1.5 GB")
	}
	// The maximum itself is allowed, as in V8.
	testScript(`"a".repeat(2 ** 29 - 24).length + "a".padEnd(2 ** 29 - 24).length`, valueInt(2*maxStringLength), t)
}

// TestStringResults_RefusedByAllocationLimit sets up a large input without a
// limit, then sets a small one and runs a call whose result is larger than
// the limit: the call must be refused with the limit's error before the
// result is allocated.
func TestStringResults_RefusedByAllocationLimit(t *testing.T) {
	const limit = 16 << 20
	const resultUnits = 50000000
	for _, tc := range []struct{ name, setup, call string }{
		{"repeat", ``, `"a".repeat(5e7)`},
		{"padStart", ``, `"".padStart(5e7, "xy")`},
		{"padEnd unicode", ``, `"é".padEnd(5e7, "ü")`},
		{"plus", `var s = "a".repeat(2.5e7)`, `s + s`},
		{"template", `var s = "a".repeat(2.5e7)`, "`${s}${s}`"},
		{"concat", `var s = "a".repeat(1e7)`, `s.concat(s, s, s, s)`},
		{"join", `var a = []; for (var i = 0; i < 5; i++) a.push("a".repeat(1e7))`, `a.join()`},
		{"toLocaleString", `var a = []; for (var i = 0; i < 5; i++) a.push("a".repeat(1e7))`, `a.toLocaleString()`},
		{"replaceAll string", `var s = "a".repeat(5e6)`, `s.replaceAll("a", "bbbbbbbbbb")`},
		// regexp2 (lookbehind) collects matches one by one and charges each.
		{"replace regexp", `var s = "a".repeat(5e6)`, `s.replace(/(?<=)a/g, "bbbbbbbbbb")`},
		{"replace function", `var s = "a".repeat(5e5)`, `s.replace(/(?<=)a/g, function() { return "b".repeat(100) })`},
		{"toUpperCase", `var s = "a".repeat(5e7)`, `s.toUpperCase()`},
		{"toLowerCase", `var s = "A".repeat(5e7)`, `s.toLowerCase()`},
		{"normalize", `var s = "é".repeat(5e7)`, `s.normalize("NFD")`},
		{"JSON.stringify", `var a = []; for (var i = 0; i < 5; i++) a.push("a".repeat(1e7))`, `JSON.stringify(a)`},
		{"encodeURIComponent", `var s = "é".repeat(1e7)`, `encodeURIComponent(s)`},
		{"escape", `var s = "é".repeat(1e7)`, `escape(s)`},
		{"String.raw", `var s = "a".repeat(1e7)`, `String.raw({raw: [s, s, s, s, s, s]}, 1, 2, 3, 4, 5)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vm := New()
			if _, err := vm.RunString(tc.setup); err != nil {
				t.Fatal(err)
			}
			prg := MustCompile("", tc.call, false)
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			vm.SetAllocationLimit(limit)
			_, err := vm.RunProgram(prg)
			runtime.ReadMemStats(&after)
			if !errors.Is(err, ErrAllocationLimitExceeded) {
				t.Fatalf("err: %v", err)
			}
			// Allocating the result would take at least resultUnits
			// bytes; refused first, the call allocates less. (What it
			// does allocate is up to one step past the limit, and what
			// that step's buffers grow by.)
			// The race detector's instrumentation allocates on its own.
			if d := after.TotalAlloc - before.TotalAlloc; d > resultUnits*8/10 && !raceDetectorEnabled {
				t.Fatalf("allocated %d bytes before refusing", d)
			}
		})
	}
}

func TestStringResults_StillCorrect(t *testing.T) {
	const script = `
	var r = [];
	r.push("ab".repeat(3), "é".repeat(2), "x".repeat(1), "".repeat(5) === "", "a".repeat(0) === "");
	r.push("5".padStart(3, "0"), "5".padEnd(4, "ab"), "5".padStart(6, "xyz"), "é".padStart(4, "ü"), "a".padEnd(3, "é"),
		"a".padStart(2), "abc".padStart(2, "z"), "a".padStart(5, ""));
	r.push("Straße".toUpperCase(), "İ".toLowerCase().length, "é".normalize("NFD").length);
	r.push(["a", "é", 1, null].join("-"), String.raw({raw: ["x", "y", "z"]}, 1, 2), escape("é a"), encodeURIComponent("é a"));
	r.join("|");
	`
	want := strings.Join([]string{
		"ababab", "éé", "x", "true", "true",
		"005", "5aba", "xyzxy5", "üüüé", "aéé", " a", "abc", "a",
		"STRASSE", "2", "2",
		"a-é-1-", "x1y2z", "%E9%20a", "%C3%A9%20a",
	}, "|")
	testScript(script, newStringValue(want), t)
}

func TestStringResults_FromCharCodeRefusedByAllocationLimit(t *testing.T) {
	// Spreading the arguments already copies them onto the vm's stack, so
	// only the refusal is checked here, not the memory.
	vm := New()
	if _, err := vm.RunString(`var codes = new Array(2e7).fill(97)`); err != nil {
		t.Fatal(err)
	}
	vm.SetAllocationLimit(16 << 20)
	for _, call := range []string{`String.fromCharCode.apply(null, codes)`, `String.fromCodePoint.apply(null, codes)`} {
		if _, err := vm.RunString(call); !errors.Is(err, ErrAllocationLimitExceeded) {
			t.Fatalf("%s: %v", call, err)
		}
		vm.SetAllocationLimit(16 << 20)
	}
}
