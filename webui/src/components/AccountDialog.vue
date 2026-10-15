<script setup>
import { ref, computed, watch } from 'vue'
import { api } from '../api.js'
import { me, resetAuth, logout } from '../auth.js'
import AppIcon from './AppIcon.vue'
import { toast } from '../toast.js'

const props = defineProps({
  open: { type: Boolean, default: false },
})
const emit = defineEmits(['update:open'])

const name = ref('')
const email = ref('')
const current = ref('')
const next = ref('')
const confirm = ref('')
const busy = ref(false)
const emailBusy = ref(false)
const error = ref('')
const notice = ref('')

const emailPending = computed(() => !!me.value?.email && !me.value?.email_verified)

function close() { emit('update:open', false) }

// Every open starts from the account as the server knows it, never from what
// was typed into a previous, abandoned edit.
watch(() => props.open, (v) => {
  if (!v) return
  name.value = me.value?.name || ''
  email.value = me.value?.email || ''
  current.value = next.value = confirm.value = ''
  error.value = ''
  notice.value = ''
})

async function rename() {
  error.value = ''
  notice.value = ''
  if (!name.value.trim() || name.value === me.value?.name) return
  busy.value = true
  try {
    const res = await api.put('/api/auth/profile', { name: name.value })
    if (res?.user) me.value = res.user
    toast.ok('用户名已更新')
  } catch (e) {
    error.value = e.message
  } finally {
    busy.value = false
  }
}

// Binding an address is what makes "forgot password" possible, so it is offered
// to everyone — not only to people who registered after that feature existed.
async function saveEmail() {
  error.value = ''
  notice.value = ''
  const wanted = email.value.trim()
  if (!wanted || wanted === me.value?.email) return
  emailBusy.value = true
  try {
    const res = await api.put('/api/auth/email', { email: wanted })
    if (res?.user) me.value = res.user
    if (res?.ok) {
      notice.value = res.message || '验证邮件已发送'
      toast.ok(notice.value)
    } else {
      // The address was saved but the mail did not go out; saying "success"
      // here would leave someone waiting for a message that never comes.
      error.value = res?.error || '验证邮件发送失败'
    }
  } catch (e) {
    error.value = e.message
  } finally {
    emailBusy.value = false
  }
}

async function resendVerification() {
  error.value = ''
  notice.value = ''
  emailBusy.value = true
  try {
    const res = await api.post('/api/auth/email/resend')
    notice.value = res?.message || '验证邮件已重新发送'
  } catch (e) {
    error.value = e.message
  } finally {
    emailBusy.value = false
  }
}

async function changePassword() {
  error.value = ''
  notice.value = ''
  if (next.value !== confirm.value) {
    error.value = '两次输入的新密码不一致'
    return
  }
  busy.value = true
  try {
    await api.post('/api/auth/password', { current_password: current.value, new_password: next.value })
    current.value = next.value = confirm.value = ''
    toast.ok('密码已修改,其他设备需要重新登录')
  } catch (e) {
    error.value = e.message
  } finally {
    busy.value = false
  }
}

function signOut() {
  close()
  logout()
}
</script>

