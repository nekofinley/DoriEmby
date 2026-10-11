<script setup>
import { ref, computed, onMounted, onBeforeUnmount, watch } from 'vue'
import AppIcon from './AppIcon.vue'
import SmartImage from './SmartImage.vue'
import { imgUrl, artIf } from '../api.js'

/**
 * Full-bleed cinematic billboard: rotating featured titles with layered scrims,
 * a slow ken-burns drift and quiet dot navigation. Modelled on the streaming
 * service hero (Netflix / Disney+): one decision per screen — play, or read more.
 */
const props = defineProps({
  items: { type: Array, default: () => [] },
  interval: { type: Number, default: 9000 },
})

const index = ref(0)
const paused = ref(false)
let timer = null

const current = computed(() => props.items[index.value] || null)
const hasMany = computed(() => props.items.length > 1)

function start() {
  stop()
  if (!hasMany.value) return
  timer = setInterval(() => { if (!paused.value) next() }, props.interval)
}
function stop() { if (timer) clearInterval(timer); timer = null }
function next() { index.value = (index.value + 1) % props.items.length }
function select(i) { index.value = i; start() }

watch(() => props.items.length, start)

onMounted(() => {
  start()
  document.addEventListener('visibilitychange', onVis)
})
onBeforeUnmount(() => { stop(); document.removeEventListener('visibilitychange', onVis) })
function onVis() { document.hidden ? stop() : start() }

function genresOf(it) {
  try {
    const g = JSON.parse(it.genres || '[]')
    return Array.isArray(g) ? g.slice(0, 3) : []
  } catch { return [] }
}
</script>

<template>
  <section
    v-if="current" class="hero"
    @mouseenter="paused = true" @mouseleave="paused = false"
  >
    <div class="stage">
      <div
        v-for="(it, i) in items" :key="it.id"
        class="layer" :class="{ on: i === index }"
      >
        <SmartImage
          class="bg" :src="artIf(it, 'backdrop')" :seed="it.name" :alt="it.name"
          :eager="i === 0"
        />
      </div>
      <div class="scrim bottom"></div>
      <div class="scrim left"></div>
      <div class="glow"></div>
    </div>

    <div class="content page">
      <Transition name="rise" mode="out-in">
        <div :key="current.id" class="copy">
          <div class="eyebrow-row">
            <span class="eyebrow">DoriyaMedia 精选</span>
            <span class="dot"></span>
            <span class="tiny">刚刚更新</span>
          </div>
          <h1 class="display-1 title">{{ current.name }}</h1>
          <div class="meta">
            <span v-if="current.rating" class="badge rating">
              <AppIcon name="star" :size="11" filled />{{ Number(current.rating).toFixed(1) }}
            </span>
            <span v-if="current.year" class="badge hd">{{ current.year }}</span>
            <span class="badge hd">{{ current.type === 'show' ? '剧集' : '电影' }}</span>
            <span v-if="current.original_title && current.original_title !== current.name" class="badge plain">
              {{ current.original_title }}
            </span>
            <span v-for="g in genresOf(current)" :key="g" class="badge plain">{{ g }}</span>
          </div>
          <p v-if="current.overview" class="ov clamp-3">{{ current.overview }}</p>
          <div class="actions">
            <router-link :to="`/item/${current.id}?play=1`" class="btn play lg">
              <AppIcon name="play" :size="17" filled /> 播放
            </router-link>
            <router-link :to="`/item/${current.id}`" class="btn glass lg">
              <AppIcon name="info" :size="17" /> 详情
            </router-link>
          </div>
        </div>
      </Transition>

      <div v-if="hasMany" class="dots">
        <button
          v-for="(it, i) in items" :key="it.id"
          class="step" :class="{ on: i === index }"
          :aria-label="it.name" @click="select(i)"
        >
          <i></i>
        </button>
      </div>
    </div>
  </section>
</template>

<style scoped>
.hero {
  position: relative;
  min-height: min(78vh, 760px);
  display: flex;
  align-items: flex-end;
  margin-bottom: clamp(20px, 3vw, 40px);
  isolation: isolate;
}
.stage { position: absolute; inset: 0; overflow: hidden; background: var(--bg-1); }
.layer {
  position: absolute;
  inset: 0;
  opacity: 0;
  transition: opacity 1200ms var(--ease-out);
  will-change: opacity;
}
.layer.on { opacity: 1; }
.bg { width: 100%; height: 100%; }
.layer :deep(.smart-img) { width: 100%; height: 100%; }
.layer :deep(img), .layer :deep(.fallback) {
  animation: kenburns 26s ease-in-out infinite alternate;
  transform-origin: 62% 42%;
}
@keyframes kenburns {
  from { transform: scale(1.02) translate3d(0, 0, 0); }
  to { transform: scale(1.11) translate3d(-1.5%, -1.5%, 0); }
}
.scrim { position: absolute; inset: 0; pointer-events: none; }
.scrim.bottom {
  background: linear-gradient(to top, var(--bg-0) 2%, rgba(7, 8, 11, 0.72) 26%, rgba(7, 8, 11, 0.1) 62%, transparent 100%);
}
.scrim.left {
  background: linear-gradient(100deg, rgba(5, 6, 9, 0.94) 6%, rgba(5, 6, 9, 0.6) 34%, rgba(5, 6, 9, 0.05) 66%, transparent 100%);
}
.glow {
  position: absolute; inset: 0;
  background: radial-gradient(60% 55% at 18% 78%, rgba(240, 169, 46, 0.13), transparent 70%);
  mix-blend-mode: screen;
}

.content {
  position: relative;
  width: 100%;
  padding-bottom: clamp(28px, 5vw, 64px);
  padding-top: calc(var(--header-h) + 40px);
}
.copy { max-width: min(660px, 62vw); }
.eyebrow-row { display: flex; align-items: center; gap: 10px; margin-bottom: 14px; }
.eyebrow-row .dot { width: 4px; height: 4px; border-radius: 50%; background: var(--text-4); }
.title {
  margin: 0 0 16px;
  text-shadow: 0 6px 40px rgba(0, 0, 0, 0.6);
}
.meta { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; margin-bottom: 16px; }
.ov {
  color: var(--text-2);
  font-size: var(--fs-15);
  line-height: 1.62;
  margin-bottom: var(--sp-5);
  text-shadow: 0 2px 20px rgba(0, 0, 0, 0.55);
  max-width: 56ch;
}
.actions { display: flex; gap: 12px; flex-wrap: wrap; }
.dots { display: flex; align-items: center; gap: 6px; margin-top: var(--sp-5); }
.step { padding: 6px 0; }
.step i {
  display: block;
  width: 20px;
  height: 3px;
  border-radius: var(--r-pill);
  background: rgba(255, 255, 255, 0.26);
  transition: all var(--t-mid);
}
.step:hover i { background: rgba(255, 255, 255, 0.5); }
.step.on i { width: 34px; background: var(--brand); }

@media (max-width: 900px) {
  .hero { min-height: min(72vh, 620px); }
  .copy { max-width: 100%; }
  .scrim.left { background: linear-gradient(to top, rgba(5, 6, 9, 0.96) 10%, rgba(5, 6, 9, 0.55) 46%, rgba(5, 6, 9, 0.12) 100%); }
}
@media (max-width: 560px) {
  .title { font-size: clamp(30px, 9vw, 42px); }
  .ov { font-size: var(--fs-14); -webkit-line-clamp: 2; }
  .actions .btn { flex: 1; }
}
</style>
