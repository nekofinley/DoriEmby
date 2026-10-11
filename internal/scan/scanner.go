package scan

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"doriya/internal/config"
	"doriya/internal/db"
	"doriya/internal/models"
	"doriya/internal/source"
)

type fileRec struct {
	Path string // driver path, or absolute URL for .strm
	Dir  string
	Size int64
	MTime int64
	Strm bool
}

type Scanner struct {
	Store      *db.Store
	Cfg        *config.Config
	OnIdentify func(lib *models.Library) // metadata enrichment hook
	states     sync.Map                  // libraryID -> *models.ScanState

	queue chan *models.Library
	once  sync.Once
}

func (sc *Scanner) State(libID int64) *models.ScanState {
	if v, ok := sc.states.Load(libID); ok {
		return v.(*models.ScanState)
	}
	return &models.ScanState{LibraryID: libID, Phase: "idle"}
}

func (sc *Scanner) AllStates() []*models.ScanState {
	var out []*models.ScanState
	sc.states.Range(func(_, v any) bool {
		out = append(out, v.(*models.ScanState))
		return true
	})
	return out
}

func (sc *Scanner) setState(libID int64, st *models.ScanState) {
	sc.states.Store(libID, st)
}

// ScanLibrary enqueues a library scan (processed sequentially by one worker).
func (sc *Scanner) ScanLibrary(lib *models.Library) error {
	sc.once.Do(func() {
		sc.queue = make(chan *models.Library, 64)
		go sc.worker()
	})
	select {
	case sc.queue <- lib:
		return nil
	default:
		return fmt.Errorf("扫描队列已满,请稍后再试")
	}
}

func (sc *Scanner) worker() {
	for lib := range sc.queue {
		sc.runScan(lib)
	}
}

func (sc *Scanner) runScan(lib *models.Library) error {
	st := &models.ScanState{LibraryID: lib.ID, Running: true, Phase: "walk", StartedAt: float64(time.Now().UnixMilli()) / 1000}
	sc.setState(lib.ID, st)

	src, err := sc.Store.SourceByID(lib.SourceID)
	if err != nil {
		st.Running, st.Error = false, "源不存在: "+err.Error()
		return err
	}
	drv, err := source.NewDriver(src)
	if err != nil {
		st.Running, st.Error = false, err.Error()
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	files, err := walkTree(ctx, drv, lib.Path, sc.Cfg.ScanConcurrency)
	if err != nil && len(files) == 0 {
		st.Running, st.Error = false, "遍历失败: "+err.Error()
		return err
	}

	st.Phase = "organize"
	st.Total = len(files)
	if err := sc.organize(lib, files, st); err != nil {
		st.Running, st.Error = false, "整理失败: "+err.Error()
		return err
	}

	st.Phase = "identify"
	if sc.OnIdentify != nil {
		sc.OnIdentify(lib)
	}
	st.Phase = "done"
	st.Running = false
	return nil
}

const maxDepth = 8

func walkTree(ctx context.Context, drv source.Driver, root string, concurrency int) ([]fileRec, error) {
	if concurrency <= 0 {
		concurrency = 4
	}
	sem := make(chan struct{}, concurrency)
	var mu sync.Mutex
	var files []fileRec
	var firstErr error
	var wg sync.WaitGroup

	var walkDir func(dir string, depth int)
	walkDir = func(dir string, depth int) {
		defer wg.Done()
		if depth > maxDepth {
			return
		}
		select {
		case <-ctx.Done():
			return
		case sem <- struct{}{}:
			defer func() { <-sem }()
		}
		entries, err := drv.List(ctx, dir)
		if err != nil {
			mu.Lock()
			if firstErr == nil {
				firstErr = err
			}
			mu.Unlock()
			return
		}
		for _, e := range entries {
			if SkipEntry(e.Name) {
				continue
			}
			if e.IsDir {
				wg.Add(1)
				walkDir(e.Path, depth+1)
				continue
			}
			low := strings.ToLower(e.Name)
			if strings.HasSuffix(low, ".strm") {
				if url := readStrm(ctx, drv, e.Path); url != "" {
					mu.Lock()
					files = append(files, fileRec{Path: url, Dir: dir, Strm: true})
					mu.Unlock()
				}
				continue
			}
			if !IsVideo(e.Name) {
				continue
			}
			if e.Size > 0 && e.Size < 1<<20 {
				continue // skip tiny junk
			}
			mu.Lock()
			files = append(files, fileRec{Path: e.Path, Dir: dir, Size: e.Size, MTime: e.MTime})
			mu.Unlock()
		}
	}

	wg.Add(1)
	root = strings.TrimSuffix(root, "/")
	if root == "" {
		root = "/"
	}
	walkDir(root, 0)
	wg.Wait()
	return files, firstErr
}

func readStrm(ctx context.Context, drv source.Driver, path string) string {
	rc, err := drv.Open(ctx, path)
	if err != nil {
		return ""
	}
	defer rc.Close()
	raw, err := io.ReadAll(io.LimitReader(rc, 64<<10))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
			return line
		}
		if line != "" && !strings.HasPrefix(line, "#") {
			return ""
		}
	}
	return ""
}

