package util

type BitVector struct {
	data      []byte
	blockSize int
}

func NewBitVector(initialData []byte, blockSize int) *BitVector {
	if initialData == nil {
		initialData = []byte{}
	}

	return &BitVector{
		data:      initialData,
		blockSize: blockSize,
	}
}

func (bv *BitVector) Get(i int) bool {
	// We assume host LittleEndian
	// The bit field is indexed LittleEndian
	//
	// Bits
	// 1 0 0 1 1 1 1 0
	//
	// Index
	// 7 6 5 4 3 2 1 0

	byteIndex := i >> 3
	if byteIndex >= len(bv.data) {
		return false
	}

	b := bv.data[byteIndex]

	byteOffset := uint32(i % 8)
	return (1<<byteOffset)&b != 0
}

func (bv *BitVector) Set(i int, value bool) {
	byteIndex := i >> 3
	if byteIndex >= len(bv.data) {
		bv.grow(i)
	}

	oldByte := bv.data[byteIndex]

	byteOffset := uint32(i % 8)

	var newByte byte
	if value {
		// Set the byteOffset'th bit
		newByte = oldByte | (1 << byteOffset)
	} else {
		// Reset the byteOffset'th bit
		mask := byte(^(1 << byteOffset))
		newByte = oldByte & mask
	}

	bv.data[byteIndex] = newByte
}

func (bv *BitVector) grow(i int) {
	numBytesNeeded := (i + 8 - 1) / 8

	numBlocks := (numBytesNeeded + bv.blockSize - 1) / bv.blockSize
	if numBlocks == 0 {
		numBlocks = 1
	}

	numBytes := numBlocks * bv.blockSize

	newData := make([]byte, numBytes)
	copy(newData, bv.data)

	bv.data = newData
}

func (bv *BitVector) Bytes() []byte {
	return bv.data
}
