<script setup lang="ts">
// Website settings window (like aaPanel's 站點修改): a menu on the left —
// domains, subdirectory binding, directories, access, traffic, rewrite
// rules, index files, nginx config, SSL, PHP, redirects, reverse proxies,
// hotlink, tamper protection, security, logs, alerts and the rest.
import { computed, h, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import {
  NAlert, NButton, NCard, NCheckbox, NDataTable, NDynamicInput, NDynamicTags, NEmpty, NForm, NFormItem, NInput, NInputNumber, NModal,
  NPopconfirm, NSelect, NSpace, NSwitch, NTabPane, NTabs, NTag, useMessage, type DataTableColumns,
} from 'naive-ui'
import { get, patch, post, put, ApiError } from '../api/client'
import type { FileEntry, InstalledApp, PathProxy, Site, SiteAdvanced } from '../api/types'
import { datetime } from '../format'
import { CodeEditor } from './code-editor'
import DomainEditor from './DomainEditor.vue'
import SiteWordPress from './SiteWordPress.vue'
import SecEvents from './SecEvents.vue'

const props = defineProps<{ site: Site | null; section?: string; phpVersions: string[]; hasCF: boolean }>()
const emit = defineEmits<{ close: []; task: [id: string, title: string]; changed: [] }>()
const { t, locale } = useI18n()
const router = useRouter()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))

const s = computed(() => props.site!)
const isPHP = computed(() => s.value?.type === 'php')
const isProject = computed(() => ['node', 'python', 'java', 'tomcat'].includes(s.value?.type ?? ''))
const sections = computed(() =>
  [
    ['domains', t('ss.domains')], ['subdir', t('ss.subdir')], ['dir', t('ss.dir')], ['access', t('ss.access')], ['traffic', t('ss.traffic')],
    ['rewrite', t('ss.rewrite')], ['index', t('ss.index')], ['config', t('ss.config')], ['ssl', 'SSL'],
    ...(isPHP.value ? [['php', 'PHP']] : []), ...(isProject.value ? [['project', t('site.tab_project')]] : []),
    ['redirect', t('ss.redirect')], ['proxy', t('ss.proxy')], ['hotlink', t('ss.hotlink')], ['tamper', t('fm.tamper')],
    ['security', t('ss.security')], ['logs', t('ss.logs')], ['alert', t('ss.alert')], ['other', t('ss.other')],
  ].map(([key, label]) => ({ key, label })),
)
const sec = ref('domains')
const narrow = ref(window.matchMedia('(max-width: 767px)').matches)
window.matchMedia('(max-width: 767px)').addEventListener?.('change', (e) => (narrow.value = e.matches))

// Draft of the advanced settings (saved as a whole by the sections that use it).
const blankAdv = (): SiteAdvanced => ({
  autoindex: false, allow_ips: [], deny_ips: [], deny_ua: [], rate_limit: 0, rate_burst: 0, conn_limit: 0, error_pages: false,
  block_xmlrpc: false, open_basedir: false, hotlink: [], max_body_mb: 0, redirects: [], varnish: false, varnish_ttl: 120, varnish_exclude: [],
  no_access_log: false, basic_auth: null, auth_dirs: [], server_conn: 0, limit_rate_kb: 0, hotlink_ext: [], hotlink_deny_empty: false, proxies: [],
})
const a = ref<SiteAdvanced>(blankAdv())
const rewrite = ref('')
const indexText = ref('')
const custom = ref('')
const vhost = ref('')
const runDir = ref('')
const php = ref('')
const all = ref<Site[]>([])
watch(() => props.site, (site, old) => {
  if (!site) return
  if (!old || old.id !== site.id) sec.value = props.section || 'domains'
  a.value = { ...blankAdv(), ...JSON.parse(JSON.stringify(site.advanced ?? {})) }
  if (a.value.basic_auth) a.value.basic_auth = { ...a.value.basic_auth, password: '' }
  rewrite.value = site.rewrite ?? ''
  indexText.value = (site.index_files?.length ? site.index_files : ['index.php', 'index.html', 'index.htm', 'default.php', 'default.htm', 'default.html']).join('\n')
  custom.value = site.custom_conf ?? ''
  runDir.value = site.run_dir || '/'
  php.value = site.php_version
  loadVhost()
  get<{ items: Site[] }>('/sites').then((r) => (all.value = r.items)).catch(() => {})
}, { immediate: true })
watch(() => props.section, (v) => v && (sec.value = v))
async function loadVhost() {
  if (!props.site) return
  vhost.value = (await get<{ config: string }>(`/sites/${props.site.id}/vhost`).catch(() => ({ config: '' }))).config
}

async function save(body: Record<string, unknown>, title: string) {
  try {
    const r = await patch<{ task_id: string }>(`/sites/${s.value.id}`, body)
    emit('task', r.task_id, `${title} · ${s.value.domain}`)
    return true
  } catch (e) {
    fail(e)
    return false
  }
}
const saveAdv = (title: string) => save({ advanced: a.value }, title)

