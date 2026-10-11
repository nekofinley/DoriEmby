<script setup>
import { ref, onMounted, watch, computed } from 'vue'
import { useRoute } from 'vue-router'
import { api, imgUrl } from '../api.js'

const route = useRoute()
const libId = computed(() => route.params.id)
const items = ref([])
const total = ref(0)
const libraries = ref([])
const search = ref('')
const sort = ref('added')
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    const q = new URLSearchParams({
      library_id: libId.value, search: search.value, sort: sort.value, limit: '500',
    })
    const r = await api.get(`/api/items?${q}`)
    items.value = r.items
    total.value = r.total
  } finally {
    loading.value = false
  }
}

onMounted(async () => {
  libraries.value = await api.get('/api/libraries')
  await load()
})
watch([libId, sort], load)
let deb
watch(search, () => { clearTimeout(deb); deb = setTimeout(load, 300) })
</script>

<template>
  <main class="page">
    <div class="lib-head">
      <h1>{{ libraries.find(l => l.id == libId)?.name || '媒体库' }}</h1>
      <div class="lib-tools">
        <input v-model="search" class="search" placeholder="搜索…" />
        <select v-model="sort" class="sort">
          <option value="added">最近添加</option>
          <option value="name">名称</option>
          <option value="year">年份</option>
          <option value="rating">评分</option>
        </select>
      </div>
    </div>
    <div class="tiny" style="margin-bottom:16px">{{ total }} 个条目</div>
    <div v-if="!items.length && !loading" class="card muted">没有条目。请先在设置里扫描该媒体库。</div>
    <div class="grid">
      <router-link v-for="it in items" :key="it.id" :to="`/item/${it.id}`" class="poster-card">
        <img :src="imgUrl(it.id, 'poster')" loading="lazy" />
        <div class="pc-title">{{ it.name }}</div>
        <div class="tiny">{{ it.year || '' }}</div>
      </router-link>
    </div>
  </main>
</template>

<style scoped>
.lib-head { display: flex; align-items: center; gap: 16px; margin-bottom: 6px; flex-wrap: wrap; }
.lib-tools { margin-left: auto; display: flex; gap: 8px; }
.search, .sort {
  background: var(--bg-1);
  border: 1px solid var(--border-1);
  border-radius: var(--radius-s);
  color: var(--text-1);
  padding: 8px 12px;
  outline: none;
}
.search:focus, .sort:focus { border-color: var(--accent); }
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(132px, 1fr));
  gap: 20px 14px;
  margin-top: 12px;
}
.poster-card img {
  width: 100%;
  aspect-ratio: 2/3;
  object-fit: cover;
  border-radius: var(--radius-m);
  border: 1px solid var(--border-1);
  background: var(--bg-2);
  transition: transform 0.15s, border-color 0.15s;
}
.poster-card:hover img { transform: translateY(-3px); border-color: var(--border-2); }
.pc-title { margin-top: 8px; font-weight: 550; font-size: 13px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
</style>
