package gocog

import (
	"fmt"
	"io"
	"sync"

	"github.com/valyala/fasthttp"
)

// Default read-ahead buffer size (64KB) for sequential access optimization
const defaultReadAheadSize = 64 * 1024

// HTTPRangeReader implements io.ReadSeeker for reading files via HTTP range requests
// It includes a read-ahead buffer to optimize sequential access patterns
type HTTPRangeReader struct {
	url    string
	client *fasthttp.Client
	size   int64
	mu     sync.Mutex
	pos    int64

	// Read-ahead buffer for sequential access optimization
	buffer        []byte
	bufferStart   int64 // Start position of buffer in file
	bufferEnd     int64 // End position of buffer in file (exclusive)
	readAheadSize int   // Size of read-ahead buffer
}

// NewHTTPRangeReader creates a new HTTP range reader
func NewHTTPRangeReader(url string, client *fasthttp.Client) *HTTPRangeReader {
	rr := &HTTPRangeReader{
		url:           url,
		client:        client,
		pos:           0,
		readAheadSize: defaultReadAheadSize,
		bufferStart:   -1,
		bufferEnd:     -1,
	}

	// Get file size
	rr.size = rr.getSize()

	return rr
}

// NewHTTPRangeReaderWithReadAhead creates a new HTTP range reader with custom read-ahead size
func NewHTTPRangeReaderWithReadAhead(url string, client *fasthttp.Client, readAheadSize int) *HTTPRangeReader {
	rr := NewHTTPRangeReader(url, client)
	if readAheadSize > 0 {
		rr.readAheadSize = readAheadSize
	}
	return rr
}

// SetReadAheadSize sets the read-ahead buffer size
// Larger values improve sequential read performance but use more memory
func (rr *HTTPRangeReader) SetReadAheadSize(size int) {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	if size > 0 {
		rr.readAheadSize = size
	}
}

// getSize gets the file size using HEAD request
func (rr *HTTPRangeReader) getSize() int64 {
	if rr.client == nil {
		return -1
	}

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	req.SetRequestURI(rr.url)
	req.Header.SetMethod("HEAD")

	err := rr.client.Do(req, resp)
	if err != nil {
		return -1
	}

	contentLength := resp.Header.ContentLength()
	if contentLength > 0 {
		return int64(contentLength)
	}

	return -1
}

// Read reads data from the current position
// Uses read-ahead buffering to optimize sequential access patterns
func (rr *HTTPRangeReader) Read(p []byte) (n int, err error) {
	rr.mu.Lock()
	defer rr.mu.Unlock()

	if rr.pos >= rr.size && rr.size > 0 {
		return 0, io.EOF
	}

	// Calculate how much to read
	toRead := len(p)
	if rr.size > 0 && rr.pos+int64(toRead) > rr.size {
		toRead = int(rr.size - rr.pos)
	}

	// Check if data is already in buffer
	if rr.buffer != nil && rr.pos >= rr.bufferStart && rr.pos < rr.bufferEnd {
		// Some or all data is in buffer
		bufferOffset := int(rr.pos - rr.bufferStart)
		availableInBuffer := int(rr.bufferEnd - rr.pos)

		if availableInBuffer >= toRead {
			// All data is in buffer
			n = copy(p[:toRead], rr.buffer[bufferOffset:bufferOffset+toRead])
			rr.pos += int64(n)
			return n, nil
		}

		// Partial data in buffer - copy what we have
		n = copy(p[:availableInBuffer], rr.buffer[bufferOffset:])
		rr.pos += int64(n)

		// Read remaining data
		remaining := toRead - n
		nn, err := rr.readFromNetwork(p[n:n+remaining], remaining)
		n += nn
		return n, err
	}

	// Data not in buffer - fetch with read-ahead
	return rr.readWithReadAhead(p, toRead)
}

