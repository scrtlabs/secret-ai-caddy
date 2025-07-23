package metering

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	
	"github.com/caddyserver/caddy/v2"
	"go.uber.org/zap"
)

// BodyHandler provides safe request/response body handling with size limits
type BodyHandler struct {
	maxBodySize    int64
	maxBufferSize  int64
	logger         *zap.Logger
	compressionEnabled bool
	bufferPool     *sync.Pool
}

func NewBodyHandler(maxBodySize int64) *BodyHandler {
	if maxBodySize <= 0 {
		maxBodySize = 10 * 1024 * 1024 // 10MB default
	}
	
	maxBufferSize := maxBodySize
	if maxBufferSize > 5*1024*1024 {
		maxBufferSize = 5 * 1024 * 1024 // 5MB max buffer
	}
	
	return &BodyHandler{
		maxBodySize:        maxBodySize,
		maxBufferSize:      maxBufferSize,
		logger:             caddy.Log(),
		compressionEnabled: true,
		bufferPool: &sync.Pool{
			New: func() interface{} {
				return make([]byte, 0, 4096)
			},
		},
	}
}

// RequestBodyInfo contains extracted information from request body
type RequestBodyInfo struct {
	Content      string
	Size         int64
	ContentType  string
	IsComplete   bool
	IsTruncated  bool
	ParsedJSON   map[string]interface{}
	ExtractedText []string
	Error        error
}

// ResponseBodyInfo contains extracted information from response body
type ResponseBodyInfo struct {
	Content     string
	Size        int64
	IsComplete  bool
	IsTruncated bool
	StatusCode  int
	ParsedJSON  map[string]interface{}
	ExtractedText []string
}

// SafeReadRequestBody safely reads request body with comprehensive handling
func (bh *BodyHandler) SafeReadRequestBody(r *http.Request) (*RequestBodyInfo, error) {
	info := &RequestBodyInfo{
		ContentType: bh.GetContentType(r),
		IsComplete:  true,
	}

	if r.Body == nil {
		return info, nil
	}

	// Handle compressed content
	reader, err := bh.getDecompressedReader(r)
	if err != nil {
		bh.logger.Error("Failed to create decompressed reader", zap.Error(err))
		info.Error = err
		return info, nil // Don't fail the request, just log the error
	}
	defer func() {
		if closer, ok := reader.(io.Closer); ok && closer != r.Body {
			closer.Close()
		}
	}()

	// Create a limited reader to prevent memory exhaustion
	limitedReader := io.LimitReader(reader, bh.maxBodySize+1)
	
	// Get buffer from pool
	bufferBytes := bh.bufferPool.Get().([]byte)
	defer bh.bufferPool.Put(bufferBytes[:0])
	
	// Read body with buffer management
	var buffer bytes.Buffer
	var totalRead int64
	
	for {
		n, err := limitedReader.Read(bufferBytes[:cap(bufferBytes)])
		if n > 0 {
			buffer.Write(bufferBytes[:n])
			totalRead += int64(n)
			
			// Check if we've exceeded our buffer limit
			if totalRead > bh.maxBufferSize {
				info.IsTruncated = true
				info.IsComplete = false
				break
			}
		}
		
		if err == io.EOF {
			break
		}
		if err != nil {
			bh.logger.Error("Error reading request body", zap.Error(err))
			info.Error = err
			break
		}
		
		// Check if we've hit the size limit
		if totalRead > bh.maxBodySize {
			info.IsTruncated = true
			info.IsComplete = false
			bh.logger.Warn("Request body exceeded size limit", 
				zap.Int64("max_size", bh.maxBodySize),
				zap.Int64("actual_size", totalRead))
			break
		}
	}

	bodyBytes := buffer.Bytes()
	info.Content = string(bodyBytes)
	info.Size = totalRead

	// Restore the body for downstream handlers
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	
	// Parse content if it's JSON
	if bh.IsJSONContent(info.ContentType) {
		if parsedJSON, err := bh.parseJSON(bodyBytes); err == nil {
			info.ParsedJSON = parsedJSON
			info.ExtractedText = bh.extractTextFromJSON(parsedJSON)
		}
	}

	return info, nil
}

