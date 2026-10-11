package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"doriya/internal/config"
	"doriya/internal/db"
	"doriya/internal/models"
	"doriya/internal/source"
)

type Manager struct {
	Store *db.Store
	Cfg   *config.Config

	listMu     sync.Mutex
	listCache  map[string][]source.Entry // per-EnrichLibrary-run dir listing cache
}

func NewManager(store *db.Store, cfg *config.Config) *Manager {
	return &Manager{Store: store, Cfg: cfg}
}

// cachedList returns the directory listing, deduplicating repeated lookups
// within one enrichment run (hundreds of episodes often share a few dirs —
// each remote List costs a public round-trip).
func (m *Manager) cachedList(ctx context.Context, drv source.Driver, dir string) ([]source.Entry, error) {
	m.listMu.Lock()
	defer m.listMu.Unlock()
	if m.listCache == nil {
		m.listCache = map[string][]source.Entry{}
	}
	if ents, ok := m.listCache[dir]; ok {
		return ents, nil
	}
	ents, err := drv.List(ctx, dir)
	if err != nil {
		return nil, err
	}
	m.listCache[dir] = ents
	return ents, nil
}

func (m *Manager) tmdb() *TMDBClient {
	c := m.Cfg.TMDB
	return NewTMDBClient(c.APIKey, c.Language, c.APIBase, c.ImgBase)
}

func providers(lib *models.Library) []string {
	var out []string
	if lib.Providers != "" {
		_ = json.Unmarshal([]byte(lib.Providers), &out)
	}
	if len(out) == 0 {
		out = []string{"nfo", "tmdb"}
	}
	return out
}

// ---------- rate limit ----------

var (
	rateMu   sync.Mutex
	lastCall time.Time
)

func throttle() {
	rateMu.Lock()
	defer rateMu.Unlock()
	if d := time.Since(lastCall); d < 200*time.Millisecond {
		time.Sleep(200*time.Millisecond - d)
	}
	lastCall = time.Now()
}

// ---------- public API ----------

// SeedListings preloads directory listings gathered during the scan walk so
// the identify pass does not re-fetch them over the network.
func (m *Manager) SeedListings(listings map[string][]source.Entry) {
	m.listMu.Lock()
	defer m.listMu.Unlock()
	if len(listings) == 0 {
		return
	}
	if m.listCache == nil {
		m.listCache = map[string][]source.Entry{}
	}
	for k, v := range listings {
		if _, ok := m.listCache[k]; !ok {
			m.listCache[k] = v
		}
	}
}

