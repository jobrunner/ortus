package gocog

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/paulmach/orb"
)

// GeoTIFF tag IDs
const (
	TagModelPixelScale     = 33550
	TagModelTiepoint       = 33922
	TagModelTransformation = 34264
	TagGeoKeyDirectory     = 34735
	TagGeoDoubleParams     = 34736
	TagGeoAsciiParams      = 34737
)

// GeoKeys
const (
	GTModelTypeGeoKey     = 1024
	GTModelTypeGeographic = 1
	GTModelTypeProjected  = 2

	GTRasterTypeGeoKey       = 1025
	GTRasterTypePixelIsArea  = 1
	GTRasterTypePixelIsPoint = 2

	GeographicTypeGeoKey    = 2048
	GeogCitationGeoKey      = 2049
	GeogGeodeticDatumGeoKey = 2050
	GeogPrimeMeridianGeoKey = 2051
	GeogLinearUnitsGeoKey   = 2052
	GeogAngularUnitsGeoKey  = 2053
	GeogSemiMajorAxisGeoKey = 2057
	GeogSemiMinorAxisGeoKey = 2058
	GeogInvFlatteningGeoKey = 2059

	ProjectedCSTypeGeoKey          = 3072
	PCSCitationGeoKey              = 3073
	ProjectionGeoKey               = 3074
	ProjCoordTransGeoKey           = 3075
	ProjLinearUnitsGeoKey          = 3076
	ProjStdParallel1GeoKey         = 3078
	ProjStdParallel2GeoKey         = 3079
	ProjNatOriginLongGeoKey        = 3080
	ProjNatOriginLatGeoKey         = 3081
	ProjFalseEastingGeoKey         = 3082
	ProjFalseNorthingGeoKey        = 3083
	ProjFalseOriginLongGeoKey      = 3084
	ProjFalseOriginLatGeoKey       = 3085
	ProjFalseOriginEastingGeoKey   = 3086
	ProjFalseOriginNorthingGeoKey  = 3087
	ProjCenterLongGeoKey           = 3088
	ProjCenterLatGeoKey            = 3089
	ProjCenterEastingGeoKey        = 3090
	ProjCenterNorthingGeoKey       = 3091
	ProjScaleAtNatOriginGeoKey     = 3092
	ProjScaleAtCenterGeoKey        = 3093
	ProjAzimuthAngleGeoKey         = 3094
	ProjStraightVertPoleLongGeoKey = 3095
)

// GeoTIFF metadata
type GeoTIFFMetadata struct {
	PixelScale                [3]float64
	TiePoints                 []TiePoint
	Transformation            [16]float64
	GeoKeys                   map[uint16]interface{}
	GeoDoubleParams           []float64
	GeoAsciiParams            string
	CRS                       string
	Width                     int
	Height                    int
	BandCount                 int
	DataType                  DataType
	PhotometricInterpretation uint16 // Tag 262: 0=WhiteIsZero, 1=BlackIsZero, 2=RGB, 3=Palette
}

// TiePoint represents a georeferencing tie point
type TiePoint struct {
	PixelX, PixelY, PixelZ float64
	GeoX, GeoY, GeoZ       float64
}

// GeoTIFFReader reads GeoTIFF metadata
type GeoTIFFReader struct {
	tr       *TIFFReader
	metadata *GeoTIFFMetadata
}

// NewGeoTIFFReader creates a new GeoTIFF reader
func NewGeoTIFFReader(tr *TIFFReader) (*GeoTIFFReader, error) {
	gtr := &GeoTIFFReader{
		tr: tr,
		metadata: &GeoTIFFMetadata{
			GeoKeys: make(map[uint16]interface{}),
		},
	}

	if err := gtr.readMetadata(0); err != nil {
		return nil, err
	}

	return gtr, nil
}

