<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NAlert, NButton, NCard, NDataTable, NInput, NPopconfirm, NRadioButton, NRadioGroup, NSelect, NSpace, NTag, useMessage, type DataTableColumns } from 'naive-ui'
import { get, post, ApiError } from '../api/client'
import type { FirewallIP, FirewallView } from '../api/types'
import { datetime } from '../format'
import IpGeo from './IpGeo.vue'
import DetailDrawer, { type DetailRow } from './DetailDrawer.vue'

// The block / allow lists: manual entries and bans (CrowdSec, MCP). The
// public lists are counted under "Auto-updated lists".
const emit = defineEmits<{ changed: [] }>()
const { t, locale } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))
const list = ref<'' | 'block' | 'allow'>('')
const items = ref<FirewallIP[]>([])
const fw = ref<FirewallView | null>(null)
const loading = ref(false)
const q = ref('')
const f = ref({ ip: '', list: 'block' as 'block' | 'allow', minutes: 0, remark: '' })
const detail = ref<FirewallIP | null>(null)

async function load() {
  loading.value = true
  try {
    const [ips, v] = await Promise.all([get<{ items: FirewallIP[] }>(`/firewall/ips?list=${list.value}`), get<FirewallView>('/firewall')])
    items.value = ips.items.filter((x) => x.list === 'block' || x.list === 'allow')
    fw.value = v
  } catch (e) {
    fail(e)
  } finally {
    loading.value = false
  }
}
onMounted(load)

async function add() {
  try {
    await post('/firewall/ips', f.value)
    f.value.ip = ''
    f.value.remark = ''
    load()
    emit('changed')
  } catch (e) {
    fail(e)
  }
}
async function remove(x: FirewallIP) {
  try {
    await post(`/firewall/ips/${x.id}/delete`)
    detail.value = null
    load()
    emit('changed')
  } catch (e) {
    fail(e)
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
const synced = (x: FirewallIP) => x.source === 'manual' && !x.expires_at
const columns: DataTableColumns<FirewallIP> = [
  { title: () => t('common.ip'), key: 'ip', minWidth: 260, render: (x) => h(IpGeo, { ip: x.ip }) },
  { title: () => t('fw.list'), key: 'list', width: 90, render: (x) => h(NTag, { size: 'small', type: x.list === 'block' ? 'error' : 'success' }, () => t(`fw.lists.${x.list}`)) },
  { title: () => t('fw.origin'), key: 'source', width: 110, render: (x) => h(NTag, { size: 'small', bordered: false }, () => t(`fw.sources.${x.source}`)) },
  { title: () => t('fw.expires'), key: 'expires_at', width: 170, render: (x) => (x.expires_at ? datetime(x.expires_at, locale.value) : t('fw.permanent')) },
  { title: () => t('site.remark'), key: 'remark', ellipsis: { tooltip: true } },
]
const rowProps = (x: FirewallIP) => ({ style: 'cursor: pointer', onClick: () => (detail.value = x) })
const detailRows = computed<DetailRow[]>(() => {
  const x = detail.value
  if (!x) return []
  return [
    { label: t('common.ip'), value: () => h(IpGeo, { ip: x.ip }) },
    { label: t('fw.list'), value: t(`fw.lists.${x.list}`) },
    { label: t('fw.origin'), value: t(`fw.sources.${x.source}`) },
    { label: t('site.remark'), value: x.remark },
    { label: t('fw.added'), value: datetime(x.created_at, locale.value) },
    { label: t('fw.expires'), value: x.expires_at ? datetime(x.expires_at, locale.value) : t('fw.permanent') },
    { label: t('fw.cloud_sync'), value: synced(x) ? t('fw.synced_yes') : t('fw.synced_no') },
  ]
})
</script>

<template>
  <n-alert v-if="fw && !fw.enabled" type="warning" :show-icon="false" style="margin-bottom: 12px">{{ t('fw.lists_inactive') }}</n-alert>
  <n-card size="small" :title="t('fw.add_ip')">
    <n-space align="center">
      <n-input v-model:value="f.ip" :placeholder="t('fw.ip_placeholder')" style="width: 220px" @keyup.enter="add" />
      <n-radio-group v-model:value="f.list">
        <n-radio-button value="block">{{ t('fw.lists.block') }}</n-radio-button>
        <n-radio-button value="allow">{{ t('fw.lists.allow') }}</n-radio-button>
      </n-radio-group>
      <n-select v-model:value="f.minutes" :options="durations" style="width: 130px" />
      <n-input v-model:value="f.remark" :placeholder="t('site.remark')" style="width: 200px" />
      <n-button type="primary" :disabled="!f.ip" @click="add">{{ t('fw.add') }}</n-button>
    </n-space>
    <p class="hint">{{ t('fw.lists_hint2') }}</p>
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
  <n-data-table size="small" :columns="columns" :data="shown" :loading="loading" :row-key="(x: FirewallIP) => x.id" :row-props="rowProps" :pagination="{ pageSize: 20 }" :scroll-x="800" />

  <DetailDrawer :show="!!detail" :title="detail?.ip ?? ''" :rows="detailRows" @close="detail = null">
    <template v-if="detail" #actions>
      <n-popconfirm @positive-click="remove(detail!)">
        <template #trigger><n-button type="error" ghost>{{ t('fw.remove') }}</n-button></template>
        {{ t('fw.remove_confirm', { ip: detail.ip }) }}
      </n-popconfirm>
    </template>
  </DetailDrawer>
</template>

<style scoped>
.hint {
  font-size: 12px;
  opacity: 0.65;
  margin: 8px 0 0;
}
</style>
