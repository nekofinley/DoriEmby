<script setup>
import { ref, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../api.js'
import AppIcon from '../components/AppIcon.vue'

// The page the reset link opens. Like the verification page, the token is kept
// until the form is submitted, so a link that a mail provider pre-fetched still
// works when the owner clicks it.
const route = useRoute()
const router = useRouter()
const token = ref(String(route.query.token || ''))
const password = ref('')
const confirm = ref('')
const showPw = ref(false)
const busy = ref(false)
const error = ref('')
const done = ref('')

const tooShort = computed(() => password.value.length > 0 && password.value.length < 4)

async function submit() {
  error.value = ''
  if (password.value !== confirm.value) {
    error.value = '两次输入的密码不一致'
    return
  }
  busy.value = true
  try {
    const res = await api.post('/api/auth/reset-password', { token: token.value, password: password.value })
    done.value = res?.message || '密码已重设,请用新密码登录'
  } catch (e) {
    error.value = e.message
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="gate">
    <form class="gate-card" @submit.prevent="submit">
      <div class="brand">
        <span class="logo"><AppIcon name="play" :size="14" filled /></span>
        <span class="wordmark">Doriya<span>Media</span></span>
      </div>

      <div class="icon" :class="{ done }">
        <AppIcon :name="done ? 'check' : 'lock'" :size="26" />
      </div>

      <h1>{{ done ? '密码已重设' : '设置新密码' }}</h1>

      <template v-if="done">
        <p class="small">{{ done }}</p>
        <button type="button" class="btn primary lg block" @click="router.push('/login')">
          <AppIcon name="arrowLeft" :size="16" /> 去登录
        </button>
      </template>

      <template v-else-if="!token">
        <p class="small bad">这个链接里没有重置令牌,请从邮件里重新点一次,或在登录页重新申请。</p>
        <button type="button" class="btn lg block" @click="router.push('/login')">
          <AppIcon name="arrowLeft" :size="16" /> 返回登录
        </button>
      </template>

      <template v-else>
        <p class="small">为安全起见,设置新密码后其他设备都会被退出登录。</p>

        <div class="field">
          <label for="rp-pw">新密码</label>
          <div class="pw-wrap">
            <input
              id="rp-pw" v-model="password" :type="showPw ? 'text' : 'password'"
              autocomplete="new-password" placeholder="至少 4 位" required :disabled="busy"
            />
            <button
              type="button" class="pw-toggle" :aria-label="showPw ? '隐藏密码' : '显示密码'"
              @click="showPw = !showPw"
            >
              <AppIcon :name="showPw ? 'eyeOff' : 'eye'" :size="17" />
            </button>
          </div>
          <span v-if="tooShort" class="hint">密码太短了。</span>
        </div>

        <div class="field">
          <label for="rp-pw2">确认新密码</label>
          <input
            id="rp-pw2" v-model="confirm" :type="showPw ? 'text' : 'password'"
            autocomplete="new-password" placeholder="再输入一次" required :disabled="busy"
          />
        </div>

        <Transition name="fade">
          <div v-if="error" class="flash err"><AppIcon name="alert" :size="16" /> {{ error }}</div>
        </Transition>

        <button class="btn primary lg block" :disabled="busy || !password || password !== confirm">
          <AppIcon name="check" :size="16" /> {{ busy ? '提交中…' : '保存新密码' }}
        </button>
      </template>
    </form>
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
h1 { font-size: var(--fs-20, 20px); margin: 0 0 10px; }
.small { margin: 0 0 var(--sp-5); line-height: 1.6; }
.small.bad { color: #ffb3b3; }
.field { text-align: left; }
.pw-wrap { position: relative; display: flex; align-items: center; }
.pw-wrap input { padding-right: 44px; }
.pw-toggle {
  position: absolute; right: 6px;
  display: grid; place-items: center;
  width: 32px; height: 32px;
  border-radius: var(--r-s);
  color: var(--text-4);
}
.pw-toggle:hover { color: var(--text-1); background: rgba(255, 255, 255, 0.07); }
</style>
