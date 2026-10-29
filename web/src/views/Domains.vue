<script setup lang="ts">
// 域名 / SNI: every domain entry once (its aliases with it), what it serves
// (a website, a container, a URL, passthrough or a redirect), its SSL and
// DNS; port forwarding; DNS zones.
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NCard, NDataTable, NForm, NFormItem, NInput, NInputGroup, NModal, NPopconfirm, NRadioButton, NRadioGroup,
  NSelect, NSpace, NTabPane, NTabs, NTag, NText, NTooltip, useMessage, type DataTableColumns,
} from 'naive-ui'
import { get, patch, post, ApiError } from '../api/client'
import type { Route, RouteResolution, Site, WebConfig, WebStatus } from '../api/types'
import { datetime } from '../format'
import SiteCreate from '../components/SiteCreate.vue'
import SiteDrawer from '../components/SiteDrawer.vue'
import TaskModal from '../components/TaskModal.vue'
import DnsZones from '../components/DnsZones.vue'
import DomainEditor from '../components/DomainEditor.vue'
import DomainCreate, { type DomainPreset } from '../components/DomainCreate.vue'
import PortForwards from '../components/PortForwards.vue'

const { t, locale } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))

const routes = ref<Route[]>([])
const sites = ref<Site[]>([])
const status = ref<WebStatus | null>(null)
const cfg = ref<WebConfig | null>(null)
const loading = ref(false)
const checked = ref(false)
const search = ref('')
const task = ref<{ id: string; title: string } | null>(null)
const selected = ref<Site | null>(null)
const editing = ref<number | null>(null)
const sitePreset = ref<Record<string, unknown> | null>(null)
const entryPreset = ref<DomainPreset | null>(null)

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

// One row per entry: its primary hostname and the aliases.
interface Entry { id: number; primary: Route; hosts: Route[]; site?: Site }
const entries = computed<Entry[]>(() => {
  const by = new Map<number, Entry>()
  for (const r of routes.value) {
    const e = by.get(r.site_id) ?? { id: r.site_id, primary: r, hosts: [], site: sites.value.find((s) => s.id === r.site_id) }
    if (r.primary) e.primary = r
    e.hosts.push(r)
    by.set(r.site_id, e)
  }
  return [...by.values()]
})
const filtered = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return entries.value
  return entries.value.filter((e) => e.hosts.some((r) => r.host.includes(q)) || e.primary.upstream.includes(q) || e.primary.container.includes(q) || (e.primary.target_name ?? '').includes(q))
})
const warned = computed(() => entries.value.filter((e) => e.hosts.some((r) => r.warnings.length)).length)

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

// ---- add ----
type Kind = 'alias' | 'site' | 'entry'
const add = ref<{ host: string; kind: Kind; site: number | null } | null>(null)
const typeName = (ty: string) => ({ php: 'PHP', static: t('site.static'), node: 'Node.js', python: 'Python', java: 'Java', tomcat: 'Tomcat', proxy: t('site.proxy') })[ty] ?? ty
const entryOptions = computed(() => sites.value.map((s) => ({ label: s.domain, value: s.id })))
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
  add.value = null
  if (a.kind === 'site') sitePreset.value = { domain: host }
  else entryPreset.value = { domain: host }
}

async function remove(e: Entry) {
  try {
    const r = await post<{ task_id: string }>(`/sites/${e.id}/delete`, { files: false, dns: true })
    task.value = { id: r.task_id, title: `${t('dom.delete')} · ${e.primary.host}` }
  } catch (err) {
    fail(err)
  }
}
async function removeAlias(e: Entry, host: string) {
  if (!e.site) return
  try {
    const r = await patch<{ task_id: string }>(`/sites/${e.id}`, { aliases: e.site.aliases.filter((x) => x !== host) })
    task.value = { id: r.task_id, title: `${t('route.remove')} · ${host}` }
  } catch (err) {
    fail(err)
  }
}

