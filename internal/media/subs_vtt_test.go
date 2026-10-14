package media

import (
	"strings"
	"testing"
)

// The ASS converter carries typesetting into WebVTT. These tests pin the parts a
// viewer actually notices: a sign landing where the author put it, italics and
// colours surviving, and no coordinate soup from a vector drawing.
func TestASSToVTTPlacement(t *testing.T) {
	head := `[Script Info]
ScriptType: v4.00+
PlayResX: 1920
PlayResY: 1080

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Dialogue,Arial,48,&H00FFFFFF,0,0,0,0,100,100,0,0,1,2,1,2,60,60,30,1
Style: Sign,Arial,72,&H0000FFFF,-1,0,0,0,100,100,0,0,1,2,1,8,10,10,20,1
Style: Boxed,Arial,48,&H00FFFFFF,0,0,0,0,100,100,0,0,3,2,1,2,60,60,30,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
`
	cases := []struct {
		name       string
		text       string
		wantCue    string // settings expected on the cue line
		wantIn     []string
		wantNotIn  []string
		dropIfOnly bool
	}{
		{
			// A line that shares its moment with nothing else is lifted clear of
			// the bottom edge, where the theatre's control bar sits.
			name:    "a lone line is lifted off the bottom edge",
			text:    `Dialogue: 0,0:00:01.00,0:00:03.00,Dialogue,,0,0,0,,你好,世界`,
			wantCue: "line:88%,end",
			wantIn:  []string{"00:00:01.000 --> 00:00:03.000 line:88%,end\n你好,世界"},
		},
		{
			// The style's own MarginV (20px of 1080) is where ASS would put it.
			name:    "a top sign goes to the top of the frame",
			text:    `Dialogue: 0,0:00:01.00,0:00:03.00,Sign,,0,0,0,,{\an8}Phone`,
			wantCue: "line:1.9%,start",
			wantIn:  []string{"Phone"},
		},
		{
			name:    "\\pos wins over the style's anchor",
			text:    `Dialogue: 0,0:00:01.00,0:00:03.00,Dialogue,,0,0,0,,{\pos(960,1000)}Bottom sign`,
			wantCue: "line:92.6%,end",
		},
		{
			// A left-aligned line has to say so: the WebVTT default is centred.
			name:    "left alignment is carried over",
			text:    `Dialogue: 0,0:00:01.00,0:00:03.00,Dialogue,,0,0,0,,{\an1}lower left`,
			wantCue: "align:start",
		},
		{
			name: "italics, bold and colour survive",
			text: `Dialogue: 0,0:00:01.00,0:00:03.00,Dialogue,,0,0,0,,{\i1}italics{\i0} and {\b1}bold{\b0} and {\c&H0000FF&}red`,
			// A lone line, so it is lifted; the point here is the markup.
			wantCue: "line:88%,end",
			wantIn:  []string{"<i>italics</i>", "<b>bold</b>", "<c.dm-cff0000>red</c>", "::cue(.dm-cff0000) { color: #ff0000 }"},
		},
		{
			// \p1 starts a vector drawing: its "text" is coordinate pairs, and
			// rendering them is worse than rendering nothing.
			name:    "a vector drawing is dropped, not shown",
			text:    `Dialogue: 0,0:00:01.00,0:00:03.00,Dialogue,,0,0,0,,{\p1}m 0 0 l 100 0 100 100 0 100{\p0}visible`,
			wantCue: "line:88%,end",
			wantIn:  []string{"visible"},
			wantNotIn: []string{
				"m 0 0", "l 100 0", "{\\p1}",
			},
		},
		{
			name:      "a cue that was only a drawing disappears",
			text:      `Dialogue: 0,0:00:01.00,0:00:03.00,Dialogue,,0,0,0,,{\p1}m 0 0 l 100 0 100 100{\p0}`,
			wantNotIn: []string{"-->", "m 0 0"},
		},
		{
			name:    "text that looks like markup is escaped",
			text:    `Dialogue: 0,0:00:01.00,0:00:03.00,Dialogue,,0,0,0,,Fish & chips <3`,
			wantCue: "line:88%,end",
			wantIn:  []string{"Fish &amp; chips &lt;3"},
		},
		{
			name:    "hard breaks become real lines, karaoke keeps its syllables",
			text:    `Dialogue: 0,0:00:01.00,0:00:03.00,Dialogue,,0,0,0,,{\k20}き{\k20}ら{\k20}き\Nsecond line`,
			wantCue: "line:88%,end",
			wantIn:  []string{"きらき\nsecond line"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(ASSToVTT([]byte(head + tc.text + "\n")))
			if tc.wantCue != "" && !strings.Contains(got, tc.wantCue) {
				t.Errorf("cue settings %q missing.\n got: %s", tc.wantCue, got)
			}
			if tc.wantCue == "" {
				// These cases are about text only: the timing line must carry no
				// placement at all.
				for _, line := range strings.Split(got, "\n") {
					if strings.Contains(line, "-->") && (strings.Contains(line, "line:") || strings.Contains(line, "align:")) {
						t.Errorf("expected no cue settings, got %q", line)
					}
				}
			}
			for _, want := range tc.wantIn {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q.\n got: %s", want, got)
				}
			}
			for _, bad := range tc.wantNotIn {
				if strings.Contains(got, bad) {
					t.Errorf("unexpected %q.\n got: %s", bad, got)
				}
			}
		})
	}
}

