<script setup lang="ts">
// Every blocked or detected request: the edge (lists, website protection,
// dedicated rules, CC) and the WAF (OWASP CRS) together, with the actions
// that follow from one: exclude a CRS rule, switch a dedicated rule, block
// the address, allow the URL.
import { computed, h, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NDataTable, NDatePicker, NDropdown, NInput, NRadioButton, NRadioGroup, NSelect, NSpace, NTag, useMessage, type DataTableColumns } from 'naive-ui'
import { get, patch, post, ApiError } from '../api/client'
import { datetime } from '../format'
import IpGeo from './IpGeo.vue'
import DetailDrawer, { type DetailRow } from './DetailDrawer.vue'

interface EvRule { id: string; message: string; severity: string; data?: string }
interface SecEvent {
  time: number; ip: string; host: string; site: string; method: string; uri: string; ua?: string; status: number
  action: 'blocked' | 'detected' | 'limited'; source: 'waf' | 'rule' | 'list' | 'ip' | 'protection' | 'cc'; rule: string
  message?: string; severity?: string; messages?: EvRule[]
}
interface SiteRef { id: number; domain: string; waf_exclude: string[] | null }
interface RuleRef { id: number; code: string; name: string; action: string; enabled: boolean }

const props = defineProps<{ site?: string }>()
const emit = defineEmits<{ crs: [id: string]; changed: []; task: [id: string, title: string] }>()
const { t, te, locale } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))

const items = ref<SecEvent[]>([])
const total = ref(0)
const counts = ref<{ sources: Record<string, number>; actions: Record<string, number>; top_ips: { ip: string; hits: number }[]; oldest: number }>({ sources: {}, actions: {}, top_ips: [], oldest: 0 })
const loading = ref(false)
const source = ref('')
const action = ref('')
const site = ref(props.site ?? '')
const q = ref('')
const ip = ref('')
const sites = ref<SiteRef[]>([])
const rules = ref<RuleRef[]>([])
// When: today, yesterday, the last 7 / 30 days or one day picked (30 days are kept).
const range = ref<'today' | 'yesterday' | '7' | '30' | 'day'>('today')
const day = ref<number | null>(null)
const page = ref(1)
const pageSize = ref(50)

function when(): Record<string, string> {
  switch (range.value) {
    case 'yesterday': return { back: '1' }
    case '7': return { days: '7' }
    case '30': return { days: '30' }
    case 'day': {
      if (!day.value) return {}
      const d = new Date(day.value)
      const pad = (n: number) => String(n).padStart(2, '0')
      return { date: `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}` }
    }
  }
  return {}
}
const keepFrom = () => { const d = new Date(); d.setHours(0, 0, 0, 0); d.setDate(d.getDate() - 29); return d.getTime() }
const dayDisabled = (ts: number) => ts > Date.now() || ts < keepFrom()

async function load() {
  loading.value = true
  try {
    const p = new URLSearchParams({
      ...when(), source: source.value, action: action.value, site: site.value, ip: ip.value, q: q.value.trim(),
      limit: String(pageSize.value), offset: String((page.value - 1) * pageSize.value),
    })
    const r = await get<{ items: SecEvent[]; total: number; sources: Record<string, number>; actions: Record<string, number>; top_ips: { ip: string; hits: number }[]; oldest: number }>(`/security/events?${p}`)
    items.value = r.items
    total.value = r.total
    counts.value = r
  } catch (e) {
    fail(e)
  } finally {
    loading.value = false
  }
}
// A new filter starts at the first page.
function reload() {
  page.value = 1
  load()
}
watch([range, day], () => {
  if (range.value !== 'day' || day.value) reload()
})
watch(ip, reload)
onMounted(async () => {
  load()
  try {
    const [w, r] = await Promise.all([get<{ sites: SiteRef[] }>('/waf'), get<{ rules: RuleRef[] }>('/protect/rules')])
    sites.value = w.sites
    rules.value = r.rules
  } catch {
    /* actions that need them stay hidden */
  }
})
defineExpose({ load })

const withN = (label: string, n?: number) => (n ? `${label} (${n.toLocaleString()})` : label)
const sourceOptions = computed(() => [{ label: t('evt.all_sources'), value: '' }, ...['waf', 'rule', 'protection', 'list', 'ip', 'cc'].map((s) => ({ label: withN(t(`evt.sources.${s}`), counts.value.sources[s]), value: s }))])
const actionOptions = computed(() => [{ label: t('evt.all_actions'), value: '' }, ...['blocked', 'detected', 'limited'].map((a) => ({ label: withN(t(`evt.actions.${a}`), counts.value.actions[a]), value: a }))])
const siteOptions = computed(() => [{ label: t('evt.all_sites'), value: '' }, ...sites.value.map((s) => ({ label: s.domain, value: s.domain }))])

