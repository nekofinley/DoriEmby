<script setup>
/**
 * The editing toolbar: a pill in the corner of the page, plus the panel that
 * opens from it.
 *
 * Everything it does is local until 发布 — the pages render the working copy, so
 * an edit is visible the moment it is typed, and a visitor never sees it until
 * the admin publishes.
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { toast } from '../toast.js'
import AppIcon from './AppIcon.vue'
import {
  site, toolbarOn, setToolbar, editMode, panelTab, dirty, canUndo, canRedo,
  undo, redo, saveSite, discardChanges, resetSite, reloadSite, publishedLabel,
} from '../site/store.js'
import CopyPanel from './site/CopyPanel.vue'
import LayoutPanel from './site/LayoutPanel.vue'
import FooterPanel from './site/FooterPanel.vue'

const route = useRoute()

const visible = computed(() => toolbarOn.value && site.canEdit && !route.meta.fullscreen)
// ?toolbar=1 without an admin session: say so instead of showing nothing.
const gated = computed(() => toolbarOn.value && !site.canEdit && site.loaded && !route.meta.fullscreen)

const TABS = [
  { id: 'copy', label: '文案', icon: 'type' },
  { id: 'layout', label: '布局', icon: 'layout' },
  { id: 'footer', label: '页脚', icon: 'list' },
]

const status = computed(() => publishedLabel())
/** Nothing to publish means nothing to press; "已发布" would be a lie before that. */
const publishLabel = computed(() => {
  if (site.saving) return '发布中'
  if (dirty.value || !site.updatedAt) return '发布'
  return '已发布'
})
const publishHint = computed(() => {
  if (!dirty.value) return site.updatedAt ? status.value : '还没有改动需要发布'
  return `发布给所有访客 (Ctrl+S) · ${status.value}`
})

function toggleTab(id) {
  panelTab.value = panelTab.value === id ? '' : id
}

async function publish() {
  const r = await saveSite()
  if (r.ok) {
    toast.ok('已发布,所有访客现在都能看到')
  } else if (/其他窗口|已被/.test(r.error || '')) {
    toast.err('有人在这期间改过站点内容,正在拉取最新版本', 4200)
    await reloadSite()
  } else {
    toast.err(`发布失败:${r.error || '未知错误'}`, 4200)
  }
}

function discard() {
  if (!dirty.value) return
  discardChanges()
  toast.info('已放弃未发布的修改')
}

async function resetAll() {
  if (!confirm('恢复到内置文案?\n\n已发布的文案、布局与页脚设置都会被清空,且不可撤销。')) return
  const r = await resetSite()
  if (r.ok) toast.ok('已恢复成内置文案')
  else toast.err(`恢复失败:${r.error || '未知错误'}`, 4200)
}

function close() {
  setToolbar(false)
}

function onKey(e) {
  const mod = e.metaKey || e.ctrlKey
  const key = (e.key || '').toLowerCase()
  if (mod && key === 's' && visible.value) {
    e.preventDefault()
    publish()
    return
  }
  if (e.key === 'Escape') {
    if (panelTab.value) panelTab.value = ''
    else if (gated.value) close()
    return
  }
  if (!visible.value) return
  // Undo and redo always mean the site document: the panel's inputs are bound to
  // it, and a contenteditable here holds text this store owns, so the browser's
  // own undo would fight the document instead of helping.
  if (mod && key === 'z') {
    e.preventDefault()
    e.shiftKey ? redo() : undo()
  } else if (mod && key === 'y') {
    e.preventDefault()
    redo()
  }
}

onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))

// The dock sits above the pill, and the pill grows a second row on a phone, so
// its measured height is handed to the stylesheet instead of guessing.
const pillEl = ref(null)
let pillObserver = null

function trackPill() {
  pillObserver?.disconnect()
  pillObserver = null
  const el = pillEl.value
  if (!el) {
    document.documentElement.style.removeProperty('--st-pill-h')
    return
  }
  const apply = () => document.documentElement.style.setProperty('--st-pill-h', `${el.offsetHeight}px`)
  apply()
  pillObserver = new ResizeObserver(apply)
  pillObserver.observe(el)
}

watch(visible, () => nextTick(trackPill))
onMounted(() => nextTick(trackPill))

// Lets the stylesheet move the toast host while the pill is on screen.
watch(visible, (on) => document.documentElement.classList.toggle('st-on-bar', on), { immediate: true })

onBeforeUnmount(() => {
  pillObserver?.disconnect()
  document.documentElement.style.removeProperty('--st-pill-h')
  document.documentElement.classList.remove('st-on-bar')
})