// getDecompressedReader handles compressed request bodies
func (bh *BodyHandler) getDecompressedReader(r *http.Request) (io.Reader, error) {
	encoding := r.Header.Get("Content-Encoding")
	
	switch strings.ToLower(encoding) {
	case "gzip":
		return gzip.NewReader(r.Body)
	case "":
		return r.Body, nil
	default:
		bh.logger.Warn("Unsupported content encoding", zap.String("encoding", encoding))
		return r.Body, nil // Return original body, let downstream handle it
	}
}

// GetContentType extracts and normalizes content type
func (bh *BodyHandler) GetContentType(r *http.Request) string {
	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		return "application/octet-stream"
	}
	
	// Extract main content type (ignore charset and other parameters)
	if idx := strings.Index(contentType, ";"); idx != -1 {
		contentType = contentType[:idx]
	}
	
	return strings.TrimSpace(strings.ToLower(contentType))
}

// IsTokenCountableContent determines if content should be token-counted
func (bh *BodyHandler) IsTokenCountableContent(contentType string) bool {
	countableTypes := map[string]bool{
		"application/json":                  true,
		"application/ld+json":              true,
		"application/json-patch+json":      true,
		"text/plain":                       true,
		"text/html":                        true,
		"text/markdown":                    true,
		"text/md":                          true,
		"application/xml":                  true,
		"text/xml":                         true,
		"application/x-yaml":               true,
		"text/yaml":                        true,
		"application/yaml":                 true,
		"text/csv":                         true,
		"application/csv":                  true,
		"text/tab-separated-values":        true,
		"application/x-httpd-php":          true,
		"text/javascript":                  true,
		"application/javascript":           true,
		"application/x-javascript":         true,
		"text/css":                         true,
	}
	
	// Check exact match first
	if countableTypes[contentType] {
		return true
	}
	
	// Check prefixes for broader categories
	countablePrefixes := []string{
		"text/",
		"application/json",
		"application/xml",
		"application/*+json",
		"application/*+xml",
	}
	
	for _, prefix := range countablePrefixes {
		if strings.HasPrefix(contentType, prefix) {
			return true
		}
	}
	
	return false
}

// IsJSONContent checks if content type indicates JSON
func (bh *BodyHandler) IsJSONContent(contentType string) bool {
	return strings.Contains(contentType, "json")
}

// parseJSON safely parses JSON content
func (bh *BodyHandler) parseJSON(data []byte) (map[string]interface{}, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty JSON data")
	}
	
	var result map[string]interface{}
	err := json.Unmarshal(data, &result)
	return result, err
}

// extractTextFromJSON recursively extracts text content from JSON structures
func (bh *BodyHandler) extractTextFromJSON(data interface{}) []string {
	var texts []string
	
	switch v := data.(type) {
	case map[string]interface{}:
		for key, value := range v {
			// Check if this is a textual field
			if bh.isTextualField(key) {
				if str, ok := value.(string); ok && str != "" {
					texts = append(texts, str)
				}
			}
			// Recurse into nested objects
			texts = append(texts, bh.extractTextFromJSON(value)...)
		}
	case []interface{}:
		for _, item := range v {
			texts = append(texts, bh.extractTextFromJSON(item)...)
		}
	case string:
		if v != "" && len(v) > 10 { // Only consider substantial strings
			texts = append(texts, v)
		}
	}
	
	return texts
}

// isTextualField identifies fields likely to contain token-countable text
func (bh *BodyHandler) isTextualField(fieldName string) bool {
	fieldName = strings.ToLower(fieldName)
	
	// Common AI API text fields
	textualFields := map[string]bool{
		"prompt":        true,
		"content":       true,
		"message":       true,
		"text":          true,
		"input":         true,
		"query":         true,
		"instruction":   true,
		"system":        true,
		"user":          true,
		"assistant":     true,
		"completion":    true,
		"response":      true,
		"output":        true,
		"choices":       true,
		"data":          true,
		"body":          true,
		"description":   true,
		"summary":       true,
		"title":         true,
		"context":       true,
		"history":       true,
		"conversation":  true,
		"dialogue":      true,
		"transcript":    true,
		"document":      true,
		"article":       true,
		"paragraph":     true,
		"sentence":      true,
		"question":      true,
		"answer":        true,
		"comment":       true,
		"note":          true,
		"memo":          true,
		"feedback":      true,
		"review":        true,
	}
	
	// Check exact matches
	if textualFields[fieldName] {
		return true
	}
	
	// Check common patterns
	textualPatterns := []string{
		"_text", "_content", "_message", "_prompt", "_input", "_output",
		"text_", "content_", "message_", "prompt_", "input_", "output_",
	}
	
	for _, pattern := range textualPatterns {
		if strings.Contains(fieldName, pattern) {
			return true
		}
	}
	
	return false
}

