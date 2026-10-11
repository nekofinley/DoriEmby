import { createRouter, createWebHistory } from 'vue-router'
import { api } from './api.js'

const routes = [
  { path: '/login', component: () => import('./views/Login.vue'), meta: { public: true, chrome: false } },
  { path: '/', component: () => import('./views/Home.vue'), meta: { title: '首页', overlay: true } },
  { path: '/library/:id', component: () => import('./views/Library.vue'), meta: { title: '媒体库', overlay: true } },
  { path: '/item/:id', component: () => import('./views/Detail.vue'), meta: { title: '详情', overlay: true } },
  { path: '/season/:id', component: () => import('./views/Season.vue'), meta: { title: '剧集', overlay: true } },
  { path: '/browse/:id?', component: () => import('./views/Browse.vue'), meta: { title: '文件' } },
  { path: '/play/:fileId', component: () => import('./views/Player.vue'), meta: { fullscreen: true, title: '播放' } },
  { path: '/settings', component: () => import('./views/Settings.vue'), meta: { title: '设置' } },
  { path: '/:pathMatch(.*)*', redirect: '/' },
]

export const router = createRouter({
  history: createWebHistory(),
  routes,
  scrollBehavior(to, from, saved) {
    if (saved) return saved
    if (to.path === from.path) return {}
    return { top: 0 }
  },
})

let authChecked = false
let authOk = false

router.beforeEach(async (to) => {
  if (to.meta.public) return true
  if (!authChecked) {
    try {
      await api.get('/api/auth/me')
      authOk = true
    } catch {
      authOk = false
    }
    authChecked = true
    window.__doriyamediaAuth = () => { authChecked = false }
  }
  if (!authOk) return { path: '/login', query: { redirect: to.fullPath } }
  return true
})

router.afterEach((to) => {
  const base = 'DoriyaMedia'
  document.title = to.meta.title ? `${to.meta.title} · ${base}` : base
})
