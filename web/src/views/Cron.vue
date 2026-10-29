<script setup lang="ts">
import { computed, h, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NButton, NCard, NCheckbox, NCode, NDataTable, NDrawer, NDrawerContent, NForm, NFormItem, NInput, NInputNumber, NModal, NPopconfirm,
  NSelect, NSpace, NSwitch, NTag, NTimePicker, useMessage, type DataTableColumns,
} from 'naive-ui'
import PathTags from '../components/PathTags.vue'
import { get, post, put, ApiError } from '../api/client'
import { datetime } from '../format'
import { CodeEditor } from '../components/code-editor'

interface Run { id: number; job_id: number; started_at: number; duration_ms: number; status: string; exit_code: number; output?: string; manual: boolean }
interface Job { id: number; name: string; type: 'shell' | 'url' | 'logrotate' | 'backup' | 'system' | 'ip_feeds' | 'malware_scan'; schedule: string; command: string; enabled: boolean; timeout: number; next: number; running: boolean; last?: Run }

const { t, locale } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))
const jobs = ref<Job[]>([])
const loading = ref(false)
let timer: number | undefined

async function load() {
  loading.value = true
  try {
    jobs.value = (await get<{ items: Job[] }>('/cron')).items
  } catch (e) {
    fail(e)
  } finally {
    loading.value = false
  }
}
onMounted(() => {
  load()
  timer = window.setInterval(() => jobs.value.some((j) => j.running) && load(), 3000)
})
onBeforeUnmount(() => clearInterval(timer))

// ---- schedule builder ----
type Mode = 'seconds' | 'minutes' | 'hourly' | 'daily' | 'weekly' | 'monthly' | 'custom'
interface Form { id: number; name: string; type: Job['type']; command: string; keep: number; mwPaths: string[]; mwClam: boolean; enabled: boolean; timeout: number; mode: Mode; n: number; minute: number; time: number; dow: number; dom: number; custom: string }
const blank = (): Form => ({ id: 0, name: '', type: 'shell', command: '', keep: 7, mwPaths: [], mwClam: false, enabled: true, timeout: 3600, mode: 'daily', n: 5, minute: 0, time: 3 * 3600_000, dow: 1, dom: 1, custom: '0 3 * * *' })
const f = ref<Form | null>(null)
const preview = ref<number[]>([])
const previewErr = ref(false)

// time pickers hold milliseconds since midnight (UTC-based value)
const hm = (ms: number) => [Math.floor(ms / 60000) % 60, Math.floor(ms / 3600000) % 24]
function expr(x: Form): string {
  const [mi, hr] = hm(x.time)
  switch (x.mode) {
    case 'seconds': return `*/${x.n} * * * * *`
    case 'minutes': return `*/${x.n} * * * *`
    case 'hourly': return `${x.minute} * * * *`
    case 'daily': return `${mi} ${hr} * * *`
    case 'weekly': return `${mi} ${hr} * * ${x.dow}`
    case 'monthly': return `${mi} ${hr} ${x.dom} * *`
  }
  return x.custom.trim()
}
const schedule = computed(() => (f.value ? expr(f.value) : ''))
let pt: number | undefined
watch(schedule, (s) => {
  clearTimeout(pt)
  pt = window.setTimeout(async () => {
    if (!s) return
    try {
      preview.value = (await get<{ next: number[] }>(`/cron/preview?schedule=${encodeURIComponent(s)}`)).next
      previewErr.value = false
    } catch {
      preview.value = []
      previewErr.value = true
    }
  }, 250)
})

function openCreate() {
  f.value = blank()
}
function openEdit(j: Job) {
  const x = blank()
  Object.assign(x, { id: j.id, name: j.name, type: j.type, enabled: j.enabled, timeout: j.timeout, mode: 'custom', custom: j.schedule })
  if (j.type === 'logrotate') x.keep = JSON.parse(j.command).keep ?? 7
  else if (j.type === 'malware_scan') {
    const m = JSON.parse(j.command || '{}')
    x.mwPaths = m.paths ?? []
    x.mwClam = !!m.clamav
  } else x.command = j.command
  f.value = x
}
function fromTemplate(kind: 'logrotate' | 'docker_prune' | 'apt') {
  const x = blank()
  if (kind === 'logrotate') Object.assign(x, { name: t('cron.tpl.logrotate'), type: 'logrotate', mode: 'daily', time: 0 })
  if (kind === 'docker_prune') Object.assign(x, { name: t('cron.tpl.docker_prune'), command: 'docker image prune -f\ndocker builder prune -f', mode: 'weekly', dow: 0, time: 4 * 3600_000 })
  if (kind === 'apt') Object.assign(x, { name: t('cron.tpl.apt'), command: 'apt-get update -q && apt-get -y -q upgrade', mode: 'weekly', dow: 0, time: 5 * 3600_000 })
  f.value = x
}

