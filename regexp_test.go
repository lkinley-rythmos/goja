package goja

import (
	"fmt"
	"regexp"
	"regexp/syntax"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unsafe"
)

func TestRegexp1(t *testing.T) {
	const SCRIPT = `
	var r = new RegExp("(['\"])(.*?)\\1");
	var m = r.exec("'test'");
	m !== null && m.length == 3 && m[2] === "test";
	`

	testScript(SCRIPT, valueTrue, t)
}

func TestRegexpInvalidUnicodeControlEscapes(t *testing.T) {
	const SCRIPT = `
	["u", "iu"].forEach(function (flags) {
		["", "0", "9", "_", "%", "й"].forEach(function (c) {
			["\\c" + c, "[\\c" + c + "]"].forEach(function (pattern) {
				try {
					new RegExp(pattern, flags);
				} catch (e) {
					if (e instanceof SyntaxError) return;
					throw e;
				}
				throw new Error("Expected SyntaxError: " + pattern);
			});
		});
	});
	/\cA/u.test("\x01") && /[\cz]/iu.test("\x1a");
	`
	testScript(SCRIPT, valueTrue, t)
}

func TestRegexp2(t *testing.T) {
	const SCRIPT = `
	var r = new RegExp("(['\"])(.*?)['\"]");
	var m = r.exec("'test'");
	m !== null && m.length == 3 && m[2] === "test";
	`

	testScript(SCRIPT, valueTrue, t)
}

func TestRegexpLiteral(t *testing.T) {
	const SCRIPT = `
	var r = /(['\"])(.*?)\1/;
	var m = r.exec("'test'");
	m !== null && m.length == 3 && m[2] === "test";
	`

	testScript(SCRIPT, valueTrue, t)
}

func TestRegexpRe2Unicode(t *testing.T) {
	const SCRIPT = `
	var r = /(тест)/i;
	var m = r.exec("'Тест'");
	m !== null && m.length == 2 && m[1] === "Тест";
	`

	testScript(SCRIPT, valueTrue, t)
}

func TestRegexpRe2UnicodeTarget(t *testing.T) {
	const SCRIPT = `
	var r = /(['\"])(.*?)['\"]/i;
	var m = r.exec("'Тест'");
	m !== null && m.length == 3 && m[2] === "Тест";
	`

	testScript(SCRIPT, valueTrue, t)
}

func TestRegexpRegexp2Unicode(t *testing.T) {
	const SCRIPT = `
	var r = /(['\"])(тест)\1/i;
	var m = r.exec("'Тест'");
	m !== null && m.length == 3 && m[2] === "Тест";
	`

	testScript(SCRIPT, valueTrue, t)
}

func TestRegexpRegexp2UnicodeTarget(t *testing.T) {
	const SCRIPT = `
	var r = /(['\"])(.*?)\1/;
	var m = r.exec("'Тест'");
	m !== null && m.length == 3 && m[2] === "Тест";
	`

	testScript(SCRIPT, valueTrue, t)
}

func TestRegexpRe2Whitespace(t *testing.T) {
	const SCRIPT = `
	"\u2000\u2001\u2002\u200b".replace(/\s+/g, "") === "\u200b";
	`

	testScript(SCRIPT, valueTrue, t)
}

func TestRegexpRegexp2Whitespace(t *testing.T) {
	const SCRIPT = `
	"A\u2000\u2001\u2002A\u200b".replace(/(A)\s+\1/g, "") === "\u200b"
	`
	testScript(SCRIPT, valueTrue, t)
}

func TestEmptyCharClassRe2(t *testing.T) {
	const SCRIPT = `
	/[]/.test("\u0000");
	`

	testScript(SCRIPT, valueFalse, t)
}

func TestNegatedEmptyCharClassRe2(t *testing.T) {
	const SCRIPT = `
	/[^]/.test("\u0000");
	`

	testScript(SCRIPT, valueTrue, t)
}

func TestEmptyCharClassRegexp2(t *testing.T) {
	const SCRIPT = `
	/([])\1/.test("\u0000\u0000");
	`

	testScript(SCRIPT, valueFalse, t)
}

func TestRegexp2Negate(t *testing.T) {
	const SCRIPT = `
	/([\D1])\1/.test("aa");
	`

	testScript(SCRIPT, valueTrue, t)
}

func TestAlternativeRe2(t *testing.T) {
	const SCRIPT = `
	/()|/.exec("") !== null;
	`

	testScript(SCRIPT, valueTrue, t)
}

func TestRegexpReplaceGlobal(t *testing.T) {
	const SCRIPT = `
	"QBZPbage\ny_cynprubyqre".replace(/^\s*|\s*$/g, '')
	`

	testScript(SCRIPT, asciiString("QBZPbage\ny_cynprubyqre"), t)
}

func TestRegexpNumCaptures(t *testing.T) {
	const SCRIPT = `
	"Fubpxjnir Synfu 9.0  e115".replace(/([a-zA-Z]|\s)+/, '')
	`
	testScript(SCRIPT, asciiString("9.0  e115"), t)
}

func TestRegexpNumCaptures1(t *testing.T) {
	const SCRIPT = `
	"Fubpxjnir Sy\tfu 9.0  e115".replace(/^.*\s+(\S+\s+\S+$)/, '')
	`
	testScript(SCRIPT, asciiString(""), t)
}

func TestRegexpSInClass(t *testing.T) {
	const SCRIPT = `
	/[\S]/.test("\u2028");
	`
	testScript(SCRIPT, valueFalse, t)
}

func TestRegexpDashAfterClassEscape(t *testing.T) {
	const SCRIPT = `
	var toCamelCase = function(s) {
		return s.replace(/^([A-Z])|[\s-_]+(\w)/g, function(m, p1, p2) {
			return p2 ? p2.toUpperCase() : p1.toLowerCase();
		});
	};
	toCamelCase("Foo bar-baz_qux");
	`

	testScript(SCRIPT, asciiString("fooBarBazQux"), t)
}

