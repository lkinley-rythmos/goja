package goja

import (
	"strconv"
	"strings"
	"testing"
)

// chargedOverN runs src n times in one run, and fails if it made more than
// it was charged and 64 KiB.
func chargedOverN(t *testing.T, r *Runtime, label, src string, n int) {
	t.Helper()
	counted, actual, err := allocDuring(t, r, MustCompile("", "for (let i = 0; i < "+strconv.Itoa(n)+"; i++) { "+src+" }", false), 0)
	if err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	if actual > counted+64<<10 {
		t.Errorf("%s, %d times: made %d bytes, charged %d", label, n, actual, counted)
	}
}

// The temporaries of a BigInt operation made in separate buffers are each
// charged as Go makes it: rounded to its size class or pages, with its
// spare words, on its own. Their sum was rounded once: ten x % y of 4096
// words by 2 made 820,560 bytes and were charged 738,240 (the copy QuoRem
// divides and the quotient it drops, 40,960 bytes each, were charged
// 73,728 for the two). The shapes are Codex's, at each site that summed
// buffers, and lengths whose buffers each just pass a page. The values a
// division makes block by block, and a step makes, are charged too
// (chargeHeaders).
func TestBigIntBuffersChargedEach(t *testing.T) {
	skipRaceAlloc(t)
	for _, c := range []struct {
		setup, op string
		reps      int
	}{
		// A division big.Int.QuoRem makes (divAbsQ).
		{"var x = (1n << 262143n) - 12345n, y = (1n << 127n) - 12345n", "x % y", 100},
		{"var x = (1n << 262143n) - 12345n, y = (1n << 127n) - 12345n", "x / y", 100},
		// Block by block (divChunked), 1,000 times: the values each block
		// makes, 275 bytes an operation uncharged, passed the allowance in
		// about 240.
		{"var x = (1n << 578303n) - 12345n, y = (1n << 6399n) - 12345n", "x % y", 1000},
		{"var x = (1n << 578303n) - 12345n, y = (1n << 6399n) - 12345n", "x / y", 1000},
		// A short quotient (divShortQuotient).
		{"var x = (1n << 524351n) - 12345n, y = x >> 1n", "x % y", 100},
		{"var x = (1n << 524351n) - 12345n, y = x >> 1n", "x / y", 100},
		// A long quotient, by the divisor's reciprocal (divLongQuotient,
		// recipAbs, its leaf and its step), shifted: 9213 words, whose
		// buffers of 9217 just pass 9 pages.
		{"var y = (1n << " + strconv.Itoa(9213*64-2) + "n) + 12345n, x = (y << " + strconv.Itoa(9213*64) + "n) + 777n", "x % y", 100},
		{"var y = (1n << " + strconv.Itoa(9213*64-2) + "n) + 12345n, x = (y << " + strconv.Itoa(9213*64) + "n) + 777n", "x / y", 100},
		// The copies of two negative operands past the bitwise leaf.
		{"var x = -((1n << 4194367n) - 12345n), y = x - 31415n", "x & y", 100},
		{"var x = -((1n << 4194367n) - 12345n), y = x - 31415n", "x | y", 100},
		{"var x = -((1n << 4194367n) - 12345n), y = x - 31415n", "x ^ y", 100},
		// Text past a leaf in a radix that is not a power of two (the
		// powers of the radix), and a parse of a long decimal string.
		{"var x = (1n << " + strconv.Itoa(4093*64-1) + "n) - 12345n", "x.toString(10)", 100},
		{"var x = (1n << " + strconv.Itoa(4093*64-1) + "n) - 12345n", "x.toString(7)", 100},
		{`var s = "7".repeat(80000)`, "BigInt(s)", 100},
	} {
		r := New()
		if _, err := r.RunString(c.setup); err != nil {
			t.Fatal(err)
		}
		chargedOverN(t, r, strings.SplitN(c.setup, ",", 2)[0]+": "+c.op, c.op, c.reps)
	}
}
