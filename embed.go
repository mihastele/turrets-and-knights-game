// Package lanesiege exposes the embedded web client so the server binary is
// fully self-contained (one static binary serves everything).
package lanesiege

import "embed"

// Web holds web/ (index.html, client.js, style.css).
//
//go:embed web
var Web embed.FS
