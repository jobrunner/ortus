package gocog

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

// TIFF constants
const (
	tiffMagicLE = 0x4949 // "II" little-endian
	tiffMagicBE = 0x4D4D // "MM" big-endian
	tiffVersion = 42
)

// Compression types
const (
	CompressionNone    = 1
	CompressionLZW     = 5
	CompressionJPEG    = 6
	CompressionDeflate = 8
)

// DataType represents the data type of pixels
type DataType uint16

const (
	DTByte      DataType = 1  // 8-bit unsigned integer
	DTASCII     DataType = 2  // 8-bit ASCII
	DTSShort    DataType = 3  // 16-bit unsigned integer
	DTSLong     DataType = 4  // 32-bit unsigned integer
	DTRational  DataType = 5  // Two longs: numerator, denominator
	DTSByte     DataType = 6  // 8-bit signed integer
	DTUndefined DataType = 7  // 8-bit undefined
	DTSShortS   DataType = 8  // 16-bit signed integer
	DTSLongS    DataType = 9  // 32-bit signed integer
	DTSRational DataType = 10 // Two signed longs
	DTFloat     DataType = 11 // 32-bit IEEE floating point
	DTDouble    DataType = 12 // 64-bit IEEE floating point
)

// Tag represents a TIFF tag
type Tag struct {
	ID       uint16
	Type     DataType
	Count    uint32
	Offset   uint32
	Value    interface{}
	IsOffset bool
}

// IFD represents an Image File Directory
type IFD struct {
	Tags      map[uint16]*Tag
	NextIFD   uint32
	ByteOrder binary.ByteOrder
}

// TIFFReader reads TIFF files
type TIFFReader struct {
	r         io.ReadSeeker
	byteOrder binary.ByteOrder
	ifds      []*IFD
}

// NewTIFFReader creates a new TIFF reader
func NewTIFFReader(r io.ReadSeeker) (*TIFFReader, error) {
	return NewTIFFReaderWithFilter(r, false, nil)
}

// NewTIFFReaderWithFilter creates a new TIFF reader with optional metadata-only filtering
func NewTIFFReaderWithFilter(r io.ReadSeeker, metadataOnly bool, allowedTags map[uint16]bool) (*TIFFReader, error) {
	tr := &TIFFReader{r: r}

	// Read TIFF header (8 bytes: magic + version + first IFD offset) in one go
	header := make([]byte, 8)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, fmt.Errorf("failed to read TIFF header: %w", err)
	}

	// Check magic (first 2 bytes) - try little-endian first
	magic := binary.LittleEndian.Uint16(header[0:2])
	if magic == tiffMagicLE {
		tr.byteOrder = binary.LittleEndian
	} else if magic == tiffMagicBE {
		tr.byteOrder = binary.BigEndian
	} else {
		return nil, fmt.Errorf("invalid TIFF magic: 0x%04x", magic)
	}

	// Read version (bytes 2-4)
	version := tr.byteOrder.Uint16(header[2:4])
	if version != tiffVersion {
		return nil, fmt.Errorf("invalid TIFF version: %d", version)
	}

	// Read first IFD offset (bytes 4-8)
	firstIFD := tr.byteOrder.Uint32(header[4:8])

	// Read all IFDs
	if err := tr.readIFDsWithFilter(firstIFD, metadataOnly, allowedTags); err != nil {
		return nil, fmt.Errorf("failed to read IFDs: %w", err)
	}

	return tr, nil
}

// readIFDsWithFilter reads all IFDs with optional metadata-only filtering
func (tr *TIFFReader) readIFDsWithFilter(offset uint32, metadataOnly bool, allowedTags map[uint16]bool) error {
	currentOffset := offset

	for currentOffset != 0 {
		ifd, err := tr.readIFDWithFilter(currentOffset, metadataOnly, allowedTags)
		if err != nil {
			return err
		}

		tr.ifds = append(tr.ifds, ifd)
		currentOffset = ifd.NextIFD
	}

	return nil
}

