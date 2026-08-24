package evidence

import (
	"errors"
	"math"
	"testing"
)

func TestScaleFactor(t *testing.T) {
	if f, err := ScaleFactor(2); err != nil || f != 100 {
		t.Fatalf("ScaleFactor(2) = %d, %v", f, err)
	}
	if _, err := ScaleFactor(-1); !errors.Is(err, ErrInvalidScale) {
		t.Fatalf("expected ErrInvalidScale, got %v", err)
	}
	if _, err := ScaleFactor(19); !errors.Is(err, ErrInvalidScale) {
		t.Fatalf("expected ErrInvalidScale, got %v", err)
	}
}

func TestAddSubOverflow(t *testing.T) {
	if _, err := Add(math.MaxInt64, 1); !errors.Is(err, ErrOverflow) {
		t.Fatalf("expected overflow, got %v", err)
	}
	if _, err := Sub(math.MinInt64, 1); !errors.Is(err, ErrOverflow) {
		t.Fatalf("expected overflow, got %v", err)
	}
	if v, err := Add(2, 3); err != nil || v != 5 {
		t.Fatalf("Add(2,3) = %d, %v", v, err)
	}
	if v, err := Sub(5, 3); err != nil || v != 2 {
		t.Fatalf("Sub(5,3) = %d, %v", v, err)
	}
}

func TestMulDivScaled(t *testing.T) {
	// 1.50 * 2.00 scaled by 10^2 = 3.00 -> 300.
	if v, err := MulScaled(150, 200, 2); err != nil || v != 300 {
		t.Fatalf("MulScaled = %d, %v", v, err)
	}
	// 3.00 / 2.00 scaled by 10^2 = 1.50 -> 150.
	if v, err := DivScaled(300, 200, 2); err != nil || v != 150 {
		t.Fatalf("DivScaled = %d, %v", v, err)
	}
	if _, err := DivScaled(100, 0, 2); !errors.Is(err, ErrDivideByZero) {
		t.Fatalf("expected divide by zero, got %v", err)
	}
	if _, err := MulScaled(math.MaxInt64, 2, 0); !errors.Is(err, ErrOverflow) {
		t.Fatalf("expected overflow, got %v", err)
	}
}

func TestConserveGrains(t *testing.T) {
	if err := ConserveGrains(100, 10, 5, 2, 3, 80); err != nil {
		t.Fatalf("expected conservation, got %v", err)
	}
	if err := ConserveGrains(100, 10, 5, 2, 3, 81); !errors.Is(err, ErrGrainMismatch) {
		t.Fatalf("expected mismatch, got %v", err)
	}
	if err := ConserveGrains(100, -1, 5, 2, 3, 91); !errors.Is(err, ErrNegativeCount) {
		t.Fatalf("expected negative count, got %v", err)
	}
}