// Simultaneous lines are stacked here, one slot per display line, the way the
// browser would have: the alternative — leaving them to the browser — puts the
// bottom-most line on the last row of the picture, under the theatre's controls.
func TestASSToVTTStacksOverlappingLines(t *testing.T) {
	src := `[Script Info]
PlayResX: 1920
PlayResY: 1080

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Dialogue,Arial,48,&H00FFFFFF,0,0,0,0,100,100,0,0,1,2,1,2,60,60,30,1
Style: Sign,Arial,72,&H0000FFFF,0,0,0,0,100,100,0,0,1,2,1,8,10,10,20,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:01.00,0:00:05.00,Dialogue,,0,0,0,,第一句
Dialogue: 0,0:00:02.00,0:00:06.00,Dialogue,,0,0,0,,第二句
Dialogue: 0,0:00:01.00,0:00:05.00,Sign,,0,0,0,,{\an8}牌子
Dialogue: 0,0:00:20.00,0:00:22.00,Dialogue,,0,0,0,,很久以后的一句
`
	got := string(ASSToVTT([]byte(src)))
	lines := strings.Split(got, "\n")
	var settings []string
	for _, line := range lines {
		if strings.Contains(line, "-->") {
			settings = append(settings, strings.TrimSpace(strings.SplitN(line, "-->", 2)[1]))
		}
	}
	if len(settings) != 4 {
		t.Fatalf("expected four cues, got %d.\n got: %s", len(settings), got)
	}
	// The first speaker takes the bottom slot, the second goes one line above it,
	// the sign keeps the anchor the file gave it, and the line twenty seconds
	// later — alone in its moment — takes the bottom slot again.
	// A slot is 7.25% and the value is rendered to one decimal, so the second
	// slot reads 80.8 rather than 80.75 — half a pixel at 1080p.
	want := []string{"line:88%,end", "line:80.8%,end", "line:1.9%,start", "line:88%,end"}
	for i, w := range want {
		if !strings.Contains(settings[i], w) {
			t.Errorf("cue %d should carry %q, got %q", i, w, settings[i])
		}
	}
}

// A cue that wraps to two lines occupies two slots, so the cue overlapping it has
// to start two lines higher — the same accounting the browser does.
// A cue's box grows downwards from the line it is given, so what a wrapped cue
// costs depends on which side of it the other cue is on: above it, the other cue
// only has to clear its top edge, while below it, the wrapped cue has to fit its
// whole height. Both directions are pinned here.
func TestASSToVTTRoomsAWrappedLineBothWays(t *testing.T) {
	head := `[Script Info]
PlayResX: 1920
PlayResY: 1080

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Dialogue,Arial,48,&H00FFFFFF,0,0,0,0,100,100,0,0,1,2,1,2,60,60,30,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
`
	settings := func(src string) []string {
		got := string(ASSToVTT([]byte(head + src)))
		var out []string
		for _, line := range strings.Split(got, "\n") {
			if strings.Contains(line, "-->") {
				out = append(out, strings.TrimSpace(strings.SplitN(line, "-->", 2)[1]))
			}
		}
		return out
	}

	// The wrapped cue is underneath: the cue added later goes one line above its
	// top edge and no further, because the wrapped cue's second line is below it.
	under := settings("Dialogue: 0,0:00:01.00,0:00:05.00,Dialogue,,0,0,0,,上面一行\\N下面一行\n" +
		"Dialogue: 0,0:00:02.00,0:00:06.00,Dialogue,,0,0,0,,另一句\n")
	if len(under) != 2 {
		t.Fatalf("expected two cues, got %d", len(under))
	}
	if !strings.Contains(under[0], "line:88%,end") || !strings.Contains(under[1], "line:80.8%,end") {
		t.Errorf("a cue above a wrapped one needs one line of room, got %q then %q", under[0], under[1])
	}

	// The wrapped cue is on top: it has to fit both of its lines above the other
	// cue, which costs two.
	over := settings("Dialogue: 0,0:00:01.00,0:00:05.00,Dialogue,,0,0,0,,另一句\n" +
		"Dialogue: 0,0:00:02.00,0:00:06.00,Dialogue,,0,0,0,,上面一行\\N下面一行\n")
	if len(over) != 2 {
		t.Fatalf("expected two cues, got %d", len(over))
	}
	if !strings.Contains(over[0], "line:88%,end") || !strings.Contains(over[1], "line:73.5%,end") {
		t.Errorf("a wrapped cue needs both of its lines above the one below, got %q then %q", over[0], over[1])
	}
}

