package scan

import (
	"regexp"
	"strings"
	"unicode"
)

// SubtitleAttachment decides whether a subtitle file belongs to a media file.
//
// The rule that used to be enough — "the subtitle's name starts with the video's
// name" — only holds for releases that name both sides the same way. Real
// libraries do not: beside
//
//	[Kamigami and VCB-Studio] … Machigatte Iru. Zoku [01][Ma10p_1080p][x265_flac].mkv
//
// VCB-Studio ship their Chinese subtitles as
//
//	[VCB-Studio][Yahari_Ore_no_Seishun_…_Zoku][01][x265_aac][BDrip_1080P][DED1CA06].SumiSora.tc.ass
//
// which shares no prefix at all with the video, so the subtitles of a whole
// season went unregistered even though every file sat in the same directory.
// Nothing here renames anything: the release is read as the two labels it
// carries — which episode, and which show.
//
// Two labels have to agree, so a file cannot be attached to the wrong thing:
//
//  1. the episode number, read by the same parser that names episodes
//     ("[01]", "S01E02", "第 01 集"), and
//  2. a word of the show's name, so that two episodes of different shows that
//     merely share a number — the one way a same-directory library can trip this
//     up — do not get wired together.
//
// Everything else about the name (codecs, resolutions, group tags, CRC, the
// language tag) is noise for this decision and is dropped.
func SubtitleAttachment(mediaName, subName string) bool {
	if mediaName == "" || subName == "" {
		return false
	}
	return subtitleAttachment(newSubName(mediaName), newSubName(subName))
}

// subName is one release name, read once. A directory holds a whole season, so
// the video side is read once and every candidate is tested against it rather
// than reparsing the same name for each file.
type subName struct {
	stem  string          // the name without its extension, lowercased
	stems []string        // stem, and stem with a trailing language tag removed
	words map[string]bool // the content words
	ep    int             // episode number, 0 when the name does not say
	isEp  bool
}

func newSubName(name string) subName {
	ep, isEp := parsedEpisode(name)
	return subName{
		stem:  strings.ToLower(stripExt(name)),
		stems: subStems(name),
		words: nameWords(name),
		ep:    ep,
		isEp:  isEp,
	}
}

// SubtitleAttachmentIn reports which of a directory's subtitles belong to one
// media file, reusing the media side across every candidate.
func SubtitleAttachmentIn(mediaName string, subNames []string) []bool {
	out := make([]bool, len(subNames))
	if mediaName == "" {
		return out
	}
	media := newSubName(mediaName)
	for i, s := range subNames {
		if s == "" {
			continue
		}
		out[i] = subtitleAttachment(media, newSubName(s))
	}
	return out
}

func subtitleAttachment(media, sub subName) bool {
	// The old rule first, but only where the two names really are one name: a
	// subtitle called after the video it belongs to is never wrong, and this keeps
	// every naming scheme that already worked. It requires the video's *whole*
	// name — episode marker included — because a bare title is not an episode:
	// "Show [01]" must not claim "Show [02]'s" subtitles, and a title that is a
	// prefix of another show's ("Show" vs "Show Zoku") is not this episode either.
	// Only a group tag in common ("[Group] …") is not a title, hence the floor on
	// how much of the name has to agree.
	if media.stem != "" {
		for _, m := range sub.stems {
			switch {
			case m == media.stem:
				return true
			case strings.HasPrefix(m, media.stem) && meaningfulPrefix(media.stem):
				return true
			case strings.HasPrefix(media.stem, m+" ") && meaningfulPrefix(m):
				return true
			}
		}
	}

	// Otherwise fall back on the two labels, both of which must be usable.
	if !media.isEp || !sub.isEp || media.ep <= 0 || media.ep != sub.ep {
		return false
	}
	// One word in common is not a show's name. Release groups write the same
	// labels on everything they touch — "[Group]", a studio, a language tag — and
	// those are single words, so two different shows would look like one. Two
	// words of the name, or a name that is one long word, is a show.
	shared := 0
	longest := 0
	for w := range sub.words {
		if !media.words[w] {
			continue
		}
		shared++
		if n := len([]rune(w)); n > longest {
			longest = n
		}
	}
	return shared >= 2 || (shared == 1 && longest >= minDistinctiveWord)
}

// parsedEpisode reads the episode number the same way the library does, so a
// name is never interpreted one way when scanning and another when matching.
func parsedEpisode(name string) (int, bool) {
	p := ParseName(name)
	if !p.IsEpisode || p.Episode <= 0 {
		return 0, false
	}
	return p.Episode, true
}

// minDistinctiveWord is the length at which a single shared word is taken as the
// show's name rather than a label both files happen to carry. Real one-word show
// names are at least this long ("Oregairu", "Firefly", "化物语"); the labels are
// shorter.
const minDistinctiveWord = 7

