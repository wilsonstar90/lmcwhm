<script setup lang="ts">
// One tab of 黑白名單: IP, User-Agent or URL entries of the allow or block
// list, added by hand (import / export / clear like aaPanel's WAF lists).
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NButton, NDataTable, NInput, NModal, NCard, NPopconfirm, NSelect, NSpace, NSwitch, NTag, useMessage, type DataTableColumns,
} from 'naive-ui'
import { get, post, put, ApiError } from '../api/client'
import type { FirewallIP } from '../api/types'
import { datetime } from '../format'
import IpGeo from './IpGeo.vue'

interface Pattern { id: number; kind: 'ua' | 'url'; list: 'allow' | 'block'; match: 'contains' | 'prefix' | 'regex'; value: string; remark: string; enabled: boolean; created_at: number }
type Row = { id: number; value: string; match?: Pattern['match']; remark: string; enabled: boolean; created_at: number; expires_at?: number; raw: Pattern | FirewallIP }

const props = defineProps<{ kind: 'ip' | 'ua' | 'url'; list: 'allow' | 'block' }>()
const emit = defineEmits<{ changed: [] }>()
const { t, locale } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))

const rows = ref<Row[]>([])
const loading = ref(false)
const q = ref('')
const value = ref('')
const match = ref<Pattern['match']>(props.kind === 'url' ? 'prefix' : 'contains')
const minutes = ref(0)
const remark = ref('')
const importing = ref(false)
const importText = ref('')

const isIP = props.kind === 'ip'
async function load() {
  loading.value = true
  try {
    if (isIP) {
      const r = await get<{ items: FirewallIP[] }>(`/firewall/ips?list=${props.list}&source=manual`)
      rows.value = r.items.map((x) => ({ id: x.id, value: x.ip, remark: x.remark, enabled: true, created_at: x.created_at, expires_at: x.expires_at, raw: x }))
    } else {
      const r = await get<{ items: Pattern[] }>(`/firewall/patterns?kind=${props.kind}&list=${props.list}`)
      rows.value = r.items.map((x) => ({ id: x.id, value: x.value, match: x.match, remark: x.remark, enabled: x.enabled, created_at: x.created_at, raw: x }))
    }
  } catch (e) {
    fail(e)
  } finally {
    loading.value = false
  }
}
onMounted(load)
defineExpose({ load })

const done = () => {
  load()
  emit('changed')
}
async function add() {
  const v = value.value.trim()
  if (!v) return
  try {
    if (isIP) await post('/firewall/ips', { ip: v, list: props.list, minutes: minutes.value, remark: remark.value })
    else await post('/firewall/patterns', { kind: props.kind, list: props.list, match: match.value, value: v, remark: remark.value, enabled: true })
    value.value = ''
    remark.value = ''
    msg.success(t('common.saved'))
    done()
  } catch (e) {
    fail(e)
  }
}
async function remove(r: Row) {
  try {
    await post(isIP ? `/firewall/ips/${r.id}/delete` : `/firewall/patterns/${r.id}/delete`)
    done()
  } catch (e) {
    fail(e)
  }
}
async function toggle(r: Row, on: boolean) {
  try {
    await put(`/firewall/patterns/${r.id}`, { ...(r.raw as Pattern), enabled: on })
    done()
  } catch (e) {
    fail(e)
  }
}
async function doImport() {
  const values = importText.value.split(/\r?\n/).map((l) => l.trim()).filter((l) => l && !l.startsWith('#'))
  if (!values.length) return
  try {
    const r = isIP
      ? await post<{ added: number; skipped: number }>('/firewall/ips/import', { list: props.list, ips: values, minutes: minutes.value, remark: remark.value })
      : await post<{ added: number; skipped: number }>('/firewall/patterns/import', { kind: props.kind, list: props.list, match: match.value, values, remark: remark.value })
    msg.success(t('fwl.imported', { added: r.added, skipped: r.skipped }))
    importing.value = false
    importText.value = ''
    done()
  } catch (e) {
    fail(e)
  }
}
function doExport() {
  const blob = new Blob([rows.value.map((r) => r.value).join('\n') + '\n'], { type: 'text/plain' })
  const a = document.createElement('a')
  a.href = URL.createObjectURL(blob)
  a.download = `${props.kind}-${props.list}.txt`
  a.click()
  URL.revokeObjectURL(a.href)
}
async function clear() {
  try {
    await post(isIP ? '/firewall/ips/clear' : '/firewall/patterns/clear', isIP ? { list: props.list } : { kind: props.kind, list: props.list })
    done()
  } catch (e) {
    fail(e)
  }
}