func TestRegexpDashNextToClassEscape(t *testing.T) {
	const SCRIPT = `
	/^[\s-_]$/.test("-") && /^[\s-_]$/.test("_") && /^[\s-_]$/.test(" ") && !/^[\s-_]$/.test("a") &&
	/^[_-\s]$/.test("-") && /^[\s-\d]$/.test("-") && /^[\d-_]$/.test("-") &&
	/^[a-z]$/.test("m") && !/^[a-z]$/.test("-") &&
	/^[\s\-_]$/.test("-") && /^[\s\-_]$/.test(" ") &&
	/^[\b-_]$/.test("Z") && !/^[\b-_]$/.test("a") &&
	/^[\s-_-a]$/.test("a") && !/^[\s-_-a]$/.test("\u0060");
	`

	testScript(SCRIPT, valueTrue, t)
}

func TestRegexpDotMatchCR(t *testing.T) {
	const SCRIPT = `
	/./.test("\r");
	`

	testScript(SCRIPT, valueFalse, t)
}

func TestRegexpDotMatchCRInGroup(t *testing.T) {
	const SCRIPT = `
	/(.)/.test("\r");
	`

	testScript(SCRIPT, valueFalse, t)
}

func TestRegexpDotMatchLF(t *testing.T) {
	const SCRIPT = `
	/./.test("\n");
	`

	testScript(SCRIPT, valueFalse, t)
}

func TestRegexpSplitWithBackRef(t *testing.T) {
	const SCRIPT = `
	"a++b+-c".split(/([+-])\1/).join(" $$ ")
	`

	testScript(SCRIPT, asciiString("a $$ + $$ b+-c"), t)
}

func TestEscapeNonASCII(t *testing.T) {
	const SCRIPT = `
	/\⩓/.test("⩓")
	`

	testScript(SCRIPT, valueTrue, t)
}

func TestRegexpUTF16(t *testing.T) {
	const SCRIPT = `
	var str = "\uD800\uDC00";

	assert(/\uD800/g.test(str), "#1");
	assert(/\uD800/.test(str), "#2");
	assert(/𐀀/.test(str), "#3");

	var re = /\uD800/;

	assert(compareArray(str.replace(re, "X"), ["X", "\uDC00"]), "#4");
	assert(compareArray(str.split(re), ["", "\uDC00"]), "#5");
	assert(compareArray("a\uD800\uDC00b".split(/\uD800/g), ["a", "\uDC00b"]), "#6");
	assert(compareArray("a\uD800\uDC00b".split(/(?:)/g), ["a", "\uD800", "\uDC00", "b"]), "#7");
	assert(compareArray("0\x80".split(/(0){0}/g), ["0", undefined, "\x80"]), "#7+");

	re = /(?=)a/; // a hack to use regexp2
	assert.sameValue(re.exec('\ud83d\ude02a').index, 2, "#8");

	assert.sameValue(/./.exec('\ud83d\ude02')[0], '\ud83d', "#9");

	assert(RegExp("\uD800").test("\uD800"), "#10");

	var cu = 0xD800;
	var xx = "a\\" + String.fromCharCode(cu);
	var pattern = eval("/" + xx + "/");
	assert.sameValue(pattern.source, "a\\\\\\ud800", "Code unit: " + cu.toString(16), "#11");
	assert(pattern.test("a\\\uD800"), "#12");
	`

	testScriptWithTestLib(SCRIPT, _undefined, t)
}

func TestRegexpUnicode(t *testing.T) {
	const SCRIPT = `

	assert(!/\uD800/u.test("\uD800\uDC00"), "#1");
	assert(!/\uFFFD/u.test("\uD800\uDC00"), "#2");

	assert(/\uD800\uDC00/u.test("\uD800\uDC00"), "#3");

	assert(/\uD800/u.test("\uD800"), "#4");

	assert(compareArray("a\uD800\uDC00b".split(/\uD800/gu), ["a\uD800\uDC00b"]), "#5");

	assert(compareArray("a\uD800\uDC00b".split(/(?:)/gu), ["a", "𐀀", "b"]), "#6");

	assert(compareArray("0\x80".split(/(0){0}/gu), ["0", undefined, "\x80"]), "#7");

	var re = eval('/' + /\ud834\udf06/u.source + '/u');
	assert(re.test('\ud834\udf06'), "#9");

	/*re = RegExp("\\p{L}", "u");
	if (!re.test("A")) {
		throw new Error("Test 9 failed");
	}*/
	`

	testScriptWithTestLib(SCRIPT, _undefined, t)
}

func TestConvertRegexpToUnicode(t *testing.T) {
	if s := convertRegexpToUnicode(`test\uD800\u0C00passed`); s != `test\uD800\u0C00passed` {
		t.Fatal(s)
	}
	if s := convertRegexpToUnicode(`test\uD800\uDC00passed`); s != `test𐀀passed` {
		t.Fatal(s)
	}
	if s := convertRegexpToUnicode(`test\u0023passed`); s != `test\u0023passed` {
		t.Fatal(s)
	}
	if s := convertRegexpToUnicode(`test\u0passed`); s != `test\u0passed` {
		t.Fatal(s)
	}
	if s := convertRegexpToUnicode(`test\uD800passed`); s != `test\uD800passed` {
		t.Fatal(s)
	}
	if s := convertRegexpToUnicode(`test\uD800`); s != `test\uD800` {
		t.Fatal(s)
	}
	if s := convertRegexpToUnicode(`test\uD80`); s != `test\uD80` {
		t.Fatal(s)
	}
	if s := convertRegexpToUnicode(`\\uD800\uDC00passed`); s != `\\uD800\uDC00passed` {
		t.Fatal(s)
	}
	if s := convertRegexpToUnicode(`testpassed`); s != `testpassed` {
		t.Fatal(s)
	}
}

func TestConvertRegexpToUtf16(t *testing.T) {
	if s := convertRegexpToUtf16(`𐀀`); s != `\ud800\udc00` {
		t.Fatal(s)
	}
	if s := convertRegexpToUtf16(`\𐀀`); s != `\\\ud800\udc00` {
		t.Fatal(s)
	}
}

