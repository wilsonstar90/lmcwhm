<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NAlert, NButton, NCard, NDataTable, NInput, NPopconfirm, NRadioButton, NRadioGroup, NSelect, NSpace, NTag, useMessage, type DataTableColumns } from 'naive-ui'
import { get, post, ApiError } from '../api/client'
import type { FirewallIP, FirewallView, SyncReport } from '../api/types'
import { datetime } from '../format'
import IpGeo from './IpGeo.vue'
import SecFeeds from './SecFeeds.vue'

const { t, locale } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))
const list = ref<'' | 'block' | 'allow'>('')
const items = ref<FirewallIP[]>([])
const fw = ref<FirewallView | null>(null)
const loading = ref(false)
const q = ref('')
const f = ref({ ip: '', list: 'block' as 'block' | 'allow', minutes: 0, remark: '' })
const syncing = ref(false)
const report = ref<SyncReport | null>(null)

async function load() {
  loading.value = true
  try {
    const [ips, v] = await Promise.all([get<{ items: FirewallIP[] }>(`/firewall/ips?list=${list.value}`), get<FirewallView>('/firewall')])
    items.value = ips.items
    fw.value = v
    report.value = v.last_sync.at ? v.last_sync : null
  } catch (e) {
    fail(e)
  } finally {
    loading.value = false
  }
}
onMounted(load)
defineExpose({ load })

async function add() {
  try {
    await post('/firewall/ips', f.value)
    f.value.ip = ''
    f.value.remark = ''
    load()
  } catch (e) {
    fail(e)
  }
}
async function remove(x: FirewallIP) {
  try {
    await post(`/firewall/ips/${x.id}/delete`)
    load()
  } catch (e) {
    fail(e)
  }
}
async function sync() {
  syncing.value = true
  try {
    report.value = await post<SyncReport>('/firewall/sync')
  } catch (e) {
    fail(e)
  } finally {
    syncing.value = false
  }
}

const shown = computed(() => {
  const s = q.value.trim().toLowerCase()
  return s ? items.value.filter((x) => x.ip.includes(s) || x.remark.toLowerCase().includes(s)) : items.value
})
const durations = computed(() => [
  { label: t('fw.permanent'), value: 0 }, { label: t('fw.dur.h1'), value: 60 }, { label: t('fw.dur.h24'), value: 1440 },
  { label: t('fw.dur.d7'), value: 10080 }, { label: t('fw.dur.d30'), value: 43200 },
])
const columns: DataTableColumns<FirewallIP> = [
  { title: () => t('common.ip'), key: 'ip', width: 320, render: (x) => h(IpGeo, { ip: x.ip }) },
  { title: () => t('fw.list'), key: 'list', width: 90, render: (x) => h(NTag, { size: 'small', type: x.list === 'block' ? 'error' : 'success' }, () => t(`fw.lists.${x.list}`)) },
  { title: () => t('fw.origin'), key: 'source', width: 110, render: (x) => h(NTag, { size: 'small', bordered: false }, () => t(`fw.sources.${x.source}`)) },
  { title: () => t('fw.expires'), key: 'expires_at', width: 180, render: (x) => (x.expires_at ? datetime(x.expires_at, locale.value) : t('fw.permanent')) },
  { title: () => t('site.remark'), key: 'remark', ellipsis: { tooltip: true } },
  {
    title: '',
    key: 'actions',
    width: 80,
    render: (x) => h(NPopconfirm, { onPositiveClick: () => remove(x) }, { trigger: () => h(NButton, { size: 'tiny', type: 'error', ghost: true }, () => t('fw.remove')), default: () => t('fw.remove_confirm', { ip: x.ip }) }),
  },
]
</script>

<template>
  <n-alert v-if="fw && !fw.enabled" type="warning" :show-icon="false" style="margin-bottom: 12px">{{ t('fw.lists_inactive') }}</n-alert>
  <n-card size="small" :title="t('fw.add_ip')">
    <n-space align="center">
      <n-input v-model:value="f.ip" :placeholder="t('fw.ip_placeholder')" style="width: 220px" @keyup.enter="add" />
      <n-radio-group v-model:value="f.list" size="small">
        <n-radio-button value="block">{{ t('fw.lists.block') }}</n-radio-button>
        <n-radio-button value="allow">{{ t('fw.lists.allow') }}</n-radio-button>
      </n-radio-group>
      <n-select v-if="f.list === 'block'" v-model:value="f.minutes" :options="durations" style="width: 120px" />
      <n-input v-model:value="f.remark" :placeholder="t('site.remark')" style="width: 200px" />
      <n-button type="primary" :disabled="!f.ip" @click="add">{{ t('fw.add') }}</n-button>
    </n-space>
    <p class="hint">{{ t('fw.lists_hint') }}</p>
  </n-card>
  <SecFeeds kind="trust" />

  <n-card size="small" :title="t('fw.sync')" style="margin-top: 16px">
    <template #header-extra><n-button size="small" :loading="syncing" :disabled="!fw?.cf_sync && !fw?.linode_sync && !fw?.linode_ports" @click="sync">{{ t('fw.sync_now') }}</n-button></template>
    <p class="hint" style="margin-top: 0">{{ t('fw.sync_hint') }}</p>
    <template v-if="report">
      <div v-for="l in report.cloudflare ?? []" :key="l" style="font-size: 13px">☁ {{ l }}</div>
      <div v-if="report.linode" style="font-size: 13px">Linode: {{ report.linode }}</div>
      <div v-for="e in report.errors ?? []" :key="e" style="font-size: 13px; color: #d03050">{{ e }}</div>
      <div class="hint">{{ datetime(report.at, locale) }}</div>
    </template>
  </n-card>

  <n-space justify="space-between" style="margin: 16px 0 8px">
    <n-radio-group v-model:value="list" size="small" @update:value="load">
      <n-radio-button value="">{{ t('db.all') }}</n-radio-button>
      <n-radio-button value="block">{{ t('fw.lists.block') }}</n-radio-button>
      <n-radio-button value="allow">{{ t('fw.lists.allow') }}</n-radio-button>
    </n-radio-group>
    <n-space>
      <n-input v-model:value="q" size="small" clearable :placeholder="t('logs.filter_kw')" style="width: 200px" />
      <n-button size="small" @click="load">{{ t('common.refresh') }}</n-button>
    </n-space>
  </n-space>
  <n-data-table size="small" :columns="columns" :data="shown" :loading="loading" :pagination="{ pageSize: 30 }" :row-key="(x: FirewallIP) => x.id" :scroll-x="1000" />
</template>

<style scoped>
.hint {
  font-size: 12px;
  opacity: 0.65;
  margin: 8px 0 0;
}
</style>
