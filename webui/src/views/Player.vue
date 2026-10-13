<script setup>
import { ref, onMounted, onBeforeUnmount, computed, nextTick, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, detectClient, fmtTime, fmtSize } from '../api.js'
import AppIcon from '../components/AppIcon.vue'
import ToastHost from '../components/ToastHost.vue'
import { toast } from '../toast.js'

const route = useRoute()
const router = useRouter()

const play = ref(null)
const error = ref('')
const fatal = ref('')
const videoEl = ref(null)
const stageEl = ref(null)
const hls = ref(null)
const currentAudio = ref(0)
const activeSub = ref(null)
const duration = ref(0)
const position = ref(0)
const buffered = ref(0)
const isLoading = ref(true)
const isPlaying = ref(false)
const muted = ref(localStorage.getItem('dm:muted') === '1')
const volume = ref(Number(localStorage.getItem('dm:volume') ?? 1))
const rate = ref(Number(localStorage.getItem('dm:rate') ?? 1))
const controlsVisible = ref(true)
const panelOpen = ref(false)
const menuOpen = ref(false)
const fsActive = ref(false)
const pipActive = ref(false)
const scrubbing = ref(false)
const scrubTime = ref(0)
const hoverTime = ref(null)
const hoverX = ref(0)
const forceMode = ref('')      // current override, reapplied when re-fetching
const probeElapsed = ref(0)    // seconds the background probe has been running
const stallSeconds = ref(0)    // seconds spent waiting on data mid-playback
const streamStatus = ref(null) // segment-generation progress from the server

const isIOS = computed(() => {
  const ua = navigator.userAgent
  return /iPhone|iPad|iPod/.test(ua) || (/Macintosh/.test(ua) && 'ontouchend' in document)
})
/** iOS gets the native control surface; everything else gets the custom chrome. */
const nativeControls = computed(() => isIOS.value)

const modeClass = computed(() => `mode-${play.value?.mode || 'direct'}`)
const modeLabel = computed(() => ({
  direct: '302 直连', proxy: '代理播放', remux: 'HLS 转封装', transcode: 'HLS 转码',
}[play.value?.mode] || play.value?.mode || ''))

const currentSize = computed(() => {
  const v = (play.value?.versions || []).find((x) => x.current)
  return v?.size ? fmtSize(v.size) : ''
})
const shownTime = computed(() => (scrubbing.value ? scrubTime.value : position.value))
const progressPct = computed(() => (duration.value > 0 ? Math.min(100, (shownTime.value / duration.value) * 100) : 0))
const bufferedPct = computed(() => (duration.value > 0 ? Math.min(100, (buffered.value / duration.value) * 100) : 0))
const volumePct = computed(() => (muted.value ? 0 : volume.value * 100))

/** Seconds of timeline the player needs buffered before it will start. */
const streamNeeded = 12

const streamPct = computed(() => {
  const s = streamStatus.value
  if (!s || !s.generated_seconds) return 0
  return Math.min(100, (s.generated_seconds / streamNeeded) * 100)
})

/** Timeline seconds per wall second: below 1 the server cannot keep up. */
const streamRate = computed(() => {
  const r = streamStatus.value?.realtime_factor
  return r && isFinite(r) ? r : null
})

const encoderTooSlow = computed(() => {
  const r = streamRate.value
  return r !== null && r < 0.9 && stallSeconds.value >= 20
})

/** What the spinner should say — never leave the user guessing. */
const loadingText = computed(() => {
  if (play.value?.state === 'probing') {
    return `正在探测媒体信息(容器 / 编码 / 音轨)… ${probeElapsed.value.toFixed(0)}s`
  }
  const s = streamStatus.value
  if (s && s.generated_seconds > 0) {
    const rate = streamRate.value
    const speed = rate !== null ? ` · 编码速度 ${rate.toFixed(2)}× 实时` : ''
    return `正在生成分片 · 已就绪 ${s.generated_seconds.toFixed(0)}s / ${streamNeeded}s${speed}`
  }
  if (stallSeconds.value >= 3) return `正在缓冲… 已等待 ${stallSeconds.value.toFixed(0)}s`
  return modeLabel.value ? `正在准备播放 · ${modeLabel.value}` : '正在准备播放…'
})
/** Long enough that the source is clearly the problem, not a slow start. */
const slowHint = computed(() => play.value?.state !== 'probing' && stallSeconds.value >= 25)
const rates = [0.5, 0.75, 1, 1.25, 1.5, 2]
const forceModes = [
  { value: '', label: '自动决策', hint: '按客户端能力自动选择' },
  { value: 'direct', label: '强制 302 直连', hint: '不经过服务器,最省带宽' },
  { value: 'proxy', label: '强制代理', hint: '服务器中转,支持拖动' },
  { value: 'remux', label: '强制 HLS 转封装', hint: '视频直拷,只转不兼容音轨' },
  { value: 'transcode', label: '强制 HLS 转码', hint: '编码不支持时使用,占用 CPU' },
]

let lastSave = 0
let hideTimer = null
let probeTimer = null
let streamTimer = null
let stallTimer = null
let stallSince = 0
let disposed = false

/* ------------------------------------------------------------------ playback */

async function loadDecision(force) {
  error.value = ''
  fatal.value = ''
  isLoading.value = true
  forceMode.value = force || ''
  stopProbePoll()
  try {
    play.value = await fetchPlay()
  } catch (e) {
    fatal.value = e.message
    isLoading.value = false
    return
  }
  if (play.value?.state === 'probing') {
    startProbePoll()
    return
  }
  await nextTick()
  attachSource()
}

