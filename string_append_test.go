package goja

import (
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// TestConcatAppend_MatchesNode builds strings by repeated concatenation
// (+, +=, templates, String.prototype.concat) past appendMinLength, forks
// them from the same prefix, mixes in units past ASCII and lone surrogates,
// and uses them as keys. The expected output is Node.js 18.20.3's.
func TestConcatAppend_MatchesNode(t *testing.T) {
	const script = `
	var out = [];
	var base = "a".repeat(300);
	var s = base;
	for (var i = 0; i < 1000; i++) s += String.fromCharCode(97 + i % 26);
	var b = base + "x", c = base + "y";
	out.push(b.length, b.charCodeAt(300), c.charCodeAt(300), b === base + "x");
	var t = s; s += "Z"; var t2 = t + "Q"; s += "W";
	out.push(s.slice(-3), t2.slice(-3), t.length, s.length, t2.length, t.slice(-2));
	var u = base; u += "é"; u += "x"; var u2 = u; u += "😀"; u2 += "y";
	out.push(u.length, u2.length, u.slice(-4), u2.slice(-3), u.codePointAt(302), u2 === base + "éxy");
	var w = "é".repeat(300); var w1 = w + "a"; var w2 = w + "b"; w1 += "c";
	out.push(w1.slice(-3), w2.slice(-2), w1 === w + "ac", w2.length);
	var tpl = base; for (var i = 0; i < 100; i++) tpl = ` + "`${tpl}${i}`" + `; var tplb = ` + "`${tpl}-`" + `; var tplc = ` + "`${tpl}+${i}`" + `;
	out.push(tpl.length, tplb.slice(-3), tplc.slice(-5));
	var cm = base; for (var i = 0; i < 100; i++) cm = cm.concat(i, "."); var cm2 = cm.concat(" "); var cm3 = cm.concat("!");
	out.push(cm.length, cm.slice(-6), cm2.slice(-2), cm3.slice(-2));
	var o = {}; o[s] = 1; o[t2] = 2;
	out.push(o[t + "ZW"], o[t + "Q"], s === t + "ZW", s < t2, Object.keys(o).map(function (k) { return k.length; }).join());
	var d = base; d += d; d += d; var d2 = d + d;
	out.push(d.length, d === base.repeat(4), d2.length);
	var ls = base; ls += "\ud800"; var ls2 = ls + "\udc00"; var ls3 = ls + "x";
	out.push(ls2.length, ls2.codePointAt(300), ls3.codePointAt(300), ls3.length);
	var m = new Map(); m.set(s, "s"); m.set(t2, "t2"); out.push(m.get(t + "ZW"), m.get(t + "Q"), m.size);
	var n = "1".repeat(300); var n1 = n + "0"; var n2 = n + "5";
	out.push(n1 == 1.1111111111111111e300 * 10, Number(n2.slice(-3)), n1.length);
	JSON.stringify(out)
	`
	const node = `[301,120,121,true,"lZW","klQ",1300,1302,1301,"kl",304,303,"éx😀","éxy",128512,true,"éac","éb",true,301,490,"99-","9+100",590,"98.99.",".` + " " + `",".!",1,2,true,false,"1302,1301",1200,true,2400,302,65536,55296,302,"s","t2",2,false,115,301]`
	testScript(script, newStringValue(node), t)
}

// TestConcatAppend_ExportAndCompare: strings grown in a buffer export to Go,
// compare, and hash as the same strings built at once.
func TestConcatAppend_ExportAndCompare(t *testing.T) {
	vm := New()
	v, err := vm.RunString(`var s = ""; for (var i = 0; i < 2000; i++) s += "ab"; var u = s; for (var i = 0; i < 10; i++) u += "é"; [s, u]`)
	if err != nil {
		t.Fatal(err)
	}
	got := v.Export().([]interface{})
	if want := strings.Repeat("ab", 2000); got[0] != want {
		t.Fatalf("s exported as %.20q..., length %d", got[0], len(got[0].(string)))
	}
	if want := strings.Repeat("ab", 2000) + strings.Repeat("é", 10); got[1] != want {
		t.Fatalf("u exported as ...%q", got[1].(string)[len(got[1].(string))-20:])
	}
	s := vm.Get("s").(String)
	if !s.SameAs(asciiString(strings.Repeat("ab", 2000))) || s.CompareTo(newStringValue(strings.Repeat("ab", 2000))) != 0 {
		t.Fatal("s differs from the string built at once")
	}
	u := vm.Get("u").(unicodeString)
	if cap(u) != len(u) {
		t.Fatalf("a unicode result has room past its length (%d > %d), which an append would write into", cap(u), len(u))
	}
}

// TestConcatAppend_LinearCharges: a million one-unit steps charge about the
// memory the string takes, not the 500 GB of copies a copy per step would
// be, so that they run in a budget that holds the result.
func TestConcatAppend_LinearCharges(t *testing.T) {
	vm := New()
	vm.SetAllocationLimit(16 << 20)
	v, err := vm.RunString(`(function() { var s = ""; for (var i = 0; i < 1e6; i++) s += "x"; return s.length; })()`)
	if err != nil {
		t.Fatal(err)
	}
	if v.ToInteger() != 1e6 {
		t.Fatalf("length %v", v)
	}
	// At least the string's million bytes; the buffer doubles, so at
	// most about four times that.
	if got := vm.AllocatedBytes(); got < 1e6 || got > 5e6 {
		t.Fatalf("charged %d bytes", got)
	}
}

// TestConcatAppend_LinearChargesUnits is LinearCharges for a string of
// units past ASCII, charged two bytes a unit.
func TestConcatAppend_LinearChargesUnits(t *testing.T) {
	vm := New()
	vm.SetAllocationLimit(32 << 20)
	v, err := vm.RunString(`(function() { var s = ""; for (var i = 0; i < 1e6; i++) s += "é"; return s.length; })()`)
	if err != nil {
		t.Fatal(err)
	}
	if v.ToInteger() != 1e6 {
		t.Fatalf("length %v", v)
	}
	// At least the string's two million bytes; the buffer doubles.
	if got := vm.AllocatedBytes(); got < 2e6 || got > 10e6 {
		t.Fatalf("charged %d bytes", got)
	}
}

// TestConcatAppend_ChainedLoopsLinear: loops that build a string with more
// than one + in an expression, or build two strings in turn, are charged
// the memory their strings take, not a copy per step: an expression's later
// + are steps of the chain its first + started (s = s + a + b is promoted as
// s += x is, and appends in place), and a mark is kept for its chain while
// another string is concatenated (s += x; t += y).
func TestConcatAppend_ChainedLoopsLinear(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"s = s + a + b", `s = s + "x" + "y"`},
		{"s = s + a + b + c", `s = s + "x" + "y" + "z"`},
		{"s += a + b", `s += "x" + i`},
		{"two strings", `s += "x"; t += "y"`},
		{"three strings", `s += "x"; t += "y"; u += "z"`},
		// Marks are hints kept in a table of appendMarkSlots, each in the
		// slot its address hashes to: 200 strings built in turn still
		// each keep theirs, but for some collisions, until promoted.
		{"200 strings", `a[i % 200] += "x"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vm := New()
			vm.SetAllocationLimit(64 << 20)
			v, err := vm.RunString(`(function() { var s = "", t = "", u = "", a = []; for (var j = 0; j < 200; j++) a.push("x".repeat(300) + j); for (var i = 0; i < 2e5; i++) { ` + tc.body + ` } return s.length + t.length + u.length + a.join("").length; })()`)
			if err != nil {
				t.Fatal(err)
			}
			// The strings' units; a buffer doubles, so at most about four
			// times that.
			n := v.ToInteger()
			if got := vm.AllocatedBytes(); got < n || got > 5*n {
				t.Fatalf("charged %d bytes for %d units", got, n)
			}
		})
	}
}

// TestConcatAppend_ExpressionExact: big + x + y, and longer expressions,
// made once, hold and are charged what they always were, each result at
// its exact size: the later + of an expression are steps of the chain its
// first + started, and a chain goes into a buffer with room only at its
// third step.
func TestConcatAppend_ExpressionExact(t *testing.T) {
	for _, tc := range []struct {
		name, unit string
		unitBytes  int64
		pre, expr  string // pre is run before keep.push(expr)
		steps      int64  // the concatenations of big
	}{
		{"ascii", "x", 1, ``, `big + i + "."`, 2},
		{"units", "é", 2, ``, `big + i + "."`, 2},
		{"four terms", "x", 1, ``, `big + i + "." + i`, 3},
		{"four terms, units", "é", 2, ``, `big + i + "." + i`, 3},
		{"six terms", "x", 1, ``, `big + i + "." + i + "," + i`, 5},
		// The expression's first + is its string's second step: the
		// later ones are not its third.
		{"a string, then an expression", "x", 1, `var t = big + i;`, `t + "." + i + ","`, 4},
		// The expression is one step: the + after it is the second.
		{"an expression, then a string", "x", 1, `var t = big + i + "." + i;`, `t + ","`, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vm := New()
			prg := MustCompile("", `var big = "`+tc.unit+`".repeat(4000), keep = []; for (var i = 0; i < 2000; i++) { `+tc.pre+` keep.push(`+tc.expr+`); }`, false)
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			if _, err := vm.RunProgram(prg); err != nil {
				t.Fatal(err)
			}
			runtime.GC()
			runtime.ReadMemStats(&after)
			// Each result is 4,001 to 4,014 units.
			exact := 2000 * 4010 * tc.unitBytes
			if held := int64(after.HeapAlloc) - int64(before.HeapAlloc); held > exact*12/10 {
				t.Fatalf("holds %d bytes, want about %d", held, exact)
			}
			// As base charged: each step its result's length in units, of
			// 4,001 to 4,014; a buffer would add a quarter to one. The
			// array the results are kept in is charged as it grows.
			pushes := New()
			if _, err := pushes.RunString(`var keep = []; for (var i = 0; i < 2000; i++) keep.push(i)`); err != nil {
				t.Fatal(err)
			}
			if got := vm.AllocatedBytes() - pushes.AllocatedBytes(); got < 2000*tc.steps*4001 || got > 2000*tc.steps*4014 {
				t.Fatalf("charged %d bytes, want %d to %d", got, 2000*tc.steps*4001, 2000*tc.steps*4014)
			}
			runtime.KeepAlive(vm)
		})
	}
}

// TestConcatAppend_ForkPromotedOnce: a string being built that is forked
// (t + "a", t + "b", t + "c") gives a buffer to its first fork only: its
// mark is used once, and the other forks are made at their exact size.
func TestConcatAppend_ForkPromotedOnce(t *testing.T) {
	vm := New()
	prg := MustCompile("", `var big = "x".repeat(4000), keep = []; for (var i = 0; i < 2000; i++) { var t = big + i; t += "."; keep.push(t + "a", t + "b", t + "c"); }`, false)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if _, err := vm.RunProgram(prg); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	// Each iteration keeps a fork of about 5,000 bytes, with room, and two
	// of about 4,006; with a buffer for each fork, three of about 5,000.
	if held := int64(after.HeapAlloc) - int64(before.HeapAlloc); held > 2000*14000 {
		t.Fatalf("holds %d bytes, want about %d", held, 2000*13000)
	}
	// About 4,004 + 4,005 for t, 5,000 for the first fork and 4,006 for each
	// other; about 23,000 with a buffer for each fork.
	if got := vm.AllocatedBytes(); got > 2000*22000 {
		t.Fatalf("charged %d bytes, want about %d", got, 2000*21000)
	}
	runtime.KeepAlive(vm)
}

// TestConcatAppend_BufferMapReleased: the map of buffers is let go when
// the buffers are gone and a new run starts, not kept at the size it once
// had.
func TestConcatAppend_BufferMapReleased(t *testing.T) {
	vm := New()
	if _, err := vm.RunString(`var keep = []; for (var i = 0; i < 3000; i++) { var t = "x".repeat(300) + i; t += "."; t += ","; keep.push(t) }`); err != nil {
		t.Fatal(err)
	}
	if n := len(vm.appendBufs.m); n < 3000 {
		t.Fatalf("%d buffers remembered for 3,000 strings being built", n)
	}
	if _, err := vm.RunString(`keep = t = null`); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	runtime.GC()
	vm.SetAllocationLimit(0)
	if vm.appendBufs.m != nil || vm.appendBufs.marks != nil {
		t.Fatalf("%d buffers and the marks kept after they were gone", len(vm.appendBufs.m))
	}
}

// TestConcatAppend_BufferMapShrinks: a Go map does not shrink, so a sweep
// that leaves it under a quarter of the most entries it had makes it again
// at its size, rather than keep the room of 3,000 entries for 100.
func TestConcatAppend_BufferMapShrinks(t *testing.T) {
	vm := New()
	if _, err := vm.RunString(`var keep = []; for (var i = 0; i < 3000; i++) { var t = "x".repeat(300) + i; t += "."; t += ","; keep.push(t) }`); err != nil {
		t.Fatal(err)
	}
	if n := vm.appendBufs.peak; n < 3000 {
		t.Fatalf("a peak of %d buffers for 3,000 strings being built", n)
	}
	if _, err := vm.RunString(`keep = keep.slice(0, 100); t = null`); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	runtime.GC()
	vm.appendBufs.sweep()
	if n, peak := len(vm.appendBufs.m), vm.appendBufs.peak; n < 100 || n > 110 || peak != n {
		t.Fatalf("%d buffers left, in a map made for %d", n, peak)
	}
	runtime.KeepAlive(vm)
}

// TestConcatAppend_BufferChargedToLimit: the buffers a string grows in count
// toward the allocation limit, in bytes of either kind.
func TestConcatAppend_BufferChargedToLimit(t *testing.T) {
	for _, tc := range []struct{ name, piece string }{
		{"ascii", `"x".repeat(1024)`},
		{"unicode", `"é".repeat(1024)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vm := New()
			if _, err := vm.RunString(`var piece = ` + tc.piece); err != nil {
				t.Fatal(err)
			}
			vm.SetAllocationLimit(4 << 20)
			// 8 MB of ASCII, or 16 MB of units.
			_, err := vm.RunString(`var s = ""; for (var i = 0; i < 8192; i++) s += piece;`)
			if !errors.Is(err, ErrAllocationLimitExceeded) {
				t.Fatalf("err: %v", err)
			}
		})
	}
}

