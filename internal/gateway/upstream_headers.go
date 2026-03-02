package gateway

import (
	"net/http"
	"strings"
)

// hopByHopHeaders are not end-to-end and must not be forwarded by proxies.
var hopByHopHeaders = map[string]bool{
	"connection":          true,
	"keep-alive":          true,
	"proxy-authenticate":  true,
	"proxy-authorization": true,
	"te":                  true,
	"trailer":             true,
	"transfer-encoding":   true,
	"upgrade":             true,
}

func copyUpstreamResponseHeaders(dst, src http.Header) {
	if dst == nil || src == nil {
		return
	}
	for key, values := range src {
		if !shouldForwardUpstreamHeader(key) {
			continue
		}
		canonical := http.CanonicalHeaderKey(key)
		dst.Del(canonical)
		for _, value := range values {
			if strings.TrimSpace(value) == "" {
				continue
			}
			dst.Add(canonical, value)
		}
	}
}

func shouldForwardUpstreamHeader(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	if k == "" {
		return false
	}

	if hopByHopHeaders[k] {
		return false
	}

	// Managed by net/http for the outgoing response, avoid stale/mismatched values.
	switch k {
	case "content-length", "date", "server":
		return false
	default:
		return true
	}
}