function fetchPlay() {
  const params = new URLSearchParams({ client: detectClient() })
  if (forceMode.value) params.set('force', forceMode.value)
  if (currentAudio.value) params.set('a', String(currentAudio.value))
  return api.get(`/api/play/${route.params.fileId}?${params}`)
}

/*
 * Probing is the slowest step of starting playback and happens in the
 * background on the server, so poll its state instead of hanging on one
 * request. This is what turns an opaque spinner into "探测媒体信息… 12s".
 */
function startProbePoll() {
  stopProbePoll()
  probeElapsed.value = 0
  const started = Date.now()
  let idleRetries = 0

  const tick = async () => {
    if (disposed) return
    probeElapsed.value = (Date.now() - started) / 1000
    let st = null
    try { st = await api.get(`/api/play/${route.params.fileId}/status`) } catch { /* transient */ }
    if (disposed) return

    if (st && st.state === 'failed') {
      fatal.value = `媒体探测失败:${st.error || '未知原因'}`
      isLoading.value = false
      return
    }
    if (st && st.state === 'ready') {
      try { play.value = await fetchPlay() } catch (e) { fatal.value = e.message; isLoading.value = false; return }
      if (disposed) return
      if (play.value?.state === 'probing') { probeTimer = setTimeout(tick, 800); return }
      await nextTick()
      attachSource()
      return
    }
    // idle: no job registered anymore (server restart, or a failed probe inside
    // its cooldown). Ask /api/play again to (re)start it, but never loop.
    if (st && st.state === 'idle') {
      if (++idleRetries > 2) {
        fatal.value = '媒体探测未能启动,请重试'
        isLoading.value = false
        return
      }
      try { play.value = await fetchPlay() } catch { /* handled below */ }
      if (disposed) return
      if (play.value?.url) { await nextTick(); attachSource(); return }
      if (play.value?.state !== 'probing') {
        fatal.value = '媒体探测未能启动,请重试'
        isLoading.value = false
        return
      }
    }
    probeTimer = setTimeout(tick, probeElapsed.value < 5 ? 700 : 1500)
  }
  probeTimer = setTimeout(tick, 500)
}

function stopProbePoll() {
  if (probeTimer) { clearTimeout(probeTimer); probeTimer = null }
}

/*
 * Segment generation progress. Preparing HLS can take a minute (the server must
 * reach the source, seek, decode and re-encode every 4s of video), so poll the
 * status endpoint and show how much timeline is ready plus the encoder's
 * realtime factor — which is what tells the user whether it will ever catch up.
 */
function startStreamPoll() {
  stopStreamPoll()
  const tick = async () => {
    if (disposed) return
    // The probe poller already owns the endpoint while a probe is running.
    if (play.value?.state !== 'probing') {
      let st = null
      try { st = await api.get(`/api/play/${route.params.fileId}/status`) } catch { /* transient */ }
      if (disposed) return
      if (st && st.stream) streamStatus.value = st.stream
      else if (st && !st.stream && !isLoading.value) streamStatus.value = null
    }
    streamTimer = setTimeout(tick, isLoading.value ? 1200 : 5000)
  }
  streamTimer = setTimeout(tick, 800)
}

function stopStreamPoll() {
  if (streamTimer) { clearTimeout(streamTimer); streamTimer = null }
}

/* Stall clock: while the player waits on data, show how long we have waited —
   that is the difference between "still loading" and "dead". */
function onWaiting() {
  isLoading.value = true
  if (stallSince) return
  stallSince = Date.now()
  stallSeconds.value = 0
  clearInterval(stallTimer)
  stallTimer = setInterval(() => {
    stallSeconds.value = (Date.now() - stallSince) / 1000
  }, 500)
}

function onPlaying() {
  isLoading.value = false
  clearStall()
}

function clearStall() {
  stallSince = 0
  stallSeconds.value = 0
  if (stallTimer) { clearInterval(stallTimer); stallTimer = null }
}

const cands = ref([])
const candIdx = ref(-1)
let HlsLib = null
let mediaRecovered = false
let netRestarted = false

// 候选链:主决策 + 后端下发的备用方案(直连/代理/转封装/转码)。
// 浏览器报错就自动换下一种,直到放出来为止。
function buildCandidates() {
  const p = play.value
  const list = [{ mode: p.mode, url: p.url, kind: p.kind || (p.url.includes('/api/hls/') ? 'hls' : 'mp4') }]
  for (const a of (p.alternates || [])) {
    list.push({ mode: a.mode, url: a.url, kind: a.url.includes('/api/hls/') ? 'hls' : 'mp4' })
  }
  const v = videoEl.value
  if (v && p.video_codec === 'hevc' && list[0].kind === 'mp4') {
    const hevcOK = v.canPlayType('video/mp4; codecs="hvc1.1.6.L93.B0"') ||
      v.canPlayType('video/mp4; codecs="hev1.1.6.L93.B0"') ||
      v.canPlayType('video/x-matroska; codecs="hvc1.1.6.L93.B0"')
    if (!hevcOK) {
      const ti = list.findIndex((c) => c.mode === 'transcode')
      if (ti > 0) {
        const [t] = list.splice(ti, 1)
        list.unshift(t)
        toast.info('本机不支持 HEVC 硬解,已切换云端转码')
      }
    }
  }
  return list
}

function destroyHls() {
  if (hls.value) { hls.value.destroy(); hls.value = null }
}

async function attachSource() {
  cands.value = buildCandidates()
  mediaRecovered = false
  netRestarted = false
  await startCand(0)
}

