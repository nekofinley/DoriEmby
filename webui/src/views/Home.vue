<script setup>
import { ref, onMounted } from 'vue'
import { api, imgUrl } from '../api.js'

const home = ref(null)
const error = ref('')

onMounted(async () => {
  try {
    home.value = await api.get('/api/home')
  } catch (e) {
    error.value = e.message
  }
})
</script>

<template>
  <main class="page">
    <div v-if="error" class="card">{{ error }}</div>
    <template v-if="home">
      <section v-if="home.continue_watching && home.continue_watching.length" class="row">
        <h2>继续观看</h2>
        <div class="hscroll">
          <router-link v-for="c in home.continue_watching" :key="c.item.id + '-' + c.file_id"
            :to="`/play/${c.file_id}`" class="cw-card">
            <div class="cw-poster">
              <img :src="imgUrl(c.item.id, 'poster')" loading="lazy" />
              <div class="cw-bar"><i :style="{ width: Math.min(100, c.percent) + '%' }"></i></div>
            </div>
            <div class="cw-title">{{ c.item.name }}</div>
            <div class="tiny">{{ Math.floor(c.percent) }}% 已观看</div>
          </router-link>
        </div>
      </section>

      <section class="row">
        <h2>最近添加的电影</h2>
        <div v-if="!home.recent_movies.length" class="muted empty-hint">
          还没有电影。到 设置 → 存储源/媒体库 添加并扫描你的网盘目录。
        </div>
        <div class="grid">
          <router-link v-for="m in home.recent_movies" :key="m.id" :to="`/item/${m.id}`" class="poster-card">
            <img :src="imgUrl(m.id, 'poster')" loading="lazy" />
            <div class="pc-title">{{ m.name }}</div>
            <div class="tiny">{{ m.year || '' }}</div>
          </router-link>
        </div>
      </section>

      <section class="row">
        <h2>最近添加的剧集</h2>
        <div v-if="!home.recent_shows.length" class="muted empty-hint">还没有剧集。</div>
        <div class="grid">
          <router-link v-for="s in home.recent_shows" :key="s.id" :to="`/item/${s.id}`" class="poster-card">
            <img :src="imgUrl(s.id, 'poster')" loading="lazy" />
            <div class="pc-title">{{ s.name }}</div>
            <div class="tiny">{{ s.year || '' }}</div>
          </router-link>
        </div>
      </section>
    </template>
  </main>
</template>

<style scoped>
.row { margin-bottom: 34px; }
.row h2 { margin-bottom: 14px; }
.hscroll {
  display: flex;
  gap: 14px;
  overflow-x: auto;
  padding-bottom: 8px;
}
.cw-card { flex: 0 0 150px; }
.cw-poster {
  position: relative;
  aspect-ratio: 2/3;
  border-radius: var(--radius-m);
  overflow: hidden;
  background: var(--bg-2);
  border: 1px solid var(--border-1);
}
.cw-poster img { width: 100%; height: 100%; object-fit: cover; display: block; }
.cw-bar {
  position: absolute; bottom: 0; left: 0; right: 0; height: 4px;
  background: rgba(255,255,255,0.15);
}
.cw-bar i { display: block; height: 100%; background: var(--accent); }
.cw-title { margin-top: 8px; font-weight: 550; font-size: 13px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(132px, 1fr));
  gap: 18px 14px;
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
.empty-hint { padding: 20px 0; }
</style>
