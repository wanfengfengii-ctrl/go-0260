package evidence

import (
	"errors"
	"math"
)

var (
	// ErrInvalidDuration reports a non-positive duration delta used to derive a
	// temperature slope.
	ErrInvalidDuration = errors.New("evidence: duration delta must be positive")
	// ErrTooManyDigits reports an integer whose magnitude exceeds the allowed
	// digit length for a fixed-decimal reading.
	ErrTooManyDigits = errors.New("evidence: integer has too many digits")
)

// MulDiv returns (a*b)/c, checking intermediate overflow and a zero divisor.
// It is the building block for slope and percentage derivation.
func MulDiv(a, b, c int64) (int64, error) {
	if c == 0 {
		return 0, ErrDivideByZero
	}
	if a != 0 && b != 0 {
		if a > math.MaxInt64/b || a < math.MinInt64/b {
			return 0, ErrOverflow
		}
	}
	return (a * b) / c, nil
}

// DeriveSlope computes the temperature slope in milli-degrees Celsius per
// minute between two consecutive coverage readings. Temperatures are supplied
// in centi-degrees (10^-2 C) and durations in minutes, so the derived slope is
// (currTemp - prevTemp) * 10 / (currDur - prevDur).
func DeriveSlope(prevTempCentiC, currTempCentiC int64, prevDurMinutes, currDurMinutes int64) (int64, error) {
	dt := currDurMinutes - prevDurMinutes
	if dt <= 0 {
		return 0, ErrInvalidDuration
	}
	dTemp := currTempCentiC - prevTempCentiC
	// dTemp is in centi-degrees; multiply by 10 to convert to milli-degrees
	// before dividing by the minute delta.
	return MulDiv(dTemp, 10, dt)
}

// PercentOf computes count/total expressed as a percentage and scaled by an
// extra 10^scale digits. For a fixed scale of 2, 5 out of 100 yields 500
// (i.e. 5.00%). It checks a zero total and intermediate overflow.
func PercentOf(count, total int64, scale int) (int64, error) {
	if total <= 0 {
		return 0, ErrDivideByZero
	}
	f, err := ScaleFactor(scale)
	if err != nil {
		return 0, err
	}
	if count < 0 {
		return 0, ErrNegativeCount
	}
	// percent = count * 100 * 10^scale / total.
	num, err := MulDiv(count, 100, 1)
	if err != nil {
		return 0, err
	}
	return MulDiv(num, f, total)
}

// CheckDigits reports whether the magnitude of v fits within maxDigits decimal
// digits. Zero is always accepted.
func CheckDigits(v int64, maxDigits int) error {
	if maxDigits < 1 {
		return ErrTooManyDigits
	}
	if v < 0 {
		v = -v
	}
	limit := int64(1)
	for i := 0; i < maxDigits; i++ {
		if limit > math.MaxInt64/10 {
			return ErrTooManyDigits
		}
		limit *= 10
	}
	if v >= limit {
		return ErrTooManyDigits
	}
	return nil
}
