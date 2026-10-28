<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NCard, NDataTable, NForm, NFormItem, NInput, NInputGroup, NModal, NPopconfirm, NRadioButton, NRadioGroup,
  NSelect, NSpace, NTabPane, NTabs, NTag, NText, NTooltip, useMessage, type DataTableColumns,
} from 'naive-ui'
import { get, patch, ApiError } from '../api/client'
import type { Container, Route, RouteResolution, Site, WebConfig, WebStatus } from '../api/types'
import { datetime } from '../format'
import SiteCreate from '../components/SiteCreate.vue'
import SiteDrawer from '../components/SiteDrawer.vue'
import TaskModal from '../components/TaskModal.vue'
import DnsZones from '../components/DnsZones.vue'

const { t, locale } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))

const routes = ref<Route[]>([])
const sites = ref<Site[]>([])
const containers = ref<Container[]>([])
const status = ref<WebStatus | null>(null)
const cfg = ref<WebConfig | null>(null)
const loading = ref(false)
const checked = ref(false)
const search = ref('')
const task = ref<{ id: string; title: string } | null>(null)
const selected = ref<Site | null>(null)
const createPreset = ref<Record<string, unknown> | null>(null)

async function load(check = checked.value) {
  loading.value = true
  try {
    const [r, s, st, c] = await Promise.all([
      get<{ items: Route[] }>(`/routes${check ? '?check=1' : ''}`),
      get<{ items: Site[] }>('/sites'),
      get<WebStatus>('/web/status'),
      get<WebConfig>('/web/config'),
    ])
    routes.value = r.items ?? []
    sites.value = s.items
    status.value = st
    cfg.value = c
    checked.value = check
    if (selected.value) selected.value = s.items.find((x) => x.id === selected.value!.id) ?? null
  } catch (e) {
    fail(e)
  } finally {
    loading.value = false
  }
}
onMounted(() => load(true))

const filtered = computed(() => {
  const q = search.value.trim().toLowerCase()
  return q ? routes.value.filter((r) => r.host.includes(q) || r.domain.includes(q) || r.upstream.includes(q) || r.container.includes(q)) : routes.value
})
const warned = computed(() => routes.value.filter((r) => r.warnings.length).length)

// ---- route tester ----
const probe = ref('')
const resolved = ref<RouteResolution | null>(null)
async function resolve() {
  if (!probe.value.trim()) return
  try {
    resolved.value = await get<RouteResolution>(`/routes/resolve?host=${encodeURIComponent(probe.value.trim())}`)
  } catch (e) {
    fail(e)
  }
}

// ---- add a hostname ----
type Kind = 'alias' | 'site' | 'docker' | 'server' | 'passthrough'
const add = ref<{ host: string; kind: Kind; site: number | null; container: string | null; port: number | null } | null>(null)
function openAdd(host = '') {
  add.value = { host, kind: 'alias', site: null, container: null, port: null }
}
const typeName = (ty: string) => ({ php: 'PHP', static: t('site.static'), node: 'Node.js', python: 'Python', java: 'Java', proxy: t('site.proxy') })[ty] ?? ty
const siteOptions = computed(() => sites.value.map((s) => ({ label: `${s.domain} (${typeName(s.type)})`, value: s.id })))
const containerOptions = computed(() =>
  containers.value.filter((c) => c.host_ports.length).map((c) => ({ label: `${c.name} (${c.image})`, value: c.name })),
)
const portOptions = computed(() => {
  const c = containers.value.find((x) => x.name === add.value?.container)
  return (c?.host_ports ?? []).map((p) => ({ label: String(p), value: p }))
})
async function loadContainers() {
  try {
    containers.value = (await get<{ items: Container[] }>('/docker/containers')).items
  } catch {
    containers.value = []
  }
}

async function submitAdd() {
  const a = add.value!
  const host = a.host.trim().toLowerCase()
  if (!host) return
  if (a.kind === 'alias') {
    const s = sites.value.find((x) => x.id === a.site)
    if (!s) return
    try {
      const r = await patch<{ task_id: string }>(`/sites/${s.id}`, { aliases: [...s.aliases, host] })
      add.value = null
      task.value = { id: r.task_id, title: `${t('route.add')} · ${host} → ${s.domain}` }
    } catch (e) {
      fail(e)
    }
    return
  }
  const preset: Record<string, unknown> = { domain: host }
  if (a.kind === 'docker') Object.assign(preset, { type: 'proxy', proxy_target: `http://127.0.0.1:${a.port}`, remark: `docker: ${a.container}` })
  if (a.kind === 'server') Object.assign(preset, { type: 'proxy' })
  if (a.kind === 'passthrough') Object.assign(preset, { type: 'proxy', passthrough: true })
  add.value = null
  createPreset.value = preset
}
const addReady = computed(() => {
  const a = add.value
  if (!a?.host.trim()) return false
  if (a.kind === 'alias') return !!a.site
  if (a.kind === 'docker') return !!a.container && !!a.port
  return true
})

