package pdb

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/sirupsen/logrus"
)

func parsePDBFile(log *logrus.Logger, filePath string, sourceIndexBehavior SourceIndexBehavior) (SymbolFile, error) {
	file, closer, err := parseMSFFile(log, filePath)
	if err != nil {
		return nil, err
	}

	output, err := func() (SymbolFile, error) {
		// Read the first part of the GUID from the PDB stream
		var pdbHeader pdbStreamHeader
		if err := binary.Read(file.Streams[pdbStreamIndex], binary.LittleEndian, &pdbHeader); err != nil {
			return nil, fmt.Errorf("Failed to read PDB header information from %s - %w", filePath, err)
		}

		guidD1 := binary.LittleEndian.Uint32(pdbHeader.UniqueId[0:4])
		guidD2 := binary.LittleEndian.Uint16(pdbHeader.UniqueId[4:6])
		guidD3 := binary.LittleEndian.Uint16(pdbHeader.UniqueId[6:8])
		guidD4 := pdbHeader.UniqueId[8:16]

		// The final GUID is the guid D1-4 plus the Age, all hex encoded
		// There *is* an `Age` field in the pdbHeader
		// However, MS tools use the Age from the DBI stream block
		// So, we do the same

		// That said, some PDBs don't *have* DBI streams (or rather, they're empty)
		// In that case, the `Age` portion of the header is left off

		// The source file paths are stored in the DBI stream as well

		var guid string
		sourceFilePaths := []string{}

		if file.Streams[pdbDBIStreamIndex].Size() == 0 {
			guid = fmt.Sprintf("%08X%04X%04X%X", guidD1, guidD2, guidD3, guidD4)
		} else {
			dbiInfo, err := parseDBIStream(file.Streams[pdbDBIStreamIndex])
			if err != nil {
				return nil, fmt.Errorf("Failed to read DBI stream from %s - %w", filePath, err)
			}

			guid = fmt.Sprintf("%08X%04X%04X%X%x", guidD1, guidD2, guidD3, guidD4, dbiInfo.Age)
			sourceFilePaths = dbiInfo.FilePaths
		}

		return &pdbSymbolFile{
			filePath:        filePath,
			guid:            guid,
			copiedPath:      "",
			sourceFilePaths: sourceFilePaths,
		}, nil
	}()

	if closeErr := closer(); closeErr != nil {
		if err != nil {
			return nil, fmt.Errorf("%w - While handling that error, failed to close file - %v", err, closeErr)
		}
		return nil, fmt.Errorf("Failed to close %s - %w", filePath, closeErr)
	}

	return output, err
}

type pdbStreamHeader struct {
	Version   uint32
	Signature uint32
	Age       uint32
	UniqueId  [16]byte
}

type dbiStreamHeader struct {
	VersionSignature        int32
	VersionHeader           uint32
	Age                     uint32
	GlobalStreamIndex       uint16
	BuildNumber             uint16
	PublicStreamIndex       uint16
	PdbDllVersion           uint16
	SymRecordStream         uint16
	PdbDllRbld              uint16
	ModInfoSize             int32
	SectionContributionSize int32
	SectionMapSize          int32
	SourceInfoSize          int32
	TypeServerMapSize       int32
	MFCTypeServerIndex      uint32
	OptionalDbgHeaderSize   int32
	ECSubstreamSize         int32
	Flags                   uint16
	Machine                 uint16
	Padding                 uint32
}

type dbiInfo struct {
	Age       uint32
	FilePaths []string
}

func parseDBIStream(stream io.ReadSeeker) (*dbiInfo, error) {
	var header dbiStreamHeader
	if err := binary.Read(stream, binary.LittleEndian, &header); err != nil {
		return nil, fmt.Errorf("Failed to read DBI header information - %w", err)
	}

	// Seek past the substreams we don't care about
	fileInfoSubstreamOffset, err := stream.Seek(int64(header.ModInfoSize)+int64(header.SectionContributionSize)+int64(header.SectionMapSize), io.SeekCurrent)
	if err != nil {
		return nil, fmt.Errorf("Failed to seek to DBI File Info Substream - %w", err)
	}

	// Now we read the File Info Substream
	// https://llvm.org/docs/PDB/DbiStream.html#file-info-substream

	var numModules uint16
	if err := binary.Read(stream, binary.LittleEndian, &numModules); err != nil {
		return nil, fmt.Errorf("Failed to read from DBI File Info Substream - %w", err)
	}

	// In theory this is supposed to contain the number of source files for which this substream contains information.
	// But that would present a problem in that the width of this field being 16-bits would prevent one from having
	// more than 64K source files in a program. In early versions of the file format, this seems to have been the case.
	// In order to support more than this, this field of the is simply ignored, and computed dynamically by summing up
	// the values of the ModFileCounts array (discussed below). In short, this value should be ignored.
	var numSourceFiles uint16
	if err := binary.Read(stream, binary.LittleEndian, &numSourceFiles); err != nil {
		return nil, fmt.Errorf("Failed to read from DBI File Info Substream - %w", err)
	}

	// Seek past ModIndices (uint16[numModules])
	if _, err := stream.Seek(int64(2)*int64(numModules), io.SeekCurrent); err != nil {
		return nil, fmt.Errorf("Failed to read from DBI File Info Substream - %w", err)
	}

	sourceFileCount := uint64(0)
	for i := uint16(0); i < numModules; i++ {
		var modFileCount uint16
		if err := binary.Read(stream, binary.LittleEndian, &modFileCount); err != nil {
			return nil, fmt.Errorf("Failed to read from DBI File Info Substream - %w", err)
		}
		sourceFileCount += uint64(modFileCount)
	}

	// TODO: Seek past FileNameOffsets (uint32[sourceFileCount]) instead of reading

	//fileNameOffsets := []int64{}
	for i := uint64(0); i < sourceFileCount; i++ {
		var offset uint32
		if err := binary.Read(stream, binary.LittleEndian, &offset); err != nil {
			return nil, fmt.Errorf("Failed to read from DBI File Info Substream - %w", err)
		}
		//fileNameOffsets = append(fileNameOffsets, int64(offset))
	}

	currentOffset, err := stream.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, fmt.Errorf("Failed to read from DBI File Info Substream - %w", err)
	}

	remainingData := header.SourceInfoSize - (int32(currentOffset) - int32(fileInfoSubstreamOffset))
	namesBuffer := make([]byte, remainingData)
	bytesRead, err := stream.Read(namesBuffer)
	if err != nil {
		return nil, fmt.Errorf("Failed to read from DBI File Info Substream - %w", err)
	}
	if bytesRead != int(remainingData) {
		return nil, fmt.Errorf("Failed to read from DBI File Info Substream - Expected to read %d bytes, but read %d instead", remainingData, bytesRead)
	}

	// Convert the buffer to strings
	// It's an array of null-terminated strings
	filePaths := []string{}
	for {
		i := bytes.IndexByte(namesBuffer, 0)
		if i == -1 {
			break
		}

		filePaths = append(filePaths, string(namesBuffer[0:i]))
		namesBuffer = namesBuffer[i+1:]
	}

	return &dbiInfo{
		Age:       header.Age,
		FilePaths: filePaths,
	}, nil
}
