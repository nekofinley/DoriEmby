/**
 * The site's editable copy, in one document.
 *
 * DEFAULTS is exactly the wording and layout the pages shipped with, so an owner
 * who never opens the toolbar sees the site they already had. Everything the
 * editor can change lives here — no strings are duplicated in the components, and
 * a saved document missing a key (an older save, or a key added later) falls back
 * to the default through mergeDeep.
 */

export const DEFAULTS = {
  site: {
    title: 'DoriyaMedia',
    // Site-wide accent for the login page and the footer.
    accent: 'amber',
  },

  login: {
    layout: {
      aside: 'left',        // left | right — which half the brand story sits on
      split: 'brand',       // brand | even | form — column ratio
      showArt: true,        // the gradient + grid backdrop behind the story
      artTone: 'normal',    // subtle | normal | bold
      showFeatures: true,
      featureCols: 2,
      formWidth: 380,
      corners: 'soft',      // square | soft | round
    },
    brand: { name: 'Doriya', suffix: 'Media' },
    pitch: {
      eyebrow: '自建流媒体服务器',
      title: '把你的网盘变成一座私人影院',
      body: '挂载网盘、扫描媒体库、刮削海报,然后在电脑、手机、iPad 上直接播放。\n针对 iOS Safari 的播放决策引擎会自动选择最省事的那条路。',
    },
    features: [
      { icon: 'cloud', t: '挂载网盘', d: 'OpenList / WebDAV / 本地目录,扫描即入库' },
      { icon: 'sparkles', t: '智能刮削', d: 'TMDB 与 Bangumi 补全中文标题、海报与简介' },
      { icon: 'playCircle', t: '302 直连', d: '网盘直链直连播放,几乎不占服务器带宽' },
      { icon: 'layers', t: '自动转封装', d: 'iOS 播不了的 MKV / HEVC 自动封装成 HLS' },
    ],
    form: {
      // One headline + one line of explanation per mode the form can be in.
      login: { title: '登录', desc: '使用你的账号继续。' },
      register: { title: '注册', desc: '创建一个账号,登录后即可浏览与播放全部媒体。' },
      forgot: { title: '找回密码', desc: '填写用户名或邮箱,我们把重置链接发到你的邮箱。' },
      setup: { title: '初始化管理员', desc: '第一次使用,先创建一个管理员账号。' },
      submit: { login: '登录', register: '注册', forgot: '发送重置邮件', setup: '创建并进入' },
      tabLogin: '登录',
      tabRegister: '注册',
      forgotLink: '忘记密码?',
      backToLogin: '返回登录',
      labels: {
        name: '用户名',
        nameOrMail: '用户名或邮箱',
        password: '密码',
        email: '邮箱',
        confirm: '确认密码',
      },
      // {host} is replaced with the address in the browser's address bar.
      foot: '服务运行在本机 {host} · 数据只保存在你自己的服务器上',
    },
  },

  footer: {
    wordmark: { name: 'Doriya', suffix: 'Media' },
    about: '轻量自建流媒体服务器:挂载网盘、扫描媒体库、TMDB / Bangumi 刮削,\n针对 iOS Safari 自动选择 302 直连、代理或 HLS 转封装播放。',
    status: '本地服务运行中',
    columns: [
      {
        heading: '浏览',
        links: [
          { label: '首页', to: '/' },
          { label: '文件浏览', to: '/browse' },
        ],
      },
      {
        heading: '管理',
        links: [
          { label: '存储源 / 媒体库', to: '/settings' },
          { label: '刮削与播放', to: '/settings' },
        ],
      },
    ],
    capabilities: {
      heading: '播放能力',
      items: ['302 直连 · 不占带宽', 'HLS 转封装 · 视频直拷', 'iOS Safari 自动适配'],
    },
    // {year} is replaced with the current year.
    copyright: '© {year} DoriyaMedia · 仅供个人媒体库使用',
  },
}

/** Accent presets. Each one rewrites the brand tokens the stylesheet reads. */
export const ACCENTS = [
  { id: 'amber', label: '琥珀', brand: '#f0a92e', brand2: '#ffca62', hover: '#ffbd4d', ink: '#1a1204' },
  { id: 'blue', label: '海蓝', brand: '#4a90ff', brand2: '#7cb0ff', hover: '#6ba3ff', ink: '#04101f' },
  { id: 'violet', label: '紫罗兰', brand: '#9b6bff', brand2: '#b995ff', hover: '#ab84ff', ink: '#100722' },
  { id: 'emerald', label: '翡翠', brand: '#2fbf7f', brand2: '#69d9a5', hover: '#49cd90', ink: '#04180f' },
  { id: 'rose', label: '玫瑰', brand: '#f2547d', brand2: '#ff8aa8', hover: '#ff6c90', ink: '#210610' },
]

