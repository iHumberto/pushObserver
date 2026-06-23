// Package server — embedded HTML templates for dashboard rendering.
//
// Templates are compiled into the binary via Go's embed package (Go 1.16+),
// eliminating runtime filesystem dependency. This fixes the Docker panic:
//
//	panic: html/template: pattern matches no files: `internal/server/templates/*.html`
//
// The Dockerfile no longer needs to COPY template files — they're part of the binary.
package server

import "embed"

// TemplatesFS provides embedded access to HTML templates used by UIRenderer.
// Templates are stored in templates/*.html and accessed via ParseFS:
//
//	tmpl, err := template.New("").Funcs(TemplateFuncs()).ParseFS(TemplatesFS, "templates/*.html")
//
//go:embed templates/*.html
var TemplatesFS embed.FS
