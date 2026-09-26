package goja

import (
	"errors"
	"runtime"
	"strings"
	"testing"
)

// Unicode property escapes (\p{...}, \P{...}) and the u flag's stricter
// syntax. The expected results were recorded with Node.js 18.20.3 (V8 10.2,
// Unicode 15.1); the inputs avoid code points added to Unicode since 15.0, so
// that the results do not depend on the Go toolchain's Unicode version.

type regexpNode18Case struct {
	pattern, flags string
	want           string
}

type regexpNode18Error struct {
	pattern, message string
	annexB           bool
}

// Inputs, as JSON.
const regexpUnicodeNode18Inputs = `["hello","Hello World","\u0391\u0392\u0393 \u03b1\u03b2\u03b3","123 \u0664\u0665","a-b_c","","\u00e9\u0301","x\ud83d\ude00y","\ud835\udc00\ud835\udc1a","\u0130i\u0131I","\u212aK k","\u017fSs","\u00df\u1e9e","\u01c4\u01c5\u01c6","\u0138","\u0345\u03b9\u0399\u1fbe","\u03c2\u03c3\u03a3","\ud800x\udc00","\u0640\u30fb","\u3042\u30a2\u4e00","\u0378\ue000","\t\u00a0\u3000 ","\udb40\udd00"]`

