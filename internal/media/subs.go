package media

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"doriyamedia/internal/models"
)

// DetectSubFormat classifies an external subtitle file.
func DetectSubFormat(name string) (format string, convertible bool) {
	low := strings.ToLower(name)
	switch {
	case strings.HasSuffix(low, ".srt"):
		return "subrip", true
	case strings.HasSuffix(low, ".ass"):
		return "ass", true
	case strings.HasSuffix(low, ".ssa"):
		return "ssa", true
	case strings.HasSuffix(low, ".vtt"):
		return "webvtt", true
	case strings.HasSuffix(low, ".sub"):
		return "sub", false
	default:
		return "", false
	}
}

// langPatterns map a file or track name to a language. The ASCII alternatives
// are word-anchored, but the CJK ones cannot be: Go's \b is an ASCII word
// boundary, so `\b简\b` never matches text such as ".简日." — which is exactly
// how Chinese subtitles are named in practice.
var langPatterns = []struct {
	re   *regexp.Regexp
	lang string
}{
	{regexp.MustCompile(`(?i)(\b(chs|sc|gb|zhhans|hans)\b|简[体中日英]|简中|中字|中文)`), "chi"},
	{regexp.MustCompile(`(?i)(\b(cht|tc|big5|zhhant|hant)\b|繁[体中日英]|繁中)`), "chi"},
	{regexp.MustCompile(`(?i)(\b(eng|english)\b|英[文语])`), "eng"},
	{regexp.MustCompile(`(?i)(\b(jpn|japanese)\b|日[文语])`), "jpn"},
	{regexp.MustCompile(`(?i)(\b(kor|korean)\b|韩[文语])`), "kor"},
	{regexp.MustCompile(`(?i)国语|普通话|mandarin`), "chi"},
	{regexp.MustCompile(`(?i)粤语|粵語|cantonese`), "yue"},
}

func GuessLang(name string) string {
	for _, lp := range langPatterns {
		if lp.re.MatchString(name) {
			return lp.lang
		}
	}
	return ""
}

func SubLabel(name string) string {
	base := name
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if i := strings.LastIndex(base, "."); i >= 0 {
		base = base[:i]
	}
	return strings.TrimSpace(base)
}

// ---------- WebVTT conversion ----------
//
// Everything this server serves as text subtitles passes through here. SubRip is
// plain text with the odd <i>; SSA/ASS carries typesetting — positioning,
// alignment, several styles, per-line colours, vector drawings. WebVTT can
// express much more of that than this converter used to admit: a cue can say
// which part of the picture it belongs to, and inline tags carry bold, italic,
// underline and class-based colour. Discarding all of it is what pinned every
// sign — a title card, a phone screen, a shop front — to the bottom centre,
// stacked on top of the dialogue that belongs there.
//
// What deliberately does not survive: pixel-exact horizontal placement, because a
// WebVTT cue box is placed by width and faking a pixel offset walks a sign off the
// edge instead of onto it; font sizes, which would fight the player's own
// scaling; and everything drawn or animated — \t, \clip, karaoke sweeps, \p
// shapes and their coordinates — which has no WebVTT equivalent at all. Burning
// the subtitle in remains the faithful path for those, and the player offers it
// for ASS tracks.
const (
	assDefaultPlayX = 1920.0
	assDefaultPlayY = 1080.0
)

// assStyle is the part of an ASS style WebVTT can carry, plus the alignment and
// margins that decide where a line sits when the line itself does not say.
type assStyle struct {
	primary   string // "#rrggbb", empty when the style has no usable colour
	bold      bool
	italic    bool
	underline bool
	strikeout bool
	boxed     bool // BorderStyle 3: the author asked for an opaque box
	align     int  // ASS numpad alignment, 1-9
	marginL   int
	marginR   int
	marginV   int
}

// assDoc is the header of an ASS file: the coordinate space its positions are in,
// and the styles its dialogue lines name.
type assDoc struct {
	playX  float64
	playY  float64
	styles map[string]assStyle
}

type assEvent struct {
	start, end string
	style      string
	text       string
	marginL    int
	marginR    int
	marginV    int
}

func newASSDoc() *assDoc {
	return &assDoc{playX: assDefaultPlayX, playY: assDefaultPlayY, styles: map[string]assStyle{}}
}

// style returns a style by name. An undefined name gets the ASS default — bottom
// centre — rather than a zero value that would put the text nowhere.
func (d *assDoc) style(name string) assStyle {
	if st, ok := d.styles[strings.ToLower(strings.TrimSpace(name))]; ok {
		return st
	}
	return assStyle{align: 2}
}

