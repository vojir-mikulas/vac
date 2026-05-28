//go:build embedui

package ui

import "embed"

//go:embed all:dist
var rawFS embed.FS

var files = mustSub(rawFS, "dist")
