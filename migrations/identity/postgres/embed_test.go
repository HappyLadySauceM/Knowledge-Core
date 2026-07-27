package postgres

import "testing"

func TestFilesContainPairedInitialMigration(t *testing.T) {
	for _, name := range []string{
		"000001_create_users.up.sql",
		"000001_create_users.down.sql",
	} {
		contents, err := Files.ReadFile(name)
		if err != nil {
			t.Fatalf("Files.ReadFile(%q) error = %v", name, err)
		}
		if len(contents) == 0 {
			t.Fatalf("Files.ReadFile(%q) returned an empty migration", name)
		}
	}
}
