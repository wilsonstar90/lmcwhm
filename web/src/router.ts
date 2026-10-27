import { createRouter, createWebHistory } from 'vue-router'
import { useSession } from './stores/session'

const soon = (milestone: string) => ({
  component: () => import('./views/Placeholder.vue'),
  props: { milestone },
})

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', name: 'login', component: () => import('./views/Login.vue'), meta: { public: true } },
    {
      path: '/',
      component: () => import('./views/Layout.vue'),
      children: [
        { path: '', name: 'home', component: () => import('./views/Dashboard.vue') },
        { path: 'website', name: 'website', component: () => import('./views/Website.vue') },
        { path: 'database', name: 'database', ...soon('M2') },
        { path: 'docker', name: 'docker', ...soon('M3') },
        { path: 'appstore', name: 'appstore', ...soon('M2') },
        { path: 'files', name: 'files', ...soon('M3') },
        { path: 'terminal', name: 'terminal', ...soon('M3') },
        { path: 'cron', name: 'cron', ...soon('M4') },
        { path: 'security', name: 'security', ...soon('M5') },
        { path: 'monitor', name: 'monitor', ...soon('M4') },
        { path: 'logs', name: 'logs', component: () => import('./views/Logs.vue') },
        { path: 'settings', name: 'settings', component: () => import('./views/Settings.vue') },
      ],
    },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})

let restored = false
router.beforeEach(async (to) => {
  const s = useSession()
  if (!restored) {
    restored = true
    await s.restore()
  }
  if (!to.meta.public && !s.user) return { name: 'login' }
  if (to.name === 'login' && s.user) return { name: 'home' }
})
