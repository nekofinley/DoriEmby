<script setup>
import { ref, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../api.js'
import AppIcon from '../components/AppIcon.vue'

// The page the verification link opens. The token is only spent when the button
// is pressed, not on load: some mail providers pre-fetch links, and a token
// consumed by a scanner would leave the real owner with a dead link.
const route = useRoute()
const router = useRouter()
const token = ref(String(route.query.token || ''))
const state = ref('idle') // idle | working | done | failed
const message = ref('')

onMounted(() => {
  if (!token.value) {
    state.value = 'failed'
    message.value = '这个链接里没有验证令牌,请从邮件里重新点一次。'
  }
})

async function confirm() {
  state.value = 'working'
  message.value = ''
  try {
    const res = await api.post('/api/auth/verify-email', { token: token.value })
    state.value = 'done'
    message.value = res?.message || '邮箱验证成功,现在可以登录了'
  } catch (e) {
    state.value = 'failed'
    message.value = e.message
  }
}
</script>

<template>
  <div class="gate">
    <div class="gate-card">
      <div class="brand">
        <span class="logo"><AppIcon name="play" :size="14" filled /></span>
        <span class="wordmark">Doriya<span>Media</span></span>
      </div>

      <div class="icon" :class="state">
        <AppIcon :name="state === 'done' ? 'check' : state === 'failed' ? 'alert' : 'link'" :size="26" />
      </div>

      <h1>
        <template v-if="state === 'done'">邮箱已验证</template>
        <template v-else-if="state === 'failed'">验证没有完成</template>
        <template v-else>确认你的邮箱地址</template>
      </h1>

      <p v-if="message" class="small" :class="{ bad: state === 'failed' }">{{ message }}</p>
      <p v-else class="small">点下面的按钮完成验证,之后就可以用这个账号登录了。</p>

      <button
        v-if="state === 'idle' || state === 'working'"
        class="btn primary lg block" :disabled="state === 'working' || !token"
        @click="confirm"
      >
        <AppIcon name="check" :size="16" />
        {{ state === 'working' ? '验证中…' : '确认验证' }}
      </button>

      <button v-else class="btn primary lg block" @click="router.push('/login')">
        <AppIcon name="arrowLeft" :size="16" /> 去登录
      </button>

      <p v-if="state === 'failed'" class="tiny mt-4">
        链接过期了?在登录页点「忘记密码」下面的位置可以重新发送一封验证邮件。
      </p>
    </div>
  </div>
</template>

<style scoped>
.gate {
  min-height: 100dvh;
  display: grid;
  place-items: center;
  padding: 24px;
  background:
    radial-gradient(70% 50% at 80% 0%, rgba(240, 169, 46, 0.12), transparent 60%),
    radial-gradient(60% 50% at 10% 10%, rgba(74, 108, 255, 0.1), transparent 62%),
    var(--bg-0);
}
.gate-card {
  width: 100%;
  max-width: 420px;
  text-align: center;
  padding: clamp(24px, 5vw, 36px);
  background: var(--bg-1);
  border: 1px solid var(--line-1);
  border-radius: var(--r-l);
  box-shadow: var(--sh-3);
}
.brand { display: flex; align-items: center; justify-content: center; gap: 9px; margin-bottom: var(--sp-5); }
.logo {
  display: grid; place-items: center;
  width: 28px; height: 28px;
  border-radius: 9px;
  background: linear-gradient(150deg, var(--brand-2), #d98a12 70%);
  color: var(--brand-ink);
}
.wordmark { font-family: var(--font-display); font-size: 16px; font-weight: 800; letter-spacing: -0.035em; }
.wordmark span { color: var(--text-3); font-weight: 600; }
.icon {
  display: grid; place-items: center;
  width: 54px; height: 54px;
  margin: 0 auto var(--sp-4);
  border-radius: 50%;
  background: var(--brand-tint);
  color: var(--brand);
  border: 1px solid rgba(240, 169, 46, 0.28);
}
.icon.done { background: var(--ok-soft); color: #86efac; border-color: rgba(74, 222, 128, 0.28); }
.icon.failed { background: var(--danger-soft); color: #ffb3b3; border-color: rgba(255, 107, 107, 0.3); }
h1 { font-size: var(--fs-20, 20px); margin: 0 0 10px; }
.small { margin: 0 0 var(--sp-5); line-height: 1.6; }
.small.bad { color: #ffb3b3; }
</style>
