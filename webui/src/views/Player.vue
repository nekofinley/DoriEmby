<script setup>
import { ref, onMounted, onBeforeUnmount, computed, nextTick, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, detectClient, fmtTime, fmtSize } from '../api.js'

const route = useRoute()
const router = useRouter()

const play = ref(null)
const error = ref('')
const fatal = ref('')
const videoEl = ref(null)
const hls = ref(null)
const currentAudio = ref(0)
const activeSub = ref(null)
const duration = ref(0)
const position = ref(0)
const isLoading = ref(true)

const modeClass = computed(() => `mode-${play.value?.mode || 'direct'}`)
const modeLabel = computed(() => ({
  direct: '302 直连', proxy: '代理播放', remux: 'HLS 转封装', transcode: 'HLS 转码',
}[play.value?.mode] || play.value?.mode))

const currentSize = computed(() => {
  const v = (play.value?.versions || []).find(x => x.current)
  return v?.size ? fmtSize(v.size) : ''
})

let lastSave = 0

async function loadDecision(force) {
  error.value = ''
  fatal.value = ''
  isLoading.value = true
  const params = new URLSearchParams({ client: detectClient() })
  if (force) params.set('force', force)
  if (currentAudio.value) params.set('a', String(currentAudio.value))
  try {
    play.value = await api.get(`/api/play/${route.params.fileId}?${params}`)
    await nextTick()
    attachSource()
  } catch (e) {
    fatal.value = e.message
    isLoading.value = false
  }
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
  // 原生直连 HEVC 只在本机真的能解时才尝试(OpenList 上的直接播放同理)
  const v = videoEl.value
  if (v && p.video_codec === 'hevc' && list[0].kind === 'mp4') {
    const hevcOK = v.canPlayType('video/mp4; codecs="hvc1.1.6.L93.B0"') ||
      v.canPlayType('video/mp4; codecs="hev1.1.6.L93.B0"') ||
      v.canPlayType('video/x-matroska; codecs="hvc1.1.6.L93.B0"')
    if (!hevcOK) {
      const ti = list.findIndex(c => c.mode === 'transcode')
      if (ti > 0) {
        const [t] = list.splice(ti, 1)
        list.unshift(t)
        showToast('本机不支持 HEVC 硬解,已切换云端转码')
      }
    }
  }
  return list
}

