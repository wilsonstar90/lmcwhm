<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NAlert, NBadge, NButton, NCard, NCheckbox, NCode, NDropdown, NInput, NModal, NSelect, NSpace, NSpin, NTag, NText, useDialog, useMessage } from 'naive-ui'
import { get, post, ApiError } from '../api/client'

// compact: the header's version, 更新, 修復 and 重啟 (a menu on phones);
// otherwise the full bar in 面板設定 with the channel and rollback.
const props = defineProps<{ compact?: boolean }>()

interface Release { current: string; latest: string; newer: boolean; notes: string; url: string; published_at: string; error?: string; rollback?: string }
interface Status { running: boolean; log: string; exit: number; version: string }

const { t } = useI18n()
const msg = useMessage()
const dialog = useDialog()
const rel = ref<Release | null>(null)
const checking = ref(false)
const confirm = ref<{ action: 'update' | 'repair' | 'rollback'; password: string; rebuild: boolean; withData: boolean } | null>(null)
const readChannel = () => {
  try {
    return localStorage.getItem('lmcwhm.channel') === 'beta' ? 'beta' : 'stable'
  } catch {
    return 'stable'
  }
}
const channel = ref<'stable' | 'beta'>(readChannel())
function setChannel(v: 'stable' | 'beta') {
  channel.value = v
  try {
    localStorage.setItem('lmcwhm.channel', v)
  } catch {
    /* private mode */
  }
  check(true)
}
const busy = ref(false)
const progress = ref<Status | null>(null)
const startedVersion = ref('')
let timer: number | undefined

async function check(force = false) {
  checking.value = true
  try {
    rel.value = await get<Release>(`/system/update?channel=${channel.value}${force ? '&force=1' : ''}`)
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
    await post(`/system/${c.action}`, { password: c.password, rebuild: c.rebuild, channel: channel.value, with_data: c.withData })
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
// ---- restart (重啟面板) ----
const restarting = ref(false)
function restart() {
  dialog.warning({
    title: t('maint.restart'),
    content: t('maint.restart_desc'),
    positiveText: t('common.confirm'),
    negativeText: t('common.cancel'),
    onPositiveClick: async () => {
      try {
        await post('/system/restart')
      } catch (e) {
        msg.error(e instanceof ApiError ? e.text : String(e))
        return
      }
      restarting.value = true
      // Wait for the panel to go away and come back.
      const t0 = Date.now()
      const wait = async () => {
        if (Date.now() - t0 > 3000) {
          try {
            await get('/public/meta')
            window.location.reload()
            return
          } catch {
            /* still restarting */
          }
        }
        if (Date.now() - t0 < 120000) setTimeout(wait, 1500)
        else restarting.value = false
      }
      setTimeout(wait, 1500)
    },
  })
}
const mq = window.matchMedia('(max-width: 767px)')
const narrow = ref(mq.matches)
mq.addEventListener?.('change', (e) => (narrow.value = e.matches))
const menu = computed(() => [
  { key: 'update', label: rel.value?.newer ? t('maint.available', { v: rel.value.latest }) : t('maint.update'), disabled: !rel.value?.newer },
  { key: 'repair', label: t('maint.repair') },
  { key: 'restart', label: t('maint.restart') },
])
function pick(k: string) {
  if (k === 'restart') restart()
  else confirm.value = { action: k as 'update' | 'repair', password: '', rebuild: false, withData: false }
}

const done = computed(() => !!progress.value && !progress.value.running && progress.value.exit >= 0)
const reload = () => window.location.reload()
const changed = computed(() => done.value && progress.value!.version !== startedVersion.value)
</script>

<template>
  <div v-if="props.compact" class="compact">
    <span v-if="!narrow" class="ver" :title="rel?.newer ? t('maint.available', { v: rel.latest }) : ''">{{ rel?.current }}</span>
    <n-dropdown v-if="narrow" :options="menu" trigger="click" @select="pick">
      <n-badge dot :show="!!rel?.newer" :offset="[-4, 4]"><n-button size="small" quaternary>⋯</n-button></n-badge>
    </n-dropdown>
    <template v-else>
      <n-badge dot :show="!!rel?.newer" :offset="[-4, 4]">
        <n-button size="small" quaternary :disabled="!rel?.newer" @click="pick('update')">⟳ {{ t('maint.update') }}</n-button>
      </n-badge>
      <n-button size="small" quaternary @click="pick('repair')">🔧 {{ t('maint.repair') }}</n-button>
      <n-button size="small" quaternary @click="restart">⏻ {{ t('maint.restart') }}</n-button>
    </template>
  </div>
  <n-card v-else size="small" style="margin-bottom: 16px">
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
      <n-space :size="8" align="center">
        <n-select
          size="small"
          :value="channel"
          :options="[{ label: t('maint.stable'), value: 'stable' }, { label: t('maint.beta'), value: 'beta' }]"
          style="width: 110px"
          @update:value="setChannel"
        />
        <n-button size="small" quaternary :loading="checking" @click="check(true)">{{ t('maint.check') }}</n-button>
        <n-button size="small" type="primary" :disabled="!rel?.newer" @click="confirm = { action: 'update', password: '', rebuild: false, withData: false }">{{ t('maint.update') }}</n-button>
        <n-button size="small" @click="confirm = { action: 'repair', password: '', rebuild: false, withData: false }">{{ t('maint.repair') }}</n-button>
        <n-button v-if="rel?.rollback" size="small" quaternary type="warning" @click="confirm = { action: 'rollback', password: '', rebuild: false, withData: false }">
          {{ t('maint.rollback_to', { v: rel.rollback }) }}
        </n-button>
      </n-space>
    </n-space>
  </n-card>

  <n-modal :show="!!confirm" @update:show="(v: boolean) => !v && (confirm = null)">
    <n-card v-if="confirm" :title="t(`maint.${confirm.action}`)" style="width: min(520px, calc(100vw - 32px))" closable @close="confirm = null">
      <p v-if="confirm.action === 'update'">{{ t('maint.update_desc', { from: rel?.current, to: rel?.latest }) }}</p>
      <p v-else-if="confirm.action === 'rollback'">{{ t('maint.rollback_desc', { from: rel?.current, to: rel?.rollback }) }}</p>
      <p v-else>{{ t('maint.repair_desc') }}</p>
      <n-alert type="success" :show-icon="false" style="margin-bottom: 12px">{{ t('maint.kept') }}</n-alert>
      <n-checkbox v-if="confirm.action === 'rollback'" v-model:checked="confirm.withData" style="margin-bottom: 12px">{{ t('maint.with_data') }}</n-checkbox>
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

  <n-modal :show="restarting" :mask-closable="false">
    <n-card style="width: min(360px, calc(100vw - 32px))">
      <n-space align="center"><n-spin size="small" /> {{ t('maint.restarting') }}</n-space>
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

<style scoped>
.compact {
  display: flex;
  align-items: center;
  gap: 2px;
}
.ver {
  font-size: 12px;
  opacity: 0.7;
  margin-right: 4px;
  font-variant-numeric: tabular-nums;
}
</style>