async function save() {
  const x = f.value!
  const command = x.type === 'logrotate' ? JSON.stringify({ keep: x.keep }) : x.type === 'malware_scan' ? JSON.stringify({ paths: x.mwPaths, clamav: x.mwClam }) : x.command
  const body = { name: x.name, type: x.type, schedule: schedule.value, command, enabled: x.enabled, timeout: x.timeout }
  try {
    if (x.id) await put(`/cron/${x.id}`, body)
    else await post('/cron', body)
    f.value = null
    load()
  } catch (e) {
    fail(e)
  }
}

async function runNow(j: Job) {
  try {
    await post(`/cron/${j.id}/run`)
    msg.success(t('cron.started'))
    setTimeout(load, 500)
  } catch (e) {
    fail(e)
  }
}
async function toggle(j: Job, on: boolean) {
  try {
    await post(`/cron/${j.id}/enabled`, { enabled: on })
    load()
  } catch (e) {
    fail(e)
  }
}
async function remove(j: Job) {
  try {
    await post(`/cron/${j.id}/delete`)
    load()
  } catch (e) {
    fail(e)
  }
}

// ---- runs ----
const runsOf = ref<Job | null>(null)
const runs = ref<Run[]>([])
const output = ref<Run | null>(null)
async function openRuns(j: Job) {
  runsOf.value = j
  runs.value = (await get<{ items: Run[] }>(`/cron/${j.id}/runs?limit=100`)).items
}
async function openOutput(r: Run) {
  output.value = await get<Run>(`/cron/runs/${r.id}`)
}

const statusTag = (s: string) =>
  h(NTag, { size: 'small', type: ({ success: 'success', failed: 'error', timeout: 'error', running: 'info', skipped: 'warning' } as const)[s] ?? 'default' }, () => t(`cron.status.${s}`))
const dur = (ms: number) => (ms < 1000 ? `${ms} ms` : `${(ms / 1000).toFixed(1)} s`)

const columns: DataTableColumns<Job> = [
  { title: () => t('cron.name'), key: 'name', render: (j) => h('b', j.name) },
  { title: () => t('cron.type'), key: 'type', width: 200, render: (j) => typeLabel(j) },
  { title: () => t('cron.schedule'), key: 'schedule', width: 150, render: (j) => h('code', j.schedule) },
  { title: () => t('cron.next'), key: 'next', width: 170, render: (j) => (j.enabled && j.next ? datetime(j.next, locale.value) : '—') },
  {
    title: () => t('cron.last'),
    key: 'last',
    width: 230,
    render: (j) =>
      j.running
        ? statusTag('running')
        : j.last
          ? h(NSpace, { size: 6, align: 'center' }, () => [statusTag(j.last!.status), h('span', { style: 'font-size:12px;opacity:.7' }, datetime(j.last!.started_at, locale.value))])
          : '—',
  },
  { title: () => t('cron.enabled'), key: 'enabled', width: 80, render: (j) => h(NSwitch, { size: 'small', value: j.enabled, 'onUpdate:value': (v: boolean) => toggle(j, v) }) },
  {
    title: '',
    key: 'actions',
    width: 260,
    render: (j) =>
      h(NSpace, { size: 6 }, () => [
        h(NButton, { size: 'tiny', type: 'primary', disabled: j.running, onClick: () => runNow(j) }, () => t('cron.run_now')),
        h(NButton, { size: 'tiny', onClick: () => openRuns(j) }, () => t('cron.history')),
        h(NButton, { size: 'tiny', onClick: () => openEdit(j) }, () => t('common.edit')),
        h(NPopconfirm, { onPositiveClick: () => remove(j) }, { trigger: () => h(NButton, { size: 'tiny', type: 'error', ghost: true }, () => t('common.delete')), default: () => t('cron.delete_confirm', { name: j.name }) }),
      ]),
  },
]
const runCols: DataTableColumns<Run> = [
  { title: () => t('common.time'), key: 'started_at', width: 170, render: (r) => datetime(r.started_at, locale.value) },
  { title: () => t('common.status'), key: 'status', width: 100, render: (r) => statusTag(r.status) },
  { title: () => t('cron.duration'), key: 'duration_ms', width: 90, render: (r) => dur(r.duration_ms) },
  { title: () => t('cron.trigger'), key: 'manual', width: 80, render: (r) => (r.manual ? t('cron.manual') : t('cron.auto')) },
  { title: () => t('cron.output'), key: 'output', ellipsis: { tooltip: false }, render: (r) => h(NButton, { text: true, type: 'primary', onClick: () => openOutput(r) }, () => (r.output || '—').split('\n')[0].slice(0, 80) || t('cron.view')) },
]