func TestEscapeInvalidUtf16(t *testing.T) {
	if s := escapeInvalidUtf16(asciiString("test")); s != "test" {
		t.Fatal(s)
	}
	if s := escapeInvalidUtf16(newStringValue("test\U00010000")); s != "test\U00010000" {
		t.Fatal(s)
	}
	if s := escapeInvalidUtf16(unicodeStringFromRunes([]rune{'t', 0xD800})); s != "t\\ud800" {
		t.Fatal(s)
	}
	if s := escapeInvalidUtf16(unicodeStringFromRunes([]rune{'t', 0xD800, 'p'})); s != "t\\ud800p" {
		t.Fatal(s)
	}
	if s := escapeInvalidUtf16(unicodeStringFromRunes([]rune{0xD800, 'p'})); s != "\\ud800p" {
		t.Fatal(s)
	}
	if s := escapeInvalidUtf16(unicodeStringFromRunes([]rune{'t', '\\', 0xD800, 'p'})); s != `t\\\ud800p` {
		t.Fatal(s)
	}
}

func TestRegexpAssertion(t *testing.T) {
	const SCRIPT = `
	var res = 'aaa'.match(/^a/g);
	res.length === 1 || res[0] === 'a';
	`
	testScript(SCRIPT, valueTrue, t)
}

func TestRegexpUnicodeAdvanceStringIndex(t *testing.T) {
	const SCRIPT = `
	// deoptimise RegExp
	var origExec = RegExp.prototype.exec;
	RegExp.prototype.exec = function(s) {
		return origExec.call(this, s);
	};

	var re = /(?:)/gu;
	var str = "a\uD800\uDC00b";
	assert(compareArray(str.split(re), ["a", "𐀀", "b"]), "#1");

	re.lastIndex = 3;
	assert.sameValue(re.exec(str).index, 3, "#2");

	re.lastIndex = 2;
	assert.sameValue(re.exec(str).index, 1, "#3");

	re.lastIndex = 4;
	assert.sameValue(re.exec(str).index, 4, "#4");

	re.lastIndex = 5;
	assert.sameValue(re.exec(str), null, "#5");

	var iterator = str.matchAll(re); // regexp is copied by matchAll, but resets lastIndex
	var matches = [];
	for (var v of iterator) {matches.push(v);}
	assert.sameValue(matches.length, 4, "#6");
	assert.sameValue(matches[0].index, 0, "#7 index");
	assert.sameValue(matches[0][0], "", "#7 value");
	assert.sameValue(matches[1].index, 1, "#8 index");
	assert.sameValue(matches[1][0], "", "#8 value");
	assert.sameValue(matches[2].index, 3, "#9 index");
	assert.sameValue(matches[2][0], "", "#9 value");
	assert.sameValue(matches[3].index, 4, "#10 index");
	assert.sameValue(matches[3][0], "", "#10 value");
	`
	testScriptWithTestLib(SCRIPT, _undefined, t)
}

func TestRegexpInit(t *testing.T) {
	const SCRIPT = `
	RegExp(".").lastIndex;
	`
	testScript(SCRIPT, intToValue(0), t)
}

func TestRegexpToString(t *testing.T) {
	const SCRIPT = `
	RegExp.prototype.toString.call({
	source: 'foo',
    flags: 'bar'});
	`
	testScript(SCRIPT, asciiString("/foo/bar"), t)
}

func TestRegexpEscapeSource(t *testing.T) {
	const SCRIPT = `
	/href="(.+?)(\/.*\/\S+?)\/"/.source;
	`
	testScript(SCRIPT, asciiString(`href="(.+?)(\/.*\/\S+?)\/"`), t)
}

func TestRegexpConsecutiveMatchCache(t *testing.T) {
	const SCRIPT = `
	(function test(unicode) {
		var regex = new RegExp('t(e)(st(\\d?))', unicode?'gu':'g');
		var string = 'test1test2';
		var match;
		var matches = [];
		while (match = regex.exec(string)) {
			matches.push(match);
		}
		var expectedMatches = [
		  [
			'test1',
			'e',
			'st1',
			'1'
		  ],
		  [
			'test2',
			'e',
			'st2',
			'2'
		  ]
		];
		expectedMatches[0].index = 0;
		expectedMatches[0].input = 'test1test2';
		expectedMatches[0].groups = undefined;
		expectedMatches[1].index = 5;
		expectedMatches[1].input = 'test1test2';
		expectedMatches[1].groups = undefined;

		assert(deepEqual(matches, expectedMatches), "#1");

		// try the same regexp with a different string
		regex.lastIndex = 0;
		match = regex.exec(' test5');
		var expectedMatch = [
		  'test5',
		  'e',
		  'st5',
		  '5'
		];
		expectedMatch.index = 1;
		expectedMatch.input = ' test5';
		expectedMatch.groups = undefined;
		assert(deepEqual(match, expectedMatch), "#2");
		assert.sameValue(regex.lastIndex, 6, "#3");

		// continue matching with a different string
		match = regex.exec(' test5test6');
		expectedMatch = [
		  'test6',
		  'e',
		  'st6',
		  '6'
		];
		expectedMatch.index = 6;
		expectedMatch.input = ' test5test6';
		expectedMatch.groups = undefined;
		assert(deepEqual(match, expectedMatch), "#4");
		assert.sameValue(regex.lastIndex, 11, "#5");

		match = regex.exec(' test5test6');
		assert.sameValue(match, null, "#6");
		return regex;
	});
	`
	vm := New()
	_, _ = vm.RunProgram(testLib())
	_, _ = vm.RunProgram(testLibX())
	v, err := vm.RunString(SCRIPT)
	if err != nil {
		t.Fatal(err)
	}
	var f func(bool) (*Object, error)
	err = vm.ExportTo(v, &f)
	if err != nil {
		t.Fatal(err)
	}

	regex, err := f(false)
	if err != nil {
		t.Fatal(err)
	}
	if regex.self.(*regexpObject).pattern.regexp2Wrapper.cache != nil {
		t.Fatal("Cache is not nil (non-unicode)")
	}

	regex, err = f(true)
	if err != nil {
		t.Fatal(err)
	}
	if regex.self.(*regexpObject).pattern.regexp2Wrapper.cache != nil {
		t.Fatal("Cache is not nil (unicode)")
	}
}

