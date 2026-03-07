package api

import (
	"fmt"
	"math"
)

// IntConverter は int から int32/uint32/uint64 への安全な変換を提供する。
// 複数の変換をまとめて行い、最初のエラーを保持する。
//
// 使用例:
//
//	conv := NewIntConverter()
//	val1 := conv.Int32(someInt)
//	val2 := conv.Uint32(anotherInt)
//	if conv.Err() != nil { return conv.Err() }
type IntConverter struct {
	err error
}

// NewIntConverter は新しい IntConverter を返す。
func NewIntConverter() *IntConverter {
	return &IntConverter{}
}

// Err は蓄積されたエラーを返す。
func (c *IntConverter) Err() error {
	return c.err
}

// Int32 は int を int32 に安全に変換する。
func (c *IntConverter) Int32(v int) int32 {
	if c.err != nil {
		return 0
	}
	if v < math.MinInt32 || v > math.MaxInt32 {
		c.err = fmt.Errorf("integer overflow: %d は int32 の範囲外です", v)
		return 0
	}
	return int32(v)
}

// Uint32 は int を uint32 に安全に変換する。
func (c *IntConverter) Uint32(v int) uint32 {
	if c.err != nil {
		return 0
	}
	if v < 0 || v > math.MaxUint32 {
		c.err = fmt.Errorf("integer overflow: %d は uint32 の範囲外です", v)
		return 0
	}
	return uint32(v)
}

// Uint64 は int を uint64 に安全に変換する。
func (c *IntConverter) Uint64(v int) uint64 {
	if c.err != nil {
		return 0
	}
	if v < 0 {
		c.err = fmt.Errorf("integer overflow: %d は負の値のため uint64 に変換できません", v)
		return 0
	}
	return uint64(v)
}
