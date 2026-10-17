<script setup>
/** A collapsible block of fields in the toolbar panel. */
import AppIcon from '../AppIcon.vue'

defineProps({
  title: { type: String, required: true },
  icon: { type: String, default: '' },
  open: { type: Boolean, default: false },
})
</script>

<template>
  <details class="st-group" :open="open">
    <summary>
      <span class="st-gt">
        <AppIcon v-if="icon" :name="icon" :size="14" />
        {{ title }}
      </span>
      <AppIcon class="st-gchev" name="chevronDown" :size="14" />
    </summary>
    <div class="st-gbody"><slot /></div>
  </details>
</template>
