<script setup lang="ts">
import AiButton from '../components/AiButton.vue'
import { computed, h, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NButton, NCard, NCode, NDataTable, NGi, NGrid, NInput, NInputNumber, NPopconfirm, NRadioButton, NRadioGroup, NSelect, NSpace,
  NStatistic, NTabPane, NTabs, NTag, useMessage, type DataTableColumns,
} from 'naive-ui'
import { get, post, ApiError } from '../api/client'
import { bytes, datetime } from '../format'
import IpGeo from '../components/IpGeo.vue'

interface LoginRow { id: number; username: string; ip: string; user_agent: string; success: boolean; reason: string; created_at: number }
interface OpRow { id: number; username: string; ip: string; category: string; action: string; detail: string; created_at: number }
interface Access { ip: string; time: number; method: string; path: string; status: number; bytes: number; referer: string; ua: string; host: string; raw?: string }
interface SSHEvent { time: number; result: 'accepted' | 'failed' | 'invalid'; user: string; ip: string; port: number; method: string }
interface SSHSummary { accepted: number; failed: number; top_ips: { ip: string; count: number }[] | null; items: SSHEvent[] }
interface CronRow { id: number; job: string; started_at: number; duration_ms: number; status: string; manual: boolean; output: string }
interface GeoStatus { edition: string; built: number; size: number; has_asn: boolean; updating: boolean; last_error?: string }

const { t, locale } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))
const PAGE = 20
const tab = ref('operation')
const time = (r: { created_at: number }) => datetime(r.created_at, locale.value)
const ipCell = (ip: string) => h(IpGeo, { ip })

function usePaged<T>(path: string) {
  const rows = ref<T[]>([])
  const loading = ref(false)
  const pagination = ref({ page: 1, pageSize: PAGE, itemCount: 0 })
  async function load(page = pagination.value.page) {
    loading.value = true
    try {
      const r = await get<{ items: T[]; total: number }>(`${path}?limit=${PAGE}&offset=${(page - 1) * PAGE}`)
      rows.value = r.items
      pagination.value = { ...pagination.value, page, itemCount: r.total }
    } finally {
      loading.value = false
    }
  }
  return { rows, loading, pagination, load }
}
const logins = usePaged<LoginRow>('/logs/login')
const ops = usePaged<OpRow>('/logs/operation')
const cronRuns = usePaged<CronRow>('/cron/runs')

const loginCols: DataTableColumns<LoginRow> = [
  { title: () => t('common.time'), key: 'created_at', width: 170, render: time },
  { title: () => t('common.user'), key: 'username', width: 120 },
  { title: () => t('common.ip'), key: 'ip', width: 300, render: (r) => ipCell(r.ip) },
  { title: () => t('common.status'), key: 'success', width: 90, render: (r) => h(NTag, { type: r.success ? 'success' : 'error', size: 'small' }, () => (r.success ? t('common.success') : t('common.failed'))) },
  { title: () => t('logs.reason'), key: 'reason', width: 130 },
  { title: 'User-Agent', key: 'user_agent', ellipsis: { tooltip: true } },
]
const opCols: DataTableColumns<OpRow> = [
  { title: () => t('common.time'), key: 'created_at', width: 170, render: time },
  { title: () => t('common.user'), key: 'username', width: 110 },
  { title: () => t('common.ip'), key: 'ip', width: 300, render: (r) => ipCell(r.ip) },
  { title: () => t('logs.category'), key: 'category', width: 90 },
  { title: () => t('logs.action'), key: 'action', width: 160 },
  { title: () => t('common.detail'), key: 'detail', ellipsis: { tooltip: true } },
]
const cronCols: DataTableColumns<CronRow> = [
  { title: () => t('common.time'), key: 'started_at', width: 170, render: (r) => datetime(r.started_at, locale.value) },
  { title: () => t('cron.name'), key: 'job', width: 180 },
  { title: () => t('common.status'), key: 'status', width: 100, render: (r) => h(NTag, { size: 'small', type: r.status === 'success' ? 'success' : r.status === 'running' ? 'info' : 'error' }, () => t(`cron.status.${r.status}`)) },
  { title: () => t('cron.trigger'), key: 'manual', width: 80, render: (r) => (r.manual ? t('cron.manual') : t('cron.auto')) },
  { title: () => t('cron.output'), key: 'output', ellipsis: { tooltip: true } },
]

