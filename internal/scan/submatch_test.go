package scan

import (
	"strings"
	"testing"
)

// The naming scheme this was written for: the video and its Chinese subtitles
// are labelled by two different groups, with nothing in common as a prefix.
const (
	s2Video   = "[Kamigami and VCB-Studio] Yahari Ore no Seishun Lovecome wa Machigatte Iru. Zoku [01][Ma10p_1080p][x265_flac].mkv"
	s2Video13 = "[Kamigami and VCB-Studio] Yahari Ore no Seishun Lovecome wa Machigatte Iru. Zoku [13][Ma10p_1080p][x265_flac].mkv"
	s2Sub01   = "[VCB-Studio][Yahari_Ore_no_Seishun_Lovecome_wa_Machigatte_Iru._Zoku][01][x265_aac][BDrip_1080P][DED1CA06].SumiSora.tc.ass"
	s2Sub13   = "[VCB-Studio][Yahari_Ore_no_Seishun_Lovecome_wa_Machigatte_Iru._Zoku][13][x265_aac][BDrip_1080P][E47B1E95].SumiSora.tc.ass"
)

func TestSubtitleAttachmentMatchesAcrossNamingSchemes(t *testing.T) {
	cases := []struct {
		name       string
		media, sub string
		want       bool
	}{
		// The regression: the same episode, named by two different groups.
		{"VCB sidecar, same episode", s2Video, s2Sub01, true},
		{"VCB sidecar, episode 13", s2Video13, s2Sub13, true},

		// Season 1 style: the subtitle is named after the video.
		{
			"named after the video",
			"[Kamigami and VCB-Studio] Yahari Ore no Seishun Lovecome wa Machigatte Iru. [01][Ma10p_1080p][x265_flac_aac].mkv",
			"[Kamigami and VCB-Studio] Yahari Ore no Seishun Lovecome wa Machigatte Iru. [01][Ma10p_1080p][x265_flac_aac].Jap.ass",
			true,
		},
		{
			"extension-less language tag",
			"[Kamigami and VCB-Studio] Yahari Ore no Seishun Lovecome wa Machigatte Iru. [01][Ma10p_1080p][x265_flac_aac].mkv",
			"[Kamigami and VCB-Studio] Yahari Ore no Seishun Lovecome wa Machigatte Iru. [01][Ma10p_1080p][x265_flac_aac].Chs",
			true,
		},
		{"S01E02 form", "Show Name S01E02 1080p.mkv", "Show Name S01E02.chs.ass", true},
		{"CJK episode form", "剧名 第01集.mkv", "剧名 第01集 简体.ass", true},

		// Must not match: a different episode of the same show.
		{"wrong episode", s2Video, s2Sub13, false},
		{"wrong episode, S01E form", "Show S01E01.mkv", "Show S01E02.chs.ass", false},

		// Must not match: a different show that happens to share the episode
		// number. This is the case the shared-word rule exists for.
		{
			"different show, same episode number",
			"[Group] Jujutsu Kaisen [01][1080p].mkv",
			"[Group] Kimetsu no Yaiba [01][1080p].ass",
			false,
		},
		{
			"different show, same episode, one shared group tag",
			"[VCB-Studio] Jujutsu Kaisen [01][Ma10p_1080p].mkv",
			"[VCB-Studio] Kimetsu no Yaiba [01][Ma10p_1080p].ass",
			false,
		},
		{
			"different show, only the format words in common",
			"Jujutsu Kaisen 1080p.mkv",
			"Kimetsu no Yaiba 1080p.ass",
			false,
		},
		{
			"one show's title is a prefix of another's",
			"Show [01].mkv",
			"Show Zoku [01].ass",
			false,
		},

		// Specials carry no episode number, so they are never claimed by number.
		{
			"specials are not matched by number",
			s2Video,
			"[VCB-Studio][Yahari_Ore_no_Seishun_Lovecome_wa_Machigatte_Iru._Zoku][OVA][x265_aac][BDrip_1080P][EA284A2C].SumiSora.tc.ass",
			false,
		},

		// Empty input is never a match.
		{"empty subtitle name", s2Video, "", false},
		{"empty media name", "", s2Sub01, false},
	}
	for _, c := range cases {
		if got := SubtitleAttachment(c.media, c.sub); got != c.want {
			t.Errorf("%s: SubtitleAttachment = %v, want %v\n  media=%s\n  sub  =%s",
				c.name, got, c.want, c.media, c.sub)
		}
	}
}