// StreamingTokenMeteringResponseWriter handles streaming responses with comprehensive buffering
type StreamingTokenMeteringResponseWriter struct {
	http.ResponseWriter
	bodyBuffer      *bytes.Buffer
	maxBufferSize   int64
	status          int
	wroteHeader     bool
	logger          *zap.Logger
	contentType     string
	contentLength   int64
	totalWritten    int64
	startTime       time.Time
	compressionType string
	bufferPool      *sync.Pool
}

func NewStreamingTokenMeteringResponseWriter(w http.ResponseWriter, maxBufferSize int64, bufferPool *sync.Pool) *StreamingTokenMeteringResponseWriter {
	if maxBufferSize <= 0 {
		maxBufferSize = 5 * 1024 * 1024 // 5MB default
	}
	
	if bufferPool == nil {
		bufferPool = &sync.Pool{
			New: func() interface{} {
				return make([]byte, 0, 4096)
			},
		}
	}
	
	return &StreamingTokenMeteringResponseWriter{
		ResponseWriter: w,
		bodyBuffer:     new(bytes.Buffer),
		maxBufferSize:  maxBufferSize,
		logger:         caddy.Log(),
		startTime:      time.Now(),
		bufferPool:     bufferPool,
	}
}

func (stmw *StreamingTokenMeteringResponseWriter) Header() http.Header {
	return stmw.ResponseWriter.Header()
}

func (stmw *StreamingTokenMeteringResponseWriter) Write(b []byte) (int, error) {
	// Capture content type and length on first write if not already set
	if !stmw.wroteHeader {
		stmw.WriteHeader(http.StatusOK)
	}
	
	// Write to the real response first
	n, err := stmw.ResponseWriter.Write(b)
	stmw.totalWritten += int64(n)
	
	// Buffer for token counting, but respect size limits
	if int64(stmw.bodyBuffer.Len()) < stmw.maxBufferSize {
		remainingBuffer := stmw.maxBufferSize - int64(stmw.bodyBuffer.Len())
		toBuffer := int64(n)
		
		if toBuffer > remainingBuffer {
			toBuffer = remainingBuffer
			if remainingBuffer == 0 {
				// Log once that we're not buffering anymore
				stmw.logger.Debug("Response buffer full, stopping buffering for token counting",
					zap.Int64("max_buffer_size", stmw.maxBufferSize),
					zap.Int64("total_written", stmw.totalWritten))
			}
		}
		
		if toBuffer > 0 {
			stmw.bodyBuffer.Write(b[:toBuffer])
		}
	}
	
	return n, err
}

func (stmw *StreamingTokenMeteringResponseWriter) WriteHeader(statusCode int) {
	if stmw.wroteHeader {
		return
	}
	
	stmw.status = statusCode
	stmw.wroteHeader = true
	
	// Capture response metadata
	headers := stmw.ResponseWriter.Header()
	stmw.contentType = headers.Get("Content-Type")
	stmw.compressionType = headers.Get("Content-Encoding")
	
	if clHeader := headers.Get("Content-Length"); clHeader != "" {
		if cl, err := strconv.ParseInt(clHeader, 10, 64); err == nil {
			stmw.contentLength = cl
		}
	}
	
	stmw.ResponseWriter.WriteHeader(statusCode)
}

func (stmw *StreamingTokenMeteringResponseWriter) Status() int {
	if stmw.wroteHeader {
		return stmw.status
	}
	return http.StatusOK
}

func (stmw *StreamingTokenMeteringResponseWriter) Body() []byte {
	return stmw.bodyBuffer.Bytes()
}

func (stmw *StreamingTokenMeteringResponseWriter) IsComplete() bool {
	// Check if we buffered the complete response
	if stmw.contentLength > 0 {
		return int64(stmw.bodyBuffer.Len()) >= stmw.contentLength
	}
	
	// If no content-length header, assume complete if we didn't hit buffer limit
	return int64(stmw.bodyBuffer.Len()) < stmw.maxBufferSize
}