// readIFDWithFilter reads a single IFD with optional metadata-only filtering
func (tr *TIFFReader) readIFDWithFilter(offset uint32, metadataOnly bool, allowedTags map[uint16]bool) (*IFD, error) {
	if _, err := tr.r.Seek(int64(offset), io.SeekStart); err != nil {
		return nil, fmt.Errorf("failed to seek to IFD: %w", err)
	}

	// Read tag count (2 bytes)
	var tagCount uint16
	if err := binary.Read(tr.r, tr.byteOrder, &tagCount); err != nil {
		return nil, fmt.Errorf("failed to read tag count: %w", err)
	}

	// Read entire IFD structure in one go to minimize HTTP requests
	// Structure: tag count (2 bytes) + tag entries (12 bytes each) + next IFD offset (4 bytes)
	ifdSize := 2 + int(tagCount)*12 + 4
	ifdBuffer := make([]byte, ifdSize-2) // Already read tag count, so subtract 2
	if _, err := io.ReadFull(tr.r, ifdBuffer); err != nil {
		return nil, fmt.Errorf("failed to read IFD structure: %w", err)
	}

	// Parse tag entries from buffer
	bufReader := &bytesReader{
		data:      ifdBuffer,
		offset:    0,
		byteOrder: tr.byteOrder,
	}

	ifd := &IFD{
		Tags:      make(map[uint16]*Tag),
		ByteOrder: tr.byteOrder,
	}

	// Parse tags from buffer
	for i := uint16(0); i < tagCount; i++ {
		tag, err := tr.readTagFromBuffer(bufReader)
		if err != nil {
			return nil, fmt.Errorf("failed to read tag %d: %w", i, err)
		}
		ifd.Tags[tag.ID] = tag
	}

	// Read next IFD offset from end of buffer
	nextIFDOffset := len(ifdBuffer) - 4
	ifd.NextIFD = tr.byteOrder.Uint32(ifdBuffer[nextIFDOffset : nextIFDOffset+4])

	// Read tag values that are stored inline or at offsets
	if metadataOnly {
		if err := tr.readTagValuesMetadataOnly(ifd, allowedTags, offset); err != nil {
			return nil, err
		}
	} else {
		if err := tr.readTagValues(ifd); err != nil {
			return nil, err
		}
	}

	return ifd, nil
}

// readTag reads a single tag entry
func (tr *TIFFReader) readTag() (*Tag, error) {
	tag := &Tag{}

	if err := binary.Read(tr.r, tr.byteOrder, &tag.ID); err != nil {
		return nil, err
	}

	var typeVal uint16
	if err := binary.Read(tr.r, tr.byteOrder, &typeVal); err != nil {
		return nil, err
	}
	tag.Type = DataType(typeVal)

	if err := binary.Read(tr.r, tr.byteOrder, &tag.Count); err != nil {
		return nil, err
	}

	if err := binary.Read(tr.r, tr.byteOrder, &tag.Offset); err != nil {
		return nil, err
	}

	return tag, nil
}

// bytesReader is a helper to read from a byte buffer
type bytesReader struct {
	data      []byte
	offset    int
	byteOrder binary.ByteOrder
}

// readTagFromBuffer reads a tag entry from a buffer
func (tr *TIFFReader) readTagFromBuffer(br *bytesReader) (*Tag, error) {
	if br.offset+12 > len(br.data) {
		return nil, fmt.Errorf("buffer too small for tag entry")
	}

	tag := &Tag{}
	tag.ID = br.byteOrder.Uint16(br.data[br.offset : br.offset+2])
	tag.Type = DataType(br.byteOrder.Uint16(br.data[br.offset+2 : br.offset+4]))
	tag.Count = br.byteOrder.Uint32(br.data[br.offset+4 : br.offset+8])
	tag.Offset = br.byteOrder.Uint32(br.data[br.offset+8 : br.offset+12])
	br.offset += 12

	return tag, nil
}

// Tag IDs for large data arrays that should be skipped during metadata reading.
// These arrays can be very large (thousands of entries) for big COG files and
// are not needed for reading metadata. They are loaded on-demand when reading pixel data.
const (
	TagStripOffsets    = 273
	TagStripByteCounts = 279
	TagTileOffsets     = 324
	TagTileByteCounts  = 325
)

// readTagValues reads the actual values for tags, skipping large data arrays.
// This optimization avoids reading TileOffsets, TileByteCounts, StripOffsets,
// and StripByteCounts arrays during metadata parsing, which can be very large
// for big COG files (>600MB). These arrays are loaded on-demand via ReadTagValue
// when actually needed for reading pixel data.
func (tr *TIFFReader) readTagValues(ifd *IFD) error {
	return tr.readTagValuesWithFilter(ifd, true)
}

