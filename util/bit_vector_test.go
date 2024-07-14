package util

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBitVectorGet(t *testing.T) {
	bv := NewBitVector([]byte{0b01000000}, 4)

	require.True(t, bv.Get(6))
	require.False(t, bv.Get(1))
}

func TestBitVectorGetOutOfBoundsReturnsFalse(t *testing.T) {
	bv := NewBitVector([]byte{0b01000000}, 4)

	require.True(t, bv.Get(6))
	require.False(t, bv.Get(1))

	require.False(t, bv.Get(33))
}

func TestBitVectorSet(t *testing.T) {
	bv := NewBitVector([]byte{0b01000000}, 4)

	bv.Set(3, true)
	require.True(t, bv.Get(3))
	require.False(t, bv.Get(5))

	require.True(t, bv.Get(6))
	require.False(t, bv.Get(1))
}

func TestBitVectorSetAutoGrow(t *testing.T) {
	bv := NewBitVector([]byte{0b01000000}, 4)

	// If we have to grow, it will grow in multiples of the block size
	// Even if we don't initially start at the multiple
	bv.Set(9, true)
	require.True(t, bv.Get(9))

	require.Len(t, bv.data, 4)

	// If we grow again, it will add another block size worth of bytes
	// Even if we set to zero
	bv.Set(33, false)
	require.False(t, bv.Get(33))

	require.Len(t, bv.data, 8)
}

func TestBitVectorSetFromEmpty(t *testing.T) {
	bv := NewBitVector(nil, 4)

	bv.Set(0, true)
	require.True(t, bv.Get(0))
}
