package api

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"doriyamedia/internal/media"
	"doriyamedia/internal/models"
	"doriyamedia/internal/scan"
	"doriyamedia/internal/source"
)

// ---------- subtitle audit ----------
//
// "Which episodes of my BDRip library have no subtitles at all?" is a question
// the library scan cannot answer on its own: it indexes video files, and
// subtitles are looked for beside a file only when that file is opened. So a
// season can look complete until someone presses play and finds nothing.
//
// Only BDRip is audited. A WebRip release usually has its subtitles burned into
// the picture — there is no file to find and no track to detect, so nothing here
// could tell a subtitled WebRip from a bare one, and reporting it as "missing
// subtitles" would be wrong far more often than right.
//
// The audit runs in two stages, because the two halves of the answer cost
// different amounts:
//
//  1. External subtitles cost one directory listing per folder, which the source
//     answers in milliseconds. Most releases ship their subtitles as sidecar
//     files, so this settles nearly every file within seconds and the list is
//     usable immediately.
//  2. Embedded tracks cost an ffprobe against the source, which for a cloud file
//     is seconds to tens of seconds each. Only the files that stage 1 could not
//     clear are probed, in the background, and a file that turns out to carry a
//     text subtitle track is taken back off the list as soon as it is known.
//
// A file that still needs probing is reported as "checking" rather than as
// missing, so the list never claims more than it has established.

// SubtitleAuditItem is one episode with no subtitles found.
type SubtitleAuditItem struct {
	FileID   int64  `json:"file_id"`
	ItemID   int64  `json:"item_id"`   // the episode, for linking to its season
	SeasonID int64  `json:"season_id"` // the item to open
	ShowID   int64  `json:"show_id"`
	Show     string `json:"show"`
	Season   int    `json:"season"`
	Episode  int    `json:"episode"`
	Title    string `json:"title"`
	Path     string `json:"path"`
	// Checking means the file is queued for (or undergoing) the probe that would
	// prove it carries an embedded track, so it is not yet known to be missing.
	Checking bool `json:"checking"`
	// Error carries a probe failure, which leaves the file on the list for want
	// of a better answer.
	Error string `json:"error,omitempty"`
}

// SubtitleAuditState is the progress and result of one library's audit.
type SubtitleAuditState struct {
	LibraryID int64  `json:"library_id"`
	Running   bool   `json:"running"`
	Phase     string `json:"phase"` // idle | listing | probing | done | error
	// Counts of what the audit looked at.
	Total    int `json:"total"`     // bdrip video files considered
	Skipped  int `json:"skipped"`   // not bdrip, so out of scope
	WithSubs int `json:"with_subs"` // cleared by a subtitle file beside them
	Embedded int `json:"embedded"`  // cleared by a subtitle track inside them
	Missing  int `json:"missing"`   // established as having neither
	Checking int `json:"checking"`  // not yet established
	Failed   int `json:"failed"`    // probes that failed
	// Unverified counts files the audit could not look at because its directory
	// could not be listed. They are left out of the list rather than reported as
	// missing: "could not look" is not "nothing there".
	Unverified int `json:"unverified"`
	ListErrors int `json:"list_errors"` // directories that failed to list
	// Progress of the background probe stage.
	ProbeTotal int     `json:"probe_total"`
	ProbeDone  int     `json:"probe_done"`
	DirCount   int     `json:"dir_count"`
	StartedAt  float64 `json:"started_at"`
	FinishedAt float64 `json:"finished_at"`
	Error      string  `json:"error,omitempty"`

	// Events is the live feed the settings page scrolls, newest last. Same shape
	// as the library scan's, so one component renders both.
	Events []models.ScanEvent `json:"events,omitempty"`
}

// subtitleAudit holds one library's audit. A run is cheap to hold (paths and
// names), so the result survives until the next run.
type subtitleAudit struct {
	mu      sync.Mutex
	state   SubtitleAuditState
	items   []SubtitleAuditItem
	cancel  context.CancelFunc
	running bool
	seq     int64
}