async function startCand(i) {
  if (i >= cands.value.length) {
    isLoading.value = false
    fatal.value = '所有播放方式都失败了(本机直连 + 云端转码)。可以切换音轨,或强制转码重试。'
    return
  }
  candIdx.value = i
  const c = cands.value[i]
  if (play.value) play.value = { ...play.value, mode: c.mode, url: c.url, kind: c.kind }
  const v = videoEl.value
  if (!v) return
  destroyHls()
  startStreamPoll()
  v.removeAttribute('src')
  activeSub.value = null

  if (c.kind === 'hls') {
    const canNative = v.canPlayType('application/vnd.apple.mpegurl')
    if (canNative) {
      v.src = c.url
    } else {
      if (!HlsLib) HlsLib = (await import('hls.js')).default
      const Hls = HlsLib
      if (Hls.isSupported()) {
        hls.value = new Hls({
          maxBufferLength: 30,
          manifestLoadingTimeOut: 30000,
          manifestLoadingMaxRetry: 4,
          levelLoadingTimeOut: 30000,
          fragLoadingTimeOut: 120000,
          fragLoadingMaxRetry: 8,
          fragLoadingRetryDelay: 1000,
          nudgeMaxRetry: 10,
        })
        hls.value.loadSource(c.url)
        hls.value.attachMedia(v)
        hls.value.on(Hls.Events.ERROR, onHlsError)
      } else {
        await startCand(i + 1)
        return
      }
    }
  } else {
    v.src = c.url
  }

  applyVolume()
  v.playbackRate = rate.value
  v.load()
  v.play().catch(() => { /* autoplay may be blocked — user presses play */ })
  showControls()
}

function onHlsError(_, data) {
  if (!data.fatal) return
  if (data.type === HlsLib.ErrorTypes.MEDIA_ERROR && !mediaRecovered) {
    mediaRecovered = true
    hls.value?.recoverMediaError()
    return
  }
  if (data.type === HlsLib.ErrorTypes.NETWORK_ERROR && !netRestarted) {
    netRestarted = true
    hls.value?.startLoad()
    return
  }
  toast.info(`${modeLabel.value}失败,切换下一种播放方式…`)
  startCand(candIdx.value + 1)
}

function onLoadedMetadata() {
  isLoading.value = false
  const v = videoEl.value
  duration.value = v.duration || play.value?.duration || 0
  // Subtitles come on by themselves; the panel is only there to change or turn
  // them off.
  autoSelectSub()
  api.get(`/api/progress?file_id=${route.params.fileId}`).then((p) => {
    if (p && p.position > 30 && duration.value > 0 && p.position < duration.value * 0.95) {
      v.currentTime = p.position
      toast.info(`已从 ${fmtTime(p.position)} 继续播放`)
    }
  }).catch(() => {})
}

function onErr() {
  isLoading.value = false
  if (!play.value) return
  if (candIdx.value < cands.value.length - 1) {
    toast.info('该方式播放失败,自动切换…')
    startCand(candIdx.value + 1)
    return
  }
  error.value = '播放失败(解码或网络错误)'
}

function onTimeUpdate() {
  const v = videoEl.value
  if (!v) return
  if (!scrubbing.value) position.value = v.currentTime
  if (v.buffered.length) buffered.value = v.buffered.end(v.buffered.length - 1)
  const now = Date.now()
  if (now - lastSave > 10000) { lastSave = now; saveProgress() }
}

function saveProgress() {
  const v = videoEl.value
  if (!v || !v.duration || !isFinite(v.duration)) return
  api.post('/api/progress', {
    file_id: Number(route.params.fileId),
    position: v.currentTime,
    duration: v.duration,
  }).catch(() => {})
}

function selectVersion(v) {
  if (v.current) return
  saveProgress()
  toast.info(`切换到 ${v.label || '另一版本'}`)
  router.replace(`/play/${v.id}`)
}

watch(() => route.params.fileId, (nv, ov) => {
  if (nv && ov && nv !== ov) loadDecision()
})

function selectAudio(i) {
  currentAudio.value = i
  panelOpen.value = false
  loadDecision(play.value.mode === 'transcode' ? 'transcode' : undefined)
}

// Subtitle preference: 'auto' (follow the server's ranking), 'off', or a
// language code the viewer picked. Kept across files so a viewing habit sticks.
const SUB_PREF_KEY = 'dm:subPref'

function findSubTrack(sub) {
  const v = videoEl.value
  if (!v) return null
  for (const el of v.querySelectorAll('track')) {
    if (el.src.includes(`/${sub.id}.vtt`)) return el
  }
  return null
}

// showSub turns one subtitle track on and the rest off. It goes through the
// element's own .track rather than indexing v.textTracks, because hls.js
// registers text tracks of its own and the indices then stop lining up.
function showSub(sub) {
  const v = videoEl.value
  if (!v) return false
  for (const t of v.textTracks) t.mode = 'disabled'
  if (!sub) {
    activeSub.value = null
    return true
  }
  const el = findSubTrack(sub)
  if (!el || !el.track) return false
  el.track.mode = 'showing'
  activeSub.value = sub
  return true
}

function selectSub(sub) {
  const isActive = sub && activeSub.value && activeSub.value.id === sub.id
  if (!sub || isActive) {
    showSub(null)
    localStorage.setItem(SUB_PREF_KEY, 'off')
    return
  }
  if (!showSub(sub)) return
  localStorage.setItem(SUB_PREF_KEY, sub.lang || 'auto')
  toast.info(`字幕:${sub.label || sub.lang || '已开启'}`)
}