func assFields(v string, n int) []string {
	parts := strings.SplitN(v, ",", n)
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func assAtoi(v string) int {
	n, err := strconv.Atoi(strings.Trim(strings.TrimSpace(v), "()"))
	if err != nil {
		return 0
	}
	return n
}

// assBool reads an ASS boolean, where -1 means true and 0 means false.
func assBool(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	if n, err := strconv.Atoi(v); err == nil {
		return n != 0
	}
	return false
}

// assTagOn reads the argument of a toggle tag: \b. \b1 \b-1 \b700 are on, \b0 is
// off. An argument we cannot read counts as on, which is what a bare tag means.
func assTagOn(arg string) bool {
	arg = strings.Trim(strings.TrimSpace(arg), "()")
	if arg == "" {
		return true
	}
	n, err := strconv.Atoi(arg)
	if err != nil {
		return true
	}
	return n != 0
}

// assColor turns an ASS colour into "#rrggbb". ASS stores it as &HAABBGGRR
// (alpha first, then the bytes reversed), and some old files use decimal. An
// unreadable value becomes empty, which leaves the text its inherited colour
// rather than painting it black by accident.
func assColor(v string) string {
	s := strings.ToLower(strings.TrimSpace(v))
	if s == "" {
		return ""
	}
	s = strings.TrimPrefix(s, "&h")
	s = strings.Trim(s, "&")
	n, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		if n, err = strconv.ParseUint(strings.TrimSpace(v), 10, 32); err != nil {
			return ""
		}
	}
	return fmt.Sprintf("#%02x%02x%02x", n&0xff, (n>>8)&0xff, (n>>16)&0xff)
}

// parseASS reads what a conversion needs: the coordinate space, the styles, and
// every dialogue line with its override tags still in the text.
func parseASS(data []byte) (*assDoc, []assEvent) {
	doc := newASSDoc()
	var events []assEvent
	section := ""
	var styleFields, eventFields []string
	src := strings.TrimPrefix(string(data), "\ufeff")
	for _, line := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, ";") {
			continue
		}
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			section = strings.ToLower(strings.TrimSpace(t[1 : len(t)-1]))
			continue
		}
		key, val := t, ""
		if i := strings.Index(t, ":"); i >= 0 {
			key, val = strings.TrimSpace(t[:i]), strings.TrimSpace(t[i+1:])
		}
		low := strings.ToLower(key)
		switch section {
		case "script info":
			switch low {
			case "playresx":
				if n, err := strconv.ParseFloat(strings.TrimSpace(val), 64); err == nil && n > 0 {
					doc.playX = n
				}
			case "playresy":
				if n, err := strconv.ParseFloat(strings.TrimSpace(val), 64); err == nil && n > 0 {
					doc.playY = n
				}
			}
		case "v4+ styles", "v4++ styles", "v4 styles":
			switch low {
			case "format":
				styleFields = assFields(val, -1)
			case "style":
				if len(styleFields) == 0 {
					continue
				}
				parts := strings.SplitN(val, ",", len(styleFields))
				if len(parts) < len(styleFields) {
					continue
				}
				m := map[string]string{}
				for i, f := range styleFields {
					m[strings.ToLower(f)] = strings.TrimSpace(parts[i])
				}
				name := strings.ToLower(m["name"])
				if name == "" {
					continue
				}
				doc.styles[name] = assStyle{
					primary:   assColor(m["primarycolour"]),
					bold:      assBool(m["bold"]),
					italic:    assBool(m["italic"]),
					underline: assBool(m["underline"]),
					strikeout: assBool(m["strikeout"]),
					boxed:     assAtoi(m["borderstyle"]) == 3,
					align:     assAtoi(m["alignment"]),
					marginL:   assAtoi(m["marginl"]),
					marginR:   assAtoi(m["marginr"]),
					marginV:   assAtoi(m["marginv"]),
				}
			}
		case "events":
			switch low {
			case "format":
				eventFields = assFields(val, -1)
			case "dialogue":
				if len(eventFields) == 0 {
					// No Format line: the documented order, whose last field is
					// the only one that may contain commas.
					eventFields = []string{"Layer", "Start", "End", "Style", "Name",
						"MarginL", "MarginR", "MarginV", "Effect", "Text"}
				}
				parts := strings.SplitN(val, ",", len(eventFields))
				if len(parts) < len(eventFields) {
					continue
				}
				m := map[string]string{}
				for i, f := range eventFields {
					m[strings.ToLower(strings.TrimSpace(f))] = strings.TrimSpace(parts[i])
				}
				ev := assEvent{
					start: m["start"], end: m["end"], style: m["style"], text: m["text"],
					marginL: assAtoi(m["marginl"]), marginR: assAtoi(m["marginr"]),
					marginV: assAtoi(m["marginv"]),
				}
				if ev.start == "" || ev.end == "" || strings.TrimSpace(ev.text) == "" {
					continue
				}
				events = append(events, ev)
			}
		}
	}
	return doc, events
}

// vttStyles collects the STYLE block a document needs. WebVTT keeps colours and
// decorations in classes rather than inline, so the renderer registers a class
// the first time it uses one.
type vttStyles struct {
	decls map[string]string
}

func (s *vttStyles) add(name, decl string) string {
	if s.decls == nil {
		s.decls = map[string]string{}
	}
	s.decls[name] = decl
	return name
}