// ---- domains ----
const newHosts = ref('')
const probes = ref<Record<string, string>>({})
const hosts = computed(() => (props.site ? [s.value.domain, ...s.value.aliases] : []))
const hostRows = computed(() => hosts.value.map((host) => ({ host })))
const rowHost = (r: { host: string }) => r.host
function addHosts() {
  const add = newHosts.value.split(/[\s,]+/).map((x) => x.trim().toLowerCase().replace(/:80$/, '')).filter(Boolean)
  if (!add.length) return
  save({ aliases: [...new Set([...s.value.aliases, ...add])] }, t('ss.domains')).then((ok) => ok && (newHosts.value = ''))
}
async function probe(host: string) {
  probes.value[host] = '…'
  try {
    const r = await post<{ ok: boolean; status?: number; ms?: number; error?: string }>(`/sites/${s.value.id}/probe`, { host, https: s.value.ssl_mode !== 'none' })
    probes.value[host] = r.ok ? `${r.status} · ${r.ms} ms` : `✕ ${r.error ?? ''}`
  } catch (e) {
    probes.value[host] = '✕'
    fail(e)
  }
}
const hostCols = computed<DataTableColumns<{ host: string }>>(() => [
  { title: () => t('site.domain'), key: 'host', render: (r) => h('a', { href: `${s.value.ssl_mode !== 'none' ? 'https' : 'http'}://${r.host}`, target: '_blank', rel: 'noopener' }, r.host) },
  { title: () => t('ss.port'), key: 'port', width: 110, render: () => (s.value.ssl_mode !== 'none' ? '80 / 443' : '80') },
  { title: () => t('ss.speed'), key: 'speed', width: 140, render: (r) => probes.value[r.host] ?? '' },
  {
    title: () => t('common.actions'),
    key: 'ops',
    width: 120,
    align: 'right',
    render: (r) =>
      h(NSpace, { size: 10, justify: 'end' }, () => [
        h('a', { class: 'op', onClick: () => probe(r.host) }, t('ss.test')),
        r.host === s.value.domain
          ? null
          : h(NPopconfirm, { onPositiveClick: () => save({ aliases: s.value.aliases.filter((x) => x !== r.host) }, t('ss.domains')) },
            { trigger: () => h('a', { class: 'op danger' }, t('common.delete')), default: () => t('ss.remove_host', { host: r.host }) }),
      ]),
  },
])
const editingDomain = ref<{ id: number; tab: string } | null>(null)

// ---- subdirectory binding ----
const subdirs = computed(() => all.value.filter((x) => x.target === 'link' && x.target_site === s.value?.id && x.target_subdir))
const dirs = ref<string[]>([])
const bind = ref({ domain: '', dir: '' })
watch(sec, async (v) => {
  if (v === 'subdir' && props.site) {
    const r = await get<{ items: FileEntry[] }>(`/files/list?path=${encodeURIComponent(s.value.root)}`).catch(() => ({ items: [] as FileEntry[] }))
    dirs.value = r.items.filter((e) => e.is_dir).map((e) => '/' + e.name)
  }
  if (v === 'logs') loadLog()
  if (v === 'tamper') loadTamper()
  if (v === 'alert') loadProbes()
  if (v === 'rewrite' && !rewrites.value) loadRewrites()
  if (v === 'config') loadVhost()
})
async function addBinding() {
  try {
    const r = await post<{ task_id: string }>('/sites', {
      domain: bind.value.domain.trim().toLowerCase(), type: 'proxy', target: 'link', target_site: s.value.id, target_subdir: bind.value.dir, ssl: 'none',
    })
    emit('task', r.task_id, `${t('ss.subdir')} · ${bind.value.domain}`)
    bind.value = { domain: '', dir: '' }
  } catch (e) {
    fail(e)
  }
}
async function removeBinding(x: Site) {
  try {
    const r = await post<{ task_id: string }>(`/sites/${x.id}/delete`, { files: false, dns: false })
    emit('task', r.task_id, `${t('common.delete')} · ${x.domain}`)
  } catch (e) {
    fail(e)
  }
}

// ---- password protection ----
const authOn = ref(false)
watch(() => a.value.basic_auth, (u) => (authOn.value = !!u?.user), { immediate: true })
function setAuth(on: boolean) {
  authOn.value = on
  a.value.basic_auth = on ? { user: a.value.basic_auth?.user || 'admin', password: '' } : null
  if (!on) saveAdv(t('ss.password'))
}

// ---- traffic presets (like aaPanel) ----
const plans: Record<string, [number, number, number]> = { bbs: [300, 25, 512], image: [200, 10, 1024], download: [50, 3, 2048], shop: [500, 10, 2048], portal: [400, 15, 1024], corp: [100, 10, 512] }
const plan = ref<string | null>(null)
function applyPlan(k: string) {
  plan.value = k
  ;[a.value.server_conn, a.value.conn_limit, a.value.limit_rate_kb] = plans[k]
}
const trafficOn = computed({
  get: () => !!(a.value.server_conn || a.value.conn_limit || a.value.limit_rate_kb),
  set: (v: boolean) => (v ? applyPlan(plan.value ?? 'bbs') : ((a.value.server_conn = 0), (a.value.conn_limit = 0), (a.value.limit_rate_kb = 0))),
})

// ---- rewrite ----
const rewrites = ref<{ names: string[]; builtin: Record<string, string>; custom: Record<string, string> } | null>(null)
const rwPick = ref('0.current')
async function loadRewrites() {
  rewrites.value = await get<{ names: string[]; builtin: Record<string, string>; custom: Record<string, string> }>('/sites/rewrites').catch(() => null)
}
const rwOptions = computed(() => [
  { label: t('ss.rw_current'), value: '0.current' },
  ...(rewrites.value?.names ?? []).map((n) => ({ label: n, value: 'b:' + n })),
  ...Object.keys(rewrites.value?.custom ?? {}).map((n) => ({ label: `★ ${n}`, value: 'c:' + n })),
])
function pickRewrite(v: string) {
  rwPick.value = v
  if (v === '0.current') rewrite.value = s.value.rewrite ?? ''
  else if (v.startsWith('b:')) rewrite.value = rewrites.value?.builtin[v.slice(2)] ?? ''
  else rewrite.value = rewrites.value?.custom[v.slice(2)] ?? ''
}
async function saveTemplate() {
  const name = prompt(t('ss.rw_name'))?.trim()
  if (!name) return
  try {
    rewrites.value!.custom = (await put<{ custom: Record<string, string> }>('/sites/rewrites', { name, body: rewrite.value })).custom
    msg.success(t('common.saved'))
  } catch (e) {
    fail(e)
  }
}

