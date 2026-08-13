// Package openapi serves the hand-written OpenAPI spec plus Swagger UI and Redoc pages, so
// the API this binary exposes is as explorable as FastAPI's auto-generated /docs was for the
// Python facade. The viewer JS/CSS is embedded (see assets/), not loaded from a CDN: the tool
// is routinely run on isolated bench networks where a CDN reference renders as a silent blank
// page. Asset provenance and licenses are documented in assets/README.md.
package openapi

import (
	"embed"
	"net/http"
)

//go:embed openapi.yaml
var Spec []byte

//go:embed assets
var assets embed.FS

const swaggerPage = `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<title>EEBUS Testbench -- Swagger UI</title>
<link rel="stylesheet" href="/docs/assets/swagger-ui.css">
</head>
<body>
<div id="swagger-ui"></div>
<script src="/docs/assets/swagger-ui-bundle.js"></script>
<script>
  window.ui = SwaggerUIBundle({url: "/openapi.yaml", dom_id: "#swagger-ui"});
</script>
</body>
</html>`

const redocPage = `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<title>EEBUS Testbench -- API reference</title>
</head>
<body>
<redoc spec-url="/openapi.yaml"></redoc>
<script src="/docs/assets/redoc.standalone.js"></script>
</body>
</html>`

func RegisterRoutes(mux *http.ServeMux) {
	serveSpec := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		_, _ = w.Write(Spec)
	}
	mux.HandleFunc("GET /openapi.yaml", serveSpec)
	// The README and examples reference the spec under the API prefix; both spellings work.
	mux.HandleFunc("GET /api/v1/openapi.yaml", serveSpec)
	mux.HandleFunc("GET /docs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(swaggerPage))
	})
	mux.Handle("GET /docs/assets/", http.StripPrefix("/docs/", http.FileServerFS(assets)))
	mux.HandleFunc("GET /redoc", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(redocPage))
	})
}
