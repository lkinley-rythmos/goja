package goja

import (
	"math/big"

	"github.com/dop251/goja/token"
)

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

// Folding is limited to small values: an operation is left to run time,
// where it gives the same value or throws the same error, when one of its
// operands or its result could be longer than these. Without the limit,
// folding 3n ** 100000000n took the compile more than 10 s and
// 1n << 100000000000n asked for 12.5 GB. With it, each fold costs little, and
// the constants a program keeps are a few times its source at most:
// 2n ** 1023n, 9 characters, is 128 bytes.
const (
	maxFoldBigIntBits   = 1024
	maxFoldStringLength = 1024
)

// foldOperandFits reports whether v is small enough to be an operand of a
// fold.
func foldOperandFits(v Value) bool {
	switch v := v.(type) {
	case String:
		return v.Length() <= maxFoldStringLength
	case *valueBigInt:
		return (*big.Int)(v).BitLen() <= maxFoldBigIntBits
	}
	return true
}

// foldsUnary reports whether a unary operation on a constant operand is
// folded.
func (c *compiler) foldsUnary(operand compiledExpr) bool {
	v, ex := c.evalConst(operand)
	return ex != nil || foldOperandFits(v)
}

// foldsBinary reports whether a binary operation on constant operands is
// folded. When an operand throws, the operation throws what it throws,
// which is folded.
func (c *compiler) foldsBinary(op token.Token, left, right compiledExpr) bool {
	l, ex := c.evalConst(left)
	if ex != nil {
		return true
	}
	r, ex := c.evalConst(right)
	if ex != nil {
		return true
	}
	if !foldOperandFits(l) || !foldOperandFits(r) {
		return false
	}
	_, ls := l.(String)
	_, rs := r.(String)
	if op == token.PLUS && (ls || rs) {
		return foldStringLength(l)+foldStringLength(r) <= maxFoldStringLength
	}
	if x, ok := l.(*valueBigInt); ok {
		if y, ok := r.(*valueBigInt); ok {
			return bigIntFoldBits(op, (*big.Int)(x), (*big.Int)(y)) <= maxFoldBigIntBits
		}
	}
	return true
}

// foldStringLength is an upper bound on the length of a constant operand of
// a fold as a string.
func foldStringLength(v Value) int {
	switch v := v.(type) {
	case String:
		return v.Length()
	case *valueBigInt:
		// Decimal digits, at most log10(2) (< 0.302) of the bits, and a sign.
		return (*big.Int)(v).BitLen()*302/1000 + 2
	}
	// A number ("-1.7976931348623157e+308"), a boolean, null or undefined.
	return 32
}

// bigIntFoldBits is an upper bound on the bit length of the result of the
// BigInt operation op on x and y, both no longer than maxFoldBigIntBits.
func bigIntFoldBits(op token.Token, x, y *big.Int) int {
	bx := x.BitLen()
	switch op {
	case token.PLUS, token.MINUS, token.AND, token.OR, token.EXCLUSIVE_OR:
		return max(bx, y.BitLen()) + 1
	case token.MULTIPLY:
		return bx + y.BitLen()
	case token.EXPONENT:
		if y.Sign() < 0 || bx <= 1 {
			// It throws, or x is -1n, 0n or 1n.
			return 1
		}
		if y.BitLen() > 16 {
			// x ** y is at least 2^y.
			return maxFoldBigIntBits + 1
		}
		return bx * int(y.Int64())
	case token.SHIFT_LEFT, token.SHIFT_RIGHT:
		if (y.Sign() > 0) != (op == token.SHIFT_LEFT) {
			// A shift to the right.
			return bx
		}
		if y.BitLen() > 16 {
			return maxFoldBigIntBits + 1
		}
		return bx + int(new(big.Int).Abs(y).Int64())
	}
	// Division, remainder, comparisons.
	return bx
}