function destroyHls() {
  if (hls.value) {
    hls.value.destroy()
    hls.value = null
  }
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
    fatal.value = '所有播放方式都失败了(本机直连 + 云端转码)。可尝试切换音轨,或点"强制转码"重试。'
    return
  }
  candIdx.value = i
  const c = cands.value[i]
  if (play.value) play.value = { ...play.value, mode: c.mode, url: c.url, kind: c.kind }
  const v = videoEl.value
  if (!v) return
  destroyHls()
  v.removeAttribute('src')
  activeSub.value = null

  if (c.kind === 'hls') {
    const canNative = v.canPlayType('application/vnd.apple.mpegurl')
    if (canNative) {
      // Safari / iOS: AVPlayer 原生 HLS,HEVC 可硬解
      v.src = c.url
    } else {
      if (!HlsLib) HlsLib = (await import('hls.js')).default
      const Hls = HlsLib
      if (Hls.isSupported()) {
        // 分片是服务端实时生成的(转码≈实时速度),超时给足余量
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

  v.load()
  v.play().catch(() => { /* autoplay might be blocked; user presses play */ })
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
  showToast(`${modeLabel.value}失败,切换下一种播放方式…`)
  startCand(candIdx.value + 1)
}

function onLoadedMetadata() {
  isLoading.value = false
  const v = videoEl.value
  duration.value = v.duration || play.value?.duration || 0
  // resume
  api.get(`/api/progress?file_id=${route.params.fileId}`).then(p => {
    if (p && p.position > 30 && duration.value > 0 && p.position < duration.value * 0.95) {
      v.currentTime = p.position
      showToast(`已从 ${fmtTime(p.position)} 继续播放`)
    }
  }).catch(() => {})
}

function onErr() {
  isLoading.value = false
  if (!play.value) return
  // 候选链里还有备选 → 自动切换,不要直接报死
  if (candIdx.value < cands.value.length - 1) {
    showToast('该方式播放失败,自动切换…')
    startCand(candIdx.value + 1)
    return
  }
  error.value = '播放失败(解码或网络错误)'
}

let toastTimer = null
const toast = ref('')
function showToast(msg) {
  toast.value = msg
  clearTimeout(toastTimer)
  toastTimer = setTimeout(() => { toast.value = '' }, 2500)
}

function onTimeUpdate() {
  const v = videoEl.value
  if (!v) return
  position.value = v.currentTime
  const now = Date.now()
  if (now - lastSave > 10000) {
    lastSave = now
    saveProgress()
  }
}

function saveProgress() {
  const v = videoEl.value
  if (!v || !v.duration) return
  api.post('/api/progress', {
    file_id: Number(route.params.fileId),
    position: v.currentTime,
    duration: v.duration,
  }).catch(() => {})
}

function selectVersion(v) {
  if (v.current) return
  saveProgress()
  showToast(`切换到 ${v.label || '另一版本'}`)
  router.replace(`/play/${v.id}`)
}

// 同一集的其它版本(bdrip/webrip…):路由参数变化时重新走决策管线
watch(() => route.params.fileId, (nv, ov) => {
  if (nv && ov && nv !== ov) loadDecision()
})

function selectAudio(i) {
  currentAudio.value = i
  loadDecision(play.value.mode === 'transcode' ? 'transcode' : undefined)
}

function selectSub(sub) {
  const v = videoEl.value
  if (!v) return
  for (const t of v.textTracks) t.mode = 'disabled'
  if (sub === null || activeSub.value === sub) {
    activeSub.value = null
    return
  }
  activeSub.value = sub
  // match by src
  const tracks = v.querySelectorAll('track')
  tracks.forEach((t, i) => {
    if (t.src.includes(`/${sub.id}.vtt`)) {
      if (v.textTracks[i]) v.textTracks[i].mode = 'showing'
    }
  })
}

function toggleFullscreen() {
  const v = videoEl.value
  if (!v) return
  if (document.fullscreenElement) {
    document.exitFullscreen?.()
  } else if (v.requestFullscreen) {
    v.requestFullscreen()
  } else if (v.webkitEnterFullscreen) {
    v.webkitEnterFullscreen() // iPhone 原生全屏
  }
}

function goBack() {
  saveProgress()
  router.back()
}

onMounted(() => {
  loadDecision()
  window.addEventListener('beforeunload', saveProgress)
})
onBeforeUnmount(() => {
  saveProgress()
  destroyHls()
  window.removeEventListener('beforeunload', saveProgress)
})
</script>

<template>
  <div class="player-page">
    <!-- 视频窗口:16:9,圆角,进度条/音量用原生控件,不再被设置按钮遮挡 -->
    <div class="stage">
      <div class="video-box">
        <video
          ref="videoEl"
          controls
          playsinline
          webkit-playsinline
          preload="auto"
          @loadedmetadata="onLoadedMetadata"
          @error="onErr"
          @timeupdate="onTimeUpdate"
          @pause="saveProgress"
        >
          <track v-for="sub in play?.subtitles || []" :key="sub.id" kind="subtitles"
            :src="sub.url" :label="sub.label || sub.lang || '字幕'" />
        </video>
        <div v-if="isLoading" class="center-msg">加载中,正在准备播放…</div>
        <div v-if="error" class="center-msg error-box">
          <div>{{ error }}</div>
          <div class="fallback-btns">
            <button class="btn" @click="loadDecision('transcode')">强制转码</button>
            <button class="btn" @click="loadDecision('remux')">强制转封装</button>
            <button class="btn" @click="loadDecision()">重试自动</button>
          </div>
        </div>
        <div v-if="fatal" class="center-msg error-box">{{ fatal }}</div>
      </div>
    </div>

    <!-- 视频外的信息与设置面板 -->
    <div class="panel" v-if="play">
      <div class="panel-head">
        <div class="panel-title">
          <div class="p-name">{{ play.title }}</div>
          <div class="p-sub">
            <span class="badge" :class="modeClass">{{ modeLabel }}</span>
            <span v-if="play.resolution" class="tiny">{{ play.resolution.replace('x', '×') }}</span>
            <span v-if="play.video_codec" class="tiny">{{ play.video_codec.toUpperCase() }}<template v-if="play.audio_codec"> / {{ play.audio_codec.toUpperCase() }}</template></span>
            <span v-if="currentSize" class="tiny">{{ currentSize }}</span>
          </div>
        </div>
        <div class="p-actions">
          <button class="btn ghost" @click="toggleFullscreen">⛶ 全屏</button>
          <button class="btn ghost" @click="goBack">← 返回</button>
        </div>
      </div>

      <div class="ctl-groups">
        <div v-if="(play.versions || []).length" class="ctl-group">
          <span class="ctl-label">版本</span>
          <button v-for="v in play.versions" :key="v.id" class="chip"
            :class="{ on: v.current }" @click="selectVersion(v)">
            {{ v.label || '默认版本' }}
          </button>
        </div>
        <div v-if="(play.audio_tracks || []).length > 1" class="ctl-group">
          <span class="ctl-label">音轨</span>
          <button v-for="a in play.audio_tracks" :key="a.ordinal" class="chip"
            :class="{ on: a.ordinal === currentAudio }"
            @click="selectAudio(a.ordinal)">
            {{ a.codec.toUpperCase() }}{{ a.lang ? ' · ' + a.lang : '' }}{{ a.title ? ' · ' + a.title : '' }}
          </button>
        </div>
        <div v-if="(play.subtitles || []).length" class="ctl-group">
          <span class="ctl-label">字幕</span>
          <button class="chip" :class="{ on: activeSub === null }" @click="selectSub(null)">关闭</button>
          <button v-for="sub in play.subtitles" :key="sub.id" class="chip"
            :class="{ on: activeSub && activeSub.id === sub.id }"
            @click="selectSub(sub)">
            {{ sub.label || sub.lang || '字幕' }}
          </button>
        </div>
      </div>

      <div v-if="play.reason" class="tiny muted">{{ play.reason }}</div>
    </div>

    <div v-if="toast" class="toast">{{ toast }}</div>
  </div>
</template>

<style scoped>
.player-page {
  max-width: 1120px;
  margin: 0 auto;
  padding: 16px 18px 40px;
}
.stage { margin-bottom: 14px; }
.video-box {
  position: relative;
  aspect-ratio: 16 / 9;
  background: #000;
  border-radius: var(--radius-l);
  overflow: hidden;
  border: 1px solid var(--border-1);
  box-shadow: var(--shadow-2);
}
video { width: 100%; height: 100%; object-fit: contain; display: block; background: #000; }

.panel {
  background: var(--bg-1);
  border: 1px solid var(--border-1);
  border-radius: var(--radius-l);
  padding: 16px 20px;
}
.panel-head { display: flex; align-items: flex-start; gap: 16px; }
.panel-title { flex: 1; min-width: 0; }
.p-name { font-weight: 700; font-size: 17px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.p-sub { display: flex; align-items: center; gap: 10px; margin-top: 6px; flex-wrap: wrap; }
.p-actions { display: flex; gap: 8px; flex-shrink: 0; }

.ctl-groups { display: flex; flex-direction: column; gap: 10px; margin-top: 14px; }
.ctl-group { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.ctl-label {
  color: var(--text-4);
  font-size: 12px;
  width: 34px;
  flex-shrink: 0;
}
.chip {
  background: var(--bg-2);
  border: 1px solid var(--border-1);
  color: var(--text-2);
  border-radius: 999px;
  padding: 5px 14px;
  font-size: 12.5px;
  cursor: pointer;
  transition: border-color 0.15s, background 0.15s;
}
.chip:hover { border-color: var(--border-2); }
.chip.on { background: var(--accent); color: #fff; border-color: transparent; }

.center-msg {
  position: absolute;
  top: 50%;
  left: 50%;
  transform: translate(-50%, -50%);
  background: rgba(10, 10, 12, 0.9);
  border: 1px solid var(--border-1);
  padding: 18px 26px;
  border-radius: var(--radius-m);
  text-align: center;
  max-width: 82%;
}
.error-box { border-color: rgba(235, 87, 87, 0.4); }
.fallback-btns { display: flex; gap: 8px; margin-top: 14px; justify-content: center; }
.toast {
  position: fixed;
  bottom: 28px;
  left: 50%;
  transform: translateX(-50%);
  background: rgba(20, 20, 24, 0.95);
  border: 1px solid var(--border-2);
  padding: 8px 18px;
  border-radius: 999px;
  font-size: 13px;
  z-index: 600;
}
@media (max-width: 720px) {
  .player-page { padding: 10px 10px 30px; }
  .panel-head { flex-direction: column; }
  .p-actions { align-self: flex-end; }
}
</style>
