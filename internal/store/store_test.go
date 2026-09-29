package store

import (
	"path/filepath"
	"testing"
)

func TestOpenMigratesOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "yardmaster.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var applied int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied == 0 {
		t.Fatal("no migrations recorded")
	}
	if _, err := db.Exec(`INSERT INTO settings (key, value) VALUES ('kept', '1')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	// Reopening applies nothing twice and keeps the data.
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var again int
	db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&again)
	if again != applied {
		t.Errorf("migrations recorded: %d after reopening, %d before", again, applied)
	}
	var kept int
	if ok, err := db.GetSetting(t.Context(), "kept", &kept); !ok || err != nil || kept != 1 {
		t.Errorf("data lost on reopen: %v %v", ok, err)
	}
}

func TestForeignKeysAreEnforced(t *testing.T) {
	db := OpenTest(t)
	var on int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&on); err != nil || on != 1 {
		t.Fatalf("foreign_keys = %d, %v", on, err)
	}
	_, err := db.Exec(`INSERT INTO sessions (token_hash, user_id, created_at, last_seen_at, expires_at)
		VALUES ('h', 999, 0, 0, 0)`)
	if err == nil {
		t.Error("a session for a user that doesn't exist was accepted")
	}
}

func TestSettings(t *testing.T) {
	db := OpenTest(t)
	ctx := t.Context()
	var got struct{ Name string }
	ok, err := db.GetSetting(ctx, "missing", &got)
	if ok || err != nil {
		t.Fatalf("missing key: %v %v", ok, err)
	}
	for _, name := range []string{"first", "second"} {
		if err := db.SetSetting(ctx, "k", struct{ Name string }{name}); err != nil {
			t.Fatal(err)
		}
	}
	if ok, err := db.GetSetting(ctx, "k", &got); !ok || err != nil || got.Name != "second" {
		t.Errorf("got %+v %v %v, want the second value", got, ok, err)
	}
}
