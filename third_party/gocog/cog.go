package gocog

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"math"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/maptile"
	"github.com/valyala/fasthttp"
	"golang.org/x/image/tiff/lzw"
)

// COG represents a Cloud Optimized GeoTIFF
type COG struct {
	reader     io.ReadSeeker
	tiffReader *TIFFReader
	geoTIFFs   []*GeoTIFFReader
	metadata   []*GeoTIFFMetadata
}

// RasterData represents raster data read from a COG.
// Data is stored as a flat array in band-interleaved-by-pixel (BIP) format:
// index = y * Width * Bands + x * Bands + band
// This provides better cache locality and fewer allocations than nested slices.
type RasterData struct {
	Data   []uint64 // Flat array: [y * Width * Bands + x * Bands + band]
	Width  int
	Height int
	Bands  int
	Bounds orb.Bound
}

// At returns the value at the specified band, x, y coordinates.
// This is the primary accessor method for pixel values.
func (r *RasterData) At(band, x, y int) uint64 {
	if band < 0 || band >= r.Bands || x < 0 || x >= r.Width || y < 0 || y >= r.Height {
		return 0
	}
	return r.Data[y*r.Width*r.Bands+x*r.Bands+band]
}

// Set sets the value at the specified band, x, y coordinates.
func (r *RasterData) Set(band, x, y int, value uint64) {
	if band < 0 || band >= r.Bands || x < 0 || x >= r.Width || y < 0 || y >= r.Height {
		return
	}
	r.Data[y*r.Width*r.Bands+x*r.Bands+band] = value
}

// AtUnchecked returns the value without bounds checking (faster but unsafe).
// Only use when you're certain the coordinates are valid.
func (r *RasterData) AtUnchecked(band, x, y int) uint64 {
	return r.Data[y*r.Width*r.Bands+x*r.Bands+band]
}

// SetUnchecked sets the value without bounds checking (faster but unsafe).
func (r *RasterData) SetUnchecked(band, x, y int, value uint64) {
	r.Data[y*r.Width*r.Bands+x*r.Bands+band] = value
}

// Index returns the flat array index for the given band, x, y coordinates.
func (r *RasterData) Index(band, x, y int) int {
	return y*r.Width*r.Bands + x*r.Bands + band
}

// GetBand returns a slice of all pixel values for a single band.
// The returned slice is newly allocated.
func (r *RasterData) GetBand(band int) []uint64 {
	if band < 0 || band >= r.Bands {
		return nil
	}
	result := make([]uint64, r.Width*r.Height)
	for y := 0; y < r.Height; y++ {
		for x := 0; x < r.Width; x++ {
			result[y*r.Width+x] = r.AtUnchecked(band, x, y)
		}
	}
	return result
}

// GetPixel returns all band values for a single pixel.
func (r *RasterData) GetPixel(x, y int) []uint64 {
	if x < 0 || x >= r.Width || y < 0 || y >= r.Height {
		return nil
	}
	result := make([]uint64, r.Bands)
	baseIdx := y*r.Width*r.Bands + x*r.Bands
	copy(result, r.Data[baseIdx:baseIdx+r.Bands])
	return result
}

// TileInfo represents information about a tile
type TileInfo struct {
	X      int
	Y      int
	Width  int
	Height int
	Offset uint32
	Size   uint32
}

// Read reads a COG from an io.ReadSeeker
func Read(r io.ReadSeeker) (*COG, error) {
	tr, err := NewTIFFReader(r)
	if err != nil {
		return nil, fmt.Errorf("failed to create TIFF reader: %w", err)
	}

	cog := &COG{
		reader:     r,
		tiffReader: tr,
		geoTIFFs:   make([]*GeoTIFFReader, 0),
		metadata:   make([]*GeoTIFFMetadata, 0),
	}

	// Read metadata for all IFDs (main image + overviews)
	for i := 0; i < tr.IFDCount(); i++ {
		gtr := &GeoTIFFReader{
			tr: tr,
			metadata: &GeoTIFFMetadata{
				GeoKeys: make(map[uint16]interface{}),
			},
		}

		// Read metadata for this specific IFD
		if err := gtr.readMetadata(i); err != nil {
			return nil, fmt.Errorf("failed to read metadata for IFD %d: %w", i, err)
		}

		cog.geoTIFFs = append(cog.geoTIFFs, gtr)
		cog.metadata = append(cog.metadata, gtr.GetMetadata())
	}

	return cog, nil
}

// ReadFromURL reads a COG from a URL using HTTP range requests
func ReadFromURL(url string, client *fasthttp.Client) (*COG, error) {
	if client == nil {
		client = &fasthttp.Client{
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
		}
	}

	// Create a range reader
	rr := NewHTTPRangeReader(url, client)

	return Read(rr)
}

// Open opens a COG from a file path or URL, validates it can be opened,
// and reads only the metadata (not the full image data).
// It automatically detects whether the input is a URL (starts with http:// or https://)
// or a file path.
func Open(pathOrURL string, client *fasthttp.Client) (*COG, error) {
	var reader io.ReadSeeker

	// Detect if it's a URL or file path
	if strings.HasPrefix(pathOrURL, "http://") || strings.HasPrefix(pathOrURL, "https://") {
		// It's a URL - use HTTP range reader
		if client == nil {
			client = &fasthttp.Client{
				ReadTimeout:  30 * time.Second,
				WriteTimeout: 30 * time.Second,
			}
		}
		reader = NewHTTPRangeReader(pathOrURL, client)
	} else {
		// It's a file path - open and validate the file can be opened
		file, err := os.Open(pathOrURL)
		if err != nil {
			return nil, fmt.Errorf("failed to open file: %w", err)
		}
		reader = file
	}

	// Create TIFF reader with metadata-only mode
	// Read all tags (not just a filtered subset) using a single 16K read
	tr, err := NewTIFFReaderWithFilter(reader, true, nil)
	if err != nil {
		if file, ok := reader.(*os.File); ok {
			file.Close()
		}
		return nil, fmt.Errorf("failed to validate COG: %w", err)
	}

	cog := &COG{
		reader:     reader,
		tiffReader: tr,
		geoTIFFs:   make([]*GeoTIFFReader, 0),
		metadata:   make([]*GeoTIFFMetadata, 0),
	}

	// Read metadata for all IFDs (main image + overviews)
	for i := 0; i < tr.IFDCount(); i++ {
		gtr := &GeoTIFFReader{
			tr: tr,
			metadata: &GeoTIFFMetadata{
				GeoKeys: make(map[uint16]interface{}),
			},
		}

		// Read metadata for this specific IFD
		if err := gtr.readMetadata(i); err != nil {
			if file, ok := reader.(*os.File); ok {
				file.Close()
			}
			return nil, fmt.Errorf("failed to read metadata for IFD %d: %w", i, err)
		}

		cog.geoTIFFs = append(cog.geoTIFFs, gtr)
		cog.metadata = append(cog.metadata, gtr.GetMetadata())
	}

	return cog, nil
}

