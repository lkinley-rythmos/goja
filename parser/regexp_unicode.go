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

// ranges returns the ranges of the property's set, which Go's regexp copies
// into its program for an escape by name.
func (p regexpProperty) ranges() int {
	return len(p.sets().set) / 2
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
	if len(o) == 0 {
		return s
	}
	res := make(runeSet, 0, len(s)+len(o))
	j := 0
	for i := 0; i < len(s); i += 2 {
		lo, hi := s[i], s[i+1]
		for j < len(o) && o[j+1] < lo {
			j += 2
		}
		for k := j; k < len(o) && o[k] <= hi; k += 2 {
			if o[k] > lo {
				res = append(res, lo, o[k]-1)
			}
			lo = o[k+1] + 1
		}
		if lo <= hi {
			res = append(res, lo, hi)
		}
	}
	return res
}

// The case folding orbits: the code points that simple case folding relates
// to another one, sorted, each with the index of its orbit (the code points
// it is related to).
var foldOrbits struct {
	once   sync.Once
	runes  []rune
	orbit  []int32
	orbits int
}

func caseFoldOrbits() ([]rune, []int32) {
	foldOrbits.once.Do(func() {
		var runes []rune
		for _, cr := range unicode.CaseRanges {
			for r := rune(cr.Lo); r <= rune(cr.Hi); r++ {
				if unicode.SimpleFold(r) != r {
					runes = append(runes, r)
				}
			}
		}
		// An orbit can have members outside CaseRanges (SimpleFold has its
		// own table of orbits), so add every orbit's members, then drop the
		// duplicates.
		for _, r := range append([]rune(nil), runes...) {
			for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
				runes = append(runes, f)
			}
		}
		sort.Slice(runes, func(i, j int) bool { return runes[i] < runes[j] })
		var uniq []rune
		for i, r := range runes {
			if i == 0 || r != runes[i-1] {
				uniq = append(uniq, r)
			}
		}
		runes = uniq
		index := func(r rune) int {
			return sort.Search(len(runes), func(i int) bool { return runes[i] >= r })
		}
		orbit := make([]int32, len(runes))
		for i := range orbit {
			orbit[i] = -1
		}
		n := 0
		for i, r := range runes {
			if orbit[i] >= 0 {
				continue
			}
			orbit[i] = int32(n)
			for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
				if j := index(f); j < len(runes) && runes[j] == f {
					orbit[j] = int32(n)
				}
			}
			n++
		}
		foldOrbits.runes, foldOrbits.orbit, foldOrbits.orbits = runes, orbit, n
	})
	return foldOrbits.runes, foldOrbits.orbit
}

// foldMembership reports, for each code point that case folding relates to
// another, whether it is in s.
func foldMembership(s runeSet) []bool {
	runes, _ := caseFoldOrbits()
	in := make([]bool, len(runes))
	j := 0
	for i, r := range runes {
		for j < len(s) && s[j+1] < r {
			j += 2
		}
		in[i] = j < len(s) && s[j] <= r
	}
	return in
}

// caseClosure adds to s every code point that simple case folding makes
// equivalent to one in s.
func caseClosure(s runeSet) runeSet {
	runes, orbit := caseFoldOrbits()
	in := foldMembership(s)
	hit := make([]bool, foldOrbits.orbits)
	for i := range runes {
		if in[i] {
			hit[orbit[i]] = true
		}
	}
	var add runeSet
	for i, r := range runes {
		if !in[i] && hit[orbit[i]] {
			add = add.appendRange(r, r)
		}
	}
	return s.union(add)
}

// complementClosure returns the case closure of the complement of s.
func complementClosure(s runeSet) runeSet {
	runes, orbit := caseFoldOrbits()
	in := foldMembership(s)
	missing := make([]bool, foldOrbits.orbits) // the orbit has a member not in s
	for i := range runes {
		if !in[i] {
			missing[orbit[i]] = true
		}
	}
	var add runeSet
	for i, r := range runes {
		if in[i] && missing[orbit[i]] {
			add = add.appendRange(r, r)
		}
	}
	return s.complement().union(add)
}

