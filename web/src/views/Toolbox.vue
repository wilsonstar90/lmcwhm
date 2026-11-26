<script setup lang="ts">
// 工具箱: processes, connections, disks, cleanup.
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import {
  NButton, NCard, NCheckbox, NDataTable, NGi, NGrid, NInput, NPopconfirm, NProgress, NSpace, NTabPane, NTabs, useMessage, type DataTableColumns,
} from 'naive-ui'
import { get, post, ApiError } from '../api/client'
import { bytes, datetime } from '../format'

interface Proc { pid: number; ppid: number; name: string; user: string; cpu: number; mem: number; rss: number; status: string; started: number; cmd: string; threads: number }
interface Conn { proto: string; local: string; remote: string; status: string; pid: number; name: string }
interface Mount { device: string; mountpoint: string; fstype: string; total: number; used: number; free: number; percent: number; inodes_percent: number }
interface Junk { key: string; size: number; count: number }

const { t, locale } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))
const tab = ref('processes')
async function act(p: Promise<unknown>, reload: () => void) {
  try {
    await p
    msg.success(t('common.saved'))
    reload()
  } catch (e) {
    fail(e)
  }
}

// ---- processes / connections ----
const procs = ref<Proc[]>([])
const procQ = ref('')
async function loadProcs() {
  try {
    procs.value = (await get<{ items: Proc[] }>('/toolbox/processes')).items
  } catch (e) {
    fail(e)
  }
}
const procRows = computed(() => {
  const q = procQ.value.trim().toLowerCase()
  return q ? procs.value.filter((p) => p.name.toLowerCase().includes(q) || p.cmd.toLowerCase().includes(q) || String(p.pid) === q || p.user === q) : procs.value
})
async function kill(p: Proc, force: boolean) {
  await act(post(`/toolbox/processes/${p.pid}/kill`, { force }), loadProcs)
}
const procCols = computed<DataTableColumns<Proc>>(() => [
  { title: 'PID', key: 'pid', width: 80, sorter: (a, b) => a.pid - b.pid },
  { title: () => t('tb.name'), key: 'name', width: 160, ellipsis: { tooltip: true } },
  { title: () => t('tb.user'), key: 'user', width: 100 },
  { title: 'CPU %', key: 'cpu', width: 90, sorter: (a, b) => a.cpu - b.cpu, render: (p) => p.cpu.toFixed(1) },
  { title: () => t('tb.memory'), key: 'rss', width: 100, sorter: (a, b) => a.rss - b.rss, render: (p) => bytes(p.rss) },
  { title: () => t('tb.started'), key: 'started', width: 160, render: (p) => datetime(p.started, locale.value) },
  { title: () => t('tb.command'), key: 'cmd', ellipsis: { tooltip: true } },
  {
    title: '',
    key: 'a',
    width: 140,
    render: (p) =>
      h(NSpace, { size: 4 }, () => [
        h(NPopconfirm, { onPositiveClick: () => kill(p, false) }, { trigger: () => h(NButton, { size: 'tiny' }, () => t('tb.terminate')), default: () => t('tb.kill_confirm', { name: p.name }) }),
        h(NPopconfirm, { onPositiveClick: () => kill(p, true) }, { trigger: () => h(NButton, { size: 'tiny', type: 'error', quaternary: true }, () => t('tb.kill')), default: () => t('tb.kill_confirm', { name: p.name }) }),
      ]),
  },
])
const conns = ref<Conn[]>([])
const listenOnly = ref(true)
async function loadConns() {
  try {
    conns.value = (await get<{ items: Conn[] }>('/toolbox/connections')).items
  } catch (e) {
    fail(e)
  }
}
const connRows = computed(() => (listenOnly.value ? conns.value.filter((c) => c.status === 'LISTEN' || (c.proto.startsWith('udp') && !c.remote)) : conns.value))
const connCols: DataTableColumns<Conn> = [
  { title: () => t('tb.proto'), key: 'proto', width: 80 },
  { title: () => t('tb.local'), key: 'local', width: 220 },
  { title: () => t('tb.remote'), key: 'remote', width: 220 },
  { title: () => t('common.status'), key: 'status', width: 120 },
  { title: 'PID', key: 'pid', width: 80 },
  { title: () => t('tb.name'), key: 'name' },
]

