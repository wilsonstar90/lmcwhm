import { createI18n } from 'vue-i18n'
import zhHK from './locales/zh-HK.json'

export const LANGUAGES = [
  { value: 'zh-HK', label: '繁體中文（香港）' },
  { value: 'zh-TW', label: '繁體中文（台灣）' },
  { value: 'zh-CN', label: '简体中文' },
  { value: 'en', label: 'English' },
  { value: 'ja', label: '日本語' },
] as const

export type Lang = (typeof LANGUAGES)[number]['value']

const loaders: Record<Lang, () => Promise<{ default: Record<string, unknown> }>> = {
  'zh-HK': () => import('./locales/zh-HK.json'),
  'zh-TW': () => import('./locales/zh-TW.json'),
  'zh-CN': () => import('./locales/zh-CN.json'),
  en: () => import('./locales/en.json'),
  ja: () => import('./locales/ja.json'),
}

export const i18n = createI18n({
  legacy: false,
  locale: 'zh-HK' as string,
  fallbackLocale: 'en',
  messages: { 'zh-HK': zhHK } as Record<string, typeof zhHK>,
})

const STORAGE_KEY = 'lmcwhm.lang'

export function storedLang(): Lang | null {
  try {
    const v = localStorage.getItem(STORAGE_KEY)
    return LANGUAGES.some((l) => l.value === v) ? (v as Lang) : null
  } catch {
    return null
  }
}

export async function setLang(lang: Lang) {
  if (!i18n.global.availableLocales.includes(lang)) {
    const mod = await loaders[lang]()
    i18n.global.setLocaleMessage(lang, mod.default as never)
  }
  if (lang !== 'en' && !i18n.global.availableLocales.includes('en')) {
    i18n.global.setLocaleMessage('en', (await loaders.en()).default as never)
  }
  i18n.global.locale.value = lang
  document.documentElement.lang = lang
  try {
    localStorage.setItem(STORAGE_KEY, lang)
  } catch {
    /* private mode: language just won't persist */
  }
}
