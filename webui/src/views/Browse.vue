<script setup>
import { ref, computed, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, fmtSize } from '../api.js'

const route = useRoute()
const router = useRouter()

const sources = ref([])
const data = ref(null)
const error = ref('')
const busyPath = ref('')

const sourceId = computed(() => route.params.id || null)
const path = computed(() => route.query.path || '/')

const crumbs = computed(() => {
  const parts = (path.value || '/').split('/').filter(Boolean)
  const list = [{ name: data.value?.name || '根目录', path: '/' }]
  let acc = ''
  for (const p of parts) {
    acc += '/' + p
    list.push({ name: p, path: acc })
  }
  return list
})

onMounted(async () => {
  if (!sourceId.value) {
    try { sources.value = await api.get('/api/sources') } catch (e) { error.value = e.message }
  }
})

async function load() {
  error.value = ''
  data.value = null
  try {
    data.value = await api.get(`/api/browse/${sourceId.value}?path=${encodeURIComponent(path.value)}`)
  } catch (e) {
    error.value = e.message
  }
}

watch(sourceId, () => {
  if (sourceId.value) load()
  else sources.value = []
})
watch(path, () => { if (sourceId.value) load() })

function open(entry) {
  router.push({ path: `/browse/${sourceId.value}`, query: { path: entry.path } })
}

async function play(entry) {
  if (busyPath.value) return
  busyPath.value = entry.path
  error.value = ''
  try {
    const resp = await api.get(`/api/browseplay/${sourceId.value}?path=${encodeURIComponent(entry.path)}`)
    router.push(`/play/${resp.file_id}`)
  } catch (e) {
    error.value = e.message
    busyPath.value = ''
  }
}
</script>

<template>
  <main class="page">
    <!-- pick a source -->
    <template v-if="!sourceId">
      <h1>文件浏览</h1>
      <p class="muted">直接浏览存储源里的文件,点开即播,无需建库和扫描。</p>
      <div v-if="!sources.length" class="card muted">还没有存储源,先到 设置 → 存储源 添加一个。</div>
      <div class="src-grid">
        <router-link v-for="s in sources" :key="s.id" :to="`/browse/${s.id}`" class="card src-card">
          <div class="src-icon">{{ s.type === 'openlist' ? '☁️' : s.type === 'webdav' ? '🌐' : '💾' }}</div>
          <div>
            <div class="src-name">{{ s.name }}</div>
            <div class="tiny">{{ s.type }} · {{ s.config.base_url || s.config.webdav_url || s.config.root }}</div>
          </div>
        </router-link>
      </div>
    </template>

    <!-- browse -->
    <template v-else>
      <div class="crumbs">
        <router-link to="/browse" class="btn ghost">← 存储源</router-link>
        <template v-for="(c, i) in crumbs" :key="c.path">
          <span class="sep">/</span>
          <button class="crumb" :class="{ last: i === crumbs.length - 1 }" @click="router.push({ path: `/browse/${sourceId}`, query: { path: c.path } })">
            {{ c.name }}
          </button>
        </template>
      </div>

      <div v-if="error" class="card" style="color: var(--danger); margin: 14px 0">{{ error }}</div>

      <div class="files card">
        <div v-if="data && !data.entries.length" class="muted" style="padding: 12px">空目录</div>
        <div v-for="e in data?.entries" :key="e.path" class="file-row" :class="{ dir: e.is_dir }">
          <button v-if="e.is_dir" class="file-main" @click="open(e)">
            <span class="f-icon">📁</span>
            <span class="f-name">{{ e.name }}</span>
          </button>
          <button v-else-if="e.video" class="file-main" @click="play(e)">
            <span class="f-icon">🎬</span>
            <span class="f-name">{{ e.name }}</span>
            <span v-if="busyPath === e.path" class="tiny">准备中…</span>
          </button>
          <div v-else class="file-main dead">
            <span class="f-icon">📄</span>
            <span class="f-name muted">{{ e.name }}</span>
          </div>
          <span v-if="!e.is_dir" class="tiny f-size">{{ fmtSize(e.size) }}</span>
        </div>
      </div>
    </template>
  </main>
</template>

<style scoped>
.src-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(260px, 1fr)); gap: 14px; margin-top: 18px; }
.src-card { display: flex; gap: 14px; align-items: center; transition: border-color 0.15s; }
.src-card:hover { border-color: var(--border-2); }
.src-icon { font-size: 26px; }
.src-name { font-weight: 650; margin-bottom: 3px; }
.crumbs { display: flex; align-items: center; gap: 6px; margin-bottom: 16px; flex-wrap: wrap; }
.sep { color: var(--text-4); }
.crumb { background: none; border: none; color: var(--text-2); padding: 4px 6px; border-radius: 6px; }
.crumb:hover { background: var(--bg-2); color: var(--text-1); }
.crumb.last { color: var(--text-1); font-weight: 650; }
.files { padding: 6px 0; }
.file-row { display: flex; align-items: center; }
.file-row + .file-row { border-top: 1px solid var(--border-1); }
.file-main {
  flex: 1; display: flex; align-items: center; gap: 12px;
  background: none; border: none; color: var(--text-1);
  padding: 11px 18px; text-align: left; border-radius: 0;
  min-width: 0;
}
.file-main:hover { background: var(--bg-2); }
.file-main.dead { cursor: default; }
.f-icon { flex-shrink: 0; }
.f-name { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.f-size { padding-right: 18px; flex-shrink: 0; }
</style>
