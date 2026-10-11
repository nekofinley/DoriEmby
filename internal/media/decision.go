package media

import (
	"fmt"
	"strings"
)

// ClientKind is the playback capability class of the requesting client.
type ClientKind string

const (
	ClientSafari  ClientKind = "safari"  // iOS/iPadOS/macOS Safari — native AVPlayer, no MSE
	ClientChrome  ClientKind = "chrome"  // Chrome/Edge/Chromium-based + Android — MSE available
	ClientFirefox ClientKind = "firefox" // no HEVC
)

// DetectClient maps a User-Agent to a capability class.
func DetectClient(ua string) ClientKind {
	ua = strings.ToLower(ua)
	isIOS := strings.Contains(ua, "iphone") || strings.Contains(ua, "ipad") || strings.Contains(ua, "ipod") ||
		(strings.Contains(ua, "macintosh") && strings.Contains(ua, "touch"))
	if isIOS {
		return ClientSafari
	}
	isSafariDesktop := strings.Contains(ua, "safari") && !strings.Contains(ua, "chrome") &&
		!strings.Contains(ua, "chromium") && !strings.Contains(ua, "edg") && !strings.Contains(ua, "opr")
	if isSafariDesktop {
		return ClientSafari
	}
	if strings.Contains(ua, "firefox") {
		return ClientFirefox
	}
	return ClientChrome
}

// Decision is the playback plan for one file on one client.
type Decision struct {
	Mode       string  `json:"mode"` // direct | proxy | remux | transcode
	Kind       string  `json:"kind"` // mp4 | hls
	URL        string  `json:"url,omitempty"`
	MimeType   string  `json:"mime_type,omitempty"`
	Reason     string  `json:"reason"`
	Duration   float64 `json:"duration,omitempty"`
	VideoCodec string  `json:"video_codec,omitempty"`
	AudioCodec string  `json:"audio_codec,omitempty"`
	VideoIndex int     `json:"video_index"`
	AudioIndex int     `json:"audio_index"`
	Container  string  `json:"container,omitempty"`
	Resolution string  `json:"resolution,omitempty"`
}

var (
	safariVideo = map[string]bool{"h264": true, "hevc": true}
	safariAudio = map[string]bool{"aac": true, "mp3": true, "flac": true, "alac": true}
	chromeVideo = map[string]bool{"h264": true, "hevc": true, "av1": true, "vp9": true, "vp8": true}
	chromeAudio = map[string]bool{"aac": true, "mp3": true, "opus": true, "vorbis": true, "flac": true}
	firefoxVideo = map[string]bool{"h264": true, "vp9": true, "vp8": true, "av1": true}
	firefoxAudio = map[string]bool{"aac": true, "mp3": true, "opus": true, "vorbis": true, "flac": true}
)

// mp4-family tokens as reported by ffprobe format_name
var mp4Family = map[string]bool{"mov": true, "mp4": true, "m4a": true, "m4v": true, "mj2": true, "3gp": true, "3g2": true}

