/**
 * Site content store — the single source of truth the pages render from.
 *
 * The document the pages read (`site.doc`) is the *working* copy: while the
 * toolbar is open the owner's edits land there and the page redraws immediately,
 * which is the whole point of the toolbar. `site.saved` keeps whatever the server
 * last confirmed, so "未发布" and "放弃修改" are honest, and a draft is mirrored
 * into localStorage so a refresh does not throw away half-finished wording.
 */
import { reactive, ref, computed, watch, nextTick } from 'vue'
import { api } from '../api.js'
import { DEFAULTS, clone, mergeDeep, getPath, setPath, fill } from './schema.js'

const DRAFT_KEY = 'dm.site.draft'
const TOOLBAR_KEY = 'dm.site.toolbar'

export const site = reactive({
  doc: clone(DEFAULTS),    // what the pages render right now
  saved: clone(DEFAULTS),  // what the server holds
  revision: 0,
  updatedAt: '',
  updatedBy: '',
  loaded: false,
  canEdit: false,
  saving: false,
  error: '',
})

/** Values the copy can interpolate: {host}, {version}, {site}, {user}. */
export const ctx = reactive({ host: location.host, version: '', site: DEFAULTS.site.title, user: '' })

// {site} always means the current site name, so renaming the site updates every
// line that mentions it.
watch(() => site.doc.site.title, (t) => { ctx.site = t || DEFAULTS.site.title }, { immediate: true })

/** Toolbar visibility (admin only) and whether the editing affordances show. */
export const toolbarOn = ref(false)
export const editMode = ref(true)
export const panelTab = ref('')
export const hoverKey = ref('')
export const activeKey = ref('')

export const dirty = computed(() => JSON.stringify(site.doc) !== JSON.stringify(site.saved))
export const canUndo = ref(false)
export const canRedo = ref(false)

/**
 * Bumped whenever the working copy is replaced wholesale (undo, redo, discard,
 * a fresh load). Pages use it to redraw text they would otherwise leave alone
 * because the caret is sitting in it.
 */
export const docEpoch = ref(0)

// ---------------------------------------------------------------- history ---

let past = []
let future = []
let lastSnap = JSON.stringify(site.doc)
let commitTimer = null
let applying = false

function syncHistoryFlags() {
  canUndo.value = past.length > 0
  canRedo.value = future.length > 0
}

/** Typing commits as one step, not one per keystroke. */
function scheduleCommit() {
  clearTimeout(commitTimer)
  commitTimer = setTimeout(commit, 500)
}

function commit() {
  clearTimeout(commitTimer)
  const snap = JSON.stringify(site.doc)
  if (snap === lastSnap) return
  past.push(lastSnap)
  if (past.length > 80) past.shift()
  future = []
  lastSnap = snap
  syncHistoryFlags()
  stashDraft()
}

async function applySnapshot(snap) {
  applying = true
  site.doc = mergeDeep(DEFAULTS, JSON.parse(snap))
  lastSnap = snap
  docEpoch.value++
  await nextTick()
  applying = false
  stashDraft()
}

watch(() => site.doc, () => {
  if (applying) return
  // The history step waits for a pause in typing; the draft never does, so a
  // refresh (or a navigation) cannot eat the last edit.
  stashDraft()
  scheduleCommit()
}, { deep: true })

export async function undo() {
  commit()
  if (!past.length) return
  future.push(lastSnap)
  await applySnapshot(past.pop())
  syncHistoryFlags()
}

export async function redo() {
  commit()
  if (!future.length) return
  past.push(lastSnap)
  await applySnapshot(future.pop())
  syncHistoryFlags()
}

// ----------------------------------------------------------------- drafts ---

function stashDraft() {
  if (!site.canEdit || !toolbarOn.value) return
  try {
    localStorage.setItem(DRAFT_KEY, JSON.stringify({ revision: site.revision, doc: site.doc }))
  } catch {}
}

function clearDraft() {
  try { localStorage.removeItem(DRAFT_KEY) } catch {}
}

/** Bring back work that was never published, unless the site moved on without us. */
function restoreDraft() {
  let saved = null
  try { saved = JSON.parse(localStorage.getItem(DRAFT_KEY) || 'null') } catch {}
  if (!saved || typeof saved !== 'object') return
  if (saved.revision !== site.revision) {
    // Someone published in the meantime; their version wins over a stale draft.
    clearDraft()
    return
  }
  const doc = mergeDeep(DEFAULTS, saved.doc)
  if (JSON.stringify(doc) === JSON.stringify(site.saved)) {
    clearDraft()
    return
  }
  site.doc = doc
  lastSnap = JSON.stringify(site.saved)
  stashDraft()
}

// ------------------------------------------------------------ load / save ---

