<script setup>
import AppIcon from './AppIcon.vue'
import { useToast } from '../toast.js'

const { items, dismiss } = useToast()
const icon = (k) => (k === 'err' ? 'alert' : k === 'ok' ? 'check' : 'info')
</script>

<template>
  <div class="toast-host">
    <TransitionGroup name="toast">
      <button
        v-for="t in items" :key="t.id"
        class="toast" :class="t.kind"
        @click="dismiss(t.id)"
      >
        <AppIcon :name="icon(t.kind)" :size="15" />
        <span>{{ t.message }}</span>
      </button>
    </TransitionGroup>
  </div>
</template>