// autoSelectSub turns subtitles on without being asked: the track the server
// ranked best, unless the viewer already turned them off or picked a language.
function autoSelectSub() {
  const subs = play.value?.subtitles || []
  if (!subs.length) {
    activeSub.value = null
    return
  }
  const pref = localStorage.getItem(SUB_PREF_KEY) || 'auto'
  if (pref === 'off') {
    // The server marks a track as default, which lets the browser turn it on by
    // itself; make sure "off" still wins.
    showSub(null)
    return
  }
  const wanted = pref !== 'auto' ? subs.find((s) => s.lang === pref) : null
  const pick = wanted || subs.find((s) => s.default) || subs[0]
  nextTick(() => {
    if (!showSub(pick)) activeSub.value = null
  })
}

/* ------------------------------------------------------------------ controls */

function togglePlay() {
  const v = videoEl.value
  if (!v) return
  if (v.paused) v.play().catch(() => {}); else v.pause()
}

function seekBy(delta) {
  const v = videoEl.value
  if (!v || !isFinite(v.duration)) return
  v.currentTime = Math.max(0, Math.min(v.duration, v.currentTime + delta))
  toast.info(`${delta > 0 ? '快进' : '后退'} ${Math.abs(delta)} 秒`, 1200)
}

function pctFromEvent(e) {
  const el = e.currentTarget
  const rect = el.getBoundingClientRect()
  const x = ('clientX' in e ? e.clientX : e.touches?.[0]?.clientX ?? 0) - rect.left
  return Math.max(0, Math.min(1, x / rect.width))
}

function beginScrub(e) {
  if (!duration.value) return
  scrubbing.value = true
  scrubTime.value = pctFromEvent(e) * duration.value
  window.addEventListener('pointermove', moveScrub)
  window.addEventListener('pointerup', endScrub)
}
function moveScrub(e) {
  if (!scrubbing.value) return
  const bar = document.querySelector('.seek')
  if (!bar) return
  const rect = bar.getBoundingClientRect()
  const x = Math.max(0, Math.min(1, (e.clientX - rect.left) / rect.width))
  scrubTime.value = x * duration.value
}
function endScrub() {
  const v = videoEl.value
  if (scrubbing.value && v && isFinite(scrubTime.value)) {
    v.currentTime = scrubTime.value
    position.value = scrubTime.value
  }
  scrubbing.value = false
  window.removeEventListener('pointermove', moveScrub)
  window.removeEventListener('pointerup', endScrub)
}

function onSeekHover(e) {
  if (!duration.value) return
  const rect = e.currentTarget.getBoundingClientRect()
  hoverX.value = e.clientX - rect.left
  hoverTime.value = pctFromEvent(e) * duration.value
}
function clearHover() { hoverTime.value = null }

function applyVolume() {
  const v = videoEl.value
  if (!v) return
  v.volume = volume.value
  v.muted = muted.value
}
function setVolume(val) {
  volume.value = Math.max(0, Math.min(1, val))
  muted.value = volume.value === 0
  localStorage.setItem('dm:volume', String(volume.value))
  localStorage.setItem('dm:muted', muted.value ? '1' : '0')
  applyVolume()
}
function toggleMute() {
  muted.value = !muted.value
  localStorage.setItem('dm:muted', muted.value ? '1' : '0')
  applyVolume()
}
function setRate(r) {
  rate.value = r
  localStorage.setItem('dm:rate', String(r))
  if (videoEl.value) videoEl.value.playbackRate = r
  menuOpen.value = false
}

async function toggleFullscreen() {
  const el = stageEl.value
  const v = videoEl.value
  if (!document.fullscreenElement) {
    if (el?.requestFullscreen) await el.requestFullscreen().catch(() => {})
    else if (v?.webkitEnterFullscreen) v.webkitEnterFullscreen()
  } else {
    document.exitFullscreen?.()
  }
}
async function togglePip() {
  const v = videoEl.value
  if (!v) return
  try {
    if (document.pictureInPictureElement) await document.exitPictureInPicture()
    else await v.requestPictureInPicture()
  } catch { toast.err('当前浏览器不支持画中画') }
}
function onFsChange() { fsActive.value = !!document.fullscreenElement }
function onPipChange() { pipActive.value = !!document.pictureInPictureElement }

function showControls() {
  controlsVisible.value = true
  clearTimeout(hideTimer)
  if (isPlaying.value && !menuOpen.value && !panelOpen.value) {
    hideTimer = setTimeout(() => { controlsVisible.value = false }, 3200)
  }
}
function onStageMove() { if (!isIOS.value) showControls() }

function onKey(e) {
  if (e.target.tagName === 'INPUT' || e.target.tagName === 'TEXTAREA') return
  const k = e.key.toLowerCase()
  if (k === ' ' || k === 'k') { e.preventDefault(); togglePlay(); showControls() }
  else if (k === 'arrowright') { e.preventDefault(); seekBy(10) }
  else if (k === 'arrowleft') { e.preventDefault(); seekBy(-10) }
  else if (k === 'l') seekBy(30)
  else if (k === 'j') seekBy(-30)
  else if (k === 'arrowup') { e.preventDefault(); setVolume(volume.value + 0.05) }
  else if (k === 'arrowdown') { e.preventDefault(); setVolume(volume.value - 0.05) }
  else if (k === 'm') toggleMute()
  else if (k === 'f') toggleFullscreen()
  else if (k === 'p') togglePip()
  else if (k === 'escape') { if (panelOpen.value) panelOpen.value = false; else if (menuOpen.value) menuOpen.value = false }
  else if (/^[0-9]$/.test(k) && videoEl.value?.duration) {
    videoEl.value.currentTime = (Number(k) / 10) * videoEl.value.duration
  }
  showControls()
}

function goBack() {
  saveProgress()
  router.back()
}

