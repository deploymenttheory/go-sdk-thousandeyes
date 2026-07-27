// Package templates holds the Go source templates the code generator renders
// into service packages.
//
// They are kept here, embedded in their own package, rather than beside the
// generator, so the emitted shape can be reviewed and edited as ordinary text
// without reading the generator that drives it.
package templates

import "embed"

// FS holds the .tmpl files.
//
//go:embed *.tmpl
var FS embed.FS