// readTagValuesMetadataOnly reads all tags (except large data arrays) for metadata extraction.
// Optimized to use a single 16K read to avoid many small HTTP range requests.
func (tr *TIFFReader) readTagValuesMetadataOnly(ifd *IFD, allowedTags map[uint16]bool, ifdOffset uint32) error {
	const bufferSize = 16 * 1024 // 16K buffer

	// Read 16K buffer starting from IFD offset
	buffer := make([]byte, bufferSize)
	originalPos, _ := tr.r.Seek(0, io.SeekCurrent)

	// Seek to IFD offset and read 16K
	if _, err := tr.r.Seek(int64(ifdOffset), io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek to IFD offset: %w", err)
	}

	n, err := io.ReadFull(tr.r, buffer)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		tr.r.Seek(originalPos, io.SeekStart)
		return fmt.Errorf("failed to read buffer: %w", err)
	}
	buffer = buffer[:n]

	// Restore original position
	tr.r.Seek(originalPos, io.SeekStart)

	// Create a reader from the buffer for parsing
	bufReader := &bufferReader{
		data:       buffer,
		offset:     0,
		baseOffset: int64(ifdOffset),
		byteOrder:  tr.byteOrder,
	}

	// Process all tags
	for _, tag := range ifd.Tags {
		// Always skip large data arrays (they're loaded on-demand)
		if tag.ID == TagStripOffsets || tag.ID == TagStripByteCounts ||
			tag.ID == TagTileOffsets || tag.ID == TagTileByteCounts {
			tag.IsOffset = true
			continue
		}

		valueSize := tag.getTypeSize() * tag.Count

		if valueSize <= 4 {
			// Value is stored inline - read from tag.Offset field
			tag.Value = tr.readInlineValue(tag)
			tag.IsOffset = false
		} else {
			// Value is stored at offset - try to read from buffer if it's within range
			tagOffset := int64(tag.Offset)
			bufferStart := int64(ifdOffset)
			bufferEnd := bufferStart + int64(len(buffer))

			if tagOffset >= bufferStart && tagOffset+int64(valueSize) <= bufferEnd {
				// Value is within the buffer - read from buffer
				relativeOffset := tagOffset - bufferStart
				tag.Value = tr.readValueFromBuffer(bufReader, tag, relativeOffset)
				tag.IsOffset = false
			} else {
				// Value is outside buffer - mark as offset for lazy loading
				tag.IsOffset = true
			}
		}
	}

	return nil
}

// bufferReader is a helper to read from a byte buffer with binary decoding
type bufferReader struct {
	data       []byte
	offset     int64
	baseOffset int64
	byteOrder  binary.ByteOrder
}