onMounted(() => {
  loadDecision()
  applyVolume()
  window.addEventListener('beforeunload', saveProgress)
  window.addEventListener('keydown', onKey)
  document.addEventListener('fullscreenchange', onFsChange)
  document.addEventListener('pointermove', showControls)
  videoEl.value?.addEventListener('enterpictureinpicture', onPipChange)
  videoEl.value?.addEventListener('leavepictureinpicture', onPipChange)
})
onBeforeUnmount(() => {
  disposed = true
  saveProgress()
  destroyHls()
  stopProbePoll()
  stopStreamPoll()
  clearStall()
  clearTimeout(hideTimer)
  window.removeEventListener('beforeunload', saveProgress)
  window.removeEventListener('keydown', onKey)
  document.removeEventListener('fullscreenchange', onFsChange)
  document.removeEventListener('pointermove', showControls)
  window.removeEventListener('pointermove', moveScrub)
  window.removeEventListener('pointerup', endScrub)
})
</script>

<template>
  <div class="player" :class="{ 'hide-ui': !controlsVisible && isPlaying && !menuOpen && !panelOpen }">
    <!-- ================= stage ================= -->
    <div ref="stageEl" class="stage" @pointermove="onStageMove" @dblclick="toggleFullscreen">
      <video
        ref="videoEl"
        :controls="nativeControls"
        playsinline
        webkit-playsinline
        preload="auto"
        @loadedmetadata="onLoadedMetadata"
        @error="onErr"
        @timeupdate="onTimeUpdate"
        @progress="onTimeUpdate"
        @pause="saveProgress(); isPlaying = false; showControls()"
        @play="isPlaying = true; showControls()"
        @waiting="onWaiting"
        @playing="onPlaying"
        @click="!nativeControls && togglePlay()"
        @ratechange="rate = videoEl?.playbackRate || 1"
      >
        <track
          v-for="sub in play?.subtitles || []" :key="sub.id" kind="subtitles"
          :src="sub.url" :label="sub.label || sub.lang || '字幕'"
          :default="!!sub.default"
        />
      </video>

      <!-- top bar -->
      <div class="top-bar">
        <button class="icon-btn solid round" aria-label="返回" @click="goBack">
          <AppIcon name="arrowLeft" :size="19" />
        </button>
        <div class="tb-title">
          <div class="tb-name truncate">{{ play?.title || '播放中' }}</div>
          <div class="tb-sub">
            <span v-if="play" class="badge" :class="modeClass">{{ modeLabel }}</span>
            <span v-if="play?.resolution" class="tiny">{{ play.resolution.replace('x', '×') }}</span>
            <span v-if="play?.video_codec" class="tiny">
              {{ play.video_codec.toUpperCase() }}<template v-if="play.audio_codec"> / {{ play.audio_codec.toUpperCase() }}</template>
            </span>
            <span v-if="currentSize" class="tiny">{{ currentSize }}</span>
          </div>
        </div>
        <button
          class="icon-btn solid round" :class="{ on: panelOpen }"
          aria-label="播放设置" @click="panelOpen = !panelOpen"
        >
          <AppIcon name="list" :size="19" />
        </button>
      </div>

      <!-- center states -->
      <Transition name="fade">
        <div v-if="isLoading && !error && !fatal" class="center-state">
          <span class="ring" />
          <div class="cs-text">{{ loadingText }}</div>
          <div v-if="streamPct > 0" class="cs-bar" role="progressbar" :aria-valuenow="Math.round(streamPct)" aria-valuemin="0" aria-valuemax="100">
            <i :style="{ width: streamPct + '%' }"></i>
          </div>
          <div v-if="encoderTooSlow" class="cs-hint">
            <div class="cs-hint-t">
              服务器编码速度只有实时速度的 {{ streamRate.toFixed(2) }} 倍,追不上播放。
              把「转码最高高度」降到 720p 通常就能追上。
            </div>
            <div class="cs-actions">
              <button class="btn sm" @click="loadDecision('remux')">强制转封装</button>
              <button class="btn sm" @click="loadDecision()">重试自动</button>
            </div>
          </div>
          <div v-else-if="slowHint" class="cs-hint">
            <div class="cs-hint-t">这个片源读取得慢,当前方式追不上播放速度。</div>
            <div class="cs-actions">
              <button class="btn sm" @click="loadDecision('remux')">强制转封装</button>
              <button class="btn sm" @click="loadDecision('transcode')">强制转码</button>
              <button class="btn sm" @click="loadDecision()">重试自动</button>
            </div>
          </div>
        </div>
      </Transition>

      <Transition name="fade">
        <div v-if="error || fatal" class="error-panel">
          <AppIcon name="alert" :size="26" />
          <div class="ep-title">播放遇到问题</div>
          <div class="ep-text">{{ error || fatal }}</div>
          <div class="ep-actions">
            <button class="btn primary" @click="loadDecision()"><AppIcon name="refresh" :size="15" /> 重试自动</button>
            <button class="btn" @click="loadDecision('remux')">强制转封装</button>
            <button class="btn" @click="loadDecision('transcode')">强制转码</button>
          </div>
        </div>
      </Transition>

      <!-- big play affordance when paused (custom chrome only) -->
      <Transition name="fade">
        <button
          v-if="!nativeControls && !isPlaying && !isLoading && !error && !fatal"
          class="big-play" aria-label="播放" @click="togglePlay"
        >
          <AppIcon name="play" :size="30" filled />
        </button>
      </Transition>

      <!-- ================= bottom control bar ================= -->
      <div v-if="!nativeControls" class="controls" @pointermove="showControls">
        <div
          class="seek" @pointerdown="beginScrub" @pointermove="onSeekHover"
          @pointerleave="clearHover"
        >
          <div class="seek-track">
            <div class="seek-buffer" :style="{ width: bufferedPct + '%' }"></div>
            <div class="seek-fill" :style="{ width: progressPct + '%' }"></div>
            <div class="seek-knob" :style="{ left: progressPct + '%' }"></div>
          </div>
          <div v-if="hoverTime !== null" class="seek-tip" :style="{ left: hoverX + 'px' }">{{ fmtTime(hoverTime) }}</div>
        </div>

        <div class="ctl-row">
          <button class="icon-btn" :aria-label="isPlaying ? '暂停' : '播放'" @click="togglePlay">
            <AppIcon :name="isPlaying ? 'pause' : 'play'" :size="20" :filled="true" />
          </button>
          <button class="icon-btn" aria-label="后退 10 秒" @click="seekBy(-10)">
            <AppIcon name="back10" :size="19" />
          </button>
          <button class="icon-btn" aria-label="前进 10 秒" @click="seekBy(10)">
            <AppIcon name="fwd10" :size="19" />
          </button>

          <div class="vol">
            <button class="icon-btn" :aria-label="muted ? '取消静音' : '静音'" @click="toggleMute">
              <AppIcon :name="muted || volume === 0 ? 'volumeMute' : 'volume'" :size="19" />
            </button>
            <div class="vol-slider">
              <div class="vol-fill" :style="{ width: volumePct + '%' }"></div>
              <input
                type="range" min="0" max="1" step="0.02" :value="muted ? 0 : volume"
                aria-label="音量" @input="setVolume(Number($event.target.value))"
              />
            </div>
          </div>

          <div class="time num">
            <span>{{ fmtTime(shownTime) }}</span>
            <span class="dim">/</span>
            <span class="dim">{{ fmtTime(duration) }}</span>
          </div>

          <span class="spacer"></span>

          <button
            v-if="(play?.subtitles || []).length" class="icon-btn"
            :class="{ on: activeSub }" aria-label="字幕" title="字幕"
            @click="panelOpen = !panelOpen"
          >
            <AppIcon name="subtitles" :size="19" />
          </button>

          <div class="menu-wrap">
            <button class="icon-btn" :class="{ on: menuOpen }" aria-label="播放速度与模式" title="设置" @click="menuOpen = !menuOpen">
              <AppIcon name="settings" :size="19" />
            </button>
            <Transition name="fade">
              <div v-if="menuOpen" class="ctl-menu">
                <div class="cm-title">播放速度</div>
                <div class="cm-rates">
                  <button
                    v-for="r in rates" :key="r" class="chip sm" :class="{ on: rate === r }"
                    @click="setRate(r)"
                  >{{ r }}×</button>
                </div>
                <div class="cm-title mt-4">播放模式</div>
                <button
                  v-for="m in forceModes" :key="m.value" class="cm-item"
                  :class="{ on: (play?.mode === m.value) || (!m.value && !forceModes.some(x => x.value === play?.mode)) }"
                  @click="menuOpen = false; loadDecision(m.value || undefined)"
                >
                  <span class="cm-label">{{ m.label }}</span>
                  <span class="tiny">{{ m.hint }}</span>
                </button>
                <div class="cm-title mt-4">画面</div>
                <button class="cm-item" @click="togglePip(); menuOpen = false">
                  <span class="cm-label"><AppIcon name="pip" :size="15" /> 画中画</span>
                  <span class="tiny">{{ pipActive ? '已开启' : '小窗播放' }}</span>
                </button>
              </div>
            </Transition>
          </div>

          <button class="icon-btn" aria-label="全屏" @click="toggleFullscreen">
            <AppIcon :name="fsActive ? 'fullscreenExit' : 'fullscreen'" :size="19" />
          </button>
        </div>
      </div>

      <!-- ================= side panel: tracks ================= -->
      <Transition name="slide">
        <aside v-if="panelOpen" class="panel glass">
          <div class="pn-head">
            <div class="card-title mb-0"><AppIcon name="layers" :size="16" /> 播放设置</div>
            <button class="icon-btn" aria-label="关闭" @click="panelOpen = false"><AppIcon name="close" :size="17" /></button>
          </div>

          <div v-if="(play?.versions || []).length" class="pn-group">
            <div class="label">版本</div>
            <button
              v-for="v in play.versions" :key="v.id"
              class="chip" :class="{ on: v.current }" @click="selectVersion(v)"
            >{{ v.label || '默认版本' }}</button>
          </div>

          <div v-if="(play?.audio_tracks || []).length > 1" class="pn-group">
            <div class="label">音轨</div>
            <button
              v-for="a in play.audio_tracks" :key="a.ordinal"
              class="chip" :class="{ on: a.ordinal === currentAudio }"
              @click="selectAudio(a.ordinal)"
            >
              {{ a.codec.toUpperCase() }}{{ a.lang ? ' · ' + a.lang : '' }}{{ a.title ? ' · ' + a.title : '' }}
            </button>
          </div>

          <div v-if="(play?.subtitles || []).length" class="pn-group">
            <div class="label">字幕</div>
            <button class="chip" :class="{ on: activeSub === null }" @click="selectSub(null)">关闭</button>
            <button
              v-for="sub in play.subtitles" :key="sub.id"
              class="chip" :class="{ on: activeSub && activeSub.id === sub.id }"
              @click="selectSub(sub)"
            >{{ sub.label || sub.lang || '字幕' }}</button>
          </div>

          <div v-if="play?.reason" class="pn-reason tiny">
            <AppIcon name="sparkles" :size="13" />
            <span>{{ play.reason }}</span>
          </div>
        </aside>
      </Transition>
    </div>

    <!-- ================= shortcuts hint (desktop) ================= -->
    <div v-if="!nativeControls" class="hints">
      <span><kbd>空格</kbd> 播放/暂停</span>
      <span><kbd>←</kbd><kbd>→</kbd> 快退/快进 10s</span>
      <span><kbd>F</kbd> 全屏</span>
      <span><kbd>M</kbd> 静音</span>
      <span><kbd>Esc</kbd> 返回</span>
    </div>

    <ToastHost />
  </div>
