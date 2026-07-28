// Package migrations embeds the SQL migration files so they ship inside the
// single ZonaryOS binary rather than needing to be deployed alongside it.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
