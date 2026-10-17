<script setup>
/** 页脚 tab — the columns and small print under every page. */
import { computed, nextTick } from 'vue'
import { useRoute } from 'vue-router'
import { site, listOf, setField, addItem, removeItem, moveItem, hoverKey, panelTab } from '../../site/store.js'
import { ROUTE_SUGGESTIONS } from '../../site/schema.js'
import AppIcon from '../AppIcon.vue'
import PanelGroup from './PanelGroup.vue'
import PanelField from './PanelField.vue'

const route = useRoute()
// The login page renders without the site chrome, so the footer is not on screen
// while this panel is open — say so instead of leaving the owner guessing.
const noFooterHere = computed(() => route.meta.chrome === false)

const columns = computed(() => listOf('footer.columns'))
const caps = computed(() => listOf('footer.capabilities.items'))

function addColumn() {
  addItem('footer.columns', { heading: '新栏目', links: [{ label: '新链接', to: '/' }] })
}
function addLink(ci) {
  addItem(`footer.columns.${ci}.links`, { label: '新链接', to: '/' })
}
function moveLink(ci, li, delta) {
  moveItem(`footer.columns.${ci}.links`, li, delta)
}
function delLink(ci, li) {
  removeItem(`footer.columns.${ci}.links`, li)
}
function hot(k) {
  hoverKey.value = k
}
function cold() {
  hoverKey.value = ''
}

/** The login page's own bottom line lives in the copy tab — go straight to it. */
async function goToLoginFoot() {
  panelTab.value = 'copy'
  await nextTick()
  document.querySelector('[data-k="login.form.foot"]')?.scrollIntoView({ block: 'center', behavior: 'smooth' })
}
</script>

<template>
  <div v-if="noFooterHere" class="st-notice">
    这个页面不显示页脚。改动会马上生效,<router-link to="/">回到首页</router-link> 就能看到页脚的样子。
    <button class="st-link" type="button" @click="goToLoginFoot">
      改登录页底部那行小字 →
    </button>
  </div>

  <PanelGroup title="品牌与简介" icon="globe" open>
    <div class="st-pair">
      <PanelField k="footer.wordmark.name" label="名称" />
      <PanelField k="footer.wordmark.suffix" label="后缀(高亮部分)" />
    </div>
    <PanelField k="footer.about" label="简介段落" :rows="3" hint="支持 {version} 等占位符" />
    <PanelField k="footer.status" label="运行状态文字" hint="前面带一个呼吸小圆点" />
  </PanelGroup>

  <PanelGroup title="链接栏目" icon="list" open>
    <div
      v-for="(col, ci) in columns" :key="ci" class="st-item"
      @mouseenter="hot(`footer.columns.${ci}.heading`)" @mouseleave="cold"
    >
      <div class="st-item-head">
        <span class="st-item-no">{{ ci + 1 }}</span>
        <span class="spacer" />
        <button class="st-mini" title="上移" :disabled="ci === 0" @click="moveItem('footer.columns', ci, -1)"><AppIcon name="chevronUp" :size="14" /></button>
        <button class="st-mini" title="下移" :disabled="ci === columns.length - 1" @click="moveItem('footer.columns', ci, 1)"><AppIcon name="chevronDown" :size="14" /></button>
        <button class="st-mini danger" title="删除栏目" @click="removeItem('footer.columns', ci)"><AppIcon name="trash" :size="14" /></button>
      </div>
      <PanelField :k="`footer.columns.${ci}.heading`" label="栏目标题" />
      <div
        v-for="(ln, li) in col.links" :key="li" class="st-link-row"
        @mouseenter="hot(`footer.columns.${ci}.links.${li}.label`)" @mouseleave="cold"
      >
        <input
          class="st-input" :value="ln.label" placeholder="文字"
          @input="setField(`footer.columns.${ci}.links.${li}.label`, $event.target.value)"
        />
        <input
          class="st-input st-mono" :value="ln.to" placeholder="/browse" list="st-routes"
          @input="setField(`footer.columns.${ci}.links.${li}.to`, $event.target.value)"
        />
        <button class="st-mini" title="上移" :disabled="li === 0" @click="moveLink(ci, li, -1)"><AppIcon name="chevronUp" :size="13" /></button>
        <button class="st-mini" title="下移" :disabled="li === col.links.length - 1" @click="moveLink(ci, li, 1)"><AppIcon name="chevronDown" :size="13" /></button>
        <button class="st-mini danger" title="删除链接" @click="delLink(ci, li)"><AppIcon name="close" :size="13" /></button>
      </div>
      <button class="st-add sm" @click="addLink(ci)"><AppIcon name="plus" :size="13" /> 添加链接</button>
    </div>
    <button class="st-add" @click="addColumn"><AppIcon name="plus" :size="14" /> 添加栏目</button>
    <datalist id="st-routes">
      <option v-for="r in ROUTE_SUGGESTIONS" :key="r" :value="r" />
    </datalist>
  </PanelGroup>

  <PanelGroup title="播放能力" icon="playCircle">
    <PanelField k="footer.capabilities.heading" label="小标题" />
    <div
      v-for="(it, i) in caps" :key="i" class="st-link-row"
      @mouseenter="hot(`footer.capabilities.items.${i}`)" @mouseleave="cold"
    >
      <input
        class="st-input" :value="it"
        @input="setField(`footer.capabilities.items.${i}`, $event.target.value)"
      />
      <button class="st-mini" title="上移" :disabled="i === 0" @click="moveItem('footer.capabilities.items', i, -1)"><AppIcon name="chevronUp" :size="13" /></button>
      <button class="st-mini" title="下移" :disabled="i === caps.length - 1" @click="moveItem('footer.capabilities.items', i, 1)"><AppIcon name="chevronDown" :size="13" /></button>
      <button class="st-mini danger" title="删除" @click="removeItem('footer.capabilities.items', i)"><AppIcon name="close" :size="13" /></button>
    </div>
    <button class="st-add sm" @click="addItem('footer.capabilities.items', '新的能力说明')"><AppIcon name="plus" :size="13" /> 添加一条</button>
  </PanelGroup>

  <PanelGroup title="版权" icon="type">
    <PanelField k="footer.copyright" label="版权行" hint="支持占位符 {year} {site} {version}" />
  </PanelGroup>
</template>
