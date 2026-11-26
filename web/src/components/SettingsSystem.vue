<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NCard, NDynamicTags, NGi, NGrid, NInput, NInputNumber, NSelect, NSpace, NSwitch, useMessage } from 'naive-ui'
import { get, post, put, ApiError } from '../api/client'
import { bytes, datetime } from '../format'
import { zoneOptions } from '../tz'
import SettingsSSH from './SettingsSSH.vue'
import TaskModal from './TaskModal.vue'

interface Sys { hostname: string; swap_total: number; swap_used: number; swap_file: number; mem_total: number; dns: string[]; timezone: string; ntp: boolean; ntp_synced: boolean; time: number; timezones?: string[]; resolved: boolean; swap_mounts: string[] }

const { t, locale } = useI18n()
const msg = useMessage()
const sys = ref<Sys | null>(null)
const swapMB = ref(0)
const host = ref('')
const dns = ref<string[]>([])
const tz = ref('')
const task = ref<{ id: string; title: string } | null>(null)
const tzOpts = computed(() => (sys.value ? zoneOptions(sys.value.timezones ?? [], [sys.value.timezone]) : []))
const suggestSwap = computed(() => {
  const gb = (sys.value?.mem_total ?? 0) / 1073741824
  return gb <= 2 ? Math.round(gb * 2 * 1024) : gb <= 8 ? Math.round(gb * 1024) : 4096
})

async function load() {
  try {
    sys.value = await get<Sys>('/toolbox/system?zones=1')
    swapMB.value = Math.round(sys.value.swap_file / 1048576)
    host.value = sys.value.hostname
    dns.value = [...sys.value.dns]
    tz.value = sys.value.timezone
  } catch (e) {
    msg.error(e instanceof ApiError ? e.text : String(e))
  }
}
async function act(p: Promise<unknown>) {
  try {
    await p
    msg.success(t('common.saved'))
    load()
  } catch (e) {
    msg.error(e instanceof ApiError ? e.text : String(e))
  }
}
async function saveSwap() {
  try {
    task.value = { id: (await put<{ task_id: string }>('/toolbox/swap', { size_mb: swapMB.value })).task_id, title: 'Swap' }
  } catch (e) {
    msg.error(e instanceof ApiError ? e.text : String(e))
  }
}
onMounted(load)
</script>

<template>
  <n-grid v-if="sys" cols="1 l:2" responsive="screen" :x-gap="16" :y-gap="16">
    <n-gi>
      <n-card size="small" title="Swap">
        <p class="hint">{{ t('tb.swap_now', { used: bytes(sys.swap_used), total: bytes(sys.swap_total) }) }}{{ sys.swap_mounts.length ? ` · ${sys.swap_mounts.join(', ')}` : '' }}</p>
        <n-space align="center">
          <n-input-number v-model:value="swapMB" :min="0" :max="65536" :step="512" style="width: 160px"><template #suffix>MB</template></n-input-number>
          <n-button type="primary" @click="saveSwap">{{ t('common.save') }}</n-button>
          <n-button quaternary size="small" @click="swapMB = suggestSwap">{{ t('tb.swap_suggest', { n: suggestSwap }) }}</n-button>
        </n-space>
        <p class="hint">{{ t('tb.swap_hint') }}</p>
      </n-card>
      <n-card size="small" :title="t('tb.hostname')" style="margin-top: 16px">
        <n-space><n-input v-model:value="host" style="width: 260px" /><n-button type="primary" :disabled="host === sys.hostname" @click="act(put('/toolbox/hostname', { hostname: host }))">{{ t('common.save') }}</n-button></n-space>
      </n-card>
    </n-gi>
    <n-gi>
      <n-card size="small" :title="t('tb.dns')">
        <n-dynamic-tags v-model:value="dns" />
        <n-space style="margin-top: 12px">
          <n-button type="primary" @click="act(put('/toolbox/dns', { servers: dns }))">{{ t('common.save') }}</n-button>
          <n-button quaternary size="small" @click="dns = ['1.1.1.1', '8.8.8.8', '2606:4700:4700::1111']">Cloudflare + Google</n-button>
        </n-space>
        <p class="hint">{{ sys.resolved ? t('tb.dns_resolved') : t('tb.dns_plain') }}</p>
      </n-card>
      <n-card size="small" :title="t('tb.time')" style="margin-top: 16px">
        <p class="hint">{{ datetime(sys.time, locale) }} · NTP {{ sys.ntp_synced ? t('tb.synced') : t('tb.not_synced') }}</p>
        <n-space align="center">
          <n-select v-model:value="tz" filterable :options="tzOpts" style="width: min(340px, 100%)" />
          <n-button type="primary" :disabled="tz === sys.timezone" @click="act(put('/toolbox/time', { timezone: tz }))">{{ t('common.save') }}</n-button>
        </n-space>
        <n-space align="center" style="margin-top: 12px">
          <n-switch :value="sys.ntp" @update:value="(v: boolean) => act(put('/toolbox/time', { ntp: v }))" /> {{ t('tb.ntp') }}
          <n-button size="small" @click="act(post('/toolbox/time/sync'))">{{ t('tb.sync_now') }}</n-button>
        </n-space>
        <p class="hint">{{ t('tb.tz_hint') }}</p>
      </n-card>
    </n-gi>
  </n-grid>
  <div v-if="sys" style="margin-top: 16px"><SettingsSSH /></div>
  <TaskModal :task-id="task?.id ?? null" :title="task?.title ?? ''" @close="task = null; load()" @done="load" />
</template>

<style scoped>
.hint {
  font-size: 12px;
  opacity: 0.65;
}
</style>
