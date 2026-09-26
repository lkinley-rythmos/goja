package parser

import (
	"regexp/syntax"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// Unicode property escapes (\p{...} and \P{...} under the u flag).
//
// Property and value names are matched exactly, as V8 does: the accepted
// names are the ones in regexp_unicode_tables.go. The character data comes
// from Go's unicode package where it has it (General_Category, Script and
// most binary properties, directly or derived as in Unicode's
// DerivedCoreProperties.txt), and from V8's tables otherwise, so the Unicode
// version of the former follows the Go toolchain.

// runeSet is a sorted list of disjoint, non-adjacent inclusive ranges,
// stored as lo0, hi0, lo1, hi1, ...
type runeSet []rune

type regexpPropertyKind int

const (
	propGeneralCategory regexpPropertyKind = iota
	propScript
	propScriptExtensions
	propBinary
)

type regexpProperty struct {
	kind regexpPropertyKind
	name string // unicode.Categories key, unicode.Scripts name or binary property name
}

// lookupRegexpProperty resolves the contents of \p{...}.
func lookupRegexpProperty(s string) (regexpProperty, bool) {
	if i := strings.IndexByte(s, '='); i >= 0 {
		name, value := s[:i], s[i+1:]
		switch name {
		case "General_Category", "gc":
			if cat, ok := regexpGeneralCategoryValues[value]; ok {
				return regexpProperty{propGeneralCategory, cat}, true
			}
		case "Script", "sc":
			if sc, ok := regexpScriptValues[value]; ok && scriptAvailable(sc) {
				return regexpProperty{propScript, sc}, true
			}
		case "Script_Extensions", "scx":
			if sc, ok := regexpScriptValues[value]; ok && scriptAvailable(sc) {
				return regexpProperty{propScriptExtensions, sc}, true
			}
		}
		return regexpProperty{}, false
	}
	if cat, ok := regexpGeneralCategoryValues[s]; ok {
		return regexpProperty{propGeneralCategory, cat}, true
	}
	if name, ok := regexpBinaryPropertyNames[s]; ok {
		return regexpProperty{propBinary, name}, true
	}
	return regexpProperty{}, false
}

func scriptAvailable(name string) bool {
	return name == "Unknown" || unicode.Scripts[name] != nil
}

// goName returns the property's key in unicode.Categories or unicode.Scripts,
// which regexp2 also knows it by, or "" if it has none.
func (p regexpProperty) goName() string {
	switch p.kind {
	case propGeneralCategory:
		if unicode.Categories[p.name] != nil {
			return p.name
		}
	case propScript:
		if unicode.Scripts[p.name] != nil {
			return p.name
		}
	}
	return ""
}

type regexpPropertySets struct {
	once                      sync.Once
	set, closure, compClosure runeSet
	foldExtra, compFoldExtra  runeSet
	notClosed, goFoldMissing  runeSet
	goRegexpKnowsName         bool
}

var regexpPropertyCache sync.Map // regexpProperty -> *regexpPropertySets

func (p regexpProperty) sets() *regexpPropertySets {
	v, ok := regexpPropertyCache.Load(p)
	if !ok {
		v, _ = regexpPropertyCache.LoadOrStore(p, &regexpPropertySets{})
	}
	s := v.(*regexpPropertySets)
	s.once.Do(func() {
		s.set = p.compute()
		s.closure = caseClosure(s.set)
		s.compClosure = complementClosure(s.set)
		// The case variants of the set's characters, and the characters
		// whose case variants are not all in it.
		s.foldExtra = s.closure.minus(s.set)
		s.notClosed = s.set.minus(s.compClosure.complement())
		s.compFoldExtra = s.foldExtra.union(s.notClosed)
		if name := p.goName(); name != "" {
			// The case variants Go's regexp adds from its tables.
			var fold *unicode.RangeTable
			if p.kind == propGeneralCategory {
				fold = unicode.FoldCategory[name]
			} else {
				fold = unicode.FoldScript[name]
			}
			s.goFoldMissing = s.foldExtra
			if fold != nil {
				s.goFoldMissing = s.goFoldMissing.minus(tableSet(fold))
			}
			// Older versions of Go's regexp do not know every script name.
			_, err := syntax.Parse(`\p{`+name+`}`, syntax.Perl)
			s.goRegexpKnowsName = err == nil
		}
	})
	return s
}

// goRegexpName returns the name Go's regexp knows the property by, or "".
func (p regexpProperty) goRegexpName() string {
	if p.goName() != "" && p.sets().goRegexpKnowsName {
		return p.goName()
	}
	return ""
}

// foldExtra returns what, with the property (negated if negate), makes up
// its closure under case folding: the case variants of its characters, and
// for negate, also the characters some of whose case variants are not in it.
// Go's regexp, ignoring case, given the property by name and these
// characters, adds the variants of the latter and matches the closure.
func (p regexpProperty) foldExtra(negate bool) runeSet {
	if negate {
		return p.sets().compFoldExtra
	}
	return p.sets().foldExtra
}

// notClosed returns the property's characters some of whose case variants
// are not in it: those its negation's closure under case folding adds.
func (p regexpProperty) notClosed() runeSet {
	return p.sets().notClosed
}

// goFoldMissing returns the case variants of the property's characters that
// Go's regexp, ignoring case, does not add when given the property by name.
func (p regexpProperty) goFoldMissing() runeSet {
	return p.sets().goFoldMissing
}

// charSet returns the characters an escape stands for; with ignoreCase, it is
// closed under simple case folding, as Canonicalize() in the spec requires.
func (p regexpProperty) charSet(negate, ignoreCase bool) runeSet {
	s := p.sets()
	switch {
	case !ignoreCase && !negate:
		return s.set
	case !ignoreCase:
		return s.set.complement()
	case !negate:
		return s.closure
	default:
		return s.compClosure
	}
}

func (p regexpProperty) compute() runeSet {
	switch p.kind {
	case propGeneralCategory:
		return tableSet(unicode.Categories[p.name])
	case propScript:
		return scriptSet(p.name)
	case propScriptExtensions:
		s := scriptSet(p.name)
		if t := regexpScriptExtensionsRemoved[p.name]; t != nil {
			s = s.minus(tableSet(t))
		}
		if t := regexpScriptExtensionsAdded[p.name]; t != nil {
			s = s.union(tableSet(t))
		}
		return s
	}
	return binaryPropertySet(p.name)
}

func scriptSet(name string) runeSet {
	if name != "Unknown" {
		return tableSet(unicode.Scripts[name])
	}
	var all runeSet
	for _, t := range unicode.Scripts {
		all = all.union(tableSet(t))
	}
	return all.complement()
}

func tables(names ...*unicode.RangeTable) runeSet {
	var s runeSet
	for _, t := range names {
		s = s.union(tableSet(t))
	}
	return s
}

// binaryPropertySet computes a binary property. The derivations are the ones
// in DerivedCoreProperties.txt.
func binaryPropertySet(name string) runeSet {
	if t := regexpBinaryPropertyTables[name]; t != nil {
		return tableSet(t)
	}
	if t := unicode.Properties[name]; t != nil {
		return tableSet(t)
	}
	switch name {
	case "Any":
		return runeSet{0, unicode.MaxRune}
	case "ASCII":
		return runeSet{0, 0x7f}
	case "Assigned":
		return tableSet(unicode.Cn).complement()
	case "Alphabetic":
		return tables(unicode.Lu, unicode.Ll, unicode.Lt, unicode.Lm, unicode.Lo, unicode.Nl, unicode.Other_Alphabetic,
			unicode.Other_Lowercase, unicode.Other_Uppercase)
	case "Lowercase":
		return tables(unicode.Ll, unicode.Other_Lowercase)
	case "Uppercase":
		return tables(unicode.Lu, unicode.Other_Uppercase)
	case "Cased":
		return tables(unicode.Ll, unicode.Other_Lowercase, unicode.Lu, unicode.Other_Uppercase, unicode.Lt)
	case "Case_Ignorable":
		// Word_Break=MidLetter, MidNumLet and Single_Quote.
		wb := runeSet{0x27, 0x27, 0x2e, 0x2e, 0x3a, 0x3a, 0xb7, 0xb7, 0x387, 0x387, 0x55f, 0x55f, 0x5f4, 0x5f4,
			0x2018, 0x2019, 0x2024, 0x2024, 0x2027, 0x2027, 0xfe13, 0xfe13, 0xfe52, 0xfe52, 0xfe55, 0xfe55,
			0xff07, 0xff07, 0xff0e, 0xff0e, 0xff1a, 0xff1a}
		return tables(unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk).union(wb)
	case "Math":
		return tables(unicode.Sm, unicode.Other_Math)
	case "ID_Start":
		return idStart()
	case "ID_Continue":
		return idContinue()
	case "XID_Start":
		// ID_Start without the characters NFKC normalization turns into
		// something that is not an identifier start.
		return idStart().minus(runeSet{0x37a, 0x37a, 0xe33, 0xe33, 0xeb3, 0xeb3, 0x309b, 0x309c,
			0xfc5e, 0xfc63, 0xfdfa, 0xfdfb, 0xfe70, 0xfe70, 0xfe72, 0xfe72, 0xfe74, 0xfe74, 0xfe76, 0xfe76,
			0xfe78, 0xfe78, 0xfe7a, 0xfe7a, 0xfe7c, 0xfe7c, 0xfe7e, 0xfe7e, 0xff9e, 0xff9f})
	case "XID_Continue":
		return idContinue().minus(runeSet{0x37a, 0x37a, 0x309b, 0x309c, 0xfc5e, 0xfc63, 0xfdfa, 0xfdfb,
			0xfe70, 0xfe70, 0xfe72, 0xfe72, 0xfe74, 0xfe74, 0xfe76, 0xfe76, 0xfe78, 0xfe78, 0xfe7a, 0xfe7a,
			0xfe7c, 0xfe7c, 0xfe7e, 0xfe7e})
	case "Default_Ignorable_Code_Point":
		return tables(unicode.Other_Default_Ignorable_Code_Point, unicode.Cf, unicode.Variation_Selector).
			minus(tables(unicode.White_Space, unicode.Prepended_Concatenation_Mark)).
			minus(runeSet{0xfff9, 0xfffb, 0x13430, 0x1343f})
	case "Grapheme_Extend":
		return graphemeExtend()
	case "Grapheme_Base":
		return tables(unicode.Cc, unicode.Cf, unicode.Cs, unicode.Co, unicode.Cn, unicode.Zl, unicode.Zp).
			union(graphemeExtend()).complement()
	}
	panic("unknown binary property " + name)
}

func idStart() runeSet {
	return tables(unicode.Lu, unicode.Ll, unicode.Lt, unicode.Lm, unicode.Lo, unicode.Nl, unicode.Other_ID_Start).
		minus(tables(unicode.Pattern_Syntax, unicode.Pattern_White_Space))
}

func idContinue() runeSet {
	return tables(unicode.Lu, unicode.Ll, unicode.Lt, unicode.Lm, unicode.Lo, unicode.Nl, unicode.Other_ID_Start,
		unicode.Mn, unicode.Mc, unicode.Nd, unicode.Pc, unicode.Other_ID_Continue).
		minus(tables(unicode.Pattern_Syntax, unicode.Pattern_White_Space))
}

func graphemeExtend() runeSet {
	return tables(unicode.Me, unicode.Mn, unicode.Other_Grapheme_Extend)
}

// tableSet converts a unicode.RangeTable.
func tableSet(t *unicode.RangeTable) runeSet {
	var s runeSet
	add := func(lo, hi, stride rune) {
		if stride == 1 {
			s = s.appendRange(lo, hi)
			return
		}
		for r := lo; r <= hi; r += stride {
			s = s.appendRange(r, r)
		}
	}
	for _, r := range t.R16 {
		add(rune(r.Lo), rune(r.Hi), rune(r.Stride))
	}
	for _, r := range t.R32 {
		add(rune(r.Lo), rune(r.Hi), rune(r.Stride))
	}
	return s
}

// appendRange adds a range that starts after every range in s.
func (s runeSet) appendRange(lo, hi rune) runeSet {
	if n := len(s); n > 0 && s[n-1]+1 >= lo {
		if hi > s[n-1] {
			s[n-1] = hi
		}
		return s
	}
	return append(s, lo, hi)
}

func (s runeSet) equal(o runeSet) bool {
	if len(s) != len(o) {
		return false
	}
	for i := range s {
		if s[i] != o[i] {
			return false
		}
	}
	return true
}

func (s runeSet) contains(r rune) bool {
	lo, hi := 0, len(s)/2
	for lo < hi {
		m := int(uint(lo+hi) >> 1)
		switch {
		case r < s[2*m]:
			hi = m
		case r > s[2*m+1]:
			lo = m + 1
		default:
			return true
		}
	}
	return false
}

func (s runeSet) union(o runeSet) runeSet {
	if len(s) == 0 {
		return o
	}
	if len(o) == 0 {
		return s
	}
	res := make(runeSet, 0, len(s)+len(o))
	i, j := 0, 0
	for i < len(s) || j < len(o) {
		var lo, hi rune
		if j >= len(o) || i < len(s) && s[i] <= o[j] {
			lo, hi = s[i], s[i+1]
			i += 2
		} else {
			lo, hi = o[j], o[j+1]
			j += 2
		}
		res = res.appendRange(lo, hi)
	}
	return res
}

func (s runeSet) complement() runeSet {
	res := make(runeSet, 0, len(s)+2)
	next := rune(0)
	for i := 0; i < len(s); i += 2 {
		if s[i] > next {
			res = append(res, next, s[i]-1)
		}
		next = s[i+1] + 1
	}
	if next <= unicode.MaxRune {
		res = append(res, next, unicode.MaxRune)
	}
	return res
}

func (s runeSet) minus(o runeSet) runeSet {
	return s.complement().union(o).complement()
}

// Case folding orbits: every code point that simple case folding relates to
// another one.
var foldRunes struct {
	once  sync.Once
	runes runeSet
}

func caseFoldRunes() runeSet {
	foldRunes.once.Do(func() {
		var cand []rune
		for _, cr := range unicode.CaseRanges {
			for r := rune(cr.Lo); r <= rune(cr.Hi); r++ {
				for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
					cand = append(cand, f)
				}
				if unicode.SimpleFold(r) != r {
					cand = append(cand, r)
				}
			}
		}
		sort.Slice(cand, func(i, j int) bool { return cand[i] < cand[j] })
		var s runeSet
		for _, r := range cand {
			s = s.appendRange(r, r)
		}
		foldRunes.runes = s
	})
	return foldRunes.runes
}