// EnrichLibrary identifies all items in a library that lack metadata.
// report is optional progress feedback: report(done, total, human message).
func (m *Manager) EnrichLibrary(lib *models.Library, report func(done, total int, msg string)) {
	say := func(done, total int, msg string) {
		if report != nil {
			report(done, total, msg)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	src, err := m.Store.SourceByID(lib.SourceID)
	if err != nil {
		return
	}
	drv, err := source.NewDriver(src)
	if err != nil {
		return
	}
	defer func() { m.listMu.Lock(); m.listCache = nil; m.listMu.Unlock() }()

	switch lib.Type {
	case "series":
		m.enrichSeries(ctx, drv, lib, say)
	default:
		m.enrichMovies(ctx, drv, lib, say)
	}
}

func (m *Manager) enrichMovies(ctx context.Context, drv source.Driver, lib *models.Library, say func(done, total int, msg string)) {
	items, _, err := m.Store.ItemsByLibrary(lib.ID, "movie", "", "", 0, 500000)
	if err != nil {
		return
	}
	for i := range items {
		select {
		case <-ctx.Done():
			return
		default:
		}
		say(i, len(items), "识别 "+items[i].Name)
		m.enrichMovie(ctx, drv, lib, &items[i])
		say(i+1, len(items), "识别 "+items[i].Name)
	}
}

func (m *Manager) enrichSeries(ctx context.Context, drv source.Driver, lib *models.Library, say func(done, total int, msg string)) {
	shows, _, _ := m.Store.ItemsByLibrary(lib.ID, "show", "", "", 0, 100000)
	seasons, _, _ := m.Store.ItemsByLibrary(lib.ID, "season", "", "", 0, 200000)
	// count all episodes for the progress denominator
	epTotal := 0
	if all, _, err := m.Store.ItemsByLibrary(lib.ID, "episode", "", "", 0, 500000); err == nil {
		epTotal = len(all)
	}
	total := len(shows) + epTotal
	done := 0
	for i := range shows {
		say(done, total, "识别剧集 "+shows[i].Name)
		m.enrichShow(ctx, drv, lib, &shows[i])
		done++
		say(done, total, "识别剧集 "+shows[i].Name)
	}
	// episodes after shows are identified
	showByID := map[int64]models.Item{}
	for _, s := range shows {
		showByID[s.ID] = s
	}
	for _, s := range seasons {
		if s.ParentID == nil {
			continue
		}
		show, ok := showByID[*s.ParentID]
		if !ok {
			continue
		}
		kids, _ := m.Store.ItemChildren(s.ID)
		for i := range kids {
			if done%25 == 0 {
				say(done, total, "识别剧集 "+show.Name)
			}
			m.enrichEpisode(ctx, drv, lib, &kids[i], &show, &s)
			done++
		}
	}
	say(total, total, "识别完成")
}

func (m *Manager) enrichMovie(ctx context.Context, drv source.Driver, lib *models.Library, it *models.Item) {
	if it.TMDBID != 0 && it.PosterPath != "" && it.Overview != "" {
		return
	}
	files, err := m.Store.FilesByItem(it.ID)
	if err != nil || len(files) == 0 {
		return
	}
	for _, p := range providers(lib) {
		switch p {
		case "nfo":
			if m.tryMovieNFO(ctx, drv, it, &files[0]) {
				return
			}
		case "tmdb":
			if m.tryMovieTMDB(ctx, it, it.Name, it.Year) {
				return
			}
		}
	}
}

func (m *Manager) enrichShow(ctx context.Context, drv source.Driver, lib *models.Library, show *models.Item) {
	if show.TMDBID != 0 && show.PosterPath != "" && show.Overview != "" {
		return
	}
	filePath, ok := m.firstEpisodeFile(show.ID)
	if !ok {
		return
	}
	for _, p := range providers(lib) {
		switch p {
		case "nfo":
			if m.tryShowNFO(ctx, drv, show, filePath) {
				return
			}
		case "tmdb":
			if m.tryShowTMDB(ctx, show, show.Name, 0) {
				return
			}
		}
	}
}

func (m *Manager) enrichEpisode(ctx context.Context, drv source.Driver, lib *models.Library, ep *models.Item, show *models.Item, season *models.Item) {
	if ep.Name != "" && !strings.HasPrefix(ep.Name, "绗?") && !strings.HasPrefix(ep.Name, "Episode") {
		// already has a real name (from a previous run) 鈥?still refresh stills if missing
		if ep.StillPath != "" {
			return
		}
	}
	files, err := m.Store.FilesByItem(ep.ID)
	if err != nil || len(files) == 0 {
		return
	}
	// nfo first for episodes (precise), then tmdb season data
	if m.tryEpisodeNFO(ctx, drv, ep, &files[0]) {
		return
	}
	if show.TMDBID > 0 && m.tmdb().Enabled() {
		throttle()
		sd, err := m.tmdb().TVSeason(ctx, show.TMDBID, season.Season)
		if err == nil && sd != nil {
			for _, e := range sd.Episodes {
				if e.EpisodeNumber == ep.Episode {
					ep.Name = orDefaultStr(e.Name, ep.Name)
					ep.Overview = e.Overview
					ep.AirDate = e.AirDate
					if e.StillPath != "" {
						ep.StillPath = m.saveTMDBImage(ctx, ep.ID, "still", show.TMDBID, e.StillPath, "w300")
					}
					_ = m.Store.UpdateItemMeta(ep)
					return
				}
			}
		}
	}
}

// RefreshItem re-identifies one item, optionally pinning a TMDB id.
func (m *Manager) RefreshItem(itemID int64, tmdbID int) error {
	it, err := m.Store.ItemByID(itemID)
	if err != nil {
		return err
	}
	lib, err := m.Store.LibraryByID(it.LibraryID)
	if err != nil {
		return err
	}
	src, err := m.Store.SourceByID(lib.SourceID)
	if err != nil {
		return err
	}
	drv, err := source.NewDriver(src)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	switch it.Type {
	case "movie":
		if tmdbID > 0 {
			return m.tryMovieTMDBByID(ctx, it, tmdbID)
		}
		it.TMDBID, it.Overview, it.PosterPath, it.BackdropPath = 0, "", "", ""
		_ = m.Store.UpdateItemMeta(it)
		f := firstFile(m.Store, it.ID)
		if m.tryMovieNFO(ctx, drv, it, f) {
			return nil
		}
		m.tryMovieTMDB(ctx, it, it.Name, it.Year)
		return nil
	case "show":
		if tmdbID > 0 {
			return m.tryShowTMDBByID(ctx, it, tmdbID)
		}
		it.TMDBID, it.Overview, it.PosterPath, it.BackdropPath = 0, "", "", ""
		_ = m.Store.UpdateItemMeta(it)
		filePath, ok := m.firstEpisodeFile(it.ID)
		if !ok {
			return fmt.Errorf("鍓ч泦涓嬫病鏈夊彲鐢ㄧ殑鍓ч泦鏂囦欢")
		}
		if m.tryShowNFO(ctx, drv, it, filePath) {
			return nil
		}
		m.tryShowTMDB(ctx, it, it.Name, 0)
		return nil
	default:
		return fmt.Errorf("璇ョ被鍨嬩笉鏀寔鎵嬪姩璇嗗埆")
	}
}

func firstFile(store *db.Store, itemID int64) *models.MediaFile {
	fls, _ := store.FilesByItem(itemID)
	if len(fls) == 0 {
		return nil
	}
	return &fls[0]
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

func orDefaultStr(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// ---------- nfo ----------

func (m *Manager) tryMovieNFO(ctx context.Context, drv source.Driver, it *models.Item, f *models.MediaFile) bool {
	if f == nil || isHTTP(f.Path) {
		return false
	}
	dir, base := dirBase(f.Path)
	list, err := m.cachedList(ctx, drv, dir)
	if err != nil {
		return false
	}
	names := map[string]source.Entry{}
	for _, e := range list {
		names[strings.ToLower(e.Name)] = e
	}
	var nfoPath string
	for _, cand := range []string{base + ".nfo", "movie.nfo", baseName2(dir) + ".nfo"} {
		if e, ok := names[strings.ToLower(cand)]; ok && !e.IsDir {
			nfoPath = e.Path
			break
		}
	}
	if nfoPath == "" {
		return false
	}
	raw, err := readSmall(ctx, drv.Open, nfoPath)
	if err != nil {
		return false
	}
	nfo, kind, err := ParseNFO(raw)
	if err != nil || (kind != "movie" && kind != "tvshow") {
		return false
	}
	apply := nfo.Title != ""
	if !apply {
		return false
	}
	if nfo.Title != "" {
		it.Name = nfo.Title
	}
	if nfo.OriginalTitle != "" {
		it.OriginalTitle = nfo.OriginalTitle
	}
	if nfo.Year > 0 {
		it.Year = nfo.Year
	}
	if nfo.Plot != "" {
		it.Overview = nfo.Plot
	}
	if nfo.Rating > 0 {
		it.Rating = nfo.Rating
	}
	if id := nfo.TMDBID(); id > 0 {
		it.TMDBID = id
	}
	// local artwork files
	if p := findImage(names, []string{"poster.jpg", "folder.jpg", "cover.jpg", base + "-poster.jpg", base + "-thumb.jpg"}); p != "" {
		it.PosterPath = m.saveRawImage(ctx, drv, p, it.ID, "poster")
	}
	if p := findImage(names, []string{"fanart.jpg", "backdrop.jpg", base + "-fanart.jpg"}); p != "" {
		it.BackdropPath = m.saveRawImage(ctx, drv, p, it.ID, "backdrop")
	}
	// artwork URLs inside nfo
	if it.PosterPath == "" {
		if u := nfo.PosterURL(); u != "" {
			it.PosterPath = m.saveURLImage(ctx, u, it.ID, "poster")
		}
	}
	if it.BackdropPath == "" {
		if u := nfo.FanartURL(); u != "" {
			it.BackdropPath = m.saveURLImage(ctx, u, it.ID, "backdrop")
		}
	}
	return m.Store.UpdateItemMeta(it) == nil
}

func (m *Manager) tryEpisodeNFO(ctx context.Context, drv source.Driver, ep *models.Item, f *models.MediaFile) bool {
	if f == nil || isHTTP(f.Path) {
		return false
	}
	dir, base := dirBase(f.Path)
	list, err := m.cachedList(ctx, drv, dir)
	if err != nil {
		return false
	}
	names := map[string]source.Entry{}
	for _, e := range list {
		names[strings.ToLower(e.Name)] = e
	}
	var nfoPath string
	if e, ok := names[strings.ToLower(base+".nfo")]; ok && !e.IsDir {
		nfoPath = e.Path
	}
	if nfoPath == "" {
		return false
	}
	raw, err := readSmall(ctx, drv.Open, nfoPath)
	if err != nil {
		return false
	}
	nfo, kind, err := ParseNFO(raw)
	if err != nil || kind != "episode" {
		return false
	}
	if nfo.Title != "" {
		ep.Name = nfo.Title
	}
	if nfo.Plot != "" {
		ep.Overview = nfo.Plot
	}
	if nfo.Season > 0 {
		ep.Season = nfo.Season
	}
	if nfo.Episode > 0 {
		ep.Episode = nfo.Episode
	}
	if nfo.Aired != "" {
		ep.AirDate = nfo.Aired
	}
	if p := findImage(names, []string{base + "-thumb.jpg", "thumb.jpg"}); p != "" {
		ep.StillPath = m.saveRawImage(ctx, drv, p, ep.ID, "still")
	}
	return m.Store.UpdateItemMeta(ep) == nil
}

func (m *Manager) tryShowNFO(ctx context.Context, drv source.Driver, show *models.Item, filePath string) bool {
	if isHTTP(filePath) {
		return false
	}
	dir, _ := dirBase(filePath)
	for depth := 0; depth < 3 && dir != "" && dir != "/" && dir != "."; depth++ {
		list, err := m.cachedList(ctx, drv, dir)
		if err != nil {
			return false
		}
		for _, e := range list {
			if !e.IsDir && strings.EqualFold(e.Name, "tvshow.nfo") {
				raw, err := readSmall(ctx, drv.Open, e.Path)
				if err != nil {
					return false
				}
				nfo, kind, err := ParseNFO(raw)
				if err != nil || (kind != "tvshow" && kind != "movie") {
					return false
				}
				if nfo.Title != "" {
					show.Name = nfo.Title
				}
				if nfo.OriginalTitle != "" {
					show.OriginalTitle = nfo.OriginalTitle
				}
				if nfo.Year > 0 {
					show.Year = nfo.Year
				}
				if nfo.Plot != "" {
					show.Overview = nfo.Plot
				}
				if nfo.Rating > 0 {
					show.Rating = nfo.Rating
				}
				if id := nfo.TMDBID(); id > 0 {
					show.TMDBID = id
				}
				names := map[string]source.Entry{}
				for _, e2 := range list {
					names[strings.ToLower(e2.Name)] = e2
				}
				if p := findImage(names, []string{"poster.jpg", "folder.jpg", "banner.jpg"}); p != "" {
					show.PosterPath = m.saveRawImage(ctx, drv, p, show.ID, "poster")
				}
				if p := findImage(names, []string{"fanart.jpg", "backdrop.jpg"}); p != "" {
					show.BackdropPath = m.saveRawImage(ctx, drv, p, show.ID, "backdrop")
				}
				if show.PosterPath == "" {
					if u := nfo.PosterURL(); u != "" {
						show.PosterPath = m.saveURLImage(ctx, u, show.ID, "poster")
					}
				}
				return m.Store.UpdateItemMeta(show) == nil
			}
		}
		dir = parentDir(dir)
	}
	return false
}

func findImage(names map[string]source.Entry, candidates []string) string {
	for _, c := range candidates {
		if e, ok := names[strings.ToLower(c)]; ok && !e.IsDir {
			return e.Path
		}
	}
	return ""
}

// ---------- tmdb ----------

func (m *Manager) tryMovieTMDB(ctx context.Context, it *models.Item, query string, year int) bool {
	t := m.tmdb()
	if !t.Enabled() || strings.TrimSpace(query) == "" {
		return false
	}
	throttle()
	results, err := t.SearchMovie(ctx, query, year)
	if err != nil || len(results) == 0 {
		return false
	}
	return m.tryMovieTMDBByID(ctx, it, results[0].ID) == nil
}

func (m *Manager) tryMovieTMDBByID(ctx context.Context, it *models.Item, id int) error {
	t := m.tmdb()
	throttle()
	d, err := t.MovieDetail(ctx, id)
	if err != nil {
		return err
	}
	it.TMDBID = d.ID
	if d.Title != "" {
		it.Name = d.Title
	}
	it.OriginalTitle = d.OriginalTitle
	it.Overview = d.Overview
	it.Rating = d.VoteAverage
	if d.ReleaseDate != "" && len(d.ReleaseDate) >= 4 {
		it.Year = atoi(d.ReleaseDate[:4])
	}
	it.Genres = genresJSON(d.Genres)
	if d.PosterPath != "" {
		it.PosterPath = m.saveTMDBImage(ctx, it.ID, "poster", d.ID, d.PosterPath, "w500")
	}
	if d.BackdropPath != "" {
		it.BackdropPath = m.saveTMDBImage(ctx, it.ID, "backdrop", d.ID, d.BackdropPath, "w1280")
	}
	return m.Store.UpdateItemMeta(it)
}

func (m *Manager) tryShowTMDB(ctx context.Context, show *models.Item, query string, year int) bool {
	t := m.tmdb()
	if !t.Enabled() || strings.TrimSpace(query) == "" {
		return false
	}
	throttle()
	results, err := t.SearchTV(ctx, query, year)
	if err != nil || len(results) == 0 {
		return false
	}
	return m.tryShowTMDBByID(ctx, show, results[0].ID) == nil
}

func (m *Manager) tryShowTMDBByID(ctx context.Context, show *models.Item, id int) error {
	t := m.tmdb()
	throttle()
	d, err := t.TVDetail(ctx, id)
	if err != nil {
		return err
	}
	show.TMDBID = d.ID
	if d.Name != "" {
		show.Name = d.Name
	}
	show.OriginalTitle = d.OriginalName
	show.Overview = d.Overview
	show.Rating = d.VoteAverage
	if d.FirstAirDate != "" && len(d.FirstAirDate) >= 4 {
		show.Year = atoi(d.FirstAirDate[:4])
	}
	show.Genres = genresJSON(d.Genres)
	if d.PosterPath != "" {
		show.PosterPath = m.saveTMDBImage(ctx, show.ID, "poster", d.ID, d.PosterPath, "w500")
	}
	if d.BackdropPath != "" {
		show.BackdropPath = m.saveTMDBImage(ctx, show.ID, "backdrop", d.ID, d.BackdropPath, "w1280")
	}
	return m.Store.UpdateItemMeta(show)
}

// ---------- images ----------

func (m *Manager) saveTMDBImage(ctx context.Context, itemID int64, kind string, tmdbID int, path, size string) string {
	t := m.tmdb()
	data, err := t.DownloadImage(ctx, path, size)
	if err != nil || len(data) < 100 {
		return ""
	}
	return m.writeImage(itemID, kind, tmdbID, path, data)
}

func (m *Manager) saveURLImage(ctx context.Context, u string, itemID int64, kind string) string {
	client := &http.Client{Timeout: 60 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return ""
	}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil || len(data) < 100 {
		return ""
	}
	return m.writeImage(itemID, kind, 0, u, data)
}

func (m *Manager) saveRawImage(ctx context.Context, drv source.Driver, p string, itemID int64, kind string) string {
	rc, err := drv.Open(ctx, p)
	if err != nil {
		return ""
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, 20<<20))
	if err != nil || len(data) < 100 {
		return ""
	}
	return m.writeImage(itemID, kind, 0, p, data)
}

func (m *Manager) writeImage(itemID int64, kind string, tmdbID int, srcPath string, data []byte) string {
	dir := m.Cfg.ImagesDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	name := fmt.Sprintf("item_%d_%s", itemID, kind)
	if tmdbID > 0 {
		name = fmt.Sprintf("tmdb_%d_%s", tmdbID, kind)
	}
	switch {
	case strings.Contains(strings.ToLower(srcPath), ".png"):
		name += ".png"
	case strings.Contains(strings.ToLower(srcPath), ".webp"):
		name += ".webp"
	default:
		name += ".jpg"
	}
	full := filepath.Join(dir, name)
	if err := os.WriteFile(full, data, 0o644); err != nil {
		return ""
	}
	return name
}

// ---------- helpers ----------

func (m *Manager) firstEpisodeFile(showID int64) (string, bool) {
	seasons, _ := m.Store.ItemChildren(showID)
	for _, s := range seasons {
		eps, _ := m.Store.ItemChildren(s.ID)
		for _, e := range eps {
			fls, _ := m.Store.FilesByItem(e.ID)
			if len(fls) > 0 {
				return fls[0].Path, true
			}
		}
	}
	return "", false
}

func isHTTP(p string) bool {
	return strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://")
}

func dirBase(p string) (string, string) {
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return "", p
	}
	return p[:i], p[i+1:]
}

func baseName2(dir string) string {
	_, b := dirBase(strings.TrimSuffix(dir, "/"))
	return b
}

func parentDir(dir string) string {
	if i := strings.LastIndex(dir, "/"); i > 0 {
		return dir[:i]
	}
	return ""
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func genresJSON(list []struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}) string {
	names := make([]string, 0, len(list))
	for _, g := range list {
		names = append(names, g.Name)
	}
	raw, _ := json.Marshal(names)
	return string(raw)
}