// SubtitleAuditors is the per-library audit registry hung off the server.
type subtitleAuditRegistry struct {
	mu   sync.Mutex
	byID map[int64]*subtitleAudit
}

func newSubtitleAuditRegistry() *subtitleAuditRegistry {
	return &subtitleAuditRegistry{byID: map[int64]*subtitleAudit{}}
}

func (reg *subtitleAuditRegistry) get(libID int64) *subtitleAudit {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	a := reg.byID[libID]
	if a == nil {
		a = &subtitleAudit{}
		reg.byID[libID] = a
	}
	return a
}

func (reg *subtitleAuditRegistry) states() []SubtitleAuditState {
	reg.mu.Lock()
	all := make([]*subtitleAudit, 0, len(reg.byID))
	for _, a := range reg.byID {
		all = append(all, a)
	}
	reg.mu.Unlock()
	out := make([]SubtitleAuditState, 0, len(all))
	for _, a := range all {
		st, _ := a.snapshot()
		out = append(out, st)
	}
	return out
}

func (a *subtitleAudit) snapshot() (SubtitleAuditState, []SubtitleAuditItem) {
	a.mu.Lock()
	defer a.mu.Unlock()
	items := make([]SubtitleAuditItem, len(a.items))
	copy(items, a.items)
	return a.state, items
}

func (a *subtitleAudit) set(fn func(*SubtitleAuditState)) {
	a.mu.Lock()
	fn(&a.state)
	a.mu.Unlock()
}

// addEvent appends one line to the audit's live feed, dropping the oldest when
// the ring is full. Same shape and bound as a library scan's feed.
func (a *subtitleAudit) addEvent(kind, text string) {
	if text == "" {
		return
	}
	a.mu.Lock()
	a.seq++
	a.state.Events = append(a.state.Events, models.ScanEvent{
		Seq:  a.seq,
		At:   float64(time.Now().UnixMilli()) / 1000,
		Kind: kind,
		Text: text,
	})
	if len(a.state.Events) > subAuditEventRing {
		a.state.Events = append(a.state.Events[:0], a.state.Events[len(a.state.Events)-subAuditEventRing:]...)
	}
	a.mu.Unlock()
}

// subAuditEventRing bounds the audit feed, matching the scan's for the same
// reason: a poll every couple of seconds only ever shows the last few lines.
const subAuditEventRing = 100

// stop cancels a running audit; returns whether one was running.
func (a *subtitleAudit) stop() bool {
	a.mu.Lock()
	cancel := a.cancel
	running := a.running
	a.mu.Unlock()
	if running && cancel != nil {
		cancel()
		return true
	}
	return false
}

// bdripOnly reports whether a path belongs to a BDRip release — the scope of the
// audit. Any of the names a Blu-ray release goes by counts.
func bdripOnly(p string) bool {
	low := strings.ToLower(p)
	for _, tok := range []string{"bdrip", "bd-rip", "blu-ray", "bluray", "blu_ray", "bdmv", "bdremux"} {
		if strings.Contains(low, tok) {
			return true
		}
	}
	return false
}

// hasTextSubtitleTrack reports whether a cached probe holds a subtitle track that
// could actually be shown. A bitmap track (PGS/VobSub) needs burning in, so it
// does not make the episode watchable the way a text track does.
func hasTextSubtitleTrack(probeJSON string) (found bool, known bool) {
	if strings.TrimSpace(probeJSON) == "" {
		return false, false
	}
	p := media.ProbeFromCache(&models.MediaFile{Probe: probeJSON})
	if p == nil {
		return false, false
	}
	for _, s := range p.SubtitleStreams() {
		switch s.CodecName {
		case "subrip", "ass", "ssa", "mov_text", "webvtt", "text":
			return true, true
		}
	}
	return false, true
}

// auditPair is a file the audit is considering, with the items needed to report
// and link it.
type auditPair struct {
	file   *models.MediaFile
	item   *models.Item
	season *models.Item
	show   *models.Item
}

