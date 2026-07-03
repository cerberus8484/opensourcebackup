// Package opensourcebackup is the module root. It exists to embed the SQL
// migrations into the binary so the control plane can apply them itself at
// startup — no separate `migrate` step that can silently drift from the code.
package opensourcebackup

import "embed"

// MigrationsFS holds every migration file (up and down) under migrations/.
// The control plane's migrate runner applies the pending *.up.sql files at boot.
//
//go:embed migrations/*.sql
var MigrationsFS embed.FS