function adopt(res) {
  const content = res?.content ? mergeDeep(DEFAULTS, res.content) : clone(DEFAULTS)
  site.revision = Number(res?.revision || 0)
  site.updatedAt = res?.updated_at || ''
  site.updatedBy = res?.updated_by || ''
  site.canEdit = !!res?.can_edit
  site.saved = clone(content)
  site.doc = clone(content)
  past = []
  future = []
  lastSnap = JSON.stringify(content)
  docEpoch.value++
  syncHistoryFlags()
}

/** Read the published site. Safe to call before the app mounts. */
export async function loadSite() {
  try {
    adopt(await api.get('/api/site'))
  } catch (e) {
    site.error = e.message
  }
  site.loaded = true
  // The toolbar flag survives a reload; ?toolbar=1 turns it on for good.
  const asked = new URLSearchParams(location.search).has('toolbar')
  if (asked) {
    try { localStorage.setItem(TOOLBAR_KEY, '1') } catch {}
  }
  toolbarOn.value = asked || localStorage.getItem(TOOLBAR_KEY) === '1'
  if (toolbarOn.value && site.canEdit) {
    panelTab.value = panelTab.value || 'copy'
    restoreDraft()
  }
  return site
}

export function setToolbar(on) {
  toolbarOn.value = on
  try { on ? localStorage.setItem(TOOLBAR_KEY, '1') : localStorage.removeItem(TOOLBAR_KEY) } catch {}
  if (!on) {
    panelTab.value = ''
    hoverKey.value = ''
  }
  if (on && !panelTab.value) panelTab.value = 'copy'
  if (on) stashDraft()
}

/** Publish the working copy so every visitor sees it. */
export async function saveSite() {
  commit()
  site.saving = true
  site.error = ''
  try {
    const res = await api.put('/api/site', { content: site.doc, revision: site.revision })
    site.revision = Number(res?.revision || site.revision + 1)
    site.updatedAt = res?.updated_at || ''
    site.updatedBy = res?.updated_by || ''
    site.saved = clone(site.doc)
    clearDraft()
    return { ok: true }
  } catch (e) {
    site.error = e.message
    return { ok: false, error: e.message }
  } finally {
    site.saving = false
  }
}

/** Throw away unpublished edits and go back to what the server has. */
export function discardChanges() {
  site.doc = clone(site.saved)
  lastSnap = JSON.stringify(site.saved)
  past = []
  future = []
  docEpoch.value++
  syncHistoryFlags()
  clearDraft()
}

/** Drop every saved word and go back to the wording the build shipped with. */
export async function resetSite() {
  site.saving = true
  site.error = ''
  try {
    const res = await api.del('/api/site')
    adopt({ ...res, content: null })
    clearDraft()
    return { ok: true }
  } catch (e) {
    site.error = e.message
    return { ok: false, error: e.message }
  } finally {
    site.saving = false
  }
}

/** Re-read from the server (used after a revision conflict). */
export async function reloadSite() {
  try {
    adopt(await api.get('/api/site'))
    clearDraft()
  } catch (e) {
    site.error = e.message
  }
}

// ------------------------------------------------------------- accessors ---

export function valueOf(path) {
  const v = getPath(site.doc, path)
  return v === undefined || v === null ? '' : String(v)
}

/** One line about the published state, shared by the toolbar and the settings card. */
export function publishedLabel() {
  if (!site.updatedAt) return '尚未发布过修改,访客看到的是内置文案'
  const d = new Date(site.updatedAt)
  if (Number.isNaN(d.getTime())) return '已发布'
  const p = (n) => String(n).padStart(2, '0')
  const when = `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
  return `上次发布 ${when}${site.updatedBy ? ` · ${site.updatedBy}` : ''}`
}

export function rawOf(path) {
  const v = getPath(site.doc, path)
  return v === undefined ? getPath(DEFAULTS, path) : v
}

export function setField(path, value) {
  setPath(site.doc, path, value)
}

/** Copy as it should read: placeholders resolved, {host} and friends filled in. */
export function text(path) {
  return fill(String(valueOf(path)), ctx)
}

/** A list from the document, guaranteed to be an array. */
export function listOf(path) {
  const v = getPath(site.doc, path)
  return Array.isArray(v) ? v : []
}

export function addItem(path, item) {
  const arr = listOf(path)
  arr.push(item)
  setPath(site.doc, path, arr.slice())
}

export function removeItem(path, index) {
  const arr = listOf(path)
  arr.splice(index, 1)
  setPath(site.doc, path, arr.slice())
}

export function moveItem(path, index, delta) {
  const arr = listOf(path)
  const to = index + delta
  if (to < 0 || to >= arr.length) return
  const [it] = arr.splice(index, 1)
  arr.splice(to, 0, it)
  setPath(site.doc, path, arr.slice())
}

/** Scroll to the element a field renders as, and flash it. */
export async function revealOnPage(key) {
  activeKey.value = key
  await nextTick()
  const el = document.querySelector(`[data-site-key="${CSS.escape(key)}"]`)
  if (el) el.scrollIntoView({ block: 'center', behavior: 'smooth' })
}