// color registers a colour class. White is left alone: it is what WebVTT already
// draws, and a class on every ordinary line of dialogue is pure noise.
func (s *vttStyles) color(c string) string {
	if c == "" || c == "#ffffff" {
		return ""
	}
	return s.add("dm-c"+strings.TrimPrefix(c, "#"), "color: "+c)
}

func (s *vttStyles) strike() string {
	return s.add("dm-so", "text-decoration: line-through")
}

// box is for a style that asked for an opaque box (BorderStyle 3). It is the one
// place a background is wanted: the author drew one, so the text is unreadable
// without it.
func (s *vttStyles) box() string {
	return s.add("dm-box", "background-color: rgba(0,0,0,.55)")
}

// cueState is the inline state of the text being rendered. WebVTT nests its tags,
// so the renderer closes and reopens them whenever the state changes.
type cueState struct {
	bold, italic, underline, strikeout bool
	color                              string
	boxed                              bool
}

func (s cueState) tags(st *vttStyles) []string {
	var tags []string
	if s.bold {
		tags = append(tags, "b")
	}
	if s.italic {
		tags = append(tags, "i")
	}
	if s.underline {
		tags = append(tags, "u")
	}
	if s.strikeout {
		tags = append(tags, "c."+st.strike())
	}
	if s.boxed {
		tags = append(tags, "c."+st.box())
	}
	if c := st.color(s.color); c != "" {
		tags = append(tags, "c."+c)
	}
	return tags
}

func tagName(tag string) string {
	if i := strings.IndexByte(tag, '.'); i >= 0 {
		return tag[:i]
	}
	return tag
}

// retag closes the tags that are no longer wanted and opens the wanted ones. It
// reopens everything on any change rather than diffing: legal nesting matters
// more here than a handful of saved bytes.
func retag(out *strings.Builder, open, want []string) []string {
	if len(open) == len(want) {
		same := true
		for i := range open {
			if open[i] != want[i] {
				same = false
				break
			}
		}
		if same {
			return open
		}
	}
	for i := len(open) - 1; i >= 0; i-- {
		out.WriteString("</" + tagName(open[i]) + ">")
	}
	for _, t := range want {
		out.WriteString("<" + t + ">")
	}
	return want
}

// escapeCue escapes the characters WebVTT gives meaning to. Subtitle text is full
// of "&" and "<3", and a raw one either breaks the cue or invents markup.
func escapeCue(s string) string {
	if !strings.ContainsAny(s, "&<>") {
		return s
	}
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// markupTags maps the tags subtitle files put in their own text onto the WebVTT
// ones, so an SRT's <i> stays italic instead of being escaped into view.
var markupTags = map[string]string{"i": "i", "b": "b", "u": "u", "em": "i", "strong": "b"}

// renderMarkup rewrites the few tags that appear inside subtitle text and escapes
// everything else. Unrecognised markup is shown rather than dropped: silently
// swallowing it is how half a line goes missing.
func renderMarkup(run string, styles *vttStyles) string {
	var b strings.Builder
	for i := 0; i < len(run); {
		lt := strings.IndexByte(run[i:], '<')
		if lt < 0 {
			b.WriteString(escapeCue(run[i:]))
			break
		}
		b.WriteString(escapeCue(run[i : i+lt]))
		rest := run[i+lt:]
		gt := strings.IndexByte(rest, '>')
		if gt < 0 {
			b.WriteString(escapeCue(rest))
			break
		}
		if rep, ok := convertMarkupTag(rest[:gt+1], styles); ok {
			b.WriteString(rep)
		} else {
			b.WriteString(escapeCue(rest[:gt+1]))
		}
		i += lt + gt + 1
	}
	return b.String()
}

func convertMarkupTag(tag string, styles *vttStyles) (string, bool) {
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(tag, "<"), ">"))
	if inner == "" {
		return "", false
	}
	name := inner
	if sp := strings.IndexAny(name, " \t"); sp >= 0 {
		name = name[:sp]
	}
	closing := strings.HasPrefix(name, "/")
	name = strings.ToLower(strings.TrimPrefix(name, "/"))
	if closing {
		if v, ok := markupTags[name]; ok {
			return "</" + v + ">", true
		}
		if name == "font" {
			return "</c>", true
		}
		return "", false
	}
	if v, ok := markupTags[name]; ok {
		return "<" + v + ">", true
	}
	if name == "font" {
		if c := colorFromFontTag(inner); c != "" {
			if cls := styles.color(c); cls != "" {
				return "<c." + cls + ">", true
			}
		}
		return "", true // a font tag we cannot use: drop the tag, keep the text
	}
	return "", false
}

func colorFromFontTag(inner string) string {
	low := strings.ToLower(inner)
	i := strings.Index(low, "color")
	if i < 0 {
		return ""
	}
	eq := strings.Index(low[i:], "=")
	if eq < 0 {
		return ""
	}
	return normalizeColor(strings.Trim(strings.TrimSpace(inner[i+eq+1:]), "\"'"))
}

