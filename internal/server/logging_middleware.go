package server

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/basecamp/kamal-proxy/internal/metrics"
)

type contextKey string

var contextKeyRequestContext = contextKey("request-context")

type loggingRequestContext struct {
	Service         string
	Target          string
	RequestHeaders  []string
	ResponseHeaders []string
}

type LoggingMiddleware struct {
	logger    *slog.Logger
	httpPort  int
	httpsPort int
	next      http.Handler
}

// WithLoggingMiddleware should be called before other middleware because it
// keeps track of the starting time and it also sets the `loggingRequestContext`
// for other middleware to be able to manipulate.
//
// Other middleware can set the `Service`, `Target` and custom response and
// requests headers. The custom response and request headers will be logged with
// the prefixes `resp_` and `req_`.
//
// By default the logs will include the following fields automatically:
// `host`, `port`, `path`, `request_id`, `status`, `service`, `target`,
// `duration`, `method`, `req_content_length`, `req_content_type`,
// `resp_content_length`, `resp_content_type`, `client_addr`, `client_port`,
// `remote_addr`, `user_agent`, `proto`, `scheme`, `query`.
//
// To determine the response bytes written, it wraps the original response into
// another struct and implements all the necessary interfaces
// (`http.ResponseWriter` along with optional interfaces such as `http.Flusher`
// and `http.Hijacker`).
//
// Apart from logging this middleware also tracks some metrics via Prometheus.
func WithLoggingMiddleware(logger *slog.Logger, httpPort, httpsPort int, next http.Handler) http.Handler {
	return &LoggingMiddleware{
		logger:    logger,
		httpPort:  httpPort,
		httpsPort: httpsPort,
		next:      next,
	}
}

// LoggingRequestContext returns a struct that should be used to set the
// `Service`, `Target`, custom request and response headers that will be logged.
func LoggingRequestContext(r *http.Request) *loggingRequestContext {
	lrc, ok := r.Context().Value(contextKeyRequestContext).(*loggingRequestContext)
	if !ok {
		return &loggingRequestContext{}
	}
	return lrc
}

func (h *LoggingMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	writer := newLoggerResponseWriter(w)

	var loggingRequestContext loggingRequestContext
	ctx := context.WithValue(r.Context(), contextKeyRequestContext, &loggingRequestContext)
	r = r.WithContext(ctx)

	started := time.Now()

	defer func() {
		elapsed := time.Since(started)

		port := h.httpPort
		scheme := "http"
		if r.TLS != nil {
			port = h.httpsPort
			scheme = "https"
		}

		clientAddr, clientPort, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			clientAddr = r.RemoteAddr
			clientPort = ""
		}

		remoteAddr := r.Header.Get("X-Forwarded-For")
		if remoteAddr == "" {
			remoteAddr = clientAddr
		}

		attrs := []slog.Attr{
			slog.String("host", r.Host),
			slog.Int("port", port),
			slog.String("path", r.URL.Path),
			slog.String("request_id", r.Header.Get("X-Request-ID")),
			slog.Int("status", writer.statusCode),
			slog.String("service", loggingRequestContext.Service),
			slog.String("target", loggingRequestContext.Target),
			slog.Int64("duration", elapsed.Nanoseconds()),
			slog.String("method", r.Method),
			slog.Int64("req_content_length", r.ContentLength),
			slog.String("req_content_type", r.Header.Get("Content-Type")),
			slog.Int64("resp_content_length", writer.bytesWritten),
			slog.String("resp_content_type", writer.Header().Get("Content-Type")),
			slog.String("client_addr", clientAddr),
			slog.String("client_port", clientPort),
			slog.String("remote_addr", remoteAddr),
			slog.String("user_agent", r.Header.Get("User-Agent")),
			slog.String("proto", r.Proto),
			slog.String("scheme", scheme),
			slog.String("query", r.URL.RawQuery),
		}

		attrs = append(attrs, h.retrieveCustomHeaders(loggingRequestContext.RequestHeaders, r.Header, "req")...)
		attrs = append(attrs, h.retrieveCustomHeaders(loggingRequestContext.ResponseHeaders, writer.Header(), "resp")...)
		h.logger.LogAttrs(context.Background(), slog.LevelInfo, "Request", attrs...)

		metrics.Tracker.TrackRequest(loggingRequestContext.Service, r.Method, writer.statusCode, elapsed)
	}()

	h.next.ServeHTTP(writer, r)
}

func (h *LoggingMiddleware) retrieveCustomHeaders(headerNames []string, header http.Header, prefix string) []slog.Attr {
	attrs := []slog.Attr{}
	for _, headerName := range headerNames {
		name := prefix + "_" + strings.ReplaceAll(strings.ToLower(headerName), "-", "_")
		value := strings.Join(header[headerName], ",")
		attrs = append(attrs, slog.String(name, value))
	}
	return attrs
}

type loggerResponseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int64
}

func newLoggerResponseWriter(w http.ResponseWriter) *loggerResponseWriter {
	return &loggerResponseWriter{w, http.StatusOK, 0}
}

// WriteHeader is used to capture the status code
func (r *loggerResponseWriter) WriteHeader(statusCode int) {
	r.statusCode = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

// Write is used to capture the amount of data written
func (r *loggerResponseWriter) Write(b []byte) (int, error) {
	bytesWritten, err := r.ResponseWriter.Write(b)
	r.bytesWritten += int64(bytesWritten)
	return bytesWritten, err
}

func (r *loggerResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("ResponseWriter does not implement http.Hijacker")
	}

	con, rw, err := hijacker.Hijack()
	if err == nil {
		// 1) Hijacking almost always implies a protocol switch
		// The most likely reason for the hijack is either WebSockets or CONNECT
		// tunnels, which both mean "We're no longer speaking HTTP.".
		// The appropriate status code is the one below.
		//
		// 2) After hijacking, the real status code may never be written
		// `WriteHeader` may never be called, since the handler may manually write
		// bytes to the socket.
		//
		// The logging middleware needs a value for the status code, and this is the
		// appropriate one considering 1) && 2).
		r.statusCode = http.StatusSwitchingProtocols
	}
	return con, rw, err
}

func (r *loggerResponseWriter) Flush() {
	flusher, ok := r.ResponseWriter.(http.Flusher)
	if ok {
		flusher.Flush()
	}
}
