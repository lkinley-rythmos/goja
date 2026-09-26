// This script generates regexp_unicode_tables.go: the names of the Unicode
// properties that RegExp property escapes (\p{...} and \P{...} under the u
// flag) accept, and the character tables for the properties Go's unicode
// package does not have. Node.js 18 (V8 10.2) is the reference, so the script
// must run under Node.js 18, with a checkout of test262 for the candidate
// property names:
//
//	docker run --rm --network none \
//	  -v "$PWD/testdata/test262:/test262:ro" -v "$PWD/parser:/out" \
//	  --entrypoint node <node 18 image> /out/regexp_unicode_gen.js
//	gofmt -w parser/regexp_unicode_tables.go
//
// A name is accepted exactly when V8 accepts it. The General_Category and
// Script tables themselves come from Go's unicode package, as do the binary
// properties that can be derived from it (see regexp_unicode.go).

'use strict';

const fs = require('fs');

const TEST262_DIR = '/test262/test/built-ins/RegExp/property-escapes/generated';
const OUT = '/out/regexp_unicode_tables.go';

// Binary properties Go's unicode package has no data for, and that cannot be
// derived from the data it has. Their tables are copied from V8.
const EMBEDDED_BINARY = [
    'Bidi_Mirrored',
    'Changes_When_Casefolded',
    'Changes_When_Casemapped',
    'Changes_When_Lowercased',
    'Changes_When_NFKC_Casefolded',
    'Changes_When_Titlecased',
    'Changes_When_Uppercased',
    'Emoji',
    'Emoji_Component',
    'Emoji_Modifier',
    'Emoji_Modifier_Base',
    'Emoji_Presentation',
    'Extended_Pictographic',
];

// The keys of unicode.Categories.
const GO_CATEGORIES = new Set(['C', 'Cc', 'Cf', 'Cn', 'Co', 'Cs', 'L', 'LC', 'Ll', 'Lm', 'Lo', 'Lt', 'Lu',
    'M', 'Mc', 'Me', 'Mn', 'N', 'Nd', 'Nl', 'No', 'P', 'Pc', 'Pd', 'Pe', 'Pf', 'Pi', 'Po', 'Ps',
    'S', 'Sc', 'Sk', 'Sm', 'So', 'Z', 'Zl', 'Zp', 'Zs']);

function accepted(name) {
    try {
        new RegExp('\\p{' + name + '}', 'u');
        return true;
    } catch (e) {
        return false;
    }
}

// A string of every code point except the surrogates, in order, so that a
// global match of /\p{X}+/gu yields the runs of X.
function range(a, b) {
    const r = [];
    for (let i = a; i < b; i++) r.push(i);
    return r;
}
const parts = [];
for (let cp = 0; cp < 0xD800; cp += 4096) parts.push(String.fromCodePoint(...range(cp, Math.min(cp + 4096, 0xD800))));
for (let cp = 0xE000; cp <= 0x10FFFF; cp += 4096) parts.push(String.fromCodePoint(...range(cp, Math.min(cp + 4096, 0x110000))));
const ALL = parts.join('');

function indexToCodePoint(i) {
    if (i < 0xD800) return i;
    if (i < 0xF800) return i + 0x800;
    return 0x10000 + (i - 0xF800) / 2;
}

function runsOf(name) {
    const runs = [];
    const one = new RegExp('^\\p{' + name + '}$', 'u');
    let start = -1;
    for (let cp = 0xD800; cp <= 0xE000; cp++) {
        const m = cp < 0xE000 && one.test(String.fromCharCode(cp));
        if (m && start < 0) start = cp;
        if (!m && start >= 0) {
            runs.push([start, cp - 1]);
            start = -1;
        }
    }
    for (const m of ALL.matchAll(new RegExp('\\p{' + name + '}+', 'gu'))) {
        const lastUnit = m[0].charCodeAt(m[0].length - 1);
        const last = lastUnit >= 0xDC00 && lastUnit <= 0xDFFF && m[0].length > 1 ? 2 : 1;
        runs.push([indexToCodePoint(m.index), indexToCodePoint(m.index + m[0].length - last)]);
    }
    runs.sort((x, y) => x[0] - y[0]);
    const out = [];
    for (const r of runs) {
        const last = out[out.length - 1];
        if (last && last[1] + 1 >= r[0]) last[1] = Math.max(last[1], r[1]);
        else out.push(r.slice());
    }
    return out;
}

