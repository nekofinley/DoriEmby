package media

import (
	"strings"
	"testing"

	"doriyamedia/internal/models"
)

// Automatic subtitle selection decides what a viewer sees without touching
// anything, so it has to prefer what they can actually read.
func TestPickDefaultSubtitle(t *testing.T) {
	sub := func(id int64, lang, title string) models.Subtitle {
		return models.Subtitle{ID: id, Lang: lang, Title: title, Convertible: true}
	}
	cases := []struct {
		name string
		subs []models.Subtitle
		want int64
	}{
		{"nothing to pick", nil, 0},
		{"only one", []models.Subtitle{sub(7, "eng", "English")}, 7},
		{
			"Chinese beats English regardless of order",
			[]models.Subtitle{sub(1, "eng", "English"), sub(2, "chi", "Chinese")},
			2,
		},
		{
			"Chinese beats Cantonese",
			[]models.Subtitle{sub(1, "yue", "粵語"), sub(2, "chi", "中文")},
			2,
		},
		{
			"Simplified beats Traditional, which share an ISO code",
			[]models.Subtitle{sub(1, "chi", "繁體"), sub(2, "chi", "简体")},
			2,
		},
		{
			// The common anime-mux shape: language tags are absent, the title says it.
			"title hints work when the language tag is missing",
			[]models.Subtitle{sub(1, "", "繁日"), sub(2, "", "简日")},
			2,
		},
		{
			"chs beats cht in the title",
			[]models.Subtitle{sub(1, "", "xxx.cht"), sub(2, "", "xxx.chs")},
			2,
		},
		{
			"an identified language beats an untagged track",
			[]models.Subtitle{sub(1, "", "subs"), sub(2, "jpn", "日本語")},
			2,
		},
		{
			"ties fall back to registration order",
			[]models.Subtitle{sub(3, "chi", "简体"), sub(4, "chi", "简体")},
			3,
		},
		{
			"Chinese still wins when it is listed last",
			[]models.Subtitle{
				sub(1, "jpn", "日本語"), sub(2, "eng", "English"), sub(3, "", "简中"),
			},
			3,
		},
	}
	for _, c := range cases {
		if got := PickDefaultSubtitle(c.subs); got != c.want {
			t.Errorf("%s: PickDefaultSubtitle = %d, want %d", c.name, got, c.want)
		}
	}
}

// An untagged Simplified track must still beat an untagged Traditional one.
func TestPickDefaultSubtitleUntaggedScript(t *testing.T) {
	subs := []models.Subtitle{
		{ID: 1, Title: "繁體中文", Convertible: true},
		{ID: 2, Title: "简体中文", Convertible: true},
	}
	if got := PickDefaultSubtitle(subs); got != 2 {
		t.Fatalf("PickDefaultSubtitle = %d, want 2 (Simplified)", got)
	}
}

// "und" means the container does not know; it must not outrank a real language,
// and the title should still be able to identify the track.
func TestSubtitleRankTreatsUndAsUntagged(t *testing.T) {
	und := subtitleRank(models.Subtitle{Lang: "und", Title: "简日"})
	chi := subtitleRank(models.Subtitle{Lang: "chi", Title: "简日"})
	jpn := subtitleRank(models.Subtitle{Lang: "jpn", Title: "日本語"})
	if und[0] != chi[0] {
		t.Errorf("und (%v) should rank like its title says, chi is %v", und, chi)
	}
	if und[0] >= jpn[0] {
		t.Errorf("a track whose title says Chinese (%v) must beat Japanese (%v)", und, jpn)
	}
}

// Regression: Go's \b is an ASCII word boundary, so `\b简\b` never matched —
// Chinese markers in ordinary names like "xxx.简日.ass" were invisible.
func TestGuessLangFindsCJKMarkers(t *testing.T) {
	cases := map[string]string{
		"Show S01E01.简日.ass":  "chi",
		"Show S01E01.繁日.ass":  "chi",
		"Show S01E01.简体.srt":  "chi",
		"Show S01E01.繁体.srt":  "chi",
		"Show S01E01.中字.srt":  "chi",
		"Show S01E01.cht.srt": "chi",
		"Show S01E01.chs.srt": "chi",
		"Show S01E01.eng.srt": "eng",
		"Show S01E01.日语.ass":  "jpn",
		"Show S01E01.粤语.srt":  "yue",
		"Show.S01E01.unknown": "",
	}
	for name, want := range cases {
		if got := GuessLang(name); got != want {
			t.Errorf("GuessLang(%q) = %q, want %q", name, got, want)
		}
	}
}

// A rendition's cues must be timed against the stream the player is on, which
// begins where a packaging session began — not against the file.
func TestShiftVTT(t *testing.T) {
	in := "WEBVTT\n\nNOTE something\n\n1\n00:00:00.500 --> 00:00:02.000 align:middle\nhello\n\n" +
		"00:00:10.000 --> 00:00:12.500\nworld\nline two\n"

	got := string(ShiftVTT([]byte(in), 0.25))
	// 0.500 - 0.25 = 0.250, and the trailing settings must survive verbatim.
	if !strings.Contains(got, "00:00:00.250 --> 00:00:01.750 align:middle") {
		t.Errorf("first cue not shifted with its settings intact:\n%s", got)
	}
	if !strings.Contains(got, "00:00:09.750 --> 00:00:12.250") {
		t.Errorf("second cue not shifted:\n%s", got)
	}
	if !strings.Contains(got, "NOTE something") || !strings.Contains(got, "WEBVTT") {
		t.Errorf("header blocks must pass through:\n%s", got)
	}
	if !strings.Contains(got, "world\nline two") {
		t.Errorf("a surviving cue's payload must stay with it:\n%s", got)
	}

	// A cue that ended before the stream began is dropped, payload and all.
	got = string(ShiftVTT([]byte(in), 5))
	if strings.Contains(got, "hello") {
		t.Errorf("a cue wholly before the offset must be dropped:\n%s", got)
	}
	if !strings.Contains(got, "00:00:05.000 --> 00:00:07.500") {
		t.Errorf("the later cue must be shifted:\n%s", got)
	}

	// A cue straddling the offset is clamped rather than dropped.
	got = string(ShiftVTT([]byte(in), 1))
	if !strings.Contains(got, "00:00:00.000 --> 00:00:01.000") {
		t.Errorf("a straddling cue must be clamped to zero:\n%s", got)
	}

	// Rounding must not produce an impossible ":60.000" timestamp.
	got = string(ShiftVTT([]byte("WEBVTT\n\n00:01:00.000 --> 00:01:01.000\nx\n"), 0.0004))
	if strings.Contains(got, ":60.000") {
		t.Errorf("rounding produced a 60-second component:\n%s", got)
	}

	// No offset is a no-op, and the input is never mutated.
	orig := []byte(in)
	if out := ShiftVTT(orig, 0); string(out) != in {
		t.Error("a zero offset must return the document unchanged")
	}
	if string(orig) != in {
		t.Error("ShiftVTT must not modify its input")
	}
}
