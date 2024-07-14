package pdb

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"

	"github.com/RichieSams/pdb/util"
)

var (
	MSFMagic = []byte{'M', 'i', 'c', 'r', 'o', 's', 'o', 'f', 't', ' ', 'C', '/', 'C', '+', '+', ' ', 'M', 'S', 'F', ' ', '7', '.', '0', '0', '\r', '\n', 0x1A, 0x44, 0x53, 0x00, 0x00, 0x00}
)

type msfSuperBlock struct {
	BlockSize         uint32
	FreeBlockMapBlock uint32
	NumBlocks         uint32
	NumDirectoryBytes uint32
	Unknown           uint32
	BlockMapAddr      uint32
}

type streamInfo struct {
	Size   uint32
	Blocks []uint32
}

type msfFile struct {
	File os.File

	SuperBlock           msfSuperBlock
	StreamDirectoryBytes []byte
	FreeBlockMap         *util.BitVector

	Streams []streamInfo
}

func (f *msfFile) Close() error {
	return f.File.Close()
}

func parseMSFFile(filePath string) (*msfFile, error) {
	diskFile, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open %s - %w", filePath, err)
	}

	file := &msfFile{
		File:    *diskFile,
		Streams: []streamInfo{},
	}

	err = func() error {
		// Check the magic bytes
		magic := make([]byte, len(MSFMagic))
		if _, err := io.ReadFull(diskFile, magic); err != nil {
			return fmt.Errorf("failed to read magic bytes from %s - %w", filePath, err)
		}

		if !bytes.Equal(MSFMagic, magic) {
			return fmt.Errorf("%s is not a valid PDB file - Magic bytes do not match", filePath)
		}

		// Read the superblock
		if err := binary.Read(diskFile, binary.LittleEndian, &file.SuperBlock); err != nil {
			return fmt.Errorf("failed to read superblock from %s - %w", filePath, err)
		}

		// Read the free block map
		freeBlockMapNeededBytes := (file.SuperBlock.NumBlocks + 8 - 1) / 8
		// Round to the next multiple of block size
		freeBlockMapBytes := make([]byte, ((freeBlockMapNeededBytes+file.SuperBlock.BlockSize-1)/file.SuperBlock.BlockSize)*file.SuperBlock.BlockSize)

		for i := uint32(0); i*8 < file.SuperBlock.NumBlocks; i += file.SuperBlock.BlockSize {
			offset := int64(file.SuperBlock.BlockSize) * int64(i+file.SuperBlock.FreeBlockMapBlock)
			if _, err := diskFile.Seek(offset, io.SeekStart); err != nil {
				return fmt.Errorf("failed to seek to free block map for %s - %w", filePath, err)
			}

			if _, err := io.ReadFull(diskFile, freeBlockMapBytes[i:(i+1)]); err != nil {
				return fmt.Errorf("failed to read free block map for %s - %w", filePath, err)
			}
		}

		file.FreeBlockMap = util.NewBitVector(freeBlockMapBytes, int(file.SuperBlock.BlockSize))

		// Read the directory stream block map
		blockMapOffset := file.SuperBlock.BlockMapAddr * file.SuperBlock.BlockSize
		if _, err := diskFile.Seek(int64(blockMapOffset), io.SeekStart); err != nil {
			return fmt.Errorf("failed to seek to stream block map for %s - %w", filePath, err)
		}

		numBlocks := (file.SuperBlock.NumDirectoryBytes + file.SuperBlock.BlockSize - 1) / file.SuperBlock.BlockSize
		blocks := []uint32{}
		for i := uint32(0); i < numBlocks; i++ {
			var block uint32
			if err := binary.Read(diskFile, binary.LittleEndian, &block); err != nil {
				return fmt.Errorf("failed to read stream block index for %s - %w", filePath, err)
			}
			blocks = append(blocks, block)
		}

		// Read the stream directory
		file.StreamDirectoryBytes, err = io.ReadAll(newMSFStreamReader(diskFile, file.SuperBlock.BlockSize, blocks, file.SuperBlock.NumDirectoryBytes))
		if err != nil {
			return fmt.Errorf("failed to read stream directory data for %s - %w", filePath, err)
		}

		// And use it to figure out all the blocks for the other streams
		streamDirectoryReader := bytes.NewReader(file.StreamDirectoryBytes)

		var numStreams uint32
		if err := binary.Read(streamDirectoryReader, binary.LittleEndian, &numStreams); err != nil {
			return fmt.Errorf("failed to read stream directory from %s - %w", filePath, err)
		}
		streamSizes := []uint32{}
		for i := uint32(0); i < numStreams; i++ {
			var streamSize uint32
			if err := binary.Read(streamDirectoryReader, binary.LittleEndian, &streamSize); err != nil {
				return fmt.Errorf("failed to read stream directory from %s - %w", filePath, err)
			}
			streamSizes = append(streamSizes, streamSize)
		}

		for i := uint32(0); i < numStreams; i++ {
			blockIndices := []uint32{}

			numBlocks := (streamSizes[i] + file.SuperBlock.BlockSize - 1) / file.SuperBlock.BlockSize
			for j := uint32(0); j < numBlocks; j++ {
				var blockIndex uint32
				if err := binary.Read(streamDirectoryReader, binary.LittleEndian, &blockIndex); err != nil {
					return fmt.Errorf("failed to read stream directory from %s - %w", filePath, err)
				}
				blockIndices = append(blockIndices, blockIndex)
			}

			file.Streams = append(file.Streams, streamInfo{
				Size:   streamSizes[i],
				Blocks: blockIndices,
			})
		}

		return nil
	}()

	if err != nil {
		if closeErr := diskFile.Close(); closeErr != nil {
			return nil, fmt.Errorf("%w - while handling that error, failed to close file - %v", err, closeErr)
		}

		return nil, err
	}

	return file, nil
}

func newMSFStreamReader(file *os.File, blockSize uint32, streamBlocks []uint32, streamSize uint32) util.SizeReadSeeker {
	// Fast out
	if streamSize == 0 || streamSize == 0xffffffff {
		return bytes.NewReader([]byte{})
	}

	readers := []util.SizeReaderAt{}
	// Add the readers for all the blocks except the last one
	for i := 0; i < len(streamBlocks)-1; i++ {
		readers = append(readers, io.NewSectionReader(file, int64(blockSize)*int64(streamBlocks[i]), int64(blockSize)))
	}
	// Add the last block
	lastIndex := streamBlocks[len(streamBlocks)-1]
	lastBlockSize := int64(streamSize % blockSize)
	// If the last block is exactly equal to the block size, be sure to use that
	if lastBlockSize == 0 {
		lastBlockSize = int64(blockSize)
	}

	readers = append(readers, io.NewSectionReader(file, int64(blockSize)*int64(lastIndex), lastBlockSize))

	multiReader := util.NewMultiReaderAt(readers...)
	return io.NewSectionReader(multiReader, 0, multiReader.Size())
}
