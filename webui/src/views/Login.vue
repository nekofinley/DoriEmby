<script setup>
import { ref, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { api } from '../api.js'
import AppIcon from '../components/AppIcon.vue'

const route = useRoute()
const host = location.host
const name = ref('')
const password = ref('')
const showPw = ref(false)
const error = ref('')
const busy = ref(false)
const setupNeeded = ref(false)
const checking = ref(true)

onMounted(async () => {
  try {
    const b = await api.get('/api/bootstrap')
    setupNeeded.value = !!b.setup_needed
    if (setupNeeded.value) name.value = 'admin'
  } catch (e) {
    error.value = e.message
  } finally {
    checking.value = false
  }
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
    location.href = route.query.redirect || '/'
  } catch (e) {
    error.value = e.message
    busy.value = false
  }
}

const features = [
  { icon: 'cloud', t: '挂载网盘', d: 'OpenList / WebDAV / 本地目录,扫描即入库' },
  { icon: 'sparkles', t: '智能刮削', d: 'TMDB 与 Bangumi 补全中文标题、海报与简介' },
  { icon: 'playCircle', t: '302 直连', d: '网盘直链直连播放,几乎不占服务器带宽' },
  { icon: 'layers', t: '自动转封装', d: 'iOS 播不了的 MKV / HEVC 自动封装成 HLS' },
]
</script>

<template>
  <div class="login">
    <!-- ================= left: brand story ================= -->
    <section class="aside">
      <div class="art"></div>
      <div class="aside-scrim"></div>
      <div class="aside-inner">
        <div class="brand">
          <span class="logo"><AppIcon name="play" :size="15" filled /></span>
          <span class="wordmark">Doriya<span>Media</span></span>
        </div>

        <div class="pitch">
          <span class="eyebrow">自建流媒体服务器</span>
          <h1 class="display-1">把你的网盘变成一座私人影院</h1>
          <p class="muted">
            挂载网盘、扫描媒体库、刮削海报,然后在电脑、手机、iPad 上直接播放。
            针对 iOS Safari 的播放决策引擎会自动选择最省事的那条路。
          </p>
        </div>

        <ul class="features">
          <li v-for="f in features" :key="f.t">
            <span class="fi"><AppIcon :name="f.icon" :size="16" /></span>
            <span>
              <b>{{ f.t }}</b>
              <em>{{ f.d }}</em>
            </span>
          </li>
        </ul>
      </div>
    </section>

    <!-- ================= right: form ================= -->
    <section class="form-side">
      <form class="form" @submit.prevent="submit">
        <div class="form-head">
          <h2>{{ setupNeeded ? '初始化管理员' : '登录' }}</h2>
          <p class="small">
            {{ setupNeeded ? '第一次使用,先创建一个管理员账号。' : '使用你的账号继续。' }}
          </p>
        </div>

        <div class="field">
          <label for="login-name">用户名</label>
          <input id="login-name" v-model="name" autocomplete="username" placeholder="admin" required :disabled="busy || checking" />
        </div>

        <div class="field">
          <label for="login-pw">密码</label>
          <div class="pw-wrap">
            <input
              id="login-pw" v-model="password" :type="showPw ? 'text' : 'password'"
              :autocomplete="setupNeeded ? 'new-password' : 'current-password'"
              :placeholder="setupNeeded ? '至少 4 位' : '输入密码'" required :disabled="busy || checking"
            />
            <button
              type="button" class="pw-toggle" :aria-label="showPw ? '隐藏密码' : '显示密码'"
              @click="showPw = !showPw"
            >
              <AppIcon :name="showPw ? 'eyeOff' : 'eye'" :size="17" />
            </button>
          </div>
          <span v-if="setupNeeded" class="hint">这个账号拥有全部管理权限,请妥善保管。</span>
        </div>

        <Transition name="fade">
          <div v-if="error" class="flash err"><AppIcon name="alert" :size="16" /> {{ error }}</div>
        </Transition>

        <button class="btn primary lg block" :disabled="busy || checking">
          <template v-if="busy">请稍候…</template>
          <template v-else-if="checking">检查服务…</template>
          <template v-else>
            <AppIcon name="lock" :size="16" />
            {{ setupNeeded ? '创建并进入' : '登录' }}
          </template>
        </button>

        <div class="foot tiny">
          <AppIcon name="wifi" :size="13" />
          <span>服务运行在本机 <code>{{ host }}</code> · 数据只保存在你自己的服务器上</span>
        </div>
      </form>
    </section>
  </div>
</template>

<style scoped>
.login { min-height: 100dvh; display: grid; grid-template-columns: 1.15fr 0.85fr; }

/* ---------- aside ---------- */
.aside { position: relative; overflow: hidden; display: flex; align-items: center; }
.art {
  position: absolute; inset: 0;
  background:
    radial-gradient(60% 55% at 78% 12%, rgba(240, 169, 46, 0.22), transparent 62%),
    radial-gradient(52% 60% at 14% 22%, rgba(74, 108, 255, 0.2), transparent 64%),
    radial-gradient(70% 70% at 40% 108%, rgba(220, 74, 120, 0.16), transparent 66%),
    linear-gradient(160deg, #0d1018, #07080b 62%);
}
.art::after {
  content: "";
  position: absolute; inset: 0;
  background-image:
    linear-gradient(rgba(255, 255, 255, 0.03) 1px, transparent 1px),
    linear-gradient(90deg, rgba(255, 255, 255, 0.03) 1px, transparent 1px);
  background-size: 56px 56px, 56px 56px;
  mask-image: radial-gradient(70% 60% at 40% 40%, #000 20%, transparent 78%);
  -webkit-mask-image: radial-gradient(70% 60% at 40% 40%, #000 20%, transparent 78%);
}
.aside-scrim {
  position: absolute; inset: 0;
  background: linear-gradient(100deg, rgba(6, 7, 10, 0.72) 30%, rgba(6, 7, 10, 0.2) 100%);
}
.aside-inner {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: clamp(24px, 5vh, 56px);
  padding: clamp(32px, 5vw, 72px);
  max-width: 720px;
}
.brand { display: flex; align-items: center; gap: 11px; }
.logo {
  display: grid; place-items: center;
  width: 34px; height: 34px;
  border-radius: 11px;
  background: linear-gradient(150deg, var(--brand-2), #d98a12 70%);
  color: var(--brand-ink);
  box-shadow: 0 6px 20px rgba(240, 169, 46, 0.3), inset 0 1px 0 rgba(255, 255, 255, 0.5);
}
.wordmark { font-family: var(--font-display); font-size: 19px; font-weight: 800; letter-spacing: -0.035em; }
.wordmark span { color: var(--text-3); font-weight: 600; }
.pitch { display: flex; flex-direction: column; gap: 12px; }
.pitch h1 { margin: 4px 0 2px; }
.pitch p { max-width: 46ch; margin: 0; }

.features { list-style: none; display: grid; grid-template-columns: 1fr 1fr; gap: var(--sp-4); margin: 0; padding: 0; }
.features li { display: flex; gap: 12px; align-items: flex-start; }
.fi {
  display: grid; place-items: center;
  width: 36px; height: 36px; flex: none;
  border-radius: var(--r-m);
  background: rgba(255, 255, 255, 0.06);
  border: 1px solid var(--line-2);
  color: var(--brand);
}
.features b { display: block; font-size: var(--fs-14); font-weight: 650; }
.features em { font-style: normal; font-size: var(--fs-12); color: var(--text-4); line-height: 1.5; }

/* ---------- form ---------- */
.form-side { display: flex; align-items: center; justify-content: center; padding: clamp(24px, 5vw, 56px); background: var(--bg-0); }
.form { width: 100%; max-width: 380px; display: flex; flex-direction: column; }
.form-head { margin-bottom: var(--sp-5); }
.form-head h2 { font-size: clamp(22px, 3vw, 28px); margin-bottom: 6px; }
.form-head p { margin: 0; }
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
.foot { display: flex; align-items: center; gap: 7px; margin-top: var(--sp-5); line-height: 1.5; }
.foot code { font-family: var(--font-mono); color: var(--text-3); }

@media (max-width: 980px) {
  .login { grid-template-columns: 1fr; }
  .aside { display: none; }
  .form-side {
    min-height: 100dvh;
    padding: max(28px, env(safe-area-inset-top)) 22px calc(28px + env(safe-area-inset-bottom));
    background:
      radial-gradient(80% 50% at 80% 0%, rgba(240, 169, 46, 0.14), transparent 60%),
      radial-gradient(60% 50% at 10% 10%, rgba(74, 108, 255, 0.12), transparent 62%),
      var(--bg-0);
  }
  .form { max-width: 420px; }
}
</style>
