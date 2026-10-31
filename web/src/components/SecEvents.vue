<script setup lang="ts">
// Every blocked or detected request: the edge (lists, website protection,
// dedicated rules, CC) and the WAF (OWASP CRS) together, with the actions
// that follow from one: exclude a CRS rule, switch a dedicated rule, block
// the address, allow the URL.
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NDataTable, NDropdown, NInput, NSelect, NSpace, NTag, useMessage, type DataTableColumns } from 'naive-ui'
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
const loading = ref(false)
const source = ref('')
const action = ref('')
const site = ref(props.site ?? '')
const q = ref('')
const sites = ref<SiteRef[]>([])
const rules = ref<RuleRef[]>([])

async function load() {
  loading.value = true
  try {
    const p = new URLSearchParams({ source: source.value, action: action.value, site: site.value, q: q.value.trim(), limit: '1000' })
    items.value = (await get<{ items: SecEvent[] }>(`/security/events?${p}`)).items
  } catch (e) {
    fail(e)
  } finally {
    loading.value = false
  }
}
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

const sourceOptions = computed(() => [{ label: t('evt.all_sources'), value: '' }, ...['waf', 'rule', 'protection', 'list', 'ip', 'cc'].map((s) => ({ label: t(`evt.sources.${s}`), value: s }))])
const actionOptions = computed(() => [{ label: t('evt.all_actions'), value: '' }, ...['blocked', 'detected', 'limited'].map((a) => ({ label: t(`evt.actions.${a}`), value: a }))])
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
    <n-select v-if="!props.site" v-model:value="site" :options="siteOptions" size="small" filterable style="width: 200px" @update:value="load" />
    <n-select v-model:value="source" :options="sourceOptions" size="small" style="width: 160px" @update:value="load" />
    <n-select v-model:value="action" :options="actionOptions" size="small" style="width: 130px" @update:value="load" />
    <n-input v-model:value="q" size="small" clearable :placeholder="t('evt.search')" style="width: 220px" @keyup.enter="load" @clear="load" />
    <n-button size="small" @click="load">{{ t('common.refresh') }}</n-button>
  </div>
  <n-data-table
    size="small" :columns="columns" :data="items" :loading="loading" :pagination="{ pageSize: 30 }" :scroll-x="1200"
    :row-props="(e: SecEvent) => ({ style: 'cursor: pointer', onClick: () => (detail = e) })"
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
</style>
