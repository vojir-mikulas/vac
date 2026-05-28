//go:build !embedui

// Package ui serves the React dashboard.
//
// The real build embeds ui/dist via the `embedui` build tag. Without that tag
// (the default), a small placeholder page is served instead, so a fresh clone
// compiles without needing the UI built.
package ui

import "embed"

//go:embed placeholder
var rawFS embed.FS

var files = mustSub(rawFS, "placeholder")