// normalizeColor accepts #rgb, #rrggbb and the colour names subtitle files use
// most; anything else is left alone rather than guessed at.
func normalizeColor(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if strings.HasPrefix(v, "#") {
		h := strings.TrimPrefix(v, "#")
		switch len(h) {
		case 3:
			return "#" + string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
		case 6:
			if _, err := strconv.ParseUint(h, 16, 32); err == nil {
				return "#" + h
			}
		}
		return ""
	}
	switch v {
	case "white":
		return "#ffffff"
	case "yellow":
		return "#ffff00"
	case "red":
		return "#ff0000"
	case "cyan", "aqua":
		return "#00ffff"
	case "lime":
		return "#00ff00"
	case "blue":
		return "#0000ff"
	case "black":
		return "#000000"
	case "gray", "grey":
		return "#808080"
	}
	return ""
}

// cuePos is what a cue says about where it belongs on the picture. Empty fields
// mean "leave it to the player", which is what a plain bottom-centred line wants
// — exactly the output the old conversion produced for every line.
type cuePos struct {
	linePct   float64
	lineSet   bool
	lineAlign string // start | center | end
	textAlign string // start | center | end
}

func (p cuePos) settings() string {
	var parts []string
	if p.lineSet {
		s := "line:" + pct(p.linePct)
		if p.lineAlign != "" {
			s += "," + p.lineAlign
		}
		parts = append(parts, s)
	}
	if p.textAlign != "" && p.textAlign != "center" {
		parts = append(parts, "align:"+p.textAlign)
	}
	return strings.Join(parts, ",")
}

// pct renders a percentage with one decimal and no pointless ".0".
func pct(v float64) string {
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	s := strconv.FormatFloat(math.Round(v*10)/10, 'f', 1, 64)
	return strings.TrimSuffix(s, ".0") + "%"
}

// assAnchor splits an ASS numpad alignment into its horizontal and vertical
// thirds: 1-3 bottom, 4-6 middle, 7-9 top, each row left/centre/right.
func assAnchor(an int) (string, string) {
	if an < 1 || an > 9 {
		an = 2
	}
	h := [3]string{"start", "center", "end"}[(an-1)%3]
	v := "bottom"
	switch {
	case an >= 7:
		v = "top"
	case an >= 4:
		v = "middle"
	}
	return h, v
}

// lineAlignOf picks the line alignment that keeps the cue box's nearest edge on
// the position: a sign near the top grows downwards, one near the bottom grows
// upwards.
func lineAlignOf(p float64) string {
	switch {
	case p < 33:
		return "start"
	case p > 66:
		return "end"
	default:
		return "center"
	}
}

// thirdOf reduces a horizontal position to the third of the frame it falls in.
func thirdOf(p float64) string {
	switch {
	case p < 33:
		return "start"
	case p > 66:
		return "end"
	default:
		return "center"
	}
}

// cuePlacement decides where a cue sits. Vertical placement comes from \pos or
// \move when the line has one, and otherwise from the style's own alignment and
// top margin — that is what puts a title card at the top of the frame instead of
// on the dialogue. Horizontal placement only distinguishes left, centre and
// right, for the reason given at the top of this file.
func cuePlacement(doc *assDoc, base assStyle, an int, hasPos bool, px, py float64, mv int) cuePos {
	h, v := assAnchor(an)
	if hasPos {
		y := py / doc.playY * 100
		return cuePos{lineSet: true, lineAlign: lineAlignOf(y), linePct: y, textAlign: thirdOf(px / doc.playX * 100)}
	}
	p := cuePos{textAlign: h}
	switch v {
	case "top":
		if mv <= 0 {
			mv = base.marginV
		}
		p.lineSet, p.lineAlign = true, "start"
		p.linePct = float64(mv) / doc.playY * 100
		if p.linePct < 1 {
			// No usable margin: sit just inside the top edge, where ASS puts a
			// top-aligned line.
			p.linePct = 4
		}
	case "middle":
		p.lineSet, p.lineAlign = true, "center"
		p.linePct = 50
	}
	return p
}

// assRun is the state a single dialogue line passes through as its override tags
// are read.
type assRun struct {
	st      cueState
	an      int
	hasPos  bool
	px, py  float64
	mv      int
	drawing bool
}

