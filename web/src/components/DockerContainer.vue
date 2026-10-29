<script setup lang="ts">
import { computed, h, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NCard, NCheckbox, NCode, NDataTable, NDatePicker, NDescriptions, NDescriptionsItem, NForm, NFormItem, NGi, NGrid,
  NInput, NInputNumber, NModal, NPopconfirm, NRadio, NRadioButton, NRadioGroup, NSelect, NSpace, NSwitch, NTabPane, NTabs, NTag, useMessage,
  type DataTableColumns,
} from 'naive-ui'
import { get, post, ApiError } from '../api/client'
import type { Container, ContainerInfo, ContainerSpec, DockerNetwork, DockerUsage, Site } from '../api/types'
import { bytes, datetime } from '../format'
import LineChart from './LineChart.vue'
import TaskModal from './TaskModal.vue'
import TerminalView from './TerminalView.vue'
import DockerVolumes from './DockerVolumes.vue'

// The settings window of one container (aaPanel 容器設定): a left menu of
// status, terminal, details, storage, networks, policy and the changes that
// recreate it, live monitor, logs and the reverse proxy.
const props = defineProps<{ container: Container | null; tab?: string }>()
const emit = defineEmits<{ close: []; changed: []; bind: [port: number, name: string] }>()
const { t, locale } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))

const tabs = ['status', 'terminal', 'details', 'volumes', 'networks', 'restart', 'commit', 'edit', 'upgrade', 'rename', 'monitor', 'logs', 'proxy'] as const
type Tab = (typeof tabs)[number]
const tab = ref<Tab>('status')
// Calls go by name: it survives recreating the container (edit, upgrade).
const name = ref('')
const info = ref<ContainerInfo | null>(null)
const task = ref<{ id: string; title: string; after?: () => void } | null>(null)
const base = computed(() => `/docker/containers/${encodeURIComponent(name.value)}`)
const running = computed(() => info.value?.state === 'running')
const locked = computed(() => !!info.value?.managed)
const composed = computed(() => !!info.value?.project)
const fmt = (u: number) => (u ? datetime(u, locale.value) : '—')

watch(
  () => props.container,
  (c) => {
    if (!c) return stopMonitor()
    name.value = c.name
    info.value = null
    raw.value = ''
    tab.value = (tabs as readonly string[]).includes(props.tab ?? '') ? (props.tab as Tab) : 'status'
    loadInfo().then(() => loadTab(tab.value))
  },
)
watch(tab, (v) => loadTab(v))

async function loadInfo() {
  try {
    info.value = await get<ContainerInfo>(`${base.value}/info`)
  } catch (e) {
    fail(e)
  }
}

function loadTab(v: Tab) {
  if (v !== 'monitor') stopMonitor()
  switch (v) {
    case 'networks': return loadNetworks()
    case 'restart': policy.value = { policy: info.value?.restart ?? 'no', max_retry: info.value?.max_retry ?? 0 }; return
    case 'commit': commit.value = { repository: name.value.toLowerCase().replace(/[^a-z0-9_.\-]/g, '-'), tag: 'latest', message: '' }; return
    case 'edit': return loadSpec()
    case 'upgrade': up.value = { image: info.value?.image ?? '', force_pull: true, force_recreate: false }; return
    case 'rename': newName.value = name.value; return
    case 'monitor': return startMonitor()
    case 'logs': return loadLogs()
    case 'proxy': proxyPort.value = info.value?.host_ports[0] ?? null; return loadSites()
  }
}

function runTask(r: { task_id: string }, label: string, after?: () => void) {
  task.value = { id: r.task_id, title: `${label} · ${name.value}`, after }
}
function taskDone() {
  task.value?.after?.()
  loadInfo()
  emit('changed')
}
async function act(fn: () => Promise<unknown>, after?: () => void) {
  try {
    await fn()
    msg.success(t('common.done'))
    after?.()
    await loadInfo()
    emit('changed')
  } catch (e) {
    fail(e)
  }
}