// ---- reverse proxies ----
const proxyEdit = ref<{ i: number; p: PathProxy } | null>(null)
function editProxy(i: number) {
  proxyEdit.value = { i, p: i < 0 ? { name: '', path: '/api/', target: 'http://127.0.0.1:3000', host: '', enabled: true } : { ...a.value.proxies![i] } }
}
function saveProxy() {
  const e = proxyEdit.value!
  const list = [...(a.value.proxies ?? [])]
  if (e.i < 0) list.push(e.p)
  else list[e.i] = e.p
  a.value.proxies = list
  proxyEdit.value = null
  saveAdv(t('ss.proxy'))
}
const proxyCols = computed<DataTableColumns<PathProxy>>(() => [
  { title: () => t('ss.name'), key: 'name' },
  { title: () => t('ss.path'), key: 'path', render: (p) => h('code', p.path) },
  { title: () => t('ss.target'), key: 'target', ellipsis: { tooltip: true } },
  { title: () => t('common.status'), key: 'enabled', width: 80, render: (p, i) => h(NSwitch, { size: 'small', value: p.enabled, 'onUpdate:value': (v: boolean) => { a.value.proxies![i].enabled = v; saveAdv(t('ss.proxy')) } }) },
  {
    title: () => t('common.actions'),
    key: 'ops',
    width: 110,
    align: 'right',
    render: (_, i) =>
      h(NSpace, { size: 10, justify: 'end' }, () => [
        h('a', { class: 'op', onClick: () => editProxy(i) }, t('common.edit')),
        h('a', { class: 'op danger', onClick: () => { a.value.proxies!.splice(i, 1); saveAdv(t('ss.proxy')) } }, t('common.delete')),
      ]),
  },
])

// ---- tamper ----
interface TamperRule { id: number; site_id: number; path: string; mode: string; enabled: boolean; state: string; files: number }
const tampers = ref<TamperRule[] | null>(null)
async function loadTamper() {
  tampers.value = (await get<{ items: TamperRule[] }>('/tamper').catch(() => ({ items: null as unknown as TamperRule[] }))).items
}
const siteTampers = computed(() => (tampers.value ?? []).filter((r) => r.site_id === s.value.id || r.path === s.value.root || r.path.startsWith(s.value.root + '/')))
async function tamperToggle(r: TamperRule, on: boolean) {
  try {
    await post(`/tamper/${r.id}/enabled`, { on })
    loadTamper()
  } catch (e) {
    fail(e)
  }
}
async function tamperCreate() {
  try {
    await post('/tamper', { site_id: s.value.id, path: s.value.root, mode: 'watch', exts: ['php', 'js', 'html', 'htm'], exclude: ['wp-content/uploads', 'cache'], restore: true, enabled: true })
    msg.success(t('common.done'))
    loadTamper()
  } catch (e) {
    fail(e)
  }
}

// ---- logs ----
interface AccessLine { time: number; ip: string; method: string; path: string; status: number; ua: string; block?: string }
const logKind = ref<'access' | 'error'>('access')
const logQ = ref('')
const logLines = ref<AccessLine[] | string[]>([])
async function loadLog() {
  try {
    const r = await get<{ items?: AccessLine[]; lines?: string[] }>(`/logs/site?domain=${encodeURIComponent(s.value.domain)}&kind=${logKind.value}&lines=500&q=${encodeURIComponent(logQ.value)}`)
    logLines.value = (r.items ?? r.lines ?? []).reverse()
  } catch (e) {
    fail(e)
  }
}
watch(logKind, loadLog)
const logCols = computed<DataTableColumns<AccessLine>>(() => [
  { title: () => t('common.time'), key: 'time', width: 165, render: (l) => (l.time ? datetime(l.time, locale.value) : '') },
  { title: () => t('common.ip'), key: 'ip', width: 140 },
  { title: () => t('ss.request'), key: 'path', ellipsis: { tooltip: true }, render: (l) => `${l.method} ${l.path}` },
  { title: () => t('common.status'), key: 'status', width: 70 },
  { title: 'User-Agent', key: 'ua', ellipsis: { tooltip: true } },
])

// ---- alerts: availability probe of the site ----
interface Probe { id: number; name: string; type: string; target: string; enabled: boolean; state: string; latency: number; interval: number }
const probesList = ref<Probe[] | null>(null)
async function loadProbes() {
  probesList.value = (await get<{ items: Probe[] }>('/probes').catch(() => ({ items: null as unknown as Probe[] }))).items
}
const siteURL = computed(() => `${s.value.ssl_mode !== 'none' ? 'https' : 'http'}://${s.value.domain}/`)
const siteProbe = computed(() => (probesList.value ?? []).find((p) => p.type === 'http' && p.target.replace(/\/$/, '') === siteURL.value.replace(/\/$/, '')))
async function probeToggle(on: boolean) {
  try {
    const p = siteProbe.value
    if (p) await put(`/probes/${p.id}`, { ...p, enabled: on })
    else await post('/probes', { name: s.value.domain, type: 'http', target: siteURL.value, interval: 60, timeout: 10, expect_status: 200, fails: 3, enabled: true })
    loadProbes()
  } catch (e) {
    fail(e)
  }
}
const expiry = ref<number | null>(null)
watch(() => props.site?.expires_at, (v) => (expiry.value = v ? v * 1000 : null), { immediate: true })

