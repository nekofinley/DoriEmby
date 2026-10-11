package models

import "time"

type User struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	PassHash  string    `json:"-"`
	Salt      string    `json:"-"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

type Session struct {
	Token     string    `json:"token"`
	UserID    int64     `json:"user_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Source struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"` // local | webdav | openlist
	Config    string    `json:"-"`    // raw json
	CreatedAt time.Time `json:"created_at"`
}

// SourceConfig is the union of all driver configs.
type SourceConfig struct {
	// local
	Root string `json:"root,omitempty"`
	// webdav
	WebDAVURL  string `json:"webdav_url,omitempty"`
	WebDAVUser string `json:"webdav_user,omitempty"`
	WebDAVPass string `json:"webdav_pass,omitempty"`
	// openlist / alist v3
	BaseURL string `json:"base_url,omitempty"`
	Token   string `json:"token,omitempty"`
}

type Library struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"` // movie | series
	SourceID  int64     `json:"source_id"`
	Path      string    `json:"path"`
	Providers string    `json:"providers"` // json array e.g. ["nfo","tmdb"]
	CreatedAt time.Time `json:"created_at"`
}

type Item struct {
	ID            int64     `json:"id"`
	LibraryID     int64     `json:"library_id"`
	Type          string    `json:"type"` // movie | show | season | episode
	ParentID      *int64    `json:"parent_id,omitempty"`
	Name          string    `json:"name"`
	OriginalTitle string    `json:"original_title,omitempty"`
	Year          int       `json:"year,omitempty"`
	TMDBID        int       `json:"tmdb_id,omitempty"`
	BGMID         int       `json:"bgm_id,omitempty"` // Bangumi (bgm.tv) subject id
	Overview      string    `json:"overview,omitempty"`
	PosterPath    string    `json:"poster_path,omitempty"`  // local cache file name
	BackdropPath  string    `json:"backdrop_path,omitempty"`
	StillPath     string    `json:"still_path,omitempty"`
	Rating        float64   `json:"rating,omitempty"`
	Genres        string    `json:"genres,omitempty"` // json array
	Season        int       `json:"season,omitempty"`
	Episode       int       `json:"episode,omitempty"`
	AirDate       string    `json:"air_date,omitempty"`
	AddedAt       time.Time `json:"added_at"`
}

type MediaFile struct {
	ID       int64     `json:"id"`
	ItemID   int64     `json:"item_id"`
	SourceID int64     `json:"source_id"`
	Path     string    `json:"path"` // driver path or absolute URL (strm)
	Size     int64     `json:"size"`
	MTime    int64     `json:"mtime"`
	Probe    string    `json:"-"` // cached probe json
	AddedAt  time.Time `json:"added_at"`
}

type Subtitle struct {
	ID        int64   `json:"id"`
	FileID    int64   `json:"file_id"`
	Path      string  `json:"path"` // driver path (external) or empty
	Lang      string  `json:"lang,omitempty"`
	Title     string  `json:"title,omitempty"`
	Format    string  `json:"format,omitempty"` // subrip|ass|webvtt|hdmv_pgs_subtitle...
	Embedded  bool    `json:"embedded"`
	StreamIdx int     `json:"stream_idx,omitempty"`
	Convertible bool  `json:"convertible"`
}

type Progress struct {
	UserID   int64     `json:"user_id"`
	FileID   int64     `json:"file_id"`
	ItemID   int64     `json:"item_id"`
	Position float64   `json:"position"`
	Duration float64   `json:"duration"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ScanState struct {
	LibraryID int64   `json:"library_id"`
	Running   bool    `json:"running"`
	Phase     string  `json:"phase"` // walk | organize | identify | done | error
	Total     int     `json:"total"`
	Done      int     `json:"done"`
	Current   string  `json:"current"`
	Error     string  `json:"error,omitempty"`
	StartedAt float64 `json:"started_at"`

	// walk progress
	DirsDone   int    `json:"dirs_done"`
	FilesFound int    `json:"files_found"`
	WalkErrs   int    `json:"walk_errors,omitempty"`
	FirstErr   string `json:"first_error,omitempty"`

	FinishedAt float64 `json:"finished_at,omitempty"`
}
