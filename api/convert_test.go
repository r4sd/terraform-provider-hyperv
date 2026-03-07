package api

import (
	"math"
	"testing"
)

func TestInt32_Normal(t *testing.T) {
	conv := NewIntConverter()
	result := conv.Int32(42)
	if conv.Err() != nil {
		t.Fatalf("unexpected error: %v", conv.Err())
	}
	if result != 42 {
		t.Errorf("expected 42, got %d", result)
	}
}

func TestInt32_Boundary(t *testing.T) {
	conv := NewIntConverter()
	maxResult := conv.Int32(math.MaxInt32)
	minResult := conv.Int32(math.MinInt32)
	if conv.Err() != nil {
		t.Fatalf("unexpected error at boundary: %v", conv.Err())
	}
	if maxResult != math.MaxInt32 {
		t.Errorf("expected MaxInt32, got %d", maxResult)
	}
	if minResult != math.MinInt32 {
		t.Errorf("expected MinInt32, got %d", minResult)
	}
}

func TestInt32_Overflow(t *testing.T) {
	conv := NewIntConverter()
	result := conv.Int32(math.MaxInt32 + 1)
	if conv.Err() == nil {
		t.Fatal("expected error for overflow, got nil")
	}
	if result != 0 {
		t.Errorf("expected 0 on error, got %d", result)
	}
}

func TestInt32_Underflow(t *testing.T) {
	conv := NewIntConverter()
	result := conv.Int32(math.MinInt32 - 1)
	if conv.Err() == nil {
		t.Fatal("expected error for underflow, got nil")
	}
	if result != 0 {
		t.Errorf("expected 0 on error, got %d", result)
	}
}

func TestUint32_Normal(t *testing.T) {
	conv := NewIntConverter()
	result := conv.Uint32(100)
	if conv.Err() != nil {
		t.Fatalf("unexpected error: %v", conv.Err())
	}
	if result != 100 {
		t.Errorf("expected 100, got %d", result)
	}
}

func TestUint32_Boundary(t *testing.T) {
	conv := NewIntConverter()
	result := conv.Uint32(math.MaxUint32)
	if conv.Err() != nil {
		t.Fatalf("unexpected error at boundary: %v", conv.Err())
	}
	if result != math.MaxUint32 {
		t.Errorf("expected MaxUint32, got %d", result)
	}
}

func TestUint32_Negative(t *testing.T) {
	conv := NewIntConverter()
	result := conv.Uint32(-1)
	if conv.Err() == nil {
		t.Fatal("expected error for negative value, got nil")
	}
	if result != 0 {
		t.Errorf("expected 0 on error, got %d", result)
	}
}

func TestUint64_Normal(t *testing.T) {
	conv := NewIntConverter()
	result := conv.Uint64(999999)
	if conv.Err() != nil {
		t.Fatalf("unexpected error: %v", conv.Err())
	}
	if result != 999999 {
		t.Errorf("expected 999999, got %d", result)
	}
}

func TestUint64_Zero(t *testing.T) {
	conv := NewIntConverter()
	result := conv.Uint64(0)
	if conv.Err() != nil {
		t.Fatalf("unexpected error: %v", conv.Err())
	}
	if result != 0 {
		t.Errorf("expected 0, got %d", result)
	}
}

func TestUint64_Negative(t *testing.T) {
	conv := NewIntConverter()
	result := conv.Uint64(-1)
	if conv.Err() == nil {
		t.Fatal("expected error for negative value, got nil")
	}
	if result != 0 {
		t.Errorf("expected 0 on error, got %d", result)
	}
}

func TestErrorAccumulation_FirstErrorPreserved(t *testing.T) {
	conv := NewIntConverter()
	_ = conv.Int32(math.MaxInt32 + 1) // overflow
	_ = conv.Uint32(100)              // valid but should be skipped
	_ = conv.Uint64(200)              // valid but should be skipped

	if conv.Err() == nil {
		t.Fatal("expected error to be preserved")
	}
	// エラーメッセージは最初のエラー（Int32 overflow）のもの
	if result := conv.Uint32(50); result != 0 {
		t.Errorf("expected 0 after error, got %d", result)
	}
}

func TestErrorAccumulation_NoErrorWhenAllValid(t *testing.T) {
	conv := NewIntConverter()
	v1 := conv.Int32(10)
	v2 := conv.Uint32(20)
	v3 := conv.Uint64(30)

	if conv.Err() != nil {
		t.Fatalf("unexpected error: %v", conv.Err())
	}
	if v1 != 10 || v2 != 20 || v3 != 30 {
		t.Errorf("unexpected values: %d, %d, %d", v1, v2, v3)
	}
}