// ---- disks ----
const mounts = ref<Mount[]>([])
const duPath = ref('/')
const du = ref<{ path: string; size: number }[]>([])
const duLoading = ref(false)
async function loadDisks() {
  try {
    mounts.value = (await get<{ items: Mount[] }>('/toolbox/disks')).items
  } catch (e) {
    fail(e)
  }
}
async function loadDu(p = duPath.value) {
  duPath.value = p
  duLoading.value = true
  try {
    du.value = (await get<{ items: { path: string; size: number }[] }>(`/toolbox/du?path=${encodeURIComponent(p)}`)).items ?? []
  } catch (e) {
    fail(e)
  } finally {
    duLoading.value = false
  }
}
const duMax = computed(() => Math.max(1, ...du.value.map((d) => d.size)))
const parent = (p: string) => p.replace(/\/[^/]+\/?$/, '') || '/'

// ---- cleanup ----
const junk = ref<Junk[]>([])
const junkSel = ref<string[]>([])
const junkLoading = ref(false)
async function loadJunk() {
  junkLoading.value = true
  try {
    junk.value = (await get<{ items: Junk[] }>('/toolbox/junk')).items
    junkSel.value = junk.value.filter((j) => j.size > 0 && j.key !== 'docker').map((j) => j.key)
  } catch (e) {
    fail(e)
  } finally {
    junkLoading.value = false
  }
}
async function clean() {
  try {
    const r = await post<{ freed: number }>('/toolbox/junk/clean', { keys: junkSel.value })
    msg.success(t('tb.freed', { n: bytes(r.freed) }))
    loadJunk()
  } catch (e) {
    fail(e)
  }
}
const junkTotal = computed(() => junk.value.filter((j) => junkSel.value.includes(j.key)).reduce((a, j) => a + j.size, 0))

const loaders: Record<string, () => void> = { processes: loadProcs, connections: loadConns, disks: () => (loadDisks(), loadDu('/')), cleanup: loadJunk }
const loaded = new Set<string>()
function onTab(v: string) {
  tab.value = v
  if (!loaded.has(v)) {
    loaded.add(v)
    loaders[v]?.()
  }
}
// ?tab=cleanup comes from the dashboard's disk card (立即清理).
const route = useRoute()
const router = useRouter()
onMounted(() => {
  const q = route.query.tab
  if (q === 'system' || q === 'ssh') {
    router.replace({ name: 'settings', query: { tab: 'system' } })
    return
  }
  onTab(typeof q === 'string' && q in loaders ? q : 'processes')
})
</script>

