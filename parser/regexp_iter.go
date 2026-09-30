package parser

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// ECMAScript resets the captures of a quantified group's body at each of its
// iterations (RepeatMatcher, ECMA-262 22.2.2.3.1): /(?:(a)|b)+/.exec("ab") is
// ["ab", undefined]. Go's regexp and regexp2 keep a capture from an earlier
// iteration. So the last iteration of such a group is told by a group whose
// last capture is it: the group itself if it captures, or else a hidden
// capturing group that is its body. A capture that starts before the last
// iteration is from an earlier one, and one that starts in it from it; an
// empty one right at its start is from it unless something the body always
// consumes comes before the group (/(?:a(b?)|c)+/ on "ac"). The caller drops
// the captures of earlier iterations, and the hidden groups.
//
// ECMAScript also refuses an iteration past the minimum that matches
// nothing, where regexp2 takes one more and ends the loop there:
// /(?:(a)|(?=b))+b/.exec("ab") is ["ab", "a"], and regexp2's last iteration
// is the empty one at "b". regexp2 keeps every capture of a group, so the
// caller drops such an iteration, and the empty captures made in it, before
// it takes the last captures; a loop whose body may match nothing is told
// by a group for that even if its captures are in every iteration. Go's
// regexp takes no such iteration of * or + (it does not go round a loop
// twice at one position), but takes the optional copies of a bounded
// repeat's body that match nothing.

// Iterations are the quantified groups of a transformed pattern that may
// iterate more than once and whose iterations a group tells (see
// IterationLoop), with what the caller needs to drop the captures ECMAScript
// does not keep; group numbers are the transformed patterns'.
type Iterations struct {
	// The loops in the order of their groups' numbers, an outer one before
	// those in it.
	Loops []IterationLoop
	// The capturing groups in a loop's body.
	Captures []IterationCapture
	// Every group (from 1; 0 is the whole match), and where it is in the
	// loops it is in.
	Groups []IterationGroup
	// The hidden groups, in order.
	Hidden []int
	// Drop reports whether a match has captures of earlier iterations to
	// drop, or hidden groups.
	Drop bool
}

// IterationLoop is a quantified group that may iterate more than once, and
// the group whose last capture is its last iteration: itself if it captures,
// or a hidden group that is its body. A loop that needs neither has none:
// one with no capturing group in it that an iteration may lack, and whose
// body always consumes something (or has no group).
type IterationLoop struct {
	Group        int  // the group whose captures are the iterations
	Last         int  // the last group in its body (Group if none)
	Min, Max     int  // its bounds (Max -1 for none)
	Depth        int  // how many groups it is in, plus one
	Parent       int  // the innermost loop it is in, -1 if none
	Alternatives bool // its body has alternatives of its own
	// Reset reports whether it has a capturing group that an iteration may
	// lack (or any, with alternatives), whose capture from an earlier
	// iteration is dropped.
	Reset bool
}

// IterationCapture is a capturing group in a loop's body: its number, the
// innermost loop it is in, and whether an iteration of a loop it is in, at
// depth d, may lack it: when d < Missing (it, or a group between them, is
// optional, or a group between them has alternatives).
type IterationCapture struct {
	Group   int
	Loop    int
	Missing int
}

// IterationGroup is a group of a transformed pattern: whether it is a hidden
// group, and, for a loop it is in at depth d, whether the loop's body
// always consumes something before it (d <= Consumed), and whether it is in
// a lookaround within the body (d < Around), whose captures need not be
// where it is and are left as the engines give them.
type IterationGroup struct {
	Consumed, Around int
	Hidden           bool
	// The innermost loop the group is in, and the loop whose iterations it
	// tells (indexes into Loops, -1 for none).
	Loop, Marks int
	// For a capturing group, the innermost capturing group it is in, and the
	// last one that is always captured before it where it is (0 for none):
	// in an iteration that has it, they are captured too.
	Parent, Before int
	// Referenced reports whether a backreference is to it: the engine
	// matched with its captures (see dropEmptyIterations).
	Referenced bool
}

// iterPlan is where hidden groups go, and the groups' numbers once they are
// in.
type iterPlan struct {
	hidden   map[int]bool // the offsets of the '(' of the loops with one
	renumber []int        // a capturing group's number (from 1) once they are in
	Iterations
}

