package secret_reverse_proxy

import (
    "bytes"
    "io"
    "fmt"
    "net/http"
    "net/http/httputil"
	"github.com/caddyserver/caddy/v2"

)

func LogRequest(req *http.Request, maxBody int) {
	logger := caddy.Log()
    // 1) Slurp entire body once, then restore it for the next handler
    var fullBody []byte
    if req.Body != nil {
        b, _ := io.ReadAll(req.Body)               // read all (avoid partial reads)
        req.Body = io.NopCloser(bytes.NewReader(b)) // restore for downstream
        fullBody = b
    }

    // 2) Clone request for logging (so we can attach a truncated body)
    clone := req.Clone(req.Context())
    clone.Body = nil

    // Redact sensitive headers if present
    if auth := clone.Header.Get("Authorization"); auth != "" {
        clone.Header.Set("Authorization", "[REDACTED]")
    }

    // 3) Attach truncated body (if any) to the clone for logging
    if maxBody > 0 && len(fullBody) > 0 {
        bodyForLog := fullBody
        if len(bodyForLog) > maxBody {
            bodyForLog = bodyForLog[:maxBody]
        }
        clone.Body = io.NopCloser(bytes.NewReader(bodyForLog))
    }

    // 4) Dump request. Set `includeBody` true only if we attached a (truncated) body.
    includeBody := clone.Body != nil
    dump, err := httputil.DumpRequest(clone, includeBody)
    if err != nil {
        logger.Error(fmt.Sprintf("DumpRequest error: %v", err))
        return
    }
    if includeBody && len(fullBody) > maxBody {
        logger.Debug(fmt.Sprintf("%s\n--- body truncated to %d bytes ---", dump, maxBody))
    } else {
        logger.Debug(fmt.Sprintf("%s", dump))
    }
}