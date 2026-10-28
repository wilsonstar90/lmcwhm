<script setup lang="ts">
import { computed, h, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NCard, NDataTable, NDescriptions, NDescriptionsItem, NInput, NInputNumber, NModal, NPopconfirm, NSelect, NSpace,
  NSwitch, NTabPane, NTabs, NTag, useMessage, type DataTableColumns,
} from 'naive-ui'
import { get, post, put, ApiError } from '../api/client'
import type { InstalledApp } from '../api/types'
import { bytes, datetime } from '../format'
import TaskModal from './TaskModal.vue'

// The settings window of one installed engine (aaPanel 軟件設定).
const props = defineProps<{ app: InstalledApp | null }>()
const emit = defineEmits<{ close: []; changed: [] }>()
const { t, locale } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))

const tab = ref('service')
const task = ref<{ id: string; title: string } | null>(null)
const isMy = computed(() => props.app?.app === 'mysql' || props.app?.app === 'mariadb')
const isRedis = computed(() => props.app?.app === 'redis')
const base = computed(() => `/apps/${props.app?.id}`)
const title = computed(() => (props.app ? `${props.app.name} ${props.app.version}` : ''))

function runTask(id: string, label: string) {
  if (id) task.value = { id, title: `${label} · ${title.value}` }
}

// ---- service ----
interface ServiceInfo extends InstalledApp { oom_protect: boolean; data_size: number; started: number }
const svc = ref<ServiceInfo | null>(null)
async function loadService() {
  svc.value = await get<ServiceInfo>(`${base.value}/service`)
  port.value = svc.value.port
}
async function control(action: string) {
  try {
    await post(`${base.value}/${action}`)
    msg.success(t('common.done'))
    await loadService()
    emit('changed')
  } catch (e) {
    fail(e)
  }
}
async function setOOM(on: boolean) {
  try {
    runTask((await post<{ task_id: string }>(`${base.value}/oom`, { on })).task_id, t('dbs.oom'))
  } catch (e) {
    fail(e)
  }
}

// ---- config file ----
const config = ref('')
async function loadConfig() {
  config.value = (await get<{ config: string }>(`${base.value}/config`)).config
}
async function saveConfig() {
  try {
    runTask((await put<{ task_id: string }>(`${base.value}/config`, { config: config.value })).task_id, t('app.config'))
  } catch (e) {
    fail(e)
  }
}

// ---- port ----
const port = ref(0)
async function savePort() {
  try {
    const r = await post<{ task_id: string }>(`${base.value}/port`, { port: port.value })
    runTask(r.task_id, t('dbs.port'))
  } catch (e) {
    fail(e)
  }
}

// ---- logs ----
const logs = ref('')
const logEl = ref<HTMLElement | null>(null)
async function loadLogs() {
  logs.value = (await get<{ logs: string }>(`${base.value}/logs?tail=500`)).logs || '—'
  await nextTick()
  logEl.value?.scrollTo({ top: logEl.value.scrollHeight })
}