// Bounds returns the geographic bounding box of the main image
func (c *COG) Bounds() orb.Bound {
	if len(c.geoTIFFs) == 0 {
		return orb.Bound{}
	}
	return c.geoTIFFs[0].Bounds()
}

// CRS returns the Coordinate Reference System
func (c *COG) CRS() string {
	if len(c.metadata) == 0 {
		return ""
	}
	return c.metadata[0].CRS
}

// Width returns the width of the main image in pixels
func (c *COG) Width() int {
	if len(c.metadata) == 0 {
		return 0
	}
	return c.metadata[0].Width
}

// Height returns the height of the main image in pixels
func (c *COG) Height() int {
	if len(c.metadata) == 0 {
		return 0
	}
	return c.metadata[0].Height
}

// BandCount returns the number of bands
func (c *COG) BandCount() int {
	if len(c.metadata) == 0 {
		return 0
	}
	return c.metadata[0].BandCount
}

// DataType returns the pixel data type
func (c *COG) DataType() DataType {
	if len(c.metadata) == 0 {
		return DTByte
	}
	return c.metadata[0].DataType
}

// PhotometricInterpretation returns the TIFF PhotometricInterpretation (tag 262)
// of the full-resolution image: 0 = WhiteIsZero, 1 = BlackIsZero, 2 = RGB, etc.
// (ortus-local addition: continuous-value consumers must reject WhiteIsZero,
// which decodeBytesToFlat would otherwise invert.)
func (c *COG) PhotometricInterpretation() uint16 {
	if len(c.metadata) == 0 {
		return 0
	}
	return c.metadata[0].PhotometricInterpretation
}

// OverviewCount returns the number of overview levels
func (c *COG) OverviewCount() int {
	if len(c.metadata) <= 1 {
		return 0
	}
	return len(c.metadata) - 1
}

// GetOverview returns metadata for a specific overview level (0 = highest resolution)
func (c *COG) GetOverview(level int) *GeoTIFFMetadata {
	overviewIndex := level + 1 // Overviews start at index 1
	if overviewIndex < 0 || overviewIndex >= len(c.metadata) {
		return nil
	}
	return c.metadata[overviewIndex]
}

// ReadRegion reads a geographic region from the COG
func (c *COG) ReadRegion(bound orb.Bound, overview int) (*RasterData, error) {
	if len(c.geoTIFFs) == 0 {
		return nil, fmt.Errorf("no image data available")
	}

	overviewIndex := overview // overview 0 = main image (IFD 0), overview 1+ = overviews (IFD 1+)
	if overviewIndex < 0 || overviewIndex >= len(c.geoTIFFs) {
		return nil, fmt.Errorf("invalid overview level: %d", overview)
	}

	gtr := c.geoTIFFs[overviewIndex]
	meta := c.metadata[overviewIndex]

	// Convert geographic bounds to pixel coordinates
	pixelBounds := c.geoToPixelBounds(bound, meta, gtr)

	// Clamp to image bounds
	pixelBounds.MinX = math.Max(0, math.Min(float64(meta.Width-1), pixelBounds.MinX))
	pixelBounds.MaxX = math.Max(0, math.Min(float64(meta.Width-1), pixelBounds.MaxX))
	pixelBounds.MinY = math.Max(0, math.Min(float64(meta.Height-1), pixelBounds.MinY))
	pixelBounds.MaxY = math.Max(0, math.Min(float64(meta.Height-1), pixelBounds.MaxY))

	width := int(math.Ceil(pixelBounds.MaxX - pixelBounds.MinX))
	height := int(math.Ceil(pixelBounds.MaxY - pixelBounds.MinY))

	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid region dimensions")
	}

	// Read the data
	data, err := c.readPixelRegion(overviewIndex, int(pixelBounds.MinX), int(pixelBounds.MinY), width, height)
	if err != nil {
		return nil, fmt.Errorf("failed to read pixel region: %w", err)
	}

	// Get byte order from IFD
	ifd := c.tiffReader.GetIFD(overviewIndex)
	if ifd == nil {
		return nil, fmt.Errorf("IFD %d not found", overviewIndex)
	}

	// Decode bytes to flat uint64 slice
	decodedData := c.decodeBytesToFlat(data, width, height, meta.BandCount, meta.DataType, ifd.ByteOrder, meta.PhotometricInterpretation)

	return &RasterData{
		Data:   decodedData,
		Width:  width,
		Height: height,
		Bands:  meta.BandCount,
		Bounds: bound,
	}, nil
}

// pixelBounds represents pixel coordinate bounds
type pixelBounds struct {
	MinX, MinY, MaxX, MaxY float64
}

// Rectangle represents a rectangle in pixel space
type Rectangle struct {
	X      int // X coordinate of top-left corner
	Y      int // Y coordinate of top-left corner
	Width  int // Width in pixels
	Height int // Height in pixels
}

// geoToPixelBounds converts geographic bounds to pixel bounds
func (c *COG) geoToPixelBounds(bound orb.Bound, meta *GeoTIFFMetadata, gtr *GeoTIFFReader) pixelBounds {
	// Get image bounds
	imgBounds := gtr.Bounds()

	// Calculate pixel coordinates
	geoWidth := imgBounds.Max[0] - imgBounds.Min[0]
	geoHeight := imgBounds.Max[1] - imgBounds.Min[1]

	if geoWidth == 0 || geoHeight == 0 {
		return pixelBounds{}
	}

	// Normalize coordinates
	minX := (bound.Min[0] - imgBounds.Min[0]) / geoWidth * float64(meta.Width)
	maxX := (bound.Max[0] - imgBounds.Min[0]) / geoWidth * float64(meta.Width)
	minY := (imgBounds.Max[1] - bound.Max[1]) / geoHeight * float64(meta.Height) // Y is inverted
	maxY := (imgBounds.Max[1] - bound.Min[1]) / geoHeight * float64(meta.Height)

	return pixelBounds{
		MinX: minX,
		MinY: minY,
		MaxX: maxX,
		MaxY: maxY,
	}
}

