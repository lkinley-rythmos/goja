package parser

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// ECMAScript resets the captures of a quantified group's body at each of its
// iterations (RepeatMatcher, ECMA-262 22.2.2.3.1): /(?:(a)|b)+/.exec("ab") is
// ["ab", undefined]. Go's regexp and regexp2 keep a capture from an earlier
// iteration. So the body of a quantified group that may iterate more than
// once and has capturing groups starts with a hidden empty capturing group,
// whose last capture is where the last iteration started; a capture that
// starts before it is from an earlier iteration, and one that starts after it
// from the last. An empty capture right at it is from the last iteration
// unless something the body always consumes comes before the group
// (/(?:a(b?)|c)+/ on "ac"). The caller drops the captures from earlier
// iterations, and the hidden groups (see IterationGroup).

// IterationGroup is the hidden group at the start of the body of a
// quantified group, in the transformed patterns' numbering, with the
// capturing groups of the body.
type IterationGroup struct {
	Hidden int
	Inner  []IterationCapture
}

// IterationCapture is a capturing group in the body of a quantified group
// that may be missing from an iteration: its number in the transformed
// patterns, and whether the body always consumes something before it. A
// group in every iteration, and one in a lookaround within the body (whose
// captures need not be where it is), are left as the engines give them.
type IterationCapture struct {
	Group          int
	ConsumedBefore bool
}

// Iterations are the hidden groups of a transformed pattern: those at the
// start of quantified groups' bodies, and their numbers, in order.
type Iterations struct {
	Groups []IterationGroup
	Hidden []int
}

// iterPlan is where hidden groups go, and the groups' numbers once they
// are in.
type iterPlan struct {
	// The offsets of the quantified groups' '(', and whether their bodies
	// have alternatives, which a group of their own must keep apart from
	// the hidden group.
	hidden   map[int]bool
	renumber []int // a capturing group's number (from 1) once they are in
	Iterations
}

// iterFrame is a group planIterations is in.
type iterFrame struct {
	open, bodyStart int
	capture, around bool
	alternatives    bool // the body has a | of its own
	optional        bool // quantified with a minimum of 0 (known once closed)
	altConsumed     bool // the current alternative consumes something so far
	allConsumed     bool // every alternative before the current one did
	caps            []iterCapture
}

// iterCapture is a capturing group in a group's body: its original
// number, whether the body always consumes something before it, whether it
// is in a lookaround within the body, and the groups from the body to it
// (itself last), which tell whether each iteration has it.
type iterCapture struct {
	num            int
	consumedBefore bool
	inAround       bool
	chain          []*iterFrame
}

// everyIteration reports whether a capturing group is in every iteration of
// a body without alternatives: none of the groups from the body to it has
// alternatives, and none (nor it) is optional. Its last capture is then
// always from the last iteration.
func (c *iterCapture) everyIteration() bool {
	for i, f := range c.chain {
		if f.optional || f.around || f.alternatives && i < len(c.chain)-1 {
			return false
		}
	}
	return true
}

// maxIterationWork bounds the notes planIterations takes, a capture for
// each group it is in (1,000 groups nested with 1,000 captures).
const maxIterationWork = 1 << 20