// iterFrame is a group planIterations has seen.
type iterFrame struct {
	open, bodyStart, close int
	parent                 *iterFrame
	depth                  int // in the stack of groups, the pattern being 0
	num                    int // its number if it captures (from 1), else 0
	// The last capturing group always captured before the current point of
	// its body (in the current alternative), and before it opens.
	lastCaptured, capturedBefore int
	capParent                    *iterFrame // the innermost capturing group it is in
	around                       bool
	alternatives                 bool // the body has a | of its own
	optional                     bool // quantified with a minimum of 0
	loop                         bool // quantified with a maximum over 1, not a lookaround
	min, max                     int
	altConsumed                  bool // the current alternative consumes something so far
	allConsumed                  bool // every alternative before the current one did
	consumes                     bool // the body always consumes something
	// Where the group is in the groups around it when it opens (see
	// IterationGroup): the deepest of them whose current alternative has
	// consumed something, and the deepest lookaround.
	consumedBelow, aroundBelow int
	// Of the capturing groups in the body, not in a lookaround in it:
	// whether there is one, and whether an iteration may lack one.
	hasCapture, hasMissing bool
	hidden                 bool // has a hidden group
	marker                 bool // a loop told by a group
	// Computed once the scan is done: the deepest of it and the groups it
	// is in that is optional, a lookaround or has alternatives (0 for
	// none), the innermost loop told by a group it is in, and how many.
	deepestBad  int
	markerAbove *iterFrame
	markers     int
	loopIndex   int
}

// maxIterationWork bounds the work of dropping captures from a match: a
// capture is checked against each loop it is in (1,000 nested loops with
// 1,000 captures), past which captures are left as the engines give them.
const maxIterationWork = 1 << 20

// maxIterationGroups is how many groups a pattern may have for a plan: past
// it, the pattern is refused (every group costs regexp2 at least
// costGroup.r2).
const maxIterationGroups = maxRegexpExpansion / 12

