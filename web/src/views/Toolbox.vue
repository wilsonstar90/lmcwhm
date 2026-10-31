<script setup lang="ts">
// 工具箱: swap, hostname, DNS, time, SSH, processes, connections, disks, cleanup.
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import {
  NAlert, NButton, NCard, NCheckbox, NDataTable, NDynamicTags, NForm, NFormItem, NGi, NGrid, NInput, NInputNumber, NPopconfirm, NProgress,
  NSelect, NSpace, NSwitch, NTabPane, NTabs, NTag, useMessage, type DataTableColumns,
} from 'naive-ui'
import { get, post, put, ApiError } from '../api/client'
import { bytes, datetime } from '../format'
import TaskModal from '../components/TaskModal.vue'

interface Sys { hostname: string; swap_total: number; swap_used: number; swap_file: number; mem_total: number; dns: string[]; timezone: string; ntp: boolean; ntp_synced: boolean; time: number; timezones?: string[]; resolved: boolean; swap_mounts: string[] }
interface Key { type: string; comment: string; fingerprint: string; line: number }
interface SSH { port: number; permit_root_login: string; password_auth: boolean; pubkey_auth: boolean; client_alive_interval: number; max_auth_tries: number; running: boolean; authorized_keys: Key[] }
interface Proc { pid: number; ppid: number; name: string; user: string; cpu: number; mem: number; rss: number; status: string; started: number; cmd: string; threads: number }
interface Conn { proto: string; local: string; remote: string; status: string; pid: number; name: string }
interface Mount { device: string; mountpoint: string; fstype: string; total: number; used: number; free: number; percent: number; inodes_percent: number }
interface Junk { key: string; size: number; count: number }

const { t, locale } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))
const tab = ref('system')
const task = ref<{ id: string; title: string } | null>(null)

// ---- system ----
const sys = ref<Sys | null>(null)
const swapMB = ref(0)
const host = ref('')
const dns = ref<string[]>([])
const tz = ref('')
async function loadSys() {
  try {
    sys.value = await get<Sys>('/toolbox/system?zones=1')
    swapMB.value = Math.round(sys.value.swap_file / 1048576)
    host.value = sys.value.hostname
    dns.value = [...sys.value.dns]
    tz.value = sys.value.timezone
  } catch (e) {
    fail(e)
  }
}
async function act(p: Promise<unknown>, reload: () => void) {
  try {
    await p
    msg.success(t('common.saved'))
    reload()
  } catch (e) {
    fail(e)
  }
}
async function saveSwap() {
  try {
    task.value = { id: (await put<{ task_id: string }>('/toolbox/swap', { size_mb: swapMB.value })).task_id, title: 'Swap' }
  } catch (e) {
    fail(e)
  }
}
const suggestSwap = computed(() => {
  const gb = (sys.value?.mem_total ?? 0) / 1073741824
  return gb <= 2 ? Math.round(gb * 2 * 1024) : gb <= 8 ? Math.round(gb * 1024) : 4096
})

// ---- ssh ----
const ssh = ref<SSH | null>(null)
const newKey = ref('')
async function loadSSH() {
  try {
    ssh.value = await get<SSH>('/toolbox/ssh')
  } catch (e) {
    fail(e)
  }
}
const sshLockout = computed(() => !!ssh.value && !ssh.value.password_auth && (!ssh.value.pubkey_auth || !ssh.value.authorized_keys.length))

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

const loaders: Record<string, () => void> = { system: loadSys, ssh: loadSSH, processes: loadProcs, connections: loadConns, disks: () => (loadDisks(), loadDu('/')), cleanup: loadJunk }
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
onMounted(() => onTab(typeof route.query.tab === 'string' && route.query.tab in loaders ? route.query.tab : 'system'))
</script>

