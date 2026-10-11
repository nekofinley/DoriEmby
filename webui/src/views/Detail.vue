<script setup>
import { ref, onMounted, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, imgUrl, fmtSize } from '../api.js'

const route = useRoute()
const router = useRouter()
const data = ref(null)
const error = ref('')
const selectedFile = ref(null)
const busy = ref(false)

const item = computed(() => data.value?.item)

function videoBadges(media) {
  if (!media || !media.video) return []
  const b = []
  if (media.video.height >= 2000 || media.video.width >= 3800) b.push('4K')
  else if (media.video.height >= 1000) b.push('1080p')
  else if (media.video.height) b.push(`${media.video.height}p`)
  b.push(media.video.codec.toUpperCase())
  if (media.video.dv_profile) b.push(`DV${media.video.dv_profile}`)
  if (media.video.tag && media.video.tag !== '[0][0][0][0]') b.push(media.video.tag)
  return b
}
function audioBadges(media) {
  if (!media) return []
  return (media.audio || []).map(a => a.codec.toUpperCase() + (a.lang ? ` · ${a.lang}` : ''))
}

async function load() {
  try {
    data.value = await api.get(`/api/items/${route.params.id}`)
    const files = data.value.files
    if (files && files.length) selectedFile.value = files[0].id
  } catch (e) {
    error.value = e.message
  }
}
onMounted(load)

function play() {
  if (selectedFile.value) router.push(`/play/${selectedFile.value}`)
}

// 点集即播:直接取该集的主文件进入播放页,不再绕道集详情
async function playEpisode(ep) {
  try {
    const d = await api.get(`/api/items/${ep.id}`)
    const files = (d && d.files) || []
    if (files.length) {
      router.push(`/play/${files[0].id}`)
    } else {
      error.value = `「${ep.name || '第 ' + ep.episode + ' 集'}」没有关联的视频文件——请对媒体库重新扫描(设置 → 媒体库)。`
    }
  } catch (e) {
    error.value = e.message
  }
}

async function refresh() {
  busy.value = true
  try {
    await api.post(`/api/items/${route.params.id}/refresh`, {})
    await load()
  } catch (e) {
    error.value = e.message
  } finally {
    busy.value = false
  }
}

const isShow = computed(() => item.value?.type === 'show')
const isSeason = computed(() => item.value?.type === 'season')
const backTarget = computed(() => {
  if (item.value?.parent_id) return `/item/${item.value.parent_id}`
  return '/'
})
</script>