// readPixelRegion reads a region of pixels from the specified IFD
func (c *COG) readPixelRegion(ifdIndex int, x, y, width, height int) ([]byte, error) {
	ifd := c.tiffReader.GetIFD(ifdIndex)
	if ifd == nil {
		return nil, fmt.Errorf("IFD %d not found", ifdIndex)
	}

	meta := c.metadata[ifdIndex]

	// Get strip or tile information
	stripOffsetsTag := ifd.Tags[273]    // StripOffsets
	stripByteCountsTag := ifd.Tags[279] // StripByteCounts
	tileOffsetsTag := ifd.Tags[324]     // TileOffsets
	tileByteCountsTag := ifd.Tags[325]  // TileByteCounts

	// Check if tiled
	if tileOffsetsTag != nil && tileByteCountsTag != nil {
		return c.readTiledRegion(ifd, meta, x, y, width, height)
	}

	// Check if stripped
	if stripOffsetsTag != nil && stripByteCountsTag != nil {
		return c.readStrippedRegion(ifd, meta, x, y, width, height)
	}

	return nil, fmt.Errorf("image is neither tiled nor stripped")
}

// decompressTile decompresses a tile/strip and then reverses any TIFF predictor
// (tag 317). tileWidth is the row width in pixels (callers pass the strip width
// for stripped images), so the horizontal predictor reversal is correct for both
// tiled and stripped layouts. ortus-local extension over upstream gocog, which
// ignores the predictor tag.
func (c *COG) decompressTile(data []byte, compression uint16, ifd *IFD, tileWidth, tileHeight, bands int, dataType DataType) ([]byte, error) {
	raw, err := c.decompressTileRaw(data, compression, ifd, tileWidth, tileHeight, bands, dataType)
	if err != nil {
		return nil, err
	}
	pred := readPredictor(ifd)
	if pred == predictorNone {
		return raw, nil
	}
	// The horizontal integer predictor only applies to lossless integer sample
	// data. JPEG output is already de-predicted, and float bands would use the
	// (unsupported) floating-point predictor 3 — guard both rather than silently
	// corrupt the decoded buffer.
	if compression == CompressionJPEG {
		return nil, fmt.Errorf("gocog: TIFF predictor %d is not valid with JPEG compression", pred)
	}
	if dataType == DTFloat || dataType == DTDouble {
		return nil, fmt.Errorf("gocog: horizontal predictor not supported for float sample data (needs predictor 3)")
	}
	if err := applyHorizontalPredictor(raw, pred, tileWidth, tileHeight, bands, c.getBytesPerSample(dataType), ifd.ByteOrder); err != nil {
		return nil, err
	}
	return raw, nil
}

// decompressTileRaw decompresses tile data based on compression type (no
// predictor handling — see decompressTile).
func (c *COG) decompressTileRaw(data []byte, compression uint16, ifd *IFD, tileWidth, tileHeight, bands int, dataType DataType) ([]byte, error) {
	switch compression {
	case CompressionNone:
		// No compression, return as-is
		return data, nil

	case CompressionLZW:
		// LZW compression - use TIFF-specific LZW decoder
		bytesPerPixel := bands * c.getBytesPerSample(dataType)
		expectedSize := tileWidth * tileHeight * bytesPerPixel

		// If compressed size equals expected size, data is likely uncompressed
		if len(data) == expectedSize {
			// Data size matches expected uncompressed size - likely not actually compressed
			return data, nil
		}

		// Attempt LZW decompression
		// Try LSB first (TIFF standard), fall back to MSB if that fails
		var decompressed []byte
		var err error

		// Try LSB order first
		reader := lzw.NewReader(bytes.NewReader(data), lzw.LSB, 8)
		decompressed, err = io.ReadAll(reader)
		reader.Close()

		if err != nil {
			// LSB failed, try MSB order
			reader = lzw.NewReader(bytes.NewReader(data), lzw.MSB, 8)
			decompressed, err = io.ReadAll(reader)
			reader.Close()
		}

		if err != nil {
			// Both LSB and MSB failed
			// If data size matches expected, it might not actually be compressed
			if len(data) == expectedSize {
				return data, nil
			}
			return nil, fmt.Errorf("failed to decompress LZW tile (data size: %d, expected: %d): %w", len(data), expectedSize, err)
		}

		// Verify we got at least the expected amount of data
		if len(decompressed) < expectedSize {
			return nil, fmt.Errorf("LZW decompression produced insufficient data: got %d bytes, expected at least %d", len(decompressed), expectedSize)
		}

		// Return only the expected size (trim any padding)
		return decompressed[:expectedSize], nil

	case CompressionDeflate:
		// Deflate/ZIP compression
		reader := flate.NewReader(bytes.NewReader(data))
		defer reader.Close()

		// Read all decompressed data (size may vary due to padding)
		decompressed, err := io.ReadAll(reader)
		if err != nil {
			return nil, fmt.Errorf("failed to decompress Deflate tile: %w", err)
		}

		// Verify we got at least the expected amount of data
		bytesPerPixel := bands * c.getBytesPerSample(dataType)
		expectedSize := tileWidth * tileHeight * bytesPerPixel
		if len(decompressed) < expectedSize {
			return nil, fmt.Errorf("Deflate decompression produced insufficient data: got %d bytes, expected at least %d", len(decompressed), expectedSize)
		}

		// Return only the expected size (trim any padding)
		return decompressed[:expectedSize], nil

	case CompressionJPEG:
		// JPEG compression - decode as JPEG image
		img, err := jpeg.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("failed to decode JPEG tile: %w", err)
		}

		// Convert image to raw bytes
		bounds := img.Bounds()
		width := bounds.Dx()
		height := bounds.Dy()
		bytesPerPixel := bands * c.getBytesPerSample(dataType)
		// Use pooled buffer for result
		result := GetBuffer(width * height * bytesPerPixel)
		result = result[:width*height*bytesPerPixel]

		// Handle different image types
		switch imgType := img.(type) {
		case *image.RGBA:
			// RGBA image - copy directly
			copy(result, imgType.Pix)
		case *image.NRGBA:
			// NRGBA image - copy directly
			copy(result, imgType.Pix)
		case *image.Gray:
			// Grayscale image - expand to RGB if needed
			for y := 0; y < height; y++ {
				for x := 0; x < width; x++ {
					gray := imgType.GrayAt(x, y)
					offset := (y*width + x) * bytesPerPixel
					if bands >= 3 {
						result[offset] = gray.Y
						result[offset+1] = gray.Y
						result[offset+2] = gray.Y
						if bands == 4 {
							result[offset+3] = 255
						}
					} else {
						result[offset] = gray.Y
					}
				}
			}
		default:
			// Generic image - convert pixel by pixel
			for y := 0; y < height; y++ {
				for x := 0; x < width; x++ {
					r, g, b, a := imgType.At(x, y).RGBA()
					offset := (y*width + x) * bytesPerPixel
					if bands >= 3 {
						result[offset] = uint8(r >> 8)
						result[offset+1] = uint8(g >> 8)
						result[offset+2] = uint8(b >> 8)
						if bands == 4 {
							result[offset+3] = uint8(a >> 8)
						}
					} else {
						result[offset] = uint8(r >> 8)
					}
				}
			}
		}

		return result, nil

	default:
		return nil, fmt.Errorf("unsupported compression type: %d", compression)
	}
}

