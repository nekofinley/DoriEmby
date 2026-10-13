<script setup>
import { ref, onMounted, onBeforeUnmount, computed, watch } from 'vue'
import { api } from '../api.js'
import AppIcon from '../components/AppIcon.vue'
import EmptyState from '../components/EmptyState.vue'
import { toast } from '../toast.js'

const tabs = [
  { id: 'sources', label: '存储源', icon: 'cloud', desc: 'OpenList / WebDAV / 本地目录' },
  { id: 'libraries', label: '媒体库', icon: 'film', desc: '路径、类型与扫描' },
  { id: 'playback', label: '刮削与播放', icon: 'sparkles', desc: 'TMDB、Bangumi 与播放决策' },
  { id: 'system', label: '系统', icon: 'server', desc: '工具路径与运行状态' },
]

const tab = ref(localStorage.getItem('dm:settings-tab') || 'sources')
watch(tab, (v) => localStorage.setItem('dm:settings-tab', v))

const sources = ref([])
const libraries = ref([])
const scanStates = ref([])
const sys = ref(null)
const cfg = ref(null)
const error = ref('')

let pollTimer = null

// new source form
const srcType = ref('openlist')
const srcForm = ref({ name: '', root: '', webdav_url: '', webdav_user: '', webdav_pass: '', base_url: '', token: '' })
const testResult = ref(null)
const testing = ref(false)
const addingSource = ref(false)

// new library form
const libForm = ref({ name: '', type: 'movie', source_id: '', path: '/', providers: ['nfo', 'bgm', 'tmdb'] })
const addingLibrary = ref(false)

const providerLabels = { nfo: '本地 nfo', tmdb: 'TMDB', bgm: 'Bangumi' }
const sourceTypes = [
  { id: 'openlist', label: 'OpenList / AList', icon: 'cloud', hint: '可 302 直连,最省带宽' },
  { id: 'webdav', label: 'WebDAV', icon: 'globe', hint: '服务器代理播放,支持拖动' },
  { id: 'local', label: '本地目录', icon: 'hdd', hint: '服务器磁盘上的绝对路径' },
]

function buildSourceConfig() {
  const f = srcForm.value
  if (srcType.value === 'local') return { root: f.root }
  if (srcType.value === 'webdav') return { webdav_url: f.webdav_url, webdav_user: f.webdav_user, webdav_pass: f.webdav_pass }
  return { base_url: f.base_url, token: f.token }
}

async function loadAll() {
  try {
    sources.value = (await api.get('/api/sources')) || []
    libraries.value = (await api.get('/api/libraries')) || []
    scanStates.value = (await api.get('/api/scan/status')) || []
    sys.value = await api.get('/api/system/info')
    cfg.value = await api.get('/api/config')
    if (!libForm.value.source_id && sources.value.length) libForm.value.source_id = sources.value[0].id
  } catch (e) {
    error.value = e.message
  }
}

const editing = ref(null)
const editForm = ref({})

function startEdit(lib) {
  editing.value = lib.id
  let provs = []
  try { provs = JSON.parse(lib.providers || '[]') } catch {}
  if (!provs.length) provs = ['nfo', 'bgm', 'tmdb']
  editForm.value = { name: lib.name, type: lib.type, path: lib.path, providers: provs }
}
function toggleProvider(p) {
  const a = editForm.value.providers
  const i = a.indexOf(p)
  i >= 0 ? a.splice(i, 1) : a.push(p)
}
async function saveEdit(id) {
  try {
    const f = editForm.value
    await api.put(`/api/libraries/${id}`, {
      name: f.name, type: f.type, path: f.path || '/',
      providers: JSON.stringify(f.providers.length ? f.providers : ['nfo', 'bgm', 'tmdb']),
    })
    editing.value = null
    await loadAll()
    toast.ok('媒体库已更新,改过路径记得重新扫描')
  } catch (e) { toast.err(e.message) }
}

async function stopScan(id) {
  try { await api.post(`/api/libraries/${id}/scan/stop`); toast.info('已请求停止扫描') } catch (e) { toast.err(e.message) }
}

function stFor(id) { return (scanStates.value || []).find((s) => s.library_id === id) }
function pct(st) {
  if (!st || !st.total) return null
  return Math.min(100, Math.round((st.done / st.total) * 100))
}
function phaseText(st) {
  if (st.phase === 'walk') return `遍历中 · ${st.dirs_done || 0} 目录 / ${st.files_found || 0} 文件${st.walk_errors ? ' · ' + st.walk_errors + ' 失败' : ''}`
  if (st.phase === 'organize') return `整理 ${st.done}/${st.total}`
  if (st.phase === 'identify') return `识别刮削 ${st.done}/${st.total}`
  return st.phase
}
function fmtClock(ts) {
  if (!ts) return ''
  return new Date(ts * 1000).toLocaleTimeString('zh-CN', { hour12: false })
}
function sourceName(id) {
  return sources.value.find((s) => s.id === id)?.name || `源 #${id}`
}
function providersOf(lib) {
  try {
    const p = JSON.parse(lib.providers || '[]')
    return p.length ? p : ['nfo', 'bgm', 'tmdb']
  } catch { return [] }
}

