import { computed, ref, watchEffect, type ComputedRef } from 'vue'
import { useOsTheme } from 'naive-ui'

// Colour scheme: follow the system, or force light / dark (kept per browser).
export type ThemeMode = 'auto' | 'light' | 'dark'
const KEY = 'lmcwhm.theme'

function stored(): ThemeMode {
  try {
    const v = localStorage.getItem(KEY)
    if (v === 'light' || v === 'dark') return v
  } catch {
    /* storage blocked */
  }
  return 'auto'
}

export const themeMode = ref<ThemeMode>(stored())

export function setTheme(m: ThemeMode) {
  themeMode.value = m
  try {
    localStorage.setItem(KEY, m)
  } catch {
    /* storage blocked */
  }
}

let dark: ComputedRef<boolean> | null = null

// useDark must first run inside a component setup (it watches the OS scheme).
// It also mirrors the choice to <html data-theme> for plain CSS.
export function useDark(): ComputedRef<boolean> {
  if (!dark) {
    const os = useOsTheme()
    const d = computed(() => (themeMode.value === 'auto' ? os.value === 'dark' : themeMode.value === 'dark'))
    watchEffect(() => {
      document.documentElement.dataset.theme = d.value ? 'dark' : 'light'
    })
    dark = d
  }
  return dark
}