// exec() on each input: "index:match" or null, as JSON.
var regexpUnicodeNode18Cases = []regexpNode18Case{
	{"^\\p{L}+$", "u", `["0:hello",null,null,null,null,null,null,null,"0:\ud835\udc00\ud835\udc1a","0:\u0130i\u0131I",null,"0:\u017fSs","0:\u00df\u1e9e","0:\u01c4\u01c5\u01c6","0:\u0138",null,"0:\u03c2\u03c3\u03a3",null,null,"0:\u3042\u30a2\u4e00",null,null,null]`},
	{"\\p{L}+", "u", `["0:hello","0:Hello","0:\u0391\u0392\u0393",null,"0:a",null,"0:\u00e9","0:x","0:\ud835\udc00\ud835\udc1a","0:\u0130i\u0131I","0:\u212aK","0:\u017fSs","0:\u00df\u1e9e","0:\u01c4\u01c5\u01c6","0:\u0138","1:\u03b9\u0399\u1fbe","0:\u03c2\u03c3\u03a3","1:x","0:\u0640","0:\u3042\u30a2\u4e00",null,null,null]`},
	{"\\P{L}+", "u", `[null,"5: ","3: ","0:123 \u0664\u0665","1:-",null,"1:\u0301","1:\ud83d\ude00",null,null,"2: ",null,null,null,null,"0:\u0345",null,"0:\ud800","1:\u30fb",null,"0:\u0378\ue000","0:\t\u00a0\u3000 ","0:\udb40\udd00"]`},
	{"\\p{Letter}+", "u", `["0:hello","0:Hello","0:\u0391\u0392\u0393",null,"0:a",null,"0:\u00e9","0:x","0:\ud835\udc00\ud835\udc1a","0:\u0130i\u0131I","0:\u212aK","0:\u017fSs","0:\u00df\u1e9e","0:\u01c4\u01c5\u01c6","0:\u0138","1:\u03b9\u0399\u1fbe","0:\u03c2\u03c3\u03a3","1:x","0:\u0640","0:\u3042\u30a2\u4e00",null,null,null]`},
	{"\\p{gc=Lu}", "u", `[null,"0:H","0:\u0391",null,null,null,null,null,"0:\ud835\udc00","0:\u0130","0:\u212a","1:S","1:\u1e9e","0:\u01c4",null,"2:\u0399","2:\u03a3",null,null,null,null,null,null]`},
	{"\\p{General_Category=Lowercase_Letter}+", "u", `["0:hello","1:ello","4:\u03b1\u03b2\u03b3",null,"0:a",null,"0:\u00e9","0:x","2:\ud835\udc1a","1:i\u0131","3:k","0:\u017f","0:\u00df","2:\u01c6","0:\u0138","1:\u03b9","0:\u03c2\u03c3","1:x",null,null,null,null,null]`},
	{"\\p{Lt}", "u", `[null,null,null,null,null,null,null,null,null,null,null,null,null,"1:\u01c5",null,null,null,null,null,null,null,null,null]`},
	{"\\p{LC}+", "u", `["0:hello","0:Hello","0:\u0391\u0392\u0393",null,"0:a",null,"0:\u00e9","0:x","0:\ud835\udc00\ud835\udc1a","0:\u0130i\u0131I","0:\u212aK","0:\u017fSs","0:\u00df\u1e9e","0:\u01c4\u01c5\u01c6","0:\u0138","1:\u03b9\u0399\u1fbe","0:\u03c2\u03c3\u03a3","1:x",null,null,null,null,null]`},
	{"\\p{Nd}+", "u", `[null,null,null,"0:123",null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null]`},
	{"\\p{digit}+", "u", `[null,null,null,"0:123",null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null]`},
	{"\\p{P}", "u", `[null,null,null,null,"1:-",null,null,null,null,null,null,null,null,null,null,null,null,null,"1:\u30fb",null,null,null,null]`},
	{"\\p{Cn}", "u", `[null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,"0:\u0378",null,null]`},
	{"\\p{Co}", "u", `[null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,"1:\ue000",null,null]`},
	{"\\p{Cs}", "u", `[null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,"0:\ud800",null,null,null,null,null]`},
	{"\\p{Script=Greek}+", "u", `[null,null,"0:\u0391\u0392\u0393",null,null,null,null,null,null,null,null,null,null,null,null,"1:\u03b9\u0399\u1fbe","0:\u03c2\u03c3\u03a3",null,null,null,null,null,null]`},
	{"\\p{sc=Grek}+", "u", `[null,null,"0:\u0391\u0392\u0393",null,null,null,null,null,null,null,null,null,null,null,null,"1:\u03b9\u0399\u1fbe","0:\u03c2\u03c3\u03a3",null,null,null,null,null,null]`},
	{"\\p{sc=Latin}+", "u", `["0:hello","0:Hello",null,null,"0:a",null,"0:\u00e9","0:x",null,"0:\u0130i\u0131I","0:\u212aK","0:\u017fSs","0:\u00df\u1e9e","0:\u01c4\u01c5\u01c6","0:\u0138",null,null,"1:x",null,null,null,null,null]`},
	{"\\p{Script=Han}", "u", `[null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,"2:\u4e00",null,null,null]`},
	{"\\p{sc=Hira}", "u", `[null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,"0:\u3042",null,null,null]`},
	{"\\p{sc=Common}+", "u", `[null,"5: ","3: ","0:123 ","1:-",null,null,"1:\ud83d\ude00","0:\ud835\udc00\ud835\udc1a",null,"2: ",null,null,null,null,null,null,null,"0:\u0640\u30fb",null,null,"0:\t\u00a0\u3000 ",null]`},
	{"\\p{sc=Inherited}", "u", `[null,null,null,null,null,null,"1:\u0301",null,null,null,null,null,null,null,null,"0:\u0345",null,null,null,null,null,null,"0:\udb40\udd00"]`},
	{"\\p{sc=Zzzz}", "u", `[null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,"0:\ud800",null,null,"0:\u0378",null,null]`},
	{"\\p{scx=Arab}", "u", `[null,null,null,"4:\u0664",null,null,null,null,null,null,null,null,null,null,null,null,null,null,"0:\u0640",null,null,null,null]`},
	{"\\p{sc=Arab}", "u", `[null,null,null,"4:\u0664",null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null]`},
	{"\\p{scx=Hira}+", "u", `[null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,"1:\u30fb","0:\u3042",null,null,null]`},
	{"\\p{Script_Extensions=Katakana}+", "u", `[null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,"1:\u30fb","1:\u30a2",null,null,null]`},
	{"\\p{scx=Zyyy}", "u", `[null,"5: ","3: ","0:1","1:-",null,null,"1:\ud83d\ude00","0:\ud835\udc00",null,"2: ",null,null,null,null,null,null,null,null,null,null,"0:\t",null]`},
	{"\\p{Any}", "u", `["0:h","0:H","0:\u0391","0:1","0:a",null,"0:\u00e9","0:x","0:\ud835\udc00","0:\u0130","0:\u212a","0:\u017f","0:\u00df","0:\u01c4","0:\u0138","0:\u0345","0:\u03c2","0:\ud800","0:\u0640","0:\u3042","0:\u0378","0:\t","0:\udb40\udd00"]`},
	{"\\P{Any}", "u", `[null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null]`},
	{"[\\P{Any}]", "u", `[null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null]`},
	{"[^\\P{Any}]", "u", `["0:h","0:H","0:\u0391","0:1","0:a",null,"0:\u00e9","0:x","0:\ud835\udc00","0:\u0130","0:\u212a","0:\u017f","0:\u00df","0:\u01c4","0:\u0138","0:\u0345","0:\u03c2","0:\ud800","0:\u0640","0:\u3042","0:\u0378","0:\t","0:\udb40\udd00"]`},
	{"\\p{ASCII}+", "u", `["0:hello","0:Hello World","3: ","0:123 ","0:a-b_c",null,null,"0:x",null,"1:i","1:K k","1:Ss",null,null,null,null,null,"1:x",null,null,null,"0:\t",null]`},
	{"\\p{Assigned}+", "u", `["0:hello","0:Hello World","0:\u0391\u0392\u0393 \u03b1\u03b2\u03b3","0:123 \u0664\u0665","0:a-b_c",null,"0:\u00e9\u0301","0:x\ud83d\ude00y","0:\ud835\udc00\ud835\udc1a","0:\u0130i\u0131I","0:\u212aK k","0:\u017fSs","0:\u00df\u1e9e","0:\u01c4\u01c5\u01c6","0:\u0138","0:\u0345\u03b9\u0399\u1fbe","0:\u03c2\u03c3\u03a3","0:\ud800x\udc00","0:\u0640\u30fb","0:\u3042\u30a2\u4e00","1:\ue000","0:\t\u00a0\u3000 ","0:\udb40\udd00"]`},
	{"\\P{Assigned}", "u", `[null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,"0:\u0378",null,null]`},
	{"\\p{Alphabetic}+", "u", `["0:hello","0:Hello","0:\u0391\u0392\u0393",null,"0:a",null,"0:\u00e9","0:x","0:\ud835\udc00\ud835\udc1a","0:\u0130i\u0131I","0:\u212aK","0:\u017fSs","0:\u00df\u1e9e","0:\u01c4\u01c5\u01c6","0:\u0138","0:\u0345\u03b9\u0399\u1fbe","0:\u03c2\u03c3\u03a3","1:x","0:\u0640","0:\u3042\u30a2\u4e00",null,null,null]`},
	{"\\p{Alpha}+", "u", `["0:hello","0:Hello","0:\u0391\u0392\u0393",null,"0:a",null,"0:\u00e9","0:x","0:\ud835\udc00\ud835\udc1a","0:\u0130i\u0131I","0:\u212aK","0:\u017fSs","0:\u00df\u1e9e","0:\u01c4\u01c5\u01c6","0:\u0138","0:\u0345\u03b9\u0399\u1fbe","0:\u03c2\u03c3\u03a3","1:x","0:\u0640","0:\u3042\u30a2\u4e00",null,null,null]`},
	{"\\p{White_Space}+", "u", `[null,"5: ","3: ","3: ",null,null,null,null,null,null,"2: ",null,null,null,null,null,null,null,null,null,null,"0:\t\u00a0\u3000 ",null]`},
	{"\\p{space}", "u", `[null,"5: ","3: ","3: ",null,null,null,null,null,null,"2: ",null,null,null,null,null,null,null,null,null,null,"0:\t",null]`},
	{"\\p{Emoji}", "u", `[null,null,null,"0:1",null,null,null,"1:\ud83d\ude00",null,null,null,null,null,null,null,null,null,null,null,null,null,null,null]`},
	{"\\p{Emoji_Presentation}", "u", `[null,null,null,null,null,null,null,"1:\ud83d\ude00",null,null,null,null,null,null,null,null,null,null,null,null,null,null,null]`},
	{"\\p{Extended_Pictographic}", "u", `[null,null,null,null,null,null,null,"1:\ud83d\ude00",null,null,null,null,null,null,null,null,null,null,null,null,null,null,null]`},
	{"\\p{Uppercase}+", "u", `[null,"0:H","0:\u0391\u0392\u0393",null,null,null,null,null,"0:\ud835\udc00","0:\u0130","0:\u212aK","1:S","1:\u1e9e","0:\u01c4",null,"2:\u0399","2:\u03a3",null,null,null,null,null,null]`},
	{"\\p{Lowercase}+", "u", `["0:hello","1:ello","4:\u03b1\u03b2\u03b3",null,"0:a",null,"0:\u00e9","0:x","2:\ud835\udc1a","1:i\u0131","3:k","0:\u017f","0:\u00df","2:\u01c6","0:\u0138","0:\u0345\u03b9","0:\u03c2\u03c3","1:x",null,null,null,null,null]`},
	{"\\p{Cased}+", "u", `["0:hello","0:Hello","0:\u0391\u0392\u0393",null,"0:a",null,"0:\u00e9","0:x","0:\ud835\udc00\ud835\udc1a","0:\u0130i\u0131I","0:\u212aK","0:\u017fSs","0:\u00df\u1e9e","0:\u01c4\u01c5\u01c6","0:\u0138","0:\u0345\u03b9\u0399\u1fbe","0:\u03c2\u03c3\u03a3","1:x",null,null,null,null,null]`},
	{"\\p{Case_Ignorable}+", "u", `[null,null,null,null,null,null,"1:\u0301",null,null,null,null,null,null,null,null,"0:\u0345",null,null,"0:\u0640",null,null,null,"0:\udb40\udd00"]`},
	{"\\p{CWL}+", "u", `[null,"0:H","0:\u0391\u0392\u0393",null,null,null,null,null,null,"0:\u0130","0:\u212aK","1:S","1:\u1e9e","0:\u01c4\u01c5",null,"2:\u0399","2:\u03a3",null,null,null,null,null,null]`},
	{"\\p{CWU}+", "u", `["0:hello","1:ello","4:\u03b1\u03b2\u03b3",null,"0:a",null,"0:\u00e9","0:x",null,"1:i\u0131","3:k","0:\u017f","0:\u00df","1:\u01c5\u01c6",null,"0:\u0345\u03b9","0:\u03c2\u03c3","1:x",null,null,null,null,null]`},
	{"\\p{CWCF}+", "u", `[null,"0:H","0:\u0391\u0392\u0393",null,null,null,null,null,null,"0:\u0130","0:\u212aK","0:\u017fS","0:\u00df\u1e9e","0:\u01c4\u01c5",null,"0:\u0345","0:\u03c2",null,null,null,null,null,null]`},
	{"\\p{CWKCF}", "u", `[null,"0:H","0:\u0391",null,null,null,null,null,"0:\ud835\udc00","0:\u0130","0:\u212a","0:\u017f","0:\u00df","0:\u01c4",null,"0:\u0345","0:\u03c2",null,null,null,null,"1:\u00a0","0:\udb40\udd00"]`},
	{"\\p{Math}", "u", `[null,null,null,null,null,null,null,null,"0:\ud835\udc00",null,null,null,null,null,null,null,null,null,null,null,null,null,null]`},
	{"\\p{Hex}+", "u", `["1:e","1:e",null,"0:123","0:a",null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null]`},
	{"\\p{Bidi_M}", "u", `[null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null]`},
	{"\\p{Default_Ignorable_Code_Point}", "u", `[null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,"0:\udb40\udd00"]`},
	{"\\p{Grapheme_Extend}", "u", `[null,null,null,null,null,null,"1:\u0301",null,null,null,null,null,null,null,null,"0:\u0345",null,null,null,null,null,null,"0:\udb40\udd00"]`},
	{"\\p{Gr_Base}+", "u", `["0:hello","0:Hello World","0:\u0391\u0392\u0393 \u03b1\u03b2\u03b3","0:123 \u0664\u0665","0:a-b_c",null,"0:\u00e9","0:x\ud83d\ude00y","0:\ud835\udc00\ud835\udc1a","0:\u0130i\u0131I","0:\u212aK k","0:\u017fSs","0:\u00df\u1e9e","0:\u01c4\u01c5\u01c6","0:\u0138","1:\u03b9\u0399\u1fbe","0:\u03c2\u03c3\u03a3","1:x","0:\u0640\u30fb","0:\u3042\u30a2\u4e00",null,"1:\u00a0\u3000 ",null]`},
	{"\\p{Variation_Selector}", "u", `[null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,"0:\udb40\udd00"]`},
	{"\\p{Dia}", "u", `[null,null,null,null,null,null,"1:\u0301",null,null,null,null,null,null,null,null,"0:\u0345",null,null,null,null,null,null,null]`},
	{"[\\p{L}\\d]+", "u", `["0:hello","0:Hello","0:\u0391\u0392\u0393","0:123","0:a",null,"0:\u00e9","0:x","0:\ud835\udc00\ud835\udc1a","0:\u0130i\u0131I","0:\u212aK","0:\u017fSs","0:\u00df\u1e9e","0:\u01c4\u01c5\u01c6","0:\u0138","1:\u03b9\u0399\u1fbe","0:\u03c2\u03c3\u03a3","1:x","0:\u0640","0:\u3042\u30a2\u4e00",null,null,null]`},
	{"[\\p{Lu}\\p{Nd}]+", "u", `[null,"0:H","0:\u0391\u0392\u0393","0:123",null,null,null,null,"0:\ud835\udc00","0:\u0130","0:\u212aK","1:S","1:\u1e9e","0:\u01c4",null,"2:\u0399","2:\u03a3",null,null,null,null,null,null]`},
	{"[^\\p{L}\\s]+", "u", `[null,null,null,"0:123","1:-",null,"1:\u0301","1:\ud83d\ude00",null,null,null,null,null,null,null,"0:\u0345",null,"0:\ud800","1:\u30fb",null,"0:\u0378\ue000",null,"0:\udb40\udd00"]`},
	{"[\\P{L}\\P{N}]+", "u", `["0:hello","0:Hello World","0:\u0391\u0392\u0393 \u03b1\u03b2\u03b3","0:123 \u0664\u0665","0:a-b_c",null,"0:\u00e9\u0301","0:x\ud83d\ude00y","0:\ud835\udc00\ud835\udc1a","0:\u0130i\u0131I","0:\u212aK k","0:\u017fSs","0:\u00df\u1e9e","0:\u01c4\u01c5\u01c6","0:\u0138","0:\u0345\u03b9\u0399\u1fbe","0:\u03c2\u03c3\u03a3","0:\ud800x\udc00","0:\u0640\u30fb","0:\u3042\u30a2\u4e00","0:\u0378\ue000","0:\t\u00a0\u3000 ","0:\udb40\udd00"]`},
	{"[^\\P{L}\\P{N}]+", "u", `[null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null]`},
	{"[\\p{sc=Greek}a-c]+", "u", `[null,null,"0:\u0391\u0392\u0393",null,"0:a",null,null,null,null,null,null,null,null,null,null,"1:\u03b9\u0399\u1fbe","0:\u03c2\u03c3\u03a3",null,null,null,null,null,null]`},
	{"[-\\p{L}]+", "u", `["0:hello","0:Hello","0:\u0391\u0392\u0393",null,"0:a-b",null,"0:\u00e9","0:x","0:\ud835\udc00\ud835\udc1a","0:\u0130i\u0131I","0:\u212aK","0:\u017fSs","0:\u00df\u1e9e","0:\u01c4\u01c5\u01c6","0:\u0138","1:\u03b9\u0399\u1fbe","0:\u03c2\u03c3\u03a3","1:x","0:\u0640","0:\u3042\u30a2\u4e00",null,null,null]`},
	{"[\\p{L}-]+", "u", `["0:hello","0:Hello","0:\u0391\u0392\u0393",null,"0:a-b",null,"0:\u00e9","0:x","0:\ud835\udc00\ud835\udc1a","0:\u0130i\u0131I","0:\u212aK","0:\u017fSs","0:\u00df\u1e9e","0:\u01c4\u01c5\u01c6","0:\u0138","1:\u03b9\u0399\u1fbe","0:\u03c2\u03c3\u03a3","1:x","0:\u0640","0:\u3042\u30a2\u4e00",null,null,null]`},
	{"\\p{L}(?=\\d)", "u", `[null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null]`},
	{"(?<=\\p{Lu})\\p{Ll}+", "u", `[null,"1:ello",null,null,null,null,null,null,"2:\ud835\udc1a","1:i\u0131",null,"2:s",null,null,null,"3:\u1fbe",null,null,null,null,null,null,null]`},
	{"(\\p{L})\\1", "u", `["2:ll","2:ll",null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null]`},
	{"(?<c>\\p{Lu})\\p{Ll}*\\k<c>", "u", `[null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null]`},
	{"(?:\\p{Lu}\\p{Ll}+)+", "u", `[null,"0:Hello",null,null,null,null,null,null,"0:\ud835\udc00\ud835\udc1a","0:\u0130i\u0131",null,"1:Ss",null,null,null,"2:\u0399\u1fbe",null,null,null,null,null,null,null]`},
	{"\\p{L}{2}", "u", `["0:he","0:He","0:\u0391\u0392",null,null,null,null,null,"0:\ud835\udc00\ud835\udc1a","0:\u0130i","0:\u212aK","0:\u017fS","0:\u00df\u1e9e","0:\u01c4\u01c5",null,"1:\u03b9\u0399","0:\u03c2\u03c3",null,null,"0:\u3042\u30a2",null,null,null]`},
	{"\\p{L}|\\p{N}", "u", `["0:h","0:H","0:\u0391","0:1","0:a",null,"0:\u00e9","0:x","0:\ud835\udc00","0:\u0130","0:\u212a","0:\u017f","0:\u00df","0:\u01c4","0:\u0138","1:\u03b9","0:\u03c2","1:x","0:\u0640","0:\u3042",null,null,null]`},
	{"\\p{L}", "uy", `["0:h","0:H","0:\u0391",null,"0:a",null,"0:\u00e9","0:x","0:\ud835\udc00","0:\u0130","0:\u212a","0:\u017f","0:\u00df","0:\u01c4","0:\u0138",null,"0:\u03c2",null,"0:\u0640","0:\u3042",null,null,null]`},
	{"\\p{Lu}+", "iu", `["0:hello","0:Hello","0:\u0391\u0392\u0393",null,"0:a",null,"0:\u00e9","0:x","0:\ud835\udc00","0:\u0130i","0:\u212aK","0:\u017fSs","0:\u00df\u1e9e","0:\u01c4\u01c5\u01c6",null,"0:\u0345\u03b9\u0399\u1fbe","0:\u03c2\u03c3\u03a3","1:x",null,null,null,null,null]`},
	{"\\P{Lu}+", "iu", `["0:hello","0:Hello World","0:\u0391\u0392\u0393 \u03b1\u03b2\u03b3","0:123 \u0664\u0665","0:a-b_c",null,"0:\u00e9\u0301","0:x\ud83d\ude00y","2:\ud835\udc1a","1:i\u0131I","0:\u212aK k","0:\u017fSs","0:\u00df\u1e9e","0:\u01c4\u01c5\u01c6","0:\u0138","0:\u0345\u03b9\u0399\u1fbe","0:\u03c2\u03c3\u03a3","0:\ud800x\udc00","0:\u0640\u30fb","0:\u3042\u30a2\u4e00","0:\u0378\ue000","0:\t\u00a0\u3000 ","0:\udb40\udd00"]`},
	{"\\p{Ll}+", "iu", `["0:hello","0:Hello","0:\u0391\u0392\u0393",null,"0:a",null,"0:\u00e9","0:x","2:\ud835\udc1a","1:i\u0131I","0:\u212aK","0:\u017fSs","0:\u00df\u1e9e","0:\u01c4\u01c5\u01c6","0:\u0138","0:\u0345\u03b9\u0399\u1fbe","0:\u03c2\u03c3\u03a3","1:x",null,null,null,null,null]`},
	{"\\P{Ll}+", "iu", `["0:hello","0:Hello World","0:\u0391\u0392\u0393 \u03b1\u03b2\u03b3","0:123 \u0664\u0665","0:a-b_c",null,"0:\u00e9\u0301","0:x\ud83d\ude00y","0:\ud835\udc00","0:\u0130i","0:\u212aK k","0:\u017fSs","0:\u00df\u1e9e","0:\u01c4\u01c5\u01c6",null,"0:\u0345\u03b9\u0399\u1fbe","0:\u03c2\u03c3\u03a3","0:\ud800x\udc00","0:\u0640\u30fb","0:\u3042\u30a2\u4e00","0:\u0378\ue000","0:\t\u00a0\u3000 ","0:\udb40\udd00"]`},
	{"[^\\P{Lu}]+", "iu", `[null,null,null,null,null,null,null,null,"0:\ud835\udc00","0:\u0130",null,null,null,null,null,null,null,null,null,null,null,null,null]`},
	{"[^\\p{Lu}]+", "iu", `[null,"5: ","3: ","0:123 \u0664\u0665","1:-",null,"1:\u0301","1:\ud83d\ude00","2:\ud835\udc1a","2:\u0131","2: ",null,null,null,"0:\u0138",null,null,"0:\ud800","0:\u0640\u30fb","0:\u3042\u30a2\u4e00","0:\u0378\ue000","0:\t\u00a0\u3000 ","0:\udb40\udd00"]`},
	{"\\p{Lt}", "iu", `[null,null,null,null,null,null,null,null,null,null,null,null,null,"0:\u01c4",null,null,null,null,null,null,null,null,null]`},
	{"\\p{LC}+", "iu", `["0:hello","0:Hello","0:\u0391\u0392\u0393",null,"0:a",null,"0:\u00e9","0:x","0:\ud835\udc00\ud835\udc1a","0:\u0130i\u0131I","0:\u212aK","0:\u017fSs","0:\u00df\u1e9e","0:\u01c4\u01c5\u01c6","0:\u0138","0:\u0345\u03b9\u0399\u1fbe","0:\u03c2\u03c3\u03a3","1:x",null,null,null,null,null]`},
	{"\\p{ASCII}+", "iu", `["0:hello","0:Hello World","3: ","0:123 ","0:a-b_c",null,null,"0:x",null,"1:i","0:\u212aK k","0:\u017fSs",null,null,null,null,null,"1:x",null,null,null,"0:\t",null]`},
	{"\\P{ASCII}+", "iu", `[null,null,"0:\u0391\u0392\u0393","4:\u0664\u0665",null,null,"0:\u00e9\u0301","1:\ud83d\ude00","0:\ud835\udc00\ud835\udc1a","0:\u0130","0:\u212aK","0:\u017fSs","0:\u00df\u1e9e","0:\u01c4\u01c5\u01c6","0:\u0138","0:\u0345\u03b9\u0399\u1fbe","0:\u03c2\u03c3\u03a3","0:\ud800","0:\u0640\u30fb","0:\u3042\u30a2\u4e00","0:\u0378\ue000","1:\u00a0\u3000","0:\udb40\udd00"]`},
	{"\\p{sc=Greek}+", "iu", `[null,null,"0:\u0391\u0392\u0393",null,null,null,null,null,null,null,null,null,null,null,null,"0:\u0345\u03b9\u0399\u1fbe","0:\u03c2\u03c3\u03a3",null,null,null,null,null,null]`},
	{"\\p{Uppercase}+", "iu", `["0:hello","0:Hello","0:\u0391\u0392\u0393",null,"0:a",null,"0:\u00e9","0:x","0:\ud835\udc00","0:\u0130i","0:\u212aK","0:\u017fSs","0:\u00df\u1e9e","0:\u01c4\u01c5\u01c6",null,"0:\u0345\u03b9\u0399\u1fbe","0:\u03c2\u03c3\u03a3","1:x",null,null,null,null,null]`},
	{"\\P{Lowercase}+", "iu", `["0:hello","0:Hello World","0:\u0391\u0392\u0393 \u03b1\u03b2\u03b3","0:123 \u0664\u0665","0:a-b_c",null,"0:\u00e9\u0301","0:x\ud83d\ude00y","0:\ud835\udc00","0:\u0130i","0:\u212aK k","0:\u017fSs","0:\u00df\u1e9e","0:\u01c4\u01c5\u01c6",null,"0:\u0345\u03b9\u0399\u1fbe","0:\u03c2\u03c3\u03a3","0:\ud800x\udc00","0:\u0640\u30fb","0:\u3042\u30a2\u4e00","0:\u0378\ue000","0:\t\u00a0\u3000 ","0:\udb40\udd00"]`},
	{"\\p{CWL}+", "iu", `["0:hello","0:Hello","0:\u0391\u0392\u0393",null,"0:a",null,"0:\u00e9","0:x",null,"0:\u0130i","0:\u212aK","0:\u017fSs","0:\u00df\u1e9e","0:\u01c4\u01c5\u01c6",null,"0:\u0345\u03b9\u0399\u1fbe","0:\u03c2\u03c3\u03a3","1:x",null,null,null,null,null]`},
	{"\\P{CWL}+", "iu", `["0:hello","0:Hello World","0:\u0391\u0392\u0393 \u03b1\u03b2\u03b3","0:123 \u0664\u0665","0:a-b_c",null,"0:\u00e9\u0301","0:x\ud83d\ude00y","0:\ud835\udc00\ud835\udc1a","1:i\u0131I","0:\u212aK k","0:\u017fSs","0:\u00df\u1e9e","0:\u01c4\u01c5\u01c6","0:\u0138","0:\u0345\u03b9\u0399\u1fbe","0:\u03c2\u03c3\u03a3","0:\ud800x\udc00","0:\u0640\u30fb","0:\u3042\u30a2\u4e00","0:\u0378\ue000","0:\t\u00a0\u3000 ","0:\udb40\udd00"]`},
	{"[\\p{Lu}\\d]+", "iu", `["0:hello","0:Hello","0:\u0391\u0392\u0393","0:123","0:a",null,"0:\u00e9","0:x","0:\ud835\udc00","0:\u0130i","0:\u212aK","0:\u017fSs","0:\u00df\u1e9e","0:\u01c4\u01c5\u01c6",null,"0:\u0345\u03b9\u0399\u1fbe","0:\u03c2\u03c3\u03a3","1:x",null,null,null,null,null]`},
	{"[^\\p{Lu}\\d]+", "iu", `[null,"5: ","3: ","3: \u0664\u0665","1:-",null,"1:\u0301","1:\ud83d\ude00","2:\ud835\udc1a","2:\u0131","2: ",null,null,null,"0:\u0138",null,null,"0:\ud800","0:\u0640\u30fb","0:\u3042\u30a2\u4e00","0:\u0378\ue000","0:\t\u00a0\u3000 ","0:\udb40\udd00"]`},
	{"[^\\P{Ll}x]+", "iu", `[null,null,null,null,null,null,null,null,"2:\ud835\udc1a","2:\u0131",null,null,null,null,"0:\u0138",null,null,null,null,null,null,null,null]`},
	{"\\p{Lu}(?=\\p{Ll})", "iu", `["0:h","0:H","0:\u0391",null,null,null,null,null,"0:\ud835\udc00","0:\u0130","0:\u212a","0:\u017f","0:\u00df","0:\u01c4",null,"0:\u0345","0:\u03c2",null,null,null,null,null,null]`},
	{"[\\P{L}\\P{N}]+", "iu", `["0:hello","0:Hello World","0:\u0391\u0392\u0393 \u03b1\u03b2\u03b3","0:123 \u0664\u0665","0:a-b_c",null,"0:\u00e9\u0301","0:x\ud83d\ude00y","0:\ud835\udc00\ud835\udc1a","0:\u0130i\u0131I","0:\u212aK k","0:\u017fSs","0:\u00df\u1e9e","0:\u01c4\u01c5\u01c6","0:\u0138","0:\u0345\u03b9\u0399\u1fbe","0:\u03c2\u03c3\u03a3","0:\ud800x\udc00","0:\u0640\u30fb","0:\u3042\u30a2\u4e00","0:\u0378\ue000","0:\t\u00a0\u3000 ","0:\udb40\udd00"]`},
	{"[^\\P{Lu}\\P{Nd}]+", "iu", `[null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null]`},
	{"(?:\\P{L}|\\P{N})+", "u", `["0:hello","0:Hello World","0:\u0391\u0392\u0393 \u03b1\u03b2\u03b3","0:123 \u0664\u0665","0:a-b_c",null,"0:\u00e9\u0301","0:x\ud83d\ude00y","0:\ud835\udc00\ud835\udc1a","0:\u0130i\u0131I","0:\u212aK k","0:\u017fSs","0:\u00df\u1e9e","0:\u01c4\u01c5\u01c6","0:\u0138","0:\u0345\u03b9\u0399\u1fbe","0:\u03c2\u03c3\u03a3","0:\ud800x\udc00","0:\u0640\u30fb","0:\u3042\u30a2\u4e00","0:\u0378\ue000","0:\t\u00a0\u3000 ","0:\udb40\udd00"]`},
	{"[\\P{LC}x]+", "iu", `[null,"5: ","3: ","0:123 \u0664\u0665","1:-",null,"1:\u0301","0:x\ud83d\ude00",null,null,"2: ",null,null,null,null,"0:\u0345\u03b9\u0399\u1fbe",null,"0:\ud800x\udc00","0:\u0640\u30fb","0:\u3042\u30a2\u4e00","0:\u0378\ue000","0:\t\u00a0\u3000 ","0:\udb40\udd00"]`},
	{"\\P{LC}+", "iu", `[null,"5: ","3: ","0:123 \u0664\u0665","1:-",null,"1:\u0301","1:\ud83d\ude00",null,null,"2: ",null,null,null,null,"0:\u0345\u03b9\u0399\u1fbe",null,"0:\ud800","0:\u0640\u30fb","0:\u3042\u30a2\u4e00","0:\u0378\ue000","0:\t\u00a0\u3000 ","0:\udb40\udd00"]`},
	{"[\\p{Alpha}\\P{N}]+", "iu", `["0:hello","0:Hello World","0:\u0391\u0392\u0393 \u03b1\u03b2\u03b3","3: ","0:a-b_c",null,"0:\u00e9\u0301","0:x\ud83d\ude00y","0:\ud835\udc00\ud835\udc1a","0:\u0130i\u0131I","0:\u212aK k","0:\u017fSs","0:\u00df\u1e9e","0:\u01c4\u01c5\u01c6","0:\u0138","0:\u0345\u03b9\u0399\u1fbe","0:\u03c2\u03c3\u03a3","0:\ud800x\udc00","0:\u0640\u30fb","0:\u3042\u30a2\u4e00","0:\u0378\ue000","0:\t\u00a0\u3000 ","0:\udb40\udd00"]`},
	{"[^\\p{AHex}\\p{sc=Grek}]+", "iu", `["0:h","0:H","3: ","3: \u0664\u0665","1:-",null,"0:\u00e9\u0301","0:x\ud83d\ude00y","0:\ud835\udc00\ud835\udc1a","0:\u0130i\u0131I","0:\u212aK k","0:\u017fSs","0:\u00df\u1e9e","0:\u01c4\u01c5\u01c6","0:\u0138",null,null,"0:\ud800x\udc00","0:\u0640\u30fb","0:\u3042\u30a2\u4e00","0:\u0378\ue000","0:\t\u00a0\u3000 ","0:\udb40\udd00"]`},
	{"[^\\P{sc=Grek}\\p{Nd}]+", "u", `[null,null,"0:\u0391\u0392\u0393",null,null,null,null,null,null,null,null,null,null,null,null,"1:\u03b9\u0399\u1fbe","0:\u03c2\u03c3\u03a3",null,null,null,null,null,null]`},
	{"[\\P{scx=Arab}]", "u", `["0:h","0:H","0:\u0391","0:1","0:a",null,"0:\u00e9","0:x","0:\ud835\udc00","0:\u0130","0:\u212a","0:\u017f","0:\u00df","0:\u01c4","0:\u0138","0:\u0345","0:\u03c2","0:\ud800","1:\u30fb","0:\u3042","0:\u0378","0:\t","0:\udb40\udd00"]`},
	{"\\P{Alpha}+", "iu", `[null,"5: ","3: ","0:123 \u0664\u0665","1:-",null,"1:\u0301","1:\ud83d\ude00",null,null,"2: ",null,null,null,null,null,null,"0:\ud800","1:\u30fb",null,"0:\u0378\ue000","0:\t\u00a0\u3000 ","0:\udb40\udd00"]`},
	{"[\\p{Lt}\\P{Cased}]+", "iu", `[null,"5: ","3: ","0:123 \u0664\u0665","1:-",null,"1:\u0301","1:\ud83d\ude00",null,null,"2: ",null,null,"0:\u01c4\u01c5\u01c6",null,null,null,"0:\ud800","0:\u0640\u30fb","0:\u3042\u30a2\u4e00","0:\u0378\ue000","0:\t\u00a0\u3000 ","0:\udb40\udd00"]`},
	{"\\p{Lu}", "", `[null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null]`},
	{"\\p{L}", "", `[null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null]`},
	{"\\P", "", `[null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null]`},
	{"[\\p{L}]+", "", `[null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null]`},
	{"\\u{0000000041}", "u", `[null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null]`},
	{"[\\-]", "u", `[null,null,null,null,"1:-",null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null]`},
	{"[\\b]", "u", `[null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null]`},
	{"\\/", "u", `[null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null]`},
	{"(?<a>.)\\k<a>", "u", `["2:ll","2:ll",null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null]`},
	{"(a)\\1", "u", `[null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null]`},
	{"\\0", "u", `[null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null]`},
	{"[\\0]", "u", `[null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null]`},
	{"a{1}?", "u", `[null,null,null,null,"0:a",null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null]`},
	{"(?:)", "u", `["0:","0:","0:","0:","0:","0:","0:","0:","0:","0:","0:","0:","0:","0:","0:","0:","0:","0:","0:","0:","0:","0:","0:"]`},
	{"[\\u{41}-\\u{5a}]+", "u", `[null,"0:H",null,null,null,null,null,null,null,"3:I","1:K","1:S",null,null,null,null,null,null,null,null,null,null,null]`},
	{"\\u{1F600}", "u", `[null,null,null,null,null,null,null,"1:\ud83d\ude00",null,null,null,null,null,null,null,null,null,null,null,null,null,null,null]`},
	{"[\\d-]", "u", `[null,null,null,"0:1","1:-",null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,null]`},
	{"[-\\w]", "u", `["0:h","0:H",null,"0:1","0:a",null,null,"0:x",null,"1:i","1:K","1:S",null,null,null,null,null,"1:x",null,null,null,null,null]`},
}

