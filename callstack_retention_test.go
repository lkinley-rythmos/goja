package goja

import (
	"runtime"
	"testing"
	"weak"
)

type retentionProbe struct {
	payload [1 << 16]byte
}

// TestNativeCallbackFrameNotRetained: a native function (forEach) calling
// back into JavaScript saves its caller's context, with the caller's
// variables (its stash), in a slot of the call stack and of the try stack,
// and a callback that throws unwinds frames of the call stack. Once the
// callback returns or throws, those slots must not keep the variables
// reachable: after the calling function returns, what only they referenced
// is collected, and the heap is back to what it was.
func TestNativeCallbackFrameNotRetained(t *testing.T) {
	for _, tc := range []struct{ name, call string }{
		{"return", `[1].forEach(function () { return probe && big.length; });`},
		{"throw", `try { [1].forEach(function () { if (probe && big) throw 1; }); } catch (e) {}`},
		{"nested throw", `[1].forEach(function () { try { [2].map(function () { if (probe && big) throw 1; }); } catch (e) {} });`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vm := New()
			var probe weak.Pointer[retentionProbe]
			vm.Set("makeProbe", func() *retentionProbe {
				p := &retentionProbe{}
				probe = weak.Make(p)
				return p
			})
			prg := MustCompile("", `
			function outer() {
				var probe = makeProbe();
				var big = new Array(1e6).fill(1);
				`+tc.call+`
			}
			outer();
			`, false)

			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			if _, err := vm.RunProgram(prg); err != nil {
				t.Fatal(err)
			}
			runtime.GC()
			runtime.GC()
			runtime.ReadMemStats(&after)
			if probe.Value() != nil {
				t.Fatal("the caller's variables are reachable after the callback and the caller returned")
			}
			// The array is 16 MB of values.
			if grown := int64(after.HeapAlloc) - int64(before.HeapAlloc); grown > 4<<20 {
				t.Fatalf("the heap grew by %d bytes", grown)
			}
			runtime.KeepAlive(vm)
		})
	}
}