// TestConcatAppend_BufferMapBounded: the runtime forgets the buffers of
// strings that are gone, so that a script building many long strings does
// not grow the map for the runtime's life.
func TestConcatAppend_BufferMapBounded(t *testing.T) {
	vm := New()
	// Each round's strings are of another size, so that their buffers do
	// not simply reuse the addresses of the last round's.
	prg := MustCompile("", `(function(n) { var big = "x".repeat(n); for (var i = 0; i < 1000; i++) { var t = big + i; t += "."; t += ","; var once = `+"`${big}!${i}`"+`; } })(size)`, false)
	for round := range 20 {
		vm.Set("size", 300+round*300)
		if _, err := vm.RunProgram(prg); err != nil {
			t.Fatal(err)
		}
		runtime.GC()
	}
	// The map is swept when it doubles past what was live at the last
	// sweep: about 1,000 strings of a round, all live until the round ends.
	if n := len(vm.appendBufs.m); n > 2100 {
		t.Fatalf("%d buffers remembered after 20,000 strings built and dropped", n)
	}
	vm.appendBufs.sweep()
	if n := len(vm.appendBufs.m); n != 0 {
		t.Fatalf("%d buffers remembered after a sweep, with none left", n)
	}
}

// TestConcatAppend_ResultsSharedAcrossGoroutines: a result handed to another
// goroutine keeps its value while the runtime goes on concatenating onto the
// operand it was made from.
func TestConcatAppend_ResultsSharedAcrossGoroutines(t *testing.T) {
	vm := New()
	if _, err := vm.RunString(`var s = "a".repeat(300); for (var i = 0; i < 100; i++) s += "b";`); err != nil {
		t.Fatal(err)
	}
	fork, err := vm.RunString(`s + "x"`)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Repeat("a", 300) + strings.Repeat("b", 100) + "x"
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if got := fork.String(); got != want {
				t.Errorf("the other goroutine read ...%q", got[len(got)-3:])
				return
			}
		}
	}()
	for i := 0; i < 200; i++ {
		if _, err := vm.RunString(`var t = s + "y"; t += "z"; s += "b";`); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
}

