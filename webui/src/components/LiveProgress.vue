<script setup>
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import AppIcon from './AppIcon.vue'

// A live progress readout for long jobs: a big bar for the whole job, a small
// bar for the step inside it, counters, a rate estimate, a sparkline of recent
// throughput, and a rolling log of what has just been found.
//
// The shape is borrowed from terminal process viewers — btop, htop, a build log —
// because that is what these jobs are: minutes of silence punctuated by lines
// going past. The point is that the screen keeps moving while nothing else tells
// you it is alive.
const props = defineProps({
  // The job's own state object, as the API returns it.
  state: { type: Object, required: true },
  // log === false for jobs whose feed is rendered elsewhere.
  showLog: { type: Boolean, default: true },
})

const emit = defineEmits(['stop'])

const KIND_ICON = {
  dir: 'folder', file: 'film', show: 'tv', season: 'layers', episode: 'playCircle',
  sub: 'subtitles', meta: 'sparkles', warn: 'alert', info: 'scan',
}
function iconFor(kind) { return KIND_ICON[kind] || 'scan' }

function fmtClock(ts) {
  if (!ts) return '--:--:--'
  return new Date(ts * 1000).toLocaleTimeString('zh-CN', { hour12: false })
}
// 1234 -> "1,234"; the counters tick fast enough that separators help reading.
function num(n) { return (n ?? 0).toLocaleString('en-US') }

// ---------- rates ----------
// The bar cannot be honest without a rate, and a rate cannot be read off one
// sample. Keep a short window of (time, done) and measure across it.
const RATE_WINDOW = 12
const hist = ref([])
let lastTotal = null

watch(
  () => [props.state.done, props.state.dirs_done, props.state.probe_done, props.state.phase],
  () => {
    const now = Date.now() / 1000
    const st = props.state
    // A phase change moves the counter to a different scale; start the window over
    // rather than reporting a rate that mixes two of them.
    if (hist.value.length && st.phase !== hist.value[hist.value.length - 1].phase) hist.value = []
    const done = (st.done || 0) + (st.dirs_done || 0) + (st.probe_done || 0)
    hist.value.push({ t: now, done, phase: st.phase })
    if (hist.value.length > RATE_WINDOW) hist.value.shift()
  },
  { immediate: true },
)

const rate = computed(() => {
  const h = hist.value
  if (h.length < 2) return 0
  const dt = h[h.length - 1].t - h[0].t
  const dd = h[h.length - 1].done - h[0].done
  if (dt < 0.5 || dd <= 0) return 0
  return dd / dt
})
const rateText = computed(() => (rate.value >= 1 ? `${rate.value.toFixed(1)} 项/秒` : ''))

const etaText = computed(() => {
  const st = props.state
  if (!st.running || rate.value <= 0) return ''
  // Only the level that is actually counting up can be extrapolated; an unknown
  // total means no honest estimate exists.
  const todo = levels.value.big
  if (todo.indet || !todo.total) return ''
  const left = todo.total - (todo.value || 0)
  if (left <= 0) return ''
  const secs = left / rate.value
  if (!isFinite(secs) || secs <= 0) return ''
  if (secs < 60) return `约剩 ${Math.ceil(secs)} 秒`
  if (secs < 3600) return `约剩 ${Math.round(secs / 60)} 分钟`
  return `约剩 ${(secs / 3600).toFixed(1)} 小时`
})

// ---------- sparkline ----------
// The last few rates, newest on the right. Widths are relative to the busiest
// sample so the shape reads even when the absolute rate is small.
const SPARK = 26
const spark = ref([])
let lastSparkAt = 0
watch(rate, (r) => {
  const now = Date.now() / 1000
  // One column per ~0.4s at most: the poll is every 2s, so a column per poll.
  if (now - lastSparkAt < 0.35) return
  lastSparkAt = now
  spark.value.push(r)
  if (spark.value.length > SPARK) spark.value.shift()
})
const sparkScale = computed(() => Math.max(1, ...spark.value))
const sparkBars = computed(() =>
  spark.value.map((v) => ({ h: Math.max(2, Math.round((v / sparkScale.value) * 100)), v })),
)

