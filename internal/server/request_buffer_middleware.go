package server

import (
	"log/slog"
	"net/http"
)

// RequestBufferMiddleware buffers the request based on the Buffer
// implementation. The maxBytes is the hard limit for buffering and exceeding that
// will return an error. maxMemBytes represents the limit for individual
// requests, and in case it's exceeded the rest of the data will be saved on
// disk.
type RequestBufferMiddleware struct {
	maxMemBytes int64
	maxBytes    int64
	next        http.Handler
}

func WithRequestBufferMiddleware(maxMemBytes, maxBytes int64, next http.Handler) http.Handler {
	return &RequestBufferMiddleware{
		maxMemBytes: maxMemBytes,
		maxBytes:    maxBytes,
		next:        next,
	}
}

func (h *RequestBufferMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestBuffer, err := NewBufferedReadCloser(r.Body, h.maxBytes, h.maxMemBytes)
	if err != nil {
		if err == ErrMaximumSizeExceeded {
			http.Error(w, "Request too large", http.StatusRequestEntityTooLarge)
		} else {
			slog.Error("Error buffering request", "path", r.URL.Path, "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	r.Body = requestBuffer
	h.next.ServeHTTP(w, r)
}
