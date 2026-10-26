package goja

import (
	"runtime"
	"runtime/debug"
	"testing"
)

var allocSizeSink []byte

// madePerAlloc is what TotalAlloc grows by for each of 100 allocations of
// n bytes with no pointers, with the collector off.
func madePerAlloc(n int) int64 {
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := 0; i < 100; i++ {
		allocSizeSink = make([]byte, n)
	}
	runtime.ReadMemStats(&after)
	allocSizeSink = nil
	return int64(after.TotalAlloc-before.TotalAlloc) / 100
}

// allocSize is what the runtime makes, at each size class's bound, one
// byte past it, and for large objects, in whole pages. Below 16 bytes the
// tiny allocator packs several in a block, and makes less.
func TestAllocSizeClasses(t *testing.T) {
	sizes := []int{1, 7, 9, 15, 32769, 40960, 40961, 1 << 20, 1<<20 + 1, 3<<20 + 5}
	for _, c := range allocSizeClasses {
		sizes = append(sizes, int(c), int(c)+1)
	}
	for _, n := range sizes {
		made, want := madePerAlloc(n), allocSize(int64(n))
		if n < 16 {
			if made > want {
				t.Errorf("%d bytes: made %d, allocSize %d", n, made, want)
			}
			continue
		}
		if made != want {
			t.Errorf("%d bytes: made %d, allocSize %d", n, made, want)
		}
	}
}