const ruleLabel = (e: SecEvent) => {
  switch (e.source) {
    case 'waf': return `CRS ${e.rule}`
    case 'rule': return te(`rules.builtin.${e.rule}.name`) ? `${e.rule} · ${t(`rules.builtin.${e.rule}.name`)}` : `${e.rule}${e.message ? ' · ' + e.message : ''}`
    case 'protection': return t(`prot.reasons.${e.rule}`)
  }
  return t(`evt.sources.${e.source}`)
}

// ---- actions ----
async function run(p: Promise<unknown>, done: string) {
  try {
    const r = (await p) as { task_id?: string }
    if (r?.task_id) emit('task', r.task_id, done)
    else msg.success(done)
    emit('changed')
    load()
  } catch (e) {
    fail(e)
  }
}
function excludeCRS(e: SecEvent, id: string) {
  const x = sites.value.find((s) => s.domain === e.site)
  if (!x) return msg.warning(t('waf.no_site', { host: e.host }))
  run(patch(`/sites/${x.id}`, { waf_exclude: [...new Set([...(x.waf_exclude ?? []), id])] }), `${t('crs.exclude_site')} · ${id} · ${x.domain}`)
}
const ruleOf = (code: string) => rules.value.find((r) => r.code === code)
function options(e: SecEvent) {
  const o: { label: string; key: string }[] = []
  if (e.source === 'waf') for (const m of e.messages ?? []) o.push({ label: t('evt.exclude_on', { id: m.id, site: e.site || e.host }), key: `x:${m.id}` })
  const r = e.source === 'rule' ? ruleOf(e.rule) : undefined
  if (r) {
    o.push({ label: t('evt.rule_off'), key: 'off' })
    o.push({ label: r.action === 'detect' ? t('evt.rule_block') : t('evt.rule_detect'), key: 'flip' })
  }
  o.push({ label: t('evt.block_ip', { ip: e.ip }), key: 'ban' })
  o.push({ label: t('evt.allow_url', { path: e.uri.split('?')[0] }), key: 'allow' })
  return o
}
function pick(e: SecEvent, key: string) {
  if (key.startsWith('x:')) return excludeCRS(e, key.slice(2))
  const r = ruleOf(e.rule)
  if (key === 'off' && r) return run(post(`/protect/rules/${r.id}/enabled`, { enabled: false }), `${t('evt.rule_off')} · ${r.code}`)
  if (key === 'flip' && r) return run(post(`/protect/rules/${r.id}/action`, { action: r.action === 'detect' ? 'block' : 'detect' }), `${t('rules.action')} · ${r.code}`)
  if (key === 'ban') return run(post('/firewall/ips', { ip: e.ip, list: 'block', minutes: 0, remark: `${t('evt.title')}: ${ruleLabel(e)}` }), t('evt.block_ip', { ip: e.ip }))
  if (key === 'allow') return run(post('/firewall/patterns', { kind: 'url', list: 'allow', match: 'prefix', value: e.uri.split('?')[0] || '/', enabled: true, remark: e.site }), t('evt.allow_url', { path: e.uri.split('?')[0] }))
}

const actType = (a: string) => (a === 'blocked' ? 'error' : a === 'limited' ? 'warning' : 'info')
const columns = computed<DataTableColumns<SecEvent>>(() => [
  { title: () => t('common.time'), key: 'time', width: 165, render: (e) => (e.time ? datetime(e.time, locale.value) : '') },
  { title: () => t('common.ip'), key: 'ip', width: 250, render: (e) => h(IpGeo, { ip: e.ip }) },
  { title: () => t('evt.request'), key: 'uri', minWidth: 260, ellipsis: { tooltip: true }, render: (e) => `${e.host} ${e.method} ${e.uri}` },
  { title: () => t('evt.action'), key: 'action', width: 100, render: (e) => h(NTag, { size: 'small', type: actType(e.action) }, () => `${t(`evt.actions.${e.action}`)}${e.status ? ' ' + e.status : ''}`) },
  {
    title: () => t('evt.reason'),
    key: 'rule',
    minWidth: 240,
    ellipsis: { tooltip: true },
    render: (e) =>
      h(NSpace, { size: 4, align: 'center', wrap: false }, () => [
        h(NTag, { size: 'tiny', bordered: false }, () => t(`evt.sources.${e.source}`)),
        e.source === 'waf'
          ? h(NButton, { text: true, type: 'primary', size: 'small', onClick: (ev: MouseEvent) => { ev.stopPropagation(); emit('crs', e.rule) } }, () => `${e.rule} ${e.message ?? ''}`)
          : h('span', ruleLabel(e)),
      ]),
  },
  {
    title: '',
    key: 'ops',
    width: 80,
    render: (e) =>
      h(NDropdown, { trigger: 'click', options: options(e), onSelect: (k: string) => pick(e, k) }, () =>
        h(NButton, { size: 'tiny', onClick: (ev: MouseEvent) => ev.stopPropagation() }, () => t('evt.ops'))),
  },
])