// meaningfulPrefix reports whether a prefix shared by two names says anything
// about which show they are. A group tag and a resolution — "[Group]", "[1080p]"
// — are prefixes of half the files in a library, so a prefix made only of those
// is not evidence; a prefix that runs through the show's name is.
func meaningfulPrefix(prefix string) bool {
	letters := 0
	for _, r := range prefix {
		if unicode.IsLetter(r) {
			letters++
			if letters >= minPrefixLetters {
				return true
			}
		}
	}
	return false
}

// minPrefixLetters is how much of a shared prefix has to be words rather than
// brackets, dots and digits. It sits above the length of a "[Group]" tag and
// below that of any real show name.
const minPrefixLetters = 6

// subStems lists the name forms of a subtitle worth comparing by prefix: the
// whole name, and the name with a trailing language tag removed ("….Jap",
// "….tc", "….SumiSora.tc").
func subStems(name string) []string {
	base := strings.ToLower(stripExt(baseName(name)))
	p := strings.Split(base, ".")
	for len(p) > 0 && junkToken[normalizeToken(p[len(p)-1])] {
		p = p[:len(p)-1]
	}
	trimmed := strings.Join(p, ".")
	if trimmed == base {
		return []string{base}
	}
	return []string{trimmed, base}
}

// nameWords returns the content words of a release name: lowercased, split on
// the punctuation releases use, with group/codec/language/CRC noise removed.
// Words shorter than two characters are dropped, and so are pure numbers —
// the episode number is compared separately, by the parser.
func nameWords(name string) map[string]bool {
	out := map[string]bool{}
	for _, w := range tokenRe.Split(baseName(stripExt(name)), -1) {
		t := normalizeToken(w)
		if t == "" || junkToken[t] || isDigits(t) || isCRC(t) {
			continue
		}
		out[t] = true
	}
	return out
}

// baseName is the last path segment of a driver path or a plain file name, and
// stripExt drops the extension from it — the same reading of a name that the
// episode parser does, so a name is never interpreted two different ways.
func stripExt(name string) string {
	base := baseName(name)
	if i := strings.LastIndex(base, "."); i > 0 {
		return base[:i]
	}
	return base
}

var tokenRe = regexp.MustCompile(`[^\p{L}\p{N}]+`)

func normalizeToken(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isDigits(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return s != ""
}

// junkToken lists the words that say nothing about *which* video a name refers
// to: release groups, codecs, resolutions, sources, eight-hex-digit CRCs, and
// the language/script markers that subtitle names carry.
var junkToken = map[string]bool{
	// release groups seen in the wild
	"vcb": true, "studio": true, "sumisora": true, "kamigami": true, "nekomoe": true,
	"kissaten": true, "beansub": true, "lolihouse": true, "sakurato": true,
	"mingy": true, "moozzi": true, "jsum": true, "uha": true, "ws": true,
	// video
	"x265": true, "x264": true, "h265": true, "h264": true, "hevc": true, "avc": true,
	"10bit": true, "8bit": true, "hi10p": true, "ma10p": true, "flac": true,
	"aac": true, "ac3": true, "eac3": true, "dts": true, "opus": true, "mp3": true,
	"pcm": true, "truehd": true, "atmos": true, "flacx2": true, "aacx2": true,
	// resolution / source / misc tags
	"1080p": true, "1080i": true, "720p": true, "2160p": true, "480p": true,
	"4k": true, "uhd": true, "hdr": true, "hdr10": true, "dovi": true, "dv": true,
	"bdrip": true, "bd": true, "bluray": true, "bdmv": true, "webrip": true,
	"web": true, "webdl": true, "dl": true, "hdtv": true, "dvdrip": true, "remux": true,
	"repack": true, "proper": true, "complete": true, "v2": true, "v3": true,
	// containers and subtitle formats
	"mkv": true, "mp4": true, "m4v": true, "avi": true, "ts": true,
	"ass": true, "ssa": true, "srt": true, "vtt": true, "sup": true, "sub": true,
	// language / script markers
	"chs": true, "cht": true, "sc": true, "tc": true, "big5": true, "gb": true,
	"zh": true, "hans": true, "hant": true, "jap": true, "jp": true, "jpn": true,
	"eng": true, "en": true, "chi": true, "kor": true, "kr": true, "yue": true,
	"chsjpn": true, "chscj": true, "jpnchs": true, "dual": true, "multi": true,
	// extras, which are never the episode they sit beside
	"ova": true, "oad": true, "sp": true, "sps": true, "ncop": true, "nced": true,
	"pv": true, "cd": true, "cds": true, "scan": true, "scans": true,
	"font": true, "fonts": true, "menu": true, "menus": true, "bonus": true,
	"preview": true, "trailer": true, "sample": true, "extra": true, "extras": true,
}

// isCRC reports the eight-hex-digit checksum release names end with, which is
// noise for matching but would otherwise look like a content word.
func isCRC(s string) bool {
	if len(s) != 8 {
		return false
	}
	for _, r := range s {
		if !unicode.Is(unicode.ASCII_Hex_Digit, r) {
			return false
		}
	}
	return true
}
