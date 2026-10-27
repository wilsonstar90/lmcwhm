import { defineStore } from 'pinia'
import { ref } from 'vue'
import { get, post, setCsrf } from '../api/client'
import type { User } from '../api/types'

export const useSession = defineStore('session', () => {
  const user = ref<User | null>(null)
  const alias = ref('lmcwhm')
  const version = ref('')

  async function loadMeta() {
    const m = await get<{ alias: string; language: string; version: string }>('/public/meta')
    alias.value = m.alias
    version.value = m.version
    return m
  }

  async function restore(): Promise<boolean> {
    try {
      const r = await get<{ user: User; csrf: string }>('/auth/me')
      user.value = r.user
      setCsrf(r.csrf)
      return true
    } catch {
      return false
    }
  }

  async function login(username: string, password: string, totp = '') {
    const r = await post<{ user?: User; csrf?: string; totp_required?: boolean }>('/auth/login', {
      username,
      password,
      totp,
    })
    if (r.totp_required) return 'totp' as const
    user.value = r.user!
    setCsrf(r.csrf!)
    return 'ok' as const
  }

  async function logout() {
    await post('/auth/logout').catch(() => {})
    user.value = null
    setCsrf('')
  }

  return { user, alias, version, loadMeta, restore, login, logout }
})