<template>
  <n-tabs :value="tab" type="line" animated @update:value="onTab">
    <n-tab-pane name="system" :tab="t('tb.system')">
      <n-grid v-if="sys" cols="1 l:2" responsive="screen" :x-gap="16" :y-gap="16">
        <n-gi>
          <n-card size="small" title="Swap">
            <p class="hint">{{ t('tb.swap_now', { used: bytes(sys.swap_used), total: bytes(sys.swap_total) }) }}{{ sys.swap_mounts.length ? ` · ${sys.swap_mounts.join(', ')}` : '' }}</p>
            <n-space align="center">
              <n-input-number v-model:value="swapMB" :min="0" :max="65536" :step="512" style="width: 160px"><template #suffix>MB</template></n-input-number>
              <n-button type="primary" @click="saveSwap">{{ t('common.save') }}</n-button>
              <n-button quaternary size="small" @click="swapMB = suggestSwap">{{ t('tb.swap_suggest', { n: suggestSwap }) }}</n-button>
            </n-space>
            <p class="hint">{{ t('tb.swap_hint') }}</p>
          </n-card>
          <n-card size="small" :title="t('tb.hostname')" style="margin-top: 16px">
            <n-space><n-input v-model:value="host" style="width: 260px" /><n-button type="primary" :disabled="host === sys.hostname" @click="act(put('/toolbox/hostname', { hostname: host }), loadSys)">{{ t('common.save') }}</n-button></n-space>
          </n-card>
        </n-gi>
        <n-gi>
          <n-card size="small" :title="t('tb.dns')">
            <n-dynamic-tags v-model:value="dns" />
            <n-space style="margin-top: 12px">
              <n-button type="primary" @click="act(put('/toolbox/dns', { servers: dns }), loadSys)">{{ t('common.save') }}</n-button>
              <n-button quaternary size="small" @click="dns = ['1.1.1.1', '8.8.8.8', '2606:4700:4700::1111']">Cloudflare + Google</n-button>
            </n-space>
            <p class="hint">{{ sys.resolved ? t('tb.dns_resolved') : t('tb.dns_plain') }}</p>
          </n-card>
          <n-card size="small" :title="t('tb.time')" style="margin-top: 16px">
            <p class="hint">{{ datetime(sys.time, locale) }} · NTP {{ sys.ntp_synced ? t('tb.synced') : t('tb.not_synced') }}</p>
            <n-space align="center">
              <n-select v-model:value="tz" filterable :options="(sys.timezones ?? [sys.timezone]).map((z) => ({ label: z, value: z }))" style="width: 240px" />
              <n-button type="primary" :disabled="tz === sys.timezone" @click="act(put('/toolbox/time', { timezone: tz }), loadSys)">{{ t('common.save') }}</n-button>
            </n-space>
            <n-space align="center" style="margin-top: 12px">
              <n-switch :value="sys.ntp" @update:value="(v: boolean) => act(put('/toolbox/time', { ntp: v }), loadSys)" /> {{ t('tb.ntp') }}
              <n-button size="small" @click="act(post('/toolbox/time/sync'), loadSys)">{{ t('tb.sync_now') }}</n-button>
            </n-space>
            <p class="hint">{{ t('tb.tz_hint') }}</p>
          </n-card>
        </n-gi>
      </n-grid>
    </n-tab-pane>

    <n-tab-pane name="ssh" tab="SSH">
      <template v-if="ssh">
        <n-card size="small" :title="t('tb.ssh_settings')">
          <template #header-extra><n-tag size="small" :type="ssh.running ? 'success' : 'error'">{{ ssh.running ? t('tb.running') : t('tb.stopped') }}</n-tag></template>
          <n-form label-placement="left" label-width="200" style="max-width: 620px">
            <n-form-item :label="t('tb.ssh_port')" :feedback="t('tb.ssh_port_hint')"><n-input-number v-model:value="ssh.port" :min="1" :max="65535" /></n-form-item>
            <n-form-item :label="t('tb.root_login')">
              <n-select v-model:value="ssh.permit_root_login" :options="['prohibit-password', 'yes', 'no'].map((v) => ({ label: t(`tb.root.${v}`), value: v }))" style="width: 260px" />
            </n-form-item>
            <n-form-item :label="t('tb.password_auth')"><n-switch v-model:value="ssh.password_auth" /></n-form-item>
            <n-form-item :label="t('tb.pubkey_auth')"><n-switch v-model:value="ssh.pubkey_auth" /></n-form-item>
            <n-form-item :label="t('tb.max_tries')"><n-input-number v-model:value="ssh.max_auth_tries" :min="0" :max="100" /></n-form-item>
            <n-form-item :label="t('tb.keepalive')"><n-input-number v-model:value="ssh.client_alive_interval" :min="0" :max="3600"><template #suffix>s</template></n-input-number></n-form-item>
          </n-form>
          <n-alert v-if="sshLockout" type="error" :show-icon="false" style="margin-bottom: 12px">{{ t('tb.lockout') }}</n-alert>
          <n-popconfirm @positive-click="act(put('/toolbox/ssh', ssh), loadSSH)">
            <template #trigger><n-button type="primary" :disabled="sshLockout">{{ t('common.save') }}</n-button></template>
            {{ t('tb.ssh_confirm') }}
          </n-popconfirm>
        </n-card>
        <n-card size="small" :title="t('tb.keys')" style="margin-top: 16px">
          <div v-for="k in ssh.authorized_keys" :key="k.line" class="key">
            <span><b>{{ k.type }}</b> {{ k.comment }} <code class="hint">{{ k.fingerprint }}</code></span>
            <n-popconfirm @positive-click="act(post('/toolbox/ssh/keys/delete', { line: k.line }), loadSSH)">
              <template #trigger><n-button size="tiny" type="error" quaternary>{{ t('common.delete') }}</n-button></template>
              {{ t('common.confirm_delete') }}
            </n-popconfirm>
          </div>
          <p v-if="!ssh.authorized_keys.length" class="hint">{{ t('tb.no_keys') }}</p>
          <n-input v-model:value="newKey" type="textarea" :rows="3" placeholder="ssh-ed25519 AAAA… user@laptop" style="margin-top: 8px" />
          <n-button style="margin-top: 8px" :disabled="!newKey.trim()" @click="act(post('/toolbox/ssh/keys', { key: newKey }), () => ((newKey = ''), loadSSH()))">{{ t('tb.add_key') }}</n-button>
        </n-card>
      </template>
    </n-tab-pane>

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
  <TaskModal :task-id="task?.id ?? null" :title="task?.title ?? ''" @close="task = null; loadSys()" @done="loadSys" />
</template>

<style scoped>
.hint {
  font-size: 12px;
  opacity: 0.65;
}
.key,
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
