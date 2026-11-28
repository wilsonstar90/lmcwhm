import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import { i18n, setLang, storedLang, type Lang } from './i18n'
import { router } from './router'
import { setUnauthorizedHandler } from './api/client'
import { useSession } from './stores/session'

const standalone = window.matchMedia('(display-mode: standalone)').matches || (navigator as Navigator & { standalone?: boolean }).standalone === true
if (standalone && window.innerWidth > window.screen.width) {
  document.querySelector('meta[name="viewport"]')?.setAttribute('content', `width=${window.screen.width}, initial-scale=1.0, viewport-fit=cover`)
}

const app = createApp(App)
const pinia = createPinia()
app.use(pinia).use(i18n).use(router)

let authRedirecting = false
setUnauthorizedHandler((kind) => {
  const session = useSession(pinia)
  session.user = null
  const current = router.currentRoute.value
  if (current.name === 'login' && kind === 'panel') return
  const redirect = current.name !== 'login' && current.fullPath.startsWith('/') ? current.fullPath : '/'
  if (kind === 'access') {
    if (!authRedirecting) {
      authRedirecting = true
      window.location.assign(`/login?redirect=${encodeURIComponent(redirect)}`)
    }
    return
  }
  router.push({ name: 'login', query: { redirect } }).catch(() => {})
})

function recoverSession() {
  const session = useSession(pinia)
  if (!document.hidden && session.user) session.restore().catch(() => {})
}
document.addEventListener('visibilitychange', recoverSession)
window.addEventListener('pageshow', recoverSession)
window.addEventListener('online', recoverSession)
window.setInterval(recoverSession, 60_000)

async function boot() {
  let lang: Lang = storedLang() ?? 'zh-HK'
  if (!storedLang()) {
    // First visit: follow the panel's configured language.
    try {
      const m = await useSession(pinia).loadMeta()
      lang = m.language as Lang
    } catch {
      /* keep default */
    }
  }
  await setLang(lang)
  await router.isReady()
  app.mount('#app')
  const url = new URL(window.location.href)
  if (url.searchParams.has('_pwa_refresh')) {
    url.searchParams.delete('_pwa_refresh')
    window.history.replaceState(null, '', url)
  }
  const splash = document.getElementById('boot')
  if (splash) {
    splash.classList.add('is-ready')
    window.setTimeout(() => splash.remove(), 250)
  }
  // Installable app (PWA). Browsers refuse service workers on untrusted
  // certificates (the self-signed panel SSL), which is harmless.
  if ('serviceWorker' in navigator && import.meta.env.PROD) {
    navigator.serviceWorker.register('/sw.js').catch(() => {})
  }
}
boot()
