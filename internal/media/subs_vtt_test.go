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
			// The regression that matters most: a plain line must come out exactly
			// as it always did, with no placement invented for it.
			name:    "plain dialogue keeps the player's default",
			text:    `Dialogue: 0,0:00:01.00,0:00:03.00,Dialogue,,0,0,0,,你好,世界`,
			wantCue: "",
			wantIn:  []string{"00:00:01.000 --> 00:00:03.000\n你好,世界"},
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
			name:   "italics, bold and colour survive",
			text:   `Dialogue: 0,0:00:01.00,0:00:03.00,Dialogue,,0,0,0,,{\i1}italics{\i0} and {\b1}bold{\b0} and {\c&H0000FF&}red`,
			wantIn: []string{"<i>italics</i>", "<b>bold</b>", "<c.dm-cff0000>red</c>", "::cue(.dm-cff0000) { color: #ff0000 }"},
		},
		{
			// \p1 starts a vector drawing: its "text" is coordinate pairs, and
			// rendering them is worse than rendering nothing.
			name:   "a vector drawing is dropped, not shown",
			text:   `Dialogue: 0,0:00:01.00,0:00:03.00,Dialogue,,0,0,0,,{\p1}m 0 0 l 100 0 100 100 0 100{\p0}visible`,
			wantIn: []string{"visible"},
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
			name:   "text that looks like markup is escaped",
			text:   `Dialogue: 0,0:00:01.00,0:00:03.00,Dialogue,,0,0,0,,Fish & chips <3`,
			wantIn: []string{"Fish &amp; chips &lt;3"},
		},
		{
			name:   "hard breaks become real lines, karaoke keeps its syllables",
			text:   `Dialogue: 0,0:00:01.00,0:00:03.00,Dialogue,,0,0,0,,{\k20}き{\k20}ら{\k20}き\Nsecond line`,
			wantIn: []string{"きらき\nsecond line"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(ASSToVTT([]byte(head + tc.text + "\n")))
			if tc.wantCue != "" && !strings.Contains(got, tc.wantCue) {
				t.Errorf("cue settings %q missing.\n got: %s", tc.wantCue, got)
			}
			if tc.wantCue == "" && strings.Contains(got, "-->") {
				// Only the placement-free cases assert this, and they are the
				// ones where the timing line must carry no settings at all.
				for _, line := range strings.Split(got, "\n") {
					if strings.Contains(line, "-->") && strings.TrimSpace(strings.SplitN(line, "-->", 2)[1]) != strings.TrimSpace(strings.Fields(strings.SplitN(line, "-->", 2)[1])[0]) {
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