// ---- status ----
function dur(sec: number) {
  const d = Math.floor(sec / 86400), hh = Math.floor((sec % 86400) / 3600), m = Math.floor((sec % 3600) / 60)
  return d ? `${d}d ${hh}h` : hh ? `${hh}h ${m}m` : `${m}m`
}
const uptime = computed(() => (running.value && info.value?.started_at ? dur(Math.max(0, Date.now() / 1000 - info.value.started_at)) : '—'))
const portText = (p: ContainerInfo['ports'][number]) => (p.host_port ? `${p.host_ip || '0.0.0.0'}:${p.host_port} → ${p.container_port}` : p.container_port)
const ips = computed(() => info.value?.networks.map((n) => n.ip).filter(Boolean).join(', ') || '—')
const ip6s = computed(() => info.value?.networks.map((n) => n.ipv6).filter(Boolean).join(', ') || '—')
const control = (a: string) => act(() => post(`${base.value}/${a}`))

// ---- terminal ----
const shell = ref<'bash' | 'sh'>('bash')
const asRoot = ref(false)
const termKey = ref(0)

// ---- details ----
const raw = ref('')
async function showRaw() {
  try {
    raw.value = (await get<{ inspect: string }>(`${base.value}/inspect`)).inspect
  } catch (e) {
    fail(e)
  }
}
const envRows = computed(() => (info.value?.env ?? []).map((e) => { const i = e.indexOf('='); return { k: i < 0 ? e : e.slice(0, i), v: i < 0 ? '' : e.slice(i + 1) } }))
const envCols: DataTableColumns<{ k: string; v: string }> = [
  { title: () => t('docker.c.key'), key: 'k', width: 220, render: (r) => h('span', { class: 'mono' }, r.k) },
  { title: () => t('docker.c.value'), key: 'v', render: (r) => h('span', { class: 'mono wrap' }, r.v) },
]

// ---- volumes ----
const mountCols: DataTableColumns<ContainerInfo['mounts'][number]> = [
  { title: () => t('docker.c.type'), key: 'type', width: 80 },
  { title: () => t('docker.c.host_path'), key: 'source', render: (m) => h('span', { class: 'mono wrap' }, m.type === 'volume' && m.name ? `${m.name}\n${m.source}` : m.source) },
  { title: () => t('docker.c.container_path'), key: 'destination', render: (m) => h('span', { class: 'mono wrap' }, m.destination) },
  { title: () => t('docker.c.mode'), key: 'rw', width: 70, render: (m) => (m.rw ? 'rw' : 'ro') },
]

// ---- networks ----
const allNets = ref<DockerNetwork[]>([])
const joinNet = ref<string | null>(null)
async function loadNetworks() {
  try {
    allNets.value = (await get<{ items: DockerNetwork[] }>('/docker/networks')).items
  } catch (e) {
    fail(e)
  }
}
const joinable = computed(() => allNets.value.filter((n) => !info.value?.networks.some((x) => x.name === n.name) && n.name !== 'host' && n.name !== 'none').map((n) => ({ label: n.name, value: n.name })))
const netAction = (network: string, action: 'connect' | 'disconnect') =>
  act(() => post(`${base.value}/networks/${action}`, { network }), () => (joinNet.value = null))
const netCols = computed<DataTableColumns<ContainerInfo['networks'][number]>>(() => [
  { title: () => t('docker.name'), key: 'name' },
  { title: 'IPv4', key: 'ip', render: (n) => n.ip || '—' },
  { title: 'IPv6', key: 'ipv6', render: (n) => n.ipv6 || '—' },
  { title: () => t('docker.c.gateway'), key: 'gateway', render: (n) => n.gateway || '—' },
  { title: 'MAC', key: 'mac', render: (n) => h('span', { class: 'mono' }, n.mac || '—') },
  {
    title: '', key: 'a', width: 90,
    render: (n) =>
      h(NPopconfirm, { onPositiveClick: () => netAction(n.name, 'disconnect') }, {
        trigger: () => h(NButton, { size: 'tiny', disabled: locked.value }, () => t('docker.c.disconnect')),
        default: () => t('docker.c.disconnect_confirm', { net: n.name }),
      }),
  },
])

// ---- restart policy ----
const policies = ['no', 'always', 'unless-stopped', 'on-failure']
const policy = ref({ policy: 'no', max_retry: 0 })
const savePolicy = () => act(() => post(`${base.value}/restart-policy`, policy.value))

