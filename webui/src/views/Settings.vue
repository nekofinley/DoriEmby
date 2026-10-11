<script setup>
import { ref, onMounted, onBeforeUnmount } from 'vue'
import { api } from '../api.js'

const tab = ref('sources')
const sources = ref([])
const libraries = ref([])
const scanStates = ref([])
const sys = ref(null)
const cfg = ref(null)
const msg = ref('')
const error = ref('')

let pollTimer = null

// new source form
const srcType = ref('openlist')
const srcForm = ref({ name: '', root: '', webdav_url: '', webdav_user: '', webdav_pass: '', base_url: '', token: '' })
const testResult = ref(null)
const testing = ref(false)

// new library form
const libForm = ref({ name: '', type: 'movie', source_id: '', path: '/', providers: ['nfo', 'tmdb'] })

function buildSourceConfig() {
  const f = srcForm.value
  if (srcType.value === 'local') return { root: f.root }
  if (srcType.value === 'webdav') return { webdav_url: f.webdav_url, webdav_user: f.webdav_user, webdav_pass: f.webdav_pass }
  return { base_url: f.base_url, token: f.token }
}

async function loadAll() {
  try {
    sources.value = await api.get('/api/sources') || []
    libraries.value = await api.get('/api/libraries') || []
    scanStates.value = await api.get('/api/scan/status') || []
    sys.value = await api.get('/api/system/info')
    cfg.value = await api.get('/api/config')
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
  if (!provs.length) provs = ['nfo', 'tmdb']
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
      name: f.name, type: f.type, path: f.path || '/', providers: JSON.stringify(f.providers.length ? f.providers : ['nfo', 'tmdb']),
    })
    editing.value = null
    await loadAll()
    flash('媒体库已更新(改路径后记得重新扫描)')
  } catch (e) { flash(e.message, true) }
}

async function stopScan(id) {
  try { await api.post(`/api/libraries/${id}/scan/stop`) } catch (e) { flash(e.message, true) }
}

function stFor(id) {
  return (scanStates.value || []).find(s => s.library_id === id)
}
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

onMounted(async () => {
  await loadAll()
  pollTimer = setInterval(async () => {
    try { scanStates.value = await api.get('/api/scan/status') } catch {}
  }, 2000)
})
onBeforeUnmount(() => clearInterval(pollTimer))

function flash(text, isErr) {
  if (isErr) error.value = text
  else msg.value = text
  setTimeout(() => { if (!isErr) msg.value = ''; else error.value = '' }, 4000)
}