// StartSubtitleAudit launches a subtitle audit for one library. It returns
// immediately; the caller polls the status. A second run for the same library
// while one is in flight is refused.
func (s *Server) StartSubtitleAudit(lib *models.Library) error {
	a := s.SubtitleAudits.get(lib.ID)
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return errAlreadyRunning
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.running = true
	a.cancel = cancel
	a.items = nil
	a.seq = 0
	a.state = SubtitleAuditState{
		LibraryID: lib.ID,
		Running:   true,
		Phase:     "listing",
		StartedAt: nowSeconds(),
	}
	a.mu.Unlock()
	a.addEvent("info", "开始字幕体检:"+lib.Name+"(只查 bdrip)")

	go s.runSubtitleAudit(ctx, lib, a)
	return nil
}

var errAlreadyRunning = errAuditRunning{}

type errAuditRunning struct{}

func (errAuditRunning) Error() string { return "该媒体库正在体检中" }

func nowSeconds() float64 { return float64(time.Now().UnixMilli()) / 1000 }

// runSubtitleAudit walks the library's files, settles what it can from directory
// listings, and leaves the rest to a background probe pass.
func (s *Server) runSubtitleAudit(ctx context.Context, lib *models.Library, a *subtitleAudit) {
	finish := func(errMsg string) {
		a.mu.Lock()
		a.state.Running = false
		a.state.FinishedAt = nowSeconds()
		if errMsg != "" {
			a.state.Phase = "error"
			a.state.Error = errMsg
		} else {
			a.state.Phase = "done"
		}
		a.running = false
		a.cancel = nil
		a.mu.Unlock()
	}

	files, err := s.libraryAuditFiles(lib.ID)
	if err != nil {
		finish("读取媒体库文件失败: " + err.Error())
		return
	}

	// Which driver serves this library: all its files share one source.
	drv, err := s.auditDriver(files)
	if err != nil {
		finish(err.Error())
		return
	}
	a.addEvent("file", fmt.Sprintf("媒体库共 %d 个文件,正在逐个目录核对字幕", len(files)))

	pending, skipped, withSubs, unverified, listErrors := s.auditListPass(ctx, lib, a, drv, files)
	if ctx.Err() != nil {
		finish("")
		return
	}

	a.mu.Lock()
	a.items = pending
	checking := 0
	for _, it := range pending {
		if it.Checking {
			checking++
		}
	}
	a.state.Phase = "probing"
	a.state.Skipped = skipped
	a.state.Total = len(pending) + withSubs
	a.state.WithSubs = withSubs
	a.state.Unverified = unverified
	a.state.ListErrors = listErrors
	a.state.Checking = checking
	a.state.Missing = len(pending) - checking
	// The progress figure is every file that still needs a probe, whether it was
	// queued now or is a retry of one that failed before.
	a.state.ProbeTotal = checking
	a.mu.Unlock()

	a.addEvent("file", fmt.Sprintf("外挂字幕查完:%d 个有字幕,%d 个已确认没有,%d 个待确认内嵌",
		withSubs, len(pending)-checking, checking))
	if listErrors > 0 {
		a.addEvent("warn", fmt.Sprintf("%d 个目录读取失败,%d 个文件无法判断", listErrors, unverified))
	}

	s.auditProbePass(ctx, a, drv)

	recount(a)
	finish("")
}