func (r *assRun) apply(block string, base assStyle, doc *assDoc) {
	for _, tag := range strings.Split(block, "\\") {
		name, arg := assTagName(tag)
		switch name {
		case "":
		case "r":
			st, an := base, base.align
			if arg = strings.Trim(strings.TrimSpace(arg), "()"); arg != "" {
				if named := doc.style(arg); named.align != 0 || named.primary != "" || named.bold {
					st, an = named, named.align
				}
			}
			r.st = cueState{bold: st.bold, italic: st.italic, underline: st.underline,
				strikeout: st.strikeout, color: st.primary, boxed: st.boxed}
			r.an = an
		case "b":
			r.st.bold = assTagOn(arg)
		case "i":
			r.st.italic = assTagOn(arg)
		case "u":
			r.st.underline = assTagOn(arg)
		case "s":
			r.st.strikeout = assTagOn(arg)
		case "c", "1c":
			if c := assColor(arg); c != "" {
				r.st.color = c
			}
		case "an":
			if n := assAtoi(arg); n >= 1 && n <= 9 {
				r.an = n
			}
		case "pos", "move":
			if x, y, ok := assXY(arg); ok {
				r.px, r.py, r.hasPos = x, y, true
			}
		case "p":
			r.drawing = assAtoi(arg) > 0
		}
		// Everything else — \fs \fscx \fn \k \t \clip \fad \bord \shad \blur
		// \alpha \2c \3c \4c \fe \org \q and friends — has no WebVTT equivalent.
		// The text stays; the decoration does not.
	}
}

// assTagName splits an override tag into its name and argument. \rStyle and
// \fnArial take a free-form string, so they are handled before the general scan,
// which would otherwise swallow the name into the tag.
func assTagName(tag string) (string, string) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return "", ""
	}
	switch tag[0] {
	case 'r', 'R':
		return "r", tag[1:]
	case 'f', 'F':
		if len(tag) >= 2 && (tag[1] == 'n' || tag[1] == 'N') {
			return "fn", tag[2:]
		}
	}
	i := 0
	if tag[0] >= '0' && tag[0] <= '9' {
		// \1c, \2c, \3c, \4c, \1a and their relatives start with the channel.
		for i < len(tag) && (isAssLetter(tag[i]) || (tag[i] >= '0' && tag[i] <= '9')) {
			i++
		}
	} else {
		for i < len(tag) && isAssLetter(tag[i]) {
			i++
		}
	}
	return strings.ToLower(tag[:i]), tag[i:]
}

func isAssLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

var assNumber = regexp.MustCompile(`-?\d+(?:\.\d+)?`)

