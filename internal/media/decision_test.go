package media

import (
	"strings"
	"testing"
)

func decisionProbe(codec, profile, tag string) *Probe {
	p := &Probe{}
	p.Format.FormatName = "matroska,webm"
	p.Streams = append(p.Streams,
		ProbeStream{Index: 0, CodecType: "video", CodecName: codec, Profile: profile, CodecTagString: tag, Width: 1920, Height: 1080},
		ProbeStream{Index: 1, CodecType: "audio", CodecName: "aac"},
	)
	return p
}

func decideWith(codec, profile, tag string, client ClientKind, opts DecideOptions) *Decision {
	return Decide(decisionProbe(codec, profile, tag), "/m/x.mkv", client, opts)
}

// Forcing a remux must not copy a video stream the client cannot decode: the
// result plays audio with no picture at all, on every client.
func TestForcedRemuxFallsBackWhenVideoCannotBeCopied(t *testing.T) {
	cases := []struct {
		name         string
		codec        string
		profile      string
		tag          string
		client       ClientKind
		appleHEVCCpy bool
		wantMode     string
		wantInCause  string
	}{
		{"AV1 is decodable by nobody", "av1", "", "", ClientSafari, false, "transcode", "AV1"},
		{"VP9 in HLS is not for Safari", "vp9", "", "", ClientSafari, false, "transcode", "VP9"},
		{"10-bit H.264 has no hardware path", "h264", "High 10", "avc1", ClientSafari, false, "transcode", "H264"},
		{"8-bit H.264 copies fine", "h264", "High", "avc1", ClientSafari, false, "remux", "手动指定转封装"},
		// Apple does not decode HEVC carried in MPEG-TS, so copying it there is
		// the "audio but no picture" bug — off by default.
		{"HEVC is not copied for Apple by default", "hevc", "Main 10", "hvc1", ClientSafari, false, "transcode", "HEVC"},
		{"HEVC is copied for Apple when opted in", "hevc", "Main 10", "hvc1", ClientSafari, true, "remux", "手动指定转封装"},
		{"HEVC is not copied for MSE clients", "hevc", "Main", "hvc1", ClientChrome, true, "transcode", "HEVC"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := decideWith(c.codec, c.profile, c.tag, c.client, DecideOptions{Force: "remux", AppleHEVCCopy: c.appleHEVCCpy})
			if d.Mode != c.wantMode {
				t.Fatalf("mode = %q, want %q (reason: %s)", d.Mode, c.wantMode, d.Reason)
			}
			if !strings.Contains(d.Reason, c.wantInCause) {
				t.Fatalf("reason %q does not mention %q", d.Reason, c.wantInCause)
			}
			if d.Kind != "hls" {
				t.Fatalf("kind = %q, want hls", d.Kind)
			}
		})
	}
}

// The unsupported-copy case must explain itself: the user forced one thing and
// got another, so the reason has to say why — and say it accurately.
func TestForcedRemuxFallbackExplainsWhy(t *testing.T) {
	d := decideWith("av1", "", "", ClientSafari, DecideOptions{Force: "remux"})
	if !strings.Contains(d.Reason, "没有画面") {
		t.Fatalf("reason should explain the symptom, got: %s", d.Reason)
	}
	if !strings.Contains(d.Reason, "已改为转码") {
		t.Fatalf("reason should say what was chosen instead, got: %s", d.Reason)
	}

	// Apple can decode HEVC — just not from MPEG-TS — so the message must blame
	// the container, not the codec's decodability.
	hevc := decideWith("hevc", "Main 10", "hvc1", ClientSafari, DecideOptions{Force: "remux"})
	if !strings.Contains(hevc.Reason, "MPEG-TS") {
		t.Fatalf("Apple HEVC fallback should blame the container, got: %s", hevc.Reason)
	}
}

func TestForcedTranscodeIsAlwaysHonoured(t *testing.T) {
	for _, codec := range []string{"h264", "hevc", "av1"} {
		d := decideWith(codec, "High", "avc1", ClientSafari, DecideOptions{Force: "transcode"})
		if d.Mode != "transcode" || d.Kind != "hls" {
			t.Fatalf("codec %s: mode=%q kind=%q, want transcode/hls", codec, d.Mode, d.Kind)
		}
	}
}

// The automatic path already refused to copy undecodable video; this pins that
// down so a future edit cannot reintroduce the same class of bug.
func TestAutoDecisionNeverCopiesUndecodableVideo(t *testing.T) {
	for _, codec := range []string{"av1", "vp9", "mpeg2video"} {
		d := decideWith(codec, "", "", ClientSafari, DecideOptions{})
		if d.Mode != "transcode" {
			t.Fatalf("codec %s: mode=%q, want transcode (reason: %s)", codec, d.Mode, d.Reason)
		}
	}
	// H.264 8-bit MKV on Safari is the one case that legitimately copies.
	d := decideWith("h264", "High", "avc1", ClientSafari, DecideOptions{})
	if d.Mode != "remux" {
		t.Fatalf("mode=%q, want remux for 8-bit H.264 (reason: %s)", d.Mode, d.Reason)
	}
}

// Apple + HEVC: off by default (transcode, which plays), opt-in to copy.
func TestAppleHEVCCopyPolicy(t *testing.T) {
	auto := decideWith("hevc", "Main 10", "hvc1", ClientSafari, DecideOptions{})
	if auto.Mode != "transcode" {
		t.Fatalf("Apple HEVC default mode = %q, want transcode", auto.Mode)
	}
	if !strings.Contains(auto.Reason, "Apple 不解码 MPEG-TS") {
		t.Fatalf("default reason should explain the container limit, got: %s", auto.Reason)
	}

	optedIn := decideWith("hevc", "Main 10", "hvc1", ClientSafari, DecideOptions{AppleHEVCCopy: true})
	if optedIn.Mode != "remux" {
		t.Fatalf("opted-in Apple HEVC mode = %q, want remux", optedIn.Mode)
	}

	// The flag must never enable HEVC copying for clients that cannot demux it.
	// Checked through a forced remux, because the automatic path legitimately
	// offers Chrome a direct play of Matroska instead — the player falls back to
	// transcoding client-side when the codec turns out to be undecodable.
	chrome := decideWith("hevc", "Main 10", "hvc1", ClientChrome, DecideOptions{Force: "remux", AppleHEVCCopy: true})
	if chrome.Mode != "transcode" {
		t.Fatalf("Chrome forced-remux mode = %q, want transcode even with AppleHEVCCopy", chrome.Mode)
	}
}