onMounted(async () => {
  await loadAll()
  pollTimer = setInterval(async () => {
    try { scanStates.value = (await api.get('/api/scan/status')) || [] } catch {}
  }, 2000)
})
onBeforeUnmount(() => clearInterval(pollTimer))

async function addSource() {
  try {
    await api.post('/api/sources', {
      name: srcForm.value.name || '未命名源', type: srcType.value, config: buildSourceConfig(),
    })
    srcForm.value = { name: '', root: '', webdav_url: '', webdav_user: '', webdav_pass: '', base_url: '', token: '' }
    addingSource.value = false
    await loadAll()
    toast.ok('存储源已添加')
  } catch (e) { toast.err(e.message) }
}

async function testSource() {
  testing.value = true
  testResult.value = null
  try {
    testResult.value = await api.post('/api/sources/test', { type: srcType.value, config: buildSourceConfig() })
  } catch (e) {
    testResult.value = { ok: false, error: e.message }
  } finally {
    testing.value = false
  }
}

// TMDB 连通性测试
const tmdbTesting = ref(false)
const tmdbTest = ref(null)

async function testTMDB() {
  tmdbTesting.value = true
  tmdbTest.value = null
  try {
    tmdbTest.value = await api.post('/api/metadata/test', {
      api_key: cfg.value.tmdb.api_key,
      api_base: cfg.value.tmdb.api_base,
      img_base: cfg.value.tmdb.img_base,
      use_proxy: !!cfg.value.tmdb.use_proxy,
      proxy: cfg.value.tmdb.proxy,
    })
  } catch (e) {
    tmdbTest.value = { ok: false, message: e.message }
  } finally {
    tmdbTesting.value = false
  }
}

// Bangumi 连通性测试
const bgmTesting = ref(false)
const bgmTest = ref(null)
const bgmKeyword = ref('孤独摇滚')

async function testBGM() {
  bgmTesting.value = true
  bgmTest.value = null
  try {
    bgmTest.value = await api.post('/api/metadata/test', {
      provider: 'bgm',
      token: cfg.value.bgm.token,
      api_base: cfg.value.bgm.api_base,
      use_proxy: !!cfg.value.bgm.use_proxy,
      proxy: cfg.value.bgm.proxy,
      keyword: bgmKeyword.value,
    })
  } catch (e) {
    bgmTest.value = { ok: false, message: e.message }
  } finally {
    bgmTesting.value = false
  }
}

async function delSource(id) {
  if (!confirm('删除该存储源?其下媒体库也会被删除。')) return
  try { await api.del(`/api/sources/${id}`); await loadAll(); toast.ok('已删除') } catch (e) { toast.err(e.message) }
}

async function addLibrary() {
  try {
    const f = libForm.value
    await api.post('/api/libraries', {
      name: f.name, type: f.type, source_id: Number(f.source_id), path: f.path || '/',
      providers: JSON.stringify(f.providers.length ? f.providers : ['nfo', 'bgm', 'tmdb']),
    })
    libForm.value = { name: '', type: 'movie', source_id: f.source_id, path: '/', providers: ['nfo', 'bgm', 'tmdb'] }
    addingLibrary.value = false
    await loadAll()
    toast.ok('媒体库已创建,记得点扫描')
  } catch (e) { toast.err(e.message) }
}

async function scanLibrary(id) {
  try {
    await api.post(`/api/libraries/${id}/scan`)
    toast.info('扫描已开始(含刮削,后台进行)')
  } catch (e) { toast.err(e.message) }
}

async function delLibrary(id) {
  if (!confirm('删除该媒体库及其所有条目?')) return
  try { await api.del(`/api/libraries/${id}`); await loadAll(); toast.ok('已删除') } catch (e) { toast.err(e.message) }
}

async function saveConfig() {
  try {
    await api.put('/api/config', {
      ffmpeg: cfg.value.ffmpeg,
      ffprobe: cfg.value.ffprobe,
      scan_concurrency: Number(cfg.value.scan_concurrency),
      tmdb: cfg.value.tmdb,
      bgm: cfg.value.bgm,
      playback: cfg.value.playback,
    })
    toast.ok('配置已保存,立即生效')
    await loadAll()
  } catch (e) { toast.err(e.message) }
}

const anyScanning = computed(() => (scanStates.value || []).some((s) => s.running))
</script>

