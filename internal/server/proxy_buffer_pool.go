package server

import "sync"

// NewBufferPool is used by httputil.ReverseProxy to improve performance.
func NewBufferPool(bufferSize int64) *BufferPool {
	return &BufferPool{
		pool: sync.Pool{
			New: func() any {
				buf := make([]byte, bufferSize)
				return &buf
			},
		},
	}
}

type BufferPool struct {
	pool sync.Pool
}

func (b *BufferPool) Get() []byte {
	return *(b.pool.Get().(*[]byte))
}

func (b *BufferPool) Put(content []byte) {
	b.pool.Put(&content)
}