async function removeAlias(r: Route) {
  const s = sites.value.find((x) => x.id === r.site_id)
  if (!s) return
  try {
    const res = await patch<{ task_id: string }>(`/sites/${s.id}`, { aliases: s.aliases.filter((x) => x !== r.host) })
    task.value = { id: res.task_id, title: `${t('route.remove')} · ${r.host}` }
  } catch (e) {
    fail(e)
  }
}

// ---- table ----
const target = (r: Route) => {
  if (r.type === 'php' || r.type === 'static') return `nginx · ${typeName(r.type)}`
  return r.upstream
}
const certCell = (r: Route) => {
  if (r.mode === 'passthrough') return h(NText, { depth: 3 }, () => t('route.cert_backend'))
  if (!r.cert_covers) return h(NTag, { size: 'small', type: r.force_https ? 'error' : 'default' }, () => t('route.cert_none'))
  return h(NTooltip, null, {
    trigger: () => h(NTag, { size: 'small', type: 'success' }, () => r.cert_issuer || 'SSL'),
    default: () => (r.cert_not_after ? datetime(r.cert_not_after, locale.value) : ''),
  })
}
const dnsCell = (r: Route) => {
  if (r.wildcard || !checked.value) return r.dns_managed ? h(NTag, { size: 'small' }, () => (r.dns_proxied ? 'Cloudflare ☁' : 'Cloudflare')) : '—'
  const type = r.dns === 'ok' ? 'success' : r.dns === 'other' ? (r.dns_proxied ? 'info' : 'warning') : 'error'
  const label = r.dns === 'other' && r.dns_proxied ? 'Cloudflare ☁' : t(`route.dns.${r.dns || 'none'}`)
  return h(NTooltip, null, { trigger: () => h(NTag, { size: 'small', type }, () => label), default: () => (r.dns_addrs ?? []).join(', ') || '—' })
}

const columns: DataTableColumns<Route> = [
  {
    title: () => t('route.host'),
    key: 'host',
    width: 240,
    render: (r) =>
      h(NSpace, { size: 4, align: 'center', wrap: false }, () => [
        h('b', { style: r.stopped ? 'opacity:.5' : '' }, r.host),
        r.wildcard ? h(NTag, { size: 'tiny', type: 'info' }, () => t('route.wildcard')) : null,
        !r.primary ? h(NTag, { size: 'tiny' }, () => t('route.alias')) : null,
      ]),
  },
  {
    title: () => t('route.mode'),
    key: 'mode',
    width: 120,
    render: (r) => h(NTag, { size: 'small', type: r.mode === 'passthrough' ? 'warning' : 'default' }, () => t(`route.modes.${r.mode}`)),
  },
  {
    title: () => t('route.target'),
    key: 'target',
    ellipsis: { tooltip: true },
    render: (r) =>
      h(NSpace, { size: 4, align: 'center', wrap: false }, () => [
        r.health ? h('span', { style: `color:${r.health === 'up' ? '#18a058' : '#d03050'}` }, '●') : null,
        h('span', target(r)),
        r.container ? h(NTag, { size: 'tiny', type: 'info' }, () => `🐳 ${r.container}`) : null,
      ]),
  },
  { title: () => t('route.site'), key: 'domain', width: 180, render: (r) => h(NButton, { text: true, type: 'primary', onClick: () => (selected.value = sites.value.find((s) => s.id === r.site_id) ?? null) }, () => r.domain) },
  { title: 'SSL', key: 'cert', width: 130, render: certCell },
  { title: 'DNS', key: 'dns', width: 120, render: dnsCell },
  {
    title: () => t('route.warnings'),
    key: 'warnings',
    width: 220,
    render: (r) =>
      h(NSpace, { size: 4, vertical: true }, () => [
        ...r.warnings.map((w) =>
          h(NTooltip, null, {
            trigger: () => h(NTag, { size: 'tiny', type: 'warning' }, () => t(`${w}.short`)),
            default: () => t(`${w}.long`, { hosts: (r.overrides ?? []).join(', ') }),
          }),
        ),
        r.stopped ? h(NTag, { size: 'tiny' }, () => t('site.stopped')) : null,
      ]),
  },
  {
    title: '',
    key: 'actions',
    width: 90,
    render: (r) =>
      r.primary
        ? null
        : h(
            NPopconfirm,
            { onPositiveClick: () => removeAlias(r) },
            { trigger: () => h(NButton, { size: 'tiny', type: 'error', ghost: true }, () => t('route.remove')), default: () => t('route.remove_confirm', { host: r.host, site: r.domain }) },
          ),
  },
]
</script>

