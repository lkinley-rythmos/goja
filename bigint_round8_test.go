package goja

import (
	"errors"
	"math/rand"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/dop251/goja/unistring"
)

// A function in a message is cut to its first 111 and last 2 code units
// past 128, and only those are read: its source was made whole first,
// through Go strings, uncharged. A native function named with 2^22 units
// of U+3000 made 149 MB under a 1 MiB limit, and one named with 2^24
// returned 345 ms after an interrupt; a script function whose source is
// 2^24 such units returned 159 ms after one.
func TestMessageFunctionSourceRead(t *testing.T) {
	r := New()
	if err := r.Set("G", strings.Repeat("é", 1<<22)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RunString(`var N = Math.abs, M = Math.min, F = eval("(function(){/*" + "　".repeat(1 << 22) + "*/})");
		Object.defineProperty(N, "name", {value: "　".repeat(1 << 22)}); Object.defineProperty(M, "name", {value: G});`); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"N", "M", "F"} {
		for _, src := range []string{
			"new Date()[Symbol.toPrimitive](" + f + ")",
			"Reflect.ownKeys(new Proxy({}, {ownKeys() { return [" + f + "] }}))",
		} {
			counted, actual, err := allocDuring(t, r, MustCompile("", src, false), 1<<20)
			var ex *Exception
			if !errors.As(err, &ex) || errors.Is(err, ErrAllocationLimitExceeded) {
				t.Fatalf("%s: got %v, want its TypeError", src, err)
			}
			if msg := ex.Value().(*Object).Get("message").String(); utf8.RuneCountInString(msg) > 160 {
				t.Errorf("%s: a message of %d characters", src, utf8.RuneCountInString(msg))
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
	if err := r.Set("G", strings.Repeat("é", 1<<24)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RunString(`F = eval("(function(){/*" + "　".repeat(1 << 24) + "*/})");
		Object.defineProperty(N, "name", {value: "　".repeat(1 << 24)}); Object.defineProperty(M, "name", {value: G});`); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"N", "M", "F"} {
		interruptWithin(t, r, "new Date()[Symbol.toPrimitive]("+f+")", 5*time.Millisecond, 20*time.Millisecond)
	}
}

// The cut of a function's source in a message is the one Node v18.20.3
// makes (Object::NoSideEffectsToString), at every length about 128 and
// with surrogate pairs across each place it cuts: the same 160 sources
// give the same lengths, and their messages the same cut, in Docker Node
// v18.20.3 (rcx-go-crud-parity-gap-goja-rle-api-1).
func TestMessageFunctionSourceCutAsNode(t *testing.T) {
	const script = `
	const P = "\ud842\udfb7", fill = n => "a".repeat(n), srcs = [];
	for (let n = 120; n < 140; n++) {
		srcs.push("(function(){/*" + fill(n - 16) + "*/})");
		srcs.push("(function(){/*" + fill(97) + P + fill(n - 18 - 97) + "*/})");
		srcs.push("(function(){/*" + fill(96) + P + fill(n - 18 - 96) + "*/})");
		srcs.push("(() => '" + fill(n - 10) + P + "')");
		srcs.push("(() => '" + fill(n - 11) + P + "a')");
		srcs.push("(() => \"" + "\u00e9".repeat(n - 8) + "\")");
		srcs.push("(() => ` + "`" + `" + "\u3000".repeat(n - 8) + "` + "`" + `)");
		srcs.push("(() => '" + P.repeat((n - 8) >> 1) + fill((n - 8) & 1) + "')");
	}
	const bad = [];
	for (const s of srcs) {
		const f = eval(s), text = Function.prototype.toString.call(f);
		const cut = text.length > 128 ? text.slice(0, 111) + "...<omitted>..." + text.slice(-2) : text;
		try { new Date()[Symbol.toPrimitive](f) } catch (e) { if (e.message !== "Invalid hint: " + cut) bad.push(s) }
		try { Reflect.ownKeys(new Proxy({}, {ownKeys() { return [f] }})) } catch (e) { if (e.message !== cut + " is not a valid property name") bad.push(s) }
	}
	bad.join("\n")`
	v, err := New().RunString(script)
	if err != nil {
		t.Fatal(err)
	}
	if s := v.String(); s != "" {
		t.Fatalf("cut otherwise than Node for:\n%s", s)
	}
}

func utf16Bytes(u []uint16) []byte {
	b := make([]byte, 0, 2*len(u))
	for _, c := range u {
		b = append(b, byte(c), byte(c>>8))
	}
	return b
}

// hostText checks the interrupt between its blocks however surrogate pairs
// fall across them: at 2^25 pairs after one ASCII character every pair
// started at an odd index, no index reached a multiple of the block, and a
// conversion returned 133 ms after an interrupt (1.5 ms with no such
// character). Its code units are kept, a pair split by a block included.
func TestHostTextBlockEdges(t *testing.T) {
	r := New()
	pair := []uint16{0xd83d, 0xde00}
	for _, at := range []int{textBlock - 2, textBlock - 1, textBlock, 2*textBlock - 1, 2 * textBlock} {
		u := make([]uint16, 1, 2*textBlock+8)
		u[0] = unistring.BOM
		for len(u)-1 < at {
			u = append(u, 'a')
		}
		u = append(u, pair...)
		u = append(u, 0xd800, 'b', 0xdc00)
		for len(u)-1 < 2*textBlock+4 {
			u = append(u, pair...)
		}
		want := string(utf16.Decode(u[1:]))
		if got := r.hostText(unicodeString(u)); got != want {
			t.Errorf("a pair at %d: the text differs", at)
		}
	}
	if raceDetectorEnabled {
		return
	}
	for _, prefix := range []string{"", "x"} {
		if err := r.Set("prefix", prefix); err != nil {
			t.Fatal(err)
		}
		if _, err := r.RunString(`var v = prefix + "\ud83d\ude00".repeat(1 << 24);
			Object.defineProperty(Uint8Array, "name", {value: v, configurable: true}); var a = new Uint8Array(0);`); err != nil {
			t.Fatal(err)
		}
		interruptWithin(t, r, "new Int16Array(a)", 5*time.Millisecond, 20*time.Millisecond)
	}
}

// The other loops that read text a block at a time keep their code units
// with a pair across a block's edge: joinText copies units, importedText
// counts runes, trimSpaceUTF8 runes, bigIntSource code units.
func TestTextBlockLoopsPairAtEdges(t *testing.T) {
	r := New()
	for _, at := range []int{textBlock - 2, textBlock - 1, textBlock, textBlock + 1} {
		// joinText: a pair at units at and at+1 of its second part.
		u := make([]uint16, 1, textBlock+8)
		u[0] = unistring.BOM
		for len(u)-1 < at {
			u = append(u, 'a')
		}
		u = append(u, 0xd83d, 0xde00, 0xdc00, 'z')
		got := r.joinText(asciiString("p"), unicodeString(u), asciiString("s"))
		want := append(append([]uint16{unistring.BOM, 'p'}, u[1:]...), 's')
		if gu, ok := got.(unicodeString); !ok || string(utf16Bytes(gu)) != string(utf16Bytes(want)) {
			t.Errorf("joinText, a pair at %d: the units differ", at)
		}

		// importedText: a non-BMP rune as the at-th rune, invalid bytes after.
		str := strings.Repeat("é", at) + "😀\xed\xa0\x80" + strings.Repeat("é", 3)
		is := &importedString{s: str}
		if gu, ok := r.importedText(is).(unicodeString); !ok || string(utf16Bytes(gu)) != string(utf16Bytes(unistring.Scan(str))) {
			t.Errorf("importedText, a pair at %d: the units differ", at)
		}

		// trimSpaceUTF8: white space up to a non-BMP rune at the at-th rune.
		lead := strings.Repeat("\u3000", at)
		if got := r.trimSpaceUTF8(lead + "😀1" + lead); got != "😀1" {
			t.Errorf("trimSpaceUTF8, a pair at %d: got %d bytes", at, len(got))
		}

		// bigIntSource: white space up to a pair at code unit at, which no
		// BigInt has; and to digits.
		w := make([]uint16, 1, 2*at+8)
		w[0] = unistring.BOM
		for len(w)-1 < at {
			w = append(w, 0x3000)
		}
		if _, ok := r.bigIntSource(unicodeString(append(append([]uint16{}, w...), 0xd83d, 0xde00, '1'))); ok {
			t.Errorf("bigIntSource, a pair at %d: read as a BigInt's text", at)
		}
		if got, ok := r.bigIntSource(unicodeString(append(append(append([]uint16{}, w...), '1', '2'), w[1:]...))); !ok || got != "12" {
			t.Errorf("bigIntSource, digits at %d: got %q, %v", at, got, ok)
		}
	}
}

// The function arm of noSideEffectsToString, which the incompatible
// receiver errors, the errors for null or undefined bases, the in
// operator's and others write a value with, cuts a function's source as
// V8 does and reads only what it keeps: it wrote the whole source, with
// no cut, through Go strings (a native function named with 2^22 units of
// U+3000 made 298 MB under a 1 MiB limit, charged 256 bytes; named with
// 2^24, 1.16 GB, and returned 659 ms after an interrupt; a script
// function's source of 2^24 such units 611 MB, 465 ms).
func TestNoSideEffectsFunctionSourceRead(t *testing.T) {
	r := New()
	if err := r.Set("G", strings.Repeat("é", 1<<22)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RunString(`var N = Math.abs, M = Math.min, F = eval("(function(){/*" + "　".repeat(1 << 22) + "*/})");
		Object.defineProperty(N, "name", {value: "　".repeat(1 << 22)}); Object.defineProperty(M, "name", {value: G});`); err != nil {
		t.Fatal(err)
	}
	sites := []string{"Map.prototype.clear.call(%s)", "null[%s]", "Set.prototype.add.call(%s, 1)", "%s in 5", "Promise.prototype.then.call(%s)"}
	for _, f := range []string{"N", "M", "F"} {
		for _, site := range sites {
			src := strings.ReplaceAll(site, "%s", f)
			counted, actual, err := allocDuring(t, r, MustCompile("", src, false), 1<<20)
			var ex *Exception
			if !errors.As(err, &ex) || errors.Is(err, ErrAllocationLimitExceeded) {
				t.Fatalf("%s: got %v, want its TypeError", src, err)
			}
			if msg := ex.Value().(*Object).Get("message").String(); utf8.RuneCountInString(msg) > 220 {
				t.Errorf("%s: a message of %d characters", src, utf8.RuneCountInString(msg))
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
	if err := r.Set("G", strings.Repeat("é", 1<<24)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RunString(`F = eval("(function(){/*" + "　".repeat(1 << 24) + "*/})");
		Object.defineProperty(N, "name", {value: "　".repeat(1 << 24)}); Object.defineProperty(M, "name", {value: G});`); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"N", "M", "F"} {
		for _, site := range sites[:2] {
			interruptWithin(t, r, strings.ReplaceAll(site, "%s", f), 5*time.Millisecond, 20*time.Millisecond)
		}
	}
}

// Those messages hold the cut Node v18.20.3 makes, of native and script
// functions shorter and longer than 128 code units, with surrogate pairs
// in what is kept: the same 624 cases give the same messages in Docker
// Node v18.20.3 (rcx-go-crud-parity-gap-goja-rle-api-1). A pair the cut
// splits leaves an unpaired surrogate, which these messages, made in Go
// strings, write as U+FFFD, where Node keeps it.
func TestNoSideEffectsFunctionSourceCutAsNode(t *testing.T) {
	const script = `
	const P = "\ud842\udfb7", fill = n => "a".repeat(n), fns = [Math.abs, Map, function f(a) { return a }, () => 1];
	for (let n = 120; n < 140; n++) {
		fns.push(eval("(function(){/*" + fill(n - 16) + "*/})"));
		fns.push(eval("(function(){/*" + fill(96) + P + fill(n - 18 - 96) + "*/})"));
		fns.push(eval("(() => '" + fill(n - 11) + P + "a')"));
		fns.push(eval("(() => \"" + "\u00e9".repeat(n - 8) + "\")"));
		fns.push(eval("(() => ` + "`" + `" + "\u3000".repeat(n - 8) + "` + "`" + `)"));
	}
	const sites = [
		[f => Map.prototype.clear.call(f), c => "Method Map.prototype.clear called on incompatible receiver " + c],
		[f => Set.prototype.add.call(f, 1), c => "Method Set.prototype.add called on incompatible receiver " + c],
		[f => Promise.prototype.then.call(f), c => "Method Promise.prototype.then called on incompatible receiver " + c],
		[f => null[f], c => "Cannot read properties of null (reading '" + c + "')"],
		[f => { undefined[f] = 1 }, c => "Cannot set properties of undefined (setting '" + c + "')"],
		[f => f in 5, c => "Cannot use 'in' operator to search for '" + c + "' in 5"],
	];
	const bad = [];
	for (const f of fns) {
		const text = Function.prototype.toString.call(f);
		const cut = text.length > 128 ? text.slice(0, 111) + "...<omitted>..." + text.slice(-2) : text;
		for (const [site, want] of sites) {
			try { site(f); bad.push("no error: " + text) } catch (e) { if (e.message !== want(cut)) bad.push(e.message) }
		}
	}
	bad.join("\n")`
	v, err := New().RunString(script)
	if err != nil {
		t.Fatal(err)
	}
	if s := v.String(); s != "" {
		t.Fatalf("written otherwise than Node:\n%s", s)
	}
}

// sourceMessageParts reads the code units a function's source keeps in a
// message from the parts source() makes it of, as making all of it and then
// cutting it would: from ASCII, UTF-16 (unpaired surrogates kept) and
// imported strings, scanned or read in their UTF-8 (an invalid byte as
// U+FFFD), unscanned ones left so.
func TestSourceMessagePartsUnits(t *testing.T) {
	r := New()
	rnd := rand.New(rand.NewSource(1))
	unitsOf := func(s String) []uint16 {
		if is, ok := s.(*importedString); ok {
			if u := unistring.Scan(is.s); u != nil {
				return u[1:]
			}
			s = asciiString(is.s)
		}
		a, u := devirtualizeString(s)
		if u != nil {
			return u[1:]
		}
		w := make([]uint16, len(a))
		for i := range a {
			w[i] = uint16(a[i])
		}
		return w
	}
	omitted := utf16.Encode([]rune("...<omitted>..."))
	for n := 0; n < 20000; n++ {
		parts := make([]String, 1+rnd.Intn(3))
		var want []uint16
		for i := range parts {
			parts[i] = randomText(rnd, rnd.Intn(4)*rnd.Intn(80))
			want = append(want, unitsOf(parts[i])...)
		}
		if len(want) > 128 {
			want = append(append(append([]uint16{}, want[:111]...), omitted...), want[len(want)-2:]...)
		}
		scanned := make([]bool, len(parts))
		for i, p := range parts {
			if is, ok := p.(*importedString); ok {
				scanned[i] = is.scanned
			}
		}
		var got []uint16
		for _, p := range r.sourceMessageParts(parts) {
			if u, ok := p.(unicodeString); ok {
				ascii := true
				for _, c := range u[1:] {
					ascii = ascii && c < utf8.RuneSelf
				}
				if ascii {
					t.Fatalf("an ASCII part as UTF-16: %v", u)
				}
			}
			got = append(got, unitsOf(p)...)
		}
		if string(utf16Bytes(got)) != string(utf16Bytes(want)) {
			t.Fatalf("parts %#v:\ngot  %v\nwant %v", parts, got, want)
		}
		for i, p := range parts {
			if is, ok := p.(*importedString); ok && is.scanned != scanned[i] {
				t.Fatalf("an imported string scanned: %q", is.s)
			}
		}
	}
}

// randomText is a String of about n pieces of every kind
// sourceMessageParts reads: ASCII, UTF-16 with pairs and unpaired
// surrogates, and imported, scanned or not, with invalid UTF-8.
func randomText(rnd *rand.Rand, n int) String {
	switch rnd.Intn(4) {
	case 0:
		return asciiString(strings.Repeat("x", n))
	case 1:
		u := []uint16{unistring.BOM, 0xe9}
		for i := 0; i < n; i++ {
			u = append(u, []uint16{'b', 0xd800, 0xdc00, 0xd83d, 0xde00, 0x3000}[rnd.Intn(6)])
		}
		return unicodeString(u)
	}
	pieces := []string{"a", "é", "\u3000", "😀", "\xff", "\x80", "\xbf", "\xc2", "\xe3\x80", "\xf0\x9f", "\xf0\x9f\x98", "\xed\xa0\x80", "\xf4\x90\x80\x80", "\xc0\xaf", "}"}
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(pieces[rnd.Intn(len(pieces))])
	}
	s := &importedString{s: b.String()}
	if rnd.Intn(3) == 0 {
		s.scan()
	}
	return s
}

// runeStartBefore finds where a rune starts as a range over all of s reads
// it, from any byte: checked against such a range at every position of
// strings of valid and invalid sequences.
func TestRuneStartBefore(t *testing.T) {
	rnd := rand.New(rand.NewSource(2))
	for n := 0; n < 50000; n++ {
		is, ok := randomText(rnd, rnd.Intn(12)).(*importedString)
		if !ok {
			continue
		}
		str := is.s
		starts := make([]bool, len(str)+1)
		for i := range str {
			starts[i] = true
		}
		starts[len(str)] = true
		for p := 0; p <= len(str); p++ {
			want := p
			for !starts[want] {
				want--
			}
			if got := runeStartBefore(str, p); got != want {
				t.Fatalf("%q at %d: got %d, want %d", str, p, got, want)
			}
		}
	}
}
