package goja

import "testing"

// These scripts' results were recorded with Node.js 18.20.3.

func TestRegexpLoneSurrogateMessageNode18(t *testing.T) {
	// The pattern a SyntaxError's message shows has its lone surrogates as
	// the code units themselves, as V8 shows them, not as \uXXXX escapes.
	const script = `
(function () {
	var out = [];
	var cases = [["\ud83d(", ""], ["\ud83d(", "u"], ["\ude00(", ""], ["a\ud83d[", ""], ["😀(", ""], ["\ud83d(", "i"], ["[\ud83d-a]", "u"], ["x{2,1}\udfff", ""], ["\udfff\ud800)", "g"]];
	for (var i = 0; i < cases.length; i++) {
		try { new RegExp(cases[i][0], cases[i][1]); out.push("ok"); } catch (e) { out.push(e.name + ": " + e.message); }
		try { /x/.compile(cases[i][0], cases[i][1]); out.push("ok"); } catch (e) { out.push(e.name + ": " + e.message); }
	}
	return JSON.stringify(out);
})()
`
	testScript(script, newStringValue(`["SyntaxError: Invalid regular expression: /\ud83d(/: Unterminated group","SyntaxError: Invalid regular expression: /\ud83d(/: Unterminated group","SyntaxError: Invalid regular expression: /\ud83d(/: Unterminated group","SyntaxError: Invalid regular expression: /\ud83d(/: Unterminated group","SyntaxError: Invalid regular expression: /\ude00(/: Unterminated group","SyntaxError: Invalid regular expression: /\ude00(/: Unterminated group","SyntaxError: Invalid regular expression: /a\ud83d[/: Unterminated character class","SyntaxError: Invalid regular expression: /a\ud83d[/: Unterminated character class","SyntaxError: Invalid regular expression: /😀(/: Unterminated group","SyntaxError: Invalid regular expression: /😀(/: Unterminated group","SyntaxError: Invalid regular expression: /\ud83d(/: Unterminated group","SyntaxError: Invalid regular expression: /\ud83d(/: Unterminated group","SyntaxError: Invalid regular expression: /[\ud83d-a]/: Range out of order in character class","SyntaxError: Invalid regular expression: /[\ud83d-a]/: Range out of order in character class","SyntaxError: Invalid regular expression: /x{2,1}\udfff/: numbers out of order in {} quantifier","SyntaxError: Invalid regular expression: /x{2,1}\udfff/: numbers out of order in {} quantifier","SyntaxError: Invalid regular expression: /\udfff\ud800)/: Unmatched ')'","SyntaxError: Invalid regular expression: /\udfff\ud800)/: Unmatched ')'"]`), t)
}
