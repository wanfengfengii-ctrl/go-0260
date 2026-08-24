// Package evidence holds the fixed-decimal integer arithmetic and the
// immutable evidence-chain records produced during the joint inspection.
package evidence

import (
	"errors"
	"math"
)

var (
	// ErrOverflow reports an out-of-range fixed-decimal result.
	ErrOverflow = errors.New("evidence: fixed-point overflow")
	// ErrDivideByZero reports division by a zero denominator.
	ErrDivideByZero = errors.New("evidence: divide by zero")
	// ErrInvalidScale reports a scale outside the supported range.
	ErrInvalidScale = errors.New("evidence: invalid fixed scale")
	// ErrNegativeCount reports a negative count where none is allowed.
	ErrNegativeCount = errors.New("evidence: negative count")
	// ErrGrainMismatch reports cut-test counts that violate bean conservation.
	ErrGrainMismatch = errors.New("evidence: cut-test grain counts do not match sample size")
)

// ScaleFactor returns 10^scale for scale in [0, 18].
func ScaleFactor(scale int) (int64, error) {
	if scale < 0 || scale > 18 {
		return 0, ErrInvalidScale
	}
	f := int64(1)
	for i := 0; i < scale; i++ {
		f *= 10
	}
	return f, nil
}

// Add returns a+b, reporting overflow.
func Add(a, b int64) (int64, error) {
	r := a + b
	if (r > a) != (b > 0) {
		return 0, ErrOverflow
	}
	return r, nil
}

// Sub returns a-b, reporting overflow.
func Sub(a, b int64) (int64, error) {
	r := a - b
	if (r < a) != (b > 0) {
		return 0, ErrOverflow
	}
	return r, nil
}

// MulScaled returns (a*b)/10^scale, checking intermediate overflow. Both
// operands are integers already scaled by 10^scale.
func MulScaled(a, b int64, scale int) (int64, error) {
	f, err := ScaleFactor(scale)
	if err != nil {
		return 0, err
	}
	if a != 0 && b != 0 {
		if a > math.MaxInt64/b || a < math.MinInt64/b {
			return 0, ErrOverflow
		}
	}
	return (a * b) / f, nil
}

// DivScaled returns (a*10^scale)/b, checking divide-by-zero and intermediate
// overflow. Both operands are integers already scaled by 10^scale.
func DivScaled(a, b int64, scale int) (int64, error) {
	f, err := ScaleFactor(scale)
	if err != nil {
		return 0, err
	}
	if b == 0 {
		return 0, ErrDivideByZero
	}
	if a != 0 {
		if a > math.MaxInt64/f || a < math.MinInt64/f {
			return 0, ErrOverflow
		}
	}
	return (a * f) / b, nil
}

// ConserveGrains verifies cut-test bean conservation: the five categories must
// be non-negative and sum exactly to the locked sample size.
func ConserveGrains(sampleSize, underfermented, purple, moldy, insect, good int64) error {
	for _, n := range []int64{sampleSize, underfermented, purple, moldy, insect, good} {
		if n < 0 {
			return ErrNegativeCount
		}
	}
	total := underfermented
	var err error
	for _, n := range []int64{purple, moldy, insect, good} {
		total, err = Add(total, n)
		if err != nil {
			return ErrOverflow
		}
	}
	if total != sampleSize {
		return ErrGrainMismatch
	}
	return nil
}