// planIterations finds the loops of a pattern that parses (its result is not
// used otherwise), in time and memory linear in its length.
func (self *_RegExp_parser) planIterations() *iterPlan {
	s := self.str
	n := len(s)
	root := &iterFrame{allConsumed: true}
	stack := []*iterFrame{root}
	var frames []*iterFrame // every group, in the order they open
	captures := 0
	// The groups backreferences are to, by number and by name.
	var refNums []int
	var refNames []string
	var names map[string]int
	// consumedTop is the deepest group on the stack whose current
	// alternative has consumed something (0 for none).
	consumedTop := func() int {
		top := stack[len(stack)-1]
		if top.altConsumed && top != root {
			return top.depth
		}
		return top.consumedBelow
	}
	// quant parses a quantifier at i: its bounds (max -1 for none) and
	// length, or ok false.
	quant := func(i int) (min, max, length int, ok bool) {
		if i >= n {
			return
		}
		switch s[i] {
		case '*':
			min, max, length, ok = 0, -1, 1, true
		case '+':
			min, max, length, ok = 1, -1, 1, true
		case '?':
			min, max, length, ok = 0, 1, 1, true
		case '{':
			j := i + 1
			num := func() (int, bool) {
				start, v := j, 0
				for j < n && s[j] >= '0' && s[j] <= '9' {
					if v < 1<<30 {
						v = v*10 + int(s[j]-'0')
					}
					j++
				}
				return v, j > start
			}
			var has bool
			if min, has = num(); !has {
				return 0, 0, 0, false
			}
			max = min
			if j < n && s[j] == ',' {
				j++
				var bounded bool
				if max, bounded = num(); !bounded {
					max = -1
				}
			}
			if j >= n || s[j] != '}' {
				return 0, 0, 0, false
			}
			length, ok = j+1-i, true
		}
		if ok && i+length < n && s[i+length] == '?' {
			length++
		}
		return
	}
	// atom notes an atom ending at i that consumes if consumes, and returns
	// where the term (with its quantifier) ends.
	atom := func(i int, consumes bool) int {
		if min, _, length, ok := quant(i); ok {
			i += length
			if min == 0 {
				consumes = false
			}
		}
		top := stack[len(stack)-1]
		top.altConsumed = top.altConsumed || consumes
		return i
	}
	for i := 0; i < n; {
		c := s[i]
		switch c {
		case '\\':
			if i+1 >= n {
				i++
				continue
			}
			e := s[i+1]
			end := i + 2
			if e >= utf8.RuneSelf {
				_, w := utf8.DecodeRuneInString(s[i+1:])
				end = i + 1 + w
			}
			consumes := true
			switch {
			case e == 'b' || e == 'B':
				consumes = false
			case e >= '1' && e <= '9':
				// A backreference may match nothing (and a legacy octal
				// escape consumes, which is taken as not).
				consumes = false
				v := 0
				for end = i + 1; end < n && s[end] >= '0' && s[end] <= '9'; end++ {
					if v < 1<<20 {
						v = v*10 + int(s[end]-'0')
					}
				}
				refNums = append(refNums, v)
			case e == 'k' && end < n && s[end] == '<':
				consumes = false
				start := end + 1
				for end < n && s[end] != '>' {
					end++
				}
				refNames = append(refNames, s[min(start, end):end])
				end++
			case (e == 'p' || e == 'P' || e == 'u') && end < n && s[end] == '{':
				for end < n && s[end] != '}' {
					end++
				}
				end++
			}
			i = atom(min(end, n), consumes)
		case '[':
			j := i + 1
			if j < n && s[j] == '^' {
				j++
			}
			for j < n && s[j] != ']' {
				if s[j] == '\\' {
					j++
				}
				j++
			}
			i = atom(min(j+1, n), true)
		case '(':
			top := stack[len(stack)-1]
			f := &iterFrame{open: i, parent: top, depth: len(stack), allConsumed: true}
			j := i + 1
			capture, name := false, ""
			switch {
			case j+1 < n && s[j] == '?' && (s[j+1] == '=' || s[j+1] == '!'):
				f.around, j = true, j+2
			case j+2 < n && s[j] == '?' && s[j+1] == '<' && (s[j+2] == '=' || s[j+2] == '!'):
				f.around, j = true, j+3
			case j+1 < n && s[j] == '?' && s[j+1] == '<':
				capture = true
				for j < n && s[j] != '>' {
					j++
				}
				name = s[min(i+3, j):j]
				j++
			case j+1 < n && s[j] == '?' && s[j+1] == ':':
				j += 2
			default:
				capture = true
			}
			f.bodyStart = min(j, n)
			if len(stack) > maxGroupNesting || len(frames) >= maxIterationGroups {
				// Refused anyway (see scanGroup, and the cost of groups).
				return nil
			}
			f.consumedBelow = consumedTop()
			f.capturedBefore = top.lastCaptured
			if f.capturedBefore == 0 {
				f.capturedBefore = top.capturedBefore
			}
			f.capParent = top.capParent
			if top.num != 0 {
				f.capParent = top
			}
			f.aroundBelow = top.aroundBelow
			if top.around {
				f.aroundBelow = top.depth
			}
			if capture {
				captures++
				f.num = captures
				if name != "" {
					if names == nil {
						names = make(map[string]int)
					}
					names[name] = captures
				}
			}
			frames = append(frames, f)
			stack = append(stack, f)
			i = f.bodyStart
		case ')':
			if len(stack) == 1 {
				i++
				continue
			}
			f := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			f.close = i
			f.consumes = f.allConsumed && f.altConsumed && !f.around
			min, max, _, ok := quant(i + 1)
			f.optional = ok && min == 0
			f.loop = ok && (max < 0 || max > 1) && !f.around
			f.min, f.max = min, max
			if f.loop {
				// It is told by a group if an iteration may lack a
				// capturing group in it (or any, with alternatives), or
				// if an iteration may match nothing (see Iterations).
				reset := f.hasCapture && (f.alternatives || f.hasMissing)
				f.marker = reset || !f.consumes && (f.num != 0 || f.hasCapture)
				f.hidden = f.marker && f.num == 0
			}
			if p := stack[len(stack)-1]; !f.optional && !f.around {
				// A group every path takes is captured before what follows.
				if f.num != 0 {
					p.lastCaptured = f.num
				} else if !f.alternatives && f.lastCaptured != 0 {
					p.lastCaptured = f.lastCaptured
				}
			}
			if p := stack[len(stack)-1]; !f.around {
				// What the parent's body has of the captures in it.
				p.hasCapture = p.hasCapture || f.num != 0 || f.hasCapture
				p.hasMissing = p.hasMissing || f.hasMissing ||
					f.num != 0 && f.optional || f.hasCapture && (f.optional || f.alternatives)
			}
			i = atom(i+1, f.consumes)
		case '|':
			top := stack[len(stack)-1]
			top.allConsumed = top.allConsumed && top.altConsumed
			top.altConsumed = false
			top.lastCaptured = 0
			top.alternatives = true
			i++
		case '^', '$':
			i = atom(i+1, false)
		case '*', '+', '?':
			// A quantifier after nothing, or Annex B's quantified lookahead.
			i++
		case '{':
			if _, _, length, ok := quant(i); ok {
				i += length
				continue
			}
			i = atom(i+1, true)
		default:
			_, w := utf8.DecodeRuneInString(s[i:])
			i = atom(i+w, true)
		}
	}
	if len(stack) > 1 {
		// Refused (an unterminated group).
		return nil
	}
	// The frames in the order they open have their parents before them.
	markers, work := 0, 0
	for _, f := range frames {
		p := f.parent
		f.deepestBad = p.deepestBad
		if f.optional || f.around || f.alternatives {
			f.deepestBad = f.depth
		}
		f.markerAbove, f.markers = p.markerAbove, p.markers
		if p.marker {
			f.markerAbove, f.markers = p, p.markers+1
		}
		if f.num != 0 && f.markerAbove != nil {
			work += f.markers
		}
		if f.marker {
			markers++
		}
	}
	if markers == 0 || work > maxIterationWork {
		return nil
	}
	// The groups in order of where they open: a hidden group opens where
	// its loop's body starts, before a capturing group opening there.
	type open struct {
		pos    int
		hidden int // 0 for a hidden group, 1 for a capturing one
		frame  *iterFrame
	}
	opens := make([]open, 0, captures+markers)
	for _, f := range frames {
		if f.hidden {
			opens = append(opens, open{f.bodyStart, 0, f})
		}
		if f.num != 0 {
			opens = append(opens, open{f.open, 1, f})
		}
	}
	sort.SliceStable(opens, func(a, b int) bool {
		if opens[a].pos != opens[b].pos {
			return opens[a].pos < opens[b].pos
		}
		return opens[a].hidden < opens[b].hidden
	})
	// The capturing groups backreferences are to (a number past them is a
	// legacy octal escape).
	for _, name := range refNames {
		if num, ok := names[name]; ok {
			refNums = append(refNums, num)
		}
	}
	referenced := make([]bool, captures+1)
	for _, num := range refNums {
		if num <= captures {
			referenced[num] = true
		}
	}
	plan := &iterPlan{hidden: make(map[int]bool), renumber: make([]int, captures+1)}
	plan.Groups = make([]IterationGroup, len(opens)+1)
	plan.Groups[0] = IterationGroup{Loop: -1, Marks: -1}
	groupOf := make(map[*iterFrame]int, markers) // a loop's group
	for k, o := range opens {
		num := k + 1
		plan.Groups[num] = IterationGroup{Consumed: o.frame.consumedBelow, Around: o.frame.aroundBelow, Hidden: o.hidden == 0, Loop: -1, Marks: -1}
		if o.hidden == 0 {
			plan.hidden[o.frame.open] = true
			plan.Hidden = append(plan.Hidden, num)
			groupOf[o.frame] = num
		} else {
			plan.renumber[o.frame.num] = num
			plan.Groups[num].Referenced = referenced[o.frame.num]
			if o.frame.marker {
				groupOf[o.frame] = num
			}
		}
	}
	// The groups in a loop's body are those that open before it closes.
	lastBefore := func(pos int) int {
		return sort.Search(len(opens), func(k int) bool { return opens[k].pos >= pos })
	}
	for _, f := range frames {
		if f.marker {
			f.loopIndex = len(plan.Loops)
			g := &plan.Groups[groupOf[f]]
			g.Marks = f.loopIndex
			if f.markerAbove != nil {
				g.Loop = f.markerAbove.loopIndex
			}
			parent := -1
			if f.markerAbove != nil {
				parent = f.markerAbove.loopIndex
			}
			reset := f.hasCapture && (f.alternatives || f.hasMissing)
			plan.Loops = append(plan.Loops, IterationLoop{
				Group: groupOf[f], Last: lastBefore(f.close), Min: f.min, Max: f.max, Depth: f.depth,
				Parent: parent, Alternatives: f.alternatives, Reset: reset,
			})
			plan.Drop = plan.Drop || reset || f.hidden
		}
		if f.num != 0 {
			g := &plan.Groups[plan.renumber[f.num]]
			if f.capParent != nil {
				g.Parent = plan.renumber[f.capParent.num]
			}
			if f.capturedBefore != 0 {
				g.Before = plan.renumber[f.capturedBefore]
			}
		}
		if f.num != 0 && f.markerAbove != nil {
			plan.Groups[plan.renumber[f.num]].Loop = f.markerAbove.loopIndex
			missing := f.parent.deepestBad
			if f.optional {
				missing = f.depth
			}
			plan.Captures = append(plan.Captures, IterationCapture{
				Group: plan.renumber[f.num], Loop: f.markerAbove.loopIndex, Missing: missing,
			})
		}
	}
	return plan
}