// readValueFromBuffer reads a tag value from the buffer at the given relative offset
func (tr *TIFFReader) readValueFromBuffer(br *bufferReader, tag *Tag, relativeOffset int64) interface{} {
	// Save current offset
	oldOffset := br.offset
	br.offset = relativeOffset

	// Read value based on type
	var value interface{}
	switch tag.Type {
	case DTByte:
		if tag.Count == 1 {
			value = br.data[br.offset]
		} else {
			values := make([]uint8, tag.Count)
			copy(values, br.data[br.offset:br.offset+int64(tag.Count)])
			value = values
		}
	case DTSShort:
		if tag.Count == 1 {
			value = br.byteOrder.Uint16(br.data[br.offset : br.offset+2])
		} else {
			values := make([]uint16, tag.Count)
			for i := uint32(0); i < tag.Count; i++ {
				values[i] = br.byteOrder.Uint16(br.data[br.offset+int64(i*2) : br.offset+int64(i*2)+2])
			}
			value = values
		}
	case DTSLong:
		if tag.Count == 1 {
			value = br.byteOrder.Uint32(br.data[br.offset : br.offset+4])
		} else {
			values := make([]uint32, tag.Count)
			for i := uint32(0); i < tag.Count; i++ {
				values[i] = br.byteOrder.Uint32(br.data[br.offset+int64(i*4) : br.offset+int64(i*4)+4])
			}
			value = values
		}
	case DTSShortS:
		if tag.Count == 1 {
			value = int16(br.byteOrder.Uint16(br.data[br.offset : br.offset+2]))
		} else {
			values := make([]int16, tag.Count)
			for i := uint32(0); i < tag.Count; i++ {
				values[i] = int16(br.byteOrder.Uint16(br.data[br.offset+int64(i*2) : br.offset+int64(i*2)+2]))
			}
			value = values
		}
	case DTSLongS:
		if tag.Count == 1 {
			value = int32(br.byteOrder.Uint32(br.data[br.offset : br.offset+4]))
		} else {
			values := make([]int32, tag.Count)
			for i := uint32(0); i < tag.Count; i++ {
				values[i] = int32(br.byteOrder.Uint32(br.data[br.offset+int64(i*4) : br.offset+int64(i*4)+4]))
			}
			value = values
		}
	case DTFloat:
		if tag.Count == 1 {
			bits := br.byteOrder.Uint32(br.data[br.offset : br.offset+4])
			value = math.Float32frombits(bits)
		} else {
			values := make([]float32, tag.Count)
			for i := uint32(0); i < tag.Count; i++ {
				bits := br.byteOrder.Uint32(br.data[br.offset+int64(i*4) : br.offset+int64(i*4)+4])
				values[i] = math.Float32frombits(bits)
			}
			value = values
		}
	case DTDouble:
		if tag.Count == 1 {
			bits := br.byteOrder.Uint64(br.data[br.offset : br.offset+8])
			value = math.Float64frombits(bits)
		} else {
			values := make([]float64, tag.Count)
			for i := uint32(0); i < tag.Count; i++ {
				bits := br.byteOrder.Uint64(br.data[br.offset+int64(i*8) : br.offset+int64(i*8)+8])
				values[i] = math.Float64frombits(bits)
			}
			value = values
		}
	case DTRational:
		if tag.Count == 1 {
			num := br.byteOrder.Uint32(br.data[br.offset : br.offset+4])
			den := br.byteOrder.Uint32(br.data[br.offset+4 : br.offset+8])
			value = [2]uint32{num, den}
		} else {
			values := make([][2]uint32, tag.Count)
			for i := uint32(0); i < tag.Count; i++ {
				offset := br.offset + int64(i*8)
				values[i][0] = br.byteOrder.Uint32(br.data[offset : offset+4])
				values[i][1] = br.byteOrder.Uint32(br.data[offset+4 : offset+8])
			}
			value = values
		}
	case DTSRational:
		if tag.Count == 1 {
			num := int32(br.byteOrder.Uint32(br.data[br.offset : br.offset+4]))
			den := int32(br.byteOrder.Uint32(br.data[br.offset+4 : br.offset+8]))
			value = [2]int32{num, den}
		} else {
			values := make([][2]int32, tag.Count)
			for i := uint32(0); i < tag.Count; i++ {
				offset := br.offset + int64(i*8)
				values[i][0] = int32(br.byteOrder.Uint32(br.data[offset : offset+4]))
				values[i][1] = int32(br.byteOrder.Uint32(br.data[offset+4 : offset+8]))
			}
			value = values
		}
	case DTASCII:
		buf := make([]byte, tag.Count)
		copy(buf, br.data[br.offset:br.offset+int64(tag.Count)])
		// Remove null terminator
		if len(buf) > 0 && buf[len(buf)-1] == 0 {
			buf = buf[:len(buf)-1]
		}
		value = string(buf)
	default:
		value = nil
	}

	// Restore offset
	br.offset = oldOffset

	return value
}

// readTagValuesWithFilter reads tag values, optionally skipping large data arrays
func (tr *TIFFReader) readTagValuesWithFilter(ifd *IFD, skipLargeArrays bool) error {
	for _, tag := range ifd.Tags {
		// Skip large data arrays during metadata reading
		if skipLargeArrays {
			if tag.ID == TagStripOffsets || tag.ID == TagStripByteCounts ||
				tag.ID == TagTileOffsets || tag.ID == TagTileByteCounts {
				// Mark as offset but don't read the value yet
				tag.IsOffset = true
				continue
			}
		}

		valueSize := tag.getTypeSize() * tag.Count

		if valueSize <= 4 {
			// Value is stored inline in the offset field
			tag.Value = tr.readInlineValue(tag)
			tag.IsOffset = false
		} else {
			// Value is stored at the offset
			tag.IsOffset = true
			oldPos, _ := tr.r.Seek(0, io.SeekCurrent)

			if _, err := tr.r.Seek(int64(tag.Offset), io.SeekStart); err != nil {
				return fmt.Errorf("failed to seek to tag value: %w", err)
			}

			tag.Value = tr.readValueAtOffset(tag)

			tr.r.Seek(oldPos, io.SeekStart)
		}
	}

	return nil
}

// ReadTagValue reads a specific tag value on-demand (for lazy loading)
func (tr *TIFFReader) ReadTagValue(ifd *IFD, tagID uint16) error {
	tag, ok := ifd.Tags[tagID]
	if !ok {
		return fmt.Errorf("tag %d not found", tagID)
	}

	// If already loaded, return
	if tag.Value != nil {
		return nil
	}

	valueSize := tag.getTypeSize() * tag.Count

	if valueSize <= 4 {
		// Value is stored inline in the offset field
		tag.Value = tr.readInlineValue(tag)
		tag.IsOffset = false
	} else {
		// Value is stored at the offset
		oldPos, _ := tr.r.Seek(0, io.SeekCurrent)

		if _, err := tr.r.Seek(int64(tag.Offset), io.SeekStart); err != nil {
			return fmt.Errorf("failed to seek to tag value: %w", err)
		}

		tag.Value = tr.readValueAtOffset(tag)

		tr.r.Seek(oldPos, io.SeekStart)
	}

	return nil
}

