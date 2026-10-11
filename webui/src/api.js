// Minimal API client with cookie auth.
export class ApiError extends Error {
  constructor(status, message) {
    super(message)
    this.status = status
  }
}

async function request(method, url, body) {
  const opts = { method, credentials: 'include', headers: {} }
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json'
    opts.body = JSON.stringify(body)
  }
  const resp = await fetch(url, opts)
  const text = await resp.text()
  let data = null
  try { data = text ? JSON.parse(text) : null } catch { data = text }
  if (!resp.ok) {
    const msg = data && data.error ? data.error : `HTTP ${resp.status}`
    throw new ApiError(resp.status, msg)
  }
  return data
}

export const api = {
  get: (url) => request('GET', url),
  post: (url, body) => request('POST', url, body ?? {}),
  put: (url, body) => request('PUT', url, body),
  del: (url) => request('DELETE', url),
}

export function imgUrl(itemId, kind) {
  return `/api/img/${itemId}/${kind}`
}

// Client capability class mirrors the server-side decision.
export function detectClient() {
  const ua = navigator.userAgent
  const isIOS = /iPhone|iPad|iPod/.test(ua) || (/Macintosh/.test(ua) && 'ontouchend' in document)
  if (isIOS) return 'safari'
  const isSafari = /Safari/.test(ua) && !/Chrome|Chromium|Edg|OPR/.test(ua)
  if (isSafari) return 'safari'
  if (/Firefox/.test(ua)) return 'firefox'
  return 'chrome'
}

export function fmtSize(n) {
  if (!n) return '-'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let v = n
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++ }
  return `${v.toFixed(v >= 10 || i === 0 ? 0 : 1)} ${units[i]}`
}

export function fmtTime(sec) {
  if (!sec || sec < 0) return '0:00'
  const s = Math.floor(sec % 60)
  const m = Math.floor((sec / 60) % 60)
  const h = Math.floor(sec / 3600)
  return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}` : `${m}:${String(s).padStart(2, '0')}`
}
