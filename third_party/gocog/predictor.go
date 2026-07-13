package gocog

import (
	"encoding/binary"
	"fmt"
)

// TIFF Predictor tag (317) values.
const (
	predictorNone       = 1
	predictorHorizontal = 2
	predictorFloating   = 3
)

// readPredictor returns the TIFF Predictor (tag 317) value, defaulting to 1
// (none) when the tag is absent.
//
// ortus-local extension: upstream gocog ignores tag 317 entirely, which silently
// mis-decodes any COG written with a predictor. This restores correct decoding
// for the horizontal integer predictor (PREDICTOR=2), letting the elevation
// pipeline use it to roughly halve tile size.
func readPredictor(ifd *IFD) int {
	tag := ifd.Tags[317]
	if tag == nil {
		return predictorNone
	}
	switch v := tag.Value.(type) {
	case uint16:
		return int(v)
	case []uint16:
		if len(v) > 0 {
			return int(v[0])
		}
	case uint32:
		return int(v)
	case []uint32:
		if len(v) > 0 {
			return int(v[0])
		}
	// Predictor is spec'd as an unsigned SHORT, but guard against a mis-typed
	// (signed) tag so a real predictor is never silently skipped → corruption.
	case int16:
		return int(v)
	case int32:
		return int(v)
	}
	return predictorNone
}

// applyHorizontalPredictor reverses TIFF horizontal differencing (Predictor=2)
// in place on a decompressed tile or strip. Samples are stored row-major and
// band-interleaved-by-pixel: each row holds cols pixels of `bands` samples. The
// reversal adds each sample to the same-band sample of the previous pixel, using
// modular (wrapping) arithmetic — correct for both signed and unsigned integers.
//
// Predictor 1 (none) is a no-op. Predictor 3 (floating point) is not supported
// (the elevation pipeline uses integer bands only) and returns an error rather
// than corrupt data.
func applyHorizontalPredictor(data []byte, predictor, cols, rows, bands, bytesPerSample int, bo binary.ByteOrder) error {
	switch predictor {
	case predictorNone:
		return nil
	case predictorHorizontal:
		// handled below
	case predictorFloating:
		return fmt.Errorf("gocog: floating-point predictor (3) not supported")
	default:
		return fmt.Errorf("gocog: unknown TIFF predictor %d", predictor)
	}

	samplesPerRow := cols * bands
	stride := samplesPerRow * bytesPerSample
	if stride == 0 || len(data) < rows*stride {
		return fmt.Errorf("gocog: predictor: data too small (%d) for %dx%d x%d @ %dB", len(data), cols, rows, bands, bytesPerSample)
	}

	for r := 0; r < rows; r++ {
		row := data[r*stride : r*stride+stride]
		switch bytesPerSample {
		case 1:
			for i := bands; i < samplesPerRow; i++ {
				row[i] += row[i-bands]
			}
		case 2:
			for i := bands; i < samplesPerRow; i++ {
				off, prev := i*2, (i-bands)*2
				v := bo.Uint16(row[off:off+2]) + bo.Uint16(row[prev:prev+2])
				bo.PutUint16(row[off:off+2], v)
			}
		case 4:
			for i := bands; i < samplesPerRow; i++ {
				off, prev := i*4, (i-bands)*4
				v := bo.Uint32(row[off:off+4]) + bo.Uint32(row[prev:prev+4])
				bo.PutUint32(row[off:off+4], v)
			}
		default:
			return fmt.Errorf("gocog: horizontal predictor: unsupported sample width %d bytes", bytesPerSample)
		}
	}
	return nil
}