// readMetadata reads GeoTIFF metadata from the specified IFD
func (gtr *GeoTIFFReader) readMetadata(ifdIndex int) error {
	ifd := gtr.tr.GetIFD(ifdIndex)
	if ifd == nil {
		return fmt.Errorf("IFD %d not found", ifdIndex)
	}

	// Read image dimensions
	if tag := ifd.Tags[256]; tag != nil { // ImageWidth
		if val, ok := tag.Value.(uint32); ok {
			gtr.metadata.Width = int(val)
		} else if val, ok := tag.Value.([]uint32); ok && len(val) > 0 {
			gtr.metadata.Width = int(val[0])
		} else if val, ok := tag.Value.(uint16); ok {
			// Some TIFF files use SHORT instead of LONG
			gtr.metadata.Width = int(val)
		} else if val, ok := tag.Value.([]uint16); ok && len(val) > 0 {
			gtr.metadata.Width = int(val[0])
		}
	}

	if tag := ifd.Tags[257]; tag != nil { // ImageLength
		if val, ok := tag.Value.(uint32); ok {
			gtr.metadata.Height = int(val)
		} else if val, ok := tag.Value.([]uint32); ok && len(val) > 0 {
			gtr.metadata.Height = int(val[0])
		} else if val, ok := tag.Value.(uint16); ok {
			// Some TIFF files use SHORT instead of LONG
			gtr.metadata.Height = int(val)
		} else if val, ok := tag.Value.([]uint16); ok && len(val) > 0 {
			gtr.metadata.Height = int(val[0])
		}
	}

	// Read samples per pixel
	if tag := ifd.Tags[277]; tag != nil { // SamplesPerPixel
		if val, ok := tag.Value.(uint16); ok {
			gtr.metadata.BandCount = int(val)
		} else if val, ok := tag.Value.([]uint16); ok && len(val) > 0 {
			gtr.metadata.BandCount = int(val[0])
		}
	} else {
		gtr.metadata.BandCount = 1 // Default
	}

	// Read data type from BitsPerSample and SampleFormat
	gtr.metadata.DataType = gtr.determineDataType(ifd)

	// Read PhotometricInterpretation (tag 262)
	// Default to RGB (2) if not specified
	gtr.metadata.PhotometricInterpretation = 2
	if tag := ifd.Tags[262]; tag != nil { // PhotometricInterpretation
		if val, ok := tag.Value.(uint16); ok {
			gtr.metadata.PhotometricInterpretation = val
		} else if val, ok := tag.Value.(uint32); ok {
			gtr.metadata.PhotometricInterpretation = uint16(val)
		} else if val, ok := tag.Value.([]uint16); ok && len(val) > 0 {
			gtr.metadata.PhotometricInterpretation = val[0]
		} else if val, ok := tag.Value.([]uint32); ok && len(val) > 0 {
			gtr.metadata.PhotometricInterpretation = uint16(val[0])
		}
	}

	// Read ModelPixelScale
	if tag := ifd.Tags[TagModelPixelScale]; tag != nil {
		if values, ok := tag.Value.([]float64); ok && len(values) >= 3 {
			copy(gtr.metadata.PixelScale[:], values[:3])
		} else if values, ok := tag.Value.([]float32); ok && len(values) >= 3 {
			gtr.metadata.PixelScale[0] = float64(values[0])
			gtr.metadata.PixelScale[1] = float64(values[1])
			gtr.metadata.PixelScale[2] = float64(values[2])
		}
	}

	// Read ModelTiepoint
	if tag := ifd.Tags[TagModelTiepoint]; tag != nil {
		if values, ok := tag.Value.([]float64); ok {
			gtr.metadata.TiePoints = parseTiePoints(values)
		} else if values, ok := tag.Value.([]float32); ok {
			float64Values := make([]float64, len(values))
			for i, v := range values {
				float64Values[i] = float64(v)
			}
			gtr.metadata.TiePoints = parseTiePoints(float64Values)
		}
	}

	// Read ModelTransformation
	if tag := ifd.Tags[TagModelTransformation]; tag != nil {
		if values, ok := tag.Value.([]float64); ok && len(values) >= 16 {
			copy(gtr.metadata.Transformation[:], values[:16])
		} else if values, ok := tag.Value.([]float32); ok && len(values) >= 16 {
			for i := 0; i < 16; i++ {
				gtr.metadata.Transformation[i] = float64(values[i])
			}
		}
	}

	// Read GeoKeys
	if err := gtr.readGeoKeys(ifd); err != nil {
		return fmt.Errorf("failed to read GeoKeys: %w", err)
	}

	// Determine CRS
	gtr.metadata.CRS = gtr.determineCRS()

	return nil
}

