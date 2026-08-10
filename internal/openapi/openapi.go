// Package openapi serves the hand-written OpenAPI spec plus Swagger UI and Redoc pages, so
// the API this binary exposes is as explorable as FastAPI's auto-generated /docs was for the
// Python facade. Both viewer pages load their JS/CSS from a CDN, same as FastAPI's own
// default Swagger UI -- this needs internet access to render, exactly like the system it
// replaces did, not an offline-capability regression.
package openapi

import (
	_ "embed"
	"net/http"
)

//go:embed openapi.yaml
var Spec []byte

const swaggerPage = `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<title>EEBUS Testbench -- Swagger UI</title>
<link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
<div id="swagger-ui"></div>
<script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
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
<script src="https://cdn.jsdelivr.net/npm/redoc@next/bundles/redoc.standalone.js"></script>
</body>
</html>`

func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		_, _ = w.Write(Spec)
	})
	mux.HandleFunc("GET /docs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(swaggerPage))
	})
	mux.HandleFunc("GET /redoc", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(redocPage))
	})
}