// ---- sources ----
const sites = ref<string[]>([])
const containers = ref<{ name: string; image: string; managed: boolean }[]>([])
async function loadSources() {
  const s = await get<{ sites: string[]; containers: typeof containers.value }>('/logs/sources')
  sites.value = s.sites
  containers.value = s.containers
  site.value ||= s.sites[0] ?? null
  container.value ||= s.containers[0]?.name ?? null
}

// ---- SSH ----
const ssh = ref<SSHSummary | null>(null)
const sshQ = ref('')
const sshLoading = ref(false)
async function loadSSH() {
  sshLoading.value = true
  try {
    ssh.value = await get<SSHSummary>(`/logs/ssh?limit=500&q=${encodeURIComponent(sshQ.value)}`)
  } catch (e) {
    fail(e)
  } finally {
    sshLoading.value = false
  }
}
const sshCols: DataTableColumns<SSHEvent> = [
  { title: () => t('common.time'), key: 'time', width: 170, render: (e) => (e.time ? datetime(e.time, locale.value) : '—') },
  { title: () => t('common.status'), key: 'result', width: 110, render: (e) => h(NTag, { size: 'small', type: e.result === 'accepted' ? 'success' : 'error' }, () => t(`logs.ssh.${e.result}`)) },
  { title: () => t('common.user'), key: 'user', width: 120 },
  { title: () => t('common.ip'), key: 'ip', render: (e) => ipCell(e.ip) },
  { title: () => t('logs.ssh.method'), key: 'method', width: 140 },
]
const topCols: DataTableColumns<{ ip: string; count: number }> = [
  { title: () => t('common.ip'), key: 'ip', render: (r) => ipCell(r.ip) },
  { title: () => t('logs.ssh.failures'), key: 'count', width: 90 },
]

// ---- website logs ----
const site = ref<string | null>(null)
const kind = ref<'access' | 'error'>('access')
const siteQ = ref('')
const siteLines = ref(500)
const access = ref<Access[]>([])
const errLines = ref<string[]>([])
const siteSize = ref(0)
const siteLoading = ref(false)
async function loadSite() {
  if (!site.value) return
  siteLoading.value = true
  try {
    const r = await get<{ items?: Access[]; lines?: string[]; size: number }>(
      `/logs/site?domain=${encodeURIComponent(site.value)}&kind=${kind.value}&lines=${siteLines.value}&q=${encodeURIComponent(siteQ.value)}`,
    )
    access.value = (r.items ?? []).slice().reverse()
    errLines.value = r.lines ?? []
    siteSize.value = r.size
  } catch (e) {
    fail(e)
  } finally {
    siteLoading.value = false
  }
}
async function clearSite() {
  try {
    await post('/logs/site/clear', { domain: site.value, kind: kind.value })
    loadSite()
  } catch (e) {
    fail(e)
  }
}
const statusType = (s: number) => (s >= 500 ? 'error' : s >= 400 ? 'warning' : s >= 300 ? 'info' : 'success')
const accessCols: DataTableColumns<Access> = [
  { title: () => t('common.time'), key: 'time', width: 170, render: (a) => (a.time ? datetime(a.time, locale.value) : '') },
  { title: () => t('common.ip'), key: 'ip', width: 280, render: (a) => (a.raw ? '' : ipCell(a.ip)) },
  { title: () => t('logs.request'), key: 'path', ellipsis: { tooltip: true }, render: (a) => (a.raw ? a.raw : `${a.method} ${a.path}`) },
  { title: () => t('common.status'), key: 'status', width: 80, render: (a) => (a.status ? h(NTag, { size: 'small', type: statusType(a.status) }, () => a.status) : '') },
  { title: () => t('logs.size'), key: 'bytes', width: 90, render: (a) => bytes(a.bytes) },
  { title: 'User-Agent', key: 'ua', width: 240, ellipsis: { tooltip: true } },
]
const filterByIP = (ip: string) => {
  siteQ.value = ip
  loadSite()
}
const accessRowProps = (a: Access) => ({ onDblclick: () => a.ip && filterByIP(a.ip) })