func TestRegexpMatchAll(t *testing.T) {
	const SCRIPT = `
	(function test(unicode) {
		var regex = new RegExp('t(e)(st(\\d?))', unicode?'gu':'g');
		var string = 'test1test2';
		var matches = [];
		for (var match of string.matchAll(regex)) {
			matches.push(match);
		}
		var expectedMatches = [
		  [
			'test1',
			'e',
			'st1',
			'1'
		  ],
		  [
			'test2',
			'e',
			'st2',
			'2'
		  ]
		];
		expectedMatches[0].index = 0;
		expectedMatches[0].input = 'test1test2';
		expectedMatches[0].groups = undefined;
		expectedMatches[1].index = 5;
		expectedMatches[1].input = 'test1test2';
		expectedMatches[1].groups = undefined;

		assert(deepEqual(matches, expectedMatches), "#1");
		assert.sameValue(regex.lastIndex, 0, "#1 lastIndex");

		// try the same regexp with a different string
		string = ' test5';
		matches = [];
		for (var match of string.matchAll(regex)) {
			matches.push(match);
		}
		expectedMatches = [
			[
			  'test5',
			  'e',
			  'st5',
			  '5'
			]
		];
		expectedMatches[0].index = 1;
		expectedMatches[0].input = ' test5';
		expectedMatches[0].groups = undefined;
		assert(deepEqual(matches, expectedMatches), "#2");
		assert.sameValue(regex.lastIndex, 0, "#2 lastIndex");

		// continue matching with a different string
		string = ' test5test6';
		matches = [];
		for (var match of string.matchAll(regex)) {
			matches.push(match);
		}
		var expectedMatches = [
		  [
			'test5',
			'e',
			'st5',
			'5'
		  ],
		  [
			'test6',
			'e',
			'st6',
			'6'
		  ]
		];
		expectedMatches[0].index = 1;
		expectedMatches[0].input = ' test5test6';
		expectedMatches[0].groups = undefined;
		expectedMatches[1].index = 6;
		expectedMatches[1].input = ' test5test6';
		expectedMatches[1].groups = undefined;
		assert(deepEqual(matches, expectedMatches), "#3");
		assert.sameValue(regex.lastIndex, 0, "#3 lastindex");
	});
	`
	vm := New()
	_, _ = vm.RunProgram(testLib())
	_, _ = vm.RunProgram(testLibX())
	v, err := vm.RunString(SCRIPT)
	if err != nil {
		t.Fatal(err)
	}
	var f func(bool) (*Object, error)
	err = vm.ExportTo(v, &f)
	if err != nil {
		t.Fatal(err)
	}

	_, err = f(false)
	if err != nil {
		t.Fatal(err)
	}

	_, err = f(true)
	if err != nil {
		t.Fatal(err)
	}
}

func TestRegexpOverrideSpecies(t *testing.T) {
	const SCRIPT = `
	Object.defineProperty(RegExp, Symbol.species, {
		configurable: true,
		value: function() {
			throw "passed";
		}
	});
	try {
		"ab".split(/a/);
		throw new Error("Expected error");
	} catch(e) {
		if (e !== "passed") {
			throw e;
		}
	}
	`
	testScript(SCRIPT, _undefined, t)
}

func TestRegexpSymbolMatchAllCallsIsRegexp(t *testing.T) {
	// This is tc39's test/built-ins/RegExp/prototype/Symbol.matchAll/isregexp-this-throws.js
	const SCRIPT = `
	var a = new Object();
	Object.defineProperty(a, Symbol.match, {
		get: function() {
			throw "passed";
		}
	});
	try {
		RegExp.prototype[Symbol.matchAll].call(a, '');
		throw new Error("Expected error");
	} catch(e) {
		if (e !== "passed") {
			throw e;
		}
	}
	`
	testScript(SCRIPT, _undefined, t)
}

func TestRegexpMatchAllConstructor(t *testing.T) {
	// This is tc39's test/built-ins/RegExp/prototype/Symbol.matchAll/species-constuctor.js
	const SCRIPT = `
	var callCount = 0;
	var callArgs;
	var regexp = /\d/u;
	var obj = {}
	Object.defineProperty(obj, Symbol.species, {
		value: function() {
		  callCount++;
		  callArgs = arguments;
		  return /\w/g;
		}
	});
	regexp.constructor = obj;
	var str = 'a*b';
	var iter = regexp[Symbol.matchAll](str);

	assert.sameValue(callCount, 1);
	assert.sameValue(callArgs.length, 2);
	assert.sameValue(callArgs[0], regexp);
	assert.sameValue(callArgs[1], 'u');

	var first = iter.next()
	assert.sameValue(first.done, false);
	assert.sameValue(first.value.length, 1);
	assert.sameValue(first.value[0], "a");
	var second = iter.next()
	assert.sameValue(second.done, true);
	`
	testScriptWithTestLib(SCRIPT, _undefined, t)
}

func TestRegexp2InvalidEscape(t *testing.T) {
	testScript(`/(?=)\x0/.test("x0")`, valueTrue, t)
}

func TestRegexpUnicodeEmptyMatch(t *testing.T) {
	testScript(`/(0)0|/gu.exec("0\xef").length === 2`, valueTrue, t)
}

func TestRegexpInvalidGroup(t *testing.T) {
	const SCRIPT = `
	["?", "(?)"].forEach(function(s) {
		assert.throws(SyntaxError, function() {new RegExp(s)}, s);
	});
	`
	testScriptWithTestLib(SCRIPT, _undefined, t)
}

func TestRegexpLookbehindAssertion(t *testing.T) {
	const SCRIPT = `
	var re = /(?<=Jack|Tom)Sprat/;
	assert(re.test("JackSprat"), "#1");
	assert(!re.test("JohnSprat"), "#2");

	re = /(?<!-)\d+/;
	assert(re.test("3"), "#3");
	assert(!re.test("-3"), "#4");
	`
	testScriptWithTestLib(SCRIPT, _undefined, t)
}

func TestRegexpInvalidUTF8(t *testing.T) {
	vm := New()
	// Note that normally vm.ToValue() would replace invalid UTF-8 sequences with RuneError
	_, err := vm.New(vm.Get("RegExp"), asciiString([]byte{0xAD}))
	if err == nil {
		t.Fatal("Expected error")
	}
}

// this should not cause data races when run with -race
func TestRegexpConcurrentLiterals(t *testing.T) {
	prg := MustCompile("test.js", `var r = /(?<!-)\d+/; r.test("");`, false)
	go func() {
		vm := New()
		_, err := vm.RunProgram(prg)
		if err != nil {
			panic(err)
		}
	}()
	vm := New()
	_, _ = vm.RunProgram(prg)
}