// With the u flag; annexB tells whether the pattern is valid without it.
var regexpUnicodeNode18SyntaxErrors = []regexpNode18Error{
	{"\\p", "Invalid regular expression: /\\p/: Invalid property name", true},
	{"\\p{", "Invalid regular expression: /\\p{/: Invalid property name", true},
	{"\\p{L", "Invalid regular expression: /\\p{L/: Invalid property name", true},
	{"\\p{}", "Invalid regular expression: /\\p{}/: Invalid property name", true},
	{"\\pL", "Invalid regular expression: /\\pL/: Invalid property name", true},
	{"\\p{ L}", "Invalid regular expression: /\\p{ L}/: Invalid property name", true},
	{"\\p{letter}", "Invalid regular expression: /\\p{letter}/: Invalid property name", true},
	{"\\p{Script=greek}", "Invalid regular expression: /\\p{Script=greek}/: Invalid property name", true},
	{"\\p{General_Category}", "Invalid regular expression: /\\p{General_Category}/: Invalid property name", true},
	{"\\p{Script}", "Invalid regular expression: /\\p{Script}/: Invalid property name", true},
	{"\\p{ASCII=Yes}", "Invalid regular expression: /\\p{ASCII=Yes}/: Invalid property name", true},
	{"\\p{Foo}", "Invalid regular expression: /\\p{Foo}/: Invalid property name", true},
	{"\\p{Hyphen}", "Invalid regular expression: /\\p{Hyphen}/: Invalid property name", true},
	{"\\p{Other_Alphabetic}", "Invalid regular expression: /\\p{Other_Alphabetic}/: Invalid property name", true},
	{"\\p{sc=Hrkt}", "Invalid regular expression: /\\p{sc=Hrkt}/: Invalid property name", true},
	{"\\p{sc=Garay}", "Invalid regular expression: /\\p{sc=Garay}/: Invalid property name", true},
	{"\\p{gc=}", "Invalid regular expression: /\\p{gc=}/: Invalid property name", true},
	{"\\p{^L}", "Invalid regular expression: /\\p{^L}/: Invalid property name", true},
	{"\\p{IsLatin}", "Invalid regular expression: /\\p{IsLatin}/: Invalid property name", true},
	{"\\p{Block=Basic_Latin}", "Invalid regular expression: /\\p{Block=Basic_Latin}/: Invalid property name", true},
	{"\\p{RGI_Emoji}", "Invalid regular expression: /\\p{RGI_Emoji}/: Invalid property name", true},
	{"\\p{Basic_Emoji}", "Invalid regular expression: /\\p{Basic_Emoji}/: Invalid property name", true},
	{"[\\p{Foo}]", "Invalid regular expression: /[\\p{Foo}]/: Invalid property name in character class", true},
	{"[\\pL]", "Invalid regular expression: /[\\pL]/: Invalid property name in character class", true},
	{"[\\p{L}-z]", "Invalid regular expression: /[\\p{L}-z]/: Invalid character class", false},
	{"[a-\\p{L}]", "Invalid regular expression: /[a-\\p{L}]/: Invalid character class", true},
	{"[\\d-a]", "Invalid regular expression: /[\\d-a]/: Invalid character class", true},
	{"[\\p{L}--]", "Invalid regular expression: /[\\p{L}--]/: Invalid character class", false},
	{"(?=\\p{Foo})", "Invalid regular expression: /(?=\\p{Foo})/: Invalid property name", true},
	{"(?!a)\\q", "Invalid regular expression: /(?!a)\\q/: Invalid escape", true},
	{"(\\w)\\1\\p{letter}", "Invalid regular expression: /(\\w)\\1\\p{letter}/: Invalid property name", true},
	{"\\q", "Invalid regular expression: /\\q/: Invalid escape", true},
	{"\\a", "Invalid regular expression: /\\a/: Invalid escape", true},
	{"\\-", "Invalid regular expression: /\\-/: Invalid escape", true},
	{"\\_", "Invalid regular expression: /\\_/: Invalid escape", true},
	{"\\ ", "Invalid regular expression: /\\ /: Invalid escape", true},
	{"\\\u00e9", "Invalid regular expression: /\\\u00e9/: Invalid escape", true},
	{"[\\q]", "Invalid regular expression: /[\\q]/: Invalid escape", true},
	{"[\\B]", "Invalid regular expression: /[\\B]/: Invalid escape", true},
	{"[\\k]", "Invalid regular expression: /[\\k]/: Invalid escape", true},
	{"\\k", "Invalid regular expression: /\\k/: Invalid named reference", true},
	{"\\k<a>", "Invalid regular expression: /\\k<a>/: Invalid named capture referenced", true},
	{"(?<a>x)\\k<b>", "Invalid regular expression: /(?<a>x)\\k<b>/: Invalid named capture referenced", false},
	{"\\k<a", "Invalid regular expression: /\\k<a/: Invalid capture group name", true},
	{"\\1", "Invalid regular expression: /\\1/: Invalid escape", true},
	{"(a)\\2", "Invalid regular expression: /(a)\\2/: Invalid escape", true},
	{"\\00", "Invalid regular expression: /\\00/: Invalid decimal escape", true},
	{"[\\00]", "Invalid regular expression: /[\\00]/: Invalid class escape", true},
	{"[\\1]", "Invalid regular expression: /[\\1]/: Invalid class escape", true},
	{"[\\8]", "Invalid regular expression: /[\\8]/: Invalid escape", true},
	{"\\c", "Invalid regular expression: /\\c/: Invalid Unicode escape", true},
	{"\\c1", "Invalid regular expression: /\\c1/: Invalid Unicode escape", true},
	{"[\\c_]", "Invalid regular expression: /[\\c_]/: Invalid Unicode escape", true},
	{"\\x1", "Invalid regular expression: /\\x1/: Invalid escape", true},
	{"\\xg", "Invalid regular expression: /\\xg/: Invalid escape", true},
	{"\\u12", "Invalid regular expression: /\\u12/: Invalid Unicode escape", true},
	{"\\u{}", "Invalid regular expression: /\\u{}/: Invalid Unicode escape", true},
	{"\\u{110000}", "Invalid regular expression: /\\u{110000}/: Invalid Unicode escape", true},
	{"\\u{41", "Invalid regular expression: /\\u{41/: Invalid Unicode escape", true},
	{"a{", "Invalid regular expression: /a{/: Incomplete quantifier", true},
	{"a{1", "Invalid regular expression: /a{1/: Incomplete quantifier", true},
	{"a{1,", "Invalid regular expression: /a{1,/: Incomplete quantifier", true},
	{"a{,1}", "Invalid regular expression: /a{,1}/: Incomplete quantifier", true},
	{"x{}", "Invalid regular expression: /x{}/: Incomplete quantifier", true},
	{"{", "Invalid regular expression: /{/: Lone quantifier brackets", true},
	{"}", "Invalid regular expression: /}/: Lone quantifier brackets", true},
	{"]", "Invalid regular expression: /]/: Lone quantifier brackets", true},
	{"a]", "Invalid regular expression: /a]/: Lone quantifier brackets", true},
	{"{1}", "Invalid regular expression: /{1}/: Nothing to repeat", false},
	{"a{2,1}", "Invalid regular expression: /a{2,1}/: numbers out of order in {} quantifier", false},
	{"a**", "Invalid regular expression: /a**/: Nothing to repeat", false},
	{"*a", "Invalid regular expression: /*a/: Nothing to repeat", false},
	{"(?=a)*", "Invalid regular expression: /(?=a)*/: Invalid quantifier", true},
	{"(?<=a)+", "Invalid regular expression: /(?<=a)+/: Invalid quantifier", false},
	{"(?=a){", "Invalid regular expression: /(?=a){/: Incomplete quantifier", true},
	{"\\b*", "Invalid regular expression: /\\b*/: Nothing to repeat", false},
	{"^*", "Invalid regular expression: /^*/: Nothing to repeat", false},
	{"a*{", "Invalid regular expression: /a*{/: Lone quantifier brackets", true},
	{"(", "Invalid regular expression: /(/: Unterminated group", false},
	{")", "Invalid regular expression: /)/: Unmatched ')'", false},
	{"(?", "Invalid regular expression: /(?/: Invalid group", false},
	{"(?i:a)", "Invalid regular expression: /(?i:a)/: Invalid group", false},
	{"(?<a", "Invalid regular expression: /(?<a/: Invalid capture group name", false},
	{"(?<1>x)", "Invalid regular expression: /(?<1>x)/: Invalid capture group name", false},
	{"(?<a>x)(?<a>y)", "Invalid regular expression: /(?<a>x)(?<a>y)/: Duplicate capture group name", false},
	{"[", "Invalid regular expression: /[/: Unterminated character class", false},
	{"[z-a]", "Invalid regular expression: /[z-a]/: Range out of order in character class", false},
	{"\\", "Invalid regular expression: /\\/: \\ at end of pattern", false},
}