async function addSource() {
  try {
    await api.post('/api/sources', { name: srcForm.value.name || '未命名源', type: srcType.value, config: buildSourceConfig() })
    srcForm.value = { name: '', root: '', webdav_url: '', webdav_user: '', webdav_pass: '', base_url: '', token: '' }
    await loadAll()
    flash('存储源已添加')
  } catch (e) { flash(e.message, true) }
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

async function delSource(id) {
  if (!confirm('删除该存储源?其下媒体库也会被删除。')) return
  try { await api.del(`/api/sources/${id}`); await loadAll() } catch (e) { flash(e.message, true) }
}

async function addLibrary() {
  try {
    const f = libForm.value
    await api.post('/api/libraries', {
      name: f.name, type: f.type, source_id: Number(f.source_id), path: f.path || '/',
      providers: JSON.stringify(f.providers.length ? f.providers : ['nfo', 'tmdb']),
    })
    libForm.value = { name: '', type: 'movie', source_id: f.source_id, path: '/', providers: ['nfo', 'tmdb'] }
    await loadAll()
    flash('媒体库已创建,记得点扫描')
  } catch (e) { flash(e.message, true) }
}

async function scanLibrary(id) {
  try {
    await api.post(`/api/libraries/${id}/scan`)
    flash('扫描已开始(含刮削,后台进行)')
  } catch (e) { flash(e.message, true) }
}

async function delLibrary(id) {
  if (!confirm('删除该媒体库及其所有条目?')) return
  try { await api.del(`/api/libraries/${id}`); await loadAll() } catch (e) { flash(e.message, true) }
}

function stateOf(libId) {
  return (scanStates.value || []).find(s => s.library_id === libId)
}

async function saveConfig() {
  try {
    await api.put('/api/config', {
      ffmpeg: cfg.value.ffmpeg,
      ffprobe: cfg.value.ffprobe,
      scan_concurrency: Number(cfg.value.scan_concurrency),
      tmdb: cfg.value.tmdb,
      playback: cfg.value.playback,
    })
    flash('配置已保存')
    await loadAll()
  } catch (e) { flash(e.message, true) }
}
</script>

<template>
  <main class="page" v-if="cfg">
    <h1>设置</h1>
    <div class="tabs">
      <button class="tab" :class="{ on: tab === 'sources' }" @click="tab = 'sources'">存储源</button>
      <button class="tab" :class="{ on: tab === 'libraries' }" @click="tab = 'libraries'">媒体库</button>
      <button class="tab" :class="{ on: tab === 'playback' }" @click="tab = 'playback'">刮削与播放</button>
      <button class="tab" :class="{ on: tab === 'system' }" @click="tab = 'system'">系统</button>
    </div>

    <div v-if="msg" class="flash ok">{{ msg }}</div>
    <div v-if="error" class="flash err">{{ error }}</div>

    <!-- SOURCES -->
    <section v-show="tab === 'sources'">
      <div class="card" style="margin-bottom:18px">
        <h2>添加存储源</h2>
        <div class="type-picker">
          <button class="chip" :class="{ on: srcType === 'openlist' }" @click="srcType = 'openlist'">OpenList / AList</button>
          <button class="chip" :class="{ on: srcType === 'webdav' }" @click="srcType = 'webdav'">WebDAV</button>
          <button class="chip" :class="{ on: srcType === 'local' }" @click="srcType = 'local'">本地目录</button>
        </div>
        <div class="form-grid">
          <div class="field"><label>名称</label><input v-model="srcForm.name" placeholder="我的天翼云盘" /></div>
          <template v-if="srcType === 'openlist'">
            <div class="field"><label>OpenList 地址</label><input v-model="srcForm.base_url" placeholder="http://127.0.0.1:5244" /></div>
            <div class="field"><label>Token(可选,建议填管理员 token)</label><input v-model="srcForm.token" placeholder="alist-xxxx" /></div>
          </template>
          <template v-else-if="srcType === 'webdav'">
            <div class="field"><label>WebDAV 地址</label><input v-model="srcForm.webdav_url" placeholder="http://127.0.0.1:5244/dav" /></div>
            <div class="field"><label>用户名</label><input v-model="srcForm.webdav_user" /></div>
            <div class="field"><label>密码</label><input v-model="srcForm.webdav_pass" type="password" /></div>
          </template>
          <template v-else>
            <div class="field"><label>服务器上的绝对路径</label><input v-model="srcForm.root" placeholder="D:\media 或 /mnt/media" /></div>
          </template>
        </div>
        <div class="btn-row">
          <button class="btn" @click="testSource" :disabled="testing">{{ testing ? '测试中…' : '测试连接' }}</button>
          <button class="btn primary" @click="addSource">添加</button>
        </div>
        <div v-if="testResult" class="tiny" style="margin-top:8px">
          <template v-if="testResult.ok">✓ 连接成功,根目录 {{ testResult.count }} 项:{{ (testResult.entries || []).slice(0, 6).join(', ') }}</template>
          <template v-else>✗ {{ testResult.error }}</template>
        </div>
      </div>

      <div v-for="s in sources" :key="s.id" class="card row-item">
        <div>
          <div class="ri-name">{{ s.name }} <span class="badge">{{ s.type }}</span></div>
          <div class="tiny">{{ s.config.base_url || s.config.webdav_url || s.config.root || '-' }}</div>
        </div>
        <div class="spacer"></div>
        <button class="btn danger" @click="delSource(s.id)">删除</button>
      </div>
    </section>

    <!-- LIBRARIES -->
    <section v-show="tab === 'libraries'">
      <div class="card" style="margin-bottom:18px">
        <h2>添加媒体库</h2>
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
          <div class="field"><label>路径(源内路径)</label><input v-model="libForm.path" placeholder="/movies" /></div>
        </div>
        <div class="field">
          <label>刮削来源(按优先级)</label>
          <div class="type-picker">
            <button class="chip" :class="{ on: libForm.providers.includes('nfo') }"
              @click="libForm.providers.includes('nfo') ? libForm.providers.splice(libForm.providers.indexOf('nfo'), 1) : libForm.providers.push('nfo')">
              本地 nfo/海报
            </button>
            <button class="chip" :class="{ on: libForm.providers.includes('tmdb') }"
              @click="libForm.providers.includes('tmdb') ? libForm.providers.splice(libForm.providers.indexOf('tmdb'), 1) : libForm.providers.push('tmdb')">
              TMDB
            </button>
          </div>
        </div>
        <button class="btn primary" @click="addLibrary" :disabled="!libForm.source_id || !libForm.name">创建媒体库</button>
      </div>

      <div v-for="lib in libraries" :key="lib.id" class="card lib-card">
        <!-- edit mode -->
        <template v-if="editing === lib.id">
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
              <button class="chip" :class="{ on: editForm.providers.includes('nfo') }" @click="toggleProvider('nfo')">本地 nfo/海报</button>
              <button class="chip" :class="{ on: editForm.providers.includes('tmdb') }" @click="toggleProvider('tmdb')">TMDB</button>
            </div>
          </div>
          <div class="btn-row">
            <button class="btn primary" @click="saveEdit(lib.id)">保存</button>
            <button class="btn" @click="editing = null">取消</button>
          </div>
        </template>

        <!-- display mode -->
        <template v-else>
          <div class="lib-line1">
            <div>
              <div class="ri-name">{{ lib.name }} <span class="badge">{{ lib.type === 'movie' ? '电影' : '剧集' }}</span></div>
              <div class="tiny">源 #{{ lib.source_id }} · 路径 {{ lib.path }} · 刮削 {{ lib.providers }}</div>
            </div>
            <div class="spacer"></div>
            <button v-if="stFor(lib.id) && stFor(lib.id).running" class="btn" @click="stopScan(lib.id)">停止</button>
            <button v-else class="btn" @click="scanLibrary(lib.id)">扫描</button>
            <button class="btn" @click="startEdit(lib)">编辑</button>
            <button class="btn danger" @click="delLibrary(lib.id)">删除</button>
          </div>

          <div v-if="stFor(lib.id) && stFor(lib.id).running" class="scan-live">
            <div class="scan-bar" :class="{ indet: pct(stFor(lib.id)) === null }">
              <i :style="pct(stFor(lib.id)) !== null ? { width: pct(stFor(lib.id)) + '%' } : {}"></i>
            </div>
            <div class="scan-line tiny">
              <span class="scan-phase">{{ phaseText(stFor(lib.id)) }}</span>
              <span v-if="stFor(lib.id).current" class="scan-cur">{{ stFor(lib.id).current }}</span>
            </div>
          </div>
          <div v-else-if="stFor(lib.id) && stFor(lib.id).phase === 'done'" class="tiny scan-done">
            ✓ 上次扫描完成:{{ stFor(lib.id).total }} 个文件 · {{ fmtClock(stFor(lib.id).finished_at) }}
            <span v-if="stFor(lib.id).error" class="scan-warn"> · {{ stFor(lib.id).error }}</span>
          </div>
          <div v-else-if="stFor(lib.id) && stFor(lib.id).phase === 'error'" class="scan-err">
            ✗ 扫描失败:{{ stFor(lib.id).error || stFor(lib.id).first_error || '未知错误' }}
          </div>
        </template>
      </div>
    </section>

    <!-- PLAYBACK / METADATA -->
    <section v-show="tab === 'playback'">
      <div class="card" style="margin-bottom:18px">
        <h2>TMDB 刮削</h2>
        <div class="form-grid">
          <div class="field">
            <label>API Key</label>
            <input v-model="cfg.tmdb.api_key" placeholder="API 密钥(v3)或 API 读访问令牌(v4)均可" />
            <span class="hint">设置→刮削与播放 里保存后立即生效,无需重启;国内网络可把下面两个地址换成镜像。</span>
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
        </div>
      </div>
      <div class="card">
        <h2>播放</h2>
        <div class="form-grid">
          <div class="field">
            <label>默认播放模式(一般保持 auto)</label>
            <select v-model="cfg.playback.force_mode">
              <option value="auto">自动决策(推荐)</option>
              <option value="direct">强制 302 直连</option>
              <option value="proxy">强制代理</option>
              <option value="remux">强制 HLS 转封装</option>
              <option value="transcode">强制 HLS 转码</option>
            </select>
            <span class="hint">自动决策:客户端能直接播的走 302/代理,不兼容的自动转封装(H.264/H.265 视频流直拷,CPU 几乎无消耗)。</span>
          </div>
          <div class="field"><label>HLS 分片时长(秒)</label><input v-model="cfg.playback.segment_seconds" type="number" step="1" min="2" max="10" /></div>
          <div class="field"><label>转码最高高度(像素)</label><input v-model="cfg.playback.transcode_height" type="number" step="80" min="480" max="2160" /></div>
        </div>
      </div>
      <button class="btn primary" style="margin-top:16px" @click="saveConfig">保存配置</button>
    </section>

    <!-- SYSTEM -->
    <section v-show="tab === 'system'">
      <div class="card" v-if="sys">
        <h2>系统状态</h2>
        <div class="sys-grid">
          <div>版本</div><div>{{ sys.version }}</div>
          <div>ffmpeg</div><div>{{ sys.ffmpeg_ok ? '✓ 可用' : '✗ 未找到(转封装/转码需要)' }}</div>
          <div>ffprobe</div><div>{{ sys.ffprobe_ok ? '✓ 可用' : '✗ 未找到(媒体探测需要)' }}</div>
          <div>TMDB</div><div>{{ sys.tmdb_ok ? '✓ 已配置' : '未配置(仅使用本地 nfo)' }}</div>
          <div>数据目录</div><div>{{ sys.data_dir }}</div>
        </div>
      </div>
      <div class="card" style="margin-top:18px">
        <h2>工具路径</h2>
        <div class="form-grid">
          <div class="field"><label>ffmpeg</label><input v-model="cfg.ffmpeg" /></div>
          <div class="field"><label>ffprobe</label><input v-model="cfg.ffprobe" /></div>
          <div class="field"><label>扫描并发数</label><input v-model="cfg.scan_concurrency" type="number" min="1" max="16" /></div>
        </div>
        <button class="btn primary" @click="saveConfig">保存配置</button>
      </div>
    </section>
  </main>
</template>

<style scoped>
.tabs { display: flex; gap: 6px; margin: 18px 0 22px; flex-wrap: wrap; }
.tab {
  background: transparent;
  border: 1px solid transparent;
  color: var(--text-3);
  padding: 8px 14px;
  border-radius: 999px;
}
.tab.on { background: var(--bg-2); color: var(--text-1); border-color: var(--border-1); }
.type-picker { display: flex; gap: 8px; margin-bottom: 16px; flex-wrap: wrap; }
.chip {
  background: var(--bg-2);
  border: 1px solid var(--border-1);
  color: var(--text-3);
  border-radius: 999px;
  padding: 6px 14px;
  font-size: 13px;
}
.chip.on { background: var(--accent); color: #fff; border-color: transparent; }
.form-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
  gap: 0 18px;
}
.btn-row { display: flex; gap: 10px; }
.row-item {
  display: flex; align-items: center; gap: 14px;
  margin-bottom: 10px; padding: 14px 18px;
}
.ri-name { font-weight: 600; margin-bottom: 3px; }
.spacer { flex: 1; }
.scanning { color: var(--accent); }
.lib-card { padding: 14px 18px; margin-bottom: 12px; }
.lib-line1 { display: flex; align-items: center; gap: 10px; }
.lib-line1 .spacer { flex: 1; }
.scan-live { margin-top: 12px; }
.scan-bar { height: 5px; background: var(--bg-3); border-radius: 4px; overflow: hidden; }
.scan-bar i { display: block; height: 100%; background: var(--accent); transition: width 0.4s; }
.scan-bar.indet i { width: 30%; animation: scanIndet 1.3s ease-in-out infinite; }
@keyframes scanIndet { 0% { margin-left: -30%; } 100% { margin-left: 100%; } }
.scan-line { margin-top: 7px; display: flex; gap: 14px; align-items: center; flex-wrap: wrap; }
.scan-phase { color: var(--accent); font-weight: 600; }
.scan-cur { color: var(--text-4); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 62%; direction: rtl; text-align: left; }
.scan-warn { color: var(--warn); }
.scan-done { margin-top: 9px; color: var(--text-3); }
.scan-err { margin-top: 9px; color: var(--danger); font-weight: 600; }
.btn-row { display: flex; gap: 10px; }
.flash {
  border-radius: var(--radius-s);
  padding: 10px 14px;
  margin-bottom: 14px;
}
.flash.ok { background: rgba(39, 166, 68, 0.1); border: 1px solid rgba(39, 166, 68, 0.3); color: #7ee787; }
.flash.err { background: rgba(235, 87, 87, 0.1); border: 1px solid rgba(235, 87, 87, 0.3); color: #ff8a8a; }
.sys-grid { display: grid; grid-template-columns: 160px 1fr; gap: 8px 16px; color: var(--text-2); }
</style>
