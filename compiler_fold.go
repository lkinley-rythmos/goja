package goja

// foldMemo is what an expression that may be constant remembers of its
// constant evaluation. Without it, a constant logical expression evaluated
// its left operand once to decide whether it is constant and again to emit
// it, and each of those evaluated the operand's own left operand twice, so
// that 3n || 0n || ... of 26 terms took 2^26 steps. With it, each expression
// is decided and evaluated at most once.
type foldMemo struct {
	decided, isConst bool

	evaluated bool
	val       Value
	ex        *Exception
}

func (m *foldMemo) memo() *foldMemo {
	return m
}

type foldMemoizer interface {
	memo() *foldMemo
}

// decide returns whether the expression is constant, deciding it with f
// the first time.
func (m *foldMemo) decide(f func() bool) bool {
	if !m.decided {
		m.isConst = f()
		m.decided = true
	}
	return m.isConst
}

func memoOf(expr compiledExpr) *foldMemo {
	if e, ok := expr.(foldMemoizer); ok {
		return e.memo()
	}
	return nil
}

// releaseConst forgets the value of a constant expression once it has been
// emitted: its parent, which asked for it, has what it needs, so that the
// values of a long chain of folded operations are not all kept until the
// end of the compile. Nothing asks for it again; if something did, it would
// be evaluated again, to the same value.
func (c *compiler) releaseConst(expr compiledExpr) {
	if m := memoOf(expr); m != nil {
		m.evaluated, m.val, m.ex = false, nil, nil
	}
}
