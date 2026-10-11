<script setup>
import { ref, onMounted, onBeforeUnmount, nextTick } from 'vue'
import AppIcon from './AppIcon.vue'

/** Netflix-style horizontal rail: snap scrolling, hover arrows, edge detection. */
const track = ref(null)
const atStart = ref(true)
const atEnd = ref(false)
const overflowing = ref(false)

function measure() {
  const el = track.value
  if (!el) return
  overflowing.value = el.scrollWidth - el.clientWidth > 8
  atStart.value = el.scrollLeft <= 4
  atEnd.value = el.scrollLeft + el.clientWidth >= el.scrollWidth - 4
}

function scrollBy(dir) {
  const el = track.value
  if (!el) return
  const step = Math.max(el.clientWidth * 0.82, 240)
  el.scrollBy({ left: dir * step, behavior: 'smooth' })
}

let ro = null
onMounted(async () => {
  await nextTick()
  measure()
  window.addEventListener('resize', measure)
  if (typeof ResizeObserver !== 'undefined' && track.value) {
    ro = new ResizeObserver(measure)
    ro.observe(track.value)
  }
})
onBeforeUnmount(() => {
  window.removeEventListener('resize', measure)
  ro?.disconnect()
})
</script>

<template>
  <div class="rail" :class="{ over: overflowing }">
    <button
      class="rail-arrow left" :hidden="atStart" aria-label="向左滚动"
      @click="scrollBy(-1)"
    ><span><AppIcon name="chevronLeft" :size="20" /></span></button>

    <div ref="track" class="rail-track no-scrollbar" @scroll.passive="measure">
      <slot />
    </div>

    <button
      class="rail-arrow right" :hidden="atEnd" aria-label="向右滚动"
      @click="scrollBy(1)"
    ><span><AppIcon name="chevronRight" :size="20" /></span></button>
  </div>
</template>
