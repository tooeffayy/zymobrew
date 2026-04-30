// Package web embeds the compiled SPA assets so the Zymo binary can
// serve them without external files. The Vite build writes into ./dist;
// `//go:embed all:dist` walks that directory at compile time.
//
// Pre-build (e.g. fresh git clone before running `npm run build`), the
// only file present is dist/.gitkeep — the static handler in
// internal/server detects that and serves a "frontend not built"
// placeholder instead of trying to render a non-existent index.html.
package web

import "embed"

//go:embed all:dist
var DistFS embed.FS