// A bracketed number that carries a label still names the episode. Releases use
// this to mark a finale ("[13 END]") or a version of an episode ("[01 (No Credit
// Ver.)]"), and before these were read the subtitle of the last episode of a
// season was the one file that could never be attached.
func TestBracketEpisodeWithLabel(t *testing.T) {
	cases := []struct {
		name string
		want int
		ok   bool
	}{
		{"13 END", 13, true},
		{"01 (No Credit Ver.)", 1, true},
		{"12v2", 12, true},
		{"12 v2", 12, true},
		{"10.5 END", 10, true},
		{"1080p", 0, false},
		{"2160p", 0, false},
		{"Hi10p", 0, false},
		{"Ma10p_1080p", 0, false},
		{"x265_flac", 0, false},
		{"VCB-Studio", 0, false},
		{"DED1CA06", 0, false},
		{"NCED_EP27", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		got, ok := bracketEpisode(c.name)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("bracketEpisode(%q) = (%d, %v), want (%d, %v)", c.name, got, ok, c.want, c.ok)
		}
	}

	// The whole point: a finale's subtitle attaches to the finale.
	if !SubtitleAttachment(
		"[VCB-Studio] OVERLORD II [13][Ma10p_1080p][x265_flac].mkv",
		"[VCB-Studio] OVERLORD II [13 END][Ma10p_1080p][x265_flac].sc.ass",
	) {
		t.Error(`"[13 END]" subtitle did not attach to episode 13`)
	}
	// …and a No-Credit version is still the episode it is numbered after, so the
	// ordinary episode's subtitles are served and not left unregistered.
	if !SubtitleAttachment(
		"[VCB-Studio] Kekkai Sensen [01][Ma10p_1080p][x265_flac].mkv",
		"[VCB-Studio] Kekkai Sensen [01 (No Credit Ver.)][Ma10p_1080p][x265_flac].Chs.Kamigami.ass",
	) {
		t.Error(`"[01 (No Credit Ver.)]" subtitle did not attach to episode 1`)
	}
	// A resolution is not an episode number, so a clean opening is not claimed
	// by the episode that happens to share its leading digits.
	if SubtitleAttachment(
		"Show [NCED_EP27][1080p].mkv",
		"Show [1080p][x265].ass",
	) {
		t.Error("a resolution bracket was read as an episode number")
	}
}

// Every episode of Season 2 must find exactly its own subtitle and no other.
func TestSubtitleAttachmentSeason2PairsExactlyOnce(t *testing.T) {
	videos := make([]string, 0, 13)
	subs := make([]string, 0, 13)
	for ep := 1; ep <= 13; ep++ {
		n := pad2(ep)
		videos = append(videos, "[Kamigami and VCB-Studio] Yahari Ore no Seishun Lovecome wa Machigatte Iru. Zoku ["+n+"][Ma10p_1080p][x265_flac].mkv")
		subs = append(subs, "[VCB-Studio][Yahari_Ore_no_Seishun_Lovecome_wa_Machigatte_Iru._Zoku]["+n+"][x265_aac][BDrip_1080P][DEADBEEF].SumiSora.tc.ass")
	}
	for i, v := range videos {
		var matched []int
		for j, s := range subs {
			if SubtitleAttachment(v, s) {
				matched = append(matched, j+1)
			}
		}
		if len(matched) != 1 || matched[0] != i+1 {
			t.Errorf("episode %d matched subtitles %v, want exactly [%d]", i+1, matched, i+1)
		}
	}
}

func pad2(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

// The word extraction must not treat codecs, groups, CRCs or script tags as
// content, or two unrelated shows sharing a group tag would look alike.
func TestNameWordsDropsNoise(t *testing.T) {
	words := nameWords(s2Sub01)
	for _, junk := range []string{"vcb", "studio", "sumisora", "x265", "aac", "bdrip", "1080p", "ded1ca06", "tc"} {
		if words[junk] {
			t.Errorf("nameWords kept the noise word %q: %v", junk, words)
		}
	}
	for _, want := range []string{"yahari", "seishun", "lovecome", "machigatte", "zoku"} {
		if !words[want] {
			t.Errorf("nameWords dropped the content word %q: %v", want, words)
		}
	}
	if strings.Contains(strings.Join(keysOf(words), " "), "01") {
		t.Errorf("nameWords kept the episode number as a word: %v", words)
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
