package goja

import "testing"

// Kept apart from native_depth_test.go, which only needs this file's
// nativeDepthOf, so that the behavioural tests also build against a runtime
// without the depth counter.

func nativeDepthOf(r *Runtime) int {
	return r.vm.nativeDepth
}

func TestObjectStack(t *testing.T) {
	vm := New()
	objs := make([]*Object, 3*objectStackScanLimit)
	for i := range objs {
		objs[i] = vm.NewObject()
	}
	var s objectStack
	for round := 0; round < 3; round++ {
		for i, o := range objs {
			if !s.push(o) {
				t.Fatalf("round %d: push %d refused", round, i)
			}
			for j := 0; j <= i; j += 7 {
				if s.push(objs[j]) {
					t.Fatalf("round %d: %d pushed twice at depth %d", round, j, i)
				}
			}
		}
		for i := len(objs) - 1; i >= 0; i-- {
			s.pop()
			if !s.push(objs[i]) {
				t.Fatalf("round %d: %d still on the stack after pop", round, i)
			}
			s.pop()
		}
		if len(s.items) != 0 || s.set != nil {
			t.Fatalf("round %d: not empty", round)
		}
	}
}
