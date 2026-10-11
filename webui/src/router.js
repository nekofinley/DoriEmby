import { createRouter, createWebHistory } from 'vue-router'
import { api } from './api.js'

const routes = [
  { path: '/login', component: () => import('./views/Login.vue'), meta: { public: true } },
  { path: '/', component: () => import('./views/Home.vue') },
  { path: '/library/:id', component: () => import('./views/Library.vue') },
  { path: '/item/:id', component: () => import('./views/Detail.vue') },
  { path: '/play/:fileId', component: () => import('./views/Player.vue'), meta: { fullscreen: true } },
  { path: '/settings', component: () => import('./views/Settings.vue') },
  { path: '/:pathMatch(.*)*', redirect: '/' },
]

export const router = createRouter({
  history: createWebHistory(),
  routes,
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
    window.__doriyaAuth = () => { authChecked = false }
  }
  if (!authOk) return { path: '/login', query: { redirect: to.fullPath } }
  return true
})
