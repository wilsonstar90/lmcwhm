<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NAlert, NButton, NCard, NCheckbox, NCode, NInput, NModal, NSpace, NTag, NText, useMessage } from 'naive-ui'
import { get, post, ApiError } from '../api/client'

interface Release { current: string; latest: string; newer: boolean; notes: string; url: string; published_at: string; error?: string }
interface Status { running: boolean; log: string; exit: number; version: string }

const { t } = useI18n()
const msg = useMessage()
const rel = ref<Release | null>(null)
const checking = ref(false)
const confirm = ref<{ action: 'update' | 'repair'; password: string; rebuild: boolean } | null>(null)
const busy = ref(false)
const progress = ref<Status | null>(null)
const startedVersion = ref('')
let timer: number | undefined

async function check(force = false) {
  checking.value = true
  try {
    rel.value = await get<Release>(`/system/update${force ? '?force=1' : ''}`)
  } catch {
    /* offline: the bar still offers repair */
  } finally {
    checking.value = false
  }
}
onMounted(() => check())
onBeforeUnmount(() => clearInterval(timer))

async function run() {
  const c = confirm.value!
  busy.value = true
  try {
    await post(`/system/${c.action}`, { password: c.password, rebuild: c.rebuild })
    startedVersion.value = rel.value?.current ?? ''
    confirm.value = null
    progress.value = { running: true, log: '', exit: -1, version: startedVersion.value }
    timer = window.setInterval(poll, 2000)
  } catch (e) {
    msg.error(e instanceof ApiError ? e.text : String(e))
  } finally {
    busy.value = false
  }
}

// The panel restarts during the run, so failed polls are expected.
async function poll() {
  try {
    const s = await get<Status>('/system/maintenance')
    progress.value = s
    if (!s.running && s.exit >= 0) clearInterval(timer)
  } catch {
    /* restarting */
  }
}
const done = computed(() => !!progress.value && !progress.value.running && progress.value.exit >= 0)
const reload = () => window.location.reload()
const changed = computed(() => done.value && progress.value!.version !== startedVersion.value)
</script>

<template>
  <n-card size="small" style="margin-bottom: 16px">
    <n-space justify="space-between" align="center">
      <n-space align="center" :size="8">
        <b>lmcwhm {{ rel?.current }}</b>
        <template v-if="rel?.latest">
          <n-tag v-if="rel.newer" type="warning" size="small">{{ t('maint.available', { v: rel.latest }) }}</n-tag>
          <n-tag v-else type="success" size="small">{{ t('maint.latest') }}</n-tag>
          <a v-if="rel.newer && rel.url" :href="rel.url" target="_blank" style="font-size: 12px">{{ t('maint.notes') }}</a>
        </template>
        <n-text v-else-if="rel?.error" depth="3" style="font-size: 12px">{{ t('maint.check_failed') }}</n-text>
      </n-space>
      <n-space :size="8">
        <n-button size="small" quaternary :loading="checking" @click="check(true)">{{ t('maint.check') }}</n-button>
        <n-button size="small" type="primary" :disabled="!rel?.newer" @click="confirm = { action: 'update', password: '', rebuild: false }">{{ t('maint.update') }}</n-button>
        <n-button size="small" @click="confirm = { action: 'repair', password: '', rebuild: false }">{{ t('maint.repair') }}</n-button>
      </n-space>
    </n-space>
  </n-card>

  <n-modal :show="!!confirm" @update:show="(v: boolean) => !v && (confirm = null)">
    <n-card v-if="confirm" :title="t(`maint.${confirm.action}`)" style="width: min(520px, calc(100vw - 32px))" closable @close="confirm = null">
      <p v-if="confirm.action === 'update'">{{ t('maint.update_desc', { from: rel?.current, to: rel?.latest }) }}</p>
      <p v-else>{{ t('maint.repair_desc') }}</p>
      <n-alert type="success" :show-icon="false" style="margin-bottom: 12px">{{ t('maint.kept') }}</n-alert>
      <n-checkbox v-if="confirm.action === 'repair'" v-model:checked="confirm.rebuild" style="margin-bottom: 12px">{{ t('maint.rebuild') }}</n-checkbox>
      <n-input v-model:value="confirm.password" type="password" show-password-on="click" :placeholder="t('maint.password')" @keyup.enter="run" />
      <template #footer>
        <n-space justify="end">
          <n-button @click="confirm = null">{{ t('common.cancel') }}</n-button>
          <n-button type="primary" :loading="busy" :disabled="!confirm.password" @click="run">{{ t('common.confirm') }}</n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>

  <n-modal :show="!!progress" :mask-closable="false">
    <n-card v-if="progress" :title="t('maint.progress')" style="width: min(760px, calc(100vw - 32px))" :closable="done" @close="progress = null">
      <n-alert v-if="!done" type="info" :show-icon="false" style="margin-bottom: 12px">{{ t('maint.wait') }}</n-alert>
      <n-alert v-else-if="progress.exit === 0" type="success" :show-icon="false" style="margin-bottom: 12px">
        {{ changed ? t('maint.done_version', { v: progress.version }) : t('maint.done') }}
      </n-alert>
      <n-alert v-else type="error" :show-icon="false" style="margin-bottom: 12px">{{ t('maint.failed', { code: progress.exit }) }}</n-alert>
      <div style="max-height: 50vh; overflow: auto"><n-code :code="progress.log || '…'" word-wrap /></div>
      <template v-if="done" #footer>
        <n-space justify="end"><n-button type="primary" @click="reload">{{ t('maint.reload') }}</n-button></n-space>
      </template>
    </n-card>
  </n-modal>
</template>