// ---- cells ----
function targetLabel(r: Route): string {
  switch (r.target) {
    case 'site': return `${t('dom.targets.site')} · ${typeName(r.type)}`
    case 'link': return `${t('dom.targets.link')} → ${r.target_name ?? '?'}`
    case 'docker': return `${r.container || 'Docker'} · ${r.upstream.replace(/^https?:\/\//, '')}`
    case 'passthrough': return `${t('dom.targets.passthrough')} → ${r.upstream}`
    case 'redirect': return `${t('dom.targets.redirect')} → ${r.redirect}`
  }
  return r.upstream
}
function sslCell(e: Entry) {
  const r = e.primary
  if (r.mode === 'passthrough') return h(NText, { depth: 3 }, () => t('route.cert_backend'))
  const covered = e.hosts.filter((x) => x.cert_covers).length
  if (!covered) return h(NTag, { size: 'small', type: r.force_https ? 'error' : 'default' }, () => t('route.cert_none'))
  const all = covered === e.hosts.length
  return h(NTooltip, null, {
    trigger: () => h(NTag, { size: 'small', type: all ? 'success' : 'warning' }, () => (all ? r.cert_issuer || 'SSL' : t('dom.cert_partial', { n: covered, total: e.hosts.length }))),
    default: () => (r.cert_not_after ? datetime(r.cert_not_after, locale.value) : ''),
  })
}
type TagType = 'default' | 'success' | 'info' | 'warning' | 'error'
const dnsTag = (r: Route): { type: TagType; label: string } => {
  if (r.wildcard || !checked.value) return { type: 'default', label: r.dns_managed ? (r.dns_proxied ? 'Cloudflare ☁' : 'Cloudflare') : '—' }
  const type: TagType = r.dns === 'ok' ? 'success' : r.dns === 'cloudflare' ? 'info' : r.dns === 'other' ? 'warning' : 'error'
  return { type, label: r.dns === 'cloudflare' ? 'Cloudflare ☁' : t(`route.dns.${r.dns || 'none'}`) }
}
const rank = (ty: TagType) => ({ error: 3, warning: 2, info: 1, success: 0, default: 0 })[ty]
function dnsCell(e: Entry) {
  const worst = [...e.hosts].sort((a, b) => rank(dnsTag(b).type) - rank(dnsTag(a).type))[0]
  const tag = dnsTag(worst)
  return h(NTooltip, null, {
    trigger: () => h(NTag, { size: 'small', type: tag.type }, () => tag.label),
    default: () => h('div', e.hosts.map((r) => h('div', `${r.host}: ${dnsTag(r).label}${r.dns_addrs?.length ? ' (' + r.dns_addrs.join(', ') + ')' : ''}`))),
  })
}
function warnings(e: Entry) {
  const seen = new Map<string, Route>()
  for (const r of e.hosts) for (const w of r.warnings) if (!seen.has(w)) seen.set(w, r)
  return [...seen.entries()]
}
const hostCols = computed<DataTableColumns<Route>>(() => [
  { title: () => t('route.host'), key: 'host', render: (r) => h(NSpace, { size: 4 }, () => [h('span', r.host), r.wildcard ? h(NTag, { size: 'tiny', type: 'info' }, () => t('route.wildcard')) : null, !r.primary ? h(NTag, { size: 'tiny' }, () => t('route.alias')) : null]) },
  { title: 'SSL', key: 'cert', width: 120, render: (r) => (r.mode === 'passthrough' ? '—' : h(NTag, { size: 'tiny', type: r.cert_covers ? 'success' : 'default' }, () => (r.cert_covers ? '✓' : t('route.cert_none')))) },
  { title: 'DNS', key: 'dns', width: 300, render: (r) => h('span', [h(NTag, { size: 'tiny', type: dnsTag(r).type }, () => dnsTag(r).label), ' ', h(NText, { depth: 3 }, () => (r.dns_addrs ?? []).join(', '))]) },
  { title: () => t('route.warnings'), key: 'warnings', render: (r) => h(NSpace, { size: 4 }, () => r.warnings.map((w) => h(NTag, { size: 'tiny', type: 'warning' }, () => t(`${w}.short`)))) },
])
const columns = computed<DataTableColumns<Entry>>(() => [
  { type: 'expand', renderExpand: (e) => h(NDataTable, { size: 'small', columns: hostCols.value, data: e.hosts, rowKey: (r: Route) => r.host, bordered: false }) },
  {
    title: () => t('route.host'),
    key: 'host',
    minWidth: 260,
    render: (e) =>
      h(NSpace, { size: 4, align: 'center' }, () => [
        h('b', { style: e.primary.stopped ? 'opacity:.5' : '' }, e.primary.host),
        ...e.hosts.filter((r) => !r.primary).map((r) =>
          h(NTag, { size: 'tiny', closable: true, onClose: () => removeAlias(e, r.host), type: r.wildcard ? 'info' : 'default' }, () => r.host),
        ),
        e.primary.stopped ? h(NTag, { size: 'tiny' }, () => t('site.stopped')) : null,
      ]),
  },
  {
    title: () => t('dom.target'),
    key: 'target',
    minWidth: 240,
    ellipsis: { tooltip: true },
    render: (e) =>
      h(NSpace, { size: 4, align: 'center', wrap: false }, () => [
        e.primary.health ? h('span', { style: `color:${e.primary.health === 'up' ? '#18a058' : '#d03050'}` }, '●') : null,
        e.primary.target === 'docker' ? h('span', '🐳') : null,
        h('span', targetLabel(e.primary)),
      ]),
  },
  { title: 'TLS', key: 'mode', width: 90, render: (e) => h(NTag, { size: 'small', type: e.primary.mode === 'passthrough' ? 'warning' : 'default' }, () => t(`route.modes.${e.primary.mode}`)) },
  { title: 'SSL', key: 'cert', width: 130, render: sslCell },
  { title: 'DNS', key: 'dns', width: 130, render: dnsCell },
  {
    title: () => t('route.warnings'),
    key: 'warnings',
    width: 200,
    render: (e) =>
      h(NSpace, { size: 4, vertical: true }, () =>
        warnings(e).map(([w, r]) =>
          h(NTooltip, null, { trigger: () => h(NTag, { size: 'tiny', type: 'warning' }, () => t(`${w}.short`)), default: () => t(`${w}.long`, { hosts: (r.overrides ?? []).join(', ') }) }),
        ),
      ),
  },
  {
    title: '',
    key: 'actions',
    width: 210,
    render: (e) =>
      h(NSpace, { size: 6 }, () => [
        h(NButton, { size: 'tiny', type: 'primary', ghost: true, onClick: () => (editing.value = e.id) }, () => t('dom.manage')),
        e.site && e.site.type !== 'proxy' ? h(NButton, { size: 'tiny', onClick: () => (selected.value = e.site ?? null) }, () => t('dom.website_settings')) : null,
        e.site?.type === 'proxy'
          ? h(NPopconfirm, { onPositiveClick: () => remove(e) }, { trigger: () => h(NButton, { size: 'tiny', type: 'error', ghost: true }, () => t('common.delete')), default: () => t('dom.delete_confirm', { host: e.primary.host }) })
          : null,
      ]),
  },
])
</script>