type rangeWriter interface {
	Write([]byte) (int, error)
	WriteByte(byte) error
	WriteString(string) (int, error)
}

// subsetOf reports whether every character of s is in o.
func (s runeSet) subsetOf(o runeSet) bool {
	j := 0
	for i := 0; i < len(s); i += 2 {
		for j < len(o) && o[j+1] < s[i] {
			j += 2
		}
		if j >= len(o) || o[j] > s[i] || o[j+1] < s[i+1] {
			return false
		}
	}
	return true
}

// A set is written for an engine as a character class. One with many
// ranges is slow to write and to compile (regexp2 sorts a class's ranges
// again for each one added), so a class is written as the tables the engine
// knows by name that the set contains, with the rest as ranges, or as the
// negation of the same for the complement, whichever is smaller. Go's regexp
// knows General_Category and Script (older versions not every script name);
// regexp2 also knows the binary properties of Go's unicode package.
type namedTable struct {
	name string
	set  runeSet
}

var namedTables struct {
	once              sync.Once
	goRegexp, regexp2 []namedTable
}

// engineNamedTables returns the tables an engine knows by name, largest
// first.
func engineNamedTables(regexp2 bool) []namedTable {
	namedTables.once.Do(func() {
		var all []namedTable
		add := func(m map[string]*unicode.RangeTable) {
			for name, t := range m {
				all = append(all, namedTable{name, tableSet(t)})
			}
		}
		add(unicode.Categories)
		add(unicode.Scripts)
		for _, t := range all {
			if _, err := syntax.Parse(`\p{`+t.name+`}`, syntax.Perl); err == nil {
				namedTables.goRegexp = append(namedTables.goRegexp, t)
			}
		}
		add(unicode.Properties)
		namedTables.regexp2 = all
		for _, tables := range [][]namedTable{namedTables.goRegexp, namedTables.regexp2} {
			sort.Slice(tables, func(i, j int) bool {
				if len(tables[i].set) != len(tables[j].set) {
					return len(tables[i].set) > len(tables[j].set)
				}
				return tables[i].name < tables[j].name
			})
		}
	})
	if regexp2 {
		return namedTables.regexp2
	}
	return namedTables.goRegexp
}

// The named tables of a class: one is used only if it saves coverMinGain
// ranges, and at most coverMaxNames are, since regexp2 checks a class's
// categories one by one on every character.
const (
	coverMinGain  = 16
	coverMaxNames = 8
)

// cover splits s into tables (largest first) it contains and the rest.
func (s runeSet) cover(tables []namedTable) (names []string, rest runeSet) {
	rest = s
	for _, t := range tables {
		if len(t.set) < 2*coverMinGain || len(rest) < 2*coverMinGain {
			break
		}
		if !t.set.subsetOf(rest) {
			continue
		}
		r := rest.minus(t.set)
		if len(rest)-len(r) < 2*coverMinGain {
			continue
		}
		names = append(names, t.name)
		rest = r
		if len(names) == coverMaxNames {
			break
		}
	}
	return
}

// classCost is the size of a class in ranges, for choosing its form.
func classCost(names []string, rest runeSet) int {
	return 2*len(names) + len(rest)/2
}

// writeClassItems writes s as class items for an engine: the tables the
// engine knows by name that it contains, and the rest as ranges. It returns
// what the class costs the engine (see classUnits).
func writeClassItems(b rangeWriter, s runeSet, regexp2 bool) int {
	if len(s) == 0 && !regexp2 {
		// The empty set; Go's regexp refuses an empty class.
		b.WriteString(`\P{Any}`)
		return 0
	}
	names, rest := s.cover(engineNamedTables(regexp2))
	return writeNamesAndRanges(b, names, rest, regexp2)
}

func writeNamesAndRanges(b rangeWriter, names []string, rest runeSet, regexp2 bool) int {
	for _, name := range names {
		b.WriteString(`\p{`)
		b.WriteString(name)
		b.WriteByte('}')
	}
	if regexp2 {
		writeRanges(b, rest, `\u`)
	} else {
		writeRanges(b, rest, `\x`)
	}
	return classUnits(names, rest, regexp2)
}