func containerTokens(formatName string) []string {
	parts := strings.Split(formatName, ",")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func containerMatches(formatName string, allowWebM bool) bool {
	found := false
	for _, t := range containerTokens(formatName) {
		if mp4Family[t] {
			found = true
			break
		}
		if allowWebM && (t == "webm" || t == "matroska") {
			// only plain webm (vp9/av1) counts; matroska alone is not webm
			if t == "webm" {
				found = true
				break
			}
		}
	}
	return found
}

// extOf returns lowercase extension (with dot) of a path/URL.
func extOf(path string) string {
	p := path
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	if i := strings.LastIndex(p, "."); i >= 0 && i > strings.LastIndex(p, "/") {
		return strings.ToLower(p[i:])
	}
	return ""
}

// CanDirectPlay checks whether the probe is playable natively by the client
// straight from a progressive URL (no server-side work).
func CanDirectPlay(probe *Probe, path string, client ClientKind) (bool, string) {
	if probe == nil || probe.Video() == nil {
		return false, "无法识别视频流"
	}
	ext := extOf(path)

	switch client {
	case ClientSafari:
		if ext != ".mp4" && ext != ".m4v" && ext != ".mov" {
			return false, "Safari 仅支持 MP4/MOV 直连"
		}
		if !containerMatches(probe.Format.FormatName, false) {
			return false, "容器不是 MP4"
		}
	case ClientChrome:
		if ext == ".mkv" {
			return false, "浏览器 <video> 不支持 MKV 容器"
		}
		if !containerMatches(probe.Format.FormatName, true) {
			return false, "容器不支持直连"
		}
	case ClientFirefox:
		if ext != ".mp4" && ext != ".webm" {
			return false, "Firefox 直连仅支持 MP4/WebM"
		}
	}

	v := probe.Video()
	var videoOK, audioOK bool
	var audioFail string
	switch client {
	case ClientSafari:
		if v.CodecName == "hevc" {
			if p := v.DVProfile(); p == 5 {
				return false, "杜比视界 Profile 5 需转封装"
			}
			if strings.EqualFold(v.CodecTagString, "hev1") {
				return false, "HEVC 编码标签为 hev1,Safari 需要 hvc1"
			}
			videoOK = true
		} else {
			videoOK = safariVideo[v.CodecName]
		}
		if a := firstAudio(probe); a != nil {
			audioOK = safariAudio[a.CodecName]
			if !audioOK {
				audioFail = "音频 " + strings.ToUpper(a.CodecName) + " iOS 不支持"
			}
		} else {
			audioOK = true // video-only file
		}
	case ClientChrome:
		videoOK = chromeVideo[v.CodecName]
		if a := firstAudio(probe); a != nil {
			audioOK = chromeAudio[a.CodecName]
			if !audioOK {
				audioFail = "音频 " + strings.ToUpper(a.CodecName) + " 不支持"
			}
		} else {
			audioOK = true
		}
	case ClientFirefox:
		videoOK = firefoxVideo[v.CodecName]
		if a := firstAudio(probe); a != nil {
			audioOK = firefoxAudio[a.CodecName]
			if !audioOK {
				audioFail = "音频 " + strings.ToUpper(a.CodecName) + " 不支持"
			}
		} else {
			audioOK = true
		}
	}
	if !videoOK {
		return false, fmt.Sprintf("视频编码 %s 不支持直连", strings.ToUpper(v.CodecName))
	}
	if !audioOK {
		return false, audioFail
	}
	return true, ""
}

func firstAudio(p *Probe) *ProbeStream {
	streams := p.AudioStreams()
	if len(streams) > 0 {
		return streams[0]
	}
	return nil
}

// Decide produces the playback plan.
// hasDirect: the source driver can produce a 302-able URL.
// directHeaders: required headers for the direct link (non-empty means the browser
// cannot fetch it and we must not 302).
func Decide(probe *Probe, path string, client ClientKind, hasDirect bool, directHeaders map[string]string, force string) *Decision {
	d := &Decision{VideoIndex: -1, AudioIndex: -1}
	if probe == nil {
		d.Mode, d.Kind, d.Reason = "transcode", "hls", "探测失败,强制转码"
		return d
	}
	v := probe.Video()
	if v != nil {
		d.VideoIndex = v.Index
		d.VideoCodec = v.CodecName
		d.Resolution = fmt.Sprintf("%dx%d", v.Width, v.Height)
	}
	if a := firstAudio(probe); a != nil {
		d.AudioIndex = a.Index
		d.AudioCodec = a.CodecName
	}
	d.Duration = probe.Duration()
	d.Container = probe.Format.FormatName

	if force == "direct" {
		if hasDirect && len(directHeaders) == 0 {
			d.Mode, d.Kind, d.Reason = "direct", "mp4", "手动指定直连"
		} else {
			d.Mode, d.Kind, d.Reason = "proxy", "mp4", "手动指定直连但源不支持,退化为代理"
		}
		return d
	}
	if force == "proxy" {
		d.Mode, d.Kind, d.Reason = "proxy", "mp4", "手动指定代理"
		return d
	}
	if force == "remux" || force == "transcode" {
		d.Mode, d.Kind, d.Reason = force, "hls", "手动指定 " + force
		return d
	}

	can, why := CanDirectPlay(probe, path, client)
	if can {
		if hasDirect && len(directHeaders) == 0 {
			d.Mode, d.Kind, d.Reason = "direct", "mp4", "客户端原生支持,302 直连"
		} else {
			d.Mode, d.Kind, d.Reason = "proxy", "mp4", "客户端原生支持,源不支持 302,走代理"
		}
		return d
	}

	// audio-only failure but video fine: remux fixes it (video copy + audio aac)
	if v != nil && (v.CodecName == "h264" || v.CodecName == "hevc") {
		d.Mode, d.Kind = "remux", "hls"
		d.Reason = why + " → HLS 转封装(视频流直拷)"
		return d
	}
	d.Mode, d.Kind = "transcode", "hls"
	d.Reason = why + " → HLS 转码"
	return d
}