// Opening 页脚 goes to the footer: it is usually a screen below the fold, and
// editing blind is exactly what this toolbar exists to avoid.
watch(panelTab, async (tab) => {
  if (tab !== 'footer') return
  await nextTick()
  document.querySelector('.foot')?.scrollIntoView({ behavior: 'smooth', block: 'end' })
})
</script>

<template>
  <div v-if="visible" class="st-root">
    <Transition name="st-dock">
      <div v-if="panelTab" class="st-dock">
        <section class="st-panel" role="dialog" aria-label="站点内容编辑">
          <header class="st-head">
            <span class="st-title"><AppIcon name="wand" :size="15" /> 站点编辑</span>
            <div class="st-tabs">
              <button
                v-for="t in TABS" :key="t.id" class="st-tab" :class="{ on: panelTab === t.id }"
                :title="t.label" @click="toggleTab(t.id)"
              >
                <AppIcon :name="t.icon" :size="13" /> {{ t.label }}
              </button>
            </div>
          </header>

          <div class="st-body">
            <CopyPanel v-if="panelTab === 'copy'" />
            <LayoutPanel v-else-if="panelTab === 'layout'" />
            <FooterPanel v-else-if="panelTab === 'footer'" />
          </div>

          <footer class="st-foot">
            <div class="st-status">
              <template v-if="dirty"><b>未发布</b> · {{ status }}</template>
              <template v-else>{{ status }}</template>
            </div>
            <button class="st-btn" :disabled="!dirty" title="放弃未发布的修改" @click="discard">
              <AppIcon name="undo" :size="14" /> 撤销全部
            </button>
            <button class="st-btn" title="恢复成内置文案" @click="resetAll">
              <AppIcon name="refresh" :size="14" />
            </button>
            <button
              class="st-btn pub" :class="{ dirty }" :disabled="site.saving || !dirty"
              :title="publishHint" @click="publish"
            >
              <AppIcon :name="site.saving ? 'refresh' : 'upload'" :size="14" />
              {{ publishLabel }}
            </button>
          </footer>
        </section>
      </div>
    </Transition>

    <div ref="pillEl" class="st-pill" role="toolbar" aria-label="站点编辑工具栏">
      <span class="st-brand" :class="{ dirty }">
        <span class="st-dot" />
        <span class="label">{{ dirty ? '有未发布的修改' : '站点编辑' }}</span>
      </span>
      <span class="st-sep" />
      <button class="st-btn" :class="{ on: panelTab === 'copy' }" title="文案" @click="toggleTab('copy')">
        <AppIcon name="type" :size="14" /> 文案
      </button>
      <button class="st-btn" :class="{ on: panelTab === 'layout' }" title="布局" @click="toggleTab('layout')">
        <AppIcon name="layout" :size="14" /> 布局
      </button>
      <button class="st-btn" :class="{ on: panelTab === 'footer' }" title="页脚" @click="toggleTab('footer')">
        <AppIcon name="list" :size="14" /> 页脚
      </button>
      <span class="st-sep" />
      <button
        class="st-btn icon" :class="{ on: !editMode }"
        :title="editMode ? '隐藏页面上的编辑标记' : '显示编辑标记'"
        @click="editMode = !editMode"
      >
        <AppIcon :name="editMode ? 'eye' : 'eyeOff'" :size="15" />
      </button>
      <button class="st-btn icon st-undo" title="撤销 (Ctrl+Z)" :disabled="!canUndo" @click="undo">
        <AppIcon name="undo" :size="15" />
      </button>
      <button class="st-btn icon st-undo" title="重做 (Ctrl+Shift+Z)" :disabled="!canRedo" @click="redo">
        <AppIcon name="redo" :size="15" />
      </button>
      <span class="st-sep" />
      <button
        class="st-btn pub" :class="{ dirty }" :disabled="site.saving || !dirty"
        :title="publishHint" @click="publish"
      >
        <AppIcon :name="site.saving ? 'refresh' : 'upload'" :size="14" />
        {{ publishLabel }}
      </button>
      <button class="st-btn icon" title="关闭编辑工具栏" @click="close">
        <AppIcon name="close" :size="15" />
      </button>
    </div>
  </div>

  <div v-else-if="gated" class="st-gate">
    <span><AppIcon name="lock" :size="13" /> 编辑工具栏需要管理员登录</span>
    <button class="st-btn icon" title="关闭" @click="close"><AppIcon name="close" :size="14" /></button>
  </div>
</template>

<style>
.st-dock-enter-active { transition: opacity 180ms var(--ease-out), transform 180ms var(--ease-out); }
.st-dock-leave-active { transition: opacity 120ms ease-in; }
.st-dock-enter-from, .st-dock-leave-to { opacity: 0; transform: translateY(14px); }
</style>
