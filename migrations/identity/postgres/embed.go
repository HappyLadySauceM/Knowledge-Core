package postgres

import "embed"

// Files contains the Identity PostgreSQL migration chain used at startup.
//
//go:embed *.sql
var Files embed.FS