// ---------- organize ----------

type movieGroup struct {
	Title string
	Year  int
	Files []fileRec
}

func (sc *Scanner) organize(lib *models.Library, files []fileRec, st *models.ScanState) error {
	switch lib.Type {
	case "series":
		return sc.organizeSeries(lib, files, st)
	default:
		return sc.organizeMovies(lib, files, st)
	}
}

func (sc *Scanner) organizeMovies(lib *models.Library, files []fileRec, st *models.ScanState) error {
	existing, _, err := sc.Store.ItemsByLibrary(lib.ID, "movie", "", "", 0, 1000000)
	if err != nil {
		return err
	}
	byKey := map[string]int64{}
	for _, it := range existing {
		byKey[NormalizeKey(fmt.Sprintf("%s|%d", it.Name, it.Year))] = it.ID
	}

	groups := map[string]*movieGroup{}
	var order []string
	add := func(key, title string, year int, fr fileRec) {
		g, ok := groups[key]
		if !ok {
			g = &movieGroup{Title: title, Year: year}
			groups[key] = g
			order = append(order, key)
		}
		g.Files = append(g.Files, fr)
	}

	for _, fr := range files {
		if fr.Strm || fr.Path == fr.Dir || fr.Dir == strings.TrimSuffix(lib.Path, "/") {
			// flat file: parse by filename
			p := ParseName(baseName(fr.Path))
			add(groupKey(p), orDefault(p.Title, baseName(fr.Path)), p.Year, fr)
			continue
		}
		fp := ParseName(baseName(fr.Path))
		dp := ParseName(baseName(fr.Dir))
		if dp.Title != "" {
			year := dp.Year
			if year == 0 {
				year = fp.Year
			}
			add(groupKey(dp), dp.Title, year, fr)
		} else {
			add(groupKey(fp), orDefault(fp.Title, baseName(fr.Path)), fp.Year, fr)
		}
	}

	for i, key := range order {
		g := groups[key]
		if g.Title == "" {
			continue
		}
		itemID, ok := byKey[key]
		if !ok {
			it := &models.Item{LibraryID: lib.ID, Type: "movie", Name: g.Title, Year: g.Year}
			if err := sc.Store.InsertItem(it); err != nil {
				return err
			}
			itemID = it.ID
			byKey[key] = it.ID
		}
		for _, fr := range g.Files {
			if _, err := sc.Store.UpsertFile(&models.MediaFile{
				ItemID: itemID, SourceID: lib.SourceID, Path: fr.Path, Size: fr.Size, MTime: fr.MTime,
			}); err != nil {
				return err
			}
		}
		st.Done = i + 1
		st.Current = g.Title
	}
	return sc.cleanup(lib, files)
}

type seriesIndex struct {
	shows    map[string]int64       // normKey -> show id
	seasons  map[string]int64       // showID|season -> season id
	eps      map[string]int64       // seasonID|ep -> episode id
}

