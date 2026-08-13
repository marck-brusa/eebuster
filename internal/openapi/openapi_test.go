package openapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serve(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec
}

func TestRoutesServeEmbeddedContent(t *testing.T) {
	cases := []struct {
		path     string
		minBytes int
	}{
		{"/openapi.yaml", 1000},
		{"/api/v1/openapi.yaml", 1000},
		{"/docs", 100},
		{"/redoc", 100},
		{"/docs/assets/swagger-ui.css", 100_000},
		{"/docs/assets/swagger-ui-bundle.js", 1_000_000},
		{"/docs/assets/redoc.standalone.js", 500_000},
	}
	for _, c := range cases {
		rec := serve(t, c.path)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: got %d, want 200", c.path, rec.Code)
		}
		if rec.Body.Len() < c.minBytes {
			t.Errorf("%s: body %d bytes, want at least %d", c.path, rec.Body.Len(), c.minBytes)
		}
	}
}

// The whole point of embedding the viewers is that the pages render on an isolated bench
// network. Any http(s) reference in the served HTML would silently reintroduce the
// blank-page-when-offline failure.
func TestViewerPagesHaveNoExternalReferences(t *testing.T) {
	for _, path := range []string{"/docs", "/redoc"} {
		body := serve(t, path).Body.String()
		if strings.Contains(body, "http://") || strings.Contains(body, "https://") {
			t.Errorf("%s page references an external URL; it must only use embedded assets", path)
		}
	}
}
