<script setup lang="ts">
import { computed, h, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NButton, NDataTable, NSpace, NTag, NInput, NSwitch, NModal, NCard, NForm, NFormItem, NSelect, NDropdown, NCode, NGrid, NGi,
  useMessage, type DataTableColumns,
} from 'naive-ui'
import { get, post, ApiError } from '../api/client'
import type { Container } from '../api/types'
import LogsModal from './LogsModal.vue'
import TerminalView from './TerminalView.vue'

const emit = defineEmits<{ task: [id: string, title: string]; bind: [port: number, name: string] }>()
const { t } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))

const items = ref<Container[]>([])
const loading = ref(false)
const stats = ref(false)
const search = ref('')
const logs = ref<{ url: string; title: string } | null>(null)
const inspect = ref<{ name: string; body: string } | null>(null)
const exec = ref<Container | null>(null)
const showCreate = ref(false)
let timer: number | undefined

async function load(quiet = false) {
  if (!quiet) loading.value = true
  try {
    items.value = (await get<{ items: Container[] }>(`/docker/containers${stats.value ? '?stats=1' : ''}`)).items
  } catch (e) {
    if (!quiet) fail(e)
  } finally {
    loading.value = false
  }
}
onMounted(() => load())
onBeforeUnmount(() => clearInterval(timer))
function toggleStats(v: boolean) {
  stats.value = v
  clearInterval(timer)
  load()
  if (v) timer = window.setInterval(() => load(true), 5000)
}
defineExpose({ load })

async function action(c: Container, a: string) {
  try {
    await post(`/docker/containers/${c.id}/${a}`)
    msg.success(t('common.done'))
    load()
  } catch (e) {
    fail(e)
  }
}

async function openInspect(c: Container) {
  try {
    inspect.value = { name: c.name, body: (await get<{ inspect: string }>(`/docker/containers/${c.id}/inspect`)).inspect }
  } catch (e) {
    fail(e)
  }
}

const filtered = computed(() => {
  const q = search.value.trim().toLowerCase()
  return q ? items.value.filter((c) => [c.name, c.image, c.project].some((x) => x.toLowerCase().includes(q))) : items.value
})

const stateType = (s: string) => (s === 'running' ? 'success' : s === 'paused' ? 'warning' : s === 'restarting' ? 'error' : 'default')

function menu(c: Container) {
  const running = c.state === 'running'
  return [
    { label: t('docker.restart'), key: 'restart', disabled: !running },
    { label: c.state === 'paused' ? t('docker.unpause') : t('docker.pause'), key: c.state === 'paused' ? 'unpause' : 'pause', disabled: !running && c.state !== 'paused' },
    { label: t('docker.inspect'), key: 'inspect' },
    { label: t('docker.bind_domain'), key: 'bind', disabled: !c.host_ports.length },
    { type: 'divider', key: 'd' },
    { label: t('common.delete'), key: 'remove', disabled: c.managed },
  ]
}
function onMenu(c: Container, key: string) {
  if (key === 'inspect') return openInspect(c)
  if (key === 'bind') return emit('bind', c.host_ports[0], c.name)
  if (key === 'remove' && !confirm(t('docker.remove_confirm', { name: c.name }))) return
  action(c, key)
}

const columns = computed<DataTableColumns<Container>>(() => [
  {
    title: () => t('docker.name'),
    key: 'name',
    render: (c) =>
      h('div', [
        h('b', c.name),
        c.managed ? h(NTag, { size: 'tiny', style: 'margin-left:6px', bordered: false }, () => 'lmcwhm') : null,
        c.project ? h(NTag, { size: 'tiny', style: 'margin-left:6px', type: 'info', bordered: false }, () => c.project) : null,
        h('div', { style: 'font-size:12px;opacity:.6' }, c.image),
      ]),
  },
  {
    title: () => t('common.status'),
    key: 'state',
    width: 170,
    render: (c) => h('div', [h(NTag, { size: 'small', type: stateType(c.state) }, () => c.state), h('div', { style: 'font-size:12px;opacity:.6' }, c.status)]),
  },
  ...(stats.value
    ? [
        { title: 'CPU', key: 'cpu', width: 80 },
        { title: () => t('docker.memory'), key: 'mem', width: 170, render: (c: Container) => (c.mem ? `${c.mem} (${c.mem_perc})` : '') },
        { title: () => t('docker.net_io'), key: 'net_io', width: 150 },
      ]
    : []),
  { title: () => t('docker.ports'), key: 'ports', ellipsis: { tooltip: true } },
  {
    title: '',
    key: 'actions',
    width: 250,
    render: (c) =>
      h(NSpace, { size: 6 }, () => [
        c.state === 'running'
          ? h(NButton, { size: 'tiny', onClick: () => action(c, 'stop') }, () => t('docker.stop'))
          : h(NButton, { size: 'tiny', type: 'primary', onClick: () => action(c, 'start') }, () => t('docker.start')),
        h(NButton, { size: 'tiny', onClick: () => (logs.value = { url: `/docker/containers/${c.id}/logs`, title: c.name }) }, () => t('app.logs')),
        h(NButton, { size: 'tiny', disabled: c.state !== 'running', onClick: () => (exec.value = c) }, () => t('docker.terminal')),
        h(NDropdown, { options: menu(c), trigger: 'click', onSelect: (k: string) => onMenu(c, k) }, () => h(NButton, { size: 'tiny' }, () => '⋯')),
      ]),
  },
])