// tileWorkItem represents work for parallel tile processing
type tileWorkItem struct {
	tileX, tileY     int
	tileIndex        int
	compressedData   []byte
	decompressedData []byte
	err              error
}

// readTiledRegion reads a region from a tiled image
// Uses parallel decompression for improved performance on multi-core systems
func (c *COG) readTiledRegion(ifd *IFD, meta *GeoTIFFMetadata, x, y, width, height int) ([]byte, error) {
	// Get compression type (default to None if not specified)
	compression := uint16(CompressionNone)
	if tag := ifd.Tags[259]; tag != nil { // Compression
		if val, ok := tag.Value.(uint16); ok {
			compression = val
		} else if val, ok := tag.Value.(uint32); ok {
			compression = uint16(val)
		}
	}

	// Get tile dimensions
	tileWidth := 256  // Default
	tileHeight := 256 // Default

	if tag := ifd.Tags[322]; tag != nil { // TileWidth
		if val, ok := tag.Value.(uint16); ok {
			tileWidth = int(val)
		} else if val, ok := tag.Value.(uint32); ok {
			tileWidth = int(val)
		}
	}

	if tag := ifd.Tags[323]; tag != nil { // TileLength
		if val, ok := tag.Value.(uint16); ok {
			tileHeight = int(val)
		} else if val, ok := tag.Value.(uint32); ok {
			tileHeight = int(val)
		}
	}

	// Get tile offsets and byte counts (lazy load if needed)
	tileOffsetsTag := ifd.Tags[324]
	tileByteCountsTag := ifd.Tags[325]

	// Lazy load tile offsets if not already loaded
	if tileOffsetsTag != nil && tileOffsetsTag.Value == nil && tileOffsetsTag.IsOffset {
		if err := c.tiffReader.ReadTagValue(ifd, 324); err != nil {
			return nil, fmt.Errorf("failed to read tile offsets: %w", err)
		}
	}

	// Lazy load tile byte counts if not already loaded
	if tileByteCountsTag != nil && tileByteCountsTag.Value == nil && tileByteCountsTag.IsOffset {
		if err := c.tiffReader.ReadTagValue(ifd, 325); err != nil {
			return nil, fmt.Errorf("failed to read tile byte counts: %w", err)
		}
	}

	var tileOffsets []uint32
	var tileByteCounts []uint32

	if offsets, ok := tileOffsetsTag.Value.([]uint32); ok {
		tileOffsets = offsets
	} else if offset, ok := tileOffsetsTag.Value.(uint32); ok {
		tileOffsets = []uint32{offset}
	}

	if counts, ok := tileByteCountsTag.Value.([]uint32); ok {
		tileByteCounts = counts
	} else if count, ok := tileByteCountsTag.Value.(uint32); ok {
		tileByteCounts = []uint32{count}
	}

	// Calculate tile indices
	tilesPerRow := (meta.Width + tileWidth - 1) / tileWidth

	startTileX := x / tileWidth
	endTileX := (x + width - 1) / tileWidth
	startTileY := y / tileHeight
	endTileY := (y + height - 1) / tileHeight

	// Allocate output buffer
	bytesPerPixel := meta.BandCount * c.getBytesPerSample(meta.DataType)
	output := make([]byte, width*height*bytesPerPixel)

	// Collect all tiles that need to be read
	var tiles []*tileWorkItem
	for tileY := startTileY; tileY <= endTileY; tileY++ {
		for tileX := startTileX; tileX <= endTileX; tileX++ {
			tileIndex := tileY*tilesPerRow + tileX
			if tileIndex >= len(tileOffsets) {
				continue
			}
			tiles = append(tiles, &tileWorkItem{
				tileX:     tileX,
				tileY:     tileY,
				tileIndex: tileIndex,
			})
		}
	}

	// If only one tile or no compression, use sequential processing
	if len(tiles) <= 1 || compression == CompressionNone {
		return c.readTiledRegionSequential(ifd, meta, x, y, width, height, tileWidth, tileHeight,
			tilesPerRow, tileOffsets, tileByteCounts, compression, bytesPerPixel, output, tiles)
	}

	// Phase 1: Read all compressed tile data sequentially (I/O bound)
	for _, tile := range tiles {
		tileOffset := tileOffsets[tile.tileIndex]
		tileSize := tileByteCounts[tile.tileIndex]

		// Read tile data using pooled buffer
		tile.compressedData = GetBuffer(int(tileSize))
		tile.compressedData = tile.compressedData[:tileSize]
		if _, err := c.reader.Seek(int64(tileOffset), io.SeekStart); err != nil {
			// Clean up on error
			for _, t := range tiles {
				if t.compressedData != nil {
					PutBuffer(t.compressedData)
				}
			}
			return nil, fmt.Errorf("failed to seek to tile: %w", err)
		}
		if _, err := io.ReadFull(c.reader, tile.compressedData); err != nil {
			// Clean up on error
			for _, t := range tiles {
				if t.compressedData != nil {
					PutBuffer(t.compressedData)
				}
			}
			return nil, fmt.Errorf("failed to read tile: %w", err)
		}
	}

	// Phase 2: Decompress tiles in parallel (CPU bound)
	numWorkers := runtime.NumCPU()
	if numWorkers > len(tiles) {
		numWorkers = len(tiles)
	}

	var wg sync.WaitGroup
	workChan := make(chan *tileWorkItem, len(tiles))

	// Start worker goroutines
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for tile := range workChan {
				tile.decompressedData, tile.err = c.decompressTile(
					tile.compressedData, compression, ifd,
					tileWidth, tileHeight, meta.BandCount, meta.DataType,
				)
				// Return compressed buffer to pool
				PutBuffer(tile.compressedData)
				tile.compressedData = nil
			}
		}()
	}

	// Send work to workers
	for _, tile := range tiles {
		workChan <- tile
	}
	close(workChan)

	// Wait for all workers to complete
	wg.Wait()

	// Check for errors and copy data to output
	for _, tile := range tiles {
		if tile.err != nil {
			// Clean up decompressed data
			for _, t := range tiles {
				if t.decompressedData != nil && compression == CompressionJPEG {
					PutBuffer(t.decompressedData)
				}
			}
			return nil, fmt.Errorf("failed to decompress tile: %w", tile.err)
		}

		// Copy tile data to output
		c.copyTileToOutput(tile, output, x, y, width, height, tileWidth, tileHeight, bytesPerPixel)

		// Return decompressed data to pool if it came from pool (JPEG only)
		if compression == CompressionJPEG && tile.decompressedData != nil {
			PutBuffer(tile.decompressedData)
		}
	}

	return output, nil
}

