<script setup lang="ts">
// Everything the panel added on its own: public lists (block / trust),
// CrowdSec and AI (MCP) bans. Paged on the server: lists can be large.
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NDataTable, NInput, NPopconfirm, NRadioButton, NRadioGroup, NSelect, NSpace, NTag, useMessage, type DataTableColumns } from 'naive-ui'
import { get, post, ApiError } from '../api/client'
import type { FirewallIP } from '../api/types'
import { datetime } from '../format'
import IpGeo from './IpGeo.vue'

interface Page { items: FirewallIP[]; total: number; sources: Record<string, number> }
const emit = defineEmits<{ changed: [] }>()
const { t, te, locale } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))
const list = ref<'' | 'block' | 'trust'>('')
const source = ref<string | null>(null)
const q = ref('')
const page = ref(1)
const size = ref(50)
const data = ref<Page>({ items: [], total: 0, sources: {} })
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    const p = new URLSearchParams({ list: list.value, source: source.value ?? '', q: q.value.trim(), page: String(page.value), size: String(size.value) })
    data.value = await get<Page>(`/firewall/ips/auto?${p}`)
  } catch (e) {
    fail(e)
  } finally {
    loading.value = false
  }
}
onMounted(load)
const reload = () => {
  page.value = 1
  load()
}

const sourceName = (s: string) => {
  if (s.startsWith('feed:')) return t('fwl.from_list', { id: s.slice(5) })
  return te(`fw.sources.${s}`) ? t(`fw.sources.${s}`) : s
}
const sourceOptions = computed(() => [
  { label: t('fwl.all_sources'), value: '' },
  ...(Object.keys(data.value.sources).some((s) => s.startsWith('feed:')) ? [{ label: t('fwl.all_lists'), value: 'feed' }] : []),
  ...Object.entries(data.value.sources)
    .sort((a, b) => b[1] - a[1])
    .map(([s, n]) => ({ label: `${sourceName(s)} (${n.toLocaleString()})`, value: s })),
])
async function remove(x: FirewallIP) {
  try {
    await post(`/firewall/ips/${x.id}/delete`)
    load()
    emit('changed')
  } catch (e) {
    fail(e)
  }
}
async function keep(x: FirewallIP) {
  try {
    await post(`/firewall/ips/${x.id}/keep`)
    msg.success(t('fwl.kept'))
    load()
    emit('changed')
  } catch (e) {
    fail(e)
  }
}
const columns: DataTableColumns<FirewallIP> = [
  { title: () => t('common.ip'), key: 'ip', minWidth: 260, render: (x) => h(IpGeo, { ip: x.ip }) },
  { title: () => t('fw.list'), key: 'list', width: 90, render: (x) => h(NTag, { size: 'small', type: x.list === 'block' ? 'error' : 'success' }, () => t(`fw.lists.${x.list}`)) },
  { title: () => t('fw.origin'), key: 'source', width: 170, render: (x) => h(NTag, { size: 'small', bordered: false }, () => sourceName(x.source)) },
  { title: () => t('site.remark'), key: 'remark', ellipsis: { tooltip: true } },
  { title: () => t('fw.expires'), key: 'expires_at', width: 170, render: (x) => (x.expires_at ? datetime(x.expires_at, locale.value) : t('fw.permanent')) },
  {
    title: '',
    key: 'actions',
    width: 170,
    render: (x) =>
      h(NSpace, { size: 6 }, () => [
        x.list === 'block' ? h(NButton, { size: 'tiny', onClick: () => keep(x) }, () => t('fwl.keep')) : null,
        h(NPopconfirm, { onPositiveClick: () => remove(x) }, { trigger: () => h(NButton, { size: 'tiny', type: 'error', ghost: true }, () => t('fw.remove')), default: () => t('fwl.remove_auto', { ip: x.ip }) }),
      ]),
  },
]
const pagination = computed(() => ({
  page: page.value, pageSize: size.value, itemCount: data.value.total, showSizePicker: true, pageSizes: [20, 50, 100, 200],
  onUpdatePage: (p: number) => { page.value = p; load() },
  onUpdatePageSize: (s: number) => { size.value = s; reload() },
}))
</script>

<template>
  <n-space align="center" :size="8" style="margin-bottom: 8px">
    <n-radio-group v-model:value="list" size="small" @update:value="reload">
      <n-radio-button value="">{{ t('db.all') }}</n-radio-button>
      <n-radio-button value="block">{{ t('fw.lists.block') }}</n-radio-button>
      <n-radio-button value="trust">{{ t('fw.lists.trust') }}</n-radio-button>
    </n-radio-group>
    <n-select v-model:value="source" :options="sourceOptions" size="small" style="width: 240px" :placeholder="t('fwl.all_sources')" @update:value="reload" />
    <n-input v-model:value="q" size="small" clearable :placeholder="t('logs.filter_kw')" style="width: 200px" @keyup.enter="reload" @clear="reload" />
    <n-button size="small" @click="load">{{ t('common.refresh') }}</n-button>
  </n-space>
  <p class="hint">{{ t('fwl.auto_hint') }}</p>
  <n-data-table remote size="small" :columns="columns" :data="data.items" :loading="loading" :row-key="(x: FirewallIP) => x.id" :pagination="pagination" :scroll-x="900" />
</template>

<style scoped>
.hint {
  font-size: 12px;
  opacity: 0.65;
  margin: 0 0 8px;
}
</style>
