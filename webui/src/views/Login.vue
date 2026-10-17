<script setup>
import { ref, computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { api } from '../api.js'
import AppIcon from '../components/AppIcon.vue'
import Turnstile from '../components/Turnstile.vue'
import SiteText from '../components/SiteText.vue'
import { site, listOf, setToolbar, toolbarOn } from '../site/store.js'
import { ACCENTS, CORNERS, SPLITS, accentVars } from '../site/schema.js'

const route = useRoute()
const name = ref('')
const password = ref('')
const confirm = ref('')
const email = ref('')
const showPw = ref(false)
const error = ref('')
const notice = ref('')
const busy = ref(false)
const checking = ref(true)
const mode = ref('login') // login | register | forgot
const turnstileToken = ref('')
const turnstileStatus = ref('loading')
const turnstile = ref(null)
const bootstrap = ref({
  setup_needed: false, can_register: false, registration: 'open', min_password: 4,
  require_email: false, mail_enabled: false, turnstile: { enabled: false, site_key: '' },
})

const setupNeeded = computed(() => !!bootstrap.value.setup_needed)
const canRegister = computed(() => !setupNeeded.value && !!bootstrap.value.can_register)
const minPassword = computed(() => bootstrap.value.min_password || 4)
const needsEmail = computed(() => !!bootstrap.value.require_email)
const turnstileOn = computed(() => !!bootstrap.value.turnstile?.enabled)
const siteKey = computed(() => bootstrap.value.turnstile?.site_key || '')
// The admin turned verification on but the server cannot send mail, so signup
// is closed from the visitor's point of view. Say why instead of hiding the tab.
const mailBroken = computed(() => !!bootstrap.value.require_email && !bootstrap.value.mail_enabled)
// A challenge that never loaded cannot be solved, and submitting without a
// token would only be rejected by the server.
const blocked = computed(() => turnstileOn.value && turnstileStatus.value === 'failed')
// "审核" mode is the admin saying yes, but not yet: worth spelling out so a new
// arrival is not left wondering why the sign-in they just created fails.
const needsApproval = computed(() => bootstrap.value.registration === 'approval' && canRegister.value)

onMounted(async () => {
  try {
    bootstrap.value = { ...bootstrap.value, ...(await api.get('/api/bootstrap')) }
    if (setupNeeded.value) name.value = 'admin'
    if (!turnstileOn.value) turnstileStatus.value = 'ready'
  } catch (e) {
    error.value = e.message
  } finally {
    checking.value = false
  }
})

function switchMode(m) {
  if (mode.value === m) return
  mode.value = m
  error.value = ''
  notice.value = ''
  password.value = ''
  confirm.value = ''
}

// One submit handler for three forms: setup, signup and sign-in all end the same
// way (a session and a redirect), while "forgot" only sends mail.
async function submit() {
  error.value = ''
  notice.value = ''
  if (mode.value === 'register' && password.value !== confirm.value) {
    error.value = '两次输入的密码不一致'
    return
  }
  if (blocked.value) {
    error.value = '人机验证组件没有加载成功,请联系管理员'
    return
  }
  busy.value = true
  try {
    if (setupNeeded.value) {
      await api.post('/api/setup', { name: name.value, password: password.value })
    } else if (mode.value === 'forgot') {
      const res = await api.post('/api/auth/forgot-password', {
        login: name.value, turnstile_token: turnstileToken.value,
      })
      notice.value = res?.message || '如果这个账号存在并且绑定了邮箱,重置密码的邮件已经发出'
      turnstile.value?.reset()
      busy.value = false
      return
    } else if (mode.value === 'register') {
      const res = await api.post('/api/auth/register', {
        name: name.value, password: password.value, email: email.value,
        turnstile_token: turnstileToken.value,
      })
      // Approval mode and email verification both create the account but refuse
      // to sign it in yet.
      if (res?.pending || res?.created) {
        notice.value = res.message || '注册成功,等待管理员审核通过后即可登录'
        mode.value = 'login'
        password.value = ''
        confirm.value = ''
        turnstile.value?.reset()
        busy.value = false
        return
      }
    } else {
      await api.post('/api/auth/login', {
        name: name.value, password: password.value, turnstile_token: turnstileToken.value,
      })
    }
    location.href = route.query.redirect || '/'
  } catch (e) {
    error.value = e.message
    // A challenge is consumed by the attempt that used it, so a retry needs a
    // fresh one or the server would reject it as a duplicate.
    turnstile.value?.reset()
    busy.value = false
  }
}

// ---------------------------------------------------------- site content ---

const layout = computed(() => site.doc.login.layout)
const features = computed(() => listOf('login.features'))
// Which set of form copy applies right now; the same key drives the panel.
const formMode = computed(() => (setupNeeded.value ? 'setup' : mode.value))
const submitIcon = computed(() => ({ setup: 'lock', register: 'user', forgot: 'link', login: 'lock' }[formMode.value]))

function rgba(hex, alpha) {
  const h = String(hex).replace('#', '')
  const full = h.length === 3 ? h.split('').map((c) => c + c).join('') : h
  const n = parseInt(full, 16)
  return `rgba(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255}, ${alpha})`
}

// Everything the layout panel offers, handed to CSS as variables so the page
// rearranges itself without a single conditional class in the markup.
const loginStyle = computed(() => {
  const l = layout.value || {}
  const art = { subtle: 'saturate(0.75) brightness(0.62)', normal: 'none', bold: 'saturate(1.3) brightness(1.32)' }
  return {
    '--lg-cols': (SPLITS.find((s) => s.id === l.split) || SPLITS[0]).cols,
    '--lg-form-max': `${l.formWidth || 380}px`,
    '--lg-feature-cols': String(l.featureCols === 1 ? 1 : 2),
    '--lg-art': art[l.artTone] || 'none',
    '--lg-art-glow': rgba(ACCENTS.find((x) => x.id === site.doc.site.accent)?.brand || '#f0a92e', 0.22),
    ...(CORNERS[l.corners] || {}),
    ...accentVars(site.doc.site.accent),
  }
})
</script>

<template>
  <div
    class="login"
    :class="{
      'aside-right': layout.aside === 'right',
      'no-art': !layout.showArt,
      'no-features': !layout.showFeatures,
    }"
    :style="loginStyle"
  >
    <!-- ================= left: brand story ================= -->
    <section class="aside">
      <div class="art"></div>
      <div class="aside-scrim"></div>
      <div class="aside-inner">
        <div class="brand">
          <span class="logo"><AppIcon name="play" :size="15" filled /></span>
          <span class="wordmark">
            <SiteText k="login.brand.name" /><SiteText k="login.brand.suffix" class="wm-suffix" />
          </span>
        </div>

        <div class="pitch">
          <SiteText k="login.pitch.eyebrow" as="span" class="eyebrow" />
          <SiteText k="login.pitch.title" as="h1" class="display-1" />
          <SiteText k="login.pitch.body" as="p" class="muted" multiline />
        </div>

        <ul class="features">
          <li v-for="(f, i) in features" :key="i">
            <span class="fi"><AppIcon :name="f.icon" :size="16" /></span>
            <span>
              <SiteText :k="`login.features.${i}.t`" as="b" />
              <SiteText :k="`login.features.${i}.d`" as="em" />
            </span>
          </li>
        </ul>
      </div>
    </section>

    <!-- ================= right: form ================= -->
    <section class="form-side">
      <form class="form" @submit.prevent="submit">
        <div class="form-head">
          <SiteText :k="`login.form.${formMode}.title`" as="h2" />
          <SiteText :k="`login.form.${formMode}.desc`" as="p" class="small" multiline />
        </div>

        <div v-if="canRegister" class="mode-switch" role="tablist">
          <button
            type="button" role="tab" :aria-selected="mode === 'login'"
            :class="{ on: mode === 'login' }" @click="switchMode('login')"
          ><SiteText k="login.form.tabLogin" passive /></button>
          <button
            type="button" role="tab" :aria-selected="mode === 'register'"
            :class="{ on: mode === 'register' }" @click="switchMode('register')"
          ><SiteText k="login.form.tabRegister" passive /></button>
        </div>

        <div v-if="mailBroken && mode !== 'forgot'" class="flash warn">
          <AppIcon name="alert" :size="16" />
          <span>管理员开启了邮箱验证,但服务器还没配好邮件发送,暂时不能注册。</span>
        </div>

        <div class="field">
          <label for="login-name">
            <SiteText :k="`login.form.labels.${mode === 'forgot' ? 'nameOrMail' : 'name'}`" passive />
          </label>
          <input
            id="login-name" v-model="name" autocomplete="username"
            :placeholder="mode === 'register' ? '2-32 个字符' : mode === 'forgot' ? 'admin 或 you@example.com' : 'admin'"
            required :disabled="busy || checking"
          />
        </div>

        <div v-if="mode !== 'forgot'" class="field">
          <label for="login-pw"><SiteText k="login.form.labels.password" passive /></label>
          <div class="pw-wrap">
            <input
              id="login-pw" v-model="password" :type="showPw ? 'text' : 'password'"
              :autocomplete="setupNeeded || mode === 'register' ? 'new-password' : 'current-password'"
              :placeholder="setupNeeded || mode === 'register' ? `至少 ${minPassword} 位` : '输入密码'"
              required :disabled="busy || checking"
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

        <div v-if="mode === 'register' && needsEmail" class="field">
          <label for="login-email"><SiteText k="login.form.labels.email" passive /></label>
          <input
            id="login-email" v-model="email" type="email" autocomplete="email"
            placeholder="you@example.com" required :disabled="busy || checking"
          />
          <span class="hint">会发一封验证邮件,点里面的链接后才能登录。</span>
        </div>

        <div v-if="mode === 'register'" class="field">
          <label for="login-pw2"><SiteText k="login.form.labels.confirm" passive /></label>
          <input
            id="login-pw2" v-model="confirm" :type="showPw ? 'text' : 'password'"
            autocomplete="new-password" placeholder="再输入一次" required :disabled="busy || checking"
          />
        </div>

        <Turnstile
          v-if="turnstileOn && !setupNeeded"
          ref="turnstile"
          :site-key="siteKey"
          @update:token="turnstileToken = $event"
          @update:status="turnstileStatus = $event"
        />

        <Transition name="fade">
          <div v-if="error" class="flash err"><AppIcon name="alert" :size="16" /> {{ error }}</div>
        </Transition>

        <Transition name="fade">
          <div v-if="notice" class="flash ok"><AppIcon name="check" :size="16" /> {{ notice }}</div>
        </Transition>

        <button class="btn primary lg block" :disabled="busy || checking || blocked">
          <template v-if="busy">请稍候…</template>
          <template v-else-if="checking">检查服务…</template>
          <template v-else>
            <AppIcon :name="submitIcon" :size="16" />
            <SiteText :k="`login.form.submit.${formMode}`" passive />
          </template>
        </button>

        <div v-if="!setupNeeded && bootstrap.mail_enabled" class="form-links">
          <button v-if="mode !== 'forgot'" type="button" class="linkish" @click="switchMode('forgot')">
            <SiteText k="login.form.forgotLink" passive />
          </button>
          <button v-else type="button" class="linkish" @click="switchMode('login')">
            <SiteText k="login.form.backToLogin" passive />
          </button>
        </div>

        <p v-if="needsApproval" class="hint approval">
          <AppIcon name="clock" :size="13" />
          注册后需要管理员审核通过才能登录<template v-if="needsEmail">,并需要先完成邮箱验证</template>。
        </p>

        <div class="foot tiny">
          <AppIcon name="wifi" :size="13" />
          <SiteText k="login.form.foot" as="span" multiline />
        </div>
      </form>
    </section>

    <!-- the way in for the owner: only an admin ever sees this -->
    <button v-if="site.canEdit && !toolbarOn" class="st-handle" @click="setToolbar(true)">
      <AppIcon name="wand" :size="14" /> 编辑此页
    </button>
  </div>