// ---- firewall drops ----
interface Drop { time: number; reason: string; proto: string; src: string; dst: string; sport?: number; dport?: number; iface: string }
interface DropSummary { by_proto: Record<string, number>; top_ips: { ip: string; count: number }[]; items: Drop[] }
const fwLog = ref<DropSummary | null>(null)
const fwQ = ref('')
const fwLoading = ref(false)
async function loadFW() {
  fwLoading.value = true
  try {
    fwLog.value = await get<DropSummary>(`/logs/firewall?limit=1000&q=${encodeURIComponent(fwQ.value)}`)
  } catch (e) {
    msg.error(e instanceof ApiError ? e.text : String(e))
  } finally {
    fwLoading.value = false
  }
}
const dropReason = (r: string) => (r.startsWith('rule') ? t('logs.fw.rule', { id: r.slice(4) }) : t(`logs.fw.reasons.${r}`))
const fwCols: DataTableColumns<Drop> = [
  { title: () => t('common.time'), key: 'time', width: 170, render: (d) => (d.time ? datetime(d.time, locale.value) : '—') },
  { title: () => t('logs.fw.reason'), key: 'reason', width: 130, render: (d) => h(NTag, { size: 'small', type: d.reason === 'policy' ? 'warning' : 'error', bordered: false }, () => dropReason(d.reason)) },
  { title: () => t('fw.proto'), key: 'proto', width: 80 },
  { title: () => t('logs.fw.src'), key: 'src', width: 280, render: (d) => ipCell(d.src) },
  { title: () => t('logs.fw.port'), key: 'dport', width: 90, render: (d) => (d.dport ? String(d.dport) : '—') },
  { title: () => t('logs.fw.iface'), key: 'iface', width: 90 },
]

// ---- containers / journal ----
const container = ref<string | null>(null)
const ctrQ = ref('')
const ctrLines = ref<string[]>([])
const ctrLoading = ref(false)
async function loadContainer() {
  if (!container.value) return
  ctrLoading.value = true
  try {
    ctrLines.value = (await get<{ lines: string[] }>(`/logs/container?name=${encodeURIComponent(container.value)}&lines=500&q=${encodeURIComponent(ctrQ.value)}`)).lines
  } catch (e) {
    fail(e)
  } finally {
    ctrLoading.value = false
  }
}
const unit = ref('')
const jQ = ref('')
const jLines = ref<string[]>([])
const jLoading = ref(false)
async function loadJournal() {
  jLoading.value = true
  try {
    jLines.value = (await get<{ lines: string[] }>(`/logs/journal?unit=${encodeURIComponent(unit.value)}&lines=500&q=${encodeURIComponent(jQ.value)}`)).lines
  } catch (e) {
    fail(e)
  } finally {
    jLoading.value = false
  }
}
const unitOptions = computed(() => [
  { label: t('logs.unit_all'), value: '' },
  { label: 'lmcwhm', value: 'lmcwhm' },
  { label: 'docker', value: 'docker' },
  { label: 'ssh', value: 'ssh' },
  { label: 'cron', value: 'cron' },
  { label: 'kernel', value: 'kernel' },
])
const containerOptions = computed(() =>
  containers.value.map((c) => ({ label: `${c.name} (${c.image})`, value: c.name })),
)

// ---- GeoIP database ----
const geoSt = ref<GeoStatus | null>(null)
async function loadGeo() {
  try {
    geoSt.value = await get<GeoStatus>('/geoip/status')
  } catch {
    geoSt.value = null
  }
}
async function updateGeo(edition: 'country' | 'city') {
  try {
    await post('/geoip/update', { edition })
    msg.info(t('geo.downloading'))
    setTimeout(loadGeo, 1000)
    const poll = window.setInterval(async () => {
      await loadGeo()
      if (!geoSt.value?.updating) clearInterval(poll)
    }, 3000)
  } catch (e) {
    fail(e)
  }
}

const loaded = new Set<string>()
function onTab(name: string) {
  if (loaded.has(name)) return
  loaded.add(name)
  ;({ operation: () => ops.load(), login: () => logins.load(), ssh: loadSSH, firewall: loadFW, site: loadSite, container: loadContainer, system: loadJournal, cron: () => cronRuns.load() } as Record<string, () => unknown>)[name]?.()
}
watch(tab, onTab)
watch([site, kind], loadSite)
watch(container, loadContainer)
watch(unit, loadJournal)
onMounted(async () => {
  onTab(tab.value)
  loadGeo()
  try {
    await loadSources()
  } catch {
    /* sources optional */
  }
})
function refresh() {
  ;({ operation: () => ops.load(), login: () => logins.load(), ssh: loadSSH, firewall: loadFW, site: loadSite, container: loadContainer, system: loadJournal, cron: () => cronRuns.load() } as Record<string, () => unknown>)[tab.value]?.()
}
</script>

