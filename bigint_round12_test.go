package goja

import (
	"runtime"
	"runtime/debug"
	"strconv"
	"testing"
)

// Every operation that may use math/big's pooled scratch is charged it,
// whatever the collections did to the pool: a count of them kept by a
// finalizer stopped while a host's finalizer blocked, and twelve
// remainders of 200 words by 100, a collection before each, made 218,112
// bytes and were charged 69,504.
func TestBigIntScratchChargedEachOperation(t *testing.T) {
	skipRaceAlloc(t)
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	// A host finalizer that blocks until the test ends.
	release := make(chan struct{})
	defer close(release)
	started := make(chan struct{})
	func() {
		v := new([16]*byte)
		runtime.SetFinalizer(v, func(*[16]*byte) { close(started); <-release })
	}()
	runtime.GC()
	<-started
	for _, c := range []struct{ setup, op string }{
		{"var x = (1n << 12799n) - 12345n, y = (1n << 6399n) - 12345n", "x % y"},
		{"var x = (1n << 12799n) - 12345n, y = (1n << 6399n) - 12345n", "x / y"},
		{"var x = (1n << 6399n) - 12345n, y = (1n << 2999n) - 7n", "x * y"},
		{"var x = (1n << 6399n) - 12345n", "x * x"},
	} {
		r := New()
		if _, err := r.RunString(c.setup); err != nil {
			t.Fatal(err)
		}
		prg := MustCompile("", c.op, false)
		// The counter is set once: setting it again for each run charged
		// the scratch anew, as if a collection had been counted.
		r.SetAllocationLimit(0)
		var made int64
		for i := 0; i < 12; i++ {
			// Two collections before each, which empty the pool.
			runtime.GC()
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			if _, err := r.RunProgram(prg); err != nil {
				t.Fatal(err)
			}
			runtime.ReadMemStats(&after)
			made += int64(after.TotalAlloc - before.TotalAlloc)
		}
		if counted := r.AllocatedBytes(); made > counted+64<<10 {
			t.Errorf("%s, 12 times after a collection: made %d bytes, charged %d", c.op, made, counted)
		}
	}
}

// A decimal string longer than a leaf is parsed with 10^4096 made by
// charged products: big.Int.Exp made it with powers on the way that were
// not charged (fifty parses of 4097 digits made 383 KB, and were charged
// 279 KB). 1,000 parses at 4097 and 8192 digits, the collector on and off.
func TestBigIntParsePowersCharged(t *testing.T) {
	skipRaceAlloc(t)
	for _, digits := range []int{4097, 8192} {
		for _, gcOff := range []bool{false, true} {
			func() {
				if gcOff {
					defer debug.SetGCPercent(debug.SetGCPercent(-1))
				}
				r := New()
				if _, err := r.RunString(`var s = "7".repeat(` + strconv.Itoa(digits) + `); BigInt("1")`); err != nil {
					t.Fatal(err)
				}
				label := strconv.Itoa(digits) + " digits"
				if gcOff {
					label += ", the collector off"
				}
				chargedOverN(t, r, label, "BigInt(s)", 1000)
			}()
		}
	}
}
