package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type TMDBClient struct {
	APIKey  string
	Language string
	APIBase string
	ImgBase string
	client  *http.Client
}

func NewTMDBClient(apiKey, language, apiBase, imgBase string) *TMDBClient {
	if language == "" {
		language = "zh-CN"
	}
	return &TMDBClient{
		APIKey:   apiKey,
		Language: language,
		APIBase:  apiBase,
		ImgBase:  imgBase,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (t *TMDBClient) Enabled() bool { return t != nil && t.APIKey != "" }

// authMode: v3 keys are 32-char hex (api_key query param); v4 read access
// tokens are long JWTs (Authorization: Bearer header). Accept both.
func (t *TMDBClient) authMode() string {
	k := strings.TrimSpace(t.APIKey)
	if strings.HasPrefix(k, "eyJ") || len(k) > 64 {
		return "bearer"
	}
	return "query"
}

func (t *TMDBClient) get(ctx context.Context, path string, params map[string]string, out any) error {
	u, err := url.Parse(t.APIBase + path)
	if err != nil {
		return err
	}
	q := u.Query()
	q.Set("language", t.Language)
	for k, v := range params {
		if v != "" {
			q.Set(k, v)
		}
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if t.authMode() == "bearer" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(t.APIKey))
	} else {
		q.Set("api_key", strings.TrimSpace(t.APIKey))
		u.RawQuery = q.Encode()
		req.URL = u
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("tmdb %s: HTTP %d %s", path, resp.StatusCode, string(b))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type TMDBSearchItem struct {
	ID           int     `json:"id"`
	Title        string  `json:"title"`
	Name         string  `json:"name"`
	OriginalTitle string `json:"original_title"`
	OriginalName string `json:"original_name"`
	Overview     string  `json:"overview"`
	PosterPath   string  `json:"poster_path"`
	BackdropPath string  `json:"backdrop_path"`
	ReleaseDate  string  `json:"release_date"`
	FirstAirDate string  `json:"first_air_date"`
	VoteAverage  float64 `json:"vote_average"`
}

type TMDBSearchResp struct {
	Results []TMDBSearchItem `json:"results"`
}

func (t *TMDBClient) SearchMovie(ctx context.Context, query string, year int) ([]TMDBSearchItem, error) {
	params := map[string]string{"query": query, "include_adult": "false"}
	if year > 0 {
		params["year"] = strconv.Itoa(year)
	}
	var r TMDBSearchResp
	if err := t.get(ctx, "/search/movie", params, &r); err != nil {
		return nil, err
	}
	return r.Results, nil
}

func (t *TMDBClient) SearchTV(ctx context.Context, query string, year int) ([]TMDBSearchItem, error) {
	params := map[string]string{"query": query, "include_adult": "false"}
	if year > 0 {
		params["first_air_date_year"] = strconv.Itoa(year)
	}
	var r TMDBSearchResp
	if err := t.get(ctx, "/search/tv", params, &r); err != nil {
		return nil, err
	}
	return r.Results, nil
}

type TMDBGenres struct {
	Genres []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"genres"`
}

type TMDBMovieDetail struct {
	ID            int     `json:"id"`
	Title         string  `json:"title"`
	OriginalTitle string  `json:"original_title"`
	Overview      string  `json:"overview"`
	PosterPath    string  `json:"poster_path"`
	BackdropPath  string  `json:"backdrop_path"`
	ReleaseDate   string  `json:"release_date"`
	VoteAverage   float64 `json:"vote_average"`
	Runtime       int     `json:"runtime"`
	TMDBGenres
}

type TMDBTVDetail struct {
	ID           int     `json:"id"`
	Name         string  `json:"name"`
	OriginalName string  `json:"original_name"`
	Overview     string  `json:"overview"`
	PosterPath   string  `json:"poster_path"`
	BackdropPath string  `json:"backdrop_path"`
	FirstAirDate string  `json:"first_air_date"`
	VoteAverage  float64 `json:"vote_average"`
	NumberOfSeasons int  `json:"number_of_seasons"`
	TMDBGenres
}

type TMDBSeasonDetail struct {
	SeasonNumber int `json:"season_number"`
	Episodes     []struct {
		EpisodeNumber int     `json:"episode_number"`
		Name          string  `json:"name"`
		Overview      string  `json:"overview"`
		StillPath     string  `json:"still_path"`
		AirDate       string  `json:"air_date"`
		VoteAverage   float64 `json:"vote_average"`
	} `json:"episodes"`
}

func (t *TMDBClient) MovieDetail(ctx context.Context, id int) (*TMDBMovieDetail, error) {
	var d TMDBMovieDetail
	if err := t.get(ctx, fmt.Sprintf("/movie/%d", id), nil, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

func (t *TMDBClient) TVDetail(ctx context.Context, id int) (*TMDBTVDetail, error) {
	var d TMDBTVDetail
	if err := t.get(ctx, fmt.Sprintf("/tv/%d", id), nil, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

func (t *TMDBClient) TVSeason(ctx context.Context, tvID, season int) (*TMDBSeasonDetail, error) {
	var d TMDBSeasonDetail
	if err := t.get(ctx, fmt.Sprintf("/tv/%d/season/%d", tvID, season), nil, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// DownloadImage fetches a TMDB image path at the given size, returns raw bytes.
func (t *TMDBClient) DownloadImage(ctx context.Context, imagePath, size string) ([]byte, error) {
	if imagePath == "" || t.ImgBase == "" {
		return nil, fmt.Errorf("no image")
	}
	u := t.ImgBase + "/" + size + imagePath
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("image HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 20<<20))
}
