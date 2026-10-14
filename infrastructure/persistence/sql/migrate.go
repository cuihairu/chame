package sql

import (
	"embed"
	"fmt"
	"sort"
	"strings"
)

//go:embed schema/*.sql
var schemaFS embed.FS

// migration is one versioned schema step (ADR-0002: explicit,
// versioned SQL checked into the repository, applied by the store, no
// ORM).
type migration struct {
	version string
	body    string
}

// migrations lists every embedded schema file in version order. A file
// whose stem is not three digits plus an underscore name is a packaging
// error caught at first use.
var migrations = loadMigrations()

func loadMigrations() []migration {
	entries, err := schemaFS.ReadDir("schema")
	if err != nil {
		panic(fmt.Sprintf("sql: embedded schema unreadable: %v", err))
	}
	var out []migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		body, err := schemaFS.ReadFile("schema/" + e.Name())
		if err != nil {
			panic(fmt.Sprintf("sql: embedded schema %s unreadable: %v", e.Name(), err))
		}
		out = append(out, migration{version: strings.TrimSuffix(e.Name(), ".sql"), body: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out
}