// classUnits is what a class of the named tables and the ranges costs an
// engine to compile, in units of maxRegexpExpansion: Go's regexp copies a
// named table's ranges into the class and sorts them with the others,
// regexp2 keeps the name; a range written out costs Go's regexp
// expansionTextRange to parse, and regexp2 regexp2TextUnits.
func classUnits(names []string, rest runeSet, regexp2 bool) int {
	if regexp2 {
		return regexp2TextUnits(len(rest)/2) + len(names)
	}
	n := expansionTextRange * len(rest) / 2
	for _, name := range names {
		n += expansionClassTable * namedTableRanges(name)
	}
	return n
}

// regexp2TextUnits is what a class of n ranges written out costs regexp2,
// which sorts the class's ranges again for each one it adds: about
// 1 µs + n/200 µs per range (600 ranges take 1.8 ms).
func regexp2TextUnits(n int) int {
	return 20*n + n*n/11
}

// namedTableRanges returns the ranges of a table Go's regexp knows by name.
func namedTableRanges(name string) int {
	if t := unicode.Categories[name]; t != nil {
		return len(tableSet(t)) / 2
	}
	if t := unicode.Scripts[name]; t != nil {
		return len(tableSet(t)) / 2
	}
	return 0
}

// writeClass writes a class standing for s for an engine, as the items of s
// or as the negation of those of its complement, whichever is smaller. It
// returns what the class costs the engine (see classUnits).
func writeClass(b rangeWriter, s runeSet, regexp2 bool) int {
	if len(s) == 0 {
		// The empty set.
		if regexp2 {
			b.WriteString(`[]`)
		} else {
			b.WriteString(`\P{Any}`)
		}
		return 0
	}
	tables := engineNamedTables(regexp2)
	names, rest := s.cover(tables)
	negated := false
	if comp := s.complement(); len(comp) > 0 {
		if cnames, crest := comp.cover(tables); classCost(cnames, crest) < classCost(names, rest) {
			names, rest, negated = cnames, crest, true
		}
	}
	b.WriteByte('[')
	if negated {
		b.WriteByte('^')
	}
	n := writeNamesAndRanges(b, names, rest, regexp2)
	b.WriteByte(']')
	return n
}

// The classes of the properties, as written for the engines, are kept: a
// pattern can have many escapes of one property, and the class of a set with
// many ranges takes time to choose.
type propertyClassKey struct {
	prop                        regexpProperty
	negate, ignoreCase, regexp2 bool
	items                       bool
}

// propertyClass is a property's escape written for an engine, and what it
// costs the engine (see classUnits).
type propertyClass struct {
	text  string
	units int
}

var propertyClassCache sync.Map // propertyClassKey -> propertyClass

// class returns the class standing for the property's escape for an engine
// (see writeClass).
func (p regexpProperty) class(negate, ignoreCase, regexp2 bool) propertyClass {
	key := propertyClassKey{prop: p, negate: negate, ignoreCase: ignoreCase, regexp2: regexp2}
	if v, ok := propertyClassCache.Load(key); ok {
		return v.(propertyClass)
	}
	var b strings.Builder
	n := writeClass(&b, p.charSet(negate, ignoreCase), regexp2)
	v, _ := propertyClassCache.LoadOrStore(key, propertyClass{b.String(), n})
	return v.(propertyClass)
}

// classItems returns the property's escape as items of a class for Go's
// regexp, matched case-sensitively (see writeClassItems).
func (p regexpProperty) classItems(negate bool) propertyClass {
	key := propertyClassKey{prop: p, negate: negate, items: true}
	if v, ok := propertyClassCache.Load(key); ok {
		return v.(propertyClass)
	}
	var b strings.Builder
	n := writeClassItems(&b, p.charSet(negate, false), false)
	v, _ := propertyClassCache.LoadOrStore(key, propertyClass{b.String(), n})
	return v.(propertyClass)
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
