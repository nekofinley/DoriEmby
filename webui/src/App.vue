<script setup>
import { ref, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from './api.js'

const route = useRoute()
const router = useRouter()
const me = ref(null)

api.get('/api/auth/me').then(r => { me.value = r.user }).catch(() => {})

const isAdmin = computed(() => me.value?.role === 'admin')

async function logout() {
  try { await api.post('/api/auth/logout') } catch {}
  location.href = '/login'
}
</script>

<template>
  <div v-if="route.meta.fullscreen">
    <router-view />
  </div>
  <div v-else class="app-shell">
    <header class="topbar">
      <router-link to="/" class="brand">
        <span class="logo">D</span>
        <span class="brand-name">Doriya</span>
      </router-link>
      <nav class="nav">
        <router-link to="/">首页</router-link>
        <router-link to="/browse">文件</router-link>
      </nav>
      <div class="spacer"></div>
      <router-link v-if="isAdmin" to="/settings" class="btn ghost">设置</router-link>
      <button v-if="me" class="btn ghost" @click="logout">{{ me.name }} · 退出</button>
    </header>
    <router-view />
  </div>
</template>

<style scoped>
.app-shell { min-height: 100vh; }
.topbar {
  position: sticky;
  top: 0;
  z-index: 100;
  display: flex;
  align-items: center;
  gap: 18px;
  height: var(--header-h);
  padding: 0 22px;
  background: rgba(11, 11, 11, 0.8);
  backdrop-filter: blur(12px);
  -webkit-backdrop-filter: blur(12px);
  border-bottom: 1px solid var(--border-1);
}
.brand { display: flex; align-items: center; gap: 9px; font-weight: 700; font-size: 15px; }
.logo {
  width: 26px; height: 26px;
  display: inline-flex; align-items: center; justify-content: center;
  background: var(--accent);
  border-radius: 8px;
  color: #fff;
  font-size: 15px;
  font-weight: 800;
}
.nav { display: flex; gap: 14px; color: var(--text-3); }
.nav a.router-link-active { color: var(--text-1); }
.nav a:hover { color: var(--text-1); }
.spacer { flex: 1; }
</style>