function toSet(runs) {
    const s = new Set();
    for (const [a, b] of runs) for (let c = a; c <= b; c++) s.add(c);
    return s;
}

function setToRuns(s) {
    const cps = [...s].sort((a, b) => a - b);
    const out = [];
    for (const c of cps) {
        const last = out[out.length - 1];
        if (last && last[1] + 1 === c) last[1] = c;
        else out.push([c, c]);
    }
    return out;
}

function minus(a, b) {
    const s = toSet(a);
    for (const c of toSet(b)) s.delete(c);
    return setToRuns(s);
}

function hex(n) {
    return '0x' + n.toString(16).padStart(4, '0');
}

// rangeTable formats runs as a unicode.RangeTable, folding runs of single
// code points with a common stride the way Go's maketables does.
function rangeTable(runs) {
    const split = [];
    for (const [a, b] of runs) {
        if (a <= 0xFFFF && b > 0xFFFF) {
            split.push([a, 0xFFFF], [0x10000, b]);
        } else {
            split.push([a, b]);
        }
    }
    const entries = [];
    for (let i = 0; i < split.length;) {
        const [a, b] = split[i];
        if (a !== b) {
            entries.push([a, b, 1]);
            i++;
            continue;
        }
        // A sequence of single code points with the same stride.
        let j = i + 1;
        if (j < split.length && split[j][0] === split[j][1] && (a > 0xFFFF) === (split[j][0] > 0xFFFF)) {
            const stride = split[j][0] - a;
            while (j + 1 < split.length && split[j + 1][0] === split[j + 1][1] &&
                split[j + 1][0] - split[j][0] === stride && (split[j + 1][0] > 0xFFFF) === (a > 0xFFFF)) {
                j++;
            }
            if (a <= 0xFFFF && stride > 0xFFFF) {
                entries.push([a, a, 1]);
                i++;
                continue;
            }
            entries.push([a, split[j][0], stride]);
            i = j + 1;
            continue;
        }
        entries.push([a, a, 1]);
        i++;
    }
    const r16 = entries.filter(e => e[1] <= 0xFFFF);
    const r32 = entries.filter(e => e[0] > 0xFFFF);
    let s = '&unicode.RangeTable{\n';
    if (r16.length) {
        s += '\t\tR16: []unicode.Range16{\n';
        for (const e of r16) s += `\t\t\t{${hex(e[0])}, ${hex(e[1])}, ${e[2]}},\n`;
        s += '\t\t},\n';
    }
    if (r32.length) {
        s += '\t\tR32: []unicode.Range32{\n';
        for (const e of r32) s += `\t\t\t{${hex(e[0])}, ${hex(e[1])}, ${e[2]}},\n`;
        s += '\t\t},\n';
    }
    const latin = r16.filter(e => e[1] <= 0xFF).length;
    if (latin) s += `\t\tLatinOffset: ${latin},\n`;
    s += '\t}';
    return s;
}

function quote(s) {
    return JSON.stringify(s);
}

const files = fs.readdirSync(TEST262_DIR).filter(f => f.endsWith('.js')).sort();
const gcValues = new Map(); // alias -> Go category key
const scriptValues = new Map(); // alias -> canonical script name
const binaryNames = new Map(); // alias -> canonical property name
const scRuns = new Map();
const scxRuns = new Map();
const embedded = new Map();