// An ASS style can ask for an opaque box (BorderStyle 3). That is the one place a
// background is wanted — the author drew one.
func TestASSToVTTKeepsABoxedStyle(t *testing.T) {
	src := `[Script Info]
ScriptType: v4.00+
PlayResX: 1920
PlayResY: 1080

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Boxed,Arial,48,&H00FFFFFF,0,0,0,0,100,100,0,0,3,2,1,2,60,60,30,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:01.00,0:00:03.00,Boxed,,0,0,0,,boxed line
`
	got := string(ASSToVTT([]byte(src)))
	if !strings.Contains(got, "::cue(.dm-box) { background-color:") {
		t.Errorf("a BorderStyle 3 style should keep its box.\n got: %s", got)
	}
}

// SubRip in the wild carries ASS-style overrides and HTML, and both are how an
// .srt says "this line is a sign at the top".
func TestSRTToVTTKeepsInlineDirection(t *testing.T) {
	src := "1\n00:00:01,000 --> 00:00:03,000\n{\\an8}Sign at the top\n\n" +
		"2\n00:00:04,000 --> 00:00:06,000\n<i>italic</i> and <font color=\"#ffcc00\">gold</font>\n\n"
	got := string(SRTToVTT([]byte(src)))
	if !strings.HasPrefix(got, "WEBVTT") {
		t.Fatalf("a conversion must start with the WebVTT header.\n got: %s", got)
	}
	if !strings.Contains(got, "line:4%,start") {
		t.Errorf("{\\an8} must place the cue at the top.\n got: %s", got)
	}
	if !strings.Contains(got, "<i>italic</i>") {
		t.Errorf("an SRT's <i> must survive.\n got: %s", got)
	}
	if !strings.Contains(got, "<c.dm-cffcc00>gold</c>") {
		t.Errorf("<font color> must become a colour class.\n got: %s", got)
	}
	// The cue text must carry no override tag and no escaped markup. (The STYLE
	// block legitimately contains braces, so the cues are what gets checked.)
	for _, cue := range strings.Split(got, "\n\n") {
		if !strings.Contains(cue, "-->") {
			continue
		}
		if strings.Contains(cue, "{") || strings.Contains(cue, "&lt;font") {
			t.Errorf("no override tag or escaped markup should be visible.\n got: %s", cue)
		}
	}
	// Timestamps are normalised from SubRip's comma to WebVTT's dot.
	if !strings.Contains(got, "00:00:01.000 --> 00:00:03.000") {
		t.Errorf("timestamps must use dots.\n got: %s", got)
	}
}

// Whatever the converter does, CueTime must not lose the timing line: ShiftVTT
// rewrites these documents for packaged sessions.
func TestConvertedVTTStillShifts(t *testing.T) {
	src := `[Script Info]
PlayResX: 1920
PlayResY: 1080

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Sign,Arial,72,&H0000FFFF,0,0,0,0,100,100,0,0,1,2,1,8,10,10,20,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:10.00,0:00:12.00,Sign,,0,0,0,,{\an8}later sign
`
	shifted := string(ShiftVTT(ASSToVTT([]byte(src)), 5))
	if !strings.Contains(shifted, "00:00:05.000 --> 00:00:07.000") {
		t.Errorf("a converted document must still be shiftable.\n got: %s", shifted)
	}
	if !strings.Contains(shifted, "line:") {
		t.Errorf("shifting must not drop the cue settings.\n got: %s", shifted)
	}
}
