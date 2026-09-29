package goja

import (
	"errors"
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

// TestInterruptLeavesNoFrames: an interrupt inside a generator or an
// async function body unwinds Go frames the VM does not clean up; after
// it ends the outermost run, the runtime's stacks must be empty, the
// interrupt cleared, and the next run must start afresh. (They kept one
// to two frames of each kind, growing with every interrupted run, and the
// next run was interrupted at once.)
func TestInterruptLeavesNoFrames(t *testing.T) {
	for _, src := range []string{
		`function* g(){ stop(); while(true){} } g().next()`,
		`async function f(){ await 1; stop(); while(true){} } f()`,
		`function* g(){ stop(); while(true){} } [1].forEach(function(){ g().next() })`,
		`function* g(){ try { stop(); while(true){} } finally { [1].map(function(){}) } } for (var x of g()) {}`,
		`import('x', {get assert() { stop(); while(true){} }})`,
		`function* g(){ import('x', {assert: {get type() { stop(); while(true){} }}}) } g().next()`,
	} {
		for _, viaCallable := range []bool{false, true} {
			vm := New()
			vm.Set("stop", func() { vm.Interrupt("stop") })
			for i := 0; i < 5; i++ {
				var err error
				if viaCallable {
					var fn Value
					fn, err = vm.RunString(`(function () { ` + src + ` })`)
					if err != nil {
						t.Fatal(err)
					}
					call, _ := AssertFunction(fn)
					_, err = call(nil)
				} else {
					_, err = vm.RunString(src)
				}
				var ie *InterruptedError
				if !errors.As(err, &ie) || ie.Value() != "stop" {
					t.Fatalf("%s: run %d: %v", src, i, err)
				}
				v := vm.vm
				if len(v.callStack) != 0 || len(v.tryStack) != 0 || len(v.iterStack) != 0 || len(v.refStack) != 0 || v.sp != 0 {
					t.Fatalf("%s (callable %v): run %d left call %d, try %d, iter %d, ref %d, sp %d", src, viaCallable, i, len(v.callStack), len(v.tryStack), len(v.iterStack), len(v.refStack), v.sp)
				}
			}
			if v, err := vm.RunString(`[1, 2].map(function (x) { return x * 2 }).join()`); err != nil || v.String() != "2,4" {
				t.Fatalf("%s: after the interrupts: %v, %v", src, v, err)
			}
		}
	}
}

// TestInterruptDropsOversizedValueStack: an interrupt that ends the
// outermost run lets go of a value stack grown past maxRetainedStackCap, as
// a run that returns does (TestLeaveDropsOversizedValueStack), and the next
// run works. (It kept 15,360 slots after a recursion 5,000 deep.)
func TestInterruptDropsOversizedValueStack(t *testing.T) {
	for _, src := range []string{
		`function f(n) { if (n === 0) { stop(); while (true) {} } return f(n - 1) + 1 } f(5000)`,
		`function* g() { yield f(5000) } function f(n) { if (n === 0) { stop(); while (true) {} } return f(n - 1) + 1 } g().next()`,
		`(function () { function f(n) { if (n === 0) { stop(); while (true) {} } return f(n - 1) + 1 } return f(5000) })`,
	} {
		vm := New()
		vm.Set("stop", func() { vm.Interrupt("stop") })
		v, err := vm.RunString(src)
		if fn, ok := AssertFunction(v); ok && err == nil {
			_, err = fn(nil)
		}
		var ie *InterruptedError
		if !errors.As(err, &ie) {
			t.Fatalf("%s: %v", src, err)
		}
		if c := cap(vm.vm.stack); c != 0 {
			t.Fatalf("%s: kept a value stack of %d slots", src, c)
		}
		if v, err := vm.RunString(`[1, 2].map(function (x) { return x * 2 }).join()`); err != nil || v.String() != "2,4" {
			t.Fatalf("%s: after the interrupt: %v, %v", src, v, err)
		}
	}
}