const shown = computed(() => {
  const s = q.value.trim().toLowerCase()
  return s ? rows.value.filter((r) => r.value.toLowerCase().includes(s) || r.remark.toLowerCase().includes(s)) : rows.value
})
const matches = computed(() => (['contains', 'prefix', 'regex'] as const).map((m) => ({ label: t(`fwl.match.${m}`), value: m })))
const durations = computed(() => [
  { label: t('fw.permanent'), value: 0 }, { label: t('fw.dur.h1'), value: 60 }, { label: t('fw.dur.h24'), value: 1440 },
  { label: t('fw.dur.d7'), value: 10080 }, { label: t('fw.dur.d30'), value: 43200 },
])
const placeholder = computed(() => t(`fwl.placeholder.${props.kind}.${props.kind === 'ip' ? 'any' : match.value}`))
const columns = computed<DataTableColumns<Row>>(() => [
  { title: () => t(`fwl.col.${props.kind}`), key: 'value', minWidth: 240, render: (r) => (isIP ? h(IpGeo, { ip: r.value }) : h('code', { style: 'word-break: break-all' }, r.value)) },
  ...(isIP
    ? [{ title: () => t('fw.expires'), key: 'expires_at', width: 170, render: (r: Row) => (r.expires_at ? datetime(r.expires_at, locale.value) : t('fw.permanent')) }]
    : [{ title: () => t('fwl.match_col'), key: 'match', width: 110, render: (r: Row) => h(NTag, { size: 'small', bordered: false }, () => t(`fwl.match.${r.match}`)) }]),
  { title: () => t('site.remark'), key: 'remark', ellipsis: { tooltip: true } },
  { title: () => t('fw.added'), key: 'created_at', width: 170, render: (r) => datetime(r.created_at, locale.value) },
  ...(isIP ? [] : [{ title: () => t('cron.enabled'), key: 'enabled', width: 80, render: (r: Row) => h(NSwitch, { size: 'small', value: r.enabled, 'onUpdate:value': (on: boolean) => toggle(r, on) }) }]),
  {
    title: '',
    key: 'actions',
    width: 80,
    render: (r) =>
      h(NPopconfirm, { onPositiveClick: () => remove(r) }, { trigger: () => h(NButton, { size: 'tiny', type: 'error', ghost: true }, () => t('common.delete')), default: () => t('fw.remove_confirm', { ip: r.value }) }),
  },
])
</script>

<template>
  <n-space align="center" :size="8" style="margin-bottom: 8px">
    <n-select v-if="!isIP" v-model:value="match" :options="matches" size="small" style="width: 120px" />
    <n-input v-model:value="value" size="small" :placeholder="placeholder" style="width: 320px" @keyup.enter="add" />
    <n-select v-if="isIP" v-model:value="minutes" :options="durations" size="small" style="width: 120px" />
    <n-input v-model:value="remark" size="small" :placeholder="t('site.remark')" style="width: 180px" />
    <n-button type="primary" size="small" :disabled="!value.trim()" @click="add">{{ t('fw.add') }}</n-button>
    <span class="sep" />
    <n-button size="small" @click="importing = true">{{ t('fwl.import') }}</n-button>
    <n-button size="small" :disabled="!rows.length" @click="doExport">{{ t('fwl.export') }}</n-button>
    <n-popconfirm @positive-click="clear">
      <template #trigger><n-button size="small" :disabled="!rows.length">{{ t('fwl.clear') }}</n-button></template>
      {{ t('fwl.clear_confirm', { n: rows.length }) }}
    </n-popconfirm>
    <n-input v-model:value="q" size="small" clearable :placeholder="t('logs.filter_kw')" style="width: 180px" />
  </n-space>
  <p class="hint">{{ t(`fwl.hint.${kind}_${list}`) }}</p>
  <n-data-table size="small" :columns="columns" :data="shown" :loading="loading" :row-key="(r: Row) => r.id" :pagination="{ pageSize: 20 }" :scroll-x="820" />

  <n-modal :show="importing" @update:show="(v: boolean) => (importing = v)">
    <n-card :title="t('fwl.import')" style="width: min(560px, calc(100vw - 32px))" closable @close="importing = false">
      <n-space vertical>
        <n-space v-if="!isIP" align="center"><span>{{ t('fwl.match_col') }}</span><n-select v-model:value="match" :options="matches" size="small" style="width: 140px" /></n-space>
        <n-input v-model:value="importText" type="textarea" :autosize="{ minRows: 8, maxRows: 18 }" :placeholder="t('fwl.import_hint')" />
      </n-space>
      <template #footer>
        <n-space justify="end">
          <n-button @click="importing = false">{{ t('common.cancel') }}</n-button>
          <n-button type="primary" :disabled="!importText.trim()" @click="doImport">{{ t('fwl.import') }}</n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>
</template>

<style scoped>
.hint {
  font-size: 12px;
  opacity: 0.65;
  margin: 0 0 8px;
}
.sep {
  width: 1px;
  height: 20px;
  background: rgba(128, 128, 128, 0.25);
}
</style>
