<script setup>
import AppIcon from './AppIcon.vue'

defineProps({
  icon: { type: String, default: 'film' },
  title: { type: String, default: '' },
  text: { type: String, default: '' },
})
</script>

<template>
  <div class="empty-state">
    <AppIcon :name="icon" :size="30" :stroke="1.4" />
    <div class="t">{{ title }}</div>
    <div v-if="text" class="small" style="max-width:420px">{{ text }}</div>
    <div v-if="$slots.default" class="mt-2"><slot /></div>
  </div>
</template>
