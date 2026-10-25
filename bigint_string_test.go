package goja

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// runLogged runs script with a console.log that collects its lines.
func runLogged(t *testing.T, script string) []string {
	t.Helper()
	r := New()
	var lines []string
	console := r.NewObject()
	console.Set("log", func(call FunctionCall) Value {
		parts := make([]string, len(call.Arguments))
		for i, a := range call.Arguments {
			parts[i] = a.String()
		}
		lines = append(lines, strings.Join(parts, " "))
		return _undefined
	})
	r.Set("console", console)
	if _, err := r.RunString(script); err != nil {
		t.Fatal(err)
	}
	return lines
}

func compareLines(t *testing.T, got []string, want string) {
	t.Helper()
	w := strings.Split(want, "\n")
	if len(got) != len(w) {
		t.Fatalf("got %d lines, want %d", len(got), len(w))
	}
	for i := range w {
		if got[i] != w[i] {
			t.Errorf("got  %s\nwant %s", got[i], w[i])
		}
	}
}

// StringToBigInt as Node v18.20.3 does it: a decimal string with leading
// zeros is decimal (big.Int.SetString in base 0 took it for octal), no sign
// after a 0x, 0o or 0b prefix, white space trimmed; the comparisons and ==
// with a string the same.
func TestStringToBigIntAsNode(t *testing.T) {
	compareLines(t, runLogged(t, stringToBigIntScript), stringToBigIntNode)
}

const stringToBigIntScript = `const show = (f) => { try { const r = f(); return typeof r === 'bigint' ? r.toString() + 'n' : typeof r + ':' + String(r); } catch (e) { return e.name + ': ' + e.message; } };
const strs = ["0123", "-0123", "+0123", "00", "-00", "0", "-0", "+0", "", "   ", " \t\n12  ", "` + "\ufeff" + `7 ", "0x1f", "0X1F", "-0x1f", "+0x1f", "0x", "0o17", "0O17", "0o8", "0b101", "0B101", "0b2", "00x1", "0x-1", "1_000", "1n", "1e3", "1.0", "1.", ".5", "+", "-", "--1", "+-1", "0123456789012345678901234567890", "-0123456789012345678901234567890", "000000000000000000000000000000000000000001", "0x0000000000000000000000000000000000000001f", "1 2", "١٢", "0x1g", "Infinity"];
for (const s of strs) console.log('BigInt(' + JSON.stringify(s) + ') =>', show(() => BigInt(s)));
for (const s of ["0123456789012345678901234567890", "0x1f", "-0123", "", "abc"]) {
  console.log(JSON.stringify(s), '== 123456789012345678901234567890n', show(() => s == 123456789012345678901234567890n), '< 2n**100n', show(() => s < 2n ** 100n), '> 0n', show(() => s > 0n));
}
`