// assXY reads the first pair of numbers in a tag argument, which is the position
// for \pos and the starting point of a \move.
func assXY(arg string) (float64, float64, bool) {
	m := assNumber.FindAllString(arg, 2)
	if len(m) < 2 {
		return 0, 0, false
	}
	x, err1 := strconv.ParseFloat(m[0], 64)
	y, err2 := strconv.ParseFloat(m[1], 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return x, y, true
}

// renderASSText turns one dialogue line into a cue payload and the placement its
// override tags imply.
func renderASSText(text string, base assStyle, doc *assDoc, styles *vttStyles) (string, cuePos) {
	var (
		out  strings.Builder
		open []string
		run  = assRun{an: base.align,
			st: cueState{bold: base.bold, italic: base.italic, underline: base.underline,
				strikeout: base.strikeout, color: base.primary, boxed: base.boxed}}
	)
	for i := 0; i < len(text); {
		if text[i] == '{' {
			if j := strings.IndexByte(text[i:], '}'); j >= 0 {
				run.apply(text[i+1:i+j], base, doc)
				i += j + 1
				continue
			}
		}
		next := strings.IndexByte(text[i:], '{')
		if next < 0 {
			next = len(text) - i
		}
		chunk := text[i : i+next]
		i += next
		if run.drawing {
			// A \p1 block is a vector shape: its "text" is coordinate pairs, and
			// showing them is worse than showing nothing.
			continue
		}
		if chunk == "" {
			// Nothing to show between two override blocks: emitting the tags here
			// would leave stray <b></b> pairs and register classes nobody uses.
			continue
		}
		chunk = strings.ReplaceAll(chunk, `\N`, "\n")
		chunk = strings.ReplaceAll(chunk, `\n`, "\n")
		chunk = strings.ReplaceAll(chunk, `\h`, "\u00a0")
		open = retag(&out, open, run.st.tags(styles))
		out.WriteString(renderMarkup(chunk, styles))
	}
	retag(&out, open, nil)
	return out.String(), cuePlacement(doc, base, run.an, run.hasPos, run.px, run.py, run.mv)
}

// hasText reports whether a rendered cue would show anything at all: a line that
// was only a drawing, or only tag changes, is not worth a cue.
func hasText(rendered string) bool {
	tag := false
	for _, r := range rendered {
		switch {
		case r == '<':
			tag = true
		case r == '>':
			tag = false
		case !tag && !unicode.IsSpace(r):
			return true
		}
	}
	return false
}

// vttCue is one rendered cue waiting to be written: its timing line, its
// payload, and where it sits. The times are kept as numbers because the bottom
// placement depends on which cues share a moment.
type vttCue struct {
	head string
	t0   float64
	t1   float64
	body string
	pos  cuePos
}

// vttBottomPct is where the bottom-most cue sits: a little above the bottom edge
// rather than against it. The browser's own bottom placement puts text on the last
// row of the picture, which in the theatre is underneath the control bar.
//
// Note that Chrome ignores the line-alignment component of a cue setting, so
// "line:88%,end" is placed as "the top of the box at 88%" there. The value is
// chosen for that reading.
const vttBottomPct = 88.0

// vttLinePct is one line of cue text as a share of the picture's height. It is
// measured, not guessed: three stacked cues in an 800px-tall picture sit 58px
// apart (7.25%), and the same three in a 400px-tall picture sit 29px apart — so
// the advance follows the picture, not the window, which is what makes a
// percentage usable in every layout.
const vttLinePct = 7.25

// vttOverlapTol is how much two cue *boxes* may overlap without their text
// touching. Also measured: the glyphs start about 1.6% below the box's top edge
// and are 4.5% tall, so a box overlap under that figure cannot reach the ink — and
// without the slack, a sign the file pinned at, say, 81% would shove the dialogue
// two lines up the frame for the sake of eight pixels of empty box.
const vttOverlapTol = 1.5

// stackBottomCues gives every cue that has no placement of its own a line, and
// stacks them the way the browser would have.
//
// Neither obvious answer works. Leaving the placement to the player puts the
// bottom-most line on the last row of the picture, under the theatre's control
// bar; pinning every cue to one line is worse, because a cue with a line of its
// own is placed there and nothing moves it out of the way, so simultaneous lines
// render on top of each other (three cues at one line: one visible). What the
// browser does for free is avoid collisions, and that is what this does — in time
// order, each cue goes one slot above everything it overlaps.
func stackBottomCues(cues []vttCue) {
	lines := make([]int, len(cues))
	for i := range cues {
		lines[i] = cueLines(cues[i].body)
	}
	order := make([]int, 0, len(cues))
	for i := range cues {
		order = append(order, i)
	}
	sort.SliceStable(order, func(a, b int) bool { return cues[order[a]].t0 < cues[order[b]].t0 })

	rank := make([]int, len(cues))
	for at, i := range order {
		rank[i] = at
	}
	// top is the top edge of a cue sitting in slot k, which is where Chrome puts a
	// cue given an explicit line (it ignores the alignment component).
	top := func(k int) float64 { return vttBottomPct - float64(k)*vttLinePct }

	slot := make([]int, len(cues))
	for _, i := range order {
		if cues[i].pos.lineSet {
			continue // placed by the file: not ours to move
		}
		// The lowest slot whose span is clear of everything already there. Walking
		// up rather than adding up the blockers' heights, because a cue the file
		// pinned low in the frame sits between slots and has to push the stack clear
		// of itself, not by a whole number of lines.
		k := 0
		for ; k < 12; k++ {
			lo := top(k)
			hi := lo + float64(lines[i])*vttLinePct
			if hi <= 0 {
				break // the stack has run off the top of the picture
			}
			clash := false
			for _, j := range order {
				if j == i || !cueOverlaps(cues[i], cues[j]) {
					continue
				}
				var oLo float64
				if cues[j].pos.lineSet {
					oLo = cues[j].pos.linePct
				} else {
					if rank[j] > rank[i] {
						continue // later in time: it stacks above this one, not below
					}
					oLo = top(slot[j])
				}
				oHi := oLo + float64(lines[j])*vttLinePct
				if oLo < hi-vttOverlapTol && lo < oHi-vttOverlapTol {
					clash = true
					break
				}
			}
			if !clash {
				break
			}
		}
		slot[i] = k
	}
	for i := range cues {
		if cues[i].pos.lineSet {
			continue
		}
		pct := vttBottomPct - float64(slot[i])*vttLinePct
		if pct < 0 {
			pct = 0
		}
		cues[i].pos.lineSet, cues[i].pos.lineAlign, cues[i].pos.linePct = true, "end", pct
	}
}

// cueLines counts the display lines a cue occupies: a wrapped cue takes two slots
// in the stack, which is what a line assigned here has to allow for.
func cueLines(body string) int {
	n := 1
	for _, r := range body {
		if r == '\n' {
			n++
		}
	}
	return n
}

// cueOverlaps reports whether two cues are on the picture at the same moment.
func cueOverlaps(a, b vttCue) bool {
	return b.t0 < a.t1 && a.t0 < b.t1
}

// cueSeconds reads a SubRip or WebVTT timestamp, either separator.
func cueSeconds(s string) (float64, bool) {
	m := assClock.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, false
	}
	h, _ := strconv.Atoi(m[1])
	mi, _ := strconv.Atoi(m[2])
	sec, _ := strconv.Atoi(m[3])
	ms := m[4]
	for len(ms) < 3 {
		ms += "0"
	}
	milli, _ := strconv.Atoi(ms[:3])
	return float64(h*3600+mi*60+sec) + float64(milli)/1000, true
}

var assClock = regexp.MustCompile(`^(\d+):(\d{1,2}):(\d{1,2})[.,](\d{1,3})$`)

