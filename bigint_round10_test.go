package goja

import (
	"strconv"
	"strings"
	"testing"
)

// classEdges are sizes in bytes at Go's allocation bounds: some size
// classes' and the first pages' past the small ones.
var classEdges = []int{256, 1024, 4096, 8192, 16384, 24576, 28672, 32768, 40960, 49152, 65536}

// chargedOver100 runs src 100 times in one run, and fails if it made more
// than it was charged and 64 KiB.
func chargedOver100(t *testing.T, r *Runtime, label, src string) {
	t.Helper()
	counted, actual, err := allocDuring(t, r, MustCompile("", "for (let i = 0; i < 100; i++) { "+src+" }", false), 0)
	if err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	if actual > counted+64<<10 {
		t.Errorf("%s: made %d bytes, charged %d", label, actual, counted)
	}
}

// The text of a BigInt whose buffers sit just past a size class, or a
// page, is charged what Go makes them in, in every radix kind: a power of
// two at once or in blocks, any other by its leaves. 100 x.toString(32)
// of 2241 words (a text of 28,685 bytes, made in 32,768 and charged
// 32,286) made 146 KB more than they were charged.
func TestBigIntTextChargedAtSizeClasses(t *testing.T) {
	skipRaceAlloc(t)
	r := New()
	r.Set("radixes", []int{2, 8, 10, 16, 32, 36})
	if _, err := r.RunString("var x = (1n << 143423n) + 1n"); err != nil {
		t.Fatal(err)
	}
	chargedOver100(t, r, "Codex's case", "x.toString(32)")
	for _, radix := range []int{2, 8, 10, 16, 32, 36} {
		for _, edge := range classEdges {
			for d := -2; d <= 3; d++ {
				// A number of edge+d digits in radix.
				if _, err := r.RunString("var x = " + strconv.Itoa(radix) + "n ** " + strconv.Itoa(edge+d-1) + "n"); err != nil {
					t.Fatal(err)
				}
				chargedOver100(t, r, strconv.Itoa(edge+d)+" digits in radix "+strconv.Itoa(radix), "x.toString("+strconv.Itoa(radix)+")")
			}
		}
	}
}

// The results and temporaries of BigInt operations whose words sit about
// a size class, or a page, are charged what Go makes them in.
func TestBigIntOpsChargedAtSizeClasses(t *testing.T) {
	skipRaceAlloc(t)
	r := New()
	for _, edge := range []int{1024, 4096, 28672, 32768, 40960, 65536} {
		// A result of words + 1 words is made in words + 5: these put it
		// at the bound and past it.
		for _, d := range []int{-5, -4, -3, 0} {
			words := edge/8 + d
			if _, err := r.RunString("var x = (1n << " + strconv.Itoa(64*words-1) + "n) - 3n, y = x >> 7n, s = x.toString()"); err != nil {
				t.Fatal(err)
			}
			for _, op := range []string{"x + y", "x - y", "-x", "x * 3n", "x / 3n", "x % y", "x << 1n", "x >> 1n", "x & -y", "x | -y", "x ^ y", "~x", "BigInt.asUintN(" + strconv.Itoa(64*words) + ", -x)", "BigInt(s)"} {
				chargedOver100(t, r, strconv.Itoa(words)+" words: "+op, op)
			}
		}
	}
}

// The messages that hold a value, about a size class or a page long, are
// charged what Go makes them in: joined as ASCII, as UTF-16, from a string
// imported from Go, and converted to UTF-8 by hostText. What the error
// and its throw make besides, which is not charged, is taken from what the
// same messages make with values a character long.
func TestMessagesChargedAtSizeClasses(t *testing.T) {
	skipRaceAlloc(t)
	r := New()
	srcs := []string{
		"try { new Date()[Symbol.toPrimitive](A) } catch (e) {}",
		"try { new Date()[Symbol.toPrimitive](U) } catch (e) {}",
		"try { new Date()[Symbol.toPrimitive](G) } catch (e) {}",
		"try { Reflect.construct(Int16Array, [new ArrayBuffer(4), 1], F) } catch (e) {}",
		"try { new Date()[Symbol.toPrimitive](F) } catch (e) {}",
	}
	setup := func(n int) {
		t.Helper()
		if err := r.Set("G", strings.Repeat("é", 17)+strings.Repeat("é", max(n/2-17, 0))); err != nil {
			t.Fatal(err)
		}
		if _, err := r.RunString(`var A = "a".repeat(` + strconv.Itoa(n) + `), U = "\u3000".repeat(` + strconv.Itoa(max(n/2, 1)) + `), F = function() {};
			Object.defineProperty(F, "name", {value: U});`); err != nil {
			t.Fatal(err)
		}
	}
	run := func(src string) (counted, actual int64) {
		t.Helper()
		counted, actual, err := allocDuring(t, r, MustCompile("", "for (let i = 0; i < 100; i++) { "+src+" }", false), 0)
		if err != nil {
			t.Fatal(err)
		}
		return counted, actual
	}
	setup(1)
	var base [][2]int64
	for _, src := range srcs {
		c, a := run(src)
		base = append(base, [2]int64{c, a})
	}
	for _, edge := range classEdges {
		for d := -3; d <= 2; d++ {
			n := edge + d
			setup(n)
			for i, src := range srcs {
				c, a := run(src)
				if a-base[i][1] > c-base[i][0]+64<<10 {
					t.Errorf("%d: %s: made %d bytes more than with short values, charged %d more", n, src, a-base[i][1], c-base[i][0])
				}
			}
		}
	}
}