// ---- commit ----
const commit = ref({ repository: '', tag: 'latest', message: '' })
async function doCommit() {
  try {
    runTask(await post<{ task_id: string }>(`${base.value}/commit`, commit.value), t('docker.c.tabs.commit'))
  } catch (e) {
    fail(e)
  }
}

// ---- edit ----
const edit = ref<{ name: string; image: string; ports: string; env: string; volumes: string[]; restart: string; network: string; command: string; cpus: string; memory: string } | null>(null)
const pull = ref(false)
const lines = (s: string) => s.split('\n').map((x) => x.trim()).filter(Boolean)
async function loadSpec() {
  try {
    const s = await get<ContainerSpec>(`${base.value}/spec`)
    edit.value = { ...s, ports: s.ports.join('\n'), env: s.env.join('\n') }
  } catch (e) {
    fail(e)
  }
}
async function saveEdit() {
  const e = edit.value!
  try {
    const r = await post<{ task_id: string }>(`${base.value}/recreate`, { ...e, ports: lines(e.ports), env: lines(e.env), pull: pull.value })
    runTask(r, t('docker.c.tabs.edit'), () => (name.value = e.name || name.value))
  } catch (err) {
    fail(err)
  }
}

// ---- upgrade ----
const up = ref({ image: '', force_pull: true, force_recreate: false })
async function doUpgrade() {
  try {
    runTask(await post<{ task_id: string }>(`${base.value}/upgrade`, up.value), t('docker.c.tabs.upgrade'))
  } catch (e) {
    fail(e)
  }
}

// ---- rename ----
const newName = ref('')
function doRename() {
  const n = newName.value.trim()
  act(() => post(`${base.value}/rename`, { name: n }), () => (name.value = n))
}

// ---- monitor ----
const samples = ref<{ at: number; u: DockerUsage }[]>([])
let monTimer: number | undefined
async function sample() {
  if (document.visibilityState !== 'visible') return
  try {
    const u = await get<DockerUsage>(`${base.value}/stats`)
    samples.value = [...samples.value.slice(-59), { at: Date.now() / 1000, u }]
  } catch {
    // keep the last samples; the next tick retries
  }
}
function startMonitor() {
  stopMonitor()
  samples.value = []
  sample()
  monTimer = window.setInterval(sample, 3000)
}
function stopMonitor() {
  clearInterval(monTimer)
  monTimer = undefined
}
onBeforeUnmount(stopMonitor)
const rate = (k: keyof DockerUsage) =>
  samples.value.slice(1).map((s, i) => {
    const p = samples.value[i]
    return Math.max(0, ((s.u[k] as number) - (p.u[k] as number)) / Math.max(s.at - p.at, 0.001))
  })
const times = computed(() => samples.value.map((s) => s.at))
const rateTimes = computed(() => times.value.slice(1))
const last = computed(() => samples.value[samples.value.length - 1]?.u)
const fmtPct = (v: number) => `${v.toFixed(1)}%`
const fmtRate = (v: number) => `${bytes(v)}/s`

// ---- logs ----
const range = ref<'3d' | '7d' | 'all' | 'custom'>('3d')
const custom = ref<[number, number] | null>(null)
const logs = ref('')
const logEl = ref<HTMLElement | null>(null)
const logQuery = computed(() => {
  const now = Math.floor(Date.now() / 1000)
  if (range.value === '3d') return `since=${now - 3 * 86400}`
  if (range.value === '7d') return `since=${now - 7 * 86400}`
  if (range.value === 'custom' && custom.value) return `since=${Math.floor(custom.value[0] / 1000)}&until=${Math.floor(custom.value[1] / 1000)}`
  return ''
})
async function loadLogs() {
  try {
    logs.value = (await get<{ logs: string }>(`${base.value}/logs?tail=2000&timestamps=1&${logQuery.value}`)).logs || t('docker.c.no_logs')
    await nextTick()
    logEl.value?.scrollTo({ top: logEl.value.scrollHeight })
  } catch (e) {
    fail(e)
  }
}
watch([range, custom], () => tab.value === 'logs' && (range.value !== 'custom' || custom.value) && loadLogs())
const downloadUrl = computed(() => `/api/v1${base.value}/logs/download?${logQuery.value}`)
const clearLogs = () => act(() => post(`${base.value}/logs/clear`), loadLogs)