<template>
  <n-space justify="end" align="center" :size="8" style="margin-bottom: -8px">
    <span v-if="geoSt" style="font-size: 12px; opacity: 0.7">
      {{ geoSt.edition ? t('geo.status', { edition: t(`geo.editions.${geoSt.edition}`), date: geoSt.built ? datetime(geoSt.built, locale).split(' ')[0] : '—' }) : t('geo.none') }}
    </span>
    <n-button size="tiny" quaternary :loading="geoSt?.updating" @click="updateGeo(geoSt?.edition === 'city' ? 'country' : 'city')">
      {{ geoSt?.edition === 'city' ? t('geo.use_country') : t('geo.use_city') }}
    </n-button>
    <n-button size="small" @click="refresh">{{ t('common.refresh') }}</n-button>
  </n-space>
  <n-tabs v-model:value="tab" type="line" animated>

    <n-tab-pane name="operation" :tab="t('logs.tab_operation')">
      <n-data-table remote size="small" :columns="opCols" :data="ops.rows.value" :loading="ops.loading.value" :pagination="ops.pagination.value" :scroll-x="1000" @update:page="ops.load" />
    </n-tab-pane>
    <n-tab-pane name="login" :tab="t('logs.tab_login')">
      <n-data-table remote size="small" :columns="loginCols" :data="logins.rows.value" :loading="logins.loading.value" :pagination="logins.pagination.value" :scroll-x="1000" @update:page="logins.load" />
    </n-tab-pane>

    <n-tab-pane name="ssh" :tab="t('logs.tab_ssh')">
      <n-space style="margin-bottom: 12px">
        <n-input v-model:value="sshQ" clearable :placeholder="t('logs.filter_user_ip')" style="width: 260px" @keyup.enter="loadSSH" />
        <n-button @click="loadSSH">{{ t('logs.search') }}</n-button>
        <AiButton :prompt="t('ai.prompt_ssh')" />
      </n-space>
      <n-grid cols="1 l:3" responsive="screen" :x-gap="16" :y-gap="16">
        <n-gi span="1 l:2">
          <n-data-table size="small" :columns="sshCols" :data="ssh?.items ?? []" :loading="sshLoading" :pagination="{ pageSize: 20 }" :scroll-x="760" />
        </n-gi>
        <n-gi>
          <n-card size="small">
            <n-space justify="space-around">
              <n-statistic :label="t('logs.ssh.accepted')" :value="ssh?.accepted ?? 0" />
              <n-statistic :label="t('logs.ssh.failed')" :value="ssh?.failed ?? 0" />
            </n-space>
          </n-card>
          <n-card size="small" :title="t('logs.ssh.top')" style="margin-top: 12px">
            <n-data-table size="small" :columns="topCols" :data="ssh?.top_ips ?? []" />
          </n-card>
        </n-gi>
      </n-grid>
    </n-tab-pane>

    <n-tab-pane name="firewall" :tab="t('logs.tab_firewall')">
      <n-space style="margin-bottom: 12px">
        <n-input v-model:value="fwQ" clearable :placeholder="t('logs.fw.filter')" style="width: 260px" @keyup.enter="loadFW" />
        <n-button @click="loadFW">{{ t('logs.search') }}</n-button>
      </n-space>
      <n-grid cols="1 l:3" responsive="screen" :x-gap="16" :y-gap="16">
        <n-gi span="1 l:2">
          <n-data-table size="small" :columns="fwCols" :data="fwLog?.items ?? []" :loading="fwLoading" :pagination="{ pageSize: 20 }" :scroll-x="840" />
        </n-gi>
        <n-gi>
          <n-card size="small">
            <n-space justify="space-around">
              <n-statistic v-for="p in ['TCP', 'UDP', 'ICMP']" :key="p" :label="p" :value="(fwLog?.by_proto[p] ?? 0) + (p === 'ICMP' ? fwLog?.by_proto.ICMPv6 ?? 0 : 0)" />
            </n-space>
          </n-card>
          <n-card size="small" :title="t('logs.ssh.top')" style="margin-top: 12px">
            <n-data-table size="small" :columns="topCols" :data="fwLog?.top_ips ?? []" />
          </n-card>
          <p class="fw-note">{{ t('logs.fw.note') }}</p>
        </n-gi>
      </n-grid>
    </n-tab-pane>

    <n-tab-pane name="site" :tab="t('logs.tab_site')">
      <n-space style="margin-bottom: 12px" align="center">
        <n-select v-model:value="site" :options="sites.map((s) => ({ label: s, value: s }))" filterable style="width: 240px" :placeholder="t('logs.no_sites')" />
        <n-radio-group v-model:value="kind" size="small">
          <n-radio-button value="access">{{ t('logs.access') }}</n-radio-button>
          <n-radio-button value="error">{{ t('logs.error') }}</n-radio-button>
        </n-radio-group>
        <n-input v-model:value="siteQ" clearable :placeholder="t('logs.filter_kw')" style="width: 220px" @keyup.enter="loadSite" />
        <n-input-number v-model:value="siteLines" :min="50" :max="5000" :step="100" style="width: 120px" />
        <n-button @click="loadSite">{{ t('logs.search') }}</n-button>
        <AiButton v-if="site" :prompt="t(kind === 'error' ? 'ai.prompt_site_error' : 'ai.prompt_site_access', { domain: site })" />
        <n-button tag="a" :href="site ? `/api/v1/logs/site/download?domain=${encodeURIComponent(site)}&kind=${kind}` : undefined" :disabled="!site">{{ t('logs.download') }}</n-button>
        <n-popconfirm @positive-click="clearSite">
          <template #trigger><n-button type="error" ghost :disabled="!site">{{ t('logs.clear') }}</n-button></template>
          {{ t('logs.clear_confirm') }}
        </n-popconfirm>
        <span style="font-size: 12px; opacity: 0.6">{{ bytes(siteSize) }}</span>
      </n-space>
      <template v-if="kind === 'access'">
        <p style="font-size: 12px; opacity: 0.6; margin: 0 0 8px">{{ t('logs.dblclick_ip') }}</p>
        <n-data-table size="small" :columns="accessCols" :data="access" :loading="siteLoading" :pagination="{ pageSize: 50 }" :row-props="accessRowProps" :scroll-x="1200" />
      </template>
      <div v-else class="lines"><n-code :code="errLines.join('\n') || '—'" word-wrap /></div>
    </n-tab-pane>

    <n-tab-pane name="container" :tab="t('logs.tab_container')">
      <n-space style="margin-bottom: 12px">
        <n-select v-model:value="container" :options="containerOptions" filterable style="width: 340px" />
        <n-input v-model:value="ctrQ" clearable :placeholder="t('logs.filter_kw')" style="width: 220px" @keyup.enter="loadContainer" />
        <n-button :loading="ctrLoading" @click="loadContainer">{{ t('logs.search') }}</n-button>
      </n-space>
      <div class="lines"><n-code :code="ctrLines.join('\n') || '—'" word-wrap /></div>
    </n-tab-pane>

    <n-tab-pane name="system" :tab="t('logs.tab_system')">
      <n-space style="margin-bottom: 12px">
        <n-select v-model:value="unit" :options="unitOptions" style="width: 200px" />
        <n-input v-model:value="jQ" clearable :placeholder="t('logs.filter_kw')" style="width: 220px" @keyup.enter="loadJournal" />
        <n-button :loading="jLoading" @click="loadJournal">{{ t('logs.search') }}</n-button>
      </n-space>
      <div class="lines"><n-code :code="jLines.join('\n') || '—'" word-wrap /></div>
    </n-tab-pane>

    <n-tab-pane name="cron" :tab="t('logs.tab_cron')">
      <n-data-table remote size="small" :columns="cronCols" :data="cronRuns.rows.value" :loading="cronRuns.loading.value" :pagination="cronRuns.pagination.value" :scroll-x="900" @update:page="cronRuns.load" />
    </n-tab-pane>
  </n-tabs>
</template>

<style scoped>
.fw-note {
  font-size: 12px;
  opacity: 0.6;
  margin-top: 12px;
}
.lines {
  max-height: 65vh;
  overflow: auto;
  font-size: 12px;
}
</style>