func forEachRune(s runeSet, f func(r rune)) {
	for i := 0; i < len(s); i += 2 {
		for r := s[i]; r <= s[i+1]; r++ {
			f(r)
		}
	}
}

// caseClosure adds to s every code point that simple case folding makes
// equivalent to one in s.
func caseClosure(s runeSet) runeSet {
	var add runeSet
	forEachRune(caseFoldRunes(), func(r rune) {
		if s.contains(r) {
			return
		}
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			if s.contains(f) {
				add = add.appendRange(r, r)
				return
			}
		}
	})
	return s.union(add)
}

// complementClosure returns the case closure of the complement of s.
func complementClosure(s runeSet) runeSet {
	var add runeSet
	forEachRune(caseFoldRunes(), func(r rune) {
		if !s.contains(r) {
			return
		}
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			if !s.contains(f) {
				add = add.appendRange(r, r)
				return
			}
		}
	})
	return s.complement().union(add)
}

type rangeWriter interface {
	Write([]byte) (int, error)
	WriteByte(byte) error
	WriteString(string) (int, error)
}

// writeRanges writes the ranges of s as class items, with escape (`\x` for
// Go's regexp, `\u` for regexp2) introducing each code point.
func writeRanges(b rangeWriter, s runeSet, escape string) {
	var buf [24]byte
	for i := 0; i < len(s); i += 2 {
		b.WriteString(escape)
		b.WriteByte('{')
		b.Write(strconv.AppendInt(buf[:0], int64(s[i]), 16))
		b.WriteByte('}')
		if s[i+1] != s[i] {
			b.WriteString("-")
			b.WriteString(escape)
			b.WriteByte('{')
			b.Write(strconv.AppendInt(buf[:0], int64(s[i+1]), 16))
			b.WriteByte('}')
		}
	}
}