const modeOptions = computed(() => (['seconds', 'minutes', 'hourly', 'daily', 'weekly', 'monthly', 'custom'] as Mode[]).map((m) => ({ label: t(`cron.modes.${m}`), value: m })))
const dowOptions = computed(() => [0, 1, 2, 3, 4, 5, 6].map((d) => ({ label: new Intl.DateTimeFormat(locale.value, { weekday: 'long' }).format(new Date(2026, 0, 4 + d)), value: d })))
const typeOptions = computed(() => (['shell', 'url', 'logrotate', 'backup', 'system', 'ip_feeds', 'malware_scan'] as const).map((v) => ({ label: t(`cron.types.${v}`), value: v })))
const systemOptions = computed(() => ['ssl_renew', 'time_sync', 'release_memory', 'security_scan', 'app_updates'].map((v) => ({ label: t(`cron.sys.${v}`), value: v })))
const feedOptions = computed(() => ['block', 'trust'].map((v) => ({ label: t(`cron.feeds.${v}`), value: v })))
// What a built-in task does, next to its type in the list.
function typeLabel(j: Job) {
  const base = t(`cron.types.${j.type}`)
  if (j.type === 'system') return `${base} · ${t(`cron.sys.${j.command}`)}`
  if (j.type === 'ip_feeds') return `${base} · ${t(`cron.feeds.${j.command}`)}`
  return base
}
watch(() => f.value?.type, (ty, old) => {
  const x = f.value
  if (!x || !old || ty === old) return
  if (ty === 'system') x.command = 'ssl_renew'
  else if (ty === 'ip_feeds') x.command = 'block'
  else if (old === 'system' || old === 'ip_feeds') x.command = ''
})
</script>

