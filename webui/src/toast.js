import { ref } from 'vue'

/** Tiny global toast queue (used by the player, settings and detail pages). */
const items = ref([])
let seq = 0

export function useToast() {
  return { items, push, dismiss }
}

export function push(message, kind = 'info', ms = 2600) {
  const id = ++seq
  items.value.push({ id, message, kind })
  setTimeout(() => dismiss(id), ms)
  return id
}

export function dismiss(id) {
  const i = items.value.findIndex((t) => t.id === id)
  if (i >= 0) items.value.splice(i, 1)
}

export const toast = {
  info: (m, ms) => push(m, 'info', ms),
  ok: (m, ms) => push(m, 'ok', ms),
  err: (m, ms) => push(m, 'err', ms),
}