// groupNumber returns what capturing group num (from 1) is numbered once the
// hidden groups are in.
func (self *_RegExp_parser) groupNumber(num int) int {
	if self.iter == nil || num >= len(self.iter.renumber) {
		return num
	}
	return self.iter.renumber[num]
}

// leadingUnboundedLoop reports whether the pattern may start with an
// unbounded loop of a set of characters, perhaps in groups, and has a
// bounded repeat of variable length or an alternative: regexp2 then looks
// for a match by the "landmarks" after the loop, and takes each as often
// as it can (or the first alternative that matches), so that it finds none
// when a landmark's last character is the next one's (/[^\w]*?[ab]{1,2}b/
// found nothing in "ab"). A lookahead that always succeeds before the loop
// stops it from doing so (see regexp2NoLandmarks). Without such a repeat or
// alternative the landmarks are literals and fixed repeats, which it finds
// as they are (a pattern like /[\w.]+@[\w.]+\.\w+/ keeps the search).
func (self *_RegExp_parser) leadingUnboundedLoop() bool {
	s := self.str
	n := len(s)
	variable := false
	for i := 0; i < n && !variable; i++ {
		switch s[i] {
		case '\\':
			i++
		case '|':
			variable = true
		case '{':
			j := i + 1
			lo := j
			for j < n && s[j] >= '0' && s[j] <= '9' {
				j++
			}
			if j == lo || j >= n || s[j] != ',' {
				continue
			}
			j++
			hi := j
			for j < n && s[j] >= '0' && s[j] <= '9' {
				j++
			}
			variable = j < n && s[j] == '}' && (j == hi || s[lo:hi-1] != s[hi:j])
		}
	}
	if !variable {
		return false
	}
	i := 0
	for i < n && s[i] == '(' {
		switch {
		case strings.HasPrefix(s[i:], "(?=)") || strings.HasPrefix(s[i:], "(?:)"):
			// regexp2 drops these.
			i += 4
		case strings.HasPrefix(s[i:], "(?<=)"):
			i += 5
		case i+2 < n && s[i+1] == '?' && s[i+2] == ':':
			i += 3
		case i+3 < n && s[i+1] == '?' && s[i+2] == '<' && s[i+3] != '=' && s[i+3] != '!':
			j := strings.IndexByte(s[i:], '>')
			if j < 0 {
				return false
			}
			i += j + 1
		case i+1 < n && s[i+1] == '?':
			return false
		default:
			i++
		}
	}
	if i >= n {
		return false
	}
	switch c := s[i]; {
	case c == '[':
		i++
		for i < n && s[i] != ']' {
			if s[i] == '\\' {
				i++
			}
			i++
		}
		i++
	case c == '\\':
		if i+1 >= n || s[i+1] >= '0' && s[i+1] <= '9' || strings.IndexByte("bBk", s[i+1]) >= 0 {
			return false
		}
		i += 2
		if i < n && s[i] == '{' {
			for i < n && s[i] != '}' {
				i++
			}
			i++
		}
	case strings.IndexByte("()|^$*+?{}", c) >= 0:
		return false
	default:
		_, w := utf8.DecodeRuneInString(s[i:])
		i += w
	}
	// The loop may be of the groups the atom is in.
	for i < n && s[i] == ')' {
		i++
	}
	if i >= n {
		return false
	}
	if s[i] == '*' || s[i] == '+' {
		return true
	}
	if s[i] != '{' {
		return false
	}
	j := i + 1
	for j < n && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	return j > i+1 && j+1 < n && s[j] == ',' && s[j+1] == '}'
}