// ---------- the two levels ----------
// Which counters mean "how far along" depends on the phase, so each phase names
// its own pair of levels rather than the template guessing from field presence.
const levels = computed(() => {
  const st = props.state
  const p = st.phase

  if (p === 'walk') {
    return {
      big: { label: '遍历目录', value: st.dirs_done || 0, total: 0, indet: true, note: '总目录数未知,按已发现文件推进' },
      small: { label: '已发现视频', value: st.files_found || 0, total: 0, indet: true },
    }
  }
  if (p === 'organize') {
    return {
      big: { label: '整理入库', value: st.done || 0, total: st.total || 0 },
      small: { label: '已遍历目录', value: st.dirs_done || 0, total: 0, indet: true },
    }
  }
  if (p === 'identify') {
    return {
      big: { label: '刮削与识别', value: st.done || 0, total: st.total || 0 },
      small: { label: '已入库文件', value: st.files_found || 0, total: 0, indet: true },
    }
  }
  if (p === 'listing') {
    return {
      big: { label: '列出目录', value: st.dir_count || 0, total: 0, indet: true, note: '正在遍历媒体库各目录' },
      small: { label: '目录数', value: st.dir_count || 0, total: 0, indet: true },
    }
  }
  if (p === 'probing') {
    // The listing stage is over by now, so the big bar can be exact.
    return {
      big: { label: '确认内嵌字幕', value: st.probe_done || 0, total: st.probe_total || 0 },
      small: {
        label: '已核对文件',
        value: (st.with_subs || 0) + (st.missing || 0),
        total: (st.total || 0) + (st.missing || 0),
      },
    }
  }
  // done / error / idle: no moving parts left, show the outcome.
  return {
    big: { label: '已完成', value: 1, total: 1 },
    small: { label: '文件总数', value: (st.total || 0) + (st.unverified || 0), total: 0, indet: true },
  }
})

function pctOf(lv) {
  if (!lv || lv.indet || !lv.total) return null
  return Math.min(100, Math.round((lv.value / lv.total) * 100))
}

const big = computed(() => {
  const lv = levels.value.big
  return { ...lv, pct: pctOf(lv) }
})
const small = computed(() => {
  const lv = levels.value.small
  return { ...lv, pct: pctOf(lv) }
})

// ETA is computed against the big level inside etaText; nothing else needs the
// totals, so they are not exposed separately.

const stalled = computed(() => {
  if (!props.state.running) return false
  const h = hist.value
  if (h.length < 3) return false
  return h[h.length - 1].done === h[0].done
})

// ---------- rolling log ----------
// The feed arrives as the last N events on every poll, so keep the ones already
// shown: appending only what is new makes the list scroll instead of flicker, and
// deduping by seq means a repeated poll cannot double a line.
const LOG_KEEP = 10
const log = ref([])
watch(
  () => props.state.events,
  (evs) => {
    if (!evs || !evs.length) return
    const have = new Set(log.value.map((e) => e.seq))
    const fresh = evs.filter((e) => !have.has(e.seq))
    if (!fresh.length) return
    log.value = log.value.concat(fresh).slice(-LOG_KEEP)
  },
  { immediate: true },
)

const logBox = ref(null)
// Keep the newest line in view as it arrives.
watch(
  () => log.value.length,
  async () => {
    await Promise.resolve()
    const el = logBox.value
    if (el) el.scrollTop = el.scrollHeight
  },
)

onBeforeUnmount(() => { hist.value = []; spark.value = [] })
</script>

