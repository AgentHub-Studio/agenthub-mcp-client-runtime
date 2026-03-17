// Package db embeds the SQL migration files so they are bundled into the binary.
package db

import "embed"

// FS holds all migration SQL files under db/migrations/.
//
//go:embed migrations
var FS embed.FS