<template>
  <n-tabs :value="tab" type="line" animated @update:value="onTab">
    <n-tab-pane name="processes" :tab="t('tb.processes')">
      <n-space style="margin-bottom: 12px"><n-input v-model:value="procQ" :placeholder="t('tb.search')" clearable style="width: 260px" /><n-button @click="loadProcs">{{ t('common.refresh') }}</n-button></n-space>
      <n-data-table size="small" :columns="procCols" :data="procRows" :row-key="(p: Proc) => p.pid" :max-height="600" virtual-scroll :scroll-x="1100" />
    </n-tab-pane>

    <n-tab-pane name="connections" :tab="t('tb.connections')">
      <n-space align="center" style="margin-bottom: 12px">
        <n-checkbox v-model:checked="listenOnly">{{ t('tb.listen_only') }}</n-checkbox>
        <n-button @click="loadConns">{{ t('common.refresh') }}</n-button>
        <span class="hint">{{ t('tb.conn_count', { n: conns.length }) }}</span>
      </n-space>
      <n-data-table size="small" :columns="connCols" :data="connRows" :max-height="600" virtual-scroll :scroll-x="900" />
    </n-tab-pane>

    <n-tab-pane name="disks" :tab="t('tb.disks')">
      <n-grid cols="1 m:2 l:3" responsive="screen" :x-gap="12" :y-gap="12" style="margin-bottom: 16px">
        <n-gi v-for="m in mounts" :key="m.mountpoint">
          <n-card size="small">
            <b>{{ m.mountpoint }}</b> <span class="hint">{{ m.device }} · {{ m.fstype }}</span>
            <n-progress type="line" :percentage="Math.round(m.percent)" :status="m.percent > 90 ? 'error' : m.percent > 75 ? 'warning' : 'success'" style="margin: 8px 0" />
            <span class="hint">{{ bytes(m.used) }} / {{ bytes(m.total) }} · {{ t('tb.free') }} {{ bytes(m.free) }} · inode {{ m.inodes_percent.toFixed(0) }}%</span>
          </n-card>
        </n-gi>
      </n-grid>
      <n-card size="small" :title="t('tb.usage')">
        <n-space align="center" style="margin-bottom: 8px">
          <n-input v-model:value="duPath" style="width: 280px" @keyup.enter="loadDu()" />
          <n-button :loading="duLoading" @click="loadDu()">{{ t('tb.analyze') }}</n-button>
          <n-button v-if="duPath !== '/'" quaternary @click="loadDu(parent(duPath))">↑ {{ t('tb.up') }}</n-button>
        </n-space>
        <div v-for="d in du" :key="d.path" class="du" @click="loadDu(d.path)">
          <span class="du-path">{{ d.path }}</span>
          <span class="du-bar"><span :style="{ width: `${(d.size / duMax) * 100}%` }" /></span>
          <span class="du-size">{{ bytes(d.size) }}</span>
        </div>
      </n-card>
    </n-tab-pane>

    <n-tab-pane name="cleanup" :tab="t('tb.cleanup')">
      <n-card size="small">
        <div v-for="j in junk" :key="j.key" class="junk">
          <n-checkbox :checked="junkSel.includes(j.key)" @update:checked="(v: boolean) => (junkSel = v ? [...junkSel, j.key] : junkSel.filter((k) => k !== j.key))">
            <b>{{ t(`tb.junk.${j.key}`) }}</b>
            <div class="hint">{{ t(`tb.junk_hint.${j.key}`) }}</div>
          </n-checkbox>
          <span>{{ bytes(j.size) }}<span v-if="j.count" class="hint"> · {{ j.count }}</span></span>
        </div>
        <n-space style="margin-top: 12px">
          <n-button :loading="junkLoading" @click="loadJunk">{{ t('tb.rescan') }}</n-button>
          <n-popconfirm @positive-click="clean">
            <template #trigger><n-button type="primary" :disabled="!junkSel.length">{{ t('tb.clean', { n: bytes(junkTotal) }) }}</n-button></template>
            {{ t('tb.clean_confirm') }}
          </n-popconfirm>
        </n-space>
      </n-card>
    </n-tab-pane>
  </n-tabs>
</template>

<style scoped>
.hint {
  font-size: 12px;
  opacity: 0.65;
}
.junk {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
  padding: 6px 0;
  border-bottom: 1px solid rgba(128, 128, 128, 0.15);
  word-break: break-all;
}
.du {
  display: grid;
  grid-template-columns: minmax(120px, 2fr) 3fr 90px;
  gap: 8px;
  align-items: center;
  padding: 3px 0;
  cursor: pointer;
  font-size: 13px;
}
.du:hover {
  background: rgba(128, 128, 128, 0.08);
}
.du-path {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.du-bar {
  height: 8px;
  background: rgba(128, 128, 128, 0.15);
  border-radius: 4px;
  overflow: hidden;
}
.du-bar span {
  display: block;
  height: 100%;
  background: #18a058;
  border-radius: 4px;
}
.du-size {
  text-align: right;
  font-variant-numeric: tabular-nums;
}
</style>