// readTiledRegionSequential handles the simple case of sequential tile reading
func (c *COG) readTiledRegionSequential(ifd *IFD, meta *GeoTIFFMetadata, x, y, width, height int,
	tileWidth, tileHeight, tilesPerRow int, tileOffsets, tileByteCounts []uint32,
	compression uint16, bytesPerPixel int, output []byte, tiles []*tileWorkItem) ([]byte, error) {

	for _, tile := range tiles {
		tileOffset := tileOffsets[tile.tileIndex]
		tileSize := tileByteCounts[tile.tileIndex]

		// Read tile data using pooled buffer
		tileData := GetBuffer(int(tileSize))
		tileData = tileData[:tileSize]
		if _, err := c.reader.Seek(int64(tileOffset), io.SeekStart); err != nil {
			PutBuffer(tileData)
			return nil, fmt.Errorf("failed to seek to tile: %w", err)
		}
		if _, err := io.ReadFull(c.reader, tileData); err != nil {
			PutBuffer(tileData)
			return nil, fmt.Errorf("failed to read tile: %w", err)
		}

		// Decompress tile data if needed
		decompressedTile, err := c.decompressTile(tileData, compression, ifd, tileWidth, tileHeight, meta.BandCount, meta.DataType)
		if compression != CompressionNone {
			PutBuffer(tileData)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to decompress tile: %w", err)
		}
		tile.decompressedData = decompressedTile

		// Copy tile data to output
		c.copyTileToOutput(tile, output, x, y, width, height, tileWidth, tileHeight, bytesPerPixel)

		// Return decompressed data to pool if it came from pool (JPEG only)
		if compression == CompressionJPEG {
			PutBuffer(tile.decompressedData)
		}
	}

	return output, nil
}

// copyTileToOutput copies decompressed tile data to the output buffer
func (c *COG) copyTileToOutput(tile *tileWorkItem, output []byte, x, y, width, height, tileWidth, tileHeight, bytesPerPixel int) {
	tileData := tile.decompressedData

	// Calculate the intersection of the tile and the requested region
	tileStartX := tile.tileX * tileWidth
	tileStartY := tile.tileY * tileHeight
	tileEndX := tileStartX + tileWidth
	tileEndY := tileStartY + tileHeight

	// Clamp to requested region bounds
	regionStartX := x
	regionStartY := y
	regionEndX := x + width
	regionEndY := y + height

	// Find intersection
	copyStartX := regionStartX
	if tileStartX > copyStartX {
		copyStartX = tileStartX
	}
	copyStartY := regionStartY
	if tileStartY > copyStartY {
		copyStartY = tileStartY
	}
	copyEndX := regionEndX
	if tileEndX < copyEndX {
		copyEndX = tileEndX
	}
	copyEndY := regionEndY
	if tileEndY < copyEndY {
		copyEndY = tileEndY
	}

	// If no intersection, skip this tile
	if copyStartX >= copyEndX || copyStartY >= copyEndY {
		return
	}

	copyWidth := copyEndX - copyStartX
	copyHeight := copyEndY - copyStartY

	// Calculate offsets within tile and output buffer
	tileOffsetX := copyStartX - tileStartX     // Offset within tile
	tileOffsetY := copyStartY - tileStartY     // Offset within tile
	outputOffsetX := copyStartX - regionStartX // Offset within output
	outputOffsetY := copyStartY - regionStartY // Offset within output

	// Copy tile data to output
	for row := 0; row < copyHeight; row++ {
		// Source: tile data at (tileOffsetX, tileOffsetY + row)
		srcRowOffset := (tileOffsetY + row) * tileWidth * bytesPerPixel
		srcOffset := srcRowOffset + tileOffsetX*bytesPerPixel

		// Destination: output buffer at (outputOffsetX, outputOffsetY + row)
		dstRowOffset := (outputOffsetY + row) * width * bytesPerPixel
		dstOffset := dstRowOffset + outputOffsetX*bytesPerPixel

		bytesToCopy := copyWidth * bytesPerPixel

		// Bounds check: ensure we don't exceed tileData length
		if srcOffset+bytesToCopy > len(tileData) {
			bytesToCopy = len(tileData) - srcOffset
			if bytesToCopy <= 0 {
				break
			}
		}

		// Bounds check: ensure we don't exceed output buffer length
		if dstOffset+bytesToCopy > len(output) {
			bytesToCopy = len(output) - dstOffset
			if bytesToCopy <= 0 {
				break
			}
		}

		// Copy the row
		copy(output[dstOffset:dstOffset+bytesToCopy],
			tileData[srcOffset:srcOffset+bytesToCopy])
	}
}

