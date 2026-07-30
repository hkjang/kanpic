package migrations

import "embed"

// FS contains the versioned PostgreSQL schema so the API can initialize an
// empty database without depending on files beside the executable.
//
//go:embed *.sql
var FS embed.FS
