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
// not grow the map for the runtime's life, and it keeps a bounded number of
// marks for the strings concatenated once.
func TestConcatAppend_BufferMapBounded(t *testing.T) {
	vm := New()
	// Each round's strings are of another size, so that their buffers do
	// not simply reuse the addresses of the last round's.
	prg := MustCompile("", `(function(n) { var big = "x".repeat(n); for (var i = 0; i < 1000; i++) { var t = big + i; t += "."; var once = `+"`${big}!${i}`"+`; } })(size)`, false)
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
	if n := len(vm.appendBufs.marks); n > appendMaxMarks {
		t.Fatalf("%d marks", n)
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
