package api

import "testing"

// The audit is scoped to BDRip on purpose: a WebRip usually has its subtitles
// burned into the picture, so nothing here could tell a subtitled one from a
// bare one and reporting it as missing would be wrong.
func TestBdripOnlyScope(t *testing.T) {
	cases := map[string]bool{
		"/public/Cartoon/剧名/bdrip/[VCB-Studio] Show [01][Ma10p_1080p].mkv": true,
		"/public/Cartoon/剧名/BDRip/Show [01].mkv":                           true,
		"/public/Cartoon/剧名/BD-rip/Show [01].mkv":                          true,
		"/public/Cartoon/剧名/BluRay/Show [01].mkv":                          true,
		"/public/Cartoon/剧名/BDMV/STREAM/00001.m2ts":                        true,
		"/public/Cartoon/剧名/BDremux/Show [01].mkv":                         true,

		"/public/Cartoon/剧名/webrip/[BeanSub] Show [01][WebRip 1080p].mkv": false,
		"/public/Cartoon/剧名/Web-DL/Show [01].mkv":                         false,
		"/public/Cartoon/剧名/Show [01][1080p].mkv":                         false,
		"/public/Cartoon/剧名/HDTV/Show [01].mkv":                           false,
		// A bare "BD" is deliberately not a marker: it is a two-letter token
		// that turns up inside other words, and guessing costs exactly the
		// BDRip/WebRip distinction the audit rests on.
		"/public/Cartoon/剧名/[VCB-Studio] Show [BD 1080p]/Show [01].mkv": false,
	}
	for path, want := range cases {
		if got := bdripOnly(path); got != want {
			t.Errorf("bdripOnly(%q) = %v, want %v", path, got, want)
		}
	}
}

// What a cached probe says. "No text subtitle track" and "no answer yet" must
// stay distinguishable: the first is a verdict, the second is not, and the audit
// reports them differently.
func TestHasTextSubtitleTrack(t *testing.T) {
	const (
		assTrack = `{"streams":[
			{"index":0,"codec_type":"video","codec_name":"hevc"},
			{"index":1,"codec_type":"audio","codec_name":"flac"},
			{"index":2,"codec_type":"subtitle","codec_name":"ass","tags":{"language":"jpn"}}]}`
		srtTrack = `{"streams":[
			{"index":0,"codec_type":"video","codec_name":"h264"},
			{"index":1,"codec_type":"subtitle","codec_name":"subrip"}]}`
		pgsOnly = `{"streams":[
			{"index":0,"codec_type":"video","codec_name":"h264"},
			{"index":1,"codec_type":"subtitle","codec_name":"hdmv_pgs_subtitle"}]}`
		noSubs = `{"streams":[
			{"index":0,"codec_type":"video","codec_name":"hevc"},
			{"index":1,"codec_type":"audio","codec_name":"aac"}]}`
	)

	cases := []struct {
		name      string
		probe     string
		wantFound bool
		wantKnown bool
	}{
		{"ASS track", assTrack, true, true},
		{"SubRip track", srtTrack, true, true},
		// A bitmap track needs burning in, so it does not make the episode
		// watchable the way a text track does — but the file *was* probed, so
		// the answer is known.
		{"PGS only", pgsOnly, false, true},
		{"no subtitle streams", noSubs, false, true},
		// Nothing probed yet: not an answer.
		{"empty probe", "", false, false},
		{"whitespace probe", "   ", false, false},
		{"unparseable probe", "{not json", false, false},
		// A probe with no video stream is treated as incomplete by
		// ProbeFromCache, so it is not an answer either.
		{"streams without video", `{"streams":[{"index":0,"codec_type":"audio","codec_name":"aac"}]}`, false, false},
	}
	for _, c := range cases {
		found, known := hasTextSubtitleTrack(c.probe)
		if found != c.wantFound || known != c.wantKnown {
			t.Errorf("%s: hasTextSubtitleTrack = (%v, %v), want (%v, %v)",
				c.name, found, known, c.wantFound, c.wantKnown)
		}
	}
}
