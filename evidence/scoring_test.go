package evidence

import (
	"errors"
	"math"
	"testing"
)

func TestMulDiv(t *testing.T) {
	if v, err := MulDiv(6, 7, 2); err != nil || v != 21 {
		t.Fatalf("MulDiv(6,7,2) = %d, %v", v, err)
	}
	if _, err := MulDiv(1, 2, 0); !errors.Is(err, ErrDivideByZero) {
		t.Fatalf("expected divide by zero, got %v", err)
	}
	if _, err := MulDiv(math.MaxInt64, 2, 1); !errors.Is(err, ErrOverflow) {
		t.Fatalf("expected overflow, got %v", err)
	}
}

func TestDeriveSlope(t *testing.T) {
	// 40.00 -> 45.00 C (500 centiC) over 50 min => 100 milli/min.
	if v, err := DeriveSlope(4000, 4500, 0, 50); err != nil || v != 100 {
		t.Fatalf("DeriveSlope = %d, %v", v, err)
	}
	if _, err := DeriveSlope(4000, 4500, 60, 60); !errors.Is(err, ErrInvalidDuration) {
		t.Fatalf("expected invalid duration, got %v", err)
	}
}

func TestPercentOf(t *testing.T) {
	// 5 out of 100 at scale 2 => 500 (5.00%).
	if v, err := PercentOf(5, 100, 2); err != nil || v != 500 {
		t.Fatalf("PercentOf = %d, %v", v, err)
	}
	if _, err := PercentOf(5, 0, 2); !errors.Is(err, ErrDivideByZero) {
		t.Fatalf("expected divide by zero, got %v", err)
	}
	if _, err := PercentOf(-1, 100, 2); !errors.Is(err, ErrNegativeCount) {
		t.Fatalf("expected negative count, got %v", err)
	}
}

func TestCheckDigits(t *testing.T) {
	if err := CheckDigits(12345, 5); err != nil {
		t.Fatalf("expected ok, got %v", err)
	}
	if err := CheckDigits(123456, 5); !errors.Is(err, ErrTooManyDigits) {
		t.Fatalf("expected too many digits, got %v", err)
	}
	if err := CheckDigits(-99999, 5); err != nil {
		t.Fatalf("expected negative within range, got %v", err)
	}
}
