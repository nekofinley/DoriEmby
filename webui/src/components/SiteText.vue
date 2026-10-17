<script setup>
/**
 * One line of copy, editable in place.
 *
 * In edit mode the element itself becomes the input: click the headline on the
 * page and type, exactly like Vercel's inline editing. The text is written to the
 * DOM by hand rather than through the template, because Vue patching a text node
 * on every keystroke would put the caret back at the start of the line.
 *
 * While the line has focus it shows the *raw* value, so a {host} placeholder is
 * what you see and edit; everywhere else it shows the resolved copy, so the page
 * always looks like the page visitors get.
 *
 * `passive` renders the same copy without making it editable: buttons, links and
 * labels keep working, and still light up when their field is hovered in the
 * panel.
 */
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { site, editMode, toolbarOn, hoverKey, activeKey, docEpoch, setField, rawOf, ctx } from '../site/store.js'
import { fill } from '../site/schema.js'

const props = defineProps({
  k: { type: String, required: true },
  as: { type: String, default: 'span' },
  multiline: { type: Boolean, default: false },
  passive: { type: Boolean, default: false },
  ph: { type: String, default: '点这里输入' },
})

const el = ref(null)
// True from the moment the pointer goes down on the line until it loses focus:
// the line then shows the *raw* template ({host} and friends), which is what the
// owner is editing.
const editing = ref(false)
const markable = computed(() => site.canEdit && toolbarOn.value && editMode.value)
const editable = computed(() => markable.value && !props.passive)
const raw = computed(() => {
  const v = rawOf(props.k)
  return v === undefined || v === null ? '' : String(v)
})
const shown = computed(() => fill(raw.value, ctx))

// Chrome and Safari take "plaintext-only" (paste and Enter arrive as plain text);
// anything else falls back to a normal contenteditable, where the handlers below
// do the same job.
const CE = (() => {
  const probe = document.createElement('div')
  try { probe.contentEditable = 'plaintext-only' } catch {}
  return probe.contentEditable === 'plaintext-only' ? 'plaintext-only' : 'true'
})()

function caretToEnd(node) {
  const range = document.createRange()
  range.selectNodeContents(node)
  range.collapse(false)
  const sel = window.getSelection()
  sel.removeAllRanges()
  sel.addRange(range)
}

function paint(force = false) {
  const node = el.value
  if (!node) return
  // Never fight the caret of the element being typed into — unless the store
  // itself moved under our feet (undo, 放弃修改, a fresh load), where the page
  // must follow the document.
  if (!force && editing.value && document.activeElement === node) return
  const next = editing.value ? raw.value : shown.value
  if (node.textContent === next) return
  const focused = document.activeElement === node
  node.textContent = next
  if (force && focused) caretToEnd(node)
}

onMounted(paint)
watch([raw, shown, editable, () => props.as], () => nextTick(paint), { flush: 'post' })
// A wholesale document swap wins over the caret.
watch(docEpoch, () => nextTick(() => paint(true)), { flush: 'post' })

function readValue(node) {
  const text = props.multiline ? node.innerText : node.textContent || ''
  return text.replace(/\r\n?/g, '\n')
}

function onInput(e) {
  let v = readValue(e.target)
  if (!props.multiline) v = v.replace(/\s*\n\s*/g, ' ')
  setField(props.k, v)
}

function onPaste(e) {
  const text = (e.clipboardData?.getData('text/plain') || '').replace(/\r\n?/g, '\n')
  e.preventDefault()
  document.execCommand('insertText', false, props.multiline ? text : text.replace(/\n+/g, ' '))
}

function onKeydown(e) {
  if (e.key === 'Escape') {
    e.preventDefault()
    e.target.blur()
    return
  }
  // Single-line copy: Enter finishes the edit instead of inserting a break.
  if (e.key === 'Enter' && !props.multiline) {
    e.preventDefault()
    e.target.blur()
  }
}

function onFocus() {
  activeKey.value = props.k
  if (!editable.value) return
  const node = el.value
  // Reached by keyboard (no pointerdown first): show the template and put the
  // caret at the end so typing does something sensible.
  if (node && node.textContent !== raw.value) {
    node.textContent = raw.value
    caretToEnd(node)
  }
  editing.value = true
}

/**
 * Swap to the raw template *before* the browser places the caret, so a click in
 * the middle of a line still lands where the pointer is.
 */
function onMousedown() {
  if (!editable.value || editing.value) return
  editing.value = true
  if (el.value) el.value.textContent = raw.value
}

function onBlur() {
  editing.value = false
  if (activeKey.value === props.k) activeKey.value = ''
  nextTick(() => paint())
}
</script>

<template>
  <component
    :is="as"
    ref="el"
    class="st-text"
    :class="{ mark: markable && !passive, on: editable, hot: hoverKey === k, act: activeKey === k }"
    :contenteditable="editable ? CE : undefined"
    :data-site-key="markable ? k : undefined"
    :data-ph="ph"
    spellcheck="false"
    @input="onInput"
    @paste="onPaste"
    @keydown="onKeydown"
    @mousedown="onMousedown"
    @focus="onFocus"
    @blur="onBlur"
  />
</template>

<style scoped>
.st-text { white-space: pre-line; }

/* Only while editing: the affordances vanish completely in normal use. */
.st-text.mark {
  outline: 1px dashed rgba(255, 255, 255, 0.22);
  outline-offset: 3px;
  border-radius: 3px;
  transition: outline-color var(--t-fast), background var(--t-fast);
}
.st-text.mark:hover { outline-color: var(--brand); background: rgba(255, 255, 255, 0.04); }
.st-text.on { cursor: text; }
.st-text.on:focus { outline: 2px solid var(--brand); outline-offset: 3px; background: rgba(255, 255, 255, 0.05); }
.st-text.on:empty::before {
  content: attr(data-ph);
  color: var(--text-4);
  font-style: italic;
}
/* Hovering the matching field in the panel points at the line it will change. */
.st-text.hot { outline-color: var(--brand); background: var(--brand-tint); }
.st-text.act.mark { outline-color: var(--brand); }
</style>
