// Package migrations embeds the SQL migrations so the desktop build carries
// them inside the executable.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
