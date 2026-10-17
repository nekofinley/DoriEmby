package site

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(path, body string) error { return os.WriteFile(path, []byte(body), 0o644) }

func TestSaveSurvivesAReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "site.json")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if doc := st.Snapshot(); doc.Revision != 0 || doc.Content != nil {
		t.Fatalf("fresh store = %+v, want revision 0 and no content", doc)
	}

	saved, err := st.Save(json.RawMessage(`{"login":{"title":"你好"}}`), "admin", 0)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if saved.Revision != 1 {
		t.Fatalf("revision = %d, want 1", saved.Revision)
	}

	// A restart must not lose the owner's wording.
	again, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	doc := again.Snapshot()
	if doc.Revision != 1 || doc.UpdatedBy != "admin" {
		t.Fatalf("reopened = %+v, want revision 1 by admin", doc)
	}
	var got map[string]any
	if err := json.Unmarshal(doc.Content, &got); err != nil {
		t.Fatalf("content is not an object: %v", err)
	}
	if got["login"] == nil {
		t.Fatalf("content lost its login section: %s", doc.Content)
	}
}

// Two windows editing the same site: the second save must be refused rather than
// silently overwriting the first.
func TestSaveRefusesAStaleRevision(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "site.json"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := st.Save(json.RawMessage(`{"a":1}`), "admin", 0); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if _, err := st.Save(json.RawMessage(`{"a":2}`), "admin", 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale save error = %v, want ErrConflict", err)
	}
	if _, err := st.Save(json.RawMessage(`{"a":2}`), "admin", 1); err != nil {
		t.Fatalf("current save: %v", err)
	}
}

// Reset has to move the revision forward, otherwise a window holding the old
// copy would happily write the wording back after it was cleared.
func TestResetBumpsTheRevision(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "site.json"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := st.Save(json.RawMessage(`{"a":1}`), "admin", 0); err != nil {
		t.Fatalf("save: %v", err)
	}
	doc, err := st.Reset("admin")
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if doc.Revision != 2 || doc.Content != nil {
		t.Fatalf("after reset = %+v, want revision 2 and no content", doc)
	}
	if _, err := st.Save(json.RawMessage(`{"a":1}`), "admin", 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("save with pre-reset revision = %v, want ErrConflict", err)
	}
}

func TestSaveRejectsWhatIsNotAnObject(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "site.json"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for _, bad := range []string{`null`, `[]`, `"text"`, `{`, ``} {
		if _, err := st.Save(json.RawMessage(bad), "admin", -1); err == nil {
			t.Errorf("save(%q) succeeded, want an error", bad)
		}
	}
	if _, err := st.Save(json.RawMessage(`{"ok":true}`), "admin", -1); err != nil {
		t.Errorf("save of an object failed: %v", err)
	}
}

// A half-written or hand-edited file must not stop the server from booting: the
// login page is how the owner gets back in to fix it.
func TestOpenSurvivesACorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "site.json")
	if err := writeFile(path, "{not json"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	st, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if doc := st.Snapshot(); doc.Revision != 0 {
		t.Fatalf("corrupt file produced %+v, want a fresh store", doc)
	}
}