<template>
  <n-tabs type="line" animated default-value="routes">
    <n-tab-pane name="routes" :tab="t('dns.routes_tab')">
      <n-alert type="default" :show-icon="false" style="margin-bottom: 12px">{{ t('dom.intro') }}</n-alert>
      <n-space justify="space-between" style="margin-bottom: 12px">
        <n-space>
          <n-button type="primary" :disabled="!status?.status.installed" @click="add = { host: '', kind: 'entry', site: null }">{{ t('route.add') }}</n-button>
          <n-input-group style="width: 340px">
            <n-input v-model:value="probe" clearable :placeholder="t('route.test_placeholder')" @keyup.enter="resolve" />
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
          <b>{{ resolved.route.domain }}</b> · {{ t(`route.modes.${resolved.route.mode}`) }} · {{ targetLabel(resolved.route) }}
        </template>
        <template v-else>{{ t('route.match.default', { host: resolved.host }) }}</template>
      </n-alert>
      <n-alert v-if="warned" type="warning" :show-icon="false" style="margin-bottom: 12px">{{ t('dom.warned', { n: warned }) }}</n-alert>

      <n-data-table size="small" :columns="columns" :data="filtered" :loading="loading" :row-key="(e: Entry) => e.id" :scroll-x="1300" />
    </n-tab-pane>
    <n-tab-pane name="forwards" :tab="t('fwd.title')" display-directive="show:lazy"><PortForwards /></n-tab-pane>
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
            <n-radio-button v-for="k in ['entry', 'alias', 'site']" :key="k" :value="k">{{ t(`dom.add_kinds.${k}`) }}</n-radio-button>
          </n-radio-group>
        </n-form-item>
        <p style="font-size: 12px; opacity: 0.7; margin-top: -8px">{{ t(`dom.add_hint.${add.kind}`) }}</p>
        <n-form-item v-if="add.kind === 'alias'" :label="t('dom.entry')">
          <n-select v-model:value="add.site" :options="entryOptions" filterable />
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="add = null">{{ t('common.cancel') }}</n-button>
          <n-button type="primary" :disabled="!add.host.trim() || (add.kind === 'alias' && !add.site)" @click="submitAdd">{{ add.kind === 'alias' ? t('common.confirm') : t('route.next') }}</n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>

  <SiteCreate
    :show="!!sitePreset"
    :php-versions="status?.php_versions ?? []"
    :has-c-f="!!cfg?.has_cf_token"
    :preset="sitePreset ?? undefined"
    @close="sitePreset = null"
    @created="(id: string, d: string) => { sitePreset = null; task = { id, title: t('route.add') + ' · ' + d } }"
  />
  <DomainCreate
    :show="!!entryPreset"
    :has-c-f="!!cfg?.has_cf_token"
    :preset="entryPreset ?? undefined"
    @close="entryPreset = null"
    @created="(id: string, d: string) => { entryPreset = null; task = { id, title: t('route.add') + ' · ' + d } }"
  />
  <DomainEditor
    :site-id="editing"
    :has-c-f="!!cfg?.has_cf_token"
    @close="editing = null"
    @changed="load()"
    @task="(id: string, title: string) => (task = { id, title })"
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