// ---- project / PHP ----
const runtimes = ref<InstalledApp[]>([])
const proj = ref({ runtime: '', command: '', port: 0 })
watch(() => props.site, (x) => {
  if (!x) return
  proj.value = { runtime: x.runtime_version, command: x.command, port: x.app_port }
  if (isProject.value) get<{ items: InstalledApp[] }>('/apps').then((r) => (runtimes.value = r.items.filter((i) => i.kind === 'runtime' && i.app === x.type))).catch(() => {})
}, { immediate: true })
async function composer(action: string) {
  try {
    const r = await post<{ task_id: string }>(`/sites/${s.value.id}/composer`, { action })
    emit('task', r.task_id, `composer ${action}`)
  } catch (e) {
    fail(e)
  }
}
async function purge() {
  try {
    await post('/varnish/purge', { site_id: s.value.id })
    msg.success(t('common.done'))
  } catch (e) {
    fail(e)
  }
}
const days = computed(() => (s.value?.cert_not_after ? Math.floor((s.value.cert_not_after * 1000 - Date.now()) / 86400000) : 0))
const title = computed(() => (props.site ? `${t('ss.title', { domain: s.value.domain })} — ${t('ss.added', { time: datetime(s.value.created_at, locale.value) })}` : ''))
</script>

<template>
  <n-modal :show="!!site" @update:show="(v: boolean) => !v && emit('close')">
    <n-card v-if="site" class="win" :title="title" closable :bordered="false" size="small" content-style="padding: 0; display: flex; min-height: 0" @close="emit('close')">
      <div class="body">
        <nav v-if="!narrow" class="menu">
          <button v-for="x in sections" :key="x.key" type="button" :class="{ on: sec === x.key }" @click="sec = x.key">{{ x.label }}</button>
        </nav>
        <n-select v-else v-model:value="sec" :options="sections.map((x) => ({ label: x.label, value: x.key }))" class="menu-select" />
        <section class="pane">
          <!-- 域名管理 -->
          <template v-if="sec === 'domains'">
            <div class="row">
              <n-input v-model:value="newHosts" type="textarea" :rows="4" :placeholder="t('ss.hosts_ph')" class="grow" />
              <n-button type="primary" @click="addHosts">{{ t('ss.add') }}</n-button>
            </div>
            <p class="hint">{{ t('ss.hosts_hint') }} <a class="op" @click="editingDomain = { id: site.id, tab: 'target' }">{{ t('dom.manage_domain') }}</a></p>
            <n-data-table size="small" :columns="hostCols" :data="hostRows" :row-key="rowHost" :scroll-x="560" />
          </template>

          <!-- 子目錄綁定 -->
          <template v-else-if="sec === 'subdir'">
            <div class="row wrap">
              <n-input v-model:value="bind.domain" :placeholder="t('ss.sub_domain')" style="width: 220px" />
              <n-select v-model:value="bind.dir" :options="dirs.map((d) => ({ label: d, value: d }))" filterable tag :placeholder="t('ss.sub_dir')" style="width: 220px" />
              <n-button type="primary" :disabled="!bind.domain || !bind.dir" @click="addBinding">{{ t('ss.add') }}</n-button>
            </div>
            <p class="hint">{{ t('ss.subdir_hint') }}</p>
            <n-empty v-if="!subdirs.length" />
            <table v-else class="simple">
              <thead><tr><th>{{ t('site.domain') }}</th><th>{{ t('ss.sub_dir') }}</th><th /></tr></thead>
              <tbody>
                <tr v-for="x in subdirs" :key="x.id">
                  <td>{{ x.domain }}</td><td><code>{{ x.target_subdir }}</code></td>
                  <td class="r"><n-popconfirm @positive-click="removeBinding(x)"><template #trigger><a class="op danger">{{ t('common.delete') }}</a></template>{{ t('ss.remove_host', { host: x.domain }) }}</n-popconfirm></td>
                </tr>
              </tbody>
            </table>
          </template>

          <!-- 網站目錄 -->
          <template v-else-if="sec === 'dir'">
            <n-form label-placement="left" label-width="120">
              <n-form-item :label="t('ss.root')" :feedback="t('ss.root_hint')">
                <n-input :value="site.root" readonly style="max-width: 360px" />
                <n-button style="margin-left: 8px" @click="router.push({ path: '/files', query: { path: site.root } })">{{ t('ss.open_files') }}</n-button>
              </n-form-item>
              <n-form-item :label="t('ss.run_dir')" :feedback="t('ss.run_dir_hint')">
                <n-input v-model:value="runDir" style="max-width: 360px" placeholder="/public" />
                <n-button type="primary" style="margin-left: 8px" @click="save({ run_dir: runDir === '/' ? '' : runDir }, t('ss.run_dir'))">{{ t('common.save') }}</n-button>
              </n-form-item>
              <n-form-item v-if="isPHP" :label="t('ss.basedir')" :feedback="t('ss.basedir_hint')">
                <n-switch :value="a.open_basedir" @update:value="(v: boolean) => ((a.open_basedir = v), saveAdv(t('ss.basedir')))" />
              </n-form-item>
              <n-form-item :label="t('ss.access_log')" :feedback="t('ss.access_log_hint')">
                <n-switch :value="!a.no_access_log" @update:value="(v: boolean) => ((a.no_access_log = !v), saveAdv(t('ss.access_log')))" />
              </n-form-item>
              <n-form-item :label="t('ss.password')" :feedback="t('ss.password_hint')">
                <n-space vertical>
                  <n-switch :value="authOn" @update:value="setAuth" />
                  <n-space v-if="authOn && a.basic_auth">
                    <n-input v-model:value="a.basic_auth.user" :placeholder="t('ss.user')" style="width: 150px" />
                    <n-input v-model:value="a.basic_auth.password" type="password" show-password-on="click" :placeholder="site.advanced?.basic_auth ? t('ss.pw_keep') : t('ss.pw')" style="width: 180px" />
                    <n-button type="primary" @click="saveAdv(t('ss.password'))">{{ t('common.save') }}</n-button>
                  </n-space>
                </n-space>
              </n-form-item>
            </n-form>
          </template>

          <!-- 訪問限制 -->
          <template v-else-if="sec === 'access'">
            <n-tabs type="card" size="small">
              <n-tab-pane name="ip" :tab="t('ss.ip_rules')">
                <n-form label-placement="top">
                  <n-form-item :label="t('adv.allow_ips')" :feedback="t('adv.allow_ips_hint')"><n-dynamic-tags v-model:value="a.allow_ips" /></n-form-item>
                  <n-form-item :label="t('adv.deny_ips')"><n-dynamic-tags v-model:value="a.deny_ips" /></n-form-item>
                  <n-form-item :label="t('adv.deny_ua')" :feedback="t('adv.deny_ua_hint')"><n-dynamic-tags v-model:value="a.deny_ua" /></n-form-item>
                </n-form>
                <n-button type="primary" @click="saveAdv(t('ss.access'))">{{ t('common.save') }}</n-button>
              </n-tab-pane>
              <n-tab-pane name="dirs" :tab="t('ss.dir_auth')">
                <p class="hint">{{ t('ss.dir_auth_hint') }}</p>
                <n-dynamic-input v-model:value="a.auth_dirs" :on-create="() => ({ name: '', path: '/admin/', user: 'admin', password: '' })">
                  <template #default="{ value }">
                    <div class="row wrap">
                      <n-input v-model:value="value.name" :placeholder="t('ss.name')" style="width: 110px" />
                      <n-input v-model:value="value.path" placeholder="/admin/" style="width: 140px" />
                      <n-input v-model:value="value.user" :placeholder="t('ss.user')" style="width: 110px" />
                      <n-input v-model:value="value.password" type="password" show-password-on="click" :placeholder="t('ss.pw_keep')" style="width: 140px" />
                    </div>
                  </template>
                </n-dynamic-input>
                <n-button type="primary" style="margin-top: 8px" @click="saveAdv(t('ss.dir_auth'))">{{ t('common.save') }}</n-button>
              </n-tab-pane>
            </n-tabs>
          </template>

          <!-- 流量限制 -->
          <template v-else-if="sec === 'traffic'">
            <n-form label-placement="left" label-width="120">
              <n-form-item :label="t('ss.traffic_on')"><n-checkbox v-model:checked="trafficOn">{{ t('ss.traffic_on_label') }}</n-checkbox></n-form-item>
              <n-form-item :label="t('ss.plan')">
                <n-select :value="plan" :options="Object.keys(plans).map((k) => ({ label: t(`ss.plans.${k}`), value: k }))" style="width: 220px" @update:value="applyPlan" />
              </n-form-item>
              <n-form-item :label="t('ss.server_conn')" :feedback="t('ss.server_conn_hint')"><n-input-number v-model:value="a.server_conn" :min="0" :max="1000000" style="width: 200px" /></n-form-item>
              <n-form-item :label="t('ss.ip_conn')" :feedback="t('ss.ip_conn_hint')"><n-input-number v-model:value="a.conn_limit" :min="0" :max="100000" style="width: 200px" /></n-form-item>
              <n-form-item :label="t('ss.rate')" :feedback="t('ss.rate_hint')"><n-input-number v-model:value="a.limit_rate_kb" :min="0" :max="10000000" style="width: 200px"><template #suffix>KB/s</template></n-input-number></n-form-item>
              <n-form-item :label="t('adv.rate')" :feedback="t('adv.rate_hint')">
                <n-space align="center">
                  <n-input-number v-model:value="a.rate_limit" :min="0" :max="10000" style="width: 130px"><template #suffix>r/s</template></n-input-number>
                  <span>{{ t('adv.burst') }}</span><n-input-number v-model:value="a.rate_burst" :min="0" :max="100000" style="width: 110px" />
                </n-space>
              </n-form-item>
              <n-form-item label=" "><n-button type="primary" @click="saveAdv(t('ss.traffic'))">{{ t('ss.save_apply') }}</n-button></n-form-item>
            </n-form>
          </template>

          <!-- 偽靜態 -->
          <template v-else-if="sec === 'rewrite'">
            <div class="row wrap">
              <n-select :value="rwPick" :options="rwOptions" style="width: 220px" filterable @update:value="pickRewrite" />
              <span class="grow" />
              <a class="op" href="https://www.winginx.com/en/htaccess" target="_blank" rel="noopener">{{ t('ss.rw_convert') }}</a>
            </div>
            <CodeEditor v-model="rewrite" language="nginx" height="340px" @save="save({ rewrite }, t('ss.rewrite'))" />
            <n-space style="margin-top: 8px">
              <n-button type="primary" @click="save({ rewrite }, t('ss.rewrite'))">{{ t('common.save') }}</n-button>
              <n-button @click="saveTemplate">{{ t('ss.rw_save_tpl') }}</n-button>
            </n-space>
            <ul class="hint"><li>{{ t('ss.rw_hint1') }}</li><li>{{ t('ss.rw_hint2') }}</li></ul>
          </template>

          <!-- 默認文檔 -->
          <template v-else-if="sec === 'index'">
            <div class="row">
              <n-input v-model:value="indexText" type="textarea" :rows="8" style="max-width: 420px" />
              <n-button type="primary" @click="save({ index_files: indexText.split(/\s+/).filter(Boolean) }, t('ss.index'))">{{ t('common.save') }}</n-button>
            </div>
            <ul class="hint"><li>{{ t('ss.index_hint') }}</li></ul>
          </template>

          <!-- 配置文件 -->
          <template v-else-if="sec === 'config'">
            <p class="hint">{{ t('ss.custom_hint') }}</p>
            <CodeEditor v-model="custom" language="nginx" height="200px" placeholder="add_header X-Frame-Options SAMEORIGIN;" @save="save({ custom_conf: custom }, t('ss.config'))" />
            <n-button type="primary" style="margin: 8px 0 16px" @click="save({ custom_conf: custom }, t('ss.config'))">{{ t('common.save') }}</n-button>
            <p class="hint">{{ t('ss.generated') }}</p>
            <CodeEditor :model-value="vhost" language="nginx" height="320px" readonly />
          </template>

          <!-- SSL -->
          <template v-else-if="sec === 'ssl'">
            <n-alert :type="site.ssl_mode === 'none' ? 'warning' : days < 15 ? 'warning' : 'success'" :show-icon="false" style="margin-bottom: 12px">
              {{ site.ssl_mode === 'none' ? t('ss.ssl_none') : t('ss.ssl_on', { issuer: site.cert_issuer || site.ssl_mode, days }) }}
              <template v-if="site.cert_error"><br />{{ site.cert_error }}</template>
            </n-alert>
            <n-space vertical>
              <n-space align="center"><n-switch :value="site.force_https" :disabled="site.ssl_mode === 'none'" @update:value="(v: boolean) => save({ force_https: v }, t('ss.force_https'))" />{{ t('ss.force_https') }}</n-space>
              <n-space align="center"><n-switch :value="site.hsts" :disabled="site.ssl_mode === 'none'" @update:value="(v: boolean) => save({ hsts: v }, 'HSTS')" />HSTS</n-space>
              <n-button type="primary" @click="editingDomain = { id: site.id, tab: 'ssl' }">{{ t('ss.ssl_manage') }}</n-button>
            </n-space>
          </template>

          <!-- PHP -->
          <template v-else-if="sec === 'php'">
            <n-form label-placement="left" label-width="120">
              <n-form-item :label="t('site.php_version')" :feedback="t('site.php_switch_hint')">
                <n-select v-model:value="php" :options="phpVersions.map((v) => ({ label: `PHP ${v}`, value: v }))" style="width: 200px" />
                <n-button type="primary" style="margin-left: 8px" :disabled="php === site.php_version" @click="save({ php_version: php }, 'PHP ' + php)">{{ t('ss.switch') }}</n-button>
              </n-form-item>
            </n-form>
            <h4>Composer</h4>
            <p class="hint">{{ t('site.composer_hint') }}</p>
            <n-space>
              <n-button v-for="c in ['install', 'update', 'dump-autoload', 'validate', 'outdated']" :key="c" size="small" @click="composer(c)">composer {{ c }}</n-button>
            </n-space>
          </template>

          <!-- 項目 -->
          <template v-else-if="sec === 'project'">
            <n-form label-placement="top">
              <n-space>
                <n-form-item :label="t('site.runtime_version')">
                  <n-select v-model:value="proj.runtime" :options="runtimes.map((r) => ({ label: `${r.name} ${r.version}`, value: r.version }))" style="width: 180px" />
                </n-form-item>
                <n-form-item :label="t('site.app_port')"><n-input-number v-model:value="proj.port" :min="1024" :max="65535" :show-button="false" style="width: 140px" /></n-form-item>
              </n-space>
              <n-form-item :label="t('site.command')" :feedback="t('site.command_hint')"><n-input v-model:value="proj.command" type="textarea" :rows="2" /></n-form-item>
              <n-button type="primary" @click="save({ runtime_version: proj.runtime, command: proj.command, app_port: proj.port }, t('site.tab_project'))">{{ t('common.save') }}</n-button>
            </n-form>
          </template>

          <!-- 重定向 -->
          <template v-else-if="sec === 'redirect'">
            <p class="hint">{{ t('ss.redirect_hint') }}</p>
            <n-dynamic-input v-model:value="a.redirects" :on-create="() => ({ from: '/', to: 'https://', code: 301, keep_path: false })">
              <template #default="{ value }">
                <div class="row wrap">
                  <n-input v-model:value="value.from" placeholder="/old-path" style="width: 160px" />
                  <n-input v-model:value="value.to" placeholder="https://example.com/new" style="width: 240px" />
                  <n-select v-model:value="value.code" :options="[301, 302, 307, 308].map((c) => ({ label: String(c), value: c }))" style="width: 90px" />
                  <n-checkbox v-model:checked="value.keep_path">{{ t('ss.keep_query') }}</n-checkbox>
                </div>
              </template>
            </n-dynamic-input>
            <n-button type="primary" style="margin-top: 8px" @click="saveAdv(t('ss.redirect'))">{{ t('common.save') }}</n-button>
            <p class="hint" style="margin-top: 12px">{{ t('ss.redirect_domain') }} <a class="op" @click="router.push('/domains')">{{ t('ss.go_domains') }}</a></p>
          </template>

          <!-- 反向代理 -->
          <template v-else-if="sec === 'proxy'">
            <n-space style="margin-bottom: 8px"><n-button type="primary" @click="editProxy(-1)">{{ t('ss.proxy_add') }}</n-button></n-space>
            <n-data-table size="small" :columns="proxyCols" :data="a.proxies ?? []" :scroll-x="600" />
            <p class="hint" style="margin-top: 8px">{{ t('ss.proxy_hint') }}</p>
          </template>

          <!-- 防盜鏈 -->
          <template v-else-if="sec === 'hotlink'">
            <n-form label-placement="left" label-width="130">
              <n-form-item :label="t('ss.hl_ext')" :feedback="t('ss.hl_ext_hint')"><n-dynamic-tags v-model:value="a.hotlink_ext" /></n-form-item>
              <n-form-item :label="t('ss.hl_domains')" :feedback="t('adv.hotlink_hint')"><n-dynamic-tags v-model:value="a.hotlink" /></n-form-item>
              <n-form-item :label="t('ss.hl_empty')"><n-checkbox :checked="!a.hotlink_deny_empty" @update:checked="(v: boolean) => (a.hotlink_deny_empty = !v)">{{ t('ss.hl_empty_label') }}</n-checkbox></n-form-item>
              <n-form-item label=" "><n-button type="primary" @click="saveAdv(t('ss.hotlink'))">{{ t('common.save') }}</n-button></n-form-item>
            </n-form>
          </template>

          <!-- 防篡改 -->
          <template v-else-if="sec === 'tamper'">
            <n-alert v-if="tampers === null" type="info" :show-icon="false">{{ t('ss.no_perm') }}</n-alert>
            <template v-else>
              <p class="hint">{{ t('ss.tamper_hint') }}</p>
              <n-empty v-if="!siteTampers.length" :description="t('ss.tamper_none')"><template #extra><n-button type="primary" @click="tamperCreate">{{ t('ss.tamper_create') }}</n-button></template></n-empty>
              <table v-else class="simple">
                <thead><tr><th>{{ t('ss.path') }}</th><th>{{ t('ss.mode') }}</th><th>{{ t('common.status') }}</th><th /></tr></thead>
                <tbody>
                  <tr v-for="r in siteTampers" :key="r.id">
                    <td><code>{{ r.path }}</code></td><td>{{ r.mode }}</td><td>{{ r.state }}</td>
                    <td class="r"><n-switch size="small" :value="r.enabled" @update:value="(v: boolean) => tamperToggle(r, v)" /></td>
                  </tr>
                </tbody>
              </table>
              <n-button style="margin-top: 12px" @click="router.push('/security?tab=tamper')">{{ t('ss.tamper_more') }}</n-button>
            </template>
          </template>

          <!-- 網站安全 -->
          <template v-else-if="sec === 'security'">
            <n-space align="center" style="margin-bottom: 8px">
              <n-switch :value="!site.waf_off" @update:value="(v: boolean) => save({ waf_off: !v }, 'WAF')" />WAF（OWASP）
              <n-tag v-if="site.waf_exclude?.length" size="small">{{ t('ss.waf_excluded', { n: site.waf_exclude.length }) }}</n-tag>
              <n-button size="small" @click="router.push('/security?tab=protect')">{{ t('ss.protect_settings') }}</n-button>
            </n-space>
            <h4>{{ t('evt.title') }}</h4>
            <SecEvents :site="site.domain" />
          </template>

          <!-- 網站日誌 -->
          <template v-else-if="sec === 'logs'">
            <div class="row wrap">
              <n-select v-model:value="logKind" :options="[{ label: t('ss.log_access'), value: 'access' }, { label: t('ss.log_error'), value: 'error' }]" style="width: 150px" />
              <n-input v-model:value="logQ" clearable :placeholder="t('ss.log_search')" style="width: 200px" @keyup.enter="loadLog" />
              <n-button @click="loadLog">{{ t('common.refresh') }}</n-button>
              <n-button tag="a" :href="`/api/v1/logs/site/download?domain=${encodeURIComponent(site.domain)}&kind=${logKind}`">{{ t('files.download') }}</n-button>
            </div>
            <n-data-table v-if="logKind === 'access'" size="small" :columns="logCols" :data="logLines as AccessLine[]" :max-height="420" :scroll-x="900" virtual-scroll />
            <pre v-else class="log">{{ (logLines as string[]).join('\n') || '—' }}</pre>
          </template>

          <!-- 網站告警 -->
          <template v-else-if="sec === 'alert'">
            <n-form label-placement="left" label-width="140">
              <n-form-item :label="t('ss.uptime')" :feedback="probesList === null ? t('ss.no_perm') : t('ss.uptime_hint', { url: siteURL })">
                <n-space align="center">
                  <n-switch :disabled="probesList === null" :value="!!siteProbe?.enabled" @update:value="probeToggle" />
                  <n-tag v-if="siteProbe" size="small" :type="siteProbe.state === 'up' ? 'success' : siteProbe.state === 'down' ? 'error' : 'default'">{{ siteProbe.state }} · {{ siteProbe.latency }} ms</n-tag>
                </n-space>
              </n-form-item>
              <n-form-item :label="t('ss.expiry')" :feedback="t('ss.expiry_hint')">
                <n-space align="center">
                  <input type="date" class="date" :value="expiry ? new Date(expiry).toISOString().slice(0, 10) : ''" @change="(e) => (expiry = (e.target as HTMLInputElement).valueAsNumber || null)" />
                  <n-button type="primary" @click="save({ expires_at: expiry ? Math.floor(expiry / 1000) + 86399 : 0 }, t('ss.expiry'))">{{ t('common.save') }}</n-button>
                  <n-button v-if="site.expires_at" @click="(expiry = null), save({ expires_at: 0 }, t('ss.expiry'))">{{ t('ss.forever') }}</n-button>
                </n-space>
              </n-form-item>
              <n-form-item :label="t('ss.ssl_alert')"><span class="hint">{{ t('ss.ssl_alert_hint') }} <a class="op" @click="router.push('/alerts')">{{ t('ss.go_alerts') }}</a></span></n-form-item>
            </n-form>
          </template>

          <!-- 其他設置 -->
          <template v-else-if="sec === 'other'">
            <n-form label-placement="left" label-width="150">
              <n-form-item :label="t('adv.max_body')"><n-input-number v-model:value="a.max_body_mb" :min="0" :max="10240" style="width: 150px"><template #suffix>MB</template></n-input-number></n-form-item>
              <n-form-item :label="t('ss.behaviour')">
                <n-space vertical>
                  <n-checkbox v-model:checked="a.autoindex">{{ t('adv.autoindex') }}</n-checkbox>
                  <n-checkbox v-model:checked="a.error_pages">{{ t('adv.error_pages') }}</n-checkbox>
                  <n-checkbox v-model:checked="a.block_xmlrpc">{{ t('adv.block_xmlrpc') }}</n-checkbox>
                </n-space>
              </n-form-item>
              <template v-if="!isProject">
                <n-form-item :label="t('adv.varnish')">
                  <n-space vertical>
                    <n-checkbox v-model:checked="a.varnish">{{ t('adv.varnish_on') }}</n-checkbox>
                    <n-input-number v-if="a.varnish" v-model:value="a.varnish_ttl" :min="0" :max="2592000" style="width: 150px"><template #suffix>s</template></n-input-number>
                    <n-dynamic-tags v-if="a.varnish" v-model:value="a.varnish_exclude" />
                    <n-button v-if="site.advanced?.varnish" size="small" @click="purge">{{ t('adv.varnish_purge') }}</n-button>
                  </n-space>
                </n-form-item>
              </template>
              <n-form-item label=" "><n-button type="primary" @click="saveAdv(t('ss.other'))">{{ t('common.save') }}</n-button></n-form-item>
            </n-form>
            <template v-if="isPHP">
              <h4>WordPress</h4>
              <SiteWordPress :site="site" @task="(id: string, ti: string) => emit('task', id, ti)" />
            </template>
          </template>
        </section>
      </div>
    </n-card>
  </n-modal>

  <n-modal :show="!!proxyEdit" @update:show="(v: boolean) => !v && (proxyEdit = null)">
    <n-card v-if="proxyEdit" :title="proxyEdit.i < 0 ? t('ss.proxy_add') : t('common.edit')" style="width: min(480px, calc(100vw - 32px))" closable @close="proxyEdit = null">
      <n-form label-placement="top">
        <n-form-item :label="t('ss.name')"><n-input v-model:value="proxyEdit.p.name" /></n-form-item>
        <n-form-item :label="t('ss.path')" :feedback="t('ss.proxy_path_hint')"><n-input v-model:value="proxyEdit.p.path" /></n-form-item>
        <n-form-item :label="t('ss.target')"><n-input v-model:value="proxyEdit.p.target" placeholder="http://127.0.0.1:3000" /></n-form-item>
        <n-form-item :label="t('ss.send_host')" :feedback="t('ss.send_host_hint')"><n-input v-model:value="proxyEdit.p.host" placeholder="$host" /></n-form-item>
      </n-form>
      <n-space justify="end"><n-button @click="proxyEdit = null">{{ t('common.cancel') }}</n-button><n-button type="primary" @click="saveProxy">{{ t('common.save') }}</n-button></n-space>
    </n-card>
  </n-modal>
  <DomainEditor
    :site-id="editingDomain?.id ?? null" :initial-tab="editingDomain?.tab" :has-c-f="hasCF" @close="editingDomain = null"
    @changed="emit('changed')" @task="(id: string, ti: string) => emit('task', id, ti)"
  />