// ---- create ----
const blank = () => ({ name: '', image: '', ports: '', env: '', volumes: '', restart: 'unless-stopped', network: '', command: '', cpus: '', memory: '' })
const f = ref(blank())
const lines = (s: string) => s.split('\n').map((x) => x.trim()).filter(Boolean)
const restartOptions = ['no', 'always', 'unless-stopped', 'on-failure'].map((v) => ({ label: v, value: v }))

async function create() {
  try {
    const r = await post<{ task_id: string }>('/docker/containers', {
      ...f.value,
      ports: lines(f.value.ports),
      env: lines(f.value.env),
      volumes: lines(f.value.volumes),
    })
    showCreate.value = false
    emit('task', r.task_id, `${t('docker.create')} · ${f.value.image}`)
    f.value = blank()
  } catch (e) {
    fail(e)
  }
}
</script>

<template>
  <n-space justify="space-between" style="margin-bottom: 12px">
    <n-space align="center">
      <n-button type="primary" @click="showCreate = true">{{ t('docker.create') }}</n-button>
      <n-switch :value="stats" size="small" @update:value="toggleStats" />
      <span>{{ t('docker.live_stats') }}</span>
    </n-space>
    <n-space>
      <n-input v-model:value="search" size="small" clearable :placeholder="t('docker.search')" style="width: 200px" />
      <n-button size="small" @click="load()">{{ t('common.refresh') }}</n-button>
    </n-space>
  </n-space>
  <n-data-table size="small" :columns="columns" :data="filtered" :loading="loading" :row-key="(r: Container) => r.id" :scroll-x="stats ? 1300 : 900" />

  <LogsModal :url="logs?.url ?? null" :title="logs?.title ?? ''" @close="logs = null" />

  <n-modal :show="!!inspect" @update:show="(v: boolean) => !v && (inspect = null)">
    <n-card v-if="inspect" :title="inspect.name" style="width: min(900px, calc(100vw - 32px))" closable @close="inspect = null">
      <div style="max-height: 65vh; overflow: auto"><n-code :code="inspect.body" language="json" /></div>
    </n-card>
  </n-modal>

  <n-modal :show="!!exec" @update:show="(v: boolean) => !v && (exec = null)">
    <n-card v-if="exec" :title="`${t('docker.terminal')} · ${exec.name}`" style="width: min(1000px, calc(100vw - 32px))" closable @close="exec = null">
      <div style="height: 60vh"><TerminalView :container="exec.id" /></div>
    </n-card>
  </n-modal>

  <n-modal :show="showCreate" @update:show="(v: boolean) => (showCreate = v)">
    <n-card :title="t('docker.create')" style="width: min(720px, calc(100vw - 32px))" closable @close="showCreate = false">
      <n-form label-placement="top">
        <n-grid cols="1 s:2" responsive="screen" :x-gap="12">
          <n-gi><n-form-item :label="t('docker.image')"><n-input v-model:value="f.image" placeholder="nginx:latest" /></n-form-item></n-gi>
          <n-gi><n-form-item :label="t('docker.name')"><n-input v-model:value="f.name" :placeholder="t('docker.optional')" /></n-form-item></n-gi>
          <n-gi>
            <n-form-item :label="t('docker.ports')" :feedback="t('docker.ports_hint')">
              <n-input v-model:value="f.ports" type="textarea" :rows="3" placeholder="127.0.0.1:8081:80" />
            </n-form-item>
          </n-gi>
          <n-gi>
            <n-form-item :label="t('docker.volumes')" :feedback="t('docker.volumes_hint')">
              <n-input v-model:value="f.volumes" type="textarea" :rows="3" placeholder="/www/data:/data" />
            </n-form-item>
          </n-gi>
          <n-gi>
            <n-form-item :label="t('docker.env')" :feedback="t('docker.env_hint')">
              <n-input v-model:value="f.env" type="textarea" :rows="3" placeholder="TZ=Asia/Hong_Kong" />
            </n-form-item>
          </n-gi>
          <n-gi>
            <n-form-item :label="t('docker.restart_policy')"><n-select v-model:value="f.restart" :options="restartOptions" /></n-form-item>
            <n-form-item :label="t('docker.network')"><n-input v-model:value="f.network" placeholder="bridge" /></n-form-item>
          </n-gi>
          <n-gi><n-form-item label="CPU"><n-input v-model:value="f.cpus" placeholder="1.5" /></n-form-item></n-gi>
          <n-gi><n-form-item :label="t('docker.memory')"><n-input v-model:value="f.memory" placeholder="512m" /></n-form-item></n-gi>
        </n-grid>
        <n-form-item :label="t('docker.command')"><n-input v-model:value="f.command" :placeholder="t('docker.optional')" /></n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showCreate = false">{{ t('common.cancel') }}</n-button>
          <n-button type="primary" :disabled="!f.image" @click="create">{{ t('common.confirm') }}</n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>
</template>
