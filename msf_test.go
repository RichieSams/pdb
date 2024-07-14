package pdb

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/RichieSams/pdb/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMSFParsing(t *testing.T) {
	file, err := parseMSFFile("test_data/simplehttp-test.pdb")
	require.NoError(t, err)

	bitmap := util.NewBitVector(nil, int(file.SuperBlock.BlockSize))
	maxBitsForFBM := int((file.SuperBlock.NumBlocks+file.SuperBlock.BlockSize-1)/file.SuperBlock.BlockSize) * int(file.SuperBlock.BlockSize) * 8
	for i := 0; i < maxBitsForFBM; i++ {
		bitmap.Set(i, true)
	}

	for _, stream := range file.Streams {
		for _, block := range stream.Blocks {
			bitmap.Set(int(block), false)
		}
	}

	// Mark the superblock
	bitmap.Set(0, false)

	// Mark the free block map blocks
	for i := uint32(0); i < file.SuperBlock.NumBlocks; i += file.SuperBlock.BlockSize {
		bitmap.Set(int(i)+1, false)
		bitmap.Set(int(i)+2, false)
	}

	// Mark the stream directory blocks
	numBlocks := (file.SuperBlock.NumDirectoryBytes + file.SuperBlock.BlockSize - 1) / file.SuperBlock.BlockSize
	for i := uint32(0); i < numBlocks; i++ {
		bitmap.Set(int(i+file.SuperBlock.BlockMapAddr), false)
	}

	err = os.WriteFile("freeMapData-calculated", bitmap.Bytes(), 0666)
	require.NoError(t, err)

	originalFile, err := os.Open("test_data/simplehttp-test.pdb")
	require.NoError(t, err)
	defer func() {
		err := originalFile.Close()
		require.NoError(t, err)
	}()

	_, err = originalFile.Seek(int64(file.SuperBlock.FreeBlockMapBlock*file.SuperBlock.BlockSize), io.SeekStart)
	require.NoError(t, err)

	data := make([]byte, file.SuperBlock.BlockSize)
	_, err = io.ReadFull(originalFile, data)
	require.NoError(t, err)

	err = os.WriteFile("freeMapData-orig", data, 0666)
	require.NoError(t, err)
}

func TestFreeBlockMap(t *testing.T) {
	dirEntries, err := os.ReadDir("test_data")
	require.NoError(t, err)

	for _, entry := range dirEntries {
		t.Run(entry.Name(), func(t *testing.T) {
			file, err := parseMSFFile(filepath.Join("test_data", entry.Name()))
			require.NoError(t, err)

			for i, stream := range file.Streams {
				for _, block := range stream.Blocks {
					byteIndex := block >> 3
					bitOffset := block % 8

					assert.False(t, file.FreeBlockMap.Get(int(block)), fmt.Sprintf("Stream %d contains block %d but the FBM says it's free - byte %0x bit %d", i, block, byteIndex, bitOffset))
				}
			}
		})
	}
}
