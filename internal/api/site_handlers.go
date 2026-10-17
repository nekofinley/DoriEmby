package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"doriyamedia/internal/models"
	"doriyamedia/internal/site"
)

// Site content — the wording, the login-page layout and the footer the owner
// edits from the toolbar.
//
// Reading is public on purpose: the login page is the one page a visitor always
// sees, and it has to render its own copy before anyone has a session. Writing
// is the admin's alone.

func siteResponse(doc site.Document, editable bool) map[string]any {
	out := map[string]any{
		"revision":   doc.Revision,
		"updated_by": doc.UpdatedBy,
		"can_edit":   editable,
		"content":    doc.Content, // nil → the UI keeps its own defaults
	}
	if !doc.UpdatedAt.IsZero() {
		out["updated_at"] = doc.UpdatedAt.Format("2006-01-02T15:04:05Z07:00")
	}
	return out
}

func (s *Server) siteStore(w http.ResponseWriter) (*site.Store, bool) {
	if s.Site == nil {
		fail(w, 503, "站点内容存储不可用")
		return nil, false
	}
	return s.Site, true
}

func (s *Server) handleGetSite(w http.ResponseWriter, r *http.Request) {
	st, ok := s.siteStore(w)
	if !ok {
		return
	}
	// Edits must be visible on the next page load — a cached copy would leave the
	// owner staring at wording they already replaced.
	w.Header().Set("Cache-Control", "no-cache")
	u := s.currentUser(r)
	writeJSON(w, http.StatusOK, siteResponse(st.Snapshot(), u != nil && u.Role == "admin"))
}

func (s *Server) handlePutSite(w http.ResponseWriter, r *http.Request, u *models.User) {
	st, ok := s.siteStore(w)
	if !ok {
		return
	}
	var body struct {
		Content json.RawMessage `json:"content"`
		// Revision is a pointer so an absent field means "no expectation"
		// rather than "expect revision 0", which would collide with a fresh
		// install that has never been saved.
		Revision *int64 `json:"revision"`
	}
	if err := readBody(r, &body); err != nil {
		fail(w, http.StatusBadRequest, "请求格式不正确")
		return
	}
	expect := int64(-1)
	if body.Revision != nil {
		expect = *body.Revision
	}
	doc, err := st.Save(body.Content, u.Name, expect)
	switch {
	case errors.Is(err, site.ErrConflict):
		fail(w, http.StatusConflict, "站点内容已被其他窗口修改,请刷新页面后重试")
		return
	case errors.Is(err, site.ErrTooLarge):
		fail(w, http.StatusRequestEntityTooLarge, err.Error())
		return
	case err != nil:
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, siteResponse(doc, true))
}

func (s *Server) handleResetSite(w http.ResponseWriter, r *http.Request, u *models.User) {
	st, ok := s.siteStore(w)
	if !ok {
		return
	}
	doc, err := st.Reset(u.Name)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, siteResponse(doc, true))
}