// readStrippedRegion reads a region from a stripped image
// Uses strip caching to avoid re-reading and re-decompressing the same strip multiple times
func (c *COG) readStrippedRegion(ifd *IFD, meta *GeoTIFFMetadata, x, y, width, height int) ([]byte, error) {
	// Get compression type (default to None if not specified)
	compression := uint16(CompressionNone)
	if tag := ifd.Tags[259]; tag != nil { // Compression
		if val, ok := tag.Value.(uint16); ok {
			compression = val
		} else if val, ok := tag.Value.(uint32); ok {
			compression = uint16(val)
		}
	}

	// Get strip information
	stripOffsetsTag := ifd.Tags[273]
	stripByteCountsTag := ifd.Tags[279]

	// Lazy load strip offsets if not already loaded
	if stripOffsetsTag != nil && stripOffsetsTag.Value == nil && stripOffsetsTag.IsOffset {
		if err := c.tiffReader.ReadTagValue(ifd, 273); err != nil {
			return nil, fmt.Errorf("failed to read strip offsets: %w", err)
		}
	}

	// Lazy load strip byte counts if not already loaded
	if stripByteCountsTag != nil && stripByteCountsTag.Value == nil && stripByteCountsTag.IsOffset {
		if err := c.tiffReader.ReadTagValue(ifd, 279); err != nil {
			return nil, fmt.Errorf("failed to read strip byte counts: %w", err)
		}
	}

	var stripOffsets []uint32
	var stripByteCounts []uint32

	if offsets, ok := stripOffsetsTag.Value.([]uint32); ok {
		stripOffsets = offsets
	} else if offset, ok := stripOffsetsTag.Value.(uint32); ok {
		stripOffsets = []uint32{offset}
	}

	if counts, ok := stripByteCountsTag.Value.([]uint32); ok {
		stripByteCounts = counts
	} else if count, ok := stripByteCountsTag.Value.(uint32); ok {
		stripByteCounts = []uint32{count}
	}

	// Get rows per strip
	rowsPerStrip := meta.Height           // Default
	if tag := ifd.Tags[278]; tag != nil { // RowsPerStrip
		if val, ok := tag.Value.(uint16); ok {
			rowsPerStrip = int(val)
		} else if val, ok := tag.Value.(uint32); ok {
			rowsPerStrip = int(val)
		}
	}

	bytesPerPixel := meta.BandCount * c.getBytesPerSample(meta.DataType)
	bytesPerRow := meta.Width * bytesPerPixel

	// Allocate output buffer
	output := make([]byte, width*height*bytesPerPixel)

	// Strip cache to avoid re-reading/re-decompressing the same strip
	// Key: strip index, Value: decompressed strip data
	stripCache := make(map[int][]byte)

	// Calculate which strips we need
	startStripIndex := y / rowsPerStrip
	endStripIndex := (y + height - 1) / rowsPerStrip

	// Pre-read and decompress all needed strips
	for stripIndex := startStripIndex; stripIndex <= endStripIndex; stripIndex++ {
		if stripIndex >= len(stripOffsets) {
			continue
		}

		stripOffset := stripOffsets[stripIndex]
		stripSize := stripByteCounts[stripIndex]

		// Read the entire strip using pooled buffer
		compressedData := GetBuffer(int(stripSize))
		compressedData = compressedData[:stripSize]
		if _, err := c.reader.Seek(int64(stripOffset), io.SeekStart); err != nil {
			PutBuffer(compressedData)
			// Clean up cached strips
			for _, cached := range stripCache {
				PutBuffer(cached)
			}
			return nil, fmt.Errorf("failed to seek to strip: %w", err)
		}
		if _, err := io.ReadFull(c.reader, compressedData); err != nil {
			PutBuffer(compressedData)
			// Clean up cached strips
			for _, cached := range stripCache {
				PutBuffer(cached)
			}
			return nil, fmt.Errorf("failed to read strip: %w", err)
		}

		// Decompress strip data
		decompressedStrip, err := c.decompressTile(compressedData, compression, ifd, meta.Width, rowsPerStrip, meta.BandCount, meta.DataType)
		// Return compressed buffer to pool
		if compression != CompressionNone {
			PutBuffer(compressedData)
		}
		if err != nil {
			// Clean up cached strips
			for _, cached := range stripCache {
				PutBuffer(cached)
			}
			return nil, fmt.Errorf("failed to decompress strip: %w", err)
		}

		stripCache[stripIndex] = decompressedStrip
	}

	// Now copy data from cached strips to output
	for row := y; row < y+height; row++ {
		stripIndex := row / rowsPerStrip
		stripData, ok := stripCache[stripIndex]
		if !ok {
			continue
		}

		stripStartRow := stripIndex * rowsPerStrip
		stripRow := row - stripStartRow

		// Copy row data
		srcOffset := stripRow*bytesPerRow + x*bytesPerPixel
		dstOffset := (row - y) * width * bytesPerPixel

		// Bounds check for safety
		if srcOffset+width*bytesPerPixel <= len(stripData) && dstOffset+width*bytesPerPixel <= len(output) {
			copy(output[dstOffset:dstOffset+width*bytesPerPixel],
				stripData[srcOffset:srcOffset+width*bytesPerPixel])
		}
	}

	// Clean up cached strips (return to pool if they came from pool)
	// Note: decompressed data from decompressTile may or may not be pooled
	// depending on compression type, so we only return JPEG results to pool
	for _, cached := range stripCache {
		if compression == CompressionJPEG {
			PutBuffer(cached)
		}
	}

	return output, nil
}