// ---- MySQL status ----
interface MyStatus { version: string; uptime: number; status: Record<string, string>; vars: Record<string, string>; bin_file: string; bin_pos: string; data_size: number }
const my = ref<MyStatus | null>(null)
async function loadMyStatus() {
  my.value = await get<MyStatus>(`${base.value}/mysql/status`)
}
const n = (k: string) => Number(my.value?.status[k] ?? 0)
const v = (k: string) => Number(my.value?.vars[k] ?? 0)
const pct = (a: number, b: number) => (b > 0 ? `${((a / b) * 100).toFixed(2)}%` : '—')
const myRows = computed(() => {
  if (!my.value) return []
  return [
    { k: 'connections', value: `${n('threads_connected')} / ${n('max_used_connections')}`, tip: t('dbs.tip.connections') },
    { k: 'thread_cache', value: pct(1 - n('threads_created') / Math.max(n('connections'), 1), 1), tip: t('dbs.tip.thread_cache') },
    { k: 'key_hit', value: pct(1 - n('key_reads') / Math.max(n('key_read_requests'), 1), 1), tip: t('dbs.tip.key_hit') },
    { k: 'innodb_hit', value: pct(1 - n('innodb_buffer_pool_reads') / Math.max(n('innodb_buffer_pool_read_requests'), 1), 1), tip: t('dbs.tip.innodb_hit') },
    ...(my.value.status.qcache_hits
      ? [{ k: 'qcache_hit', value: pct(n('qcache_hits'), n('qcache_hits') + n('qcache_inserts') + n('qcache_not_cached')), tip: t('dbs.tip.qcache_hit') }]
      : []),
    { k: 'tmp_disk', value: pct(n('created_tmp_disk_tables'), n('created_tmp_tables')), tip: t('dbs.tip.tmp_disk') },
    { k: 'open_tables', value: String(n('open_tables')), tip: t('dbs.tip.open_tables') },
    { k: 'full_join', value: String(n('select_full_join')), tip: t('dbs.tip.full_join') },
    { k: 'range_check', value: String(n('select_range_check')), tip: t('dbs.tip.range_check') },
    { k: 'sort_merge', value: String(n('sort_merge_passes')), tip: t('dbs.tip.sort_merge') },
    { k: 'locks_waited', value: String(n('table_locks_waited')), tip: t('dbs.tip.locks_waited') },
    { k: 'slow_queries', value: String(n('slow_queries')), tip: '' },
  ]
})
const rowCols: DataTableColumns<{ k: string; value: string; tip: string }> = [
  { title: () => t('dbs.field'), key: 'k', width: 200, render: (r) => t(`dbs.m.${r.k}`) },
  { title: () => t('dbs.value'), key: 'value', width: 140 },
  { title: () => t('dbs.note'), key: 'tip', render: (r) => h('span', { class: 'hint' }, r.tip) },
]

// ---- MySQL tuning ----
type Unit = 'M' | 'K' | ''
const tuneFields: { key: string; unit: Unit }[] = [
  { key: 'query_cache_size', unit: 'M' }, { key: 'table_open_cache', unit: '' }, { key: 'thread_cache_size', unit: '' },
  { key: 'key_buffer_size', unit: 'M' }, { key: 'tmp_table_size', unit: 'M' }, { key: 'innodb_buffer_pool_size', unit: 'M' },
  { key: 'innodb_log_buffer_size', unit: 'M' }, { key: 'max_connections', unit: '' }, { key: 'sort_buffer_size', unit: 'K' },
  { key: 'read_buffer_size', unit: 'K' }, { key: 'read_rnd_buffer_size', unit: 'K' }, { key: 'join_buffer_size', unit: 'K' },
  { key: 'thread_stack', unit: 'K' }, { key: 'binlog_cache_size', unit: 'K' },
]
const tune = ref<Record<string, number>>({})
const shownTune = computed(() => tuneFields.filter((f) => f.key !== 'query_cache_size' || props.app?.app === 'mariadb'))
function fillTune() {
  const out: Record<string, number> = {}
  for (const f of tuneFields) {
    const raw = v(f.key)
    out[f.key] = f.unit === 'M' ? Math.round(raw / 1048576) : f.unit === 'K' ? Math.round(raw / 1024) : raw
  }
  tune.value = out
}
// [key_buffer, query_cache, tmp_table, innodb_buffer_pool, innodb_log_buffer, sort, read, read_rnd, join, stack, binlog_cache, thread_cache, table_open_cache, max_connections]
const presets: Record<string, number[]> = {
  '1-2GB': [128, 64, 64, 256, 32, 768, 768, 512, 1024, 256, 64, 64, 128, 100],
  '2-4GB': [256, 128, 384, 384, 64, 768, 768, 512, 2048, 256, 64, 96, 192, 200],
  '4-8GB': [384, 192, 512, 512, 128, 1024, 1024, 768, 2048, 256, 128, 128, 384, 300],
  '8-16GB': [512, 256, 1024, 1024, 128, 2048, 2048, 1024, 4096, 384, 192, 192, 1024, 400],
  '16-32GB': [1024, 384, 2048, 4096, 256, 4096, 4096, 2048, 8192, 512, 256, 256, 2048, 500],
}
const preset = ref<string | null>(null)
function applyPreset(name: string) {
  const p = presets[name]
  const keys = ['key_buffer_size', 'query_cache_size', 'tmp_table_size', 'innodb_buffer_pool_size', 'innodb_log_buffer_size', 'sort_buffer_size',
    'read_buffer_size', 'read_rnd_buffer_size', 'join_buffer_size', 'thread_stack', 'binlog_cache_size', 'thread_cache_size', 'table_open_cache', 'max_connections']
  keys.forEach((k, i) => (tune.value[k] = p[i]))
}
const maxMemMB = computed(() => {
  const x = tune.value
  const global = (x.key_buffer_size ?? 0) + (props.app?.app === 'mariadb' ? x.query_cache_size ?? 0 : 0) + (x.tmp_table_size ?? 0) +
    (x.innodb_buffer_pool_size ?? 0) + (x.innodb_log_buffer_size ?? 0)
  const per = (x.sort_buffer_size ?? 0) + (x.read_buffer_size ?? 0) + (x.read_rnd_buffer_size ?? 0) + (x.join_buffer_size ?? 0) +
    (x.thread_stack ?? 0) + (x.binlog_cache_size ?? 0)
  return global + ((x.max_connections ?? 0) * per) / 1024
})
async function saveTune() {
  const values: Record<string, string> = {}
  for (const f of shownTune.value) {
    const val = tune.value[f.key]
    if (val != null) values[f.key] = `${Math.round(val)}${f.unit}`
  }
  try {
    runTask((await put<{ task_id: string }>(`${base.value}/mysql/tuning`, { values })).task_id, t('dbs.tuning'))
  } catch (e) {
    fail(e)
  }
}