// determineDataType determines the data type from BitsPerSample and SampleFormat tags
func (gtr *GeoTIFFReader) determineDataType(ifd *IFD) DataType {
	// Read BitsPerSample (tag 258)
	bitsPerSample := uint16(8)            // Default to 8-bit
	if tag := ifd.Tags[258]; tag != nil { // BitsPerSample
		if val, ok := tag.Value.(uint16); ok {
			bitsPerSample = val
		} else if val, ok := tag.Value.([]uint16); ok && len(val) > 0 {
			bitsPerSample = val[0]
		} else if val, ok := tag.Value.(uint32); ok {
			bitsPerSample = uint16(val)
		} else if val, ok := tag.Value.([]uint32); ok && len(val) > 0 {
			bitsPerSample = uint16(val[0])
		}
	}

	// Read SampleFormat (tag 339) - default to unsigned integer (1)
	sampleFormat := uint16(1)             // 1 = unsigned integer data
	if tag := ifd.Tags[339]; tag != nil { // SampleFormat
		if val, ok := tag.Value.(uint16); ok {
			sampleFormat = val
		} else if val, ok := tag.Value.([]uint16); ok && len(val) > 0 {
			sampleFormat = val[0]
		} else if val, ok := tag.Value.(uint32); ok {
			sampleFormat = uint16(val)
		} else if val, ok := tag.Value.([]uint32); ok && len(val) > 0 {
			sampleFormat = uint16(val[0])
		}
	}

	// Map BitsPerSample + SampleFormat to DataType
	// SampleFormat: 1 = unsigned integer, 2 = signed integer, 3 = IEEE floating point
	switch {
	case bitsPerSample == 8 && sampleFormat == 1:
		return DTByte // 8-bit unsigned integer
	case bitsPerSample == 8 && sampleFormat == 2:
		return DTSByte // 8-bit signed integer
	case bitsPerSample == 16 && sampleFormat == 1:
		return DTSShort // 16-bit unsigned integer
	case bitsPerSample == 16 && sampleFormat == 2:
		return DTSShortS // 16-bit signed integer
	case bitsPerSample == 32 && sampleFormat == 1:
		return DTSLong // 32-bit unsigned integer
	case bitsPerSample == 32 && sampleFormat == 2:
		return DTSLongS // 32-bit signed integer
	case bitsPerSample == 32 && sampleFormat == 3:
		return DTFloat // 32-bit IEEE floating point
	case bitsPerSample == 64 && sampleFormat == 3:
		return DTDouble // 64-bit IEEE floating point
	default:
		// Default to 8-bit unsigned if unknown
		return DTByte
	}
}

// parseTiePoints parses tie point values
func parseTiePoints(values []float64) []TiePoint {
	if len(values) < 6 {
		return nil
	}

	tiePoints := make([]TiePoint, 0, len(values)/6)
	for i := 0; i < len(values); i += 6 {
		if i+5 < len(values) {
			tiePoints = append(tiePoints, TiePoint{
				PixelX: values[i],
				PixelY: values[i+1],
				PixelZ: values[i+2],
				GeoX:   values[i+3],
				GeoY:   values[i+4],
				GeoZ:   values[i+5],
			})
		}
	}
	return tiePoints
}