// runRegexpNode18Cases runs exec() for every case on every input, with the
// pattern wrapped by wrap, and reports the results that differ from Node's.
func runRegexpNode18Cases(t *testing.T, wrap string) {
	t.Helper()
	vm := New()
	vm.Set("inputs", regexpUnicodeNode18Inputs)
	vm.Set("wrap", wrap)
	check, err := vm.RunString(`
	inputs = JSON.parse(inputs);
	(function(pattern, flags, want) {
		var re = new RegExp(wrap.replace("%s", pattern), flags);
		want = JSON.parse(want);
		var bad = [];
		inputs.forEach(function(s, i) {
			re.lastIndex = 0;
			var m = re.exec(s);
			var got = m === null ? null : m.index + ":" + m[0];
			if (got !== want[i]) {
				bad.push(JSON.stringify(s) + ": " + JSON.stringify(got) + ", want " + JSON.stringify(want[i]));
			}
		});
		return bad.join("; ");
	})`)
	if err != nil {
		t.Fatal(err)
	}
	fn, _ := AssertFunction(check)
	for _, c := range regexpUnicodeNode18Cases {
		res, err := fn(nil, vm.ToValue(c.pattern), vm.ToValue(c.flags), vm.ToValue(c.want))
		if err != nil {
			t.Errorf("/%s/%s: %v", c.pattern, c.flags, err)
			continue
		}
		if s := res.String(); s != "" {
			t.Errorf("/%s/%s: %s", c.pattern, c.flags, s)
		}
	}
}

