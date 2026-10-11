package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"doriya/internal/models"

	_ "modernc.org/sqlite"
)

type Store struct {
	DB *sql.DB
}

func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // sqlite safe default
	s := &Store{DB: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT UNIQUE NOT NULL,
			pass_hash TEXT NOT NULL,
			salt TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'admin',
			created_at TIMESTAMP NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			token TEXT PRIMARY KEY,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			expires_at TIMESTAMP NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS sources (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			type TEXT NOT NULL,
			config TEXT NOT NULL DEFAULT '{}',
			created_at TIMESTAMP NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS libraries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			type TEXT NOT NULL,
			source_id INTEGER NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
			path TEXT NOT NULL DEFAULT '/',
			providers TEXT NOT NULL DEFAULT '["nfo","tmdb"]',
			created_at TIMESTAMP NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS items (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			library_id INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
			type TEXT NOT NULL,
			parent_id INTEGER REFERENCES items(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			original_title TEXT DEFAULT '',
			year INTEGER DEFAULT 0,
			tmdb_id INTEGER DEFAULT 0,
			overview TEXT DEFAULT '',
			poster_path TEXT DEFAULT '',
			backdrop_path TEXT DEFAULT '',
			still_path TEXT DEFAULT '',
			rating REAL DEFAULT 0,
			genres TEXT DEFAULT '[]',
			season INTEGER DEFAULT 0,
			episode INTEGER DEFAULT 0,
			air_date TEXT DEFAULT '',
			added_at TIMESTAMP NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_items_library ON items(library_id, type)`,
		`CREATE INDEX IF NOT EXISTS idx_items_parent ON items(parent_id)`,
		`CREATE TABLE IF NOT EXISTS files (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
			source_id INTEGER NOT NULL,
			path TEXT NOT NULL,
			size INTEGER DEFAULT 0,
			mtime INTEGER DEFAULT 0,
			probe TEXT DEFAULT '',
			added_at TIMESTAMP NOT NULL,
			UNIQUE(item_id, source_id, path)
		)`,
		`CREATE TABLE IF NOT EXISTS subtitles (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			file_id INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
			path TEXT DEFAULT '',
			lang TEXT DEFAULT '',
			title TEXT DEFAULT '',
			format TEXT DEFAULT '',
			embedded INTEGER DEFAULT 0,
			stream_idx INTEGER DEFAULT 0,
			convertible INTEGER DEFAULT 1,
			UNIQUE(file_id, path, stream_idx)
		)`,
		`CREATE TABLE IF NOT EXISTS progress (
			user_id INTEGER NOT NULL,
			file_id INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
			item_id INTEGER NOT NULL,
			position REAL NOT NULL DEFAULT 0,
			duration REAL NOT NULL DEFAULT 0,
			updated_at TIMESTAMP NOT NULL,
			PRIMARY KEY (user_id, file_id)
		)`,
	}
	for _, stmt := range stmts {
		if _, err := s.DB.Exec(stmt); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}

// ---------- users ----------

func (s *Store) UserCount() (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) CreateUser(u *models.User) error {
	res, err := s.DB.Exec(`INSERT INTO users (name, pass_hash, salt, role, created_at) VALUES (?,?,?,?,?)`,
		u.Name, u.PassHash, u.Salt, u.Role, time.Now())
	if err != nil {
		return err
	}
	u.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) UserByName(name string) (*models.User, error) {
	u := &models.User{}
	err := s.DB.QueryRow(`SELECT id, name, pass_hash, salt, role, created_at FROM users WHERE name = ?`, name).
		Scan(&u.ID, &u.Name, &u.PassHash, &u.Salt, &u.Role, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (s *Store) UserByID(id int64) (*models.User, error) {
	u := &models.User{}
	err := s.DB.QueryRow(`SELECT id, name, pass_hash, salt, role, created_at FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.Name, &u.PassHash, &u.Salt, &u.Role, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}

// ---------- sessions ----------

func (s *Store) CreateSession(token string, userID int64, expires time.Time) error {
	_, err := s.DB.Exec(`INSERT INTO sessions (token, user_id, expires_at) VALUES (?,?,?)`, token, userID, expires)
	return err
}

func (s *Store) SessionByToken(token string) (*models.Session, error) {
	sess := &models.Session{}
	err := s.DB.QueryRow(`SELECT token, user_id, expires_at FROM sessions WHERE token = ?`, token).
		Scan(&sess.Token, &sess.UserID, &sess.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return sess, nil
}

func (s *Store) DeleteSession(token string) error {
	_, err := s.DB.Exec(`DELETE FROM sessions WHERE token = ?`, token)
	return err
}

// ---------- sources ----------

func (s *Store) CreateSource(src *models.Source) error {
	res, err := s.DB.Exec(`INSERT INTO sources (name, type, config, created_at) VALUES (?,?,?,?)`,
		src.Name, src.Type, src.Config, time.Now())
	if err != nil {
		return err
	}
	src.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) UpdateSource(src *models.Source) error {
	_, err := s.DB.Exec(`UPDATE sources SET name=?, type=?, config=? WHERE id=?`, src.Name, src.Type, src.Config, src.ID)
	return err
}

func (s *Store) DeleteSource(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM sources WHERE id=?`, id)
	return err
}

func (s *Store) SourceByID(id int64) (*models.Source, error) {
	src := &models.Source{}
	err := s.DB.QueryRow(`SELECT id, name, type, config, created_at FROM sources WHERE id=?`, id).
		Scan(&src.ID, &src.Name, &src.Type, &src.Config, &src.CreatedAt)
	if err != nil {
		return nil, err
	}
	return src, nil
}

func (s *Store) Sources() ([]models.Source, error) {
	rows, err := s.DB.Query(`SELECT id, name, type, config, created_at FROM sources ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Source
	for rows.Next() {
		var src models.Source
		if err := rows.Scan(&src.ID, &src.Name, &src.Type, &src.Config, &src.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, src)
	}
	return out, rows.Err()
}

// ---------- libraries ----------

func (s *Store) CreateLibrary(lib *models.Library) error {
	res, err := s.DB.Exec(`INSERT INTO libraries (name, type, source_id, path, providers, created_at) VALUES (?,?,?,?,?,?)`,
		lib.Name, lib.Type, lib.SourceID, lib.Path, lib.Providers, time.Now())
	if err != nil {
		return err
	}
	lib.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) UpdateLibrary(lib *models.Library) error {
	_, err := s.DB.Exec(`UPDATE libraries SET name=?, type=?, source_id=?, path=?, providers=? WHERE id=?`,
		lib.Name, lib.Type, lib.SourceID, lib.Path, lib.Providers, lib.ID)
	return err
}

func (s *Store) DeleteLibrary(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM libraries WHERE id=?`, id)
	return err
}

func (s *Store) LibraryByID(id int64) (*models.Library, error) {
	lib := &models.Library{}
	err := s.DB.QueryRow(`SELECT id, name, type, source_id, path, providers, created_at FROM libraries WHERE id=?`, id).
		Scan(&lib.ID, &lib.Name, &lib.Type, &lib.SourceID, &lib.Path, &lib.Providers, &lib.CreatedAt)
	if err != nil {
		return nil, err
	}
	return lib, nil
}

func (s *Store) Libraries() ([]models.Library, error) {
	rows, err := s.DB.Query(`SELECT id, name, type, source_id, path, providers, created_at FROM libraries ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Library
	for rows.Next() {
		var lib models.Library
		if err := rows.Scan(&lib.ID, &lib.Name, &lib.Type, &lib.SourceID, &lib.Path, &lib.Providers, &lib.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, lib)
	}
	return out, rows.Err()
}

// ---------- items ----------

func (s *Store) InsertItem(it *models.Item) error {
	res, err := s.DB.Exec(`INSERT INTO items
		(library_id, type, parent_id, name, original_title, year, tmdb_id, overview, poster_path, backdrop_path, still_path, rating, genres, season, episode, air_date, added_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		it.LibraryID, it.Type, it.ParentID, it.Name, it.OriginalTitle, it.Year, it.TMDBID, it.Overview,
		it.PosterPath, it.BackdropPath, it.StillPath, it.Rating, it.Genres, it.Season, it.Episode, it.AirDate, time.Now())
	if err != nil {
		return err
	}
	it.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) UpdateItemMeta(it *models.Item) error {
	_, err := s.DB.Exec(`UPDATE items SET name=?, original_title=?, year=?, tmdb_id=?, overview=?,
		poster_path=?, backdrop_path=?, still_path=?, rating=?, genres=?, season=?, episode=?, air_date=? WHERE id=?`,
		it.Name, it.OriginalTitle, it.Year, it.TMDBID, it.Overview,
		it.PosterPath, it.BackdropPath, it.StillPath, it.Rating, it.Genres, it.Season, it.Episode, it.AirDate, it.ID)
	return err
}

func (s *Store) DeleteItem(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM items WHERE id=?`, id)
	return err
}

func (s *Store) ItemByID(id int64) (*models.Item, error) {
	it := &models.Item{}
	err := s.DB.QueryRow(`SELECT id, library_id, type, parent_id, name, original_title, year, tmdb_id, overview,
		poster_path, backdrop_path, still_path, rating, genres, season, episode, air_date, added_at
		FROM items WHERE id=?`, id).
		Scan(&it.ID, &it.LibraryID, &it.Type, &it.ParentID, &it.Name, &it.OriginalTitle, &it.Year, &it.TMDBID,
			&it.Overview, &it.PosterPath, &it.BackdropPath, &it.StillPath, &it.Rating, &it.Genres,
			&it.Season, &it.Episode, &it.AirDate, &it.AddedAt)
	if err != nil {
		return nil, err
	}
	return it, nil
}

// ItemChildren returns child items ordered by season/episode or name.
func (s *Store) ItemChildren(parentID int64) ([]models.Item, error) {
	rows, err := s.DB.Query(`SELECT id, library_id, type, parent_id, name, original_title, year, tmdb_id, overview,
		poster_path, backdrop_path, still_path, rating, genres, season, episode, air_date, added_at
		FROM items WHERE parent_id=? ORDER BY season, episode, name`, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanItems(rows)
}

func (s *Store) ItemsByLibrary(libraryID int64, typ string, search string, sort string, offset, limit int) ([]models.Item, int, error) {
	where := []string{"library_id = ?"}
	args := []any{libraryID}
	if typ != "" {
		where = append(where, "type = ?")
		args = append(args, typ)
	}
	if search != "" {
		where = append(where, "name LIKE ?")
		args = append(args, "%"+search+"%")
	}
	w := strings.Join(where, " AND ")
	orderBy := "added_at DESC"
	switch sort {
	case "name":
		orderBy = "name COLLATE NOCASE ASC"
	case "year":
		orderBy = "year DESC, name COLLATE NOCASE ASC"
	case "rating":
		orderBy = "rating DESC, name COLLATE NOCASE ASC"
	}
	var total int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM items WHERE `+w, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := `SELECT id, library_id, type, parent_id, name, original_title, year, tmdb_id, overview,
		poster_path, backdrop_path, still_path, rating, genres, season, episode, air_date, added_at
		FROM items WHERE ` + w + ` ORDER BY ` + orderBy + ` LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items, err := scanItems(rows)
	return items, total, err
}

func (s *Store) RecentItems(typ string, limit int) ([]models.Item, error) {
	rows, err := s.DB.Query(`SELECT id, library_id, type, parent_id, name, original_title, year, tmdb_id, overview,
		poster_path, backdrop_path, still_path, rating, genres, season, episode, air_date, added_at
		FROM items WHERE type=? ORDER BY added_at DESC LIMIT ?`, typ, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanItems(rows)
}

// ItemsNeedingIdent returns items of given types without tmdb id and without custom names,
// used by the metadata enricher.
func (s *Store) ItemsNeedingIdent(types []string) ([]models.Item, error) {
	q := `SELECT id, library_id, type, parent_id, name, original_title, year, tmdb_id, overview,
		poster_path, backdrop_path, still_path, rating, genres, season, episode, air_date, added_at
		FROM items WHERE tmdb_id = 0 AND overview = '' AND type IN (` + placeholders(len(types)) + `)`
	args := make([]any, 0, len(types))
	for _, t := range types {
		args = append(args, t)
	}
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanItems(rows)
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func scanItems(rows *sql.Rows) ([]models.Item, error) {
	var out []models.Item
	for rows.Next() {
		var it models.Item
		if err := rows.Scan(&it.ID, &it.LibraryID, &it.Type, &it.ParentID, &it.Name, &it.OriginalTitle, &it.Year,
			&it.TMDBID, &it.Overview, &it.PosterPath, &it.BackdropPath, &it.StillPath, &it.Rating, &it.Genres,
			&it.Season, &it.Episode, &it.AirDate, &it.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ---------- files ----------

func (s *Store) UpsertFile(f *models.MediaFile) (id int64, err error) {
	res, err := s.DB.Exec(`INSERT INTO files (item_id, source_id, path, size, mtime, probe, added_at)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(item_id, source_id, path) DO UPDATE SET size=excluded.size, mtime=excluded.mtime`,
		f.ItemID, f.SourceID, f.Path, f.Size, f.MTime, f.Probe, time.Now())
	if err != nil {
		return 0, err
	}
	id, _ = res.LastInsertId()
	if n, _ := res.RowsAffected(); n == 1 && id == 0 {
		id, _ = res.LastInsertId()
	}
	return id, nil
}

func (s *Store) FileByID(id int64) (*models.MediaFile, error) {
	f := &models.MediaFile{}
	err := s.DB.QueryRow(`SELECT id, item_id, source_id, path, size, mtime, probe, added_at FROM files WHERE id=?`, id).
		Scan(&f.ID, &f.ItemID, &f.SourceID, &f.Path, &f.Size, &f.MTime, &f.Probe, &f.AddedAt)
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (s *Store) FilesByItem(itemID int64) ([]models.MediaFile, error) {
	rows, err := s.DB.Query(`SELECT id, item_id, source_id, path, size, mtime, probe, added_at FROM files WHERE item_id=? ORDER BY id`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.MediaFile
	for rows.Next() {
		var f models.MediaFile
		if err := rows.Scan(&f.ID, &f.ItemID, &f.SourceID, &f.Path, &f.Size, &f.MTime, &f.Probe, &f.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) UpdateFileProbe(id int64, probe string) error {
	_, err := s.DB.Exec(`UPDATE files SET probe=? WHERE id=?`, probe, id)
	return err
}

func (s *Store) DeleteFilesNotIn(itemID int64, keep map[string]bool) error {
	rows, err := s.DB.Query(`SELECT id, path FROM files WHERE item_id=?`, itemID)
	if err != nil {
		return err
	}
	var toDelete []int64
	for rows.Next() {
		var id int64
		var p string
		if err := rows.Scan(&id, &p); err != nil {
			rows.Close()
			return err
		}
		if !keep[p] {
			toDelete = append(toDelete, id)
		}
	}
	rows.Close()
	for _, id := range toDelete {
		if _, err := s.DB.Exec(`DELETE FROM files WHERE id=?`, id); err != nil {
			return err
		}
	}
	return nil
}

// ---------- subtitles ----------

func (s *Store) UpsertSubtitle(sub *models.Subtitle) error {
	_, err := s.DB.Exec(`INSERT INTO subtitles (file_id, path, lang, title, format, embedded, stream_idx, convertible)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(file_id, path, stream_idx) DO UPDATE SET lang=excluded.lang, title=excluded.title,
		format=excluded.format, embedded=excluded.embedded, convertible=excluded.convertible`,
		sub.FileID, sub.Path, sub.Lang, sub.Title, sub.Format, sub.Embedded, sub.StreamIdx, sub.Convertible)
	return err
}

func (s *Store) SubtitlesByFile(fileID int64) ([]models.Subtitle, error) {
	rows, err := s.DB.Query(`SELECT id, file_id, path, lang, title, format, embedded, stream_idx, convertible
		FROM subtitles WHERE file_id=? ORDER BY id`, fileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Subtitle
	for rows.Next() {
		var sub models.Subtitle
		var embedded, convertible int
		if err := rows.Scan(&sub.ID, &sub.FileID, &sub.Path, &sub.Lang, &sub.Title, &sub.Format, &embedded, &sub.StreamIdx, &convertible); err != nil {
			return nil, err
		}
		sub.Embedded = embedded == 1
		sub.Convertible = convertible == 1
		out = append(out, sub)
	}
	return out, rows.Err()
}

func (s *Store) SubtitleByID(id int64) (*models.Subtitle, error) {
	sub := &models.Subtitle{}
	var embedded, convertible int
	err := s.DB.QueryRow(`SELECT id, file_id, path, lang, title, format, embedded, stream_idx, convertible
		FROM subtitles WHERE id=?`, id).
		Scan(&sub.ID, &sub.FileID, &sub.Path, &sub.Lang, &sub.Title, &sub.Format, &embedded, &sub.StreamIdx, &convertible)
	if err != nil {
		return nil, err
	}
	sub.Embedded = embedded == 1
	sub.Convertible = convertible == 1
	return sub, nil
}

// ---------- progress ----------

func (s *Store) SaveProgress(p *models.Progress) error {
	_, err := s.DB.Exec(`INSERT INTO progress (user_id, file_id, item_id, position, duration, updated_at)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT(user_id, file_id) DO UPDATE SET position=excluded.position, duration=excluded.duration,
		item_id=excluded.item_id, updated_at=excluded.updated_at`,
		p.UserID, p.FileID, p.ItemID, p.Position, p.Duration, time.Now())
	return err
}

func (s *Store) ProgressByUser(userID int64) ([]models.Progress, error) {
	rows, err := s.DB.Query(`SELECT user_id, file_id, item_id, position, duration, updated_at
		FROM progress WHERE user_id=? AND position > 30 ORDER BY updated_at DESC LIMIT 50`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Progress
	for rows.Next() {
		var p models.Progress
		if err := rows.Scan(&p.UserID, &p.FileID, &p.ItemID, &p.Position, &p.Duration, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) ProgressForFile(userID, fileID int64) (*models.Progress, error) {
	p := &models.Progress{}
	err := s.DB.QueryRow(`SELECT user_id, file_id, item_id, position, duration, updated_at
		FROM progress WHERE user_id=? AND file_id=?`, userID, fileID).
		Scan(&p.UserID, &p.FileID, &p.ItemID, &p.Position, &p.Duration, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return p, nil
}
