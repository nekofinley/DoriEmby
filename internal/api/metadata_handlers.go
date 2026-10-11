package api

import (
	"net/http"

	"doriyamedia/internal/metadata"
	"doriyamedia/internal/models"
)

// handleMetadataTest performs a live metadata request (TMDB or Bangumi) with
// either the saved settings or the values the user is currently editing (so
// "测试连接" works before saving).
func (s *Server) handleMetadataTest(w http.ResponseWriter, r *http.Request, user *models.User) {
	var req struct {
		Provider string `json:"provider"` // "" / "tmdb" / "bgm"
		APIKey   string `json:"api_key"`
		APIBase  string `json:"api_base"`
		ImgBase  string `json:"img_base"`
		UseProxy *bool  `json:"use_proxy"`
		Proxy    string `json:"proxy"`
		Token    string `json:"token"`
		Keyword  string `json:"keyword"`
	}
	if err := readBody(r, &req); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if req.Provider == "bgm" {
		useProxy := s.Cfg.BGM.UseProxy
		if req.UseProxy != nil {
			useProxy = *req.UseProxy
		}
		res := metadata.TestBGM(r.Context(), s.Cfg.BGM,
			req.Token, req.APIBase, req.Keyword, req.Proxy, useProxy)
		writeJSON(w, 200, res)
		return
	}
	useProxy := s.Cfg.TMDB.UseProxy
	if req.UseProxy != nil {
		useProxy = *req.UseProxy
	}
	res := metadata.TestTMDB(r.Context(), s.Cfg.TMDB,
		req.APIKey, req.APIBase, req.ImgBase, req.Proxy, useProxy)
	writeJSON(w, 200, res)
}