func (sc *Scanner) organizeSeries(lib *models.Library, files []fileRec, st *models.ScanState) error {
	idx := &seriesIndex{shows: map[string]int64{}, seasons: map[string]int64{}, eps: map[string]int64{}}

	shows, _, err := sc.Store.ItemsByLibrary(lib.ID, "show", "", "", 0, 100000)
	if err != nil {
		return err
	}
	for _, it := range shows {
		idx.shows[NormalizeKey(it.Name)] = it.ID
	}
	seasons, _, err := sc.Store.ItemsByLibrary(lib.ID, "season", "", "", 0, 200000)
	if err != nil {
		return err
	}
	for _, it := range seasons {
		if it.ParentID != nil {
			idx.seasons[fmt.Sprintf("%d|%d", *it.ParentID, it.Season)] = it.ID
		}
	}
	episodes, _, err := sc.Store.ItemsByLibrary(lib.ID, "episode", "", "", 0, 500000)
	if err != nil {
		return err
	}
	for _, it := range episodes {
		if it.ParentID != nil {
			idx.eps[fmt.Sprintf("%d|%d", *it.ParentID, it.Episode)] = it.ID
		}
	}

	knownFiles := map[string]int64{} // path -> item id (to avoid churn)
	for _, ep := range episodes {
		fls, err := sc.Store.FilesByItem(ep.ID)
		if err != nil {
			return err
		}
		for _, f := range fls {
			knownFiles[f.Path] = ep.ID
		}
	}

	for _, fr := range files {
		if itemID, ok := knownFiles[fr.Path]; ok {
			_ = itemID // file already attached; UpsertFile below will hit conflict by path
		}
		p := ParseName(baseName(fr.Path))
		showTitle := orDefault(p.Title, baseName(fr.Path))
		season := p.Season
		if season == 0 {
			season = seasonFromPath(fr.Path)
		}
		if season == 0 {
			season = 1
		}
		ep := p.Episode

		showKey := NormalizeKey(showTitle)
		showID, ok := idx.shows[showKey]
		if !ok {
			it := &models.Item{LibraryID: lib.ID, Type: "show", Name: showTitle}
			if err := sc.Store.InsertItem(it); err != nil {
				return err
			}
			showID = it.ID
			idx.shows[showKey] = showID
		}

		seasonKey := fmt.Sprintf("%d|%d", showID, season)
		seasonID, ok := idx.seasons[seasonKey]
		if !ok {
			pid := showID
			it := &models.Item{LibraryID: lib.ID, Type: "season", ParentID: &pid, Name: fmt.Sprintf("第 %d 季", season), Season: season}
			if err := sc.Store.InsertItem(it); err != nil {
				return err
			}
			seasonID = it.ID
			idx.seasons[seasonKey] = seasonID
		}

		epKey := fmt.Sprintf("%d|%d", seasonID, ep)
		epID, ok := idx.eps[epKey]
		if !ok {
			pid := seasonID
			it := &models.Item{LibraryID: lib.ID, Type: "episode", ParentID: &pid,
				Name: fmt.Sprintf("第 %d 集", ep), Season: season, Episode: ep}
			if err := sc.Store.InsertItem(it); err != nil {
				return err
			}
			epID = it.ID
			idx.eps[epKey] = epID
		}

		if _, err := sc.Store.UpsertFile(&models.MediaFile{
			ItemID: epID, SourceID: lib.SourceID, Path: fr.Path, Size: fr.Size, MTime: fr.MTime,
		}); err != nil {
			return err
		}
		st.Done++
		st.Current = fmt.Sprintf("%s S%02dE%02d", showTitle, season, ep)
	}
	return sc.cleanup(lib, files)
}

// cleanup deletes files no longer present and prunes childless items.
func (sc *Scanner) cleanup(lib *models.Library, files []fileRec) error {
	seen := map[string]bool{}
	for _, fr := range files {
		seen[fr.Path] = true
	}
	rows, err := sc.Store.DB.Query(`SELECT f.id, f.path FROM files f JOIN items i ON i.id = f.item_id WHERE i.library_id = ?`, lib.ID)
	if err != nil {
		return err
	}
	var gone []int64
	for rows.Next() {
		var id int64
		var p string
		if err := rows.Scan(&id, &p); err != nil {
			rows.Close()
			return err
		}
		if !seen[p] {
			gone = append(gone, id)
		}
	}
	rows.Close()
	for _, id := range gone {
		if _, err := sc.Store.DB.Exec(`DELETE FROM files WHERE id=?`, id); err != nil {
			return err
		}
	}
	stmts := []string{
		`DELETE FROM items WHERE library_id=? AND type IN ('movie','episode') AND id NOT IN (SELECT DISTINCT item_id FROM files)`,
		`DELETE FROM items WHERE library_id=? AND type='season' AND id NOT IN (SELECT DISTINCT parent_id FROM items WHERE parent_id IS NOT NULL AND library_id=?)`,
		`DELETE FROM items WHERE library_id=? AND type='show' AND id NOT IN (SELECT DISTINCT parent_id FROM items WHERE parent_id IS NOT NULL AND library_id=?)`,
		// second pass in case shows became empty after seasons were pruned
		`DELETE FROM items WHERE library_id=? AND type IN ('movie','episode') AND id NOT IN (SELECT DISTINCT item_id FROM files)`,
	}
	for _, stmt := range stmts {
		if strings.Count(stmt, "?") == 1 {
			if _, err := sc.Store.DB.Exec(stmt, lib.ID); err != nil {
				return err
			}
		} else {
			if _, err := sc.Store.DB.Exec(stmt, lib.ID, lib.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func groupKey(p Parsed) string {
	return NormalizeKey(fmt.Sprintf("%s|%d", p.Title, p.Year))
}

func baseName(path string) string {
	p := strings.TrimSuffix(path, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func seasonFromPath(path string) int {
	parts := strings.Split(path, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if n, ok := ParseSeasonDir(parts[i]); ok {
			return n
		}
	}
	return 0
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}
