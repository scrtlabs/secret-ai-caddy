package metering

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// TestNewBodyHandler_BufferCapMatchesBodySize pins that maxBufferSize is no
// longer independently clamped to 5MB: it must always equal the configured
// maxBodySize, both below and above the old clamp value, so the legacy
// billing path and the x402 path enforce the same effective limit.
func TestNewBodyHandler_BufferCapMatchesBodySize(t *testing.T) {
	cases := []int64{
		1 * 1024 * 1024,  // below old 5MB clamp
		5 * 1024 * 1024,  // at old clamp boundary
		10 * 1024 * 1024, // above old 5MB clamp
	}

	for _, maxBodySize := range cases {
		bh := NewBodyHandler(maxBodySize)
		if bh.maxBufferSize != maxBodySize {
			t.Errorf("NewBodyHandler(%d): maxBufferSize = %d, want %d", maxBodySize, bh.maxBufferSize, maxBodySize)
		}
		if bh.maxBodySize != maxBodySize {
			t.Errorf("NewBodyHandler(%d): maxBodySize = %d, want %d", maxBodySize, bh.maxBodySize, maxBodySize)
		}
	}
}

// TestBodyHandler_SafeReadRequestBody_TruncatesAtConfiguredSize is a small-N
// behavioral check that buffering still truncates right at maxBodySize (not
// at the old, independent 5MB buffer clamp) in both directions.
func TestBodyHandler_SafeReadRequestBody_TruncatesAtConfiguredSize(t *testing.T) {
	const maxBodySize = 20 // bytes; small enough to keep the test fast

	bh := NewBodyHandler(maxBodySize)

	t.Run("under limit is not truncated", func(t *testing.T) {
		body := strings.Repeat("a", maxBodySize-1)
		req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
		info, err := bh.SafeReadRequestBody(req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.IsTruncated {
			t.Errorf("expected body of length %d (under limit %d) not to be truncated", len(body), maxBodySize)
		}
	})

	t.Run("over limit is truncated", func(t *testing.T) {
		body := strings.Repeat("a", maxBodySize+1)
		req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
		info, err := bh.SafeReadRequestBody(req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !info.IsTruncated {
			t.Errorf("expected body of length %d (over limit %d) to be truncated", len(body), maxBodySize)
		}
	})
}

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