// ---- reverse proxy ----
const proxyPort = ref<number | null>(null)
const sites = ref<Site[]>([])
async function loadSites() {
  try {
    sites.value = (await get<{ items: Site[] }>('/sites')).items
  } catch {
    sites.value = []
  }
}
const bound = computed(() =>
  sites.value.filter((s) => s.type === 'proxy' && info.value?.host_ports.some((p) => new RegExp(`^https?://(127\\.0\\.0\\.1|localhost):${p}(/|$)`).test(s.proxy_target))),
)
</script>

<template>
  <n-modal :show="!!container" @update:show="(v: boolean) => !v && emit('close')">
    <n-card v-if="container" style="width: min(1040px, calc(100vw - 32px))" closable @close="emit('close')">
      <template #header>
        <span class="title"><i class="dot" :class="info?.state" />{{ name }}</span>
        <n-tag v-if="info" size="small" :bordered="false" style="margin-left: 8px">{{ info.state }}</n-tag>
        <n-tag v-if="locked" size="small" :bordered="false" style="margin-left: 6px">lmcwhm</n-tag>
        <n-tag v-if="composed" size="small" type="info" :bordered="false" style="margin-left: 6px">{{ info?.project }}</n-tag>
      </template>
      <n-select v-model:value="tab" class="tab-select" :options="tabs.map((k) => ({ label: t(`docker.c.tabs.${k}`), value: k }))" />
      <n-tabs v-model:value="tab" type="line" placement="left" class="cs" animated>
        <n-tab-pane v-for="k in tabs" :key="k" :name="k" :tab="t(`docker.c.tabs.${k}`)">
          <template v-if="!info" />

          <!-- status -->
          <template v-else-if="k === 'status'">
            <n-space style="margin-bottom: 14px">
              <n-button v-if="!running" type="primary" @click="control('start')">{{ t('docker.start') }}</n-button>
              <n-button v-else @click="control('stop')">{{ t('docker.stop') }}</n-button>
              <n-button :disabled="!running" @click="control('restart')">{{ t('docker.restart') }}</n-button>
              <n-button v-if="info.state === 'paused'" @click="control('unpause')">{{ t('docker.unpause') }}</n-button>
              <n-button v-else :disabled="!running" @click="control('pause')">{{ t('docker.pause') }}</n-button>
            </n-space>
            <n-descriptions :column="1" label-placement="left" bordered size="small" label-style="width: 120px">
              <n-descriptions-item :label="t('docker.name')">{{ info.name }}</n-descriptions-item>
              <n-descriptions-item label="ID"><span class="mono">{{ info.id.slice(0, 12) }}</span></n-descriptions-item>
              <n-descriptions-item :label="t('docker.image')"><span class="mono">{{ info.image }}</span></n-descriptions-item>
              <n-descriptions-item :label="t('docker.c.uptime')">{{ uptime }}</n-descriptions-item>
              <n-descriptions-item :label="t('docker.created')">{{ fmt(info.created) }}</n-descriptions-item>
              <n-descriptions-item :label="t('docker.c.started')">{{ fmt(info.started_at) }}</n-descriptions-item>
              <n-descriptions-item :label="t('docker.ports')">
                <div v-if="info.ports.length" class="mono">
                  <div v-for="p in info.ports" :key="portText(p)">{{ portText(p) }}</div>
                </div>
                <span v-else>—</span>
              </n-descriptions-item>
              <n-descriptions-item label="IP"><span class="mono">{{ ips }}</span></n-descriptions-item>
              <n-descriptions-item label="IPv6"><span class="mono">{{ ip6s }}</span></n-descriptions-item>
            </n-descriptions>
          </template>

          <!-- terminal -->
          <template v-else-if="k === 'terminal'">
            <n-alert v-if="!running" type="warning" :show-icon="false">{{ t('docker.c.need_running') }}</n-alert>
            <template v-else>
              <n-space align="center" style="margin-bottom: 10px">
                <span>{{ t('docker.c.shell') }}</span>
                <n-radio-group v-model:value="shell" size="small" @update:value="termKey++">
                  <n-radio-button value="bash">bash</n-radio-button>
                  <n-radio-button value="sh">sh</n-radio-button>
                </n-radio-group>
                <n-switch v-model:value="asRoot" size="small" @update:value="termKey++" />
                <span>{{ t('docker.c.as_root') }}</span>
              </n-space>
              <div style="height: 440px"><TerminalView :key="`${name}-${termKey}`" :container="name" :shell="shell" :root="asRoot" /></div>
            </template>
          </template>

          <!-- details -->
          <template v-else-if="k === 'details'">
            <n-descriptions :column="1" label-placement="left" bordered size="small" label-style="width: 120px">
              <n-descriptions-item :label="t('docker.image')"><span class="mono">{{ info.image }}</span></n-descriptions-item>
              <n-descriptions-item :label="t('docker.c.image_id')"><span class="mono wrap">{{ info.image_id }}</span></n-descriptions-item>
              <n-descriptions-item :label="t('docker.c.digest')">
                <span class="mono wrap">{{ info.digests.join('\n') || '—' }}</span>
              </n-descriptions-item>
              <n-descriptions-item :label="t('docker.ports')">
                <span class="mono">{{ info.ports.map(portText).join(', ') || '—' }}</span>
              </n-descriptions-item>
              <n-descriptions-item label="CMD"><span class="mono wrap">{{ info.cmd.length ? JSON.stringify(info.cmd) : '—' }}</span></n-descriptions-item>
              <n-descriptions-item label="ENTRYPOINT"><span class="mono wrap">{{ info.entrypoint.length ? JSON.stringify(info.entrypoint) : '—' }}</span></n-descriptions-item>
              <n-descriptions-item :label="t('docker.c.workdir')"><span class="mono">{{ info.working_dir || '—' }}</span></n-descriptions-item>
              <n-descriptions-item :label="t('docker.c.user')"><span class="mono">{{ info.user || 'root' }}</span></n-descriptions-item>
            </n-descriptions>
            <h4 class="sub">{{ t('docker.env') }}</h4>
            <n-data-table size="small" :columns="envCols" :data="envRows" :max-height="260" />
            <n-space style="margin-top: 14px">
              <n-button size="small" @click="raw ? (raw = '') : showRaw()">{{ raw ? t('docker.c.hide_raw') : t('docker.c.raw') }}</n-button>
            </n-space>
            <div v-if="raw" class="raw"><n-code :code="raw" language="json" /></div>
          </template>

          <!-- volumes -->
          <template v-else-if="k === 'volumes'">
            <n-data-table size="small" :columns="mountCols" :data="info.mounts" />
            <p class="hint">{{ t('docker.c.volumes_hint') }}</p>
          </template>

          <!-- networks -->
          <template v-else-if="k === 'networks'">
            <n-alert v-if="locked" type="info" :show-icon="false" style="margin-bottom: 12px">{{ t('docker.c.managed') }}</n-alert>
            <n-data-table size="small" :columns="netCols" :data="info.networks" />
            <n-space align="center" style="margin-top: 14px">
              <n-select v-model:value="joinNet" :options="joinable" :placeholder="t('docker.c.pick_network')" style="width: 220px" size="small" />
              <n-button size="small" type="primary" :disabled="!joinNet || locked" @click="netAction(joinNet!, 'connect')">{{ t('docker.c.connect') }}</n-button>
            </n-space>
          </template>

          <!-- restart policy -->
          <template v-else-if="k === 'restart'">
            <n-alert v-if="locked" type="info" :show-icon="false" style="margin-bottom: 12px">{{ t('docker.c.managed') }}</n-alert>
            <n-radio-group v-model:value="policy.policy">
              <n-space vertical>
                <n-radio v-for="p in policies" :key="p" :value="p">
                  <b class="mono">{{ p }}</b> <span class="hint inline">{{ t(`docker.c.policy.${p}`) }}</span>
                </n-radio>
              </n-space>
            </n-radio-group>
            <n-space v-if="policy.policy === 'on-failure'" align="center" style="margin-top: 12px">
              <span>{{ t('docker.c.max_retry') }}</span>
              <n-input-number v-model:value="policy.max_retry" :min="0" :max="1000" size="small" style="width: 120px" />
            </n-space>
            <n-space style="margin-top: 16px"><n-button type="primary" :disabled="locked" @click="savePolicy">{{ t('common.save') }}</n-button></n-space>
          </template>

          <!-- commit -->
          <template v-else-if="k === 'commit'">
            <p class="hint top">{{ t('docker.c.commit_hint') }}</p>
            <n-form label-placement="left" label-width="100" style="max-width: 520px">
              <n-form-item :label="t('docker.repository')"><n-input v-model:value="commit.repository" placeholder="myapp" /></n-form-item>
              <n-form-item :label="t('docker.c.tag')"><n-input v-model:value="commit.tag" placeholder="latest" /></n-form-item>
              <n-form-item :label="t('docker.c.message')"><n-input v-model:value="commit.message" :placeholder="t('docker.optional')" /></n-form-item>
            </n-form>
            <n-button type="primary" :disabled="!commit.repository" @click="doCommit">{{ t('docker.c.commit') }}</n-button>
          </template>

          <!-- edit -->
          <template v-else-if="k === 'edit'">
            <n-alert v-if="locked || composed" type="info" :show-icon="false" style="margin-bottom: 12px">
              {{ locked ? t('docker.c.managed') : t('docker.c.compose_owned') }}
            </n-alert>
            <template v-if="edit">
              <n-alert type="warning" :show-icon="false" style="margin-bottom: 12px">{{ t('docker.c.edit_hint') }}</n-alert>
              <n-form label-placement="top" :disabled="locked || composed">
                <n-grid cols="1 s:2" responsive="screen" :x-gap="12">
                  <n-gi><n-form-item :label="t('docker.name')"><n-input v-model:value="edit.name" /></n-form-item></n-gi>
                  <n-gi><n-form-item :label="t('docker.image')"><n-input v-model:value="edit.image" /></n-form-item></n-gi>
                  <n-gi>
                    <n-form-item :label="t('docker.ports')" :feedback="t('docker.ports_hint')">
                      <n-input v-model:value="edit.ports" type="textarea" :rows="3" class="mono" />
                    </n-form-item>
                  </n-gi>
                  <n-gi span="1 s:2">
                    <n-form-item :label="t('docker.volumes')" :feedback="t('docker.volumes_hint')">
                      <DockerVolumes v-model:value="edit.volumes" />
                    </n-form-item>
                  </n-gi>
                  <n-gi>
                    <n-form-item :label="t('docker.env')" :feedback="t('docker.env_hint')">
                      <n-input v-model:value="edit.env" type="textarea" :rows="3" class="mono" />
                    </n-form-item>
                  </n-gi>
                  <n-gi>
                    <n-form-item :label="t('docker.restart_policy')">
                      <n-select v-model:value="edit.restart" :options="policies.map((p) => ({ label: p, value: p }))" />
                    </n-form-item>
                    <n-form-item :label="t('docker.network')"><n-input v-model:value="edit.network" placeholder="bridge" /></n-form-item>
                  </n-gi>
                  <n-gi><n-form-item label="CPU"><n-input v-model:value="edit.cpus" :placeholder="t('docker.c.unlimited')" /></n-form-item></n-gi>
                  <n-gi><n-form-item :label="t('docker.memory')"><n-input v-model:value="edit.memory" :placeholder="t('docker.c.unlimited')" /></n-form-item></n-gi>
                </n-grid>
                <n-form-item :label="t('docker.command')"><n-input v-model:value="edit.command" :placeholder="t('docker.c.image_default')" /></n-form-item>
              </n-form>
              <n-space align="center">
                <n-button type="primary" :disabled="locked || composed || !edit.image" @click="saveEdit">{{ t('docker.c.recreate') }}</n-button>
                <n-checkbox v-model:checked="pull">{{ t('docker.c.pull_first') }}</n-checkbox>
              </n-space>
            </template>
          </template>

          <!-- upgrade -->
          <template v-else-if="k === 'upgrade'">
            <n-alert v-if="locked || composed" type="info" :show-icon="false" style="margin-bottom: 12px">
              {{ locked ? t('docker.c.managed') : t('docker.c.compose_owned') }}
            </n-alert>
            <p class="hint top">{{ t('docker.c.upgrade_hint') }}</p>
            <n-form label-placement="left" label-width="100" style="max-width: 560px">
              <n-form-item :label="t('docker.c.current')"><span class="mono">{{ info.image }}</span></n-form-item>
              <n-form-item :label="t('docker.c.new_image')"><n-input v-model:value="up.image" class="mono" :placeholder="info.image" /></n-form-item>
              <n-form-item label=" ">
                <n-space vertical>
                  <n-checkbox v-model:checked="up.force_pull">{{ t('docker.c.force_pull') }}</n-checkbox>
                  <n-checkbox v-model:checked="up.force_recreate">{{ t('docker.c.force_recreate') }}</n-checkbox>
                </n-space>
              </n-form-item>
            </n-form>
            <n-button type="primary" :disabled="locked || composed" @click="doUpgrade">{{ t('docker.c.upgrade') }}</n-button>
          </template>

          <!-- rename -->
          <template v-else-if="k === 'rename'">
            <n-alert v-if="locked" type="info" :show-icon="false" style="margin-bottom: 12px">{{ t('docker.c.managed') }}</n-alert>
            <n-space align="center">
              <n-input v-model:value="newName" style="width: 280px" :disabled="locked" @keyup.enter="doRename" />
              <n-button type="primary" :disabled="locked || !newName.trim() || newName.trim() === name" @click="doRename">{{ t('common.save') }}</n-button>
            </n-space>
            <p class="hint">{{ t('docker.c.rename_hint') }}</p>
          </template>

          <!-- monitor -->
          <template v-else-if="k === 'monitor'">
            <n-alert v-if="!running" type="warning" :show-icon="false">{{ t('docker.c.need_running') }}</n-alert>
            <div v-else class="charts">
              <div>
                <h4 class="sub">CPU <span class="now">{{ last ? fmtPct(last.cpu) : '' }}</span></h4>
                <LineChart :times="times" :series="[{ name: 'CPU', values: samples.map((s) => s.u.cpu) }]" :format="fmtPct" :height="150" />
              </div>
              <div>
                <h4 class="sub">{{ t('docker.memory') }} <span class="now">{{ last ? `${bytes(last.mem_used)} / ${bytes(last.mem_limit)}` : '' }}</span></h4>
                <LineChart :times="times" :series="[{ name: t('docker.memory'), values: samples.map((s) => s.u.mem_used) }]" :format="bytes" :height="150" />
              </div>
              <div>
                <h4 class="sub">{{ t('docker.c.disk_io') }}</h4>
                <LineChart :times="rateTimes" :series="[{ name: t('docker.c.read'), values: rate('blk_read') }, { name: t('docker.c.write'), values: rate('blk_write') }]" :format="fmtRate" :height="150" />
              </div>
              <div>
                <h4 class="sub">{{ t('docker.net_io') }}</h4>
                <LineChart :times="rateTimes" :series="[{ name: t('docker.c.rx'), values: rate('net_rx') }, { name: t('docker.c.tx'), values: rate('net_tx') }]" :format="fmtRate" :height="150" />
              </div>
            </div>
          </template>

          <!-- logs -->
          <template v-else-if="k === 'logs'">
            <n-space align="center" justify="space-between" style="margin-bottom: 10px">
              <n-space align="center">
                <n-radio-group v-model:value="range" size="small">
                  <n-radio-button value="3d">{{ t('docker.c.last_days', { n: 3 }) }}</n-radio-button>
                  <n-radio-button value="7d">{{ t('docker.c.last_days', { n: 7 }) }}</n-radio-button>
                  <n-radio-button value="all">{{ t('docker.c.all') }}</n-radio-button>
                  <n-radio-button value="custom">{{ t('docker.c.custom') }}</n-radio-button>
                </n-radio-group>
                <n-date-picker v-if="range === 'custom'" v-model:value="custom" type="datetimerange" size="small" clearable />
              </n-space>
              <n-space>
                <n-button size="small" @click="loadLogs">{{ t('common.refresh') }}</n-button>
                <n-button size="small" tag="a" :href="downloadUrl">{{ t('docker.c.download') }}</n-button>
                <n-popconfirm @positive-click="clearLogs">
                  <template #trigger><n-button size="small" :disabled="!info.has_log_file">{{ t('docker.c.clear') }}</n-button></template>
                  {{ t('docker.c.clear_confirm') }}
                </n-popconfirm>
              </n-space>
            </n-space>
            <pre :ref="(el) => (logEl = el as HTMLElement | null)" class="log">{{ logs }}</pre>
            <p class="hint">{{ t('docker.c.logs_hint') }}</p>
          </template>

          <!-- reverse proxy -->
          <template v-else-if="k === 'proxy'">
            <n-alert v-if="!info.host_ports.length" type="warning" :show-icon="false">{{ t('docker.c.no_ports') }}</n-alert>
            <template v-else>
              <p class="hint top">{{ t('docker.c.proxy_hint') }}</p>
              <n-space align="center">
                <span>{{ t('docker.c.port') }}</span>
                <n-select v-model:value="proxyPort" :options="info.host_ports.map((p) => ({ label: `127.0.0.1:${p}`, value: p }))" size="small" style="width: 180px" />
                <n-button type="primary" size="small" :disabled="!proxyPort" @click="emit('bind', proxyPort!, name)">{{ t('docker.bind_domain') }}</n-button>
              </n-space>
              <h4 class="sub">{{ t('docker.c.bound') }}</h4>
              <div v-if="bound.length" class="bound">
                <div v-for="s in bound" :key="s.id"><a :href="`https://${s.domain}`" target="_blank" rel="noopener">{{ s.domain }}</a> <span class="hint inline">→ {{ s.proxy_target }}</span></div>
              </div>
              <p v-else class="hint">{{ t('docker.c.none_bound') }}</p>
            </template>
          </template>
        </n-tab-pane>
      </n-tabs>
    </n-card>
  </n-modal>
  <TaskModal :task-id="task?.id ?? null" :title="task?.title ?? ''" @close="task = null" @done="taskDone" />