<template>
  <div class="page settings-page" v-if="cfg">
    <header class="set-head">
      <div>
        <span class="eyebrow">管理</span>
        <h1>设置</h1>
      </div>
      <div class="status-chips">
        <span v-if="sys" class="badge" :class="sys.ffmpeg_ok ? 'ok' : 'err'">
          <AppIcon :name="sys.ffmpeg_ok ? 'check' : 'alert'" :size="12" /> ffmpeg
        </span>
        <span v-if="sys" class="badge" :class="sys.ffprobe_ok ? 'ok' : 'err'">
          <AppIcon :name="sys.ffprobe_ok ? 'check' : 'alert'" :size="12" /> ffprobe
        </span>
        <span v-if="sys" class="badge" :class="sys.tmdb_ok ? 'ok' : ''">
          <AppIcon name="sparkles" :size="12" /> TMDB {{ sys.tmdb_ok ? '已配置' : '未配置' }}
        </span>
        <span v-if="anyScanning" class="badge warn"><AppIcon name="scan" :size="12" /> 扫描进行中</span>
      </div>
    </header>

    <div v-if="error" class="flash err"><AppIcon name="alert" :size="16" /> {{ error }}</div>

    <div class="set-layout">
      <!-- ================= nav ================= -->
      <aside class="set-nav">
        <button
          v-for="t in tabs" :key="t.id" class="nav-item" :class="{ on: tab === t.id }"
          @click="tab = t.id"
        >
          <span class="ni-icon"><AppIcon :name="t.icon" :size="17" /></span>
          <span class="ni-text">
            <span class="ni-label">{{ t.label }}</span>
            <span class="ni-desc">{{ t.desc }}</span>
          </span>
        </button>
      </aside>

      <!-- ================= content ================= -->
      <div class="set-body">
        <!-- ---------- sources ---------- -->
        <section v-show="tab === 'sources'" class="stack">
          <div class="card">
            <div class="card-head">
              <div>
                <div class="card-title"><AppIcon name="plus" :size="16" /> 添加存储源</div>
                <div class="tiny">支持 OpenList / AList、WebDAV 与服务器本地目录。</div>
              </div>
              <button class="btn sm" @click="addingSource = !addingSource">
                {{ addingSource ? '收起' : '展开表单' }}
              </button>
            </div>

            <Transition name="fade">
              <div v-if="addingSource" class="add-panel">
                <div class="type-cards">
                  <button
                    v-for="t in sourceTypes" :key="t.id" class="type-card"
                    :class="{ on: srcType === t.id }" @click="srcType = t.id"
                  >
                    <AppIcon :name="t.icon" :size="18" />
                    <span class="tc-label">{{ t.label }}</span>
                    <span class="tiny">{{ t.hint }}</span>
                  </button>
                </div>

                <div class="form-grid">
                  <div class="field">
                    <label>名称</label>
                    <input v-model="srcForm.name" placeholder="我的天翼云盘" />
                  </div>
                  <template v-if="srcType === 'openlist'">
                    <div class="field">
                      <label>OpenList 地址</label>
                      <input v-model="srcForm.base_url" placeholder="http://127.0.0.1:5244" />
                    </div>
                    <div class="field">
                      <label>Token(可选,建议填管理员 token)</label>
                      <input v-model="srcForm.token" placeholder="alist-xxxx" />
                    </div>
                  </template>
                  <template v-else-if="srcType === 'webdav'">
                    <div class="field">
                      <label>WebDAV 地址</label>
                      <input v-model="srcForm.webdav_url" placeholder="http://127.0.0.1:5244/dav" />
                    </div>
                    <div class="field"><label>用户名</label><input v-model="srcForm.webdav_user" /></div>
                    <div class="field"><label>密码</label><input v-model="srcForm.webdav_pass" type="password" /></div>
                  </template>
                  <template v-else>
                    <div class="field">
                      <label>服务器上的绝对路径</label>
                      <input v-model="srcForm.root" placeholder="D:\media 或 /mnt/media" />
                    </div>
                  </template>
                </div>

                <div class="btn-row">
                  <button class="btn" :disabled="testing" @click="testSource">
                    <AppIcon name="wifi" :size="15" /> {{ testing ? '测试中…' : '测试连接' }}
                  </button>
                  <button class="btn primary" @click="addSource"><AppIcon name="check" :size="15" /> 添加</button>
                </div>

                <Transition name="fade">
                  <div v-if="testResult" class="flash" :class="testResult.ok ? 'ok' : 'err'" style="margin:14px 0 0">
                    <AppIcon :name="testResult.ok ? 'check' : 'alert'" :size="16" />
                    <span v-if="testResult.ok">连接成功,根目录 {{ testResult.count }} 项:{{ (testResult.entries || []).slice(0, 6).join(', ') }}</span>
                    <span v-else>{{ testResult.error }}</span>
                  </div>
                </Transition>
              </div>
            </Transition>
          </div>

          <div v-if="sources.length" class="list">
            <div v-for="s in sources" :key="s.id" class="row-card">
              <span class="r-icon"><AppIcon :name="s.type === 'openlist' ? 'cloud' : s.type === 'webdav' ? 'globe' : 'hdd'" :size="18" /></span>
              <div class="r-main">
                <div class="r-title">{{ s.name }} <span class="badge">{{ s.type }}</span></div>
                <div class="tiny truncate">{{ s.config.base_url || s.config.webdav_url || s.config.root || '-' }}</div>
              </div>
              <button class="btn sm danger" @click="delSource(s.id)"><AppIcon name="trash" :size="14" /> 删除</button>
            </div>
          </div>

          <EmptyState v-else icon="cloud" title="还没有存储源" text="点上面的「展开表单」添加第一个存储源。" />
        </section>

        <!-- ---------- libraries ---------- -->
        <section v-show="tab === 'libraries'" class="stack">
          <div class="card">
            <div class="card-head">
              <div>
                <div class="card-title"><AppIcon name="plus" :size="16" /> 创建媒体库</div>
                <div class="tiny">选择存储源与目录,扫描后会自动整理剧集结构并刮削元数据。</div>
              </div>
              <button class="btn sm" @click="addingLibrary = !addingLibrary">{{ addingLibrary ? '收起' : '展开表单' }}</button>
            </div>

            <Transition name="fade">
              <div v-if="addingLibrary" class="add-panel">
                <div v-if="!sources.length" class="flash err">
                  <AppIcon name="alert" :size="16" /> 请先在「存储源」里添加一个源。
                </div>
                <template v-else>
                  <div class="form-grid">
                    <div class="field"><label>名称</label><input v-model="libForm.name" placeholder="电影" /></div>
                    <div class="field">
                      <label>类型</label>
                      <select v-model="libForm.type">
                        <option value="movie">电影</option>
                        <option value="series">剧集</option>
                      </select>
                    </div>
                    <div class="field">
                      <label>存储源</label>
                      <select v-model="libForm.source_id">
                        <option v-for="s in sources" :key="s.id" :value="s.id">{{ s.name }}</option>
                      </select>
                    </div>
                    <div class="field">
                      <label>路径(源内路径)</label>
                      <input v-model="libForm.path" placeholder="/movies" />
                    </div>
                  </div>
                  <div class="field">
                    <label>刮削来源(按优先级)</label>
                    <div class="type-picker">
                      <button
                        v-for="p in ['nfo', 'tmdb', 'bgm']" :key="p" class="chip"
                        :class="{ on: libForm.providers.includes(p) }"
                        @click="libForm.providers.includes(p) ? libForm.providers.splice(libForm.providers.indexOf(p), 1) : libForm.providers.push(p)"
                      >{{ providerLabels[p] }}</button>
                    </div>
                  </div>
                  <button class="btn primary" :disabled="!libForm.source_id || !libForm.name" @click="addLibrary">
                    <AppIcon name="check" :size="15" /> 创建媒体库
                  </button>
                </template>
              </div>
            </Transition>
          </div>

          <div v-if="libraries.length" class="list">
            <div v-for="lib in libraries" :key="lib.id" class="card lib-card">
              <template v-if="editing === lib.id">
                <div class="card-title"><AppIcon name="settings" :size="16" /> 编辑「{{ lib.name }}」</div>
                <div class="form-grid">
                  <div class="field"><label>名称</label><input v-model="editForm.name" /></div>
                  <div class="field">
                    <label>类型</label>
                    <select v-model="editForm.type">
                      <option value="movie">电影</option>
                      <option value="series">剧集</option>
                    </select>
                  </div>
                  <div class="field"><label>路径(源内路径)</label><input v-model="editForm.path" placeholder="/movies" /></div>
                </div>
                <div class="field">
                  <label>刮削来源(按优先级)</label>
                  <div class="type-picker">
                    <button v-for="p in ['nfo', 'tmdb', 'bgm']" :key="p" class="chip" :class="{ on: editForm.providers.includes(p) }" @click="toggleProvider(p)">
                      {{ providerLabels[p] }}
                    </button>
                  </div>
                </div>
                <div class="btn-row">
                  <button class="btn primary" @click="saveEdit(lib.id)"><AppIcon name="check" :size="15" /> 保存</button>
                  <button class="btn" @click="editing = null">取消</button>
                </div>
              </template>

              <template v-else>
                <div class="lib-line">
                  <span class="r-icon"><AppIcon :name="lib.type === 'series' ? 'tv' : 'film'" :size="18" /></span>
                  <div class="r-main">
                    <div class="r-title">
                      {{ lib.name }}
                      <span class="badge">{{ lib.type === 'movie' ? '电影' : '剧集' }}</span>
                    </div>
                    <div class="tiny truncate">
                      {{ sourceName(lib.source_id) }} · 路径 {{ lib.path }}
                      · 刮削 {{ providersOf(lib).map(p => providerLabels[p] || p).join(' / ') }}
                    </div>
                  </div>
                  <div class="lib-actions">
                    <button v-if="stFor(lib.id)?.running" class="btn sm" @click="stopScan(lib.id)">
                      <AppIcon name="close" :size="14" /> 停止
                    </button>
                    <button v-else class="btn sm primary" @click="scanLibrary(lib.id)">
                      <AppIcon name="scan" :size="14" /> 扫描
                    </button>
                    <button class="btn sm" @click="startEdit(lib)"><AppIcon name="settings" :size="14" /> 编辑</button>
                    <button class="btn sm danger" @click="delLibrary(lib.id)"><AppIcon name="trash" :size="14" /></button>
                  </div>
                </div>

                <div v-if="stFor(lib.id)?.running" class="scan-live">
                  <div class="bar" :class="{ indet: pct(stFor(lib.id)) === null }">
                    <i :style="pct(stFor(lib.id)) !== null ? { width: pct(stFor(lib.id)) + '%' } : {}"></i>
                  </div>
                  <div class="scan-line tiny">
                    <span class="scan-phase"><AppIcon name="scan" :size="12" /> {{ phaseText(stFor(lib.id)) }}</span>
                    <span v-if="stFor(lib.id).current" class="scan-cur truncate">{{ stFor(lib.id).current }}</span>
                    <span v-if="pct(stFor(lib.id)) !== null" class="num">{{ pct(stFor(lib.id)) }}%</span>
                  </div>
                </div>
                <div v-else-if="stFor(lib.id)?.phase === 'done'" class="tiny scan-done">
                  <AppIcon name="check" :size="13" />
                  上次扫描完成:{{ stFor(lib.id).total }} 个文件 · {{ fmtClock(stFor(lib.id).finished_at) }}
                  <span v-if="stFor(lib.id).error" class="scan-warn"> · {{ stFor(lib.id).error }}</span>
                </div>
                <div v-else-if="stFor(lib.id)?.phase === 'error'" class="scan-err">
                  <AppIcon name="alert" :size="14" />
                  扫描失败:{{ stFor(lib.id).error || stFor(lib.id).first_error || '未知错误' }}
                </div>
              </template>
            </div>
          </div>

          <EmptyState v-else icon="film" title="还没有媒体库" text="创建媒体库后扫描,首页就会出现海报墙。" />
        </section>

        <!-- ---------- playback / metadata ---------- -->
        <section v-show="tab === 'playback'" class="stack">
          <div class="card">
            <div class="card-title"><AppIcon name="film" :size="16" /> TMDB 刮削</div>
            <div class="form-grid">
              <div class="field wide">
                <label>API Key</label>
                <input v-model="cfg.tmdb.api_key" placeholder="API 密钥(v3)或 API 读访问令牌(v4)均可" />
                <span class="hint">保存后立即生效,无需重启;国内网络请开启下面的本地代理,或把地址换成镜像。</span>
              </div>
              <div class="field"><label>语言</label><input v-model="cfg.tmdb.language" placeholder="zh-CN" /></div>
              <div class="field">
                <label>API 地址(可填镜像/反代)</label>
                <input v-model="cfg.tmdb.api_base" placeholder="https://api.themoviedb.org/3" />
              </div>
              <div class="field">
                <label>图片地址(可填镜像/反代)</label>
                <input v-model="cfg.tmdb.img_base" placeholder="https://image.tmdb.org/t/p" />
              </div>
              <div class="field wide">
                <label>网络代理</label>
                <label class="check-line">
                  <input type="checkbox" v-model="cfg.tmdb.use_proxy" />
                  走本地代理访问 TMDB(国内直连不通时必开)
                </label>
                <input v-model="cfg.tmdb.proxy" placeholder="socks5://127.0.0.1:7890" :disabled="!cfg.tmdb.use_proxy" />
                <span class="hint">支持 socks5:// 与 http://(Clash / v2rayN 默认端口通常是 7890)。海报与背景图下载也走这个代理。</span>
                <div class="test-line">
                  <button class="btn" :disabled="tmdbTesting" @click="testTMDB">
                    <AppIcon name="wifi" :size="15" /> {{ tmdbTesting ? '测试中…' : '测试连接' }}
                  </button>
                  <span v-if="tmdbTest" class="tiny" :class="tmdbTest.ok ? 'ok-text' : 'err-text'">
                    <AppIcon :name="tmdbTest.ok ? 'check' : 'alert'" :size="13" /> {{ tmdbTest.message }}
                  </span>
                </div>
              </div>
            </div>
          </div>

          <div class="card">
            <div class="card-title"><AppIcon name="sparkles" :size="16" /> Bangumi 刮削(bgm.tv)</div>
            <div class="form-grid">
              <div class="field wide">
                <label>访问令牌(可选)</label>
                <input v-model="cfg.bgm.token" placeholder="留空即可,公开接口够用;填了可提高限流上限" />
                <span class="hint">Bangumi 是番剧信息最全的中文数据源:中文剧名、每集中文标题、播出日期、评分与标签。在媒体库里勾选 <b>Bangumi</b> 才启用。</span>
              </div>
              <div class="field">
                <label>API 地址(可填镜像/反代)</label>
                <input v-model="cfg.bgm.api_base" placeholder="https://api.bgm.tv" />
              </div>
              <div class="field wide">
                <label>网络代理</label>
                <label class="check-line">
                  <input type="checkbox" v-model="cfg.bgm.use_proxy" />
                  走本地代理访问 Bangumi(api.bgm.tv 常被 DNS 污染,建议保持开启)
                </label>
                <input v-model="cfg.bgm.proxy" placeholder="socks5://127.0.0.1:7890" :disabled="!cfg.bgm.use_proxy" />
                <div class="test-line">
                  <button class="btn" :disabled="bgmTesting" @click="testBGM">
                    <AppIcon name="wifi" :size="15" /> {{ bgmTesting ? '测试中…' : '测试连接' }}
                  </button>
                  <input v-model="bgmKeyword" class="input inline-input" placeholder="测试关键词,如:孤独摇滚" />
                  <span v-if="bgmTest" class="tiny" :class="bgmTest.ok ? 'ok-text' : 'err-text'">
                    <AppIcon :name="bgmTest.ok ? 'check' : 'alert'" :size="13" /> {{ bgmTest.message }}
                  </span>
                </div>
                <span v-if="bgmTest && bgmTest.hits && bgmTest.hits.length" class="hint">搜到:{{ bgmTest.hits.join(' / ') }}</span>
              </div>
            </div>
          </div>

          <div class="card">
            <div class="card-title"><AppIcon name="playCircle" :size="16" /> 播放</div>
            <div class="form-grid">
              <div class="field">
                <label>默认播放模式</label>
                <select v-model="cfg.playback.force_mode">
                  <option value="auto">自动决策(推荐)</option>
                  <option value="direct">强制 302 直连</option>
                  <option value="proxy">强制代理</option>
                  <option value="remux">强制 HLS 转封装</option>
                  <option value="transcode">强制 HLS 转码</option>
                </select>
                <span class="hint">自动决策:客户端能直接播的走 302/代理,不兼容的自动转封装(H.264/H.265 视频流直拷,CPU 几乎无消耗)。失败后的备用顺序是 直连 → 代理 → 转封装,而且只会尝试这个客户端真的有可能播的方式。</span>
              </div>
              <div class="field"><label>HLS 分片时长(秒)</label><input v-model="cfg.playback.segment_seconds" type="number" step="1" min="2" max="10" /></div>
              <div class="field"><label>转码最高高度(像素)</label><input v-model="cfg.playback.transcode_height" type="number" step="80" min="480" max="2160" /></div>
              <div class="field">
                <label>探测读取上限(MB)</label>
                <input v-model="cfg.playback.probe_size_mb" type="number" step="1" min="1" max="256" />
                <span class="hint">决定起播前要从网盘读多少数据来识别容器/编码。默认 8MB,越小起播越快;读不出来时会自动用 50MB 深度探测兜底。</span>
              </div>
              <div class="field">
                <label>探测分析时长(秒)</label>
                <input v-model="cfg.playback.probe_analyze_sec" type="number" step="1" min="1" max="60" />
                <span class="hint">探测时分析多少秒的数据流,默认 4 秒。音轨/字幕流很靠后的片源可以调大。</span>
              </div>
              <div class="field wide">
                <label>扫描后预探测</label>
                <label class="check-line">
                  <input type="checkbox" v-model="cfg.playback.preprobe_after_scan" />
                  扫描完成后在后台预探测新文件(打开即播,不用等)
                </label>
                <span class="hint">只探测文件名未缓存过的文件,限流串行执行,不会与播放抢带宽。</span>
              </div>
              <div class="field">
                <label>预探测文件数上限</label>
                <input v-model="cfg.playback.preprobe_max_files" type="number" step="10" min="0" :disabled="!cfg.playback.preprobe_after_scan" />
                <span class="hint">每次扫描最多预探测多少个文件,0 表示不限。大库建议限量,避免把网盘读限速。</span>
              </div>
              <div class="field">
                <label>预探测并发数</label>
                <input v-model="cfg.playback.preprobe_concurrency" type="number" step="1" min="1" max="8" :disabled="!cfg.playback.preprobe_after_scan" />
                <span class="hint">默认 1(串行)。网盘容易限速时不要调大。</span>
              </div>
              <div class="field wide">
                <label>HEVC 直拷(iOS / Apple)</label>
                <label class="check-line">
                  <input type="checkbox" v-model="cfg.playback.apple_hevc_copy" />
                  允许 Apple 设备把 HEVC 直拷进 MPEG-TS
                </label>
                <span class="hint">
                  <b>建议保持关闭</b>：Apple 不解码 MPEG-TS 里的 HEVC,直拷会变成「有声音、没画面」;
                  关闭时这类片子走转码(能看,但吃 CPU)。真机确认能播再打开,可以省下全部转码开销。
                </span>
              </div>
              <div class="field wide">
                <label>会话打包(fMP4,Apple 直拷 HEVC)</label>
                <label class="check-line">
                  <input type="checkbox" v-model="cfg.playback.hls_session_packager" />
                  用长驻 ffmpeg 打包 fMP4,让 Apple 设备直拷 HEVC 而不转码
                </label>
                <span class="hint">
                  Apple 只接受 fMP4 里的 HEVC,这个模式会输出正确的 fMP4(带 #EXT-X-MAP 与 hvc1),
                  于是 HEVC 不再转码、CPU 几乎为零。每个分片只生成一次、不再逐片重启 ffmpeg,
                  也不再来回重读探测窗口。
                </span>
              </div>
              <div class="field">
                <label>分片缓存上限(MB)</label>
                <input v-model="cfg.playback.hls_cache_mb" type="number" step="256" min="0" />
                <span class="hint">
                  打包完成的产物留在 <code>data/cache/hls</code>,同一部片子**再次播放直接秒开**;
                  超出上限时按最近最少使用淘汰,未完成的残留会在启动时清掉。填 0 表示不留缓存
                  (播放结束即删)。默认 2048。
                </span>
              </div>
              <div class="field wide">
                <label>失败后允许自动改用转码</label>
                <label class="check-line">
                  <input type="checkbox" v-model="cfg.playback.transcode_fallback" />
                  直连 / 代理 / 转封装都失败时,再试一次真转码
                </label>
                <span class="hint">
                  <b>默认关闭</b>:转码是实时的重新编码,小服务器本来就跟不上,失败后再切过去只会白吃 CPU
                  并继续转圈。关闭时失败会明确告诉你试过哪几种方式,需要时播放页仍可手动「强制转码」。
                  片源本身只能转码的(如 AV1、10bit H.264)决策照样会选转码,不受这里影响。
                </span>
              </div>
            </div>
          </div>

          <div class="save-bar">
            <span class="tiny">改动保存在 config.json,立即生效</span>
            <button class="btn primary" @click="saveConfig"><AppIcon name="check" :size="15" /> 保存配置</button>
          </div>
        </section>

        <!-- ---------- system ---------- -->
        <section v-show="tab === 'system'" class="stack">
          <div v-if="sys" class="tiles">
            <div class="tile">
              <span class="t-icon"><AppIcon name="server" :size="17" /></span>
              <div><div class="t-k">版本</div><div class="t-v">{{ sys.version }}</div></div>
            </div>
            <div class="tile">
              <span class="t-icon" :class="sys.ffmpeg_ok ? 'ok' : 'err'"><AppIcon name="video" :size="17" /></span>
              <div><div class="t-k">ffmpeg</div><div class="t-v">{{ sys.ffmpeg_ok ? '可用' : '未找到' }}</div></div>
            </div>
            <div class="tile">
              <span class="t-icon" :class="sys.ffprobe_ok ? 'ok' : 'err'"><AppIcon name="scan" :size="17" /></span>
              <div><div class="t-k">ffprobe</div><div class="t-v">{{ sys.ffprobe_ok ? '可用' : '未找到' }}</div></div>
            </div>
            <div class="tile">
              <span class="t-icon" :class="sys.tmdb_ok ? 'ok' : ''"><AppIcon name="sparkles" :size="17" /></span>
              <div><div class="t-k">TMDB</div><div class="t-v">{{ sys.tmdb_ok ? '已配置' : '未配置' }}</div></div>
            </div>
            <div class="tile wide">
              <span class="t-icon"><AppIcon name="hdd" :size="17" /></span>
              <div><div class="t-k">数据目录</div><div class="t-v truncate">{{ sys.data_dir }}</div></div>
            </div>
          </div>

          <div class="card">
            <div class="card-title"><AppIcon name="settings" :size="16" /> 工具路径与性能</div>
            <div class="form-grid">
              <div class="field">
                <label>ffmpeg</label>
                <input v-model="cfg.ffmpeg" />
                <span class="hint">转封装 / 转码与字幕转换都依赖它。找不到时填绝对路径。</span>
              </div>
              <div class="field"><label>ffprobe</label><input v-model="cfg.ffprobe" /></div>
              <div class="field">
                <label>扫描并发数</label>
                <input v-model="cfg.scan_concurrency" type="number" min="1" max="16" />
                <span class="hint">网盘源建议 2–4,本地磁盘可以更高。</span>
              </div>
            </div>
            <div class="save-bar flat">
              <span class="tiny">保存后立即生效</span>
              <button class="btn primary" @click="saveConfig"><AppIcon name="check" :size="15" /> 保存配置</button>
            </div>
          </div>
        </section>
      </div>
    </div>
  </div>

  <div v-else class="page">
    <div class="set-skeleton">
      <div class="sk" style="height:52px;border-radius:var(--r-m)" />
      <div class="sk mt-5" style="height:260px;border-radius:var(--r-l)" />
    </div>
  </div>
</template>

<style scoped>
.settings-page { padding-top: var(--sp-6); }
.set-head {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: var(--sp-4);
  flex-wrap: wrap;
  margin-bottom: var(--sp-5);
}
.status-chips { display: flex; gap: 7px; flex-wrap: wrap; }
.set-layout { display: grid; grid-template-columns: 262px minmax(0, 1fr); gap: var(--sp-6); align-items: start; }

.set-nav { position: sticky; top: calc(var(--header-h) + 20px); display: flex; flex-direction: column; gap: 4px; }
.nav-item {
  display: flex; align-items: center; gap: 12px;
  padding: 11px 13px;
  border-radius: var(--r-m);
  text-align: left;
  color: var(--text-3);
  transition: all var(--t-fast);
  border: 1px solid transparent;
}
.nav-item:hover { background: var(--bg-1); color: var(--text-1); }
.nav-item.on { background: var(--bg-2); border-color: var(--line-2); color: var(--text-1); }
.nav-item.on .ni-icon { color: var(--brand); background: var(--brand-tint); border-color: rgba(240, 169, 46, 0.24); }
.ni-icon {
  display: grid; place-items: center;
  width: 34px; height: 34px; flex: none;
  border-radius: var(--r-s);
  background: rgba(255, 255, 255, 0.05);
  border: 1px solid var(--line-1);
  color: var(--text-3);
}
.ni-text { display: flex; flex-direction: column; min-width: 0; }
.ni-label { font-size: var(--fs-14); font-weight: 600; }
.ni-desc { font-size: var(--fs-11); color: var(--text-4); }

.set-body { min-width: 0; }
.card-head { display: flex; align-items: flex-start; justify-content: space-between; gap: var(--sp-4); }
.add-panel { margin-top: var(--sp-4); padding-top: var(--sp-4); border-top: 1px solid var(--line-1); }

.type-cards { display: grid; grid-template-columns: repeat(auto-fit, minmax(180px, 1fr)); gap: var(--sp-3); margin-bottom: var(--sp-4); }
.type-card {
  display: flex; flex-direction: column; gap: 4px; align-items: flex-start;
  padding: 14px;
  border-radius: var(--r-m);
  border: 1px solid var(--line-1);
  background: var(--bg-1);
  color: var(--text-3);
  transition: all var(--t-fast);
  text-align: left;
}
.type-card:hover { border-color: var(--line-3); color: var(--text-1); }
.type-card.on { border-color: var(--brand); background: var(--brand-tint); color: var(--text-1); }
.type-card.on svg { color: var(--brand); }
.tc-label { font-size: var(--fs-13); font-weight: 650; }

.btn-row { display: flex; gap: 10px; flex-wrap: wrap; }
.list { display: flex; flex-direction: column; gap: var(--sp-3); }
.row-card {
  display: flex; align-items: center; gap: 14px;
  padding: 14px 16px;
  border-radius: var(--r-m);
  border: 1px solid var(--line-1);
  background: var(--bg-1);
  transition: border-color var(--t-fast);
}
.row-card:hover { border-color: var(--line-2); }
.r-icon {
  display: grid; place-items: center;
  width: 40px; height: 40px; flex: none;
  border-radius: var(--r-m);
  background: rgba(255, 255, 255, 0.05);
  border: 1px solid var(--line-1);
  color: var(--brand);
}
.r-main { flex: 1; min-width: 0; }
.r-title { display: flex; align-items: center; gap: 8px; font-size: var(--fs-15); font-weight: 650; margin-bottom: 2px; }

.lib-card { padding: var(--sp-4) var(--sp-5); }
.lib-line { display: flex; align-items: center; gap: 14px; }
.lib-actions { display: flex; gap: 8px; flex: none; flex-wrap: wrap; }
.scan-live { margin-top: 14px; }
.scan-line { margin-top: 8px; display: flex; gap: 14px; align-items: center; flex-wrap: wrap; }
.scan-phase { display: inline-flex; align-items: center; gap: 6px; color: var(--brand); font-weight: 600; }
.scan-cur { color: var(--text-4); max-width: 46%; direction: rtl; text-align: left; }
.scan-done { margin-top: 10px; display: flex; align-items: center; gap: 6px; color: var(--text-3); }
.scan-done svg { color: var(--ok); }
.scan-warn { color: var(--warn); }
.scan-err { margin-top: 10px; display: flex; align-items: center; gap: 7px; color: var(--danger); font-weight: 600; font-size: var(--fs-13); }

.type-picker { display: flex; gap: 8px; flex-wrap: wrap; }
.test-line { display: flex; align-items: center; gap: 10px; margin-top: 10px; flex-wrap: wrap; }
.inline-input { max-width: 220px; min-height: 36px; height: 36px; }
.ok-text { display: inline-flex; align-items: center; gap: 6px; color: #86efac; }
.err-text { display: inline-flex; align-items: center; gap: 6px; color: #ffb3b3; }

.tiles { display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: var(--sp-3); }
.tile {
  display: flex; align-items: center; gap: 13px;
  padding: 15px 16px;
  border-radius: var(--r-m);
  border: 1px solid var(--line-1);
  background: linear-gradient(180deg, rgba(255, 255, 255, 0.035), rgba(255, 255, 255, 0)), var(--bg-1);
}
.tile.wide { grid-column: 1 / -1; }
.t-icon {
  display: grid; place-items: center;
  width: 38px; height: 38px; flex: none;
  border-radius: var(--r-m);
  background: rgba(255, 255, 255, 0.05);
  border: 1px solid var(--line-1);
  color: var(--text-3);
}
.t-icon.ok { color: var(--ok); background: var(--ok-soft); border-color: rgba(74, 222, 128, 0.28); }
.t-icon.err { color: var(--danger); background: var(--danger-soft); border-color: rgba(255, 107, 107, 0.3); }
.t-k { font-size: var(--fs-11); letter-spacing: 0.08em; text-transform: uppercase; color: var(--text-4); font-weight: 700; }
.t-v { font-size: var(--fs-14); font-weight: 600; }

.save-bar {
  position: sticky;
  bottom: calc(16px + env(safe-area-inset-bottom));
  display: flex; align-items: center; justify-content: space-between; gap: var(--sp-4);
  padding: 12px 18px;
  border-radius: var(--r-m);
  background: rgba(16, 18, 23, 0.94);
  border: 1px solid var(--line-2);
  box-shadow: var(--sh-3);
  backdrop-filter: blur(18px);
  z-index: 400;
}
.save-bar.flat { position: static; margin-top: var(--sp-4); background: var(--bg-0); box-shadow: none; }

.set-skeleton { padding-top: calc(var(--header-h) + 40px); }

@media (max-width: 1080px) {
  .set-layout { grid-template-columns: 1fr; gap: var(--sp-5); }
  .set-nav {
    position: static;
    flex-direction: row;
    overflow-x: auto;
    padding-bottom: 4px;
    gap: 6px;
    scrollbar-width: none;
  }
  .set-nav::-webkit-scrollbar { display: none; }
  .nav-item { flex: none; padding: 9px 13px; }
  .ni-desc { display: none; }
}
@media (max-width: 720px) {
  .lib-line { flex-wrap: wrap; }
  .lib-actions { width: 100%; }
  .settings-page { padding-bottom: calc(96px + env(safe-area-inset-bottom)); }
}
</style>
