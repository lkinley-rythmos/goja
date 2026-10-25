package goja

import (
	"errors"
	"runtime"
	"testing"
	"time"
)

// V8 (Node.js 18) refuses a BigInt longer than 2^30 bits with
// "RangeError: Maximum BigInt size exceeded", decided from the lengths of
// the operands before the result is made. The values and errors are Node
// v18.20.3's.
func TestBigIntMaxSize(t *testing.T) {
	const SCRIPT = `
	function tooBig(f, what) {
		assert.throws(RangeError, f, what);
		try { f(); } catch (e) { assert.sameValue(e.message, "Maximum BigInt size exceeded", what); }
	}
	tooBig(() => 1n << 100000000000n, "1n << 1e11");
	tooBig(() => 1n << (2n ** 30n), "1n << 2^30");
	tooBig(() => 1n << (2n ** 64n), "1n << 2^64");
	tooBig(() => 1n >> -(2n ** 30n), "1n >> -2^30");
	tooBig(() => 3n ** 100000000000n, "3n ** 1e11");
	tooBig(() => 3n ** (2n ** 30n), "3n ** 2^30");
	tooBig(() => 3n ** (2n ** 64n), "3n ** 2^64");
	tooBig(() => 2n ** (2n ** 30n), "2n ** 2^30");
	tooBig(() => 3n ** 700000000n, "3n ** 7e8");
	// V8 computes these for 26 s and 25 s before one of its products
	// passes the limit.
	tooBig(() => 4n ** (2n ** 29n - 1n), "4n ** (2^29 - 1)");
	tooBig(() => (2n ** 64n) ** (2n ** 24n - 1n), "(2^64) ** (2^24 - 1)");
	tooBig(() => BigInt.asUintN(2 ** 53 - 1, -1n), "asUintN(2^53 - 1, -1n)");
	tooBig(() => BigInt.asUintN(2 ** 30 + 1, -1n), "asUintN(2^30 + 1, -1n)");

	assert.sameValue(0n << (2n ** 64n), 0n, "0n << 2^64");
	assert.sameValue(5n >> (2n ** 64n), 0n, "5n >> 2^64");
	assert.sameValue(-5n >> (2n ** 64n), -1n, "-5n >> 2^64");
	assert.sameValue(5n << -(2n ** 64n), 0n, "5n << -2^64");
	assert.sameValue(-5n << -(2n ** 64n), -1n, "-5n << -2^64");
	assert.sameValue(-5n >> 1n, -3n, "-5n >> 1n");
	assert.sameValue(1n ** (2n ** 64n), 1n, "1n ** 2^64");
	assert.sameValue((-1n) ** (2n ** 64n), 1n, "(-1n) ** 2^64");
	assert.sameValue((-1n) ** (2n ** 64n + 1n), -1n, "(-1n) ** (2^64 + 1)");
	assert.sameValue(0n ** (2n ** 64n), 0n, "0n ** 2^64");
	assert.sameValue((-2n) ** 65n, -(2n ** 65n), "(-2n) ** 65n");
	assert.sameValue((-3n) ** 3n, -27n, "(-3n) ** 3n");
	assert.sameValue(7n ** 0n, 1n, "7n ** 0n");
	assert.sameValue((-7n) ** 1n, -7n, "(-7n) ** 1n");
	assert.throws(RangeError, () => 3n ** -1n, "3n ** -1n");
	try { 0n ** -1n; } catch (e) { assert.sameValue(e.message, "Exponent must be positive", "0n ** -1n"); }

	assert.sameValue(BigInt.asUintN(2 ** 53 - 1, 1n), 1n, "asUintN(2^53 - 1, 1n)");
	assert.sameValue(BigInt.asIntN(2 ** 53 - 1, -1n), -1n, "asIntN(2^53 - 1, -1n)");
	assert.sameValue(BigInt.asUintN(0, -1n), 0n, "asUintN(0, -1n)");
	assert.sameValue(BigInt.asIntN(0, -1n), 0n, "asIntN(0, -1n)");
	assert.sameValue(BigInt.asIntN(1, 1n), -1n, "asIntN(1, 1n)");
	assert.sameValue(BigInt.asIntN(1, -1n), -1n, "asIntN(1, -1n)");
	assert.sameValue(BigInt.asIntN(64, 2n ** 63n), -(2n ** 63n), "asIntN(64, 2^63)");
	assert.sameValue(BigInt.asIntN(64, -(2n ** 63n)), -(2n ** 63n), "asIntN(64, -2^63)");
	assert.sameValue(BigInt.asIntN(64, 2n ** 64n + 5n), 5n, "asIntN(64, 2^64 + 5)");
	assert.sameValue(BigInt.asIntN(64, -(2n ** 64n) - 5n), -5n, "asIntN(64, -2^64 - 5)");
	assert.sameValue(BigInt.asIntN(8, -129n), 127n, "asIntN(8, -129n)");
	assert.sameValue(BigInt.asUintN(64, -(2n ** 64n)), 0n, "asUintN(64, -2^64)");
	assert.sameValue(BigInt.asUintN(65, -(2n ** 64n)), 2n ** 64n, "asUintN(65, -2^64)");
	assert.sameValue(BigInt.asUintN(8, -1n), 255n, "asUintN(8, -1n)");
	assert.sameValue(BigInt.asUintN(8, -257n), 255n, "asUintN(8, -257n)");
	assert.sameValue(BigInt.asUintN(8, 257n), 1n, "asUintN(8, 257n)");
	assert.sameValue(BigInt.asUintN(2 ** 30 + 5, 0n), 0n, "asUintN(2^30 + 5, 0n)");
	`
	testScriptWithTestLib(SCRIPT, _undefined, t)
}