// libraryAuditFiles loads every video file of a library together with the items
// it belongs to, resolving each episode's season and show once.
func (s *Server) libraryAuditFiles(libID int64) ([]auditPair, error) {
	rows, err := s.Store.DB.Query(`
		SELECT f.id, f.item_id, f.source_id, f.path, f.size, f.mtime, f.probe, f.added_at
		FROM files f JOIN items i ON i.id = f.item_id
		WHERE i.library_id = ?
		ORDER BY f.path`, libID)
	if err != nil {
		return nil, err
	}
	var files []*models.MediaFile
	for rows.Next() {
		f := &models.MediaFile{}
		if err := rows.Scan(&f.ID, &f.ItemID, &f.SourceID, &f.Path, &f.Size, &f.MTime, &f.Probe, &f.AddedAt); err != nil {
			rows.Close()
			return nil, err
		}
		files = append(files, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	itemCache := map[int64]*models.Item{}
	loadItem := func(id int64) *models.Item {
		if it, ok := itemCache[id]; ok {
			return it
		}
		it, err := s.Store.ItemByID(id)
		if err != nil {
			itemCache[id] = nil
			return nil
		}
		itemCache[id] = it
		return it
	}

	out := make([]auditPair, 0, len(files))
	for _, f := range files {
		it := loadItem(f.ItemID)
		var season, show *models.Item
		if it != nil && it.ParentID != nil {
			season = loadItem(*it.ParentID)
			if season != nil && season.ParentID != nil {
				show = loadItem(*season.ParentID)
			}
		}
		out = append(out, auditPair{file: f, item: it, season: season, show: show})
	}
	return out, nil
}

func (s *Server) auditDriver(files []auditPair) (source.Driver, error) {
	for _, p := range files {
		if strings.HasPrefix(p.file.Path, "http://") || strings.HasPrefix(p.file.Path, "https://") {
			continue
		}
		return s.Engine.DriverFor(p.file)
	}
	return nil, errNoFiles{}
}

type errNoFiles struct{}

func (errNoFiles) Error() string { return "该媒体库没有可检查的文件" }

// auditListingConcurrency bounds how many directories are listed at once. The
// source is a cloud drive; a handful in parallel is quick without being rude.
const auditListingConcurrency = 6

// auditListPass settles every file it can from the directory it lives in, and
// returns the ones that need a probe. A subtitle file found beside a video
// registers it exactly as playback would, so opening the episode later needs no
// work.
func (s *Server) auditListPass(ctx context.Context, lib *models.Library, a *subtitleAudit, drv source.Driver, files []auditPair) (pending []SubtitleAuditItem, skipped, withSubs, unverified, listErrors int) {
	// group by directory, and collect the subtitle-shaped names in each
	byDir := map[string][]auditPair{}
	var dirs []string
	for _, p := range files {
		if strings.HasPrefix(p.file.Path, "http://") || strings.HasPrefix(p.file.Path, "https://") {
			continue
		}
		d := path.Dir(p.file.Path)
		if _, ok := byDir[d]; !ok {
			dirs = append(dirs, d)
		}
		byDir[d] = append(byDir[d], p)
	}
	a.set(func(st *SubtitleAuditState) { st.DirCount = len(dirs) })

	type listing struct {
		names []string          // subtitle-shaped file names in the directory
		paths map[string]string // name -> driver path, for registering them
		err   error
	}
	listings := make([]listing, len(dirs))
	sem := make(chan struct{}, auditListingConcurrency)
	var wg sync.WaitGroup
	for i, d := range dirs {
		wg.Add(1)
		go func(i int, d string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			lctx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			entries, err := drv.List(lctx, d)
			if err != nil {
				listings[i] = listing{err: err}
				return
			}
			names := make([]string, 0, len(entries))
			paths := map[string]string{}
			for _, e := range entries {
				if e.IsDir {
					continue
				}
				if _, ok := subFormatOf(e.Name); ok {
					names = append(names, e.Name)
					paths[e.Name] = e.Path
				}
			}
			listings[i] = listing{names: names, paths: paths}
		}(i, d)
	}
	wg.Wait()

	for i := range dirs {
		res := listings[i]
		if res.err != nil {
			listErrors++
		}
		for _, p := range byDir[dirs[i]] {
			if !bdripOnly(p.file.Path) {
				skipped++
				continue
			}
			// A directory that cannot be listed cannot be checked. Saying so is
			// not the same as saying the episode has no subtitles, so it is
			// counted apart and kept out of the list.
			if res.err != nil {
				unverified++
				continue
			}
			// A subtitle file beside it settles the question outright.
			if len(res.names) > 0 {
				attached := false
				for _, ok := range scan.SubtitleAttachmentIn(path.Base(p.file.Path), res.names) {
					if ok {
						attached = true
						break
					}
				}
				if attached {
					withSubs++
					s.registerAuditSubtitles(p.file, res.names, res.paths)
					continue
				}
			}
			// No subtitle file. An embedded track would still save it, and a
			// cached probe already knows.
			if found, known := hasTextSubtitleTrack(p.file.Probe); known {
				if found {
					continue
				}
				// Probed, and there is no text track: established as missing.
				it := auditItemFor(p, false, "")
				pending = append(pending, it)
				a.addEvent("sub", "缺字幕 "+auditEventLabel(it))
				continue
			}
			// Nothing known yet: it needs a probe. Not announced as missing —
			// the whole point of the second stage is that this may yet be wrong.
			it := auditItemFor(p, true, "")
			pending = append(pending, it)
			a.addEvent("file", "待确认 "+auditEventLabel(it))
		}
	}
	return pending, skipped, withSubs, unverified, listErrors
}

// auditEventLabel is one episode as the live feed names it.
func auditEventLabel(it SubtitleAuditItem) string {
	show := it.Show
	if show == "" {
		show = "未知剧集"
	}
	if it.Episode > 0 {
		return fmt.Sprintf("%s S%02dE%02d", show, it.Season, it.Episode)
	}
	return show + " 特别篇"
}

func auditItemFor(p auditPair, checking bool, errMsg string) SubtitleAuditItem {
	it := SubtitleAuditItem{
		FileID:   p.file.ID,
		Path:     p.file.Path,
		Checking: checking,
		Error:    errMsg,
	}
	if p.item != nil {
		it.ItemID = p.item.ID
		it.Title = p.item.Name
		it.Season = p.item.Season
		it.Episode = p.item.Episode
	}
	if p.season != nil {
		it.SeasonID = p.season.ID
	}
	if p.show != nil {
		it.ShowID = p.show.ID
		it.Show = p.show.Name
	} else if p.season != nil {
		it.Show = p.season.Name
	}
	return it
}

// registerAuditSubtitles stores what the audit found beside a file, so that
// opening that episode does not have to look again. Failures are ignored: the
// audit is a report, and playback rediscovers subtitles for itself.
func (s *Server) registerAuditSubtitles(file *models.MediaFile, names []string, paths map[string]string) {
	flags := scan.SubtitleAttachmentIn(path.Base(file.Path), names)
	for i, name := range names {
		if name == "" || !flags[i] {
			continue
		}
		dst, ok := paths[name]
		if !ok {
			continue
		}
		format, conv := subFormatOf(name)
		_ = s.Store.UpsertSubtitle(&models.Subtitle{
			FileID: file.ID, Path: dst,
			Lang: media.GuessLang(name), Title: media.SubLabel(name),
			Format: format, Convertible: conv,
		})
	}
}

// auditProbePass probes the files that no listing could settle and takes each
// one off the list the moment its probe shows a text track.
//
// It runs one probe at a time by default: each one pulls a window of a cloud
// file, and this work is in the background behind playback. The concurrency is
// the same knob the post-scan pre-probe uses, so an operator who has already
// decided how hard the server may lean on the source decides this too.
func (s *Server) auditProbePass(ctx context.Context, a *subtitleAudit, drv source.Driver) {
	a.mu.Lock()
	work := make([]SubtitleAuditItem, 0, len(a.items))
	for _, it := range a.items {
		if it.Checking {
			work = append(work, it)
		}
	}
	a.mu.Unlock()

	workers := 1
	if s.Cfg != nil && s.Cfg.Playback.PreProbeConcurrency > 0 {
		workers = s.Cfg.Playback.PreProbeConcurrency
	}
	if workers > 4 {
		workers = 4
	}

	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for _, it := range work {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(it SubtitleAuditItem) {
			defer wg.Done()
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			file, err := s.Store.FileByID(it.FileID)
			if err != nil {
				return
			}
			if _, err := s.Engine.EnsureProbe(ctx, file, drv); err != nil {
				// A failed probe leaves the file on the list, flagged: an
				// unanswered question is not a "no".
				a.addEvent("warn", "探测失败 "+auditEventLabel(it)+":"+err.Error())
				a.markProbed(it.FileID, "", err.Error())
				return
			}
			probeJSON := ""
			if fresh, err := s.Store.FileByID(it.FileID); err == nil {
				probeJSON = fresh.Probe
			}
			a.markProbed(it.FileID, probeJSON, "")
		}(it)
	}
	wg.Wait()
}

// markProbed reconciles one file's entry with what its probe turned out to say.
func (a *subtitleAudit) markProbed(fileID int64, probeJSON, errMsg string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.ProbeDone++
	idx := -1
	for i := range a.items {
		if a.items[i].FileID == fileID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	// remove swaps the found item out of the list, order irrelevant here
	remove := func() {
		a.items = append(a.items[:idx], a.items[idx+1:]...)
		a.state.Checking--
		a.state.Embedded++
	}
	if errMsg != "" {
		a.items[idx].Checking = false
		a.items[idx].Error = errMsg
		a.state.Checking--
		a.state.Missing++
		a.state.Failed++
		return
	}
	if found, known := hasTextSubtitleTrack(probeJSON); known && found {
		a.addEvent("sub", "有内嵌字幕 "+auditEventLabel(a.items[idx])+" → 移出名单")
		remove()
		return
	}
	// Probed, and there is no text track: now it really is missing.
	a.addEvent("sub", "缺字幕 "+auditEventLabel(a.items[idx]))
	a.items[idx].Checking = false
	a.state.Checking--
	a.state.Missing++
}

// recount refreshes the summary numbers from the final list.
func recount(a *subtitleAudit) {
	a.mu.Lock()
	defer a.mu.Unlock()
	missing, checking := 0, 0
	for i := range a.items {
		if a.items[i].Checking {
			checking++
		} else {
			missing++
		}
	}
	a.state.Missing = missing
	a.state.Checking = checking
}

// ---------- handlers ----------

// handleSubtitleAuditStart begins an audit of one library's BDRip subtitles.
func (s *Server) handleSubtitleAuditStart(w http.ResponseWriter, r *http.Request, user *models.User) {
	id, _ := pathID(r)
	lib, err := s.Store.LibraryByID(id)
	if err != nil {
		fail(w, 404, "媒体库不存在")
		return
	}
	if err := s.StartSubtitleAudit(lib); err != nil {
		fail(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// handleSubtitleAuditStop cancels a running audit. Whatever it has established
// so far is kept.
func (s *Server) handleSubtitleAuditStop(w http.ResponseWriter, r *http.Request, user *models.User) {
	id, _ := pathID(r)
	stopped := s.SubtitleAudits.get(id).stop()
	writeJSON(w, 200, map[string]bool{"ok": true, "stopped": stopped})
}

// handleSubtitleAuditStatus reports every library's audit: progress while one
// runs, and the result list afterwards.
func (s *Server) handleSubtitleAuditStatus(w http.ResponseWriter, r *http.Request, user *models.User) {
	q := r.URL.Query()
	only, _ := strconv.ParseInt(q.Get("library_id"), 10, 64)

	type entry struct {
		State SubtitleAuditState  `json:"state"`
		Items []SubtitleAuditItem `json:"items"`
	}
	out := []entry{}
	for _, st := range s.SubtitleAudits.states() {
		if only > 0 && st.LibraryID != only {
			continue
		}
		_, items := s.SubtitleAudits.get(st.LibraryID).snapshot()
		if items == nil {
			items = []SubtitleAuditItem{}
		}
		out = append(out, entry{State: st, Items: items})
	}
	writeJSON(w, 200, map[string]any{"audits": out})
}
