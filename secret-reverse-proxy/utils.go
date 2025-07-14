package secret_reverse_proxy

import (
	"bytes"
	"io"
	"net/http"
)

func readRequestBody(r *http.Request) string {
	if r.Body == nil {
		return ""
	}
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		return ""
	}
	// Replace body so downstream handlers can still read it
	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	return string(bodyBytes)
}