func TestRegexpDotAll(t *testing.T) {
	const SCRIPT = `
	var re = /./s;
	re.test("\r") && re.test("\n")
	`
	testScript(SCRIPT, valueTrue, t)
}

func TestRegexpDotAllInGroup(t *testing.T) {
	const SCRIPT = `
	var re = /(.)/s;
	re.test("\r") && re.test("\n")
	`
	testScript(SCRIPT, valueTrue, t)
}

func TestRegexpNumSeparators(t *testing.T) {
	const SCRIPT = `
	const re = /(?<=a)\u{65}_/u;
	assert(re.test("ae_") && !re.test("e_"));

	assert.throws(SyntaxError, () => {
		new RegExp("(?<=a)\\u{6_5}", "u");
	});

	assert.throws(SyntaxError, () => {
		new RegExp("a\\u{6_5}", "u");
	});

	`
	testScriptWithTestLib(SCRIPT, _undefined, t)
}

func TestRegexpUnicodeEscape(t *testing.T) {
	const SCRIPT = `
	assert.sameValue("u{0_2}".match(/\u{0_2}/)[0], "u{0_2}");
	assert.sameValue("uu\x02".match(/\u{2}/u)[0], '\x02');
	assert.sameValue("uu\x02".match(/\u{2}/)[0], "uu");
	`
	testScriptWithTestLib(SCRIPT, _undefined, t)
}

func TestRegexpUnicodeGroupNames(t *testing.T) {
	const SCRIPT = `
	var string = "The quick brown fox jumped over the lazy dog's back";
	let match = string.match(/(?<狸>fox).*(?<狗>dog)/);
	assert.sameValue(match.groups.狸, "fox");
	assert.sameValue(match.groups.狗, "dog");
	`
	testScriptWithTestLib(SCRIPT, _undefined, t)
}

func TestRegExpLastIndex(t *testing.T) {
	const SCRIPT = `
	function t(R, type) {
		const re = new R("x+", "g");
		re.exec("xxy");
		assert.sameValue(re.lastIndex, 2, type+": exec");
		"xxy".replace(re);
		assert.sameValue(re.lastIndex, 0, type+": replace must reset lastIndex");
		re.lastIndex = 1;
		assert.sameValue("xxy".match(re)[0], "xx", type+": match");
		assert.sameValue(re.lastIndex, 0, type+": match must reset lastIndex");
	}

	class CustomRegExp extends RegExp {
		exec(s) {
			return super.exec(s);
		}
	}

	t(RegExp, "std");
	t(CustomRegExp, "generic");
	`
	testScriptWithTestLib(SCRIPT, _undefined, t)
}

func TestRegExpRepeatCount(t *testing.T) {
	const SCRIPT = `
	const re = /^0x[0-9a-fA-F]{0,4096}$/;
	re.test("0xABCD");
	`
	testScript(SCRIPT, valueTrue, t)
}

func BenchmarkRegexpSplitWithBackRef(b *testing.B) {
	const SCRIPT = `
	"aaaaaaaaaaaaaaaaaaaaaaaaa++bbbbbbbbbbbbbbbbbbbbbb+-ccccccccccccccccccccccc".split(/([+-])\1/)
	`
	b.StopTimer()
	prg, err := Compile("test.js", SCRIPT, false)
	if err != nil {
		b.Fatal(err)
	}
	vm := New()
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		vm.RunProgram(prg)
	}
}

func BenchmarkRegexpMatch(b *testing.B) {
	const SCRIPT = `
        "a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
        ".match(/[^\r\n]+/g)
        `
	b.StopTimer()
	prg, err := Compile("test.js", SCRIPT, false)
	if err != nil {
		b.Fatal(err)
	}
	vm := New()
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		vm.RunProgram(prg)
	}
}

func BenchmarkRegexpMatchCache(b *testing.B) {
	const SCRIPT = `
	(function() {
		var s = "a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
        "
		var r = /[^\r\n]+/g
		while(r.exec(s)) {};
	});
	`
	vm := New()
	v, err := vm.RunString(SCRIPT)
	if err != nil {
		b.Fatal(err)
	}
	if fn, ok := AssertFunction(v); ok {
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			fn(_undefined)
		}
	} else {
		b.Fatal("not a function")
	}
}

func BenchmarkRegexpMatchAll(b *testing.B) {
	const SCRIPT = `
	(function() {
		var s = "a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
         a\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\ra\nb\r\c\nd\r\e\n\f\rg\nh\r\
        "
		var r = /[^\r\n]+/g
		for (var v of s.matchAll(r)) {}
	});
	`
	vm := New()
	v, err := vm.RunString(SCRIPT)
	if err != nil {
		b.Fatal(err)
	}
	if fn, ok := AssertFunction(v); ok {
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			fn(_undefined)
		}
	} else {
		b.Fatal("not a function")
	}
}

func BenchmarkRegexpSingleExec(b *testing.B) {
	vm := New()
	regexp := vm.Get("RegExp")
	f := func(reStr, str string, b *testing.B) {
		r, err := vm.New(regexp, vm.ToValue(reStr))
		if err != nil {
			b.Fatal(err)
		}
		exec, ok := AssertFunction(r.Get("exec"))
		if !ok {
			b.Fatal("RegExp.exec is not a function")
		}
		arg := vm.ToValue(str)
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, err := exec(r, arg)
			if err != nil {
				b.Fatal(err)
			}
		}
	}

	b.Run("Re-ASCII", func(b *testing.B) {
		f("test", "aaaaaaaaaaaaaaaaaaaaaaaaa testing", b)
	})

	b.Run("Re2-ASCII", func(b *testing.B) {
		f("(?=)test", "aaaaaaaaaaaaaaaaaaaaaaaaa testing", b)
	})

	b.Run("Re-Unicode", func(b *testing.B) {
		f("test", "aaaaaaaaaaaaaaaaaaaaaaaaa testing 😀", b)
	})

	b.Run("Re2-Unicode", func(b *testing.B) {
		f("(?=)test", "aaaaaaaaaaaaaaaaaaaaaaaaa testing 😀", b)
	})

}