const stringToBigIntNode = `BigInt("0123") => 123n
BigInt("-0123") => -123n
BigInt("+0123") => 123n
BigInt("00") => 0n
BigInt("-00") => 0n
BigInt("0") => 0n
BigInt("-0") => 0n
BigInt("+0") => 0n
BigInt("") => 0n
BigInt("   ") => 0n
BigInt(" \t\n12  ") => 12n
BigInt("` + "\ufeff" + `7 ") => 7n
BigInt("0x1f") => 31n
BigInt("0X1F") => 31n
BigInt("-0x1f") => SyntaxError: Cannot convert -0x1f to a BigInt
BigInt("+0x1f") => SyntaxError: Cannot convert +0x1f to a BigInt
BigInt("0x") => SyntaxError: Cannot convert 0x to a BigInt
BigInt("0o17") => 15n
BigInt("0O17") => 15n
BigInt("0o8") => SyntaxError: Cannot convert 0o8 to a BigInt
BigInt("0b101") => 5n
BigInt("0B101") => 5n
BigInt("0b2") => SyntaxError: Cannot convert 0b2 to a BigInt
BigInt("00x1") => SyntaxError: Cannot convert 00x1 to a BigInt
BigInt("0x-1") => SyntaxError: Cannot convert 0x-1 to a BigInt
BigInt("1_000") => SyntaxError: Cannot convert 1_000 to a BigInt
BigInt("1n") => SyntaxError: Cannot convert 1n to a BigInt
BigInt("1e3") => SyntaxError: Cannot convert 1e3 to a BigInt
BigInt("1.0") => SyntaxError: Cannot convert 1.0 to a BigInt
BigInt("1.") => SyntaxError: Cannot convert 1. to a BigInt
BigInt(".5") => SyntaxError: Cannot convert .5 to a BigInt
BigInt("+") => SyntaxError: Cannot convert + to a BigInt
BigInt("-") => SyntaxError: Cannot convert - to a BigInt
BigInt("--1") => SyntaxError: Cannot convert --1 to a BigInt
BigInt("+-1") => SyntaxError: Cannot convert +-1 to a BigInt
BigInt("0123456789012345678901234567890") => 123456789012345678901234567890n
BigInt("-0123456789012345678901234567890") => -123456789012345678901234567890n
BigInt("000000000000000000000000000000000000000001") => 1n
BigInt("0x0000000000000000000000000000000000000001f") => 31n
BigInt("1 2") => SyntaxError: Cannot convert 1 2 to a BigInt
BigInt("١٢") => SyntaxError: Cannot convert ١٢ to a BigInt
BigInt("0x1g") => SyntaxError: Cannot convert 0x1g to a BigInt
BigInt("Infinity") => SyntaxError: Cannot convert Infinity to a BigInt
"0123456789012345678901234567890" == 123456789012345678901234567890n boolean:true < 2n**100n boolean:true > 0n boolean:true
"0x1f" == 123456789012345678901234567890n boolean:false < 2n**100n boolean:true > 0n boolean:true
"-0123" == 123456789012345678901234567890n boolean:false < 2n**100n boolean:true > 0n boolean:false
"" == 123456789012345678901234567890n boolean:false < 2n**100n boolean:true > 0n boolean:false
"abc" == 123456789012345678901234567890n boolean:false < 2n**100n boolean:false > 0n boolean:false`

// runWithin runs src and fails unless it returns within limit (in a
// goroutine, so that a run that does not return fails the test).
func runWithin(t *testing.T, r *Runtime, src string, limit time.Duration) (Value, error) {
	t.Helper()
	limit = slack(limit)
	type result struct {
		v   Value
		err error
	}
	done := make(chan result, 1)
	go func() {
		v, err := r.RunString(src)
		done <- result{v, err}
	}()
	select {
	case res := <-done:
		return res.v, res.err
	case <-time.After(limit):
		t.Fatalf("%.60s: did not return within %v", src, limit)
	}
	return nil, nil
}

// V8 refuses a string of more digits than a BigInt can have (bigdec.TooLong)
// before converting it, with the SyntaxError it gives a string that is not
// a number, whose text it cuts at 1,000 code units; a comparison with it is
// false. Node v18.20.3's results.
func TestStringToBigIntMaxSize(t *testing.T) {
	skipHeavy(t)
	r := New()
	if _, err := r.RunString(`
	var dec = "1".repeat(318767105), hex = "0x" + "f".repeat(268435457), oct = "0o" + "7".repeat(352321537);
	function msg(f) { try { f(); } catch (e) { return e.name + ": " + e.message; } return "no error"; }
	`); err != nil {
		t.Fatal(err)
	}
	want := "SyntaxError: Cannot convert " + strings.Repeat("1", 1000) + "… to a BigInt"
	for _, src := range []string{`msg(() => BigInt(dec))`, `msg(() => BigInt.asUintN(5, dec))`} {
		v, err := runWithin(t, r, src, 10*time.Second)
		if err != nil || v.String() != want {
			t.Fatalf("%s: got %.80v, %v", src, v, err)
		}
	}
	for _, src := range []string{`msg(() => BigInt(hex))`, `msg(() => BigInt(oct))`} {
		v, err := runWithin(t, r, src, 10*time.Second)
		if err != nil || !strings.HasPrefix(v.String(), "SyntaxError: Cannot convert 0") || !strings.HasSuffix(v.String(), "… to a BigInt") {
			t.Fatalf("%s: got %.80v, %v", src, v, err)
		}
	}
	v, err := runWithin(t, r, `[1n < dec, 1n == dec, dec > 1n, dec == 1n, BigInt("x".repeat(1001)) === 0]`, 10*time.Second)
	if err == nil || !strings.Contains(err.Error(), "Cannot convert "+strings.Repeat("x", 1000)+"… to a BigInt") {
		t.Fatalf("got %v, %v", v, err)
	}
	v, err = runWithin(t, r, `[1n < dec, 1n == dec, dec > 1n, dec == 1n].join()`, 10*time.Second)
	if err != nil || v.String() != "false,false,false,false" {
		t.Fatalf("got %v, %v", v, err)
	}
	// At the limit, the strings convert.
	v, err = runWithin(t, r, `var h = BigInt("0x" + "f".repeat(268435456)); [h >> (2n ** 30n - 1n), h % 65536n, typeof BigInt("0o00" + "7".repeat(352321536))].join()`, 60*time.Second)
	if err != nil || v.String() != "1,65535,bigint" {
		t.Fatalf("got %v, %v", v, err)
	}
	// A literal of more digits is a syntax error.
	_, err = Compile("", "0x"+strings.Repeat("f", 268435457)+"n", false)
	if err == nil || !strings.Contains(err.Error(), "Maximum BigInt size exceeded") {
		t.Fatalf("literal: got %v", err)
	}
}

