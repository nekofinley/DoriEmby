package metadata

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// NFOMovie is the Kodi-style movie/tvshow/episode nfo subset we understand.
type NFOMovie struct {
	XMLName       xml.Name `xml:"-"`
	Title         string   `xml:"title"`
	OriginalTitle string   `xml:"originaltitle"`
	Year          int      `xml:"year"`
	Plot          string   `xml:"plot"`
	Rating        float64  `xml:"rating"`
	Season        int      `xml:"season"`
	Episode       int      `xml:"episode"`
	ShowTitle     string   `xml:"showtitle"`
	Aired         string   `xml:"aired"`
	UniqueIDs     []struct {
		Type  string `xml:"type,attr"`
		Value string `xml:",chardata"`
	} `xml:"uniqueid"`
	Thumbs  []string `xml:"thumb"`
	Fanart  struct {
		Thumbs []string `xml:"thumb"`
	} `xml:"fanart"`
}

func ParseNFO(raw []byte) (*NFOMovie, string, error) {
	// trim possible non-xml preamble (kodi allows <root> wrappers)
	decoder := xml.NewDecoder(strings.NewReader(string(raw)))
	decoder.Strict = false
	var probe xml.StartElement
	for {
		tok, err := decoder.Token()
		if err != nil {
			return nil, "", fmt.Errorf("nfo: no xml element found")
		}
		if se, ok := tok.(xml.StartElement); ok {
			probe = se
			break
		}
	}
	switch probe.Name.Local {
	case "movie":
		var m NFOMovie
		if err := xml.Unmarshal(raw, &m); err != nil {
			return nil, "", err
		}
		return &m, "movie", nil
	case "tvshow":
		var m NFOMovie
		if err := xml.Unmarshal(raw, &m); err != nil {
			return nil, "", err
		}
		return &m, "tvshow", nil
	case "episodedetails":
		var m NFOMovie
		if err := xml.Unmarshal(raw, &m); err != nil {
			return nil, "", err
		}
		return &m, "episode", nil
	default:
		return nil, "", fmt.Errorf("nfo: unsupported root <%s>", probe.Name.Local)
	}
}

func (n *NFOMovie) TMDBID() int {
	for _, u := range n.UniqueIDs {
		if u.Type == "tmdb" {
			id, _ := strconv.Atoi(strings.TrimSpace(u.Value))
			return id
		}
	}
	return 0
}

func (n *NFOMovie) PosterURL() string {
	for _, t := range n.Thumbs {
		t = strings.TrimSpace(t)
		if t != "" && !strings.Contains(t, "preview=") {
			return t
		}
	}
	return ""
}

func (n *NFOMovie) FanartURL() string {
	if len(n.Fanart.Thumbs) > 0 {
		return strings.TrimSpace(n.Fanart.Thumbs[0])
	}
	return ""
}

func readSmall(ctx context.Context, open func(context.Context, string) (io.ReadCloser, error), path string) ([]byte, error) {
	rc, err := open(ctx, path)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, 4<<20))
}
