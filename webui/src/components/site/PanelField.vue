<script setup>
/**
 * One copy field in the panel, bound straight to the document.
 *
 * Hovering or focusing a row highlights the same line on the page (hoverKey), so
 * it is obvious which headline an input is about to change.
 */
import { computed } from 'vue'
import { valueOf, setField, hoverKey, revealOnPage, ctx } from '../../site/store.js'
import { fill } from '../../site/schema.js'
import AppIcon from '../AppIcon.vue'

const props = defineProps({
  k: { type: String, required: true },
  label: { type: String, default: '' },
  hint: { type: String, default: '' },
  rows: { type: Number, default: 0 },
})

const value = computed(() => valueOf(props.k))
const resolved = computed(() => (/\{[a-z]+\}/.test(value.value) ? fill(value.value, ctx) : ''))

function onInput(e) {
  setField(props.k, e.target.value)
}
function hot() {
  hoverKey.value = props.k
}
function cold() {
  if (hoverKey.value === props.k) hoverKey.value = ''
}
</script>

<template>
  <div class="st-row" @mouseenter="hot" @mouseleave="cold">
    <div v-if="label" class="st-label-row">
      <span class="st-label">{{ label }}</span>
      <button class="st-locate" title="在页面上定位这一行" @click="revealOnPage(k)">
        <AppIcon name="eye" :size="12" />
      </button>
    </div>
    <textarea
      v-if="rows" class="st-input st-area" :data-k="k" :value="value" :rows="rows"
      @input="onInput" @focus="hot" @blur="cold"
    />
    <input v-else class="st-input" :data-k="k" :value="value" @input="onInput" @focus="hot" @blur="cold" />
    <div v-if="resolved" class="st-hint">页面上显示:{{ resolved }}</div>
    <div v-else-if="hint" class="st-hint">{{ hint }}</div>
  </div>
</template>
