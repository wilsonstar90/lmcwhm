<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NCard, NDataTable, NDynamicTags, NForm, NFormItem, NInput, NInputNumber, NRadioButton, NRadioGroup, NSelect,
  NSpace, NSwitch, NTag, NTooltip, useMessage, type DataTableColumns,
} from 'naive-ui'
import { get, patch, put, ApiError } from '../api/client'
import { datetime } from '../format'
import IpGeo from './IpGeo.vue'
import DetailDrawer, { type DetailRow } from './DetailDrawer.vue'
import TaskModal from './TaskModal.vue'
import AiButton from './AiButton.vue'

interface Settings { enabled: boolean; engine: 'On' | 'DetectionOnly'; paranoia: number; anomaly: number }
interface SiteWAF { id: number; domain: string; waf_off: boolean; waf_exclude: string[] | null }
interface Rule { id: string; message: string; severity: string; data?: string }
interface Event { time: number; ip: string; host: string; method: string; uri: string; status: number; blocked: boolean; messages: Rule[] }

const { t, locale } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))
const s = ref<Settings | null>(null)
const image = ref('')
const sites = ref<SiteWAF[]>([])
const events = ref<Event[]>([])
const q = ref('')
const loading = ref(false)
const task = ref<{ id: string; title: string } | null>(null)

async function load() {
  try {
    const r = await get<{ settings: Settings; image: string; sites: SiteWAF[] }>('/waf')
    s.value = r.settings
    image.value = r.image
    sites.value = r.sites.map((x) => ({ ...x, waf_exclude: x.waf_exclude ?? [] }))
  } catch (e) {
    fail(e)
  }
  loadEvents()
}
async function loadEvents() {
  loading.value = true
  try {
    events.value = (await get<{ items: Event[] }>(`/waf/events?lines=500&q=${encodeURIComponent(q.value)}`)).items
  } finally {
    loading.value = false
  }
}
onMounted(load)
defineExpose({ load })

async function save() {
  try {
    const r = await put<{ task_id: string }>('/waf', s.value)
    task.value = { id: r.task_id, title: t('waf.apply') }
  } catch (e) {
    fail(e)
  }
}
async function saveSite(x: SiteWAF) {
  try {
    const r = await patch<{ task_id: string }>(`/sites/${x.id}`, { waf_off: x.waf_off, waf_exclude: x.waf_exclude })
    task.value = { id: r.task_id, title: `WAF · ${x.domain}` }
  } catch (e) {
    fail(e)
  }
}
function exclude(e: Event, id: string) {
  const host = e.host.split(':')[0]
  const x = sites.value.find((st) => st.domain === host) ?? sites.value.find((st) => host.endsWith('.' + st.domain))
  if (!x) return msg.warning(t('waf.no_site', { host }))
  if (!x.waf_exclude!.includes(id)) x.waf_exclude!.push(id)
  saveSite(x)
}

const siteCols: DataTableColumns<SiteWAF> = [
  { title: () => t('route.site'), key: 'domain', width: 220, render: (x) => h('b', x.domain) },
  { title: 'WAF', key: 'waf_off', width: 80, render: (x) => h(NSwitch, { size: 'small', value: !x.waf_off, 'onUpdate:value': (on: boolean) => { x.waf_off = !on; saveSite(x) } }) },
  {
    title: () => t('waf.excluded'),
    key: 'waf_exclude',
    render: (x) => h(NDynamicTags, { size: 'small', value: x.waf_exclude!, 'onUpdate:value': (v: string[]) => { x.waf_exclude = v.filter((id) => /^\d{1,7}$/.test(id)); saveSite(x) } }),
  },
]
const sevType = (sv: string) => (['EMERGENCY', 'ALERT', 'CRITICAL'].includes(sv) ? 'error' : sv === 'ERROR' || sv === 'WARNING' ? 'warning' : 'default')
const ev = ref<Event | null>(null)
const evRows = computed<DetailRow[]>(() => {
  const e = ev.value
  if (!e) return []
  return [
    { label: t('common.time'), value: e.time ? datetime(e.time, locale.value) : '' },
    { label: t('common.ip'), value: () => h(IpGeo, { ip: e.ip }) },
    { label: t('waf.host'), value: e.host },
    { label: t('logs.request'), value: `${e.method} ${e.uri}`, mono: true },
    { label: t('waf.http_status'), value: e.status },
    { label: t('common.status'), value: e.blocked ? t('waf.blocked') : t('waf.detected') },
    ...e.messages.map((m) => ({ label: `${t('waf.rule')} ${m.id}`, value: `[${m.severity}] ${m.message}${m.data ? `\n${m.data}` : ''}`, mono: true })),
  ]
})
const evCols: DataTableColumns<Event> = [
  { title: () => t('common.time'), key: 'time', width: 170, render: (e) => (e.time ? datetime(e.time, locale.value) : '') },
  { title: () => t('common.ip'), key: 'ip', width: 270, render: (e) => h(IpGeo, { ip: e.ip }) },
  { title: () => t('logs.request'), key: 'uri', ellipsis: { tooltip: true }, render: (e) => `${e.host} ${e.method} ${e.uri}` },
  { title: () => t('common.status'), key: 'status', width: 110, render: (e) => h(NTag, { size: 'small', type: e.blocked ? 'error' : 'warning' }, () => (e.blocked ? t('waf.blocked') : t('waf.detected'))) },
  {
    title: () => t('waf.rules'),
    key: 'messages',
    width: 360,
    render: (e) =>
      h(NSpace, { size: 4, vertical: true }, () =>
        e.messages.map((m) =>
          h(NSpace, { size: 4, align: 'center', wrap: false }, () => [
            h(NTooltip, null, { trigger: () => h(NTag, { size: 'tiny', type: sevType(m.severity) }, () => m.id), default: () => [m.message, m.data ? h('div', { style: 'opacity:.7' }, m.data) : null] }),
            h('span', { style: 'font-size:12px' }, m.message.slice(0, 50)),
            h(NButton, { size: 'tiny', quaternary: true, onClick: () => exclude(e, m.id) }, () => t('waf.exclude')),
          ]),
        ),
      ),
  },
]
</script>