// ---- slow log ----
const slow = ref({ on: false, seconds: 3, log: '' })
async function loadSlow() {
  await loadMyStatus()
  slow.value.on = my.value?.vars.slow_query_log === 'ON'
  slow.value.seconds = Number(my.value?.vars.long_query_time ?? 3) || 3
  slow.value.log = (await get<{ log: string }>(`${base.value}/mysql/slowlog`)).log
}
async function setSlow() {
  try {
    await post(`${base.value}/mysql/slowlog`, { on: slow.value.on, seconds: slow.value.seconds })
    msg.success(t('common.saved'))
    loadSlow()
  } catch (e) {
    fail(e)
  }
}
async function clearSlow() {
  try {
    await post(`${base.value}/mysql/slowlog/clear`)
    slow.value.log = ''
  } catch (e) {
    fail(e)
  }
}

// ---- binary logs ----
interface Binlogs { enabled: boolean; expire_seconds: number; total: number; files: { name: string; size: number; modified: number }[] }
const bin = ref<Binlogs | null>(null)
const expireDays = ref(30)
async function loadBin() {
  bin.value = await get<Binlogs>(`${base.value}/mysql/binlogs`)
  expireDays.value = Math.round(bin.value.expire_seconds / 86400) || 30
}
async function setBin(on: boolean) {
  try {
    runTask((await post<{ task_id: string }>(`${base.value}/mysql/binlogs`, { on, expire_days: expireDays.value })).task_id, t('dbs.binlog'))
  } catch (e) {
    fail(e)
  }
}
async function purge(before: string) {
  try {
    await post(`${base.value}/mysql/binlogs/purge`, { before })
    loadBin()
  } catch (e) {
    fail(e)
  }
}
const binCols = computed<DataTableColumns<Binlogs['files'][number]>>(() => [
  { title: () => t('dbs.file'), key: 'name' },
  { title: () => t('dbs.size'), key: 'size', width: 120, render: (f) => bytes(f.size) },
  { title: () => t('dbs.modified'), key: 'modified', width: 180, render: (f) => (f.modified ? datetime(f.modified, locale.value) : '—') },
  {
    title: '', key: 'a', width: 150,
    render: (f, i) =>
      i === 0
        ? null
        : h(NPopconfirm, { onPositiveClick: () => purge(f.name) }, {
          trigger: () => h(NButton, { size: 'tiny', ghost: true, type: 'error' }, () => t('dbs.purge_before')),
          default: () => t('dbs.purge_confirm', { name: f.name }),
        }),
  },
])

