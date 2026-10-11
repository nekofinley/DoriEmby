<script setup>
import { ref, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../api.js'

const route = useRoute()
const router = useRouter()
const name = ref('')
const password = ref('')
const error = ref('')
const busy = ref(false)
const setupNeeded = ref(false)

onMounted(async () => {
  try {
    const b = await api.get('/api/bootstrap')
    setupNeeded.value = b.setup_needed
  } catch {}
})

async function submit() {
  error.value = ''
  busy.value = true
  try {
    if (setupNeeded.value) {
      await api.post('/api/setup', { name: name.value, password: password.value })
    } else {
      await api.post('/api/auth/login', { name: name.value, password: password.value })
    }
    const redirect = route.query.redirect || '/'
    location.href = redirect
  } catch (e) {
    error.value = e.message
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="login-wrap">
    <form class="login card" @submit.prevent="submit">
      <div class="brand-row">
        <span class="logo">D</span>
        <h1>{{ setupNeeded ? '初始化 Doriya' : '登录 Doriya' }}</h1>
      </div>
      <p class="muted">{{ setupNeeded ? '创建管理员账号' : '请输入你的账号密码' }}</p>
      <div class="field">
        <label>用户名</label>
        <input v-model="name" autocomplete="username" required />
      </div>
      <div class="field">
        <label>密码</label>
        <input v-model="password" type="password" autocomplete="current-password" required />
      </div>
      <div v-if="error" class="error">{{ error }}</div>
      <button class="btn primary" style="justify-content:center; width:100%" :disabled="busy">
        {{ busy ? '请稍候…' : setupNeeded ? '创建并登录' : '登录' }}
      </button>
    </form>
  </div>
</template>

<style scoped>
.login-wrap {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background:
    radial-gradient(1200px 600px at 70% -10%, rgba(113, 112, 255, 0.13), transparent 60%),
    var(--bg-0);
  padding: 20px;
}
.login { width: 380px; padding: 28px; }
.brand-row { display: flex; align-items: center; gap: 10px; margin-bottom: 6px; }
.logo {
  width: 30px; height: 30px;
  display: inline-flex; align-items: center; justify-content: center;
  background: var(--accent);
  border-radius: 9px;
  color: #fff;
  font-weight: 800;
}
.error {
  background: rgba(235, 87, 87, 0.1);
  border: 1px solid rgba(235, 87, 87, 0.3);
  color: #ff8a8a;
  border-radius: 8px;
  padding: 8px 12px;
  margin-bottom: 12px;
  font-size: 13px;
}
</style>
