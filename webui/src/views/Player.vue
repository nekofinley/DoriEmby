<script setup>
import { ref, onMounted, onBeforeUnmount, computed, nextTick } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, detectClient, fmtTime } from '../api.js'

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
const showOverlay = ref(true)
const overlayTimer = ref(null)
const isLoading = ref(true)

const modeClass = computed(() => `mode-${play.value?.mode || 'direct'}`)
const modeLabel = computed(() => ({
  direct: '302 直连', proxy: '代理播放', remux: 'HLS 转封装', transcode: 'HLS 转码',
}[play.value?.mode] || play.value?.mode))

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

function destroyHls() {
  if (hls.value) {
    hls.value.destroy()
    hls.value = null
  }
}

async function attachSource() {
  const v = videoEl.value
  if (!v || !play.value) return
  destroyHls()
  v.removeAttribute('src')
  activeSub.value = null

  if (play.value.kind === 'hls') {
    const canNative = v.canPlayType('application/vnd.apple.mpegurl')
    if (canNative) {
      // Safari / iOS: AVPlayer handles HEVC-HLS natively with hardware decode
      v.src = play.value.url
    } else {
      const mod = await import('hls.js')
      const Hls = mod.default
      if (Hls.isSupported()) {
        hls.value = new Hls({ maxBufferLength: 30 })
        hls.value.loadSource(play.value.url)
        hls.value.attachMedia(v)
        hls.value.on(Hls.Events.ERROR, (_, data) => {
          if (data.fatal) {
            error.value = `HLS 播放出错: ${data.details}`
          }
        })
      } else {
        fatal.value = '当前浏览器不支持 HLS 播放'
        isLoading.value = false
        return
      }
    }
  } else {
    v.src = play.value.url
  }

  v.load()
  v.play().catch(() => { /* autoplay might be blocked; user presses play */ })
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

function nudgeOverlay() {
  showOverlay.value = true
  clearTimeout(overlayTimer)
  overlayTimer = setTimeout(() => { showOverlay.value = false }, 3500)
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
  clearTimeout(overlayTimer)
  window.removeEventListener('beforeunload', saveProgress)
})
</script>

<template>
  <div class="player-page" @mousemove="nudgeOverlay" @touchstart="nudgeOverlay">
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

    <div class="overlay" :class="{ hidden: !showOverlay }">
      <div class="ov-top">
        <button class="btn ghost ov-btn" @click="goBack">← 返回</button>
        <div class="ov-title">
          <div class="ov-name">{{ play?.title }}</div>
          <div class="ov-sub">
            <span class="badge" :class="modeClass">{{ modeLabel }}</span>
            <span class="tiny">{{ play?.reason }}</span>
          </div>
        </div>
      </div>
      <div class="ov-bottom" v-if="play">
        <div v-if="(play.audio_tracks || []).length > 1" class="ov-group">
          <span class="tiny">音轨</span>
          <button v-for="a in play.audio_tracks" :key="a.ordinal" class="chip"
            :class="{ on: a.ordinal === currentAudio }"
            @click="selectAudio(a.ordinal)">
            {{ a.codec.toUpperCase() }}{{ a.lang ? ' · ' + a.lang : '' }}{{ a.title ? ' · ' + a.title : '' }}
          </button>
        </div>
        <div v-if="(play.subtitles || []).length" class="ov-group">
          <span class="tiny">字幕</span>
          <button class="chip" :class="{ on: activeSub === null }" @click="selectSub(null)">关闭</button>
          <button v-for="sub in play.subtitles" :key="sub.id" class="chip"
            :class="{ on: activeSub && activeSub.id === sub.id }"
            @click="selectSub(sub)">
            {{ sub.label || sub.lang || '字幕' }}
          </button>
        </div>
      </div>
    </div>

    <div v-if="isLoading" class="center-msg">加载中,正在探测媒体信息…</div>
    <div v-if="error" class="center-msg error-box">
      <div>{{ error }}</div>
      <div class="fallback-btns">
        <button class="btn" @click="loadDecision('remux')">强制转封装</button>
        <button class="btn" @click="loadDecision('transcode')">强制转码</button>
        <button class="btn" @click="loadDecision()">重试自动</button>
      </div>
    </div>
    <div v-if="fatal" class="center-msg error-box">{{ fatal }}</div>
    <div v-if="toast" class="toast">{{ toast }}</div>
  </div>
</template>

<style scoped>
.player-page {
  position: fixed;
  inset: 0;
  background: #000;
  z-index: 500;
}
video {
  width: 100%;
  height: 100%;
  object-fit: contain;
  background: #000;
}
.overlay {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  padding: 16px;
  pointer-events: none;
  transition: opacity 0.3s;
  background: linear-gradient(to bottom, rgba(0,0,0,0.55), transparent 22%, transparent 75%, rgba(0,0,0,0.55));
}
.overlay.hidden { opacity: 0; }
.ov-top { display: flex; gap: 12px; align-items: flex-start; pointer-events: auto; }
.ov-btn { background: rgba(0,0,0,0.4); }
.ov-name { font-weight: 700; font-size: 16px; text-shadow: 0 1px 3px rgba(0,0,0,0.8); }
.ov-sub { display: flex; align-items: center; gap: 8px; margin-top: 4px; }
.ov-bottom { display: flex; gap: 26px; flex-wrap: wrap; pointer-events: auto; }
.ov-group { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; }
.chip {
  background: rgba(0,0,0,0.45);
  border: 1px solid rgba(255,255,255,0.18);
  color: var(--text-2);
  border-radius: 999px;
  padding: 4px 12px;
  font-size: 12px;
}
.chip.on { background: var(--accent); color: #fff; border-color: transparent; }
.center-msg {
  position: absolute;
  top: 50%;
  left: 50%;
  transform: translate(-50%, -50%);
  background: rgba(10,10,12,0.9);
  border: 1px solid var(--border-1);
  padding: 18px 26px;
  border-radius: var(--radius-m);
  text-align: center;
  max-width: 82vw;
}
.error-box { border-color: rgba(235,87,87,0.4); }
.fallback-btns { display: flex; gap: 8px; margin-top: 14px; justify-content: center; }
</style>