// ---- Redis ----
interface RedisSettings { port: number; timeout: string; maxclients: string; databases: string; maxmemory: string; maxmemory_policy: string; appendonly: string; appendfsync: string; save: string[]; password: string }
const rs = ref<RedisSettings | null>(null)
const maxMemMBRedis = ref(0)
const saveRules = ref<{ sec: number; changes: number }[]>([])
function memToMB(s: string): number {
  const m = /^(\d+)([kmg]?)b?$/i.exec(s.trim())
  if (!m) return 0
  const n = Number(m[1])
  const u = m[2].toLowerCase()
  return u === 'g' ? n * 1024 : u === 'm' ? n : u === 'k' ? n / 1024 : n / 1048576
}
async function loadRedis() {
  rs.value = await get<RedisSettings>(`${base.value}/redis/settings`)
  maxMemMBRedis.value = Math.round(memToMB(rs.value.maxmemory))
  saveRules.value = rs.value.save.filter((x) => x !== '""').map((x) => {
    const [sec, ch] = x.split(' ').map(Number)
    return { sec, changes: ch }
  })
}
async function saveRedis() {
  if (!rs.value) return
  const body = { ...rs.value, maxmemory: maxMemMBRedis.value > 0 ? `${maxMemMBRedis.value}mb` : '0', save: saveRules.value.map((r) => `${r.sec} ${r.changes}`) }
  try {
    const r = await put<{ task_id: string }>(`${base.value}/redis/settings`, body)
    if (r.task_id) runTask(r.task_id, t('dbs.tuning'))
    else msg.success(t('dbs.applied_live'))
    if (rs.value.port !== port.value && rs.value.port) {
      port.value = rs.value.port
      await savePort()
    }
  } catch (e) {
    fail(e)
  }
}
async function bgsave() {
  try {
    await post(`${base.value}/redis/bgsave`)
    msg.success(t('dbs.bgsave_started'))
  } catch (e) {
    fail(e)
  }
}
const info = ref<Record<string, string>>({})
const redisFields = ['redis_version', 'uptime_in_days', 'tcp_port', 'connected_clients', 'used_memory_rss_human', 'used_memory_human', 'used_memory_peak_human',
  'mem_fragmentation_ratio', 'total_connections_received', 'total_commands_processed', 'instantaneous_ops_per_sec', 'keyspace_hits', 'keyspace_misses',
  'hit_rate', 'latest_fork_usec', 'rdb_last_save_time', 'aof_enabled']
const infoRows = computed(() =>
  redisFields.map((k) => {
    let val = info.value[k] ?? '—'
    if (k === 'hit_rate') {
      const hits = Number(info.value.keyspace_hits ?? 0)
      val = pct(hits, hits + Number(info.value.keyspace_misses ?? 0))
    }
    if (k === 'rdb_last_save_time' && info.value[k]) val = datetime(Number(info.value[k]), locale.value)
    return { k, value: val, tip: t(`dbs.r.${k}`) }
  }),
)
const infoCols: DataTableColumns<{ k: string; value: string; tip: string }> = [
  { title: () => t('dbs.field'), key: 'k', width: 230 },
  { title: () => t('dbs.value'), key: 'value', width: 160 },
  { title: () => t('dbs.note'), key: 'tip', render: (r) => h('span', { class: 'hint' }, r.tip) },
]

const policies = ['noeviction', 'allkeys-lru', 'allkeys-lfu', 'allkeys-random', 'volatile-lru', 'volatile-lfu', 'volatile-random', 'volatile-ttl'].map((x) => ({ label: x, value: x }))

// ---- tab loading ----
const tabs = computed(() => {
  if (isMy.value) return ['service', 'config', 'port', 'status', 'tuning', 'errorlog', 'slowlog', 'binlog']
  if (isRedis.value) return ['service', 'tuning', 'config', 'status', 'persistence', 'logs']
  return ['service', 'port', 'logs']
})
async function loadTab(name: string) {
  if (!props.app) return
  try {
    switch (name) {
      case 'service': await loadService(); break
      case 'config': await loadConfig(); break
      case 'port': await loadService(); break
      case 'logs': case 'errorlog': await loadLogs(); break
      case 'status': isMy.value ? await loadMyStatus() : (info.value = await get<Record<string, string>>(`${base.value}/redis/info`)); break
      case 'tuning': if (isMy.value) { await loadMyStatus(); fillTune() } else { await loadRedis(); await loadService() } break
      case 'persistence': await loadRedis(); break
      case 'slowlog': await loadSlow(); break
      case 'binlog': await loadBin(); break
    }
  } catch (e) {
    fail(e)
  }
}
watch(() => props.app?.id, (id) => {
  if (!id) return
  tab.value = 'service'
  svc.value = null
  loadTab('service')
}, { immediate: true })
watch(tab, loadTab)
function taskDone() {
  loadTab(tab.value)
  emit('changed')
}
</script>