// planIterations finds the quantified groups that need a hidden group, in a
// pattern that parses (its result is not used otherwise).
func (self *_RegExp_parser) planIterations() *iterPlan {
	type capRec = iterCapture
	type frame = iterFrame
	s := self.str
	n := len(s)
	stack := []*frame{{allConsumed: true}}
	capOpens := []int{0} // by original number, from 1
	type q struct {
		open, bodyStart int
		alternatives    bool
		caps            []capRec
	}
	var qs []q
	work := 0
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
				for end < n && s[end] >= '0' && s[end] <= '9' {
					end++
				}
			case e == 'k' && end < n && s[end] == '<':
				consumes = false
				for end < n && s[end] != '>' {
					end++
				}
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
			f := &frame{open: i, allConsumed: true}
			j := i + 1
			switch {
			case j+1 < n && s[j] == '?' && (s[j+1] == '=' || s[j+1] == '!'):
				f.around, j = true, j+2
			case j+2 < n && s[j] == '?' && s[j+1] == '<' && (s[j+2] == '=' || s[j+2] == '!'):
				f.around, j = true, j+3
			case j+1 < n && s[j] == '?' && s[j+1] == '<':
				f.capture = true
				for j < n && s[j] != '>' {
					j++
				}
				j++
			case j+1 < n && s[j] == '?' && s[j+1] == ':':
				j += 2
			default:
				f.capture = true
			}
			f.bodyStart = min(j, n)
			if len(stack) > maxGroupNesting {
				// Refused anyway (see scanGroup).
				return nil
			}
			if f.capture {
				num := len(capOpens)
				capOpens = append(capOpens, i)
				consumed, around := false, false
				// A capture is noted for each group it is in: the work is
				// bounded, past which captures are left as the engines
				// give them.
				if work += len(stack); work > maxIterationWork {
					return nil
				}
				chain := append(stack[:len(stack):len(stack)], f)
				for k := len(stack) - 1; k >= 1; k-- {
					consumed = consumed || stack[k].altConsumed
					stack[k].caps = append(stack[k].caps, capRec{num, consumed, around, chain[k+1:]})
					around = around || stack[k].around
				}
			}
			stack = append(stack, f)
			i = f.bodyStart
		case ')':
			if len(stack) == 1 {
				i++
				continue
			}
			f := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			consumes := f.allConsumed && f.altConsumed && !f.around
			min, max, _, ok := quant(i + 1)
			f.optional = ok && min == 0
			if ok && (max < 0 || max > 1) && !f.around && len(f.caps) > 0 {
				qs = append(qs, q{f.open, f.bodyStart, f.alternatives, f.caps})
			}
			i = atom(i+1, consumes)
		case '|':
			top := stack[len(stack)-1]
			top.allConsumed = top.allConsumed && top.altConsumed
			top.altConsumed = false
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
	// A group needs a hidden group if a capturing group in it (not in a
	// lookaround) may be missing from an iteration.
	kept := qs[:0]
	for _, g := range qs {
		var caps []capRec
		for _, c := range g.caps {
			if !c.inAround && (g.alternatives || !c.everyIteration()) {
				caps = append(caps, c)
			}
		}
		if len(caps) > 0 {
			g.caps = caps
			kept = append(kept, g)
		}
	}
	qs = kept
	if len(qs) == 0 {
		return nil
	}
	// The groups in order of where they open: a hidden group opens where
	// the body starts, before a capturing group opening there.
	type open struct {
		pos, hidden int // hidden: 0 for a hidden group, 1 for a capturing one
		index       int // into qs, or the original number
	}
	var opens []open
	for k, g := range qs {
		opens = append(opens, open{g.bodyStart, 0, k})
	}
	for num := 1; num < len(capOpens); num++ {
		opens = append(opens, open{capOpens[num], 1, num})
	}
	sort.Slice(opens, func(a, b int) bool {
		if opens[a].pos != opens[b].pos {
			return opens[a].pos < opens[b].pos
		}
		return opens[a].hidden < opens[b].hidden
	})
	plan := &iterPlan{hidden: make(map[int]bool), renumber: make([]int, len(capOpens))}
	hiddenNum := make([]int, len(qs))
	for k, o := range opens {
		if o.hidden == 0 {
			hiddenNum[o.index] = k + 1
		} else {
			plan.renumber[o.index] = k + 1
		}
	}
	for k, g := range qs {
		plan.hidden[g.open] = g.alternatives
		ig := IterationGroup{Hidden: hiddenNum[k]}
		for _, c := range g.caps {
			ig.Inner = append(ig.Inner, IterationCapture{Group: plan.renumber[c.num], ConsumedBefore: c.consumedBefore})
		}
		plan.Groups = append(plan.Groups, ig)
		plan.Hidden = append(plan.Hidden, hiddenNum[k])
	}
	sort.Ints(plan.Hidden)
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
