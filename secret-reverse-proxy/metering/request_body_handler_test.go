package metering

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http/httptest"
	"strconv"
	"testing"
)

// TestBodyHandler_SafeReadRequestBody_Gzip_RestoresCoherentRequest pins that
// when the request body was gzip-compressed, SafeReadRequestBody restores
// r.Body to the decompressed bytes AND makes the surrounding request coherent
// with that restored body: the now-stale Content-Encoding header is removed,
// and both r.ContentLength and the Content-Length header reflect the
// decompressed length. Without this, a downstream forwarder would send
// plaintext bytes still labeled as gzip.
func TestBodyHandler_SafeReadRequestBody_Gzip_RestoresCoherentRequest(t *testing.T) {
	plain := `{"model":"llama-3","messages":[{"role":"user","content":"hi"}]}`

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(plain)); err != nil {
		t.Fatalf("failed to gzip-compress test body: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("failed to close gzip writer: %v", err)
	}

	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(buf.Bytes()))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")

	bh := NewBodyHandler(10 * 1024 * 1024)
	info, err := bh.SafeReadRequestBody(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Error != nil {
		t.Fatalf("unexpected body info error: %v", info.Error)
	}
	if info.Content != plain {
		t.Errorf("expected decompressed content %q, got %q", plain, info.Content)
	}

	if enc := req.Header.Get("Content-Encoding"); enc != "" {
		t.Errorf("expected Content-Encoding header to be removed after decompression, got %q", enc)
	}

	restored, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("failed to read restored body: %v", err)
	}
	if string(restored) != plain {
		t.Errorf("expected restored r.Body to contain decompressed bytes %q, got %q", plain, string(restored))
	}

	if req.ContentLength != int64(len(plain)) {
		t.Errorf("expected r.ContentLength %d, got %d", len(plain), req.ContentLength)
	}
	wantCL := strconv.FormatInt(int64(len(plain)), 10)
	if cl := req.Header.Get("Content-Length"); cl != wantCL {
		t.Errorf("expected Content-Length header %q, got %q", wantCL, cl)
	}
}
