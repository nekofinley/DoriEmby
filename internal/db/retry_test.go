package db

import (
	"errors"
	"testing"
)

// The retry exists for one concrete failure seen in the wild: a scan issuing
// thousands of writes over many minutes hit SQLite's "unable to open database
// file" — the code it also uses when it cannot create a temporary file — and the
// whole organize phase was thrown away. A statement whose *environment* stumbled
// is worth repeating; one that is simply wrong is not.
func TestRetryableDBError(t *testing.T) {
	retry := []string{
		"unable to open database file (14)",
		"SQL logic error: unable to open database file",
		"database is locked (5) (SQLITE_BUSY)",
		"database table is locked",
		"disk I/O error",
		"database or disk is full",
	}
	for _, m := range retry {
		if !retryableDBError(errors.New(m)) {
			t.Errorf("%q should be retried", m)
		}
	}

	// These mean the statement or schema is wrong; repeating them cannot help.
	permanent := []string{
		"UNIQUE constraint failed: files.source_id, files.path",
		"no such column: nope",
		"syntax error near \"SELEC\"",
		"FOREIGN KEY constraint failed",
		"NOT NULL constraint failed: items.name",
	}
	for _, m := range permanent {
		if retryableDBError(errors.New(m)) {
			t.Errorf("%q must not be retried", m)
		}
	}

	if retryableDBError(nil) {
		t.Error("a nil error is not a failure to retry")
	}
}
