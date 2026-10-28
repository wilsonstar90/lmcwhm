import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import { i18n, setLang, storedLang, type Lang } from './i18n'
import { router } from './router'
import { setUnauthorizedHandler } from './api/client'
import { useSession } from './stores/session'

const app = createApp(App)
const pinia = createPinia()
app.use(pinia).use(i18n).use(router)

setUnauthorizedHandler(() => {
  useSession(pinia).user = null
  if (router.currentRoute.value.name !== 'login') router.push({ name: 'login' })
})

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
  app.mount('#app')
  // Installable app (PWA). Browsers refuse service workers on untrusted
  // certificates (the self-signed panel SSL), which is harmless.
  if ('serviceWorker' in navigator && import.meta.env.PROD) {
    navigator.serviceWorker.register('/sw.js').catch(() => {})
  }
}
boot()