<template>
  <n-space justify="space-between" style="margin-bottom: 12px">
    <n-space>
      <n-button type="primary" @click="openCreate">{{ t('cron.create') }}</n-button>
      <n-button @click="fromTemplate('logrotate')">{{ t('cron.tpl.logrotate') }}</n-button>
      <n-button @click="fromTemplate('docker_prune')">{{ t('cron.tpl.docker_prune') }}</n-button>
      <n-button @click="fromTemplate('apt')">{{ t('cron.tpl.apt') }}</n-button>
    </n-space>
    <n-button size="small" @click="load">{{ t('common.refresh') }}</n-button>
  </n-space>
  <n-data-table size="small" :columns="columns" :data="jobs" :loading="loading" :row-key="(j: Job) => j.id" :scroll-x="1100" />

  <n-modal :show="!!f" @update:show="(v: boolean) => !v && (f = null)">
    <n-card v-if="f" :title="f.id ? t('common.edit') : t('cron.create')" style="width: min(640px, calc(100vw - 32px))" closable @close="f = null">
      <n-form label-placement="top">
        <n-form-item :label="t('cron.name')"><n-input v-model:value="f.name" /></n-form-item>
        <n-form-item :label="t('cron.type')"><n-select v-model:value="f.type" :options="typeOptions" /></n-form-item>
        <n-form-item :label="t('cron.schedule')" :feedback="previewErr ? t('errors.cron.invalid_schedule') : ''" :validation-status="previewErr ? 'error' : undefined">
          <n-space :size="8" align="center">
            <n-select v-model:value="f.mode" :options="modeOptions" style="width: 150px" />
            <template v-if="f.mode === 'seconds' || f.mode === 'minutes'">
              <span>{{ t('cron.every') }}</span>
              <n-input-number v-model:value="f.n" :min="1" :max="59" style="width: 100px" />
              <span>{{ f.mode === 'seconds' ? t('cron.unit_s') : t('cron.unit_m') }}</span>
            </template>
            <template v-if="f.mode === 'hourly'">
              <span>{{ t('cron.at_minute') }}</span><n-input-number v-model:value="f.minute" :min="0" :max="59" style="width: 100px" />
            </template>
            <n-select v-if="f.mode === 'weekly'" v-model:value="f.dow" :options="dowOptions" style="width: 130px" />
            <template v-if="f.mode === 'monthly'">
              <n-input-number v-model:value="f.dom" :min="1" :max="31" style="width: 90px" /><span>{{ t('cron.day') }}</span>
            </template>
            <n-time-picker v-if="['daily', 'weekly', 'monthly'].includes(f.mode)" v-model:value="f.time" format="HH:mm" time-zone="UTC" style="width: 110px" />
            <n-input v-if="f.mode === 'custom'" v-model:value="f.custom" placeholder="*/5 * * * *" style="width: 220px" />
          </n-space>
        </n-form-item>
        <p style="font-size: 12px; opacity: 0.7; margin-top: -8px">
          <code>{{ schedule }}</code> · {{ t('cron.preview') }}: {{ preview.slice(0, 3).map((x) => datetime(x, locale)).join(' · ') || '—' }}
        </p>
        <n-form-item v-if="f.type === 'shell'" :label="t('cron.script')" :feedback="t('cron.script_hint')">
          <CodeEditor v-model="f.command" language="shell" height="240px" placeholder="#!/bin/bash" />
        </n-form-item>
        <n-form-item v-if="f.type === 'url'" label="URL"><n-input v-model:value="f.command" placeholder="https://example.com/cron.php" /></n-form-item>
        <n-form-item v-if="f.type === 'backup'" :label="t('cron.types.backup')" :feedback="t('bk.cron_hint')">
          <CodeEditor v-model="f.command" language="json" height="140px" placeholder='{"kind":"site","target":"*","dests":["local"],"keep":7}' />
        </n-form-item>
        <n-form-item v-if="f.type === 'system'" :label="t('cron.task')" :feedback="t(`cron.sys_hint.${f.command}`)">
          <n-select v-model:value="f.command" :options="systemOptions" />
        </n-form-item>
        <n-form-item v-if="f.type === 'ip_feeds'" :label="t('cron.task')" :feedback="t('cron.feeds_hint')">
          <n-select v-model:value="f.command" :options="feedOptions" />
        </n-form-item>
        <template v-if="f.type === 'malware_scan'">
          <n-form-item :label="t('cron.mw_paths')" :feedback="t('cron.mw_paths_hint')"><PathTags v-model:value="f.mwPaths" /></n-form-item>
          <n-checkbox v-model:checked="f.mwClam" style="margin-bottom: 12px">{{ t('cron.mw_clam') }}</n-checkbox>
        </template>
        <n-form-item v-if="f.type === 'logrotate'" :label="t('cron.keep')" :feedback="t('cron.logrotate_hint')">
          <n-input-number v-model:value="f.keep" :min="1" :max="365" />
        </n-form-item>
        <n-space>
          <n-form-item :label="t('cron.timeout')"><n-input-number v-model:value="f.timeout" :min="1" :max="604800" style="width: 140px"><template #suffix>s</template></n-input-number></n-form-item>
          <n-form-item :label="t('cron.enabled')"><n-switch v-model:value="f.enabled" /></n-form-item>
        </n-space>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="f = null">{{ t('common.cancel') }}</n-button>
          <n-button type="primary" :disabled="!f.name || previewErr" @click="save">{{ t('common.save') }}</n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>

  <n-drawer :show="!!runsOf" :width="760" @update:show="(v: boolean) => !v && (runsOf = null)">
    <n-drawer-content v-if="runsOf" :title="`${t('cron.history')} · ${runsOf.name}`" closable>
      <n-data-table size="small" :columns="runCols" :data="runs" :row-key="(r: Run) => r.id" />
    </n-drawer-content>
  </n-drawer>
  <n-modal :show="!!output" @update:show="(v: boolean) => !v && (output = null)">
    <n-card v-if="output" :title="`${t('cron.output')} · ${datetime(output.started_at, locale)}`" style="width: min(820px, calc(100vw - 32px))" closable @close="output = null">
      <div style="max-height: 60vh; overflow: auto"><n-code :code="output.output || '—'" word-wrap /></div>
    </n-card>
  </n-modal>
</template>
