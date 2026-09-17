package goja

import "testing"

// coerceGoldenScript exercises every string/regexp builtin that coerces
// its receiver or an argument to a string, with receivers/arguments that
// already are strings, objects with toString/valueOf, primitives,
// null/undefined, Symbols (TypeError text), non-ASCII strings, and the
// empty-string edge cases.
const coerceGoldenScript = `
var out = [];
var s = 'hello world hello';
var objS = {toString: function() { return 'wor' }}; var objV = {valueOf: function() { return 42 }, toString: function() { return 'v' }};
out.push(s.indexOf('hello'), s.indexOf('hello', 1), s.indexOf(objS), s.indexOf(objV), s.indexOf(), s.indexOf(undefined), s.indexOf(null), s.indexOf(1), s.lastIndexOf('hello'), s.lastIndexOf(objS), s.includes('world'), s.includes(objS), s.startsWith('hel'), s.startsWith(objS, 6), s.endsWith('llo'), s.endsWith(objS, 9));
out.push(s.split(' ').join('|'), s.split(objS).length, s.replace('hello', 'bye'), s.replace(objS, objV), s.replaceAll('hello', 'x'), s.search('wor'), s.search(objS), JSON.stringify(s.match('l+')), s.match(objS)[0], s.concat(objS, 1, null), s.padStart(20, objS), s.repeat(2).length, s.at(-1), s.charAt(objV), s.substring(objV), s.slice(objS), s.localeCompare(objS), s.normalize(), s.trim(), s.toUpperCase(), s.codePointAt(objV), s.charCodeAt(1));
out.push(/wor/.test(s), /wor/.test(objS), /wor/.test(objV), /o/g.exec(s).index, /o/.exec(objS)[0], String(/x/.exec(null)), /nul/.test(null), /undef/.test(undefined), /42/.test(objV), new RegExp(objS).source, new RegExp('a', 'g').flags, 'aXbXc'.split(/X/).join('-'), 'abc'.replace(/b/, objS), 'abc'.replace(/b/, function(m) { return m.toUpperCase() }), s.match(/l/g).length, [...s.matchAll(/l/g)].length, s.search(/x/));
var nonStr = [123, true, null, undefined, {a: 1}, [1, 2], 1.5];
for (var i = 0; i < nonStr.length; i++) { try { out.push(String.prototype.indexOf.call(nonStr[i], '1') + ':' + String.prototype.slice.call(nonStr[i], 0, 2) + ':' + String.prototype.split.call(nonStr[i], ',').length) } catch (e) { out.push(e.constructor.name + ':' + e.message) } }
try { s.indexOf(Symbol('x')) } catch (e) { out.push(e.constructor.name + ':' + e.message) }
try { /x/.test(Symbol('x')) } catch (e) { out.push(e.constructor.name + ':' + e.message) }
try { String.prototype.indexOf.call(Symbol('x'), 'x') } catch (e) { out.push(e.constructor.name + ':' + e.message) }
try { s.split(Symbol('x')) } catch (e) { out.push(e.constructor.name + ':' + e.message) }
var u = 'héllo wörld'; out.push(u.indexOf('wör'), u.indexOf({toString: function() { return 'ö' }}), u.split('ö').join('|'), u.replace('é', 'e'), /ö/.test(u), u.lastIndexOf('l'), u.includes('llo w'), u.startsWith('hé'), u.endsWith('rld'));
out.push(''.indexOf(''), 'abc'.indexOf('', 5), 'abc'.lastIndexOf('', 1), 'aaa'.split('').length, 'a,b'.split(',', 1).join(''), 'x'.padEnd(3, objS), String.raw({raw: ['a', 'b']}, objS), 'abc'.startsWith(''), 'abc'.endsWith('c', 3));
out.join('|');
`

// coerceGolden was captured from the unmodified engine at e41cf0f.
const coerceGolden = `0|12|6|-1|-1|-1|-1|-1|12|6|true|true|true|true|true|true|hello|world|hello|2|bye world hello|hello vld hello|x world x|6|6|["ll"]|wor|hello world hellowor1null|worhello world hello|34|o|||hello world hello|-1|hello world hello|hello world hello|HELLO WORLD HELLO||101|true|true|false|4|o|null|true|true|false|wor|g|a-b-c|aworc|aBc|5|5|-1|0:12:1|-1:tr:1|TypeError:Value is not object coercible|TypeError:Value is not object coercible|-1:[o:1|0:1,:2|0:1.:1|TypeError:Cannot convert a Symbol value to a string|TypeError:Cannot convert a Symbol value to a string|TypeError:Cannot convert a Symbol value to a string|TypeError:Cannot convert a Symbol value to a string|6|7|héllo w|rld|hello wörld|true|9|true|true|true|0|3|1|3|a|xwo|aworb|true|true`

// runGolden runs script in a fresh runtime and compares the result's
// string form with want. (testScript compares Values, and an asciiString
// expectation would never equal a unicodeString result such as the
// non-ASCII segments of the coercion golden.)
func runGolden(t *testing.T, name, script, want string) {
	t.Helper()
	v, err := New().RunString(script)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if got := v.String(); got != want {
		t.Fatalf("%s golden mismatch:\n got %q\nwant %q", name, got, want)
	}
}

func TestStringCoercionCharacterization(t *testing.T) {
	runGolden(t, "coerce", coerceGoldenScript, coerceGolden)
}

// measureProgramAllocs returns the allocations of running src once in vm,
// minus the fixed cost of RunProgram itself (measured on the constant 1).
func measureProgramAllocs(t *testing.T, vm *Runtime, src string) float64 {
	t.Helper()
	run := func(src string) float64 {
		prg, err := Compile("m", src, false)
		if err != nil {
			t.Fatal(err)
		}
		return testing.AllocsPerRun(100, func() {
			if _, err := vm.RunProgram(prg); err != nil {
				panic(err)
			}
		})
	}
	return run(src) - run("1")
}

// TestStringCoercionNoRebox: coercing a value that already is a String
// must not allocate. indexOf coerces this and the argument (2 boxes);
// split coerces its receiver (1 box — the separator is devirtualised without a box);
// RegExp.prototype.test coerces the argument (1). Counts measured on e41cf0f
// before: 3 / 3 / 10 / 9; after: 1 / 1 / 8 / 8 (split is 8 since Task 4's
// array box).
func TestStringCoercionNoRebox(t *testing.T) {
	vm := New()
	if _, err := vm.RunString(`var str = 'hello world'`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		src      string
		want     float64
		raceSkip bool
	}{
		{"str.indexOf('l')", 1, false},
		{"str.indexOf('l', 1)", 1, false},
		{"str.split('l')", 8, false},
		// regexp caches backtracker state in a sync.Pool; under the race
		// detector sync.Pool.Put randomly drops one item in four (see
		// GOROOT sync/pool.go), so the allocation count here is not stable.
		{"/l/.test(str)", 8, true},
	} {
		if raceDetectorEnabled && tc.raceSkip {
			t.Logf("skipping %s under the race detector: regexp's sync.Pool drops entries at random there, so the count is not stable", tc.src)
			continue
		}
		if got := measureProgramAllocs(t, vm, tc.src); got != tc.want {
			t.Errorf("%s: %v allocations, want %v", tc.src, got, tc.want)
		}
	}
}