// Converting a long decimal string to a BigInt is made in parts, with the
// interrupt checked between them.
func TestStringToBigIntInterruptible(t *testing.T) {
	skipHeavy(t)
	r := New()
	r.Set("s", strings.Repeat("7", 20000000))
	delay := 300 * time.Millisecond
	timer := time.AfterFunc(delay, func() { r.Interrupt("stop") })
	defer timer.Stop()
	start := time.Now()
	_, err := runWithin(t, r, "BigInt(s)", 30*time.Second)
	var ie *InterruptedError
	if !errors.As(err, &ie) {
		t.Fatalf("got %v, want interrupted", err)
	}
	if took := time.Since(start); took > delay+slack(250*time.Millisecond) {
		t.Fatalf("interrupted after %v", took)
	}
}

// A script's == and < with a BigInt and a string convert the string through
// the runtime: interruptibly and charged (Runtime.looseEquals,
// Runtime.cmp).
func TestStringToBigIntComparisonsInterruptible(t *testing.T) {
	skipHeavy(t)
	for _, src := range []string{"s == 7n", "7n == s", "1n < s", "s > 1n", "s != 7n", "({valueOf() { return s }}) == 7n"} {
		t.Run(src, func(t *testing.T) {
			r := New()
			r.Set("s", strings.Repeat("7", 20000000))
			delay := 300 * time.Millisecond
			timer := time.AfterFunc(delay, func() { r.Interrupt("stop") })
			defer timer.Stop()
			start := time.Now()
			_, err := runWithin(t, r, src, 60*time.Second)
			var ie *InterruptedError
			if !errors.As(err, &ie) {
				t.Fatalf("got %v, want interrupted", err)
			}
			if took := time.Since(start); took > delay+slack(250*time.Millisecond) {
				t.Fatalf("interrupted after %v", took)
			}

			r = New()
			r.Set("s", strings.Repeat("7", 20000000))
			r.SetAllocationLimit(1 << 20)
			start = time.Now()
			_, err = runWithin(t, r, src, 60*time.Second)
			if !errors.Is(err, ErrAllocationLimitExceeded) {
				t.Fatalf("got %v, want the allocation limit", err)
			}
			if took := time.Since(start); took > slack(500*time.Millisecond) {
				t.Fatalf("took %v", took)
			}
		})
	}
	// Strings with white space other than ASCII compare as Node v18.20.3
	// compares them.
	testScript(`[12n == "\u00a012", "\u00a012" == 12n, 12n == "\u2003 12\ufeff", ({valueOf(){return "12"}}) == 12n, 12n == {toString(){return "12"}}, 12n < "\u00a013", new String("12") == 12n].join()`,
		asciiString("true,true,true,true,true,true,true"), t)
}
