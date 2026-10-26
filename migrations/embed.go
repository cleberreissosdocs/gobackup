// Package migrations embeds the SQL migration files so they can be shipped
// inside the agent/central binaries without needing a separate goose CLI
// install or migrations/ directory on the target machine.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
