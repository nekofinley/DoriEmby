<script setup>
/** 布局 tab — how the login page is arranged, and the site accent. */
import { computed } from 'vue'
import { site, setField } from '../../site/store.js'
import { SPLITS, ACCENTS, ART_TONES } from '../../site/schema.js'
import AppIcon from '../AppIcon.vue'
import PanelGroup from './PanelGroup.vue'
import PanelSegmented from './PanelSegmented.vue'

const L = computed(() => site.doc.login.layout)

const splitOptions = SPLITS.map((s) => ({ id: s.id, label: s.label }))
const sideOptions = [{ id: 'left', label: '左侧品牌' }, { id: 'right', label: '右侧品牌' }]
const cornerOptions = [
  { id: 'square', label: '直角' },
  { id: 'soft', label: '微圆角' },
  { id: 'round', label: '大圆角' },
]
const toneOptions = Object.keys(ART_TONES).map((id) => ({
  id,
  label: { subtle: '淡', normal: '标准', bold: '浓' }[id],
}))
</script>

<template>
  <PanelGroup title="分栏" icon="layout" open>
    <div class="st-row">
      <div class="st-label">品牌面板位置</div>
      <PanelSegmented
        :model-value="L.aside" :options="sideOptions"
        @update:model-value="setField('login.layout.aside', $event)"
      />
    </div>
    <div class="st-row">
      <div class="st-label">分栏比例</div>
      <PanelSegmented
        :model-value="L.split" :options="splitOptions"
        @update:model-value="setField('login.layout.split', $event)"
      />
    </div>
    <div class="st-row">
      <div class="st-label">表单宽度 · {{ L.formWidth }}px</div>
      <input
        class="st-range" type="range" min="300" max="560" step="10" :value="L.formWidth"
        @input="setField('login.layout.formWidth', Number($event.target.value))"
      />
      <div class="st-hint">窄一点更像登录门户,宽一点更好填。</div>
    </div>
    <div class="st-row">
      <div class="st-label">圆角</div>
      <PanelSegmented
        :model-value="L.corners" :options="cornerOptions"
        @update:model-value="setField('login.layout.corners', $event)"
      />
    </div>
  </PanelGroup>

  <PanelGroup title="背景艺术" icon="image" open>
    <div class="st-row">
      <label class="st-switch">
        <input
          type="checkbox" :checked="L.showArt"
          @change="setField('login.layout.showArt', $event.target.checked)"
        />
        <span>显示渐变与网格背景</span>
      </label>
    </div>
    <div class="st-row">
      <div class="st-label">强度</div>
      <PanelSegmented
        :model-value="L.artTone" :options="toneOptions"
        @update:model-value="setField('login.layout.artTone', $event)"
      />
    </div>
    <div class="st-row">
      <label class="st-switch">
        <input
          type="checkbox" :checked="L.showFeatures"
          @change="setField('login.layout.showFeatures', $event.target.checked)"
        />
        <span>显示亮点卡片</span>
      </label>
    </div>
    <div class="st-row">
      <div class="st-label">卡片列数</div>
      <PanelSegmented
        :model-value="String(L.featureCols)"
        :options="[{ id: '1', label: '单列' }, { id: '2', label: '双列' }]"
        @update:model-value="setField('login.layout.featureCols', Number($event))"
      />
    </div>
  </PanelGroup>

  <PanelGroup title="主题色" icon="palette" open>
    <div class="st-swatches">
      <button
        v-for="a in ACCENTS" :key="a.id" type="button" class="st-swatch"
        :class="{ on: site.doc.site.accent === a.id }" :title="a.label"
        :style="{ background: `linear-gradient(135deg, ${a.brand2}, ${a.brand})` }"
        @click="setField('site.accent', a.id)"
      >
        <AppIcon v-if="site.doc.site.accent === a.id" name="check" :size="14" />
      </button>
    </div>
    <div class="st-hint">作用于登录页与页脚的高亮色,不改动后台其他页面。</div>
  </PanelGroup>
</template>