func BenchmarkRegExpCtorRepeated(b *testing.B) {
	resetRegexpCache()
	r := New()
	v, err := r.RunString(`(function() { var n = 0; for (var i = 0; i < 100; i++) { if (new RegExp("a+b", "g").test("aab")) n++; } return n; })`)
	if err != nil {
		b.Fatal(err)
	}
	f, _ := AssertFunction(v)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := f(nil); err != nil {
			b.Fatal(err)
		}
	}
}

func resetRegexpCache() {
	SetRegexpCacheSize(regexpCacheMaxEntries)
}

func regexpCacheLen() int {
	regexpCache.Lock()
	defer regexpCache.Unlock()
	return len(regexpCache.m)
}

func TestRegexpCacheReturnsIndependentClones(t *testing.T) {
	resetRegexpCache()
	p1, err := compileRegexp("a(b)", "g")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := compileRegexp("a(b)", "g")
	if err != nil {
		t.Fatal(err)
	}
	if p1 == p2 {
		t.Fatal("cache must hand out distinct clones, not the template")
	}
	if regexpCacheLen() != 1 {
		t.Fatalf("cache len = %d, want 1", regexpCacheLen())
	}
	// A pattern that requires regexp2 (lookbehind) must share the compiled
	// engine but not the per-clone match cache.
	q1, err := compileRegexp("(?<=a)b", "")
	if err != nil {
		t.Fatal(err)
	}
	q2, err := compileRegexp("(?<=a)b", "")
	if err != nil {
		t.Fatal(err)
	}
	if q1.regexp2Wrapper == nil || q2.regexp2Wrapper == nil {
		t.Fatal("expected regexp2 wrappers")
	}
	if q1.regexp2Wrapper == q2.regexp2Wrapper {
		t.Fatal("regexp2 wrapper must be cloned per caller")
	}
	if q1.regexp2Wrapper.rx != q2.regexp2Wrapper.rx {
		t.Fatal("compiled regexp2 engine must be shared")
	}
	if q1.regexp2Wrapper.cache != nil {
		t.Fatal("clone must start with an empty match cache")
	}
}

func TestRegexpCacheRuntimeStateIndependent(t *testing.T) {
	resetRegexpCache()
	const mk = `var re = new RegExp("a", "g");`
	r1, r2 := New(), New()
	v, err := r1.RunString(mk + `re.test("aa"); re.lastIndex`)
	if err != nil {
		t.Fatal(err)
	}
	if v.ToInteger() != 1 {
		t.Fatalf("r1 lastIndex = %v, want 1", v)
	}
	v, err = r2.RunString(mk + `re.lastIndex`)
	if err != nil {
		t.Fatal(err)
	}
	if v.ToInteger() != 0 {
		t.Fatalf("r2 lastIndex = %v, want 0 (state leaked across runtimes)", v)
	}
	v, err = r1.RunString(`re.test("aa"); re.lastIndex`)
	if err != nil {
		t.Fatal(err)
	}
	if v.ToInteger() != 2 {
		t.Fatalf("r1 second lastIndex = %v, want 2", v)
	}
	// matchAll's per-call species clone is the main runtime compile site.
	v, err = r1.RunString(`var out = []; for (var i = 0; i < 3; i++) { out.push([..."aXa".matchAll(/a/g)].length); } out.join(",")`)
	if err != nil {
		t.Fatal(err)
	}
	if v.String() != "2,2,2" {
		t.Fatalf("matchAll results = %v", v)
	}
}

func TestRegexpCacheCapResets(t *testing.T) {
	resetRegexpCache()
	for i := 0; i < regexpCacheMaxEntries+5; i++ {
		if _, err := compileRegexp("p"+intToValue(int64(i)).String(), ""); err != nil {
			t.Fatal(err)
		}
	}
	if n := regexpCacheLen(); n != 5 {
		t.Fatalf("cache len after overflow = %d, want 5 (map dropped on the 1025th insert, then 5 inserted)", n)
	}
	if _, err := compileRegexp("after-reset", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := compileRegexp("after-reset", ""); err != nil {
		t.Fatal(err)
	}
}

func TestRegexpCacheBytesBound(t *testing.T) {
	// The cache is bounded by the estimated size of its templates too: a
	// pattern with many property escapes holds hundreds of kilobytes.
	resetRegexpCache()
	big := strings.Repeat(`\P{Alphabetic}`, 100)
	p, err := compileRegexp(big, "giu")
	if err != nil {
		t.Fatal(err)
	}
	if p.size < 200<<10 || p.size > 4<<20 {
		t.Fatalf("size %d", p.size)
	}
	regexpCache.Lock()
	bytes := regexpCache.bytes
	regexpCache.Unlock()
	if bytes != p.size {
		t.Fatalf("cache bytes %d, want %d", bytes, p.size)
	}
	n := 0
	for i := 0; int64(i)*p.size <= regexpCacheMaxBytes+p.size; i++ {
		if _, err := compileRegexp(big+strconv.Itoa(i), "giu"); err != nil {
			t.Fatal(err)
		}
		n++
	}
	regexpCache.Lock()
	bytes, entries := regexpCache.bytes, len(regexpCache.m)
	regexpCache.Unlock()
	if bytes > regexpCacheMaxBytes || entries >= n {
		t.Fatalf("after %d patterns: %d bytes, %d entries", n, bytes, entries)
	}
	resetRegexpCache()
}

func TestRegexpCacheBytesCountPrefixSkip(t *testing.T) {
	// The anchored copy a template compiles for its prefix skip counts in
	// the cache's bound, compiled or not: once compiled it is held as long
	// as the template.
	resetRegexpCache()
	defer resetRegexpCache()
	p, err := compileRegexp(`foo\d`, "")
	if err != nil {
		t.Fatal(err)
	}
	if p.prefixSkip == nil || p.prefixSkip.size == 0 {
		t.Fatal("no prefix skip")
	}
	regexpCache.Lock()
	bytes := regexpCache.bytes
	regexpCache.Unlock()
	if bytes != p.size+p.prefixSkip.size {
		t.Errorf("cache bytes %d, want %d + %d", bytes, p.size, p.prefixSkip.size)
	}
}

func TestRegexpCacheSkipsOversizeTemplate(t *testing.T) {
	// A template larger than the cache's byte bound is not cached: it would
	// take the whole cache, and the memory would be nobody's to charge.
	resetRegexpCache()
	defer resetRegexpCache()
	if _, err := compileRegexp("small", ""); err != nil {
		t.Fatal(err)
	}
	// A thousand instructions a copy, as many as make the bound.
	copies := int(regexpCacheMaxBytes/int64(unsafe.Sizeof(syntax.Inst{})))/1000 + 100
	src := "^" + strings.Repeat("(?:a{1000})", copies)
	p, err := compileRegexp(src, "")
	if err != nil {
		t.Fatal(err)
	}
	if p.size <= regexpCacheMaxBytes {
		t.Fatalf("size %d", p.size)
	}
	regexpCache.Lock()
	_, cached := regexpCache.m[regexpCacheKey{src: src}]
	bytes := regexpCache.bytes
	regexpCache.Unlock()
	if cached || regexpCacheLen() != 1 || bytes > regexpCacheMaxBytes {
		t.Errorf("cached %v, %d entries, %d bytes", cached, regexpCacheLen(), bytes)
	}
}

// retainedHeap returns what f's result holds on the heap.
func retainedHeap(f func() any) (any, int64) {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	v := f()
	runtime.GC()
	runtime.ReadMemStats(&after)
	return v, int64(after.HeapAlloc) - int64(before.HeapAlloc)
}

// distinctClasses returns n classes of k ranges each, all different.
func distinctClasses(n, k int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteByte('[')
		base := 0x100 + (i*7919)%0x8000
		for j := 0; j < k; j++ {
			fmt.Fprintf(&b, `\u{%x}-\u{%x}`, base+4*j, base+4*j+1)
		}
		b.WriteByte(']')
	}
	return b.String()
}