func (stmw *StreamingTokenMeteringResponseWriter) IsTruncated() bool {
	return !stmw.IsComplete()
}

func (stmw *StreamingTokenMeteringResponseWriter) GetResponseInfo() *ResponseBodyInfo {
	info := &ResponseBodyInfo{
		Content:     string(stmw.bodyBuffer.Bytes()),
		Size:        stmw.totalWritten,
		IsComplete:  stmw.IsComplete(),
		IsTruncated: stmw.IsTruncated(),
		StatusCode:  stmw.Status(),
	}
	
	// Parse JSON if applicable
	if strings.Contains(stmw.contentType, "json") && len(stmw.bodyBuffer.Bytes()) > 0 {
		var parsedJSON map[string]interface{}
		if err := json.Unmarshal(stmw.bodyBuffer.Bytes(), &parsedJSON); err == nil {
			info.ParsedJSON = parsedJSON
			// Extract text using same logic as request handling
			bh := &BodyHandler{logger: stmw.logger}
			info.ExtractedText = bh.extractTextFromJSON(parsedJSON)
		}
	}
	
	return info
}

func (stmw *StreamingTokenMeteringResponseWriter) GetMetrics() map[string]interface{} {
	duration := time.Since(stmw.startTime)
	
	return map[string]interface{}{
		"status_code":        stmw.Status(),
		"content_type":       stmw.contentType,
		"content_length":     stmw.contentLength,
		"total_written":      stmw.totalWritten,
		"buffered_bytes":     stmw.bodyBuffer.Len(),
		"is_complete":        stmw.IsComplete(),
		"is_truncated":       stmw.IsTruncated(),
		"compression":        stmw.compressionType,
		"response_time_ms":   float64(duration.Nanoseconds()) / 1000000,
	}
}

// Flush implements http.Flusher if the underlying ResponseWriter supports it
func (stmw *StreamingTokenMeteringResponseWriter) Flush() {
	if flusher, ok := stmw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Hijack implements http.Hijacker if the underlying ResponseWriter supports it
func (stmw *StreamingTokenMeteringResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := stmw.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, fmt.Errorf("response writer does not support hijacking")
}

// Push implements http.Pusher if the underlying ResponseWriter supports it (HTTP/2 Server Push)
func (stmw *StreamingTokenMeteringResponseWriter) Push(target string, opts *http.PushOptions) error {
	if pusher, ok := stmw.ResponseWriter.(http.Pusher); ok {
		return pusher.Push(target, opts)
	}
	return fmt.Errorf("response writer does not support server push")
}

// SafeReadResponseBody processes response body information for token counting
func (bh *BodyHandler) SafeReadResponseBody(responseInfo *ResponseBodyInfo) string {
	if responseInfo == nil || responseInfo.Content == "" {
		return ""
	}

	content := responseInfo.Content
	
	// Apply size limit to response body for token counting
	if int64(len(content)) > bh.maxBufferSize {
		bh.logger.Warn("Response body exceeded buffer size for token counting", 
			zap.Int64("max_buffer_size", bh.maxBufferSize),
			zap.Int("actual_size", len(content)))
		
		// Truncate for token counting (but this doesn't modify the actual response)
		content = content[:bh.maxBufferSize]
		responseInfo.IsTruncated = true
	}

	return content
}

// GetContentLength safely extracts content length from request
func (bh *BodyHandler) GetContentLength(r *http.Request) int64 {
	if r.ContentLength >= 0 {
		return r.ContentLength
	}
	
	if clHeader := r.Header.Get("Content-Length"); clHeader != "" {
		if cl, err := strconv.ParseInt(clHeader, 10, 64); err == nil {
			return cl
		}
	}
	
	return -1 // Unknown length
}

// ValidateRequestSize checks if request is within acceptable limits
func (bh *BodyHandler) ValidateRequestSize(r *http.Request) error {
	contentLength := bh.GetContentLength(r)
	
	if contentLength > bh.maxBodySize {
		return fmt.Errorf("request body size %d exceeds maximum allowed size %d", 
			contentLength, bh.maxBodySize)
	}
	
	return nil
}