for (const f of files) {
    const key = f.replace(/\.js$/, '');
    const src = fs.readFileSync(TEST262_DIR + '/' + f, 'utf8');
    const names = [...new Set([...src.matchAll(/\\\\[pP]\{([^}]*)\}/g)].map(m => m[1]))].filter(accepted);
    if (names.length === 0) continue;
    let m;
    if ((m = /^General_Category_-_(.*)$/.exec(key))) {
        const bare = names.filter(n => !n.includes('='));
        const goKey = bare.filter(n => GO_CATEGORIES.has(n));
        if (goKey.length !== 1) throw new Error('no Go category for ' + key + ': ' + bare);
        for (const n of bare) gcValues.set(n, goKey[0]);
    } else if ((m = /^Script_-_(.*)$/.exec(key))) {
        for (const n of names) scriptValues.set(n.slice(n.indexOf('=') + 1), m[1]);
        scRuns.set(m[1], runsOf(names[0]));
    } else if ((m = /^Script_Extensions_-_(.*)$/.exec(key))) {
        for (const n of names) {
            const v = n.slice(n.indexOf('=') + 1);
            if (scriptValues.get(v) !== m[1]) throw new Error('Script_Extensions value not a Script value: ' + n);
        }
        scxRuns.set(m[1], runsOf(names[0]));
    } else {
        for (const n of names) binaryNames.set(n, key);
        if (EMBEDDED_BINARY.includes(key)) embedded.set(key, runsOf(names[0]));
    }
}
for (const name of EMBEDDED_BINARY) {
    if (!embedded.has(name)) throw new Error('missing ' + name);
}

let out = `// Code generated by regexp_unicode_gen.js; DO NOT EDIT.

// Generated with Node.js ${process.versions.node} (V8 ${process.versions.v8}, ICU ${process.versions.icu}, Unicode ${process.versions.unicode}).

package parser

import "unicode"

// regexpGeneralCategoryValues maps each General_Category value and alias that
// V8 accepts to the key of the category in unicode.Categories.
var regexpGeneralCategoryValues = map[string]string{
`;
for (const [k, v] of [...gcValues].sort()) out += `\t${quote(k)}: ${quote(v)},\n`;
out += `}

// regexpScriptValues maps each Script and Script_Extensions value and alias
// that V8 accepts to the name of the script in unicode.Scripts ("Unknown" has
// no table there).
var regexpScriptValues = map[string]string{
`;
for (const [k, v] of [...scriptValues].sort()) out += `\t${quote(k)}: ${quote(v)},\n`;
out += `}

// regexpBinaryPropertyNames maps each binary property name and alias that V8
// accepts to the property's name.
var regexpBinaryPropertyNames = map[string]string{
`;
for (const [k, v] of [...binaryNames].sort()) out += `\t${quote(k)}: ${quote(v)},\n`;
out += `}

// regexpBinaryPropertyTables holds V8's tables for the binary properties that
// Go's unicode package cannot provide.
var regexpBinaryPropertyTables = map[string]*unicode.RangeTable{
`;
for (const name of EMBEDDED_BINARY) out += `\t${quote(name)}: ${rangeTable(embedded.get(name))},\n`;
out += `}

// regexpScriptExtensionsAdded holds, for each script, the code points whose
// Script_Extensions include the script although their Script is another one.
var regexpScriptExtensionsAdded = map[string]*unicode.RangeTable{
`;
let removed = '';
for (const [name, sc] of [...scRuns].sort()) {
    const scx = scxRuns.get(name);
    if (!scx) throw new Error('no Script_Extensions for ' + name);
    const add = minus(scx, sc);
    const rem = minus(sc, scx);
    if (add.length) out += `\t${quote(name)}: ${rangeTable(add)},\n`;
    if (rem.length) removed += `\t${quote(name)}: ${rangeTable(rem)},\n`;
}
out += `}

// regexpScriptExtensionsRemoved holds, for each script, the code points of the
// script whose Script_Extensions do not include it.
var regexpScriptExtensionsRemoved = map[string]*unicode.RangeTable{
${removed}}
`;
fs.writeFileSync(OUT, out);
console.log('wrote', OUT, gcValues.size, 'gc values,', scriptValues.size, 'script values,', binaryNames.size, 'binary names');
