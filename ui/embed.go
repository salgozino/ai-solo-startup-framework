// Package ui serves the hand-written monitoring UI via //go:embed.
// No build step, no node toolchain — plain HTML/CSS/JS files embedded at compile time.
package ui

import "embed"

//go:embed index.html style.css app.js
var FS embed.FS