</template>

<style scoped>
.login {
  min-height: 100dvh;
  display: grid;
  grid-template-columns: var(--lg-cols, 1.15fr 0.85fr);
}
/* "品牌面板在右侧" — the two halves swap places without changing the markup. */
.login.aside-right .aside { order: 2; }
.login.aside-right .form-side { order: 1; }

/* ---------- aside ---------- */
.aside { position: relative; overflow: hidden; display: flex; align-items: center; }
.art {
  position: absolute; inset: 0;
  filter: var(--lg-art, none);
  background:
    radial-gradient(60% 55% at 78% 12%, var(--lg-art-glow, rgba(240, 169, 46, 0.22)), transparent 62%),
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
.login.no-art .art, .login.no-art .aside-scrim { display: none; }
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
  background: linear-gradient(150deg, var(--brand-2), var(--brand) 70%);
  color: var(--brand-ink);
  box-shadow: 0 6px 20px var(--brand-tint), inset 0 1px 0 rgba(255, 255, 255, 0.5);
}
.wordmark { font-family: var(--font-display); font-size: 19px; font-weight: 800; letter-spacing: -0.035em; }
.wordmark .wm-suffix { color: var(--text-3); font-weight: 600; }
.pitch { display: flex; flex-direction: column; gap: 12px; }
.pitch h1 { margin: 4px 0 2px; }
.pitch p { max-width: 46ch; margin: 0; }