<template>
  <n-card v-if="s" size="small" :title="t('waf.title')">
    <template #header-extra><n-tag :type="s.enabled ? 'success' : 'default'" size="small">{{ s.enabled ? t('fw.on') : t('fw.off') }}</n-tag></template>
    <n-form label-placement="left" label-width="140">
      <n-form-item :label="t('waf.enable')">
        <n-switch v-model:value="s.enabled" />
        <span class="hint">{{ t('waf.enable_hint', { image }) }}</span>
      </n-form-item>
      <n-form-item :label="t('waf.engine')">
        <n-radio-group v-model:value="s.engine">
          <n-radio-button value="On">{{ t('waf.engines.On') }}</n-radio-button>
          <n-radio-button value="DetectionOnly">{{ t('waf.engines.DetectionOnly') }}</n-radio-button>
        </n-radio-group>
      </n-form-item>
      <n-form-item :label="t('waf.paranoia')">
        <n-select v-model:value="s.paranoia" :options="[1, 2, 3, 4].map((n) => ({ label: t(`waf.pl.${n}`), value: n }))" style="width: 320px" />
      </n-form-item>
      <n-form-item :label="t('waf.anomaly')">
        <n-input-number v-model:value="s.anomaly" :min="1" :max="100" style="width: 120px" />
        <span class="hint">{{ t('waf.anomaly_hint') }}</span>
      </n-form-item>
      <n-button type="primary" @click="save">{{ t('waf.save_apply') }}</n-button>
    </n-form>
  </n-card>

  <n-card size="small" :title="t('waf.sites')" style="margin-top: 16px">
    <n-alert v-if="s && !s.enabled" type="default" :show-icon="false" style="margin-bottom: 8px">{{ t('waf.sites_off') }}</n-alert>
    <n-data-table size="small" :columns="siteCols" :data="sites" :row-key="(x: SiteWAF) => x.id" />
  </n-card>

  <n-card size="small" :title="t('waf.events')" style="margin-top: 16px">
    <template #header-extra>
      <n-space>
        <n-input v-model:value="q" size="small" clearable :placeholder="t('logs.filter_kw')" style="width: 200px" @keyup.enter="loadEvents" />
        <n-button size="small" @click="loadEvents">{{ t('common.refresh') }}</n-button>
        <AiButton :prompt="t('ai.prompt_waf')" />
      </n-space>
    </template>
    <n-data-table size="small" :columns="evCols" :data="events" :loading="loading" :pagination="{ pageSize: 20 }" :row-props="(e: Event) => ({ style: 'cursor: pointer', onClick: () => (ev = e) })" :scroll-x="1200" />
  </n-card>
  <TaskModal :task-id="task?.id ?? null" :title="task?.title ?? ''" @close="task = null; load()" @done="load" />
  <DetailDrawer :show="!!ev" :title="ev ? `${ev.method} ${ev.host}` : ''" :rows="evRows" :width="600" @close="ev = null" />
</template>

<style scoped>
.hint {
  margin-left: 12px;
  font-size: 12px;
  opacity: 0.65;
}
</style>
