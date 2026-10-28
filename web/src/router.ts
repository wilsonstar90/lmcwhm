import { createRouter, createWebHistory } from 'vue-router'
import { useSession } from './stores/session'

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
        { path: 'backup', name: 'backup', component: () => import('./views/Backup.vue') },
        { path: 'ftp', name: 'ftp', component: () => import('./views/Ftp.vue') },
        { path: 'processes', name: 'processes', component: () => import('./views/Processes.vue') },
        { path: 'uptime', name: 'uptime', component: () => import('./views/Uptime.vue') },
        { path: 'alerts', name: 'alerts', component: () => import('./views/Alerts.vue') },
        { path: 'ai', name: 'ai', component: () => import('./views/Assistant.vue') },
        { path: 'security', name: 'security', component: () => import('./views/Security.vue') },
        { path: 'monitor', name: 'monitor', component: () => import('./views/Monitor.vue') },
        { path: 'logs', name: 'logs', component: () => import('./views/Logs.vue') },
        { path: 'settings', name: 'settings', component: () => import('./views/Settings.vue') },
        { path: 'toolbox', name: 'toolbox', component: () => import('./views/Toolbox.vue') },
        { path: 'mail', name: 'mail', component: () => import('./views/Mail.vue') },
        { path: 'api-docs', name: 'apidocs', component: () => import('./views/ApiDocs.vue') },
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