<template>
  <n-modal :show="!!app" @update:show="(x: boolean) => !x && emit('close')">
    <n-card v-if="app" :title="title" style="width: min(900px, calc(100vw - 32px))" closable @close="emit('close')">
      <n-tabs v-model:value="tab" type="line" placement="left" class="dbs" animated>
        <n-tab-pane v-for="name in tabs" :key="name" :name="name" :tab="t(`dbs.tabs.${name}`)">
          <!-- service -->
          <template v-if="name === 'service' && svc">
            <n-descriptions :column="2" label-placement="left" bordered size="small">
              <n-descriptions-item :label="t('common.status')"><n-tag size="small" :type="svc.state === 'running' ? 'success' : 'error'">{{ svc.state || '—' }}</n-tag></n-descriptions-item>
              <n-descriptions-item :label="t('dbs.started')">{{ svc.started && svc.state === 'running' ? datetime(svc.started, locale) : '—' }}</n-descriptions-item>
              <n-descriptions-item :label="t('dbs.address')">127.0.0.1:{{ svc.port }}</n-descriptions-item>
              <n-descriptions-item :label="t('dbs.data_size')">{{ bytes(svc.data_size) }}</n-descriptions-item>
              <n-descriptions-item :label="t('dbs.container')">{{ svc.container }}</n-descriptions-item>
              <n-descriptions-item :label="t('dbs.image')">{{ svc.image }}</n-descriptions-item>
            </n-descriptions>
            <n-space style="margin-top: 16px">
              <n-button v-if="svc.state !== 'running'" type="primary" @click="control('start')">{{ t('app.start') }}</n-button>
              <n-button v-else @click="control('stop')">{{ t('app.stop') }}</n-button>
              <n-button @click="control('restart')">{{ t('app.restart') }}</n-button>
            </n-space>
            <div class="section">
              <n-space align="center"><b>{{ t('dbs.oom') }}</b><n-switch :value="svc.oom_protect" @update:value="setOOM" /></n-space>
              <p class="hint">{{ t('dbs.oom_hint') }}</p>
            </div>
          </template>

          <!-- config file -->
          <template v-else-if="name === 'config'">
            <p class="hint" style="margin-top: 0">{{ t(isMy ? 'dbs.config_hint_my' : 'dbs.config_hint_redis') }}</p>
            <n-input v-model:value="config" type="textarea" :rows="20" class="mono" />
            <n-space style="margin-top: 12px"><n-button type="primary" @click="saveConfig">{{ t('app.save_restart') }}</n-button></n-space>
          </template>

          <!-- port -->
          <template v-else-if="name === 'port'">
            <n-space align="center">
              <n-input-number v-model:value="port" :min="1024" :max="65535" style="width: 160px" />
              <n-button type="primary" :disabled="!svc || port === svc.port" @click="savePort">{{ t('dbs.change') }}</n-button>
            </n-space>
            <p class="hint">{{ t('dbs.port_hint') }}</p>
          </template>

          <!-- status -->
          <template v-else-if="name === 'status' && isMy && my">
            <n-descriptions :column="2" label-placement="left" bordered size="small">
              <n-descriptions-item :label="t('dbs.started')">{{ datetime(Date.now() / 1000 - my.uptime, locale) }}</n-descriptions-item>
              <n-descriptions-item :label="t('dbs.total_conn')">{{ n('connections') }}</n-descriptions-item>
              <n-descriptions-item :label="t('dbs.sent')">{{ bytes(n('bytes_sent')) }}</n-descriptions-item>
              <n-descriptions-item :label="t('dbs.received')">{{ bytes(n('bytes_received')) }}</n-descriptions-item>
              <n-descriptions-item :label="t('dbs.qps')">{{ my.uptime ? Math.round(n('questions') / my.uptime) : 0 }}</n-descriptions-item>
              <n-descriptions-item :label="t('dbs.tps')">{{ my.uptime ? Math.round((n('com_commit') + n('com_rollback')) / my.uptime) : 0 }}</n-descriptions-item>
              <n-descriptions-item label="File">{{ my.bin_file || '—' }}</n-descriptions-item>
              <n-descriptions-item label="Position">{{ my.bin_pos || '—' }}</n-descriptions-item>
            </n-descriptions>
            <n-data-table size="small" :columns="rowCols" :data="myRows" style="margin-top: 16px" />
          </template>
          <template v-else-if="name === 'status' && isRedis">
            <n-data-table size="small" :columns="infoCols" :data="infoRows" :max-height="460" />
          </template>

          <!-- MySQL tuning -->
          <template v-else-if="name === 'tuning' && isMy">
            <n-space align="center" style="margin-bottom: 12px">
              <span>{{ t('dbs.preset') }}</span>
              <n-select v-model:value="preset" :options="Object.keys(presets).map((k) => ({ label: k, value: k }))" :placeholder="t('dbs.custom')" style="width: 140px" @update:value="applyPreset" />
              <span>{{ t('dbs.max_mem') }}</span><n-tag>{{ maxMemMB.toFixed(0) }} MB</n-tag>
            </n-space>
            <div class="grid">
              <template v-for="f in shownTune" :key="f.key">
                <span class="mono k">{{ f.key }}</span>
                <n-input-number v-model:value="tune[f.key]" :min="0" size="small" style="width: 150px" />
                <span class="hint u">{{ f.unit === 'M' ? 'MB' : f.unit === 'K' ? t('dbs.kb_per_conn') : '' }} {{ t(`dbs.t.${f.key}`) }}</span>
              </template>
            </div>
            <n-space style="margin-top: 12px"><n-button type="primary" @click="saveTune">{{ t('app.save_restart') }}</n-button></n-space>
          </template>

          <!-- Redis performance -->
          <template v-else-if="name === 'tuning' && isRedis && rs">
            <div class="grid">
              <span class="mono k">port</span><n-input-number v-model:value="rs.port" :min="1024" :max="65535" size="small" style="width: 150px" /><span class="hint u">{{ t('dbs.rt.port') }}</span>
              <span class="mono k">timeout</span><n-input v-model:value="rs.timeout" size="small" style="width: 150px" /><span class="hint u">{{ t('dbs.rt.timeout') }}</span>
              <span class="mono k">maxclients</span><n-input v-model:value="rs.maxclients" size="small" style="width: 150px" /><span class="hint u">{{ t('dbs.rt.maxclients') }}</span>
              <span class="mono k">databases</span><n-input v-model:value="rs.databases" size="small" style="width: 150px" /><span class="hint u">{{ t('dbs.rt.databases') }}</span>
              <span class="mono k">requirepass</span><n-input :value="rs.password" type="password" show-password-on="click" readonly size="small" style="width: 150px" /><span class="hint u">{{ t('dbs.rt.requirepass') }}</span>
              <span class="mono k">maxmemory</span><n-input-number v-model:value="maxMemMBRedis" :min="0" size="small" style="width: 150px" /><span class="hint u">{{ t('dbs.rt.maxmemory') }}</span>
              <span class="mono k">maxmemory-policy</span><n-select v-model:value="rs.maxmemory_policy" :options="policies" size="small" style="width: 150px" /><span class="hint u">{{ t('dbs.rt.policy') }}</span>
            </div>
            <n-space style="margin-top: 12px"><n-button type="primary" @click="saveRedis">{{ t('common.save') }}</n-button></n-space>
            <n-alert type="warning" :show-icon="false" style="margin-top: 12px">{{ t('dbs.redis_note') }}</n-alert>
          </template>

          <!-- Redis persistence -->
          <template v-else-if="name === 'persistence' && rs">
            <b>{{ t('dbs.aof') }}</b>
            <div class="grid" style="margin-top: 8px">
              <span class="mono k">appendonly</span><n-select v-model:value="rs.appendonly" :options="[{ label: t('fw.on'), value: 'yes' }, { label: t('fw.off'), value: 'no' }]" size="small" style="width: 150px" /><span />
              <span class="mono k">appendfsync</span><n-select v-model:value="rs.appendfsync" :options="['always', 'everysec', 'no'].map((x) => ({ label: x, value: x }))" size="small" style="width: 150px" /><span class="hint u">{{ t('dbs.fsync_hint') }}</span>
            </div>
            <div class="section">
              <b>{{ t('dbs.rdb') }}</b>
              <n-space vertical style="margin-top: 8px">
                <n-space v-for="(r, i) in saveRules" :key="i" align="center">
                  <n-input-number v-model:value="r.sec" :min="1" size="small" style="width: 110px" /><span>{{ t('dbs.rdb_sec') }}</span>
                  <n-input-number v-model:value="r.changes" :min="1" size="small" style="width: 110px" /><span>{{ t('dbs.rdb_changes') }}</span>
                  <n-button size="tiny" quaternary type="error" @click="saveRules.splice(i, 1)">{{ t('common.delete') }}</n-button>
                </n-space>
                <n-button size="small" dashed @click="saveRules.push({ sec: 300, changes: 10 })">{{ t('dbs.rdb_add') }}</n-button>
              </n-space>
              <p class="hint">{{ t('dbs.rdb_hint') }}</p>
            </div>
            <n-space style="margin-top: 12px">
              <n-button type="primary" @click="saveRedis">{{ t('common.save') }}</n-button>
              <n-button @click="bgsave">{{ t('dbs.bgsave') }}</n-button>
            </n-space>
          </template>

          <!-- logs -->
          <template v-else-if="name === 'logs' || name === 'errorlog'">
            <n-space style="margin-bottom: 8px"><n-button size="small" @click="loadLogs">{{ t('common.refresh') }}</n-button></n-space>
            <pre ref="logEl" class="log">{{ logs }}</pre>
          </template>

          <!-- slow log -->
          <template v-else-if="name === 'slowlog'">
            <n-space align="center">
              <n-switch v-model:value="slow.on" />
              <span>{{ t('dbs.slow_over') }}</span>
              <n-input-number v-model:value="slow.seconds" :min="0.1" :max="3600" :step="0.5" size="small" style="width: 110px" />
              <span>{{ t('dbs.seconds') }}</span>
              <n-button size="small" type="primary" @click="setSlow">{{ t('common.save') }}</n-button>
              <n-button size="small" @click="loadSlow">{{ t('common.refresh') }}</n-button>
              <n-popconfirm @positive-click="clearSlow">
                <template #trigger><n-button size="small">{{ t('dbs.clear') }}</n-button></template>
                {{ t('dbs.clear_confirm') }}
              </n-popconfirm>
            </n-space>
            <pre class="log" style="margin-top: 8px">{{ slow.log || t('dbs.slow_empty') }}</pre>
          </template>

          <!-- binary logs -->
          <template v-else-if="name === 'binlog' && bin">
            <n-space align="center" justify="space-between">
              <n-space align="center">
                <b>{{ t('dbs.binlog') }}</b>
                <n-switch :value="bin.enabled" @update:value="setBin" />
                <n-tag v-if="bin.enabled">{{ bytes(bin.total) }}</n-tag>
              </n-space>
              <n-space align="center">
                <span>{{ t('dbs.keep') }}</span>
                <n-input-number v-model:value="expireDays" :min="1" :max="365" size="small" style="width: 100px" />
                <span>{{ t('dbs.days') }}</span>
                <n-button size="small" :disabled="!bin.enabled" @click="setBin(true)">{{ t('app.save_restart') }}</n-button>
              </n-space>
            </n-space>
            <p class="hint">{{ t('dbs.binlog_hint') }}</p>
            <n-data-table v-if="bin.enabled" size="small" :columns="binCols" :data="[...bin.files].reverse()" :max-height="380" />
          </template>
        </n-tab-pane>
      </n-tabs>
    </n-card>
  </n-modal>
  <TaskModal :task-id="task?.id ?? null" :title="task?.title ?? ''" @close="task = null; taskDone()" @done="taskDone" />
</template>

<style scoped>
.hint,
:deep(.hint) {
  font-size: 12px;
  opacity: 0.65;
  margin: 8px 0 0;
}
.section {
  margin-top: 20px;
  padding-top: 16px;
  border-top: 1px solid var(--n-border-color, rgba(128, 128, 128, 0.2));
}
.mono,
:deep(.mono textarea) {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12.5px;
}
.grid {
  display: grid;
  grid-template-columns: max-content max-content 1fr;
  gap: 8px 12px;
  align-items: center;
}
.grid .u {
  margin: 0;
}
.log {
  background: #111;
  color: #ddd;
  padding: 10px 12px;
  border-radius: 6px;
  max-height: 460px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-all;
  font: 12px/1.5 ui-monospace, SFMono-Regular, Menlo, monospace;
  margin: 0;
}
.dbs :deep(.n-tab-pane) {
  padding-left: 16px;
  min-height: 460px;
}
@media (max-width: 640px) {
  .grid {
    grid-template-columns: 1fr;
  }
}
</style>