// readGeoKeys reads GeoKey directory
func (gtr *GeoTIFFReader) readGeoKeys(ifd *IFD) error {
	geoKeyTag := ifd.Tags[TagGeoKeyDirectory]
	if geoKeyTag == nil {
		return nil // No GeoKeys
	}

	// GeoKeyDirectory is stored as SHORT values
	var geoKeyValues []uint16
	if values, ok := geoKeyTag.Value.([]uint16); ok {
		geoKeyValues = values
	} else if val, ok := geoKeyTag.Value.(uint16); ok {
		geoKeyValues = []uint16{val}
	} else {
		return fmt.Errorf("invalid GeoKeyDirectory format")
	}

	if len(geoKeyValues) < 4 {
		return fmt.Errorf("GeoKeyDirectory too short")
	}

	// First 4 values are header: version, revision, minor revision, number of keys
	numKeys := int(geoKeyValues[3])

	// Read GeoDoubleParams if needed (load on demand if not already loaded)
	var geoDoubleParams []float64
	if tag := ifd.Tags[TagGeoDoubleParams]; tag != nil {
		if tag.Value == nil && tag.IsOffset {
			// Load on demand
			if err := gtr.tr.ReadTagValue(ifd, TagGeoDoubleParams); err == nil {
				// Retry after loading
			}
		}
		if values, ok := tag.Value.([]float64); ok {
			geoDoubleParams = values
		} else if values, ok := tag.Value.([]float32); ok {
			geoDoubleParams = make([]float64, len(values))
			for i, v := range values {
				geoDoubleParams[i] = float64(v)
			}
		}
	}
	gtr.metadata.GeoDoubleParams = geoDoubleParams

	// Read GeoAsciiParams if needed (load on demand if not already loaded)
	if tag := ifd.Tags[TagGeoAsciiParams]; tag != nil {
		if tag.Value == nil && tag.IsOffset {
			// Load on demand
			if err := gtr.tr.ReadTagValue(ifd, TagGeoAsciiParams); err == nil {
				// Retry after loading
			}
		}
		if str, ok := tag.Value.(string); ok {
			gtr.metadata.GeoAsciiParams = str
		}
	}

	// Parse GeoKeys (each key is 4 SHORTs: keyID, location, count, value/offset)
	for i := 4; i < len(geoKeyValues) && (i-4)/4 < numKeys; i += 4 {
		if i+3 >= len(geoKeyValues) {
			break
		}

		keyID := uint16(geoKeyValues[i])
		location := uint16(geoKeyValues[i+1])
		count := uint16(geoKeyValues[i+2])
		valueOrOffset := geoKeyValues[i+3]

		var keyValue interface{}

		switch location {
		case 0: // Value stored directly
			if count == 1 {
				keyValue = valueOrOffset
			} else {
				// Multi-value keys are rare, handle if needed
				keyValue = valueOrOffset
			}
		case 34736: // GeoDoubleParams
			if int(valueOrOffset) < len(geoDoubleParams) {
				if count == 1 {
					keyValue = geoDoubleParams[valueOrOffset]
				} else {
					end := int(valueOrOffset) + int(count)
					if end <= len(geoDoubleParams) {
						keyValue = geoDoubleParams[valueOrOffset:end]
					}
				}
			}
		case 34737: // GeoAsciiParams
			asciiParams := gtr.metadata.GeoAsciiParams
			if int(valueOrOffset) < len(asciiParams) {
				end := int(valueOrOffset) + int(count) - 1 // -1 to exclude null terminator
				if end > len(asciiParams) {
					end = len(asciiParams)
				}
				keyValue = asciiParams[valueOrOffset:end]
			}
		}

		if keyValue != nil {
			gtr.metadata.GeoKeys[keyID] = keyValue
		}
	}

	return nil
}

// determineCRS determines the CRS from GeoKeys
func (gtr *GeoTIFFReader) determineCRS() string {
	// Check for ProjectedCSTypeGeoKey first
	if projCSType, ok := gtr.metadata.GeoKeys[ProjectedCSTypeGeoKey]; ok {
		if code, ok := projCSType.(uint16); ok && code != 0 {
			return fmt.Sprintf("EPSG:%d", code)
		}
	}

	// Check for GeographicTypeGeoKey
	if geoType, ok := gtr.metadata.GeoKeys[GeographicTypeGeoKey]; ok {
		if code, ok := geoType.(uint16); ok && code != 0 {
			// Common geographic codes
			if code == 4326 {
				return "EPSG:4326"
			}
			return fmt.Sprintf("EPSG:%d", code)
		}
	}

	return ""
}

