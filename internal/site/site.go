// Package site stores the site's own copy — the wording, the login-page layout
// choices and the footer the owner edits from the browser toolbar.
//
// It is deliberately schema-less: the server keeps one JSON object, checks that
// it is an object and that it is not absurdly large, and hands it back. The
// shape of that object belongs to the web UI, which owns both the defaults and
// the fields it knows how to render, so a new editable line never needs a server
// release — and a key the owner saved but a later UI no longer understands is
// simply ignored rather than rejected.
package site

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ErrConflict is returned when a save was built on a revision that is no longer
// the current one — two windows editing the same site.
var ErrConflict = errors.New("revision conflict")

// ErrTooLarge is returned for a document past MaxBytes.
var ErrTooLarge = fmt.Errorf("site content is too large (limit %d KB)", MaxBytes>>10)

// MaxBytes bounds the stored document. The real one is a couple of kilobytes of
// text; the limit only exists so a bug in the UI cannot fill the disk.
const MaxBytes = 96 << 10

// Document is the stored site: the content the UI renders plus who last touched
// it and when, so the toolbar can say "已发布" honestly.
type Document struct {
	// Revision counts saves. A client sends back the revision it loaded and the
	// store refuses the write if someone else got there first.
	Revision int64 `json:"revision"`
	UpdatedAt time.Time `json:"updated_at"`
	UpdatedBy string    `json:"updated_by"`
	// Content is the JSON object the web UI renders. Nil means "never saved":
	// the UI falls back to the defaults it ships with.
	Content json.RawMessage `json:"content"`
}

// Store keeps the document in memory and mirrors it to a file beside the
// database, so an edit survives a restart without a migration.
type Store struct {
	path string

	mu  sync.RWMutex
	doc Document
}

// Open reads the stored document if there is one. A missing file is not an
// error: it is a fresh install, which renders the built-in defaults.
func Open(path string) (*Store, error) {
	s := &Store{path: path}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	var doc Document
	if err := json.Unmarshal(raw, &doc); err != nil {
		// A corrupt file must not stop the server from booting: the login page
		// is how the owner gets in to fix it.
		return s, nil
	}
	s.doc = doc
	return s, nil
}

// Snapshot returns the current document. The caller gets its own copy of the
// content, so a later save cannot mutate a response already being written.
func (s *Store) Snapshot() Document {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return clone(s.doc)
}

// clone detaches the content bytes from the stored document. It takes no lock of
// its own: Save and Reset already hold the write lock when they hand their result
// back, and sync.RWMutex would deadlock on a nested read.
func clone(doc Document) Document {
	out := doc
	if doc.Content != nil {
		out.Content = append(json.RawMessage(nil), doc.Content...)
	}
	return out
}

// Save stores content as the new revision. Pass expect >= 0 to require that the
// stored revision still matches what the client loaded; pass -1 to force the
// write regardless (used by tests and by a deliberate "overwrite").
func (s *Store) Save(content json.RawMessage, by string, expect int64) (Document, error) {
	if err := validate(content); err != nil {
		return Document{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if expect >= 0 && expect != s.doc.Revision {
		return Document{}, ErrConflict
	}
	next := Document{
		Revision:  s.doc.Revision + 1,
		UpdatedAt: time.Now(),
		UpdatedBy: by,
		Content:   append(json.RawMessage(nil), content...),
	}
	if err := s.persist(next); err != nil {
		return Document{}, err
	}
	s.doc = next
	return clone(next), nil
}

// Reset drops the stored content back to the built-in defaults. It bumps the
// revision rather than zeroing it, so a window still holding the old copy is
// told its write is stale instead of silently reinstating what was just wiped.
func (s *Store) Reset(by string) (Document, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := Document{
		Revision:  s.doc.Revision + 1,
		UpdatedAt: time.Now(),
		UpdatedBy: by,
		Content:   nil,
	}
	if err := s.persist(next); err != nil {
		return Document{}, err
	}
	s.doc = next
	return clone(next), nil
}

// persist writes the document to disk before it is published in memory, and
// writes through a temporary file so a crash mid-write cannot leave half a
// document behind for the next boot to read.
func (s *Store) persist(doc Document) error {
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// validate accepts only a JSON object of a sane size. The UI owns the schema, so
// this is the whole contract: an object, not null, not an array, not huge.
func validate(content json.RawMessage) error {
	if len(content) == 0 {
		return errors.New("内容为空")
	}
	if len(content) > MaxBytes {
		return ErrTooLarge
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(content, &obj); err != nil {
		return errors.New("内容不是合法的 JSON 对象")
	}
	if obj == nil {
		return errors.New("内容不是合法的 JSON 对象")
	}
	return nil
}