// Small-string workloads, which do not go through a shared buffer, and long
// ones, which do.
func benchmarkConcatScript(b *testing.B, script string) {
	vm := New()
	prg := MustCompile("", script, false)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := vm.RunProgram(prg); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkConcatSmallKeys(b *testing.B) {
	benchmarkConcatScript(b, `(function() { var o = {}; for (var i = 0; i < 100; i++) { o["k" + i + ":" + (i * 2)] = i; } return o; })()`)
}

func BenchmarkConcatSmallMessage(b *testing.B) {
	benchmarkConcatScript(b, `(function() { var r; var name = "memberId", rule = "tier-upgrade"; for (var i = 0; i < 100; i++) { r = "Unable to process: " + name + " is required for " + rule + " (" + i + ")"; } return r; })()`)
}

func BenchmarkConcatSmallTemplate(b *testing.B) {
	benchmarkConcatScript(b, "(function() { var r; for (var i = 0; i < 100; i++) { r = `${i}/${i + 1}:${'abc'}`; } return r; })()")
}

func BenchmarkConcatShortAccumulator(b *testing.B) {
	benchmarkConcatScript(b, `(function() { var s = ""; for (var i = 0; i < 60; i++) { s += "ab,"; } return s; })()`)
}

func BenchmarkConcatLongOnce(b *testing.B) {
	benchmarkConcatScript(b, `(function() { var big = "x".repeat(1000), t; for (var i = 0; i < 100; i++) { t = big + i; } return t; })()`)
}

func BenchmarkConcatLongOnceUnicode(b *testing.B) {
	benchmarkConcatScript(b, `(function() { var big = "é".repeat(1000), t; for (var i = 0; i < 100; i++) { t = big + i; } return t; })()`)
}

func BenchmarkConcatLongAccumulator(b *testing.B) {
	benchmarkConcatScript(b, `(function() { var s = ""; for (var i = 0; i < 10000; i++) { s += "abc" + i; } return s.length; })()`)
}

func BenchmarkConcatNestedRender(b *testing.B) {
	// AJV's code generator renders nested blocks into one string with
	// reduce((code, n) => code + n.render(), "").
	benchmarkConcatScript(b, `(function() {
		function render(depth) {
			if (depth === 0) return "if (x) { y = 1; }\n";
			var parts = [];
			for (var i = 0; i < 6; i++) parts.push(depth);
			return parts.reduce(function (code, d) { return code + "{" + render(d - 1) + "}\n"; }, "");
		}
		return render(5).length;
	})()`)
}

func BenchmarkConcatThreeTerm(b *testing.B) {
	benchmarkConcatScript(b, `(function() { var big = "x".repeat(4000), keep = []; for (var i = 0; i < 100; i++) { keep.push(big + i + "."); } return keep; })()`)
}

func BenchmarkConcatFourTerm(b *testing.B) {
	benchmarkConcatScript(b, `(function() { var big = "x".repeat(1000), keep = []; for (var i = 0; i < 100; i++) { keep.push(big + i + "." + i); } return keep; })()`)
}

// TestAddChain_MatchesNode: the later + of an expression (addChain) convert
// and add their operands as + does: numbers and BigInts before a string,
// objects' valueOf and toString in order, and the errors of Symbols and of
// mixing BigInts. The expected output is Node.js 18.20.3's.
func TestAddChain_MatchesNode(t *testing.T) {
	const script = `var log = [];
var o = {valueOf: function () { log.push("v"); return 1 }, toString: function () { log.push("t"); return "O" }};
var d = {toString: function () { log.push("d"); return "D" }, valueOf: undefined};
var big = "s".repeat(300);
var r = [1 + 2 + "a", "a" + 1 + 2, 1n + 2n + 3n, 1 + 2 + o, "x" + o + o, o + o + "y", null + 1 + "z", undefined + "a" + undefined, true + 1 + "b", [1] + [2] + [3], ({}) + 1 + 2, "a" + d + d, big + o + d + 1 + 2, 1 + 2 + 3 + big, big + null + undefined + true];
function err(f) { try { f(); return "no error" } catch (e) { return e.constructor.name + ": " + e.message } }
r.push(err(function () { return "a" + 1 + Symbol() }), err(function () { return 1n + 2n + 1 }), err(function () { return big + "a" + Symbol("q") }), err(function () { return 1 + 2 + 3n }), err(function () { return "a" + 1 + {toString: function () { throw new RangeError("ts") }, valueOf: undefined} }));
JSON.stringify([r.map(function (x) { return typeof x + ":" + String(x).length + ":" + String(x).slice(-12) }), log.join("")])
`
	const node = `[["string:2:3a","string:3:a12","bigint:1:6","number:1:4","string:3:x11","string:2:2y","string:2:1z","string:19:edaundefined","string:2:2b","string:3:123","string:17:ct Object]12","string:3:aDD","string:304:ssssssss1D12","string:301:ssssssssssss","string:317:ndefinedtrue","string:52: to a string","string:70: conversions","string:52: to a string","string:70: conversions","string:14:ngeError: ts"],"vvvvvddvd"]`
	testScript(script, newStringValue(node), t)
}