.features {
  list-style: none;
  display: grid;
  grid-template-columns: repeat(var(--lg-feature-cols, 2), minmax(0, 1fr));
  gap: var(--sp-4);
  margin: 0; padding: 0;
}
.login.no-features .features { display: none; }
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
.form { width: 100%; max-width: var(--lg-form-max, 380px); display: flex; flex-direction: column; }
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

/* The accent the layout panel picks has to reach the shared form controls too. */
.login :deep(.field input:focus) { box-shadow: 0 0 0 3px var(--brand-tint); }

/* login / register switch — the same segmented look the rest of the app uses */
.mode-switch {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 3px;
  padding: 3px;
  margin-bottom: var(--sp-5);
  background: rgba(0, 0, 0, 0.32);
  border: 1px solid var(--line-1);
  border-radius: var(--r-m);
}
.mode-switch button {
  height: 34px;
  border-radius: var(--r-s);
  color: var(--text-3);
  font-size: var(--fs-13);
  font-weight: 600;
  transition: color var(--t-fast), background var(--t-fast);
}
.mode-switch button:hover { color: var(--text-1); }
.mode-switch button.on { background: var(--bg-3); color: var(--text-1); box-shadow: var(--sh-1); }

.approval { display: flex; align-items: center; gap: 6px; margin: var(--sp-4) 0 0; text-align: center; justify-content: center; }

/* 忘记密码 / 返回登录 */
.form-links { display: flex; justify-content: center; margin-top: var(--sp-4); }
.linkish {
  font-size: var(--fs-13);
  color: var(--text-3);
  text-decoration: underline;
  text-underline-offset: 3px;
  transition: color var(--t-fast);
}
.linkish:hover { color: var(--brand); }

@media (max-width: 980px) {
  .login { grid-template-columns: 1fr; }
  .aside { display: none; }
  .form-side {
    min-height: 100dvh;
    padding: max(28px, env(safe-area-inset-top)) 22px calc(28px + env(safe-area-inset-bottom));
    background:
      radial-gradient(80% 50% at 80% 0%, var(--lg-art-glow, rgba(240, 169, 46, 0.14)), transparent 60%),
      radial-gradient(60% 50% at 10% 10%, rgba(74, 108, 255, 0.12), transparent 62%),
      var(--bg-0);
  }
  .form { max-width: 420px; }
}
</style>
