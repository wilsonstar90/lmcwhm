<script setup lang="ts">
import { computed, h, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NButton, NCard, NCheckbox, NCode, NDataTable, NDrawer, NDrawerContent, NForm, NFormItem, NInput, NInputNumber, NModal, NPopconfirm,
  NSelect, NSpace, NSwitch, NTag, useMessage, type DataTableColumns,
} from 'naive-ui'
import PathTags from '../components/PathTags.vue'
import SchedulePicker from '../components/SchedulePicker.vue'
import RemarkCell from '../components/RemarkCell.vue'
import { get, post, put, ApiError } from '../api/client'
import { datetime } from '../format'
import { CodeEditor } from '../components/code-editor'

interface Run { id: number; job_id: number; started_at: number; duration_ms: number; status: string; exit_code: number; output?: string; manual: boolean }
interface Job { id: number; name: string; remark: string; type: 'shell' | 'url' | 'logrotate' | 'backup' | 'system' | 'ip_feeds' | 'malware_scan'; schedule: string; command: string; enabled: boolean; timeout: number; next: number; running: boolean; last?: Run }

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

// ---- form ----
interface Form { id: number; name: string; remark: string; type: Job['type']; command: string; keep: number; mwPaths: string[]; mwClam: boolean; enabled: boolean; timeout: number; schedule: string }
const blank = (): Form => ({ id: 0, name: '', remark: '', type: 'shell', command: '', keep: 7, mwPaths: [], mwClam: false, enabled: true, timeout: 3600, schedule: '0 3 * * *' })
const f = ref<Form | null>(null)
const previewErr = ref(false)

function openCreate() {
  f.value = blank()
}
function openEdit(j: Job) {
  const x = blank()
  Object.assign(x, { id: j.id, name: j.name, remark: j.remark ?? '', type: j.type, enabled: j.enabled, timeout: j.timeout, schedule: j.schedule })
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
  if (kind === 'logrotate') Object.assign(x, { name: t('cron.tpl.logrotate'), type: 'logrotate', schedule: '0 0 * * *' })
  if (kind === 'docker_prune') Object.assign(x, { name: t('cron.tpl.docker_prune'), command: 'docker image prune -f\ndocker builder prune -f', schedule: '0 4 * * 0' })
  if (kind === 'apt') Object.assign(x, { name: t('cron.tpl.apt'), command: 'apt-get update -q && apt-get -y -q upgrade', schedule: '0 5 * * 0' })
  f.value = x
}

async function save() {
  const x = f.value!
  const command = x.type === 'logrotate' ? JSON.stringify({ keep: x.keep }) : x.type === 'malware_scan' ? JSON.stringify({ paths: x.mwPaths, clamav: x.mwClam }) : x.command
  const body = { name: x.name, remark: x.remark, type: x.type, schedule: x.schedule, command, enabled: x.enabled, timeout: x.timeout }
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
  { title: () => t('cron.name'), key: 'name', minWidth: 180, render: (j) => h('b', j.name) },
  { title: () => t('cron.type'), key: 'type', width: 200, render: (j) => typeLabel(j) },
  { title: () => t('site.remark'), key: 'remark', minWidth: 140, render: (j) => h(RemarkCell, { value: j.remark, save: (v: string) => put(`/cron/${j.id}/remark`, { remark: v }).then(() => (j.remark = v)) }) },
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

const typeOptions = computed(() => (['shell', 'url', 'logrotate', 'backup', 'system', 'ip_feeds', 'malware_scan'] as const).map((v) => ({ label: t(`cron.types.${v}`), value: v })))
const systemOptions = computed(() => ['ssl_renew', 'time_sync', 'release_memory', 'security_scan', 'app_updates', 'site_expiry'].map((v) => ({ label: t(`cron.sys.${v}`), value: v })))
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
  <n-data-table size="small" :columns="columns" :data="jobs" :loading="loading" :row-key="(j: Job) => j.id" :scroll-x="1400" />

  <n-modal :show="!!f" @update:show="(v: boolean) => !v && (f = null)">
    <n-card v-if="f" :title="f.id ? t('common.edit') : t('cron.create')" style="width: min(640px, calc(100vw - 32px))" closable @close="f = null">
      <n-form label-placement="top">
        <n-form-item :label="t('cron.name')"><n-input v-model:value="f.name" /></n-form-item>
        <n-form-item :label="t('cron.type')"><n-select v-model:value="f.type" :options="typeOptions" /></n-form-item>
        <n-form-item :label="t('site.remark')"><n-input v-model:value="f.remark" maxlength="200" /></n-form-item>
        <n-form-item :label="t('cron.schedule')">
          <SchedulePicker v-model="f.schedule" @invalid="(v: boolean) => (previewErr = v)" />
        </n-form-item>
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