// getBytesPerSample returns the number of bytes per sample for a data type
func (c *COG) getBytesPerSample(dt DataType) int {
	switch dt {
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

// decodeBytesToFlat decodes byte data into a flat uint64 slice.
// The output is in band-interleaved-by-pixel (BIP) format:
// index = y * width * bands + x * bands + band
// This provides better cache locality than nested slices.
func (c *COG) decodeBytesToFlat(data []byte, width, height, bands int, dataType DataType, byteOrder binary.ByteOrder, photometricInterpretation uint16) []uint64 {
	// Allocate flat result array - single allocation
	totalPixels := width * height * bands
	result := make([]uint64, totalPixels)

	bytesPerSample := c.getBytesPerSample(dataType)
	bytesPerPixel := bands * bytesPerSample

	// Decode each pixel - optimized loop order for cache locality
	for y := 0; y < height; y++ {
		rowOffset := y * width * bands
		for x := 0; x < width; x++ {
			pixelOffset := (y*width + x) * bytesPerPixel
			resultBase := rowOffset + x*bands

			// Decode each band for this pixel
			for b := 0; b < bands; b++ {
				sampleOffset := pixelOffset + b*bytesPerSample

				if sampleOffset+bytesPerSample > len(data) {
					continue // Skip if out of bounds
				}

				var value uint64
				switch dataType {
				case DTByte, DTASCII, DTUndefined:
					value = uint64(data[sampleOffset])
				case DTSByte:
					value = uint64(int8(data[sampleOffset]))
				case DTSShort:
					value = uint64(byteOrder.Uint16(data[sampleOffset : sampleOffset+2]))
				case DTSShortS:
					value = uint64(int16(byteOrder.Uint16(data[sampleOffset : sampleOffset+2])))
				case DTSLong:
					value = uint64(byteOrder.Uint32(data[sampleOffset : sampleOffset+4]))
				case DTSLongS:
					value = uint64(int32(byteOrder.Uint32(data[sampleOffset : sampleOffset+4])))
				case DTFloat:
					// Convert float32 to uint64 (preserving bit pattern)
					bits := byteOrder.Uint32(data[sampleOffset : sampleOffset+4])
					value = uint64(bits)
				case DTDouble:
					// Convert float64 to uint64 (preserving bit pattern)
					value = byteOrder.Uint64(data[sampleOffset : sampleOffset+8])
				case DTRational:
					// Rational: numerator (uint32) / denominator (uint32)
					num := byteOrder.Uint32(data[sampleOffset : sampleOffset+4])
					den := byteOrder.Uint32(data[sampleOffset+4 : sampleOffset+8])
					if den != 0 {
						value = uint64(num) / uint64(den)
					}
				case DTSRational:
					// Signed rational: numerator (int32) / denominator (int32)
					num := int32(byteOrder.Uint32(data[sampleOffset : sampleOffset+4]))
					den := int32(byteOrder.Uint32(data[sampleOffset+4 : sampleOffset+8]))
					if den != 0 {
						value = uint64(num) / uint64(den)
					}
				default:
					value = uint64(data[sampleOffset])
				}

				result[resultBase+b] = value
			}
		}
	}

	// Handle PhotometricInterpretation: WhiteIsZero (0) requires inversion for grayscale
	if photometricInterpretation == 0 && bands == 1 {
		// Invert grayscale values: white (max) becomes black (0) and vice versa
		var maxValue uint64
		switch dataType {
		case DTByte, DTASCII, DTUndefined:
			maxValue = 255
		case DTSByte:
			maxValue = 127
		case DTSShort:
			maxValue = 65535
		case DTSShortS:
			maxValue = 32767
		case DTSLong:
			maxValue = 4294967295
		case DTSLongS:
			maxValue = 2147483647
		default:
			maxValue = 255
		}
		for i := range result {
			result[i] = maxValue - result[i]
		}
	}

	return result
}

// ReadWindow reads a window (rectangle) from the COG in pixel space.
// The rectangle is specified in the main image's pixel coordinates.
// The function automatically selects the appropriate overview level to minimize data transfer
// while maintaining sufficient resolution.
func (c *COG) ReadWindow(rect Rectangle) (*RasterData, error) {
	if len(c.metadata) == 0 {
		return nil, fmt.Errorf("no image data available")
	}

	// Validate rectangle bounds against main image
	mainMeta := c.metadata[0]
	if rect.X < 0 || rect.Y < 0 {
		return nil, fmt.Errorf("rectangle coordinates must be non-negative")
	}
	if rect.Width <= 0 || rect.Height <= 0 {
		return nil, fmt.Errorf("rectangle dimensions must be positive")
	}
	if rect.X+rect.Width > mainMeta.Width {
		return nil, fmt.Errorf("rectangle extends beyond image width")
	}
	if rect.Y+rect.Height > mainMeta.Height {
		return nil, fmt.Errorf("rectangle extends beyond image height")
	}

	// Determine which overview to use
	overviewIndex := c.selectOverview(rect)
	meta := c.metadata[overviewIndex]

	// Scale rectangle coordinates to the selected overview
	scaleX := float64(meta.Width) / float64(mainMeta.Width)
	scaleY := float64(meta.Height) / float64(mainMeta.Height)

	overviewX := int(float64(rect.X) * scaleX)
	overviewY := int(float64(rect.Y) * scaleY)
	overviewWidth := int(math.Ceil(float64(rect.Width) * scaleX))
	overviewHeight := int(math.Ceil(float64(rect.Height) * scaleY))

	// Clamp to overview bounds
	if overviewX+overviewWidth > meta.Width {
		overviewWidth = meta.Width - overviewX
	}
	if overviewY+overviewHeight > meta.Height {
		overviewHeight = meta.Height - overviewY
	}

	if overviewWidth <= 0 || overviewHeight <= 0 {
		return nil, fmt.Errorf("invalid window dimensions after scaling to overview")
	}

	// Read pixel data from the selected overview
	data, err := c.readPixelRegion(overviewIndex, overviewX, overviewY, overviewWidth, overviewHeight)
	if err != nil {
		return nil, fmt.Errorf("failed to read pixel region: %w", err)
	}

	// Get byte order from IFD
	ifd := c.tiffReader.GetIFD(overviewIndex)
	if ifd == nil {
		return nil, fmt.Errorf("IFD %d not found", overviewIndex)
	}

	// Decode bytes to flat uint64 slice
	decodedData := c.decodeBytesToFlat(data, overviewWidth, overviewHeight, meta.BandCount, meta.DataType, ifd.ByteOrder, meta.PhotometricInterpretation)

	// Calculate geographic bounds using main image georeferencing
	mainGTR := c.geoTIFFs[0]
	topLeftX, topLeftY := mainGTR.pixelToGeo(float64(rect.X), float64(rect.Y))
	bottomRightX, bottomRightY := mainGTR.pixelToGeo(float64(rect.X+rect.Width), float64(rect.Y+rect.Height))

	bounds := orb.Bound{
		Min: orb.Point{topLeftX, bottomRightY},
		Max: orb.Point{bottomRightX, topLeftY},
	}

	return &RasterData{
		Data:   decodedData,
		Width:  overviewWidth,
		Height: overviewHeight,
		Bands:  meta.BandCount,
		Bounds: bounds,
	}, nil
}

// selectOverview determines which overview level to use for reading a window.
// It selects the overview that minimizes data transfer while maintaining reasonable resolution.
// The strategy prefers higher resolution overviews for smaller windows and lower resolution
// overviews for larger windows.
func (c *COG) selectOverview(rect Rectangle) int {
	if len(c.metadata) == 0 {
		return 0
	}

	mainMeta := c.metadata[0]
	windowArea := rect.Width * rect.Height
	mainArea := mainMeta.Width * mainMeta.Height

	// If window is very small relative to main image, use main image (highest resolution)
	if windowArea < mainArea/100 {
		return 0
	}

	// Calculate data transfer for each overview
	bestOverview := 0
	minDataTransfer := math.MaxFloat64

	for i := 0; i < len(c.metadata); i++ {
		meta := c.metadata[i]

		// Scale window to this overview
		scaleX := float64(meta.Width) / float64(mainMeta.Width)
		scaleY := float64(meta.Height) / float64(mainMeta.Height)

		overviewWidth := int(math.Ceil(float64(rect.Width) * scaleX))
		overviewHeight := int(math.Ceil(float64(rect.Height) * scaleY))

		// Estimate data transfer (pixels * bytes per pixel)
		bytesPerPixel := c.getBytesPerSample(meta.DataType) * meta.BandCount
		dataTransfer := float64(overviewWidth * overviewHeight * bytesPerPixel)

		// Prefer this overview if it requires less data transfer
		// But also consider resolution - don't go too low resolution
		// We want to minimize data transfer while keeping resolution reasonable
		if dataTransfer < minDataTransfer {
			// Check if resolution is still reasonable (at least 1/4 of original)
			resolutionRatio := float64(meta.Width*meta.Height) / float64(mainMeta.Width*mainMeta.Height)
			if resolutionRatio >= 0.25 || i == 0 {
				minDataTransfer = dataTransfer
				bestOverview = i
			}
		}
	}

	return bestOverview
}

// ReadTile reads a map tile from the COG and returns it as a RasterData image.
// The tile is specified using a maptile.Tile object and will be resampled to the specified tile size.
// The GeoTiff must be in CRS EPSG:4326 or EPSG:3857, otherwise an error is returned.
// If tileSize is not provided or is <= 0, it defaults to 256.
func (c *COG) ReadTile(tile maptile.Tile, tileSize ...int) (*RasterData, error) {
	if len(c.geoTIFFs) == 0 {
		return nil, fmt.Errorf("no image data available")
	}

	// Default tile size is 256
	size := 256
	if len(tileSize) > 0 && tileSize[0] > 0 {
		size = tileSize[0]
	}

	// Validate CRS
	crs := c.CRS()
	if crs != "EPSG:4326" && crs != "EPSG:3857" {
		return nil, fmt.Errorf("unsupported CRS: %s (only EPSG:4326 and EPSG:3857 are supported)", crs)
	}

	// Get tile bounds (tile.Bound() returns WGS84/EPSG:4326 bounds)
	tileBounds := tile.Bound()

	// Convert tile bounds to match GeoTiff CRS if needed
	var geoBounds orb.Bound
	if crs == "EPSG:4326" {
		// Already in WGS84
		geoBounds = tileBounds
	} else {
		// Convert WGS84 bounds to Web Mercator
		geoBounds = wgs84ToMercator(tileBounds)
	}

	// Convert geographic bounds to pixel coordinates
	meta := c.metadata[0]
	gtr := c.geoTIFFs[0]
	pixelBounds := c.geoToPixelBounds(geoBounds, meta, gtr)

	// Clamp to image bounds
	pixelBounds.MinX = math.Max(0, math.Min(float64(meta.Width-1), pixelBounds.MinX))
	pixelBounds.MaxX = math.Max(0, math.Min(float64(meta.Width-1), pixelBounds.MaxX))
	pixelBounds.MinY = math.Max(0, math.Min(float64(meta.Height-1), pixelBounds.MinY))
	pixelBounds.MaxY = math.Max(0, math.Min(float64(meta.Height-1), pixelBounds.MaxY))

	width := int(math.Ceil(pixelBounds.MaxX - pixelBounds.MinX))
	height := int(math.Ceil(pixelBounds.MaxY - pixelBounds.MinY))

	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid tile dimensions after projection")
	}

	// Read the pixel data
	data, err := c.readPixelRegion(0, int(pixelBounds.MinX), int(pixelBounds.MinY), width, height)
	if err != nil {
		return nil, fmt.Errorf("failed to read pixel region: %w", err)
	}

	// Resample to tile size if needed
	if width != size || height != size {
		data, err = c.resampleImage(data, width, height, size, size, meta.BandCount, meta.DataType)
		if err != nil {
			return nil, fmt.Errorf("failed to resample image: %w", err)
		}
		width = size
		height = size
	}

	// Get byte order from IFD
	ifd := c.tiffReader.GetIFD(0)
	if ifd == nil {
		return nil, fmt.Errorf("IFD 0 not found")
	}

	// Decode bytes to flat uint64 slice
	decodedData := c.decodeBytesToFlat(data, width, height, meta.BandCount, meta.DataType, ifd.ByteOrder, meta.PhotometricInterpretation)

	return &RasterData{
		Data:   decodedData,
		Width:  width,
		Height: height,
		Bands:  meta.BandCount,
		Bounds: geoBounds,
	}, nil
}

// mercatorToWGS84 converts Web Mercator (EPSG:3857) bounds to WGS84 (EPSG:4326) bounds
func mercatorToWGS84(bound orb.Bound) orb.Bound {
	const maxMercator = 20037508.342789244

	minLon := bound.Min[0] / maxMercator * 180.0
	maxLon := bound.Max[0] / maxMercator * 180.0

	minLat := math.Atan(math.Exp(bound.Min[1]*math.Pi/maxMercator))*360.0/math.Pi - 90.0
	maxLat := math.Atan(math.Exp(bound.Max[1]*math.Pi/maxMercator))*360.0/math.Pi - 90.0

	return orb.Bound{
		Min: orb.Point{minLon, minLat},
		Max: orb.Point{maxLon, maxLat},
	}
}

// wgs84ToMercator converts WGS84 (EPSG:4326) bounds to Web Mercator (EPSG:3857) bounds
func wgs84ToMercator(bound orb.Bound) orb.Bound {
	const maxMercator = 20037508.342789244

	minX := bound.Min[0] / 180.0 * maxMercator
	maxX := bound.Max[0] / 180.0 * maxMercator

	minY := math.Log(math.Tan((90.0+bound.Min[1])*math.Pi/360.0)) / math.Pi * maxMercator
	maxY := math.Log(math.Tan((90.0+bound.Max[1])*math.Pi/360.0)) / math.Pi * maxMercator

	return orb.Bound{
		Min: orb.Point{minX, minY},
		Max: orb.Point{maxX, maxY},
	}
}

// resampleImage resamples image data from source dimensions to target dimensions
func (c *COG) resampleImage(data []byte, srcWidth, srcHeight, dstWidth, dstHeight, bands int, dataType DataType) ([]byte, error) {
	bytesPerPixel := bands * c.getBytesPerSample(dataType)
	srcSize := srcWidth * srcHeight * bytesPerPixel
	dstSize := dstWidth * dstHeight * bytesPerPixel

	if len(data) < srcSize {
		return nil, fmt.Errorf("insufficient data: expected %d bytes, got %d", srcSize, len(data))
	}

	dstData := make([]byte, dstSize)

	// Simple nearest-neighbor resampling
	for y := 0; y < dstHeight; y++ {
		for x := 0; x < dstWidth; x++ {
			// Map destination coordinates to source coordinates
			srcX := int(float64(x) * float64(srcWidth) / float64(dstWidth))
			srcY := int(float64(y) * float64(srcHeight) / float64(dstHeight))

			// Clamp to source bounds
			if srcX >= srcWidth {
				srcX = srcWidth - 1
			}
			if srcY >= srcHeight {
				srcY = srcHeight - 1
			}

			// Copy pixel data
			srcOffset := (srcY*srcWidth + srcX) * bytesPerPixel
			dstOffset := (y*dstWidth + x) * bytesPerPixel

			copy(dstData[dstOffset:dstOffset+bytesPerPixel], data[srcOffset:srcOffset+bytesPerPixel])
		}
	}

	return dstData, nil
}
