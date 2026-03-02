package gateway

import (
	"net/http"
	"testing"
)

func TestCopyUpstreamResponseHeaders(t *testing.T) {
	dst := http.Header{}
	src := http.Header{}
	src.Set("x-request-id", "req_123")
	src.Set("Mcp-Session-Id", "mcp_sess_abc")
	src.Set("X-Custom-Feature", "enabled")
	src.Set("Content-Length", "999")
	src.Set("Transfer-Encoding", "chunked")

	copyUpstreamResponseHeaders(dst, src)

	if got := dst.Get("X-Request-Id"); got != "req_123" {
		t.Fatalf("expected X-Request-Id=req_123, got %q", got)
	}
	if got := dst.Get("Mcp-Session-Id"); got != "mcp_sess_abc" {
		t.Fatalf("expected Mcp-Session-Id=mcp_sess_abc, got %q", got)
	}
	if got := dst.Get("X-Custom-Feature"); got != "enabled" {
		t.Fatalf("expected X-Custom-Feature=enabled, got %q", got)
	}
	if got := dst.Get("Content-Length"); got != "" {
		t.Fatalf("expected Content-Length not forwarded, got %q", got)
	}
	if got := dst.Get("Transfer-Encoding"); got != "" {
		t.Fatalf("expected Transfer-Encoding not forwarded, got %q", got)
	}
}