</template>

<style scoped>
.player {
  position: fixed;
  inset: 0;
  background: #000;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
/* toasts must clear the control bar, not sit inside it */
.player :deep(.toast-host) { bottom: calc(118px + env(safe-area-inset-bottom)); }
.stage { position: relative; flex: 1; min-height: 0; background: #000; display: flex; align-items: center; justify-content: center; }
video { width: 100%; height: 100%; object-fit: contain; background: #000; display: block; }

.top-bar {
  position: absolute;
  inset: 0 0 auto 0;
  display: flex;
  align-items: center;
  gap: 14px;
  padding: max(14px, env(safe-area-inset-top)) clamp(14px, 3vw, 34px) 34px;
  background: linear-gradient(to bottom, rgba(3, 4, 6, 0.86), rgba(3, 4, 6, 0));
  transition: opacity var(--t-mid), transform var(--t-mid);
  z-index: 5;
}
.tb-title { min-width: 0; }
.tb-name { font-size: var(--fs-15); font-weight: 650; text-shadow: 0 2px 12px rgba(0, 0, 0, 0.6); }
.tb-sub { display: flex; align-items: center; gap: 9px; margin-top: 4px; }

.center-state {
  position: absolute;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 14px;
  z-index: 4;
  pointer-events: none;
}
.ring {
  width: 44px; height: 44px; border-radius: 50%;
  border: 2.5px solid rgba(255, 255, 255, 0.16);
  border-top-color: var(--brand);
  animation: spin 0.9s linear infinite;
}
@keyframes spin { to { transform: rotate(360deg); } }
.cs-text { font-size: var(--fs-13); color: var(--text-2); background: rgba(0, 0, 0, 0.5); padding: 6px 14px; border-radius: var(--r-pill); }
.cs-bar {
  width: min(260px, 62vw);
  height: 6px;
  border-radius: var(--r-pill);
  background: rgba(255, 255, 255, 0.18);
  overflow: hidden;
}
.cs-bar i { display: block; height: 100%; background: var(--brand); border-radius: inherit; transition: width 0.4s ease; }
.cs-hint {
  pointer-events: auto;
  max-width: min(420px, 86vw);
  padding: 14px 16px;
  border-radius: var(--r-m);
  background: rgba(14, 16, 21, 0.94);
  border: 1px solid var(--line-2);
  box-shadow: var(--sh-4);
  text-align: center;
}
.cs-hint-t { font-size: var(--fs-12); color: var(--text-2); line-height: 1.6; }
.cs-actions { display: flex; gap: 8px; justify-content: center; flex-wrap: wrap; margin-top: 10px; }

.error-panel {
  position: relative;
  z-index: 6;
  max-width: min(520px, 88vw);
  text-align: center;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
  padding: 28px;
  border-radius: var(--r-l);
  background: rgba(14, 16, 21, 0.94);
  border: 1px solid rgba(255, 107, 107, 0.34);
  box-shadow: var(--sh-4);
  color: var(--text-2);
}
.error-panel svg { color: var(--danger); }
.ep-title { font-size: var(--fs-16); font-weight: 700; color: var(--text-1); }
.ep-text { font-size: var(--fs-13); line-height: 1.6; }
.ep-actions { display: flex; gap: 8px; flex-wrap: wrap; justify-content: center; margin-top: 6px; }

.big-play {
  position: absolute;
  z-index: 4;
  width: 84px; height: 84px;
  display: grid; place-items: center;
  border-radius: 50%;
  background: rgba(255, 255, 255, 0.94);
  color: #0a0b0f;
  padding-left: 5px;
  box-shadow: 0 20px 60px rgba(0, 0, 0, 0.55);
  transition: transform var(--t-fast), background var(--t-fast);
}
.big-play:hover { transform: scale(1.06); background: #fff; }

.controls {
  position: absolute;
  inset: auto 0 0 0;
  padding: 60px clamp(14px, 3vw, 34px) max(20px, env(safe-area-inset-bottom));
  background: linear-gradient(to top, rgba(3, 4, 6, 0.94) 10%, rgba(3, 4, 6, 0.6) 55%, rgba(3, 4, 6, 0));
  transition: opacity var(--t-mid), transform var(--t-mid);
  z-index: 5;
}
.hide-ui .controls { opacity: 0; transform: translateY(12px); pointer-events: none; }
.hide-ui .top-bar { opacity: 0; transform: translateY(-12px); pointer-events: none; }
.hide-ui { cursor: none; }

/* seek bar */
.seek { position: relative; padding: 10px 0; cursor: pointer; touch-action: none; }
.seek-track {
  position: relative;
  height: 5px;
  border-radius: var(--r-pill);
  background: rgba(255, 255, 255, 0.22);
  transition: height var(--t-fast);
}
.seek:hover .seek-track { height: 8px; }
.seek-buffer { position: absolute; inset: 0 auto 0 0; background: rgba(255, 255, 255, 0.28); border-radius: inherit; }
.seek-fill { position: absolute; inset: 0 auto 0 0; background: var(--brand); border-radius: inherit; }
.seek-knob {
  position: absolute;
  top: 50%;
  width: 14px; height: 14px;
  margin-left: -7px;
  border-radius: 50%;
  background: #fff;
  transform: translateY(-50%) scale(0);
  transition: transform var(--t-fast);
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.5);
}
.seek:hover .seek-knob { transform: translateY(-50%) scale(1); }
.seek-tip {
  position: absolute;
  bottom: 26px;
  transform: translateX(-50%);
  padding: 4px 9px;
  border-radius: var(--r-xs);
  background: rgba(12, 14, 18, 0.95);
  border: 1px solid var(--line-2);
  font-size: var(--fs-12);
  font-variant-numeric: tabular-nums;
  pointer-events: none;
}

.ctl-row { display: flex; align-items: center; gap: 4px; margin-top: 4px; }
.ctl-row .icon-btn { width: 42px; height: 42px; color: #fff; }
.ctl-row .icon-btn:hover { background: rgba(255, 255, 255, 0.14); }
.ctl-row .icon-btn.on { color: var(--brand); }
.time { display: flex; gap: 6px; font-size: var(--fs-13); margin-left: 8px; color: #fff; }
.time .dim { color: rgba(255, 255, 255, 0.55); }

.vol { display: flex; align-items: center; }
.vol-slider { position: relative; width: 0; overflow: hidden; transition: width var(--t-mid), margin var(--t-mid); display: flex; align-items: center; }
.vol:hover .vol-slider { width: 92px; margin-right: 6px; }
.vol-fill {
  position: absolute; left: 0; height: 4px; border-radius: var(--r-pill);
  background: var(--brand); pointer-events: none; max-width: 100%;
}
.vol-slider input[type="range"] {
  appearance: none;
  width: 92px; height: 4px; border-radius: var(--r-pill);
  background: rgba(255, 255, 255, 0.28);
  outline: none;
}
.vol-slider input[type="range"]::-webkit-slider-thumb {
  appearance: none;
  width: 12px; height: 12px; border-radius: 50%;
  background: #fff; border: none;
  box-shadow: 0 1px 6px rgba(0, 0, 0, 0.5);
}
.vol-slider input[type="range"]::-moz-range-thumb { width: 12px; height: 12px; border: none; border-radius: 50%; background: #fff; }

.menu-wrap { position: relative; }
.ctl-menu {
  position: absolute;
  bottom: calc(100% + 12px);
  right: 0;
  width: 268px;
  padding: 14px;
  border-radius: var(--r-m);
  background: rgba(14, 16, 21, 0.96);
  border: 1px solid var(--line-2);
  box-shadow: var(--sh-4);
  backdrop-filter: blur(20px);
  z-index: 8;
}
.cm-title { font-size: var(--fs-11); font-weight: 700; letter-spacing: 0.1em; text-transform: uppercase; color: var(--text-4); margin-bottom: 8px; }
.cm-rates { display: flex; flex-wrap: wrap; gap: 6px; }
.cm-item {
  display: flex;
  flex-direction: column;
  gap: 1px;
  width: 100%;
  padding: 9px 11px;
  border-radius: var(--r-s);
  text-align: left;
  transition: background var(--t-fast);
}
.cm-item:hover { background: rgba(255, 255, 255, 0.07); }
.cm-item.on { background: var(--brand-tint); }
.cm-label { font-size: var(--fs-13); font-weight: 600; color: var(--text-1); display: inline-flex; align-items: center; gap: 7px; }

.panel {
  position: absolute;
  inset: 0 0 0 auto;
  width: min(370px, 90vw);
  padding: max(18px, env(safe-area-inset-top)) 20px 24px;
  border-radius: 0;
  border-left: 1px solid var(--line-2);
  display: flex;
  flex-direction: column;
  gap: 18px;
  overflow-y: auto;
  z-index: 7;
}
.pn-head { display: flex; align-items: center; justify-content: space-between; }
.pn-group { display: flex; flex-direction: column; gap: 8px; align-items: flex-start; }
.pn-group .label { font-size: var(--fs-11); font-weight: 700; letter-spacing: 0.1em; text-transform: uppercase; color: var(--text-4); }
.pn-group .chip { max-width: 100%; height: auto; min-height: 34px; padding: 7px 14px; white-space: normal; text-align: left; }
.pn-reason { display: flex; gap: 8px; line-height: 1.55; margin-top: auto; padding-top: 16px; border-top: 1px solid var(--line-1); }

.slide-enter-active, .slide-leave-active { transition: transform var(--t-mid), opacity var(--t-mid); }
.slide-enter-from, .slide-leave-to { transform: translateX(24px); opacity: 0; }

.hints {
  position: absolute;
  bottom: calc(104px + env(safe-area-inset-bottom));
  right: clamp(14px, 3vw, 34px);
  display: flex;
  gap: 14px;
  font-size: var(--fs-11);
  color: rgba(255, 255, 255, 0.42);
  transition: opacity var(--t-mid);
  z-index: 3;
  pointer-events: none;
}
.hide-ui .hints { opacity: 0; }
kbd {
  display: inline-grid; place-items: center;
  min-width: 18px; height: 18px;
  padding: 0 4px; margin-right: 4px;
  border-radius: 4px;
  border: 1px solid var(--line-2);
  background: rgba(255, 255, 255, 0.06);
  font-family: var(--font-mono);
  font-size: 10px;
  color: var(--text-3);
}

@media (max-width: 860px) {
  .hints { display: none; }
  .vol-slider { display: none; }
  .panel { width: 100%; border-left: none; }
}
@media (max-width: 620px) {
  .ctl-row .icon-btn { width: 38px; height: 38px; }
  .time { font-size: var(--fs-12); margin-left: 4px; }
  .big-play { width: 66px; height: 66px; }
}
</style>