// getTypeSize returns the size in bytes of a data type
func (t *Tag) getTypeSize() uint32 {
	switch t.Type {
	case DTByte, DTASCII, DTSByte, DTUndefined:
		return 1
	case DTSShort, DTSShortS:
		return 2
	case DTSLong, DTSLongS, DTFloat:
		return 4
	case DTRational, DTSRational, DTDouble:
		return 8
	default:
		return 1
	}
}

// readInlineValue reads a value stored inline (in the offset field)
func (tr *TIFFReader) readInlineValue(tag *Tag) interface{} {
	switch tag.Type {
	case DTByte:
		if tag.Count == 1 {
			return uint8(tag.Offset)
		}
		return []uint8{uint8(tag.Offset)}
	case DTSShort:
		if tag.Count == 1 {
			return uint16(tag.Offset)
		}
		return []uint16{uint16(tag.Offset)}
	case DTSLong:
		if tag.Count == 1 {
			return tag.Offset
		}
		return []uint32{tag.Offset}
	case DTSShortS:
		if tag.Count == 1 {
			return int16(tag.Offset)
		}
		return []int16{int16(tag.Offset)}
	case DTSLongS:
		if tag.Count == 1 {
			return int32(tag.Offset)
		}
		return []int32{int32(tag.Offset)}
	default:
		return tag.Offset
	}
}

// readValueAtOffset reads a value stored at an offset
func (tr *TIFFReader) readValueAtOffset(tag *Tag) interface{} {
	switch tag.Type {
	case DTByte:
		values := make([]uint8, tag.Count)
		binary.Read(tr.r, tr.byteOrder, values)
		if tag.Count == 1 {
			return values[0]
		}
		return values
	case DTSShort:
		values := make([]uint16, tag.Count)
		binary.Read(tr.r, tr.byteOrder, values)
		if tag.Count == 1 {
			return values[0]
		}
		return values
	case DTSLong:
		values := make([]uint32, tag.Count)
		binary.Read(tr.r, tr.byteOrder, values)
		if tag.Count == 1 {
			return values[0]
		}
		return values
	case DTSShortS:
		values := make([]int16, tag.Count)
		binary.Read(tr.r, tr.byteOrder, values)
		if tag.Count == 1 {
			return values[0]
		}
		return values
	case DTSLongS:
		values := make([]int32, tag.Count)
		binary.Read(tr.r, tr.byteOrder, values)
		if tag.Count == 1 {
			return values[0]
		}
		return values
	case DTFloat:
		values := make([]float32, tag.Count)
		binary.Read(tr.r, tr.byteOrder, values)
		if tag.Count == 1 {
			return values[0]
		}
		return values
	case DTDouble:
		values := make([]float64, tag.Count)
		binary.Read(tr.r, tr.byteOrder, values)
		if tag.Count == 1 {
			return values[0]
		}
		return values
	case DTRational:
		values := make([][2]uint32, tag.Count)
		for i := uint32(0); i < tag.Count; i++ {
			binary.Read(tr.r, tr.byteOrder, &values[i][0])
			binary.Read(tr.r, tr.byteOrder, &values[i][1])
		}
		if tag.Count == 1 {
			return values[0]
		}
		return values
	case DTSRational:
		values := make([][2]int32, tag.Count)
		for i := uint32(0); i < tag.Count; i++ {
			binary.Read(tr.r, tr.byteOrder, &values[i][0])
			binary.Read(tr.r, tr.byteOrder, &values[i][1])
		}
		if tag.Count == 1 {
			return values[0]
		}
		return values
	case DTASCII:
		buf := make([]byte, tag.Count)
		binary.Read(tr.r, tr.byteOrder, buf)
		// Remove null terminator
		if len(buf) > 0 && buf[len(buf)-1] == 0 {
			buf = buf[:len(buf)-1]
		}
		return string(buf)
	default:
		return nil
	}
}

// GetIFD returns the IFD at the specified index (0 = main image)
func (tr *TIFFReader) GetIFD(index int) *IFD {
	if index < 0 || index >= len(tr.ifds) {
		return nil
	}
	return tr.ifds[index]
}

// IFDCount returns the number of IFDs (main image + overviews)
func (tr *TIFFReader) IFDCount() int {
	return len(tr.ifds)
}