// The same, for results of about V8's limit: V8 takes x + y for x and y of
// the same sign, x * y, and some of the bitwise operators, to need more
// digits than the result may have.
func TestBigIntMaxSizeEdges(t *testing.T) {
	if testing.Short() {
		t.Skip("makes BigInts of 128 MB")
	}
	const SCRIPT = `
	const X = 1n << (2n ** 30n - 1n);  // 2^24 digits
	const A = X | (X - 1n);            // 2^24 digits, all ones
	function tooBig(f, what) {
		try { f(); } catch (e) {
			assert(e instanceof RangeError, what);
			assert.sameValue(e.message, "Maximum BigInt size exceeded", what);
			return;
		}
		throw new Error(what + ": did not throw");
	}
	function bits(x) {
		if (x < 0n) x = -x;
		let n = 0n;
		for (let step = 1n << 30n; step > 0n; step >>= 1n) {
			if ((x >> (n + step - 1n)) > 0n) n += step;
		}
		return n;
	}
	tooBig(() => X + 1n, "X + 1n");
	tooBig(() => (X - 1n) + 1n, "(X - 1n) + 1n");
	tooBig(() => -X - 1n, "-X - 1n");
	tooBig(() => X - -1n, "X - -1n");
	tooBig(() => X * 1n, "X * 1n");
	tooBig(() => -X * 1n, "-X * 1n");
	tooBig(() => X << 1n, "X << 1n");
	tooBig(() => X >> -1n, "X >> -1n");
	tooBig(() => X ** 2n, "X ** 2n");
	tooBig(() => ~A, "~A");
	tooBig(() => { let a = A; a++; }, "A++");
	tooBig(() => X ^ -1n, "X ^ -1n");
	tooBig(() => 0n ^ -X, "0n ^ -X");
	tooBig(() => -X & -1n, "-X & -1n");
	assert.sameValue(bits(X - 1n), 1073741823n, "X - 1n");
	assert.sameValue(bits(-X + 1n), 1073741823n, "-X + 1n");
	assert.sameValue(X + 0n, X, "X + 0n");
	assert.sameValue(-X - 0n, -X, "-X - 0n");
	assert.sameValue(0n - -X, X, "0n - -X");
	assert.sameValue(X * 0n, 0n, "X * 0n");
	assert.sameValue(X ** 1n, X, "X ** 1n");
	assert.sameValue(bits((X - 1n) << 1n), 1073741824n, "(X - 1n) << 1n");
	assert.sameValue(~X + 1n, -X, "~X");
	assert.sameValue(-A >> 64n, -(A >> 64n) - 1n, "-A >> 64n");
	assert.sameValue(0n | -X, -X, "0n | -X");
	assert.sameValue((X ^ 1n) - X, 1n, "X ^ 1n");
	assert.sameValue(BigInt.asIntN(2 ** 30, A), -1n, "asIntN(2^30, A)");
	assert.sameValue(BigInt.asUintN(2 ** 30, -1n), A, "asUintN(2^30, -1n)");
	assert.sameValue(bits((-2n) ** (2n ** 30n - 1n)), 1073741824n, "(-2n) ** (2^30 - 1)");
	`
	testScriptWithTestLib(SCRIPT, _undefined, t)
}

// The digits of a BigInt result are charged to the allocation counter
// before the result is made, so a script past its limit stops at once.
func TestBigIntAllocationLimit(t *testing.T) {
	for _, src := range []string{
		"3n ** 10000000n",
		"1n << 20000000n",
		"let x = 1n << 4000000n; for (;;) x = x * 3n",
		"let x = 7n ** 1000000n; x * x * x * x * x * x * x * x",
		"BigInt.asUintN(20000000, -1n)",
		"let x = 1n << 1000000n; for (;;) x = x + x",
	} {
		t.Run(src, func(t *testing.T) {
			r := New()
			r.SetAllocationLimit(1 << 20)
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			start := time.Now()
			_, err := r.RunString(src)
			took := time.Since(start)
			runtime.ReadMemStats(&after)
			var ie *InterruptedError
			if !errors.As(err, &ie) || !errors.Is(err, ErrAllocationLimitExceeded) {
				t.Fatalf("got %v, want the allocation limit", err)
			}
			if took > 500*time.Millisecond {
				t.Fatalf("took %v", took)
			}
			if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 8<<20 {
				t.Fatalf("allocated %d bytes", alloc)
			}
		})
	}
	r := New()
	r.SetAllocationLimit(1 << 20)
	v, err := r.RunString("(2n ** 4000n * 3n + 1n).toString(16).length")
	if err != nil {
		t.Fatal(err)
	}
	if v.ToInteger() != 1001 {
		t.Fatal(v)
	}
}