// wideClasses returns n classes of one range of span characters each, all
// different.
func wideClasses(n, span int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `[\u{%x}-\u{%x}]`, 0x41+i, 0x41+i+span-1)
	}
	return b.String()
}

func TestRegexpChargeTracksRetainedHeap(t *testing.T) {
	// The memory charged for a compiled pattern, and for the copy anchored
	// at its start, is an estimate made before the engines compile it (see
	// goRegexpSize and regexp2Estimate). It is an upper bound on the heap
	// it holds, and within three times it for these kinds of pattern.
	for _, tc := range []struct{ src, flags string }{
		{strings.Repeat("a", 100000), ""},
		{strings.Repeat("é😀", 20000), "u"},
		{strings.Repeat(`[a-z]\d+`, 20000), "g"},
		{strings.Repeat(`(?=a)b`, 20000), ""},
		{strings.Repeat(`(?<=a)b`, 20000), ""},
		{strings.Repeat(`(?=a)(?!b)(?<=c)(?<!d)`, 10000), ""},
		{"(?=)" + strings.Repeat("abc", 30000), "i"},
		{strings.Repeat(`(a)(b)`, 10000), ""},
		{strings.Repeat(`(a)\1`, 10000), ""},
		{strings.Repeat(`\bfoo\b`, 10000), ""},
		{strings.Repeat(`(?:cat|dog|bird)`, 5000), "g"},
		{strings.Repeat("abc", 30000), "gi"},
		{distinctClasses(2000, 20), "gu"},
		{"x" + strings.Repeat(`\p{L}`, 300) + "c", "u"},
		{strings.Repeat(`[\p{L}\p{N}_]`, 100), "u"},
		{strings.Repeat(`\P{Alphabetic}`, 100), "giu"},
		{strings.Repeat(`[\p{L}\p{M}\s'-]`, 100), "iu"},
		{strings.Repeat(`\p{Lu}`, 100), "iu"},
		// Case folding grows the class's slice to 1,344 runes, of which Go's
		// regexp keeps the two it needs, in a slice of that capacity.
		{strings.Repeat(`[A-\uffef]`, 400), "i"},
		{strings.Repeat(`[a-zA-Z0-9_\u00c0-\u024f]`, 2000), "gi"},
		// regexp2 alone, with classes all different; ignoring case with the
		// u flag, it adds each class's case variants (up to 27 KB).
		{"(?=)" + distinctClasses(200, 300), "u"},
		{"(?=)" + wideClasses(100, 65000), "iu"},
		{"(?=)" + wideClasses(300, 1000), "iu"},
	} {
		// The first compile of a property fills the caches of its sets.
		if _, err := compileRegexpUncached(nil, tc.src, tc.flags); err != nil {
			t.Fatal(err)
		}
		v, retained := retainedHeap(func() any {
			// The template may hold its source, which is counted.
			p, err := compileRegexpUncached(nil, strings.Clone(tc.src), tc.flags)
			if err != nil {
				t.Fatal(err)
			}
			return p
		})
		p := v.(*regexpPattern)
		if retained < 256<<10 {
			t.Fatalf("/%.20s.../%s: retained %d bytes", tc.src, tc.flags, retained)
		}
		t.Logf("/%.20s.../%s: charged %d KB, holds %d KB (%.2f)", tc.src, tc.flags, p.size>>10, retained>>10, float64(p.size)/float64(retained))
		if p.size < retained || p.size > 3*retained {
			t.Errorf("/%.20s.../%s: charged %d bytes, the compiled pattern holds %d", tc.src, tc.flags, p.size, retained)
		}
		if p.prefixSkip != nil {
			vm := New().vm
			re, retained := retainedHeap(func() any { return p.prefixSkip.get(vm) })
			if re.(*regexp.Regexp) == nil {
				t.Fatal("no anchored copy")
			}
			t.Logf("  anchored copy: charged %d KB, holds %d KB (%.2f)", p.prefixSkip.size>>10, retained>>10, float64(p.prefixSkip.size)/float64(retained))
			if n := p.prefixSkip.size; n < retained || n > 3*retained {
				t.Errorf("/%.20s.../%s: charged %d bytes for the anchored copy, which holds %d", tc.src, tc.flags, n, retained)
			}
		}
		runtime.KeepAlive(p)
	}
}

