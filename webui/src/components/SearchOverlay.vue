<script setup>
import { ref, watch, computed, nextTick } from 'vue'
import { useRouter } from 'vue-router'
import AppIcon from './AppIcon.vue'
import PosterCard from './PosterCard.vue'
import SmartImage from './SmartImage.vue'
import { api, imgUrl } from '../api.js'

/**
 * Command-palette style search across every media library.
 * The REST API filters per library, so we fan out and merge — a few libraries
 * at most, so this stays a single round trip in practice.
 */
const props = defineProps({
  open: { type: Boolean, default: false },
  libraries: { type: Array, default: () => [] },
})
const emit = defineEmits(['update:open'])

const router = useRouter()
const q = ref('')
const input = ref(null)
const loading = ref(false)
const results = ref([])
const recents = ref([])
const active = ref(0)

watch(() => props.open, async (v) => {
  if (v) {
    await nextTick()
    input.value?.focus()
    if (!recents.value.length) loadRecents()
  } else {
    q.value = ''
    results.value = []
  }
})

async function loadRecents() {
  try {
    const home = await api.get('/api/home')
    const rows = [...(home.recent_movies || []), ...(home.recent_shows || [])].slice(0, 6)
    recents.value = rows
  } catch {}
}

let timer = null
watch(q, () => {
  active.value = 0
  clearTimeout(timer)
  const term = q.value.trim()
  if (!term) { results.value = []; loading.value = false; return }
  loading.value = true
  timer = setTimeout(run, 220)
})

async function run() {
  const term = q.value.trim()
  const libs = props.libraries.length ? props.libraries : []
  try {
    const lists = await Promise.all(libs.map((l) =>
      api.get(`/api/items?library_id=${l.id}&search=${encodeURIComponent(term)}&limit=40&sort=name`)
        .then((r) => r.items || []).catch(() => []),
    ))
    const seen = new Set()
    const merged = []
    for (const it of lists.flat()) {
      if (seen.has(it.id)) continue
      seen.add(it.id)
      merged.push(it)
    }
    const low = term.toLowerCase()
    merged.sort((a, b) => {
      const as = a.name.toLowerCase().startsWith(low) ? 0 : 1
      const bs = b.name.toLowerCase().startsWith(low) ? 0 : 1
      return as - bs || (b.rating || 0) - (a.rating || 0) || a.name.localeCompare(b.name)
    })
    results.value = merged.slice(0, 60)
  } finally {
    loading.value = false
  }
}

const shown = computed(() => (q.value.trim() ? results.value : recents.value))

function go(item) {
  emit('update:open', false)
  router.push(`/item/${item.id}`)
}

function onKeydown(e) {
  if (e.key === 'Escape') { emit('update:open', false); return }
  if (!shown.value.length) return
  const cols = 6
  if (e.key === 'ArrowRight') { e.preventDefault(); active.value = Math.min(shown.value.length - 1, active.value + 1) }
  if (e.key === 'ArrowLeft') { e.preventDefault(); active.value = Math.max(0, active.value - 1) }
  if (e.key === 'ArrowDown') { e.preventDefault(); active.value = Math.min(shown.value.length - 1, active.value + cols) }
  if (e.key === 'ArrowUp') { e.preventDefault(); active.value = Math.max(0, active.value - cols) }
  if (e.key === 'Enter') { e.preventDefault(); go(shown.value[active.value] || shown.value[0]) }
}
</script>

