package server

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
)

var (
	ErrMaximumSizeExceeded = errors.New("maximum size exceeded")
	ErrWriteAfterRead      = errors.New("write after read")
)

// Buffer is a struct that does in memory buffering as well as saving to disk
// when a certain limit is reached. The data can then be read when it's all
// available.
//
// Useful when you want to buffer data that might be too big to fit into memory.
//
// Implements io.Writer, io.Reader and io.Closer interfaces.
type Buffer struct {
	// maxBytes is the maximum number of bytes that can be written to the Buffer.
	// Exceeding this limit will return a ErrMaximumSizeExceeded error.
	maxBytes int64
	// maxMemBytes represents the maximum number of bytes that can be stored into
	// memory. If this limit is exceeded then the rest of the bytes are written to
	// disk.
	maxMemBytes int64

	// memoryBuffer stores the in memory bytes.
	memoryBuffer bytes.Buffer
	// memBytesWritten is a counter for the length of the bytes stored into memory.
	memBytesWritten int64
	// diskBuffer stores the spilled bytes (ones that exceed maxMemBytes) on disk.
	diskBuffer *os.File
	// diskBytesWritten is a counter for the length of the bytes stored on disk.
	diskBytesWritten int64
	// overflowed is set to true when a ErrMaximumSizeExceeded happens.
	overflowed bool
	// reader consists of either one or two io.Reader grouped together.
	// See Read function for more information.
	reader io.Reader
	// closeOnce used for making sure the cleanup happens only once.
	closeOnce sync.Once
}

// NewBufferedReadCloser is a convenience constructor when needing to pump data
// from a io.ReadCloser into the buffer. Such an occurance might be when
// buffering HTTP request data from http/net *Request.Body and replacing the
// original body with the returned io.ReadCloser.
//
// Unless an error happens, it's the client's responsibility to call Close on
// the io.ReadCloser returned by the function.
func NewBufferedReadCloser(r io.ReadCloser, maxBytes, maxMemBytes int64) (io.ReadCloser, error) {
	buf := &Buffer{
		maxBytes:    maxBytes,
		maxMemBytes: maxMemBytes,
	}

	_, err := io.Copy(buf, r)
	if err != nil {
		// Ensure failed request buffers are cleaned up.
		//
		// When using `NewBufferedReadCloser`, it's normally the client's
		// responsibility to `Close()` the returned buffer. However, if there's an
		// error when initially populating the buffer, we don't return the failed
		// buffer to the client, and we weren't closing it ourselves. This leads to
		// stale buffers being left behind.
		//
		// Instead, if we can't return a properly populated buffer, we should close
		// and discard it before returning nil.
		buf.Close()
		return nil, err
	}

	return buf, nil
}

// NewBufferedWriteCloser is a constructor that returns a raw *Buffer.
func NewBufferedWriteCloser(maxBytes, maxMemBytes int64) *Buffer {
	return &Buffer{
		maxBytes:    maxBytes,
		maxMemBytes: maxMemBytes,
	}
}

// Write does the actual writing to memory and disk if needed.
// If the data currently written to memory exceeds maxMemBytes it will try to
// write to disk.
// If the total data written exceeds the maxBytes limit it will return a
// ErrMaximumSizeExceeded error.
func (b *Buffer) Write(p []byte) (int, error) {
	if b.reader != nil {
		return 0, ErrWriteAfterRead
	}

	length := int64(len(p))
	totalWritten := b.memBytesWritten + b.diskBytesWritten

	if b.maxBytes > 0 && totalWritten+length > b.maxBytes {
		b.overflowed = true
		return 0, ErrMaximumSizeExceeded
	}

	if b.diskBuffer != nil {
		return b.writeToDisk(p)
	}

	if b.memBytesWritten+length <= b.maxMemBytes {
		return b.writeToMemory(p)
	}

	// We're writing past the memory buffer, so we need to start the spill to disk
	err := b.createSpill()
	if err != nil {
		return 0, err
	}

	memWritten, err := b.writeToMemory(p[:b.maxMemBytes-b.memBytesWritten])
	if err != nil {
		return memWritten, err
	}

	diskWritten, err := b.writeToDisk(p[memWritten:])
	return memWritten + diskWritten, err
}

// Read is a proxy function to call io.Reader.Read on the underlying buffer.
func (b *Buffer) Read(p []byte) (n int, err error) {
	b.setReader()
	return b.reader.Read(p)
}

func (b *Buffer) Overflowed() bool {
	return b.overflowed
}

// Send writes all the buffer data to the io.Writer.
func (b *Buffer) Send(w io.Writer) error {
	b.setReader()
	_, err := io.Copy(w, b.reader)
	return err
}

// Close performs disk buffer cleanup in case that exists.
func (b *Buffer) Close() error {
	b.closeOnce.Do(func() {
		b.discardSpill()
	})

	return nil
}

// writeToMemory also keeps track of the total bytes written to memory, while
// performing the actual writing.
func (b *Buffer) writeToMemory(p []byte) (int, error) {
	n, err := b.memoryBuffer.Write(p)
	b.memBytesWritten += int64(n)
	return n, err
}

// writeToDisk also keeps track of the total bytes written to disk, while
// performing the actual writing.
func (b *Buffer) writeToDisk(p []byte) (int, error) {
	n, err := b.diskBuffer.Write(p)
	b.diskBytesWritten += int64(n)
	return n, err
}

// setReader creates a reader that will read from the memory buffer as well as
// the disk buffer in case that exists.
// The reader will be created only once so it's safe to call this method
// multiple times.
func (b *Buffer) setReader() {
	if b.reader == nil {
		if b.diskBuffer != nil {
			b.diskBuffer.Seek(0, 0)
			b.reader = io.MultiReader(&b.memoryBuffer, b.diskBuffer)
		} else {
			b.reader = &b.memoryBuffer
		}
	}
}

// createSpill creates a temporary file to save the data that exceeds the
// limit of the memory buffer.
func (b *Buffer) createSpill() error {
	f, err := os.CreateTemp("", "proxy-buffer-")
	if err != nil {
		slog.Error("Buffer: failed to create spill file", "error", err)
		return err
	}

	b.diskBuffer = f
	slog.Debug("Buffer: spilling to disk", "file", b.diskBuffer.Name())

	return nil
}

// discardSpill cleans up by closing the file and removing it from disk.
func (b *Buffer) discardSpill() {
	if b.diskBuffer != nil {
		b.diskBuffer.Close()

		slog.Debug("Buffer: removing spill", "file", b.diskBuffer.Name())
		err := os.Remove(b.diskBuffer.Name())
		if err != nil {
			slog.Error("Buffer: failed to remove spill", "file", b.diskBuffer.Name(), "error", err)
		}
	}
}