<template>
  <div class="live">
    <!-- 大进度条:整个任务 -->
    <div class="lv-row">
      <span class="lv-label">{{ big.label }}</span>
      <div class="lv-bar big" :class="{ indet: big.pct === null }">
        <i :style="big.pct !== null ? { width: big.pct + '%' } : {}"></i>
        <b v-if="big.pct === null" class="lv-shimmer"></b>
      </div>
      <span v-if="big.pct !== null" class="lv-num">{{ big.pct }}<small>%</small></span>
      <span v-else class="lv-num dim">—</span>
    </div>

    <!-- 小进度条:当前这一步 -->
    <div class="lv-row sub">
      <span class="lv-label">{{ small.label }}</span>
      <div class="lv-bar small" :class="{ indet: small.pct === null }">
        <i :style="small.pct !== null ? { width: small.pct + '%' } : {}"></i>
        <b v-if="small.pct === null" class="lv-shimmer"></b>
      </div>
      <span v-if="small.pct !== null" class="lv-num sm">{{ small.pct }}<small>%</small></span>
      <span v-else class="lv-num sm dim">—</span>
    </div>

    <!-- 计数 / 速率 / 最近吞吐 -->
    <div class="lv-stats">
      <span class="lv-stat"><b>{{ num(big.value) }}</b><em v-if="big.total">/{{ num(big.total) }}</em> {{ big.label }}</span>
      <span class="lv-stat"><b>{{ num(small.value) }}</b><em v-if="small.total">/{{ num(small.total) }}</em> {{ small.label }}</span>
      <span v-if="rateText" class="lv-stat accent"><b>{{ rateText }}</b></span>
      <span v-if="etaText" class="lv-stat">{{ etaText }}</span>
      <span v-if="stalled" class="lv-stat warn">暂无进展</span>
      <div v-if="sparkBars.length > 1" class="lv-spark" :title="'最近吞吐(每 2 秒采样一次)'">
        <i v-for="(b, i) in sparkBars" :key="i" :style="{ height: b.h + '%' }" :class="{ hot: i === sparkBars.length - 1 }"></i>
      </div>
    </div>

    <!-- 当前正在处理的路径 + 停止 -->
    <div v-if="state.current" class="lv-cur tiny">
      <AppIcon name="chevronRight" :size="12" />
      <span class="truncate">{{ state.current }}</span>
      <button v-if="state.running" class="lv-stop" @click="emit('stop')">
        <AppIcon name="close" :size="12" /> 停止
      </button>
    </div>
    <div v-else-if="state.running" class="lv-cur tiny">
      <button class="lv-stop" @click="emit('stop')">
        <AppIcon name="close" :size="12" /> 停止
      </button>
    </div>

    <!-- 滚动日志 -->
    <TransitionGroup v-if="showLog && log.length" name="ev" tag="div" class="lv-log" ref="logBox">
      <div v-for="e in log" :key="e.seq" class="ev" :class="'ev-' + e.kind">
        <span class="ev-t">{{ fmtClock(e.at) }}</span>
        <AppIcon class="ev-i" :name="iconFor(e.kind)" :size="11" />
        <span class="ev-x truncate">{{ e.text }}</span>
      </div>
    </TransitionGroup>
  </div>
</template>

<style scoped>
.live { margin-top: 14px; }

/* ---- 两级进度条 ---- */
.lv-row { display: flex; align-items: center; gap: 10px; }
.lv-row + .lv-row { margin-top: 6px; }
.lv-label {
  flex: none; width: 84px; text-align: right;
  font-size: var(--fs-12); color: var(--text-3); white-space: nowrap;
}
.lv-bar {
  position: relative; flex: 1; min-width: 0;
  background: var(--bg-3); border-radius: var(--r-pill); overflow: hidden;
}
.lv-bar.big { height: 12px; }
.lv-bar.small { height: 5px; }
.lv-bar > i {
  display: block; height: 100%; border-radius: inherit;
  background: linear-gradient(90deg, var(--brand-2), var(--brand));
  transition: width var(--t-mid) var(--ease-out);
}
.lv-bar.small > i { background: linear-gradient(90deg, var(--brand-tint), var(--brand-2)); }
.lv-bar.indet > i { display: none; }
/* 总数未知时用扫光,而不是假装有百分比 */
.lv-shimmer {
  position: absolute; inset: 0; width: 34%;
  background: linear-gradient(90deg, transparent, var(--brand), transparent);
  opacity: 0.75; animation: lv-sweep 1.5s var(--ease) infinite;
}
@keyframes lv-sweep {
  from { transform: translateX(-110%); }
  to { transform: translateX(320%); }
}
.lv-num {
  flex: none; width: 46px; text-align: right;
  font-family: var(--font-mono); font-size: var(--fs-13);
  font-variant-numeric: tabular-nums; color: var(--text-1);
}
.lv-num small { font-size: 9px; color: var(--text-4); }
.lv-num.sm { font-size: var(--fs-12); color: var(--text-3); }
.lv-num.dim { color: var(--text-4); }

