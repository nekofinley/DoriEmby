package db

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"doriyamedia/internal/models"
)

// A server that has been running since before multi-user support has a users
// table without status or last_login_at. Opening that database must add the
// columns and leave the existing admin account usable, rather than failing to
// start or locking its owner out.
func TestOpenUpgradesADatabaseFromBeforeMultiUser(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")

	old, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	if _, err := old.Exec(`CREATE TABLE users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT UNIQUE NOT NULL,
		pass_hash TEXT NOT NULL,
		salt TEXT NOT NULL,
		role TEXT NOT NULL DEFAULT 'admin',
		created_at TIMESTAMP NOT NULL
	)`); err != nil {
		t.Fatalf("create legacy users table: %v", err)
	}
	if _, err := old.Exec(`INSERT INTO users (name, pass_hash, salt, role, created_at) VALUES (?,?,?,?,?)`,
		"admin", "hash", "salt", "admin", time.Now()); err != nil {
		t.Fatalf("insert legacy admin: %v", err)
	}
	if err := old.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open on a legacy database: %v", err)
	}
	defer store.DB.Close()

	u, err := store.UserByName("admin")
	if err != nil {
		t.Fatalf("legacy admin not readable after migration: %v", err)
	}
	if u.Status != models.StatusActive {
		t.Errorf("migrated status = %q, want %q", u.Status, models.StatusActive)
	}
	if u.LastLogin != nil {
		t.Errorf("migrated last_login = %v, want none", u.LastLogin)
	}
	// The account that owned the server before must still own it.
	if !u.IsAdmin() || !u.CanLogin() {
		t.Errorf("migrated admin is not usable: role=%q status=%q", u.Role, u.Status)
	}
	if n, err := store.ActiveAdminCount(); err != nil || n != 1 {
		t.Errorf("ActiveAdminCount = %d (err %v), want 1", n, err)
	}

	// The new columns are usable on the migrated table.
	if err := store.UpdateUserStatus(u.ID, models.StatusDisabled); err != nil {
		t.Fatalf("update status on migrated table: %v", err)
	}
	if again, err := store.UserByID(u.ID); err != nil || again.Status != models.StatusDisabled {
		t.Errorf("status after update = %v (err %v)", again, err)
	}
}