// assembleVTT puts the document together with its STYLE block, which WebVTT only
// allows before the first cue.
func assembleVTT(styles *vttStyles, cues []vttCue) []byte {
	var b strings.Builder
	b.WriteString("WEBVTT\n\n")
	if len(styles.decls) > 0 {
		names := make([]string, 0, len(styles.decls))
		for n := range styles.decls {
			names = append(names, n)
		}
		sort.Strings(names)
		b.WriteString("STYLE\n")
		for _, n := range names {
			fmt.Fprintf(&b, "::cue(.%s) { %s }\n", n, styles.decls[n])
		}
		b.WriteString("\n")
	}
	for _, c := range cues {
		head := c.head
		if s := c.pos.settings(); s != "" {
			head += " " + s
		}
		fmt.Fprintf(&b, "%s\n%s\n\n", head, c.body)
	}
	return []byte(b.String())
}

// splitBlocks cuts a subtitle document into blank-line separated blocks.
func splitBlocks(src string) []string {
	var out []string
	var cur []string
	for _, l := range strings.Split(src, "\n") {
		if strings.TrimSpace(l) == "" {
			if len(cur) > 0 {
				out = append(out, strings.Join(cur, "\n"))
				cur = nil
			}
			continue
		}
		cur = append(cur, strings.TrimRight(l, "\r"))
	}
	if len(cur) > 0 {
		out = append(out, strings.Join(cur, "\n"))
	}
	return out
}

var assTime = regexp.MustCompile(`(\d+):(\d{1,2}):(\d{1,2})\.(\d{1,3})`)

func assTimestamp(t string) string {
	m := assTime.FindStringSubmatch(t)
	if m == nil {
		return "00:00:00.000"
	}
	h, mi, se, ms := m[1], m[2], m[3], m[4]
	for len(mi) < 2 {
		mi = "0" + mi
	}
	for len(se) < 2 {
		se = "0" + se
	}
	for len(ms) < 3 {
		ms += "0"
	}
	return fmt.Sprintf("%02s:%s:%s.%s", h, mi, se, ms)
}

// SRTToVTT converts SubRip bytes to WebVTT. Cue text goes through the same
// renderer as an ASS line, because real SubRip files carry ASS-style overrides
// ({\an8} for a sign) and HTML (<i>, <font color=…>) — those are how a subtitle
// says "this line belongs at the top".
func SRTToVTT(data []byte) []byte {
	src := strings.ReplaceAll(strings.TrimPrefix(string(data), "\ufeff"), "\r\n", "\n")
	doc := newASSDoc()
	styles := &vttStyles{}
	base := assStyle{align: 2}
	var cues []vttCue
	for _, block := range splitBlocks(src) {
		lines := strings.Split(block, "\n")
		at := -1
		for i, l := range lines {
			if strings.Contains(l, "-->") {
				at = i
				break
			}
		}
		if at < 0 {
			continue // the header, or a NOTE
		}
		head := strings.ReplaceAll(lines[at], ",", ".")
		text, pos := renderASSText(strings.Join(lines[at+1:], "\n"), base, doc, styles)
		text = strings.TrimRight(text, "\n")
		if !hasText(text) {
			continue
		}
		c := vttCue{head: head, body: text, pos: pos}
		if parts := strings.SplitN(head, "-->", 2); len(parts) == 2 {
			c.t0, _ = cueSeconds(parts[0])
			c.t1, _ = cueSeconds(parts[1])
		}
		cues = append(cues, c)
	}
	stackBottomCues(cues)
	return assembleVTT(styles, cues)
}

// ASSToVTT converts SSA/ASS bytes to WebVTT, carrying across what WebVTT can
// express: when a line appears and where on the picture it belongs (from \an,
// \pos and the style's own alignment and margins), plus bold, italic, underline
// and colour. A style the file never defines, and a line with no positioning at
// all, come out exactly as a plain conversion would.
func ASSToVTT(data []byte) []byte {
	doc, events := parseASS(data)
	styles := &vttStyles{}
	cues := make([]vttCue, 0, len(events))
	for _, ev := range events {
		base := doc.style(ev.style)
		if ev.marginL > 0 {
			base.marginL = ev.marginL
		}
		if ev.marginR > 0 {
			base.marginR = ev.marginR
		}
		if ev.marginV > 0 {
			base.marginV = ev.marginV
		}
		text, pos := renderASSText(ev.text, base, doc, styles)
		text = strings.TrimRight(text, "\n")
		if !hasText(text) {
			continue
		}
		begin, end := assTimestamp(ev.start), assTimestamp(ev.end)
		c := vttCue{head: begin + " --> " + end, body: text, pos: pos}
		c.t0, _ = cueSeconds(begin)
		c.t1, _ = cueSeconds(end)
		cues = append(cues, c)
	}
	stackBottomCues(cues)
	return assembleVTT(styles, cues)
}

// ToVTT converts known subtitle bytes to WebVTT by format name.
func ToVTT(format string, data []byte) []byte {
	switch format {
	case "subrip", "srt":
		return SRTToVTT(data)
	case "ass", "ssa":
		return ASSToVTT(data)
	case "webvtt", "vtt":
		return data
	default:
		return SRTToVTT(data)
	}
}