<template>
  <Transition name="modal">
    <div v-if="open" class="modal-mask" @click.self="close">
      <div class="modal acct" role="dialog" aria-modal="true" aria-label="账号设置">
        <div class="modal-head">
          <span class="av lg">{{ (me?.name || 'D').slice(0, 1).toUpperCase() }}</span>
          <div class="grow">
            <div class="acct-name">{{ me?.name }}</div>
            <div class="tiny">
              <span class="badge" :class="me?.role === 'admin' ? 'rating' : ''">
                {{ me?.role === 'admin' ? '管理员' : '普通用户' }}
              </span>
            </div>
          </div>
          <button class="icon-btn" aria-label="关闭" @click="close"><AppIcon name="close" :size="18" /></button>
        </div>

        <Transition name="fade">
          <div v-if="error" class="flash err"><AppIcon name="alert" :size="16" /> {{ error }}</div>
        </Transition>
        <Transition name="fade">
          <div v-if="notice" class="flash ok"><AppIcon name="check" :size="16" /> {{ notice }}</div>
        </Transition>

        <div class="field">
          <label for="acct-name">用户名</label>
          <div class="inline">
            <input id="acct-name" v-model="name" :disabled="busy" @keyup.enter="rename" />
            <button class="btn sm" :disabled="busy || !name.trim() || name === me?.name" @click="rename">改名</button>
          </div>
          <span class="hint">登录时使用的名字,改完立即生效。</span>
        </div>

        <!-- 邮箱:忘记密码时唯一的自救途径,所以每个账号都能绑 -->
        <div class="field">
          <label for="acct-email">邮箱</label>
          <div class="inline">
            <input
              id="acct-email" v-model="email" type="email" autocomplete="email"
              :placeholder="me?.email ? '' : 'you@example.com'" :disabled="emailBusy"
              @keyup.enter="saveEmail"
            />
            <button
              class="btn sm"
              :disabled="emailBusy || !email.trim() || email.trim() === (me?.email || '')"
              @click="saveEmail"
            >{{ me?.email ? '换绑' : '绑定' }}</button>
          </div>
          <span v-if="me?.email" class="hint">
            <template v-if="me.email_verified">
              <span class="badge ok">已验证</span> 忘记密码时可以用这个邮箱重置。
            </template>
            <template v-else>
              <span class="badge warn">未验证</span> 还没点邮件里的链接
              <button class="linkish" :disabled="emailBusy" @click="resendVerification">重新发送</button>
            </template>
          </span>
          <span v-else class="hint">绑定后忘记密码可以自己重置,不必找管理员。</span>
        </div>

        <form class="pw" @submit.prevent="changePassword">
          <div class="label-row"><AppIcon name="lock" :size="14" /> 修改密码</div>
          <div class="field">
            <label for="acct-cur">当前密码</label>
            <input id="acct-cur" v-model="current" type="password" autocomplete="current-password" :disabled="busy" required />
          </div>
          <div class="field">
            <label for="acct-new">新密码</label>
            <input id="acct-new" v-model="next" type="password" autocomplete="new-password" :disabled="busy" required />
          </div>
          <div class="field">
            <label for="acct-confirm">确认新密码</label>
            <input id="acct-confirm" v-model="confirm" type="password" autocomplete="new-password" :disabled="busy" required />
            <span class="hint">改完只有当前设备保持登录,其他设备需要重新登录。</span>
          </div>
          <button class="btn primary block" :disabled="busy || !current || !next">
            {{ busy ? '请稍候…' : '修改密码' }}
          </button>
        </form>

        <div class="acct-foot">
          <button class="btn ghost sm" @click="signOut"><AppIcon name="logout" :size="15" /> 退出登录</button>
        </div>
      </div>
    </div>
  </Transition>
</template>

<style scoped>
.acct { max-width: 440px; width: 100%; }
.modal-head .av.lg { width: 42px; height: 42px; font-size: var(--fs-16); }
.grow { flex: 1; min-width: 0; }
.acct-name { font-size: var(--fs-16); font-weight: 700; margin-bottom: 3px; }
.icon-btn { color: var(--text-4); }
.icon-btn:hover { color: var(--text-1); background: rgba(255, 255, 255, 0.07); }
.inline { display: flex; gap: 8px; align-items: center; }
.inline input { flex: 1; }
.pw { border-top: 1px solid var(--line-1); margin-top: var(--sp-4); padding-top: var(--sp-4); }
.label-row {
  display: flex; align-items: center; gap: 7px;
  font-size: var(--fs-12); font-weight: 600; color: var(--text-3);
  margin-bottom: var(--sp-3);
}
.acct-foot { border-top: 1px solid var(--line-1); margin-top: var(--sp-4); padding-top: var(--sp-3); display: flex; justify-content: flex-end; }
.linkish {
  font-size: var(--fs-12);
  color: var(--brand);
  text-decoration: underline;
  text-underline-offset: 3px;
}
.linkish:disabled { opacity: 0.5; }
</style>