func TestRegexpUnicodePropertiesMatchNode18(t *testing.T) {
	// Patterns Go's regexp can run are run by it, the others by regexp2.
	runRegexpNode18Cases(t, "%s")
}

func TestRegexpUnicodePropertiesMatchNode18Regexp2(t *testing.T) {
	// An empty lookahead, which Go's regexp does not support, makes regexp2
	// run every pattern.
	runRegexpNode18Cases(t, "(?=)(?:%s)")
}

func TestRegexpUnicodePropertiesEngines(t *testing.T) {
	// The table must exercise Go's regexp: every pattern without a construct
	// it lacks is compiled for it.
	for _, c := range regexpUnicodeNode18Cases {
		p, err := compileRegexp(c.pattern, c.flags)
		if err != nil {
			t.Fatalf("/%s/%s: %v", c.pattern, c.flags, err)
		}
		needsRegexp2 := strings.Contains(c.pattern, "(?=") || strings.Contains(c.pattern, "(?<=") ||
			strings.Contains(c.pattern, `\1`) || strings.Contains(c.pattern, `\k`)
		if (p.regexpWrapper == nil) != needsRegexp2 {
			t.Errorf("/%s/%s: Go regexp compiled: %v", c.pattern, c.flags, p.regexpWrapper != nil)
		}
	}
	p, err := compileRegexp(`(?=)(?:\p{L})`, "u")
	if err != nil || p.regexpWrapper != nil {
		t.Fatalf("lookahead compiled for Go's regexp: %v", err)
	}
}