<template>
  <n-tabs type="line" animated default-value="routes">
    <n-tab-pane name="routes" :tab="t('dns.routes_tab')">
  <n-alert type="default" :show-icon="false" style="margin-bottom: 12px">{{ t('route.intro') }}</n-alert>
  <n-space justify="space-between" style="margin-bottom: 12px">
    <n-space>
      <n-button type="primary" :disabled="!status?.status.installed" @click="openAdd(); loadContainers()">{{ t('route.add') }}</n-button>
      <n-input-group style="width: 340px">
        <n-input v-model:value="probe" size="medium" clearable :placeholder="t('route.test_placeholder')" @keyup.enter="resolve" />
        <n-button @click="resolve">{{ t('route.test') }}</n-button>
      </n-input-group>
    </n-space>
    <n-space>
      <n-input v-model:value="search" clearable :placeholder="t('site.search')" style="width: 200px" />
      <n-button :loading="loading" @click="load(true)">{{ t('route.check') }}</n-button>
    </n-space>
  </n-space>

  <n-alert v-if="resolved" :type="resolved.match === 'default' ? 'warning' : 'success'" closable style="margin-bottom: 12px" @close="resolved = null">
    <template v-if="resolved.route">
      {{ t(`route.match.${resolved.match}`, { host: resolved.host, pattern: resolved.pattern }) }} →
      <b>{{ resolved.route.domain }}</b> · {{ t(`route.modes.${resolved.route.mode}`) }} · {{ target(resolved.route) }}
    </template>
    <template v-else>{{ t('route.match.default', { host: resolved.host }) }}</template>
  </n-alert>
  <n-alert v-if="warned" type="warning" :show-icon="false" style="margin-bottom: 12px">{{ t('route.warned', { n: warned }) }}</n-alert>

  <n-data-table size="small" :columns="columns" :data="filtered" :loading="loading" :row-key="(r: Route) => r.host" :scroll-x="1300" />
    </n-tab-pane>
    <n-tab-pane name="dns" :tab="t('dns.tab')" display-directive="show:lazy"><DnsZones /></n-tab-pane>
  </n-tabs>

  <n-modal :show="!!add" @update:show="(v: boolean) => !v && (add = null)">
    <n-card v-if="add" :title="t('route.add')" style="width: min(560px, calc(100vw - 32px))" closable @close="add = null">
      <n-form label-placement="top">
        <n-form-item :label="t('route.host')" :feedback="t('route.host_hint')">
          <n-input v-model:value="add.host" placeholder="api.example.com / *.example.com" />
        </n-form-item>
        <n-form-item :label="t('route.connect_to')">
          <n-radio-group v-model:value="add.kind">
            <n-radio-button v-for="k in ['alias', 'site', 'docker', 'server', 'passthrough']" :key="k" :value="k">{{ t(`route.kinds.${k}`) }}</n-radio-button>
          </n-radio-group>
        </n-form-item>
        <p style="font-size: 12px; opacity: 0.7; margin-top: -8px">{{ t(`route.kind_hint.${add.kind}`) }}</p>
        <n-form-item v-if="add.kind === 'alias'" :label="t('route.site')">
          <n-select v-model:value="add.site" :options="siteOptions" filterable />
        </n-form-item>
        <template v-if="add.kind === 'docker'">
          <n-form-item :label="t('route.container')">
            <n-select v-model:value="add.container" :options="containerOptions" filterable @update:value="add.port = portOptions[0]?.value ?? null" />
          </n-form-item>
          <n-form-item :label="t('route.port')">
            <n-select v-model:value="add.port" :options="portOptions" />
          </n-form-item>
        </template>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="add = null">{{ t('common.cancel') }}</n-button>
          <n-button type="primary" :disabled="!addReady" @click="submitAdd">{{ add.kind === 'alias' ? t('common.confirm') : t('route.next') }}</n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>

  <SiteCreate
    :show="!!createPreset"
    :php-versions="status?.php_versions ?? []"
    :has-c-f="!!cfg?.has_cf_token"
    :preset="createPreset ?? undefined"
    @close="createPreset = null"
    @created="(id: string, d: string) => { createPreset = null; task = { id, title: t('route.add') + ' · ' + d } }"
  />
  <SiteDrawer
    :site="selected"
    :php-versions="status?.php_versions ?? []"
    :has-c-f="!!cfg?.has_cf_token"
    @close="selected = null"
    @changed="load()"
    @task="(id: string, title: string) => (task = { id, title })"
  />
  <TaskModal :task-id="task?.id ?? null" :title="task?.title ?? ''" @close="task = null; load()" @done="load()" />
</template>