const detail = ref<SecEvent | null>(null)
const detailRows = computed<DetailRow[]>(() => {
  const e = detail.value
  if (!e) return []
  return [
    { label: t('common.time'), value: e.time ? datetime(e.time, locale.value) : '' },
    { label: t('common.ip'), value: () => h(IpGeo, { ip: e.ip }) },
    { label: t('route.site'), value: e.site || '—' },
    { label: t('evt.request'), value: `${e.method} ${e.host}${e.uri}`, mono: true },
    { label: 'User-Agent', value: e.ua || '—', mono: true },
    { label: t('evt.action'), value: `${t(`evt.actions.${e.action}`)} (${e.status})` },
    { label: t('evt.reason'), value: `${t(`evt.sources.${e.source}`)} · ${ruleLabel(e)}` },
    ...(e.messages ?? []).map((m) => ({ label: `CRS ${m.id}`, value: `[${m.severity}] ${m.message}${m.data ? `\n${m.data}` : ''}`, mono: true })),
  ]
})
</script>

<template>
  <div class="bar">
    <n-radio-group v-model:value="range" size="small">
      <n-radio-button value="today">{{ t('evt.today') }}</n-radio-button>
      <n-radio-button value="yesterday">{{ t('evt.yesterday') }}</n-radio-button>
      <n-radio-button value="7">{{ t('evt.last_days', { n: 7 }) }}</n-radio-button>
      <n-radio-button value="30">{{ t('evt.last_days', { n: 30 }) }}</n-radio-button>
      <n-radio-button value="day">{{ t('evt.pick_day') }}</n-radio-button>
    </n-radio-group>
    <n-date-picker v-if="range === 'day'" v-model:value="day" type="date" size="small" clearable :is-date-disabled="dayDisabled" style="width: 150px" />
  </div>
  <div class="bar">
    <n-select v-if="!props.site" v-model:value="site" :options="siteOptions" size="small" filterable style="width: 200px" @update:value="reload" />
    <n-select v-model:value="source" :options="sourceOptions" size="small" style="width: 190px" @update:value="reload" />
    <n-select v-model:value="action" :options="actionOptions" size="small" style="width: 150px" @update:value="reload" />
    <n-input v-model:value="q" size="small" clearable :placeholder="t('evt.search')" style="width: 220px" @keyup.enter="reload" @clear="reload" />
    <n-button size="small" @click="load">{{ t('common.refresh') }}</n-button>
  </div>
  <div v-if="counts.top_ips.length || ip" class="top">
    <span class="top-label">{{ t('evt.top_ips') }}</span>
    <n-tag v-if="ip" size="small" type="primary" closable @close="ip = ''">{{ ip }}</n-tag>
    <template v-else>
      <n-tag v-for="x in counts.top_ips" :key="x.ip" size="small" class="top-ip" :bordered="false" @click="ip = x.ip">{{ x.ip }} · {{ x.hits.toLocaleString() }}</n-tag>
    </template>
  </div>
  <n-data-table
    remote size="small" :columns="columns" :data="items" :loading="loading" :scroll-x="1200"
    :pagination="{ page, pageSize, itemCount: total, showSizePicker: true, pageSizes: [50, 100, 200], prefix: () => t('evt.total', { n: total.toLocaleString() }) }"
    :row-props="(e: SecEvent) => ({ style: 'cursor: pointer', onClick: () => (detail = e) })"
    @update:page="(p: number) => { page = p; load() }"
    @update:page-size="(n: number) => { pageSize = n; reload() }"
  />
  <DetailDrawer :show="!!detail" :title="detail ? `${detail.method} ${detail.host}` : ''" :rows="detailRows" :width="600" @close="detail = null" />
</template>

<style scoped>
.bar {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
  margin-bottom: 8px;
}
.top {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: center;
  margin-bottom: 8px;
  font-size: 12.5px;
}
.top-label {
  opacity: 0.65;
}
.top-ip {
  cursor: pointer;
}
</style>
