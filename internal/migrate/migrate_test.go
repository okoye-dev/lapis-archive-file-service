package migrate

import "testing"

func TestMigrationFilesSortedAndComplete(t *testing.T) {
	files, err := migrationFiles()
	if err != nil {
		t.Fatalf("migrationFiles: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no migration files embedded")
	}

	for i := 1; i < len(files); i++ {
		if files[i-1] >= files[i] {
			t.Errorf("migrations not sorted: %q before %q", files[i-1], files[i])
		}
	}
	if files[0] != "0001_init.sql" {
		t.Errorf("first migration = %q, want 0001_init.sql", files[0])
	}
}