</template>

<style scoped>
.title {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}
.dot {
  width: 9px;
  height: 9px;
  border-radius: 50%;
  background: #8a8f98;
}
.dot.running {
  background: #18a058;
}
.dot.paused,
.dot.restarting {
  background: #f0a020;
}
.cs :deep(.n-tab-pane) {
  padding-left: 18px;
  min-height: 480px;
  max-height: calc(100vh - 220px);
  overflow: auto;
  min-width: 0;
}
.mono,
:deep(.mono),
:deep(.mono textarea) {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12.5px;
}
.wrap,
:deep(.wrap) {
  white-space: pre-wrap;
  word-break: break-all;
}
.hint {
  font-size: 12px;
  opacity: 0.65;
  margin: 10px 0 0;
}
.hint.top {
  margin: 0 0 14px;
}
.hint.inline {
  margin: 0 0 0 6px;
}
.sub {
  font-size: 13px;
  margin: 18px 0 8px;
}
.sub .now {
  font-weight: 400;
  opacity: 0.65;
  margin-left: 6px;
}
.raw {
  margin-top: 10px;
  max-height: 360px;
  overflow: auto;
  border: 1px solid rgba(128, 128, 128, 0.2);
  border-radius: 6px;
  padding: 8px;
}
.tab-select {
  display: none;
  margin-bottom: 12px;
}
.charts {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(320px, 1fr));
  gap: 4px 18px;
}
.charts .sub {
  margin-top: 6px;
}
.log {
  background: #111;
  color: #ddd;
  padding: 10px 12px;
  border-radius: 6px;
  height: 400px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-all;
  font: 12px/1.5 ui-monospace, SFMono-Regular, Menlo, monospace;
  margin: 0;
}
.bound {
  display: grid;
  gap: 6px;
}
@media (max-width: 720px) {
  .cs :deep(.n-tabs-nav) {
    display: none;
  }
  .cs :deep(.n-tab-pane) {
    padding-left: 0;
  }
  .tab-select {
    display: block;
  }
}
</style>
