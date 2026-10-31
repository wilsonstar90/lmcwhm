<script setup lang="ts">
// 面板設定 → 介面 → Logo: the panel alias as text, or an uploaded image
// (PNG, SVG or WebP, at most 256 KB), shown in the header and on the login page.
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NCard, NRadio, NRadioGroup, NSpace, NText, useMessage } from 'naive-ui'
import { ApiError, getCsrf, post } from '../api/client'
import { useSession } from '../stores/session'
import BrandLogo from './BrandLogo.vue'

const { t } = useI18n()
const msg = useMessage()
const session = useSession()
const mode = ref<'text' | 'image'>(session.logoImage ? 'image' : 'text')
const file = ref<HTMLInputElement | null>(null)
const busy = ref(false)

async function upload(e: Event) {
  const f = (e.target as HTMLInputElement).files?.[0]
  ;(e.target as HTMLInputElement).value = ''
  if (!f) return
  if (f.size > 256 * 1024) {
    msg.error(t('logo.too_big'))
    return
  }
  busy.value = true
  try {
    const res = await fetch('/api/v1/settings/logo', { method: 'POST', credentials: 'same-origin', headers: { 'X-CSRF-Token': getCsrf() }, body: f })
    const data = await res.json().catch(() => ({}))
    if (!res.ok) throw new ApiError(res.status, data.code ?? 'common.internal', data.params)
    session.logoImage = true
    session.logoRev++
    msg.success(t('common.saved'))
  } catch (err) {
    msg.error(err instanceof ApiError ? err.text : String(err))
  } finally {
    busy.value = false
  }
}

async function useText() {
  mode.value = 'text'
  if (!session.logoImage) return
  try {
    await post('/settings/logo/delete')
    session.logoImage = false
    msg.success(t('common.saved'))
  } catch (err) {
    msg.error(err instanceof ApiError ? err.text : String(err))
  }
}
</script>

<template>
  <n-card size="small" :title="t('logo.title')" style="margin-bottom: 16px">
    <n-space vertical :size="12">
      <n-radio-group :value="mode" @update:value="(v: 'text' | 'image') => (v === 'text' ? useText() : (mode = 'image'))">
        <n-radio value="text">{{ t('logo.text') }}</n-radio>
        <n-radio value="image">{{ t('logo.image') }}</n-radio>
      </n-radio-group>
      <n-text v-if="mode === 'text'" depth="3" style="font-size: 12px">{{ t('logo.text_hint') }}</n-text>
      <n-space v-else align="center">
        <n-button :loading="busy" @click="file?.click()">{{ t('logo.upload') }}</n-button>
        <n-text depth="3" style="font-size: 12px">{{ t('logo.image_hint') }}</n-text>
        <input ref="file" type="file" accept="image/png,image/svg+xml,image/webp" hidden @change="upload" />
      </n-space>
      <div class="preview"><BrandLogo /></div>
    </n-space>
  </n-card>
</template>

<style scoped>
.preview {
  display: inline-flex;
  padding: 8px 12px;
  border: 1px dashed rgba(128, 128, 128, 0.35);
  border-radius: 6px;
}
</style>
