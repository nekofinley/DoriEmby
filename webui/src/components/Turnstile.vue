<script setup>
import { ref, onMounted, onBeforeUnmount, watch } from 'vue'

// Cloudflare Turnstile, rendered explicitly so the form controls when it appears
// and can reset it after a failed attempt (a token is single-use).
//
// The script is loaded once per page even if the widget mounts twice: the login
// page keeps it around while switching between 登录 / 注册 / 忘记密码.
const props = defineProps({
  siteKey: { type: String, required: true },
})
const emit = defineEmits(['update:token', 'update:status'])

const holder = ref(null)
// loading → the script is on its way; ready → a challenge is on screen;
// failed → it never arrived, which the form must not paper over.
const status = ref('loading')
const detail = ref('')

let widgetId = null
let disposed = false

let scriptPromise = null

function loadScript() {
  if (window.turnstile) return Promise.resolve()
  if (scriptPromise) return scriptPromise
  scriptPromise = new Promise((resolve, reject) => {
    const s = document.createElement('script')
    s.src = 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit'
    s.async = true
    s.defer = true
    s.onload = () => resolve()
    s.onerror = () => reject(new Error('无法加载人机验证组件'))
    document.head.appendChild(s)
  })
  return scriptPromise
}

function setStatus(next, why = '') {
  status.value = next
  detail.value = why
  emit('update:status', next)
}

function renderWidget() {
  if (disposed || !holder.value) return
  if (!props.siteKey) {
    setStatus('failed', '缺少 Site Key')
    return
  }
  try {
    widgetId = window.turnstile.render(holder.value, {
      sitekey: props.siteKey,
      theme: 'dark',
      language: 'zh-cn',
      size: 'flexible',
      callback: (token) => {
        setStatus('ready')
        emit('update:token', token)
      },
      'expired-callback': () => {
        setStatus('ready')
        emit('update:token', '')
      },
      'error-callback': () => {
        setStatus('ready')
        emit('update:token', '')
        detail.value = '验证组件报错,请重新点击'
      },
    })
  } catch (e) {
    setStatus('failed', e.message || '验证组件初始化失败')
  }
}

function reset() {
  emit('update:token', '')
  if (widgetId !== null && window.turnstile) {
    try { window.turnstile.reset(widgetId) } catch { /* the widget is gone */ }
  }
}

defineExpose({ reset })

onMounted(async () => {
  try {
    await loadScript()
  } catch (e) {
    // Worth showing rather than silently disabling the button: from mainland
    // China challenges.cloudflare.com is often unreachable, and the visitor
    // should be told that instead of staring at a dead form.
    setStatus('failed', e.message)
    return
  }
  if (disposed) return
  renderWidget()
})

onBeforeUnmount(() => {
  disposed = true
  if (widgetId !== null && window.turnstile) {
    try { window.turnstile.remove(widgetId) } catch { /* already gone */ }
  }
})

watch(() => props.siteKey, () => {
  if (status.value === 'failed') return
  if (widgetId !== null && window.turnstile) {
    try { window.turnstile.remove(widgetId) } catch { /* ignore */ }
    widgetId = null
  }
  renderWidget()
})
</script>

<template>
  <div class="turnstile">
    <div v-show="status !== 'failed'" ref="holder" class="holder"></div>
    <div v-if="status === 'failed'" class="flash err">
      <span>
        人机验证加载失败:{{ detail }}。
        这个组件需要浏览器能访问 <code>challenges.cloudflare.com</code>,国内网络通常需要代理。
      </span>
    </div>
  </div>
</template>

<style scoped>
.turnstile { margin-bottom: var(--sp-4); }
.holder { display: flex; justify-content: center; min-height: 65px; }
.flash code { font-family: var(--font-mono); font-size: var(--fs-12); }
</style>
