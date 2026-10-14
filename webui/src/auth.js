import { ref, computed } from 'vue'
import { api } from './api.js'

// One shared answer to "who am I", because both the router guard and the header
// need it and two independent fetches could disagree mid-navigation.
export const me = ref(null)
export const ready = ref(false)

export const isAdmin = computed(() => me.value?.role === 'admin')

/** Load the signed-in account once; force re-reads it from the server. */
export async function loadMe(force = false) {
  if (ready.value && !force) return me.value
  try {
    me.value = (await api.get('/api/auth/me')).user
  } catch {
    me.value = null
  }
  ready.value = true
  return me.value
}

/** Forget the cached answer, so the next guard run hits the server. */
export function resetAuth() {
  ready.value = false
  me.value = null
}

export async function logout() {
  try { await api.post('/api/auth/logout') } catch {}
  resetAuth()
  location.href = '/login'
}