func TestRegexpCacheInvalidNotCached(t *testing.T) {
	resetRegexpCache()
	if _, err := compileRegexp("(", ""); err == nil {
		t.Fatal("expected an error for an unterminated group")
	}
	if _, err := compileRegexp("(", ""); err == nil {
		t.Fatal("error must be reported again, not served from cache")
	}
	if regexpCacheLen() != 0 {
		t.Fatalf("invalid pattern was cached")
	}
	if _, err := compileRegexp("x", "gg"); err == nil {
		t.Fatal("expected an error for duplicate flags")
	}
	if regexpCacheLen() != 0 {
		t.Fatalf("invalid flags were cached")
	}
}

func TestRegexpCacheConcurrent(t *testing.T) {
	resetRegexpCache()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := New()
			for i := 0; i < 100; i++ {
				v, err := r.RunString(`new RegExp("x+y", "i").test("XXY") && new RegExp("(?<=a)b").test("ab")`)
				if err != nil {
					errs <- err
					return
				}
				if !v.ToBoolean() {
					errs <- fmt.Errorf("unexpected false")
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestRegexpCacheHitAllocations(t *testing.T) {
	resetRegexpCache()
	if _, err := compileRegexp("warm", "g"); err != nil {
		t.Fatal(err)
	}
	allocs := testing.AllocsPerRun(200, func() {
		if _, err := compileRegexp("warm", "g"); err != nil {
			t.Fatal(err)
		}
	})
	if allocs > 2 {
		t.Fatalf("cache hit allocated %v times, want <= 2 (the clone)", allocs)
	}
	if _, err := compileRegexp("(?<=a)b", ""); err != nil {
		t.Fatal(err)
	}
	allocs = testing.AllocsPerRun(200, func() {
		if _, err := compileRegexp("(?<=a)b", ""); err != nil {
			t.Fatal(err)
		}
	})
	if allocs > 2 {
		t.Fatalf("regexp2 cache hit allocated %v times, want <= 2 (pattern + wrapper)", allocs)
	}
}

func TestRegexpCacheSizeControl(t *testing.T) {
	defer resetRegexpCache()
	SetRegexpCacheSize(0)
	if _, err := compileRegexp("disabled", ""); err != nil {
		t.Fatal(err)
	}
	if n := regexpCacheLen(); n != 0 {
		t.Fatalf("cache must stay empty when disabled, len = %d", n)
	}
	r := New()
	if v, err := r.RunString(`new RegExp("a+b", "g").test("aab")`); err != nil || !v.ToBoolean() {
		t.Fatalf("regexps must still work with the cache disabled: %v %v", v, err)
	}
	SetRegexpCacheSize(2)
	for _, p := range []string{"s1", "s2", "s3"} {
		if _, err := compileRegexp(p, ""); err != nil {
			t.Fatal(err)
		}
	}
	if n := regexpCacheLen(); n != 1 {
		t.Fatalf("cap 2: expected drop on the 3rd insert leaving 1, got %d", n)
	}
	ResetRegexpCache()
	if n := regexpCacheLen(); n != 0 {
		t.Fatalf("ResetRegexpCache left %d entries", n)
	}
	if _, err := compileRegexp("after", ""); err != nil {
		t.Fatal(err)
	}
	if n := regexpCacheLen(); n != 1 {
		t.Fatalf("cache must accept entries after reset, len = %d", n)
	}
}

func TestRegexpEagerRegexp2ForGlobal(t *testing.T) {
	resetRegexpCache()
	for _, flags := range []string{"g", "y", "gy", "gi", "gu"} {
		p, err := compileRegexp("a+", flags)
		if err != nil {
			t.Fatal(err)
		}
		if p.regexpWrapper == nil {
			t.Fatalf("%q: expected the Go regexp engine", flags)
		}
		if p.regexp2Wrapper == nil {
			t.Fatalf("%q: expected regexp2 to be built eagerly for a global/sticky pattern", flags)
		}
		q, err := compileRegexp("a+", flags)
		if err != nil {
			t.Fatal(err)
		}
		if q.regexp2Wrapper.rx != p.regexp2Wrapper.rx {
			t.Fatalf("%q: clones must share the eagerly built regexp2 engine", flags)
		}
	}
	p, err := compileRegexp("a+", "")
	if err != nil {
		t.Fatal(err)
	}
	if p.regexp2Wrapper != nil {
		t.Fatal("a non-global, non-sticky pattern must not build regexp2 eagerly")
	}
	p, err = compileRegexp("(?<=a)b", "g")
	if err != nil {
		t.Fatal(err)
	}
	if p.regexpWrapper != nil || p.regexp2Wrapper == nil {
		t.Fatal("a pattern Go's regexp rejects must still take the regexp2-only path")
	}
	// Behaviour is unchanged across the matching paths that use lastIndex.
	testScript(`
		var out = [];
		out.push([..."a1b22c333".matchAll(/\d+/g)].map(function(m) { return m.index + ":" + m[0]; }).join(","));
		var re = /a/gy; out.push(re.exec("aab")[0], re.lastIndex, re.exec("aab")[0], re.lastIndex, re.exec("aab"), re.lastIndex);
		out.push([..."ab".matchAll(/(?:)/g)].map(function(m) { return m.index; }).join(","));
		out.push([..."x\u{1F600}y".matchAll(/./gu)].length);
		out.push("aXbXc".replace(/X/g, "-"), "a1b2".split(/\d/).join("|"));
		out.join("|");
	`, asciiString("1:1,3:22,6:333|a|1|a|2||0|0,1,2|3|a-b-c|a|b|"), t)
}

func TestRegexpEagerRegexp2Concurrent(t *testing.T) {
	resetRegexpCache()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := New()
			for i := 0; i < 50; i++ {
				v, err := r.RunString(`
					var re = /a(b)?c/g, s = "ac abc ac", n = 0;
					while (re.exec(s)) n++;
					var m = [...s.matchAll(/ac/g)].length;
					var y = /abc/y; y.lastIndex = 3;
					n + ":" + m + ":" + (y.exec(s) !== null) + ":" + s.replace(/a/g, "x") + ":" + s.split(/ /g).length`)
				if err != nil {
					errs <- err
					return
				}
				if v.String() != "3:2:true:xc xbc xc:3" {
					errs <- fmt.Errorf("unexpected result %s", v)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}