<template>
  <Teleport to="body">
    <Transition name="fade">
      <div v-if="open" class="search-mask" @click.self="emit('update:open', false)">
        <div class="search-panel" @keydown="onKeydown">
          <div class="bar">
            <AppIcon name="search" :size="20" />
            <input
              ref="input" v-model="q" class="q"
              placeholder="搜索电影、剧集、番剧…" autocomplete="off" spellcheck="false"
            />
            <span v-if="loading" class="tiny">搜索中…</span>
            <button class="icon-btn" aria-label="关闭" @click="emit('update:open', false)">
              <AppIcon name="close" :size="18" />
            </button>
          </div>

          <div class="body no-scrollbar">
            <div v-if="!q.trim() && recents.length" class="hint-row">
              <AppIcon name="history" :size="14" /> 最近添加
            </div>
            <div v-else-if="q.trim() && !loading && !results.length" class="none">
              <AppIcon name="search" :size="26" />
              <div class="t">没有找到「{{ q }}」</div>
              <div class="tiny">试试更短的关键词,或检查媒体库是否已扫描</div>
            </div>

            <div v-if="shown.length" class="results stagger">
              <PosterCard
                v-for="(it, i) in shown" :key="it.id" :item="it"
                :class="{ active: i === active }"
                :style="{ '--i': Math.min(i, 11) }"
                @mouseenter="active = i"
              />
            </div>

            <div v-if="!q.trim() && !recents.length" class="none">
              <AppIcon name="sparkles" :size="26" />
              <div class="t">还没有内容</div>
              <div class="tiny">先在设置里添加存储源与媒体库并扫描</div>
            </div>
          </div>

          <div class="foot tiny">
            <span><kbd>↑</kbd><kbd>↓</kbd> 移动</span>
            <span><kbd>↵</kbd> 打开</span>
            <span><kbd>Esc</kbd> 关闭</span>
            <span class="spacer"></span>
            <span v-if="q.trim()">{{ results.length }} 个结果</span>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.search-mask {
  position: fixed;
  inset: 0;
  z-index: 1500;
  background: rgba(3, 4, 7, 0.74);
  backdrop-filter: blur(10px);
  -webkit-backdrop-filter: blur(10px);
  display: flex;
  align-items: flex-start;
  justify-content: center;
  padding: clamp(12px, 8vh, 96px) 16px 16px;
}
.search-panel {
  width: min(1180px, 100%);
  max-height: 100%;
  display: flex;
  flex-direction: column;
  background: rgba(13, 15, 20, 0.97);
  border: 1px solid var(--line-2);
  border-radius: var(--r-l);
  box-shadow: var(--sh-4);
  overflow: hidden;
  animation: panelIn 340ms var(--ease-out) backwards;
}
.bar {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 14px 16px;
  border-bottom: 1px solid var(--line-1);
  color: var(--text-3);
}
.q {
  flex: 1;
  border: none;
  background: none;
  outline: none;
  font-size: var(--fs-16);
  color: var(--text-1);
}
.q::placeholder { color: var(--text-4); }
.body { padding: var(--sp-5); overflow: auto; }
.hint-row { display: flex; align-items: center; gap: 7px; font-size: var(--fs-12); color: var(--text-4); margin-bottom: var(--sp-4); }
.results {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 150px), 1fr));
  gap: var(--sp-5) var(--sp-4);
}
.results :deep(.poster) { width: 100%; }
.results :deep(.poster.active) { transform: translateY(-4px); }
.results :deep(.poster.active .art) { border-color: var(--brand); box-shadow: 0 0 0 2px var(--brand), var(--sh-3); }
.none {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  padding: 60px 0;
  color: var(--text-4);
  text-align: center;
}
.none .t { font-size: var(--fs-15); font-weight: 650; color: var(--text-2); }
.foot {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 10px 16px;
  border-top: 1px solid var(--line-1);
  color: var(--text-4);
}
kbd {
  display: inline-grid;
  place-items: center;
  min-width: 18px;
  height: 18px;
  margin-right: 3px;
  padding: 0 4px;
  border-radius: 4px;
  border: 1px solid var(--line-2);
  background: var(--bg-2);
  font-family: var(--font-mono);
  font-size: 10px;
}
@media (max-width: 720px) {
  .foot { display: none; }
  .body { padding: var(--sp-4); }
}
</style>
