package postgres

import "embed"

// Files contains the Knowledge PostgreSQL migration chain used at startup.
//
//go:embed *.sql
var Files embed.FS
