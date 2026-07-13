package gocog

import (
	"encoding/binary"
	"testing"
)

// forwardHorizontal applies TIFF horizontal differencing (the encode direction)
// so the test can round-trip against applyHorizontalPredictor (the decode).
func forwardHorizontal(data []byte, cols, rows, bands, bps int, bo binary.ByteOrder) {
	spr := cols * bands
	stride := spr * bps
	for r := 0; r < rows; r++ {
		row := data[r*stride : r*stride+stride]
		for i := spr - 1; i >= bands; i-- {
			switch bps {
			case 1:
				row[i] -= row[i-bands]
			case 2:
				off, prev := i*2, (i-bands)*2
				bo.PutUint16(row[off:off+2], bo.Uint16(row[off:off+2])-bo.Uint16(row[prev:prev+2]))
			case 4:
				off, prev := i*4, (i-bands)*4
				bo.PutUint32(row[off:off+4], bo.Uint32(row[off:off+4])-bo.Uint32(row[prev:prev+4]))
			}
		}
	}
}

func TestReadPredictorDefault(t *testing.T) {
	ifd := &IFD{Tags: map[uint16]*Tag{}}
	if got := readPredictor(ifd); got != 1 {
		t.Errorf("absent tag → %d, want 1", got)
	}
	ifd.Tags[317] = &Tag{Value: uint16(2)}
	if got := readPredictor(ifd); got != 2 {
		t.Errorf("tag 2 → %d, want 2", got)
	}
	ifd.Tags[317] = &Tag{Value: []uint16{2}}
	if got := readPredictor(ifd); got != 2 {
		t.Errorf("tag []2 → %d, want 2", got)
	}
}

// TestHorizontalRoundTripInt16 encodes known Int16 elevations with horizontal
// differencing, then reverses it — the exact path a PREDICTOR=2 COG exercises.
func TestHorizontalRoundTripInt16(t *testing.T) {
	bo := binary.LittleEndian
	cols, rows, bands, bps := 5, 2, 1, 2
	orig := []int16{100, 101, 250, 249, -5, 900, 901, 3000, -32000, 32000}
	buf := make([]byte, len(orig)*bps)
	for i, v := range orig {
		bo.PutUint16(buf[i*2:], uint16(v))
	}
	forwardHorizontal(buf, cols, rows, bands, bps, bo)
	if err := applyHorizontalPredictor(buf, 2, cols, rows, bands, bps, bo); err != nil {
		t.Fatalf("apply: %v", err)
	}
	for i, want := range orig {
		if got := int16(bo.Uint16(buf[i*2:])); got != want {
			t.Errorf("sample %d = %d, want %d", i, got, want)
		}
	}
}

// TestHorizontalRoundTripMultiBand covers band-interleaved 8-bit data.
func TestHorizontalRoundTripMultiBand(t *testing.T) {
	bo := binary.BigEndian
	cols, rows, bands, bps := 3, 2, 3, 1 // RGB
	orig := []byte{10, 20, 30, 11, 22, 33, 9, 19, 29, 200, 100, 50, 201, 101, 51, 255, 0, 128}
	buf := append([]byte(nil), orig...)
	forwardHorizontal(buf, cols, rows, bands, bps, bo)
	if err := applyHorizontalPredictor(buf, 2, cols, rows, bands, bps, bo); err != nil {
		t.Fatalf("apply: %v", err)
	}
	for i := range orig {
		if buf[i] != orig[i] {
			t.Errorf("byte %d = %d, want %d", i, buf[i], orig[i])
		}
	}
}

func TestPredictorNoneAndFloatingAndBadWidth(t *testing.T) {
	bo := binary.LittleEndian
	buf := []byte{1, 2, 3, 4}
	orig := append([]byte(nil), buf...)
	if err := applyHorizontalPredictor(buf, 1, 4, 1, 1, 1, bo); err != nil {
		t.Fatalf("predictor 1 should be a no-op: %v", err)
	}
	for i := range orig {
		if buf[i] != orig[i] {
			t.Fatalf("predictor 1 mutated data")
		}
	}
	if err := applyHorizontalPredictor(buf, 3, 4, 1, 1, 1, bo); err == nil {
		t.Error("floating predictor (3) must be rejected")
	}
	if err := applyHorizontalPredictor(buf, 2, 1, 1, 1, 8, bo); err == nil {
		t.Error("8-byte sample width must be rejected")
	}
}