</template>

<style scoped>
.win {
  width: min(1000px, calc(100vw - 16px));
  height: min(760px, calc(100dvh - 32px));
  display: flex;
  flex-direction: column;
}
.win :deep(.n-card__content) {
  flex: 1;
}
.body {
  flex: 1;
  display: flex;
  min-height: 0;
}
.menu {
  width: 130px;
  flex: none;
  overflow-y: auto;
  background: rgba(128, 128, 128, 0.07);
  padding: 6px 0;
}
.menu button {
  display: block;
  width: 100%;
  border: 0;
  background: transparent;
  color: inherit;
  font: inherit;
  text-align: left;
  padding: 9px 16px;
  cursor: pointer;
}
.menu button.on {
  background: rgba(32, 165, 58, 0.1);
  color: #20a53a;
  font-weight: 500;
}
.menu-select {
  margin: 8px 12px 0;
}
.body:has(.menu-select) {
  flex-direction: column;
}
.pane {
  flex: 1;
  min-width: 0;
  overflow: auto;
  padding: 16px;
}
.row {
  display: flex;
  gap: 8px;
  align-items: flex-start;
  margin-bottom: 8px;
}
.row.wrap {
  flex-wrap: wrap;
  align-items: center;
}
.grow {
  flex: 1;
  min-width: 0;
}
.hint {
  font-size: 12px;
  opacity: 0.7;
  margin: 4px 0 10px;
  padding-left: 0;
}
ul.hint {
  padding-left: 18px;
}
:deep(.op) {
  color: #20a53a;
  cursor: pointer;
}
:deep(.op.danger) {
  color: #d03050;
}
.simple {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}
.simple th,
.simple td {
  text-align: left;
  padding: 8px;
  border-bottom: 1px solid rgba(128, 128, 128, 0.15);
}
.simple .r {
  text-align: right;
}
.log {
  max-height: 420px;
  overflow: auto;
  font-size: 12px;
  padding: 10px;
  border-radius: 6px;
  background: #1e1e1e;
  color: #ddd;
  white-space: pre-wrap;
  word-break: break-all;
  margin: 0;
}
.date {
  font: inherit;
  padding: 4px 8px;
  border: 1px solid rgba(128, 128, 128, 0.35);
  border-radius: 4px;
  background: transparent;
  color: inherit;
}
h4 {
  margin: 18px 0 8px;
}
</style>