// ---------- automatic subtitle selection ----------

var (
	reSimplified  = regexp.MustCompile(`(?i)(简|chs|hans|\bsc\b|\bgb\b|zh[-_]cn)`)
	reTraditional = regexp.MustCompile(`(?i)(繁|cht|hant|\btc\b|big5|zh[-_]tw)`)
)

// subtitleRank orders subtitle tracks for automatic selection; the
// lexicographically smallest rank wins. Order of preference:
//
//  1. Chinese, then Cantonese, then an identified other language, then an
//     untagged track. Chinese first because this library (and its viewers) are
//     Chinese; an untagged track last because we cannot tell what it is.
//  2. Simplified before Traditional — the two share an ISO code, and a viewer
//     who reads one usually cannot comfortably read the other.
//  3. Tracks whose language is known before untagged ones.
//  4. Registration order, which follows the file's own track order.
func subtitleRank(s models.Subtitle) [4]int {
	lang := strings.ToLower(strings.TrimSpace(s.Lang))
	// "und" is the container's way of saying "no idea", so treat it as untagged.
	if lang == "und" {
		lang = ""
	}
	// When the container has no usable tag, the title usually still says it
	// ("简日", "繁體", "English").
	if lang == "" {
		lang = GuessLang(s.Title)
	}
	// Both the tag and the human title carry hints ("简日", "SC", "繁體").
	text := strings.ToLower(s.Title + " " + s.Path)

	langRank := 3
	switch {
	case lang == "chi" || lang == "zho" || lang == "zh" || strings.HasPrefix(lang, "zh"):
		langRank = 0
	case lang == "yue":
		langRank = 1
	case lang != "":
		langRank = 2
	}

	scriptRank := 1
	switch {
	case reSimplified.MatchString(text):
		scriptRank = 0
	case reTraditional.MatchString(text):
		scriptRank = 2
	}

	untagged := 0
	if lang == "" {
		untagged = 1
	}
	return [4]int{langRank, scriptRank, untagged, int(s.ID)}
}

func lessRank(a, b [4]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// PickDefaultSubtitle returns the id of the subtitle to enable automatically
// when a file starts playing, or 0 when there is nothing to pick. Callers must
// pass only subtitles they can actually serve (see Subtitle.Convertible).
func PickDefaultSubtitle(subs []models.Subtitle) int64 {
	var best int64
	var bestRank [4]int
	for _, s := range subs {
		r := subtitleRank(s)
		if best == 0 || lessRank(r, bestRank) {
			best, bestRank = s.ID, r
		}
	}
	return best
}

// ---------- timing shifts ----------

func parseVTTTime(s string) (float64, bool) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 3 {
		return 0, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	sec, err3 := strconv.ParseFloat(parts[2], 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, false
	}
	return float64(h)*3600 + float64(m)*60 + sec, true
}

// formatVTTTime renders seconds as a WebVTT timestamp, rounded to milliseconds
// so a shifted cue can never come out as "…:60.000".
func formatVTTTime(t float64) string {
	ms := int64(math.Round(t * 1000))
	if ms < 0 {
		ms = 0
	}
	h := ms / 3600000
	m := (ms % 3600000) / 60000
	s := (ms % 60000) / 1000
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms%1000)
}

// ShiftVTT moves a WebVTT document's cues back by offset seconds, dropping cues
// that end before the new zero point and clamping ones that straddle it.
//
// HLS subtitle renditions are timed against the stream the player is on, and a
// packaging session's stream starts wherever the session started, while a sidecar
// file's cues are in file time. Blocks without a cue timing line (the WEBVTT
// header, STYLE, NOTE) are passed through untouched, and so is a whole cue's
// payload when it survives.
func ShiftVTT(data []byte, offset float64) []byte {
	if offset <= 0 {
		return data
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	blocks := strings.Split(text, "\n\n")
	out := make([]string, 0, len(blocks))
	for _, block := range blocks {
		lines := strings.Split(block, "\n")
		idx := -1
		for i, line := range lines {
			if strings.Contains(line, "-->") {
				idx = i
				break
			}
		}
		if idx < 0 {
			out = append(out, block)
			continue
		}
		fields := strings.Fields(lines[idx])
		arrow := -1
		for i, f := range fields {
			if f == "-->" {
				arrow = i
				break
			}
		}
		if arrow < 1 || arrow+1 >= len(fields) {
			out = append(out, block)
			continue
		}
		start, ok1 := parseVTTTime(fields[arrow-1])
		end, ok2 := parseVTTTime(fields[arrow+1])
		if !ok1 || !ok2 {
			out = append(out, block)
			continue
		}
		if end <= offset {
			continue // wholly before the stream starts
		}
		fields[arrow-1] = formatVTTTime(start - offset)
		fields[arrow+1] = formatVTTTime(end - offset)
		lines[idx] = strings.Join(fields, " ")
		out = append(out, strings.Join(lines, "\n"))
	}
	return []byte(strings.Join(out, "\n\n"))
}