func TestRegexpUnicodeSyntaxErrorsMatchNode18(t *testing.T) {
	vm := New()
	for _, c := range regexpUnicodeNode18SyntaxErrors {
		for _, src := range []string{"new RegExp(p, 'u')", "eval('/' + p + '/u')"} {
			vm.Set("p", c.pattern)
			_, err := vm.RunString(src)
			var ex *Exception
			if !errors.As(err, &ex) {
				t.Errorf("%s with /%s/: err %v", src, c.pattern, err)
				continue
			}
			v := ex.Value().(*Object)
			if name := v.Get("name").String(); name != "SyntaxError" {
				t.Errorf("%s with /%s/: %s", src, c.pattern, v)
			}
			if msg := v.Get("message").String(); src[0] == 'n' && msg != c.message {
				t.Errorf("/%s/u: message %q, want %q", c.pattern, msg, c.message)
			}
		}
		// Annex B: without the u flag, the patterns Node accepts are still
		// accepted. ([\B] was rejected before; that is a separate matter.)
		if c.annexB && c.pattern != `[\B]` {
			if _, err := vm.RunString("new RegExp(p)"); err != nil {
				t.Errorf("/%s/: %v", c.pattern, err)
			}
		}
	}
}

func TestRegexpUnicodeIdentifierProperties(t *testing.T) {
	// Recorded with Node.js 18, on code points whose properties have not
	// changed since Unicode 6. U+037A, U+0E33, U+309B, U+FC5E, U+FE70 and
	// U+FF9E are identifier characters that NFKC normalization turns into
	// something else.
	const script = `
	var cps = [0x24, 0x30, 0x41, 0x5f, 0xb7, 0x37a, 0x387, 0xe33, 0x1369, 0x2118, 0x212e, 0x309b, 0x2e2f, 0xfc5e, 0xfe70, 0xff9e, 0x1885, 0x19da];
	var want = {
		ID_Start: ["41", "37a", "e33", "2118", "212e", "309b", "fc5e", "fe70", "ff9e", "1885"],
		ID_Continue: ["30", "41", "5f", "b7", "37a", "387", "e33", "1369", "2118", "212e", "309b", "fc5e", "fe70", "ff9e", "1885", "19da"],
		XID_Start: ["41", "2118", "212e", "1885"],
		XID_Continue: ["30", "41", "5f", "b7", "387", "e33", "1369", "2118", "212e", "ff9e", "1885", "19da"]
	};
	Object.keys(want).forEach(function(p) {
		["", "g"].forEach(function(g) {
			var re = new RegExp("^\\p{" + p + "}$", "u" + g);
			var got = cps.filter(function(c) { re.lastIndex = 0; return re.test(String.fromCodePoint(c)); }).map(function(c) { return c.toString(16); });
			assert.sameValue(got.join(), want[p].join(), p + " " + g);
		});
	});
	`
	testScriptWithTestLib(script, _undefined, t)
}