export const SPLITS = [
  { id: 'brand', label: '品牌优先', cols: '1.15fr 0.85fr' },
  { id: 'even', label: '对半', cols: '1fr 1fr' },
  { id: 'form', label: '表单优先', cols: '0.85fr 1.15fr' },
]

function rgba(hex, alpha) {
  const h = String(hex).replace('#', '')
  const full = h.length === 3 ? h.split('').map((c) => c + c).join('') : h
  const n = parseInt(full, 16)
  return `rgba(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255}, ${alpha})`
}

/**
 * The brand tokens for one accent preset, ready to hand to a :style binding.
 * Scoped to the page that applies it — the rest of the app keeps its own accent.
 */
export function accentVars(accentId) {
  const a = ACCENTS.find((x) => x.id === accentId) || ACCENTS[0]
  return {
    '--brand': a.brand,
    '--brand-2': a.brand2,
    '--accent-hover': a.hover,
    '--brand-ink': a.ink,
    '--brand-tint': rgba(a.brand, 0.14),
    '--sh-glow': `0 8px 26px ${rgba(a.brand, 0.3)}`,
  }
}

/** Radius scale per corner style; the keys mirror the stylesheet's tokens. */
export const CORNERS = {
  square: { '--r-xs': '3px', '--r-s': '4px', '--r-m': '6px', '--r-l': '8px', '--r-xl': '10px', '--r-pill': '999px' },
  soft: {},
  round: { '--r-xs': '8px', '--r-s': '12px', '--r-m': '18px', '--r-l': '24px', '--r-xl': '30px', '--r-pill': '999px' },
}

export const ART_TONES = {
  subtle: 0.45,
  normal: 1,
  bold: 1.35,
}

/** Icon names the feature editor offers, from the inline icon set. */
export const FEATURE_ICONS = [
  'cloud', 'sparkles', 'playCircle', 'layers', 'film', 'tv', 'server', 'hdd',
  'globe', 'folder', 'wifi', 'lock', 'user', 'scan', 'image', 'video',
  'star', 'heart', 'clock', 'link', 'download', 'speed', 'subtitles', 'audio', 'list', 'grid',
]

/** Routes offered as suggestions when editing a footer link target. */
export const ROUTE_SUGGESTIONS = ['/', '/browse', '/settings']

export function clone(v) {
  return JSON.parse(JSON.stringify(v))
}

/** Deep merge of a saved document onto the defaults; arrays are replaced whole. */
export function mergeDeep(base, patch) {
  if (Array.isArray(patch)) return clone(patch)
  if (patch === null || patch === undefined) return clone(base)
  if (typeof patch !== 'object') return patch
  if (typeof base !== 'object' || base === null || Array.isArray(base)) return clone(patch)
  const out = {}
  for (const k of new Set([...Object.keys(base), ...Object.keys(patch)])) {
    out[k] = k in patch ? mergeDeep(base[k], patch[k]) : clone(base[k])
  }
  return out
}

export function getPath(doc, path) {
  return String(path).split('.').reduce((o, k) => (o == null ? undefined : o[k]), doc)
}

export function setPath(doc, path, value) {
  const keys = String(path).split('.')
  const last = keys.pop()
  let node = doc
  for (const k of keys) {
    if (node[k] == null || typeof node[k] !== 'object') node[k] = {}
    node = node[k]
  }
  node[last] = value
}

/**
 * Substitute the few placeholders copy may carry. Kept deliberately small: the
 * owner writes text, not templates.
 */
export function fill(text, ctx = {}) {
  if (typeof text !== 'string') return ''
  return text
    .replace(/\{host\}/g, ctx.host || '')
    .replace(/\{year\}/g, String(new Date().getFullYear()))
    .replace(/\{version\}/g, ctx.version || '')
    .replace(/\{site\}/g, ctx.site || '')
    .replace(/\{user\}/g, ctx.user || '')
}

export const PLACEHOLDER_HINTS = ['{host}', '{year}', '{version}', '{site}', '{user}']
