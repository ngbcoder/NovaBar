package webview

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newRequest builds a request for a path, decoding any percent-encoding so the
// traversal tests exercise the same value a server would see.
func newRequest(t *testing.T, path string) *http.Request {
	t.Helper()
	return httptest.NewRequest(http.MethodGet, path, nil)
}

// recorder captures a handler's response.
type recorder struct {
	*httptest.ResponseRecorder
}

func newRecorder() *recorder {
	return &recorder{httptest.NewRecorder()}
}

func (r *recorder) status() int {
	if r.Code == 0 {
		return http.StatusOK
	}
	return r.Code
}

// statusOf is a small readability helper for assertions.
func statusOf(r *recorder) int { return r.status() }

var _ = strings.TrimSpace