func TestRegexpUnicodePropertiesAnnexB(t *testing.T) {
	// Without the u flag, \p is the letter p.
	testScript(`/\p{L}/.test("p{L}") && !/\p{L}/.test("a") && /^[\p{L}]+$/.test("{pL}") && /\P/.test("P")`, valueTrue, t)
}

func TestRegexpUnicodePropertiesInterrupt(t *testing.T) {
	for _, tc := range []struct{ name, script string }{
		// Catastrophic backtracking in regexp2.
		{"backtracking", `/^(\p{L}+)+\1$/u.test("a".repeat(40) + "1")`},
		{"backtracking ignoring case", `/^(\P{Nd}+)+\1$/iu.test("a".repeat(40) + "1")`},
		{"class", `/^([\p{L}\p{N}]+)+\1$/iu.test("a".repeat(40) + "-")`},
		// Many matches.
		{"global", `"ab".repeat(1 << 22).replace(/(?<=\p{L})\p{Ll}/gu, "c")`},
	} {
		t.Run(tc.name, func(t *testing.T) { runInterrupted(t, tc.script, nil) })
	}
}

func TestRegexpUnicodePropertiesAllocationLimit(t *testing.T) {
	const limit = 16 << 20
	for _, call := range []string{`s.match(/\p{L}/gu)`, `s.match(/\p{L}/giu)`, `s.replace(/(?<=\p{L})\P{N}/gu, "b")`} {
		t.Run(call, func(t *testing.T) {
			vm := New()
			if _, err := vm.RunString(`var s = "a".repeat(1e7)`); err != nil {
				t.Fatal(err)
			}
			prg := MustCompile("", call, false)
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			vm.SetAllocationLimit(limit)
			_, err := vm.RunProgram(prg)
			runtime.ReadMemStats(&after)
			if !errors.Is(err, ErrAllocationLimitExceeded) {
				t.Fatalf("err: %v", err)
			}
			if d := after.TotalAlloc - before.TotalAlloc; d > 100<<20 && !raceDetectorEnabled {
				t.Fatalf("allocated %d bytes before refusing", d)
			}
		})
	}
}