/* ---- 计数 / 速率 / 吞吐 ---- */
.lv-stats {
  display: flex; align-items: center; gap: 14px; flex-wrap: wrap;
  margin-top: 9px; padding-left: 94px;
  font-size: var(--fs-12); color: var(--text-3);
}
.lv-stat b {
  font-family: var(--font-mono); font-variant-numeric: tabular-nums;
  color: var(--text-1); font-weight: 650;
}
.lv-stat em { font-style: normal; color: var(--text-4); }
.lv-stat.accent b { color: var(--brand); }
.lv-stat.warn { color: var(--warn); }
.lv-spark {
  display: flex; align-items: flex-end; gap: 1px;
  height: 14px; width: 78px; margin-left: auto;
}
.lv-spark > i {
  flex: 1; min-width: 1px; border-radius: 1px;
  background: var(--brand-tint);
}
.lv-spark > i.hot { background: var(--brand); }

/* ---- 当前路径 / 停止 ---- */
.lv-cur {
  display: flex; align-items: center; gap: 6px;
  margin-top: 8px; padding-left: 94px; color: var(--text-4);
}
.lv-cur svg { flex: none; }
.lv-stop {
  flex: none; margin-left: auto;
  display: inline-flex; align-items: center; gap: 4px;
  height: 22px; padding: 0 9px; cursor: pointer;
  border: 1px solid var(--line-2); border-radius: var(--r-pill);
  background: var(--btn-bg); color: var(--text-2); font-size: var(--fs-11);
}
.lv-stop:hover { border-color: var(--danger); color: var(--danger); }

/* ---- 滚动日志 ---- */
.lv-log {
  margin-top: 10px; margin-left: 94px;
  max-height: 132px; overflow-y: auto; overflow-x: hidden;
  border: 1px solid var(--line-1); border-radius: var(--r-s);
  background: var(--bg-0);
  scrollbar-width: thin;
}
.ev {
  display: flex; align-items: center; gap: 7px;
  padding: 3px 9px; font-size: var(--fs-11);
  font-family: var(--font-mono);
  border-bottom: 1px solid var(--line-1);
}
.ev:last-child { border-bottom: 0; }
.ev-t { flex: none; color: var(--text-4); font-variant-numeric: tabular-nums; }
.ev-i { flex: none; color: var(--text-4); }
.ev-x { min-width: 0; color: var(--text-2); }
.ev-sub .ev-i, .ev-sub .ev-x { color: var(--brand); }
.ev-warn .ev-i, .ev-warn .ev-x { color: var(--warn); }
.ev-show .ev-i, .ev-season .ev-i { color: var(--ok); }
.ev-episode .ev-i { color: var(--info); }
.ev-meta .ev-i { color: var(--brand-2); }

/* 新行淡入,旧的留在原地:滚动而不是闪烁 */
.ev-enter-active { transition: opacity var(--t-mid) var(--ease-out), transform var(--t-mid) var(--ease-out); }
.ev-enter-from { opacity: 0; transform: translateY(5px); }
.ev-leave-active { display: none; }

@media (max-width: 640px) {
  .lv-label { width: 62px; font-size: var(--fs-11); }
  .lv-stats, .lv-cur { padding-left: 0; }
  .lv-log { margin-left: 0; }
  .lv-spark { display: none; }
}
@media (prefers-reduced-motion: reduce) {
  .lv-shimmer { animation: none; width: 100%; opacity: 0.25; }
  .lv-bar > i { transition: none; }
  .ev-enter-active { transition: none; }
}
</style>
