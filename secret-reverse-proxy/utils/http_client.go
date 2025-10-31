package utils

import (
	"crypto/tls"
	"net/http"
	"os"
	"strings"
)

// GetHTTPClient returns an HTTP client configured based on the SKIP_SSL_VALIDATION environment variable.
// When SKIP_SSL_VALIDATION=true, the client will skip TLS certificate verification.
// This is useful for development/testing environments but should not be used in production.
func GetHTTPClient() *http.Client {
	skipSSL := os.Getenv("SKIP_SSL_VALIDATION")
	if strings.ToLower(skipSSL) == "true" {
		tr := &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
		return &http.Client{Transport: tr}
	}
	return http.DefaultClient
}
