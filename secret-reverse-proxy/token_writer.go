package secret_reverse_proxy

import (
	"bytes"
	"net/http"
)

// TokenMeteringResponseWriter wraps http.ResponseWriter and captures output body.
type TokenMeteringResponseWriter struct {
	http.ResponseWriter

	// Buffer to capture response body
	body *bytes.Buffer

	// Status code
	status int

	// Whether WriteHeader has been explicitly called
	wroteHeader bool
}

// NewTokenMeteringResponseWriter creates a new wrapped writer.
func NewTokenMeteringResponseWriter(w http.ResponseWriter) *TokenMeteringResponseWriter {
	return &TokenMeteringResponseWriter{
		ResponseWriter: w,
		body:           new(bytes.Buffer),
	}
}

// Write captures the response body while forwarding to the real writer.
func (tmw *TokenMeteringResponseWriter) Write(b []byte) (int, error) {
	tmw.body.Write(b) // buffer the response
	return tmw.ResponseWriter.Write(b)
}

// WriteHeader intercepts the status code.
func (tmw *TokenMeteringResponseWriter) WriteHeader(statusCode int) {
	tmw.status = statusCode
	tmw.wroteHeader = true
	tmw.ResponseWriter.WriteHeader(statusCode)
}

// Status returns the HTTP status.
func (tmw *TokenMeteringResponseWriter) Status() int {
	if tmw.wroteHeader {
		return tmw.status
	}
	return http.StatusOK // default if WriteHeader not called
}

// Body returns the buffered response body.
func (tmw *TokenMeteringResponseWriter) Body() []byte {
	return tmw.body.Bytes()
}
