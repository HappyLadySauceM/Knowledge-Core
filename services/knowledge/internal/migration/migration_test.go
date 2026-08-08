package migration

import (
	"io/fs"
	"sort"
	"testing"
)

func TestEmbeddedMigrationsHaveIncreasingVersions(t *testing.T) {
	names, err := fs.Glob(files, "migrations/*.sql")
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	sort.Strings(names)
	var previous int64
	for _, name := range names {
		version, err := migrationVersion(name)
		if err != nil {
			t.Fatalf("migrationVersion(%q) error = %v", name, err)
		}
		if version <= previous {
			t.Fatalf("migration %q version = %d after %d", name, version, previous)
		}
		previous = version
	}
}