// pixelToGeo converts pixel coordinates to geographic coordinates
func (gtr *GeoTIFFReader) pixelToGeo(pixelX, pixelY float64) (float64, float64) {
	// Use ModelTransformation if available (affine transformation matrix)
	if gtr.hasTransformation() {
		return gtr.transformPixel(pixelX, pixelY)
	}

	// Use ModelTiepoint and ModelPixelScale
	if len(gtr.metadata.TiePoints) > 0 && gtr.metadata.PixelScale[0] != 0 {
		tp := gtr.metadata.TiePoints[0]

		geoX := tp.GeoX + (pixelX-tp.PixelX)*gtr.metadata.PixelScale[0]
		geoY := tp.GeoY - (pixelY-tp.PixelY)*gtr.metadata.PixelScale[1] // Note: Y is inverted

		return geoX, geoY
	}

	return 0, 0
}

// hasTransformation checks if ModelTransformation is available
func (gtr *GeoTIFFReader) hasTransformation() bool {
	// Check if transformation matrix is non-zero
	for i := 0; i < 16; i++ {
		if gtr.metadata.Transformation[i] != 0 {
			return true
		}
	}
	return false
}

// transformPixel transforms pixel coordinates using the transformation matrix
func (gtr *GeoTIFFReader) transformPixel(pixelX, pixelY float64) (float64, float64) {
	t := gtr.metadata.Transformation

	// Transformation matrix format:
	// [0] [1] [2] [3]
	// [4] [5] [6] [7]
	// [8] [9] [10] [11]
	// [12] [13] [14] [15]

	geoX := t[0]*pixelX + t[1]*pixelY + t[3]
	geoY := t[4]*pixelX + t[5]*pixelY + t[7]

	return geoX, geoY
}

// Bounds calculates the geographic bounding box
func (gtr *GeoTIFFReader) Bounds() orb.Bound {
	if gtr.metadata.Width == 0 || gtr.metadata.Height == 0 {
		return orb.Bound{}
	}

	// Get corners
	topLeftX, topLeftY := gtr.pixelToGeo(0, 0)
	topRightX, topRightY := gtr.pixelToGeo(float64(gtr.metadata.Width), 0)
	bottomLeftX, bottomLeftY := gtr.pixelToGeo(0, float64(gtr.metadata.Height))
	bottomRightX, bottomRightY := gtr.pixelToGeo(float64(gtr.metadata.Width), float64(gtr.metadata.Height))

	minX := math.Min(math.Min(topLeftX, topRightX), math.Min(bottomLeftX, bottomRightX))
	maxX := math.Max(math.Max(topLeftX, topRightX), math.Max(bottomLeftX, bottomRightX))
	minY := math.Min(math.Min(topLeftY, topRightY), math.Min(bottomLeftY, bottomRightY))
	maxY := math.Max(math.Max(topLeftY, topRightY), math.Max(bottomLeftY, bottomRightY))

	return orb.Bound{
		Min: orb.Point{minX, minY},
		Max: orb.Point{maxX, maxY},
	}
}

// GetMetadata returns the GeoTIFF metadata
func (gtr *GeoTIFFReader) GetMetadata() *GeoTIFFMetadata {
	return gtr.metadata
}

// ReadGeoKeys reads GeoKeys from a tag value
func ReadGeoKeys(r io.ReadSeeker, byteOrder binary.ByteOrder, offset uint32, count uint32) ([]uint16, error) {
	oldPos, _ := r.Seek(0, io.SeekCurrent)
	defer r.Seek(oldPos, io.SeekStart)

	if _, err := r.Seek(int64(offset), io.SeekStart); err != nil {
		return nil, err
	}

	values := make([]uint16, count)
	if err := binary.Read(r, byteOrder, values); err != nil {
		return nil, err
	}

	return values, nil
}

// ParseEPSGCode extracts EPSG code from CRS string
func ParseEPSGCode(crs string) (int, error) {
	if strings.HasPrefix(crs, "EPSG:") {
		code, err := strconv.Atoi(crs[5:])
		if err != nil {
			return 0, err
		}
		return code, nil
	}
	return 0, fmt.Errorf("invalid CRS format: %s", crs)
}
