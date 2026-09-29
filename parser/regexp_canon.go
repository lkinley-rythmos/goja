package parser

import (
	"sort"
	"strings"
	"sync"
)

// Without the u flag, a pattern that ignores case matches a character
// against another if Canonicalize (ECMA-262 22.2.2.7.3) makes them the same:
// toUpperCase, unless that is not one code unit or maps a code unit from 128
// up to one below 128 (so U+017F and U+212A are not variants of s and k).
// Go's regexp and regexp2 fold case otherwise, so both engines get such a
// pattern case-sensitively, with each character and class written as the
// set of code units Canonicalize makes it equivalent to (see
// regexpCanonClasses); a backreference is left to regexp2 ignoring case.

var canonTable struct {
	once  sync.Once
	units []uint16 // the code units that have variants, sorted
	class []int32  // the index in classes of each's class
	sets  []runeSet
}

func canonClasses() ([]uint16, []int32, []runeSet) {
	canonTable.once.Do(func() {
		type entry struct {
			unit  uint16
			class int32
		}
		var entries []entry
		var sets []runeSet
		for i := 0; i < len(regexpCanonClasses); {
			n := int(regexpCanonClasses[i])
			var set runeSet
			for _, u := range regexpCanonClasses[i+1 : i+1+n] {
				set = set.union(runeSet{rune(u), rune(u)})
				entries = append(entries, entry{u, int32(len(sets))})
			}
			sets = append(sets, set)
			i += 1 + n
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].unit < entries[j].unit })
		for _, e := range entries {
			canonTable.units = append(canonTable.units, e.unit)
			canonTable.class = append(canonTable.class, e.class)
		}
		canonTable.sets = sets
	})
	return canonTable.units, canonTable.class, canonTable.sets
}

// canonClass returns the code units Canonicalize makes r equivalent to.
func canonClass(r rune) runeSet {
	units, class, sets := canonClasses()
	if r <= 0xffff {
		i := sort.Search(len(units), func(i int) bool { return rune(units[i]) >= r })
		if i < len(units) && rune(units[i]) == r {
			return sets[class[i]]
		}
	}
	return runeSet{r, r}
}

// canonClosure adds to s every code unit Canonicalize makes equivalent to
// one in s.
func canonClosure(s runeSet) runeSet {
	units, class, sets := canonClasses()
	hit := make([]bool, len(sets))
	var add runeSet
	for i, u := range units {
		if !hit[class[i]] && s.contains(rune(u)) {
			hit[class[i]] = true
			add = add.union(sets[class[i]])
		}
	}
	return s.union(add)
}

// writeCanonChar writes, for both engines, the character r stands for (the
// input from start to end) as the class Canonicalize makes it equivalent
// to, when that has more than r.
func (self *_RegExp_parser) writeCanonChar(start, end int, r rune) {
	cc, ok := self.canonChars[r]
	if !ok {
		set := canonClass(r)
		if len(set) > 2 || set[0] != set[1] {
			var goText, r2Text strings.Builder
			goText.WriteByte('[')
			writeRanges(&goText, set, `\x`)
			goText.WriteByte(']')
			writeBMPClass(&r2Text, set)
			cc = canonChar{goText.String(), r2Text.String(), len(set) / 2}
		}
		if self.canonChars == nil {
			self.canonChars = make(map[rune]canonChar)
		}
		self.canonChars[r] = cc
	}
	if cc.goText == "" {
		return
	}
	if self.goUsed() {
		self.goRegexp.Truncate(self.canonGoStart)
		self.goRegexp.WriteString(cc.goText)
		self.goExpansion += goClassUnits + goClassItemUnits*cc.items
	}
	if self.regexp2Offset > start {
		// The escape was written for regexp2 already.
		self.regexp2.Truncate(self.canonR2Len)
		self.regexp2Offset = self.canonR2Offset
	}
	self.writeRegexp2(start, end, cc.r2Text)
}

// canonChar is a character written as the class Canonicalize makes it
// equivalent to (goText is empty when that is the character alone).
type canonChar struct {
	goText, r2Text string
	items          int
}

// markCanon notes where the Go text of the next atom starts, for
// writeCanonChar.
func (self *_RegExp_parser) markCanon() {
	if self.passOffset != -1 {
		self.stopPassing()
	}
	self.canonGoStart = self.goRegexp.Len()
	self.canonR2Len, self.canonR2Offset = self.regexp2.Len(), self.regexp2Offset
}

// canonFold reports whether the pattern is matched with Canonicalize
// (without the u flag, ignoring case).
func (self *_RegExp_parser) canonFold() bool {
	return self.ignoreCase && !self.unicode
}

// canonFoldClass writes the class just scanned for both engines as the set
// Canonicalize makes its characters equivalent to, and counts what that
// costs; a pattern computes each distinct class once.
func (self *_RegExp_parser) canonFoldClass(negated bool) {
	end := self.chrOffset // after the ]
	text := self.str[self.classStart:end]
	rw, ok := self.foldedClasses[text]
	if !ok {
		set := canonClosure(self.classSet)
		if negated {
			set = set.complement()
		}
		var goText, r2Text strings.Builder
		rw.goUnits = writeClass(&goText, set, false)
		rw.r2Units = writeBMPClass(&r2Text, set)
		rw.goText, rw.r2Text = goText.String(), r2Text.String()
		if self.foldedClasses == nil {
			self.foldedClasses = make(map[string]foldedClass)
		}
		self.foldedClasses[text] = rw
		self.expandGo(classFoldUnits)
		self.expandRegexp2(classFoldUnits)
	}
	if self.goUsed() && self.classGoStart >= 0 {
		self.goRegexp.Truncate(self.classGoStart)
		self.goRegexp.WriteString(rw.goText)
		self.expandGo(rw.goUnits)
	}
	self.restartRegexp2Class()
	self.writeRegexp2(self.classStart, end, rw.r2Text)
	self.expandRegexp2(rw.r2Units)
}

// CanonClasses returns the classes of more than one code unit that
// Canonicalize makes equivalent without the u flag.
func CanonClasses() [][]uint16 {
	var res [][]uint16
	for i := 0; i < len(regexpCanonClasses); {
		n := int(regexpCanonClasses[i])
		res = append(res, regexpCanonClasses[i+1:i+1+n])
		i += 1 + n
	}
	return res
}
