import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { get, post } from '../api/client'
import type { AppUpdate } from '../api/types'

// Patch-update state of installed apps ("app:<id>") and PHP versions ("php:<v>").
export const useUpdates = defineStore('updates', () => {
  const items = ref<Record<string, AppUpdate>>({})
  const checking = ref(false)

  async function load() {
    items.value = (await get<{ items: Record<string, AppUpdate> }>('/apps/updates')).items ?? {}
  }
  async function check() {
    checking.value = true
    try {
      items.value = (await post<{ items: Record<string, AppUpdate> }>('/apps/updates/check')).items ?? {}
    } finally {
      checking.value = false
    }
  }
  const available = computed(() => Object.values(items.value).filter((u) => u.available))
  const checkedAt = computed(() => Math.max(0, ...Object.values(items.value).map((u) => u.checked_at || 0)))
  const forApp = (id: number) => items.value[`app:${id}`]
  const forPHP = (v: string) => items.value[`php:${v}`]
  return { items, checking, load, check, available, checkedAt, forApp, forPHP }
})
