package gocog

import (
	"bytes"
	"sync"
)

// Buffer pools for reducing GC pressure in hot paths

// byteSlicePool pools byte slices of various sizes
type byteSlicePool struct {
	// Small buffers (up to 64KB) - typical for small tiles
	small sync.Pool
	// Medium buffers (up to 256KB) - typical for 256x256 tiles
	medium sync.Pool
	// Large buffers (up to 1MB) - typical for 512x512 tiles or strips
	large sync.Pool
	// XLarge buffers (up to 4MB) - for large tiles or multiple strips
	xlarge sync.Pool
}

const (
	smallBufferSize  = 64 * 1024       // 64KB
	mediumBufferSize = 256 * 1024      // 256KB
	largeBufferSize  = 1024 * 1024     // 1MB
	xlargeBufferSize = 4 * 1024 * 1024 // 4MB
)

var bufferPool = &byteSlicePool{
	small: sync.Pool{
		New: func() interface{} {
			buf := make([]byte, smallBufferSize)
			return &buf
		},
	},
	medium: sync.Pool{
		New: func() interface{} {
			buf := make([]byte, mediumBufferSize)
			return &buf
		},
	},
	large: sync.Pool{
		New: func() interface{} {
			buf := make([]byte, largeBufferSize)
			return &buf
		},
	},
	xlarge: sync.Pool{
		New: func() interface{} {
			buf := make([]byte, xlargeBufferSize)
			return &buf
		},
	},
}

// GetBuffer returns a byte slice of at least the requested size from the pool.
// The returned slice may be larger than requested.
// Call PutBuffer when done to return it to the pool.
func GetBuffer(size int) []byte {
	if size <= smallBufferSize {
		bufPtr := bufferPool.small.Get().(*[]byte)
		return (*bufPtr)[:size]
	}
	if size <= mediumBufferSize {
		bufPtr := bufferPool.medium.Get().(*[]byte)
		return (*bufPtr)[:size]
	}
	if size <= largeBufferSize {
		bufPtr := bufferPool.large.Get().(*[]byte)
		return (*bufPtr)[:size]
	}
	if size <= xlargeBufferSize {
		bufPtr := bufferPool.xlarge.Get().(*[]byte)
		return (*bufPtr)[:size]
	}
	// For very large buffers, allocate directly
	return make([]byte, size)
}

// PutBuffer returns a buffer to the pool.
// The buffer should not be used after calling this function.
func PutBuffer(buf []byte) {
	cap := cap(buf)
	if cap == 0 {
		return
	}

	// Reset slice to full capacity for reuse
	buf = buf[:cap]

	if cap == smallBufferSize {
		bufferPool.small.Put(&buf)
	} else if cap == mediumBufferSize {
		bufferPool.medium.Put(&buf)
	} else if cap == largeBufferSize {
		bufferPool.large.Put(&buf)
	} else if cap == xlargeBufferSize {
		bufferPool.xlarge.Put(&buf)
	}
	// Don't pool non-standard sizes or very large buffers
}

// bytesBufferPool pools bytes.Buffer instances
var bytesBufferPool = sync.Pool{
	New: func() interface{} {
		return new(bytes.Buffer)
	},
}

// GetBytesBuffer returns a bytes.Buffer from the pool
func GetBytesBuffer() *bytes.Buffer {
	buf := bytesBufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	return buf
}

// PutBytesBuffer returns a bytes.Buffer to the pool
func PutBytesBuffer(buf *bytes.Buffer) {
	if buf == nil {
		return
	}
	// Don't pool very large buffers as they consume too much memory
	if buf.Cap() > xlargeBufferSize {
		return
	}
	bytesBufferPool.Put(buf)
}

// uint64SlicePool pools uint64 slices for decoded raster data
type uint64SlicePool struct {
	// Pool for typical tile sizes
	tile256 sync.Pool // 256*256*4 = 262144 uint64s
	tile512 sync.Pool // 512*512*4 = 1048576 uint64s
}

const (
	tile256Size = 256 * 256 * 4 // 4 bands max for typical case
	tile512Size = 512 * 512 * 4
)

var uint64Pool = &uint64SlicePool{
	tile256: sync.Pool{
		New: func() interface{} {
			buf := make([]uint64, tile256Size)
			return &buf
		},
	},
	tile512: sync.Pool{
		New: func() interface{} {
			buf := make([]uint64, tile512Size)
			return &buf
		},
	},
}

// GetUint64Slice returns a uint64 slice of at least the requested size
func GetUint64Slice(size int) []uint64 {
	if size <= tile256Size {
		bufPtr := uint64Pool.tile256.Get().(*[]uint64)
		return (*bufPtr)[:size]
	}
	if size <= tile512Size {
		bufPtr := uint64Pool.tile512.Get().(*[]uint64)
		return (*bufPtr)[:size]
	}
	// For larger slices, allocate directly
	return make([]uint64, size)
}

// PutUint64Slice returns a uint64 slice to the pool
func PutUint64Slice(buf []uint64) {
	cap := cap(buf)
	if cap == 0 {
		return
	}

	// Reset slice to full capacity
	buf = buf[:cap]

	if cap == tile256Size {
		uint64Pool.tile256.Put(&buf)
	} else if cap == tile512Size {
		uint64Pool.tile512.Put(&buf)
	}
	// Don't pool non-standard sizes
}

// tileWorkPool pools tileWork structs for parallel tile processing
type tileWork struct {
	tileX, tileY int
	tileIndex    int
	tileOffset   uint32
	tileSize     uint32
	tileData     []byte
	decompressed []byte
	err          error
}

var tileWorkPool = sync.Pool{
	New: func() interface{} {
		return &tileWork{}
	},
}

// GetTileWork returns a tileWork struct from the pool
func GetTileWork() *tileWork {
	tw := tileWorkPool.Get().(*tileWork)
	// Reset fields
	tw.tileX = 0
	tw.tileY = 0
	tw.tileIndex = 0
	tw.tileOffset = 0
	tw.tileSize = 0
	tw.tileData = nil
	tw.decompressed = nil
	tw.err = nil
	return tw
}

// PutTileWork returns a tileWork struct to the pool
func PutTileWork(tw *tileWork) {
	if tw == nil {
		return
	}
	tileWorkPool.Put(tw)
}
