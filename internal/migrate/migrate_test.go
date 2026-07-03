package migrate

import (
	"testing"
	"testing/fstest"
)

func TestLoadMigrations_ParsesAndSorts(t *testing.T) {
	fsys := fstest.MapFS{
		"migrations/000002_b.up.sql":   {Data: []byte("SELECT 2;")},
		"migrations/000002_b.down.sql": {Data: []byte("-- down")},
		"migrations/000010_c.up.sql":   {Data: []byte("SELECT 10;")},
		"migrations/000001_a.up.sql":   {Data: []byte("SELECT 1;")},
		"migrations/000001_a.down.sql": {Data: []byte("-- down")},
		"README.md":                    {Data: []byte("ignore me")},
	}

	migs, err := loadMigrations(fsys)
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}

	// Only *.up.sql files, sorted ascending by numeric version — not lexical
	// (so 10 comes after 2, and the down files and README are ignored).
	wantVersions := []int{1, 2, 10}
	if len(migs) != len(wantVersions) {
		t.Fatalf("want %d migrations, got %d", len(wantVersions), len(migs))
	}
	for i, want := range wantVersions {
		if migs[i].version != want {
			t.Errorf("migration[%d] version = %d, want %d", i, migs[i].version, want)
		}
	}
	if migs[0].sql != "SELECT 1;" {
		t.Errorf("migration sql not loaded: %q", migs[0].sql)
	}
}

func TestLoadMigrations_RejectsBadName(t *testing.T) {
	fsys := fstest.MapFS{
		"migrations/notanumber_x.up.sql": {Data: []byte("SELECT 1;")},
	}
	if _, err := loadMigrations(fsys); err == nil {
		t.Error("expected error for non-numeric migration prefix, got nil")
	}
}

func TestLoadMigrations_EmptyIsOK(t *testing.T) {
	migs, err := loadMigrations(fstest.MapFS{})
	if err != nil {
		t.Fatalf("empty fs should not error: %v", err)
	}
	if len(migs) != 0 {
		t.Errorf("want 0 migrations, got %d", len(migs))
	}
}