<template>
  <main class="page" v-if="item">
    <router-link :to="backTarget" class="btn ghost back">← 返回</router-link>

    <!-- show: seasons + episodes -->
    <template v-if="isShow">
      <div class="hero" v-if="item.backdrop_path">
        <img :src="imgUrl(item.id, 'backdrop')" class="hero-img" />
        <div class="hero-fade"></div>
      </div>
      <div class="detail-head">
        <img class="poster" :src="imgUrl(item.id, 'poster')" />
        <div class="meta">
          <h1>{{ item.name }}</h1>
          <div class="badges">
            <span v-if="item.year" class="badge">{{ item.year }}</span>
            <span v-if="item.rating" class="badge">★ {{ item.rating.toFixed(1) }}</span>
          </div>
          <p class="overview">{{ item.overview }}</p>
          <button class="btn" :disabled="busy" @click="refresh">重新识别</button>
        </div>
      </div>
      <div v-for="season in data.seasons" :key="season.id" class="season">
        <h2>{{ season.name }}</h2>
        <div class="episodes">
          <button v-for="ep in season.episodes" :key="ep.id" class="ep card" @click="playEpisode(ep)">
            <img v-if="ep.still_path" :src="imgUrl(ep.id, 'still')" class="ep-still" />
            <div v-else class="ep-still placeholder">E{{ ep.episode }}</div>
            <div class="ep-meta">
              <div class="ep-name">{{ ep.name }}</div>
              <div class="tiny">{{ ep.air_date || '' }}</div>
            </div>
          </button>
        </div>
      </div>
    </template>

    <!-- movie / episode -->
    <template v-else>
      <div class="hero" v-if="item.backdrop_path">
        <img :src="imgUrl(item.id, 'backdrop')" class="hero-img" />
        <div class="hero-fade"></div>
      </div>
      <div class="detail-head">
        <img class="poster" :src="imgUrl(item.id, 'poster')" />
        <div class="meta">
          <h1>{{ item.name }}</h1>
          <div class="badges">
            <span v-if="item.year" class="badge">{{ item.year }}</span>
            <span v-if="item.rating" class="badge">★ {{ item.rating.toFixed(1) }}</span>
          </div>
          <p class="overview">{{ item.overview }}</p>

          <div v-if="data.files && data.files.length" class="files">
            <div v-if="data.files.length > 1" class="field" style="max-width:420px">
              <label>版本</label>
              <select v-model="selectedFile">
                <option v-for="f in data.files" :key="f.id" :value="f.id">
                  {{ f.label || '默认' }} · {{ fmtSize(f.size) }}
                </option>
              </select>
            </div>
            <div v-for="f in data.files" v-show="f.id === selectedFile" :key="f.id" class="fileinfo">
              <div class="badges">
                <span v-for="b in videoBadges(f.media)" :key="'v' + b" class="badge">{{ b }}</span>
                <span v-for="(b, i) in audioBadges(f.media)" :key="'a' + i" class="badge">{{ b }}</span>
                <span class="badge">{{ fmtSize(f.size) }}</span>
              </div>
              <div v-if="f.subtitles && f.subtitles.length" class="tiny subs-line">
                字幕:{{ f.subtitles.map(s => s.title || s.lang || s.format).join(' / ') }}
              </div>
            </div>
          </div>

          <div class="actions">
            <button class="btn play" @click="play" :disabled="!selectedFile">▶ 播放</button>
            <button class="btn" :disabled="busy" @click="refresh">重新识别</button>
          </div>
        </div>
      </div>
    </template>

    <div v-if="error" class="card" style="margin-top:16px; color: var(--danger)">{{ error }}</div>
  </main>
</template>

<style scoped>
.back { margin-bottom: 14px; }
.hero {
  position: relative;
  border-radius: var(--radius-l);
  overflow: hidden;
  margin-bottom: -110px;
  max-height: 380px;
}
.hero-img { width: 100%; height: 380px; object-fit: cover; display: block; }
.hero-fade {
  position: absolute; inset: 0;
  background: linear-gradient(to bottom, transparent 30%, var(--bg-0) 96%);
}
.detail-head {
  position: relative;
  display: flex;
  gap: 26px;
  padding: 0 6px;
  margin-top: 60px;
}
.poster {
  width: 190px;
  aspect-ratio: 2/3;
  object-fit: cover;
  border-radius: var(--radius-m);
  border: 1px solid var(--border-1);
  background: var(--bg-2);
  flex-shrink: 0;
  box-shadow: var(--shadow-2);
}
.meta { flex: 1; min-width: 0; padding-top: 60px; }
.badges { display: flex; gap: 6px; flex-wrap: wrap; margin: 8px 0; }
.overview { color: var(--text-2); line-height: 1.65; max-width: 760px; }
.actions { display: flex; gap: 10px; margin-top: 16px; align-items: center; }
.fileinfo { margin: 8px 0; }
.subs-line { margin-top: 8px; }
.season { margin-top: 34px; }
.season h2 { margin-bottom: 14px; }
.episodes { display: flex; flex-direction: column; gap: 10px; }
.ep {
  display: flex; gap: 14px; align-items: center;
  text-align: left; padding: 10px; cursor: pointer;
  transition: border-color 0.15s;
}
.ep:hover { border-color: var(--border-2); }
.ep-still {
  width: 150px; aspect-ratio: 16/9; object-fit: cover;
  border-radius: 8px; background: var(--bg-3); flex-shrink: 0;
  border: 1px solid var(--border-1);
  display: flex; align-items: center; justify-content: center;
  color: var(--text-4); font-weight: 700;
}
.ep-name { font-weight: 600; }
@media (max-width: 720px) {
  .detail-head { flex-direction: column; margin-top: 30px; }
  .meta { padding-top: 0; }
  .poster { width: 140px; }
  .hero { margin-bottom: -40px; }
}
</style>
