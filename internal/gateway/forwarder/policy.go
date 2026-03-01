package forwarder

import (
	"io"
	"net/http"
	"strings"
	"time"
)

func ShouldFailover(statusCode int) bool {
	switch statusCode {
	case http.StatusTooManyRequests, // 429
		http.StatusInternalServerError, // 500
		http.StatusBadGateway,          // 502
		http.StatusServiceUnavailable,  // 503
		http.StatusGatewayTimeout,      // 504
		529:                            // 529: Anthropic overloaded
		return true
	}
	return false
}

func ParseRetryAfter(header string) time.Duration {
	if header == "" {
		return 60 * time.Second
	}
	if d, err := time.ParseDuration(header + "s"); err == nil {
		return d
	}
	return 60 * time.Second
}

func ShouldHandleAsStreamingResponse(shouldStream bool, contentType string) bool {
	normalizedContentType := strings.ToLower(strings.TrimSpace(contentType))
	if strings.HasPrefix(normalizedContentType, "text/event-stream") {
		return true
	}
	// Some upstreams omit Content-Type for streaming responses.
	return shouldStream && normalizedContentType == ""
}

func DrainAndClose(body io.ReadCloser) {
	io.Copy(io.Discard, io.LimitReader(body, 1<<20))
	body.Close()
}
