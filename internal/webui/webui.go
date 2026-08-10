// Package webui embeds the dashboard's static HTML into the binary, so the same file that
// used to be served from src/facade/static/ui.html ships inside the executable with no
// separate asset directory to distribute.
package webui

import _ "embed"

//go:embed ui.html
var UI []byte