// readWithReadAhead fetches data with read-ahead buffering
func (rr *HTTPRangeReader) readWithReadAhead(p []byte, toRead int) (n int, err error) {
	// Determine read-ahead size
	readSize := rr.readAheadSize
	if readSize < toRead {
		readSize = toRead
	}

	// Cap read size to not exceed file size
	if rr.size > 0 && rr.pos+int64(readSize) > rr.size {
		readSize = int(rr.size - rr.pos)
	}

	// Fetch data from network
	data, err := rr.fetchRange(rr.pos, rr.pos+int64(readSize)-1)
	if err != nil {
		return 0, err
	}

	// Store in buffer for future reads
	if len(data) > toRead {
		// Allocate or reuse buffer
		if cap(rr.buffer) >= len(data) {
			rr.buffer = rr.buffer[:len(data)]
		} else {
			rr.buffer = make([]byte, len(data))
		}
		copy(rr.buffer, data)
		rr.bufferStart = rr.pos
		rr.bufferEnd = rr.pos + int64(len(data))
	}

	// Copy requested data to output
	if len(data) < toRead {
		toRead = len(data)
	}
	n = copy(p[:toRead], data[:toRead])

	if n == 0 && len(data) == 0 {
		return 0, io.EOF
	}

	rr.pos += int64(n)
	return n, nil
}

// readFromNetwork reads directly from network without read-ahead
func (rr *HTTPRangeReader) readFromNetwork(p []byte, toRead int) (n int, err error) {
	data, err := rr.fetchRange(rr.pos, rr.pos+int64(toRead)-1)
	if err != nil {
		return 0, err
	}

	if len(data) < toRead {
		toRead = len(data)
	}
	n = copy(p[:toRead], data[:toRead])
	rr.pos += int64(n)
	return n, nil
}

// fetchRange fetches a byte range from the server
func (rr *HTTPRangeReader) fetchRange(start, end int64) ([]byte, error) {
	if rr.size > 0 && end >= rr.size {
		end = rr.size - 1
	}

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	req.SetRequestURI(rr.url)
	req.Header.SetMethod("GET")
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))

	err := rr.client.Do(req, resp)
	if err != nil {
		return nil, err
	}

	statusCode := resp.StatusCode()
	if statusCode != fasthttp.StatusPartialContent && statusCode != fasthttp.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", statusCode)
	}

	// Copy body since response will be released
	body := resp.Body()
	result := make([]byte, len(body))
	copy(result, body)

	return result, nil
}

// Seek sets the offset for the next Read
// Invalidates the read-ahead buffer if seeking outside the buffered range
func (rr *HTTPRangeReader) Seek(offset int64, whence int) (int64, error) {
	rr.mu.Lock()
	defer rr.mu.Unlock()

	var newPos int64
	switch whence {
	case io.SeekStart:
		newPos = offset
	case io.SeekCurrent:
		newPos = rr.pos + offset
	case io.SeekEnd:
		if rr.size < 0 {
			return 0, fmt.Errorf("cannot seek from end: file size unknown")
		}
		newPos = rr.size + offset
	default:
		return 0, fmt.Errorf("invalid whence: %d", whence)
	}

	if newPos < 0 {
		return 0, fmt.Errorf("negative position: %d", newPos)
	}

	// Check if new position is outside buffer range
	if rr.buffer != nil && (newPos < rr.bufferStart || newPos >= rr.bufferEnd) {
		// Invalidate buffer for non-sequential access
		rr.bufferStart = -1
		rr.bufferEnd = -1
	}

	rr.pos = newPos
	return rr.pos, nil
}

// ClearBuffer clears the read-ahead buffer to free memory
func (rr *HTTPRangeReader) ClearBuffer() {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	rr.buffer = nil
	rr.bufferStart = -1
	rr.bufferEnd = -1
}

// Size returns the file size, or -1 if unknown
func (rr *HTTPRangeReader) Size() int64 {
	return rr.size
}
