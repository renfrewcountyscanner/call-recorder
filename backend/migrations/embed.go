// Package migrations exposes the ordered PostgreSQL migrations to the
// application migration runner. Keeping the SQL embedded makes migrations
// identical in local, container, and external-PostgreSQL deployments.
package migrations

import "embed"

// Files contains every numbered SQL migration in this directory.
//
//go:embed *.sql
var Files embed.FS
