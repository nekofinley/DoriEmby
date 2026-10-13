<script setup>
import { ref, computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { api } from './api.js'
import AppHeader from './components/AppHeader.vue'
import AppFooter from './components/AppFooter.vue'
import ToastHost from './components/ToastHost.vue'

const route = useRoute()
const me = ref(null)
const version = ref('')

const fullscreen = computed(() => !!route.meta.fullscreen)
const bare = computed(() => route.meta.chrome === false)
const overlay = computed(() => !!route.meta.overlay)

onMounted(async () => {
  try {
    me.value = (await api.get('/api/auth/me')).user
    // The footer names the binary it is talking to. /api/system/info is
    // authenticated, so it is asked for only once there is a session; if it
    // fails the footer simply omits the version.
    version.value = (await api.get('/api/system/info')).version || ''
  } catch {}
})
</script>

<template>
  <!-- player: its own chrome, letterbox black, no site header -->
  <div v-if="fullscreen" class="shell fullscreen">
    <router-view />
  </div>

  <!-- login: full-bleed page with its own layout -->
  <div v-else-if="bare" class="shell bare">
    <router-view />
    <ToastHost />
  </div>

  <div v-else class="shell">
    <AppHeader :me="me" :overlay="overlay" />
    <main class="content" :class="{ overlay }">
      <router-view v-slot="{ Component, route: r }">
        <Transition name="page" mode="out-in">
          <component :is="Component" :key="r.path" />
        </Transition>
      </router-view>
    </main>
    <AppFooter :me="me" :version="version" />
    <ToastHost />
  </div>
</template>

<style scoped>
.shell { min-height: 100dvh; display: flex; flex-direction: column; }
.shell.fullscreen { background: #000; }
.shell.bare { display: block; }
.content { flex: 1; }
.content.overlay { margin-top: calc(var(--header-h) * -1); }
.content:not(.overlay) { padding-top: var(--header-h); }
</style>

<style>
/* route transition — quiet, no layout jank */
.page-enter-active { transition: opacity 260ms var(--ease-out), transform 260ms var(--ease-out); }
.page-leave-active { transition: opacity 140ms ease-in; }
.page-enter-from { opacity: 0; transform: translateY(10px); }
.page-leave-to { opacity: 0; }
</style>
