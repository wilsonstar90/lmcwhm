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
        { path: 'domains', name: 'domains', component: () => import('./views/Domains.vue') },
        { path: 'database', name: 'database', component: () => import('./views/Database.vue') },
        { path: 'docker', name: 'docker', component: () => import('./views/Docker.vue') },
        { path: 'appstore', name: 'appstore', component: () => import('./views/AppStore.vue') },
        { path: 'files', name: 'files', component: () => import('./views/Files.vue') },
        { path: 'terminal', name: 'terminal', component: () => import('./views/Terminal.vue') },
        { path: 'cron', name: 'cron', component: () => import('./views/Cron.vue') },
        { path: 'security', name: 'security', ...soon('M5') },
        { path: 'monitor', name: 'monitor', component: () => import('./views/Monitor.vue') },
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
