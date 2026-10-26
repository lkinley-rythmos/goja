package goja

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The name and message of an error, and a constructor's name, in a message
// that holds a value are read for whether they are empty without the
// whole of a string imported from Go: Length scanned all of it, uncharged
// and with no interrupt check, before the message was charged (2^22 units
// of U+3000 as an error's message made 8.4 MB under a 1 MiB limit; 2^24
// returned 152 to 338 ms after an interrupt).
func TestMessagePropertiesImported(t *testing.T) {
	const setup = `var E = new Error(); E.message = S;
		var N = new Error("m"); N.name = S;
		var B = {name: "N", message: S, toString: Error.prototype.toString};
		function C() {} Object.defineProperty(C, "name", {value: S}); var K = new C();`
	sites := []string{"new Date()[Symbol.toPrimitive](%s)", "Reflect.ownKeys(new Proxy({}, {ownKeys() { return [%s] }}))"}
	r := New()
	for _, v := range []string{"E", "N", "B", "K"} {
		for _, site := range sites {
			// A string not scanned yet for each.
			if err := r.Set("S", strings.Repeat("　", 1<<22)); err != nil {
				t.Fatal(err)
			}
			if _, err := r.RunString(setup); err != nil {
				t.Fatal(err)
			}
			src := strings.ReplaceAll(site, "%s", v)
			counted, actual, err := allocDuring(t, r, MustCompile("", src, false), 1<<20)
			if !errors.Is(err, ErrAllocationLimitExceeded) {
				t.Errorf("%s: got %v, want the allocation limit", src, err)
			}
			if actual > 2<<20 {
				t.Errorf("%s: made %d bytes, charged %d", src, actual, counted)
			}
		}
	}
	if raceDetectorEnabled {
		return
	}
	r.SetAllocationLimit(0)
	for _, v := range []string{"E", "N", "B", "K"} {
		if err := r.Set("S", strings.Repeat("　", 1<<24)); err != nil {
			t.Fatal(err)
		}
		if _, err := r.RunString(setup); err != nil {
			t.Fatal(err)
		}
		interruptWithin(t, r, strings.ReplaceAll(sites[0], "%s", v), 5*time.Millisecond, 20*time.Millisecond)
	}
}

// What big.Int.Text makes to write a number of up to 2048 words itself is
// charged, in every radix: twenty x.toString(36) of 2048 words made
// 4,520,784 bytes and were charged 2,505,600, under a limit between the two.
func TestBigIntTextLeafCharged(t *testing.T) {
	skipRaceAlloc(t)
	r := New()
	for _, words := range []int{1, 2, 8, 9, 30, 64, 100, 700, 1500, 2048, 2049, 4500} {
		if _, err := r.RunString("var x = (1n << " + strconv.Itoa(64*words-1) + "n) - 12345n, y = -(1n << " + strconv.Itoa(64*(words-1)) + "n)"); err != nil {
			t.Fatal(err)
		}
		for radix := 2; radix <= 36; radix++ {
			if words > 100 && radix%7 != 3 && radix&(radix-1) != 0 && radix != 10 && radix != 36 {
				// Every radix up to 100 words; past it the powers of
				// two, 10, 36 and every seventh (3, 17, 24, 31).
				continue
			}
			src := "for (let i = 0; i < 20; i++) { x.toString(" + strconv.Itoa(radix) + "); y.toString(" + strconv.Itoa(radix) + ") }"
			counted, actual, err := allocDuring(t, r, MustCompile("", src, false), 0)
			if err != nil {
				t.Fatal(err)
			}
			if actual > counted+64<<10 {
				t.Errorf("%d words, radix %d: made %d bytes, charged %d", words, radix, actual, counted)
			}
		}
	}
	// Codex's case: refused under the limit between what it was charged
	// and what it made.
	if _, err := r.RunString("var z = (1n << 131071n) - 12345n"); err != nil {
		t.Fatal(err)
	}
	_, _, err := allocDuring(t, r, MustCompile("", "for (let i = 0; i < 20; i++) z.toString(36)", false), 2621440)
	if !errors.Is(err, ErrAllocationLimitExceeded) {
		t.Errorf("got %v, want the allocation limit", err)
	}
}

// A value is written as an error when its toString is Error.prototype.toString
// as the runtime made it, and not whatever function the property holds
// now, as Node v18.20.3 writes it (Docker,
// rcx-go-crud-parity-gap-goja-rle-api-1).
func TestMessageErrorToStringIntrinsic(t *testing.T) {
	const script = `
	const out = [];
	const msgs = v => {
		try { new Date()[Symbol.toPrimitive](v) } catch (e) { out.push(e.message) }
		try { Reflect.ownKeys(new Proxy({}, {ownKeys() { return [v] }})) } catch (e) { out.push(e.message) }
	};
	const orig = Error.prototype.toString;
	Error.prototype.toString = function replacement() {};
	msgs({name: "N", message: "M", toString: Error.prototype.toString});
	msgs({name: "N", message: "M", toString: orig});
	msgs(Object.assign(new Error("m"), {name: "E"}));
	Error.prototype.toString = orig;
	msgs({name: "N", message: "M", toString: orig});
	out.join("\n")`
	const want = `Invalid hint: [object Object]
[object Object] is not a valid property name
Invalid hint: N: M
N: M is not a valid property name
Invalid hint: E: m
E: m is not a valid property name
Invalid hint: N: M
N: M is not a valid property name`
	v, err := New().RunString(script)
	if err != nil {
		t.Fatal(err)
	}
	if got := v.String(); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}
