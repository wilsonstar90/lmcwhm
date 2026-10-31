<script setup lang="ts">
// Websites (like aaPanel's 網站管理): status, backups, root, today's traffic,
// expiry, remark (edited in place), PHP, SSL and actions per row; batch
// operations; categories; the settings window.
import { computed, h, nextTick, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import {
  NAlert, NButton, NCard, NCheckbox, NDataTable, NInput, NModal, NPopconfirm, NRadio, NRadioGroup, NSelect, NSpace, NTabPane, NTabs, NTag,
  useMessage, type DataTableColumns,
} from 'naive-ui'
import { get, patch, post, ApiError } from '../api/client'
import type { Site, WebConfig, WebStatus } from '../api/types'
import { bytes, datetime } from '../format'
import SiteCreate from '../components/SiteCreate.vue'
import SiteSettings from '../components/SiteSettings.vue'
import SiteBackups from '../components/SiteBackups.vue'
import SiteStats from '../components/SiteStats.vue'
import TaskModal from '../components/TaskModal.vue'
import WebEnvPanel from '../components/WebEnvPanel.vue'

const { t, locale } = useI18n()
const router = useRouter()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))

const sites = ref<Site[]>([])
const overview = ref<Record<string, { backups: number; bytes: number; pv: number }>>({})
const categories = ref<string[]>([])
const loading = ref(false)
const status = ref<WebStatus | null>(null)
const cfg = ref<WebConfig | null>(null)
const showCreate = ref(false)
const settings = ref<{ id: number; section: string } | null>(null)
const task = ref<{ id: string; title: string } | null>(null)
const tab = ref('sites')
const search = ref('')
const category = ref<string | null>(null)
const checked = ref<number[]>([])

async function load() {
  loading.value = true
  try {
    const [s, st, c] = await Promise.all([get<{ items: Site[] }>('/sites'), get<WebStatus>('/web/status'), get<WebConfig>('/web/config')])
    // Domain entries without website files live on the domain page.
    sites.value = s.items.filter((x) => x.type !== 'proxy')
    status.value = st
    cfg.value = c
    get<{ sites: typeof overview.value }>('/sites/overview').then((r) => (overview.value = r.sites)).catch(() => {})
    get<{ items: string[] }>('/sites/categories').then((r) => (categories.value = r.items)).catch(() => {})
  } finally {
    loading.value = false
  }
}
onMounted(load)
const current = computed(() => (settings.value ? sites.value.find((x) => x.id === settings.value!.id) ?? null : null))
const openTask = (id: string, title: string) => (task.value = { id, title })
const open = (s: Site, section = 'domains') => (settings.value = { id: s.id, section })

const filtered = computed(() => {
  const q = search.value.trim().toLowerCase()
  return sites.value.filter((s) =>
    (category.value === null || (s.category ?? '') === category.value) &&
    (!q || [s.domain, ...s.aliases, s.remark, s.root].some((x) => x.toLowerCase().includes(q))))
})

// ---- status: stop shows 404 or a chosen error page ----
const stopDlg = ref<{ ids: number[]; mode: '404' | 'page'; page: number | null } | null>(null)
const pages = ref<{ id: number; code: string; name: string }[]>([])
async function askStop(ids: number[]) {
  const first = sites.value.find((x) => x.id === ids[0])
  stopDlg.value = { ids, mode: first?.stop_mode ?? '404', page: first?.stop_page || null }
  if (!pages.value.length) pages.value = (await get<{ pages: typeof pages.value }>('/errpages').catch(() => ({ pages: [] }))).pages
}
async function stopNow() {
  const d = stopDlg.value!
  try {
    const r = await post<{ task_id: string }>('/sites/batch', { ids: d.ids, action: 'stop', stop_mode: d.mode, stop_page: d.mode === 'page' ? d.page ?? 0 : 0 })
    openTask(r.task_id, t('site.stop'))
    stopDlg.value = null
  } catch (e) {
    fail(e)
  }
}
async function start(ids: number[]) {
  try {
    const r = await post<{ task_id: string }>('/sites/batch', { ids, action: 'start' })
    openTask(r.task_id, t('site.start'))
  } catch (e) {
    fail(e)
  }
}
const pageName = (p: { code: string; name: string }) => (p.code ? t(`ep.builtin.${p.code}`) : p.name)

// ---- remark: click to type ----
const remarkEdit = ref<{ id: number; text: string } | null>(null)
const remarkEl = ref<HTMLInputElement | null>(null)
async function editRemark(s: Site) {
  remarkEdit.value = { id: s.id, text: s.remark }
  await nextTick()
  remarkEl.value?.focus()
}
async function saveRemark() {
  const r = remarkEdit.value
  remarkEdit.value = null
  const s = sites.value.find((x) => x.id === r?.id)
  if (!r || !s || s.remark === r.text.trim()) return
  try {
    await patch(`/sites/${s.id}`, { remark: r.text.trim() })
    s.remark = r.text.trim()
  } catch (e) {
    fail(e)
  }
}

// ---- delete ----
const delDlg = ref<{ sites: Site[]; files: boolean; dns: boolean } | null>(null)
async function doDelete() {
  const d = delDlg.value!
  try {
    for (const s of d.sites) {
      const r = await post<{ task_id: string }>(`/sites/${s.id}/delete`, { files: d.files, dns: d.dns })
      openTask(r.task_id, `${t('site.delete')} · ${s.domain}`)
    }
    delDlg.value = null
    checked.value = []
  } catch (e) {
    fail(e)
  }
}

// ---- batch ----
const batch = ref<string | null>(null)
const batchDlg = ref<{ action: 'expiry' | 'category'; date: string; category: string } | null>(null)
async function runBatch() {
  const ids = checked.value
  const list = sites.value.filter((s) => ids.includes(s.id))
  switch (batch.value) {
    case 'start':
      return start(ids)
    case 'stop':
      return askStop(ids)
    case 'backup':
      for (const s of list) {
        try {
          const r = await post<{ task_id: string }>('/backups/run', { kind: 'site', target: s.domain, dests: ['local'], keep: 0 })
          openTask(r.task_id, `${t('sb.backup_now')} · ${s.domain}`)
        } catch (e) {
          fail(e)
        }
      }
      return
    case 'expiry':
    case 'category':
      batchDlg.value = { action: batch.value, date: '', category: '' }
      return
    case 'delete':
      delDlg.value = { sites: list, files: false, dns: true }
  }
}
async function applyBatchDlg() {
  const d = batchDlg.value!
  const body: Record<string, unknown> = { ids: checked.value, action: d.action }
  if (d.action === 'expiry') body.expires_at = d.date ? Math.floor(new Date(d.date + 'T23:59:59').getTime() / 1000) : 0
  else body.category = d.category
  try {
    const r = await post<{ task_id: string }>('/sites/batch', body)
    openTask(r.task_id, t(`ws.batch.${d.action}`))
    batchDlg.value = null
  } catch (e) {
    fail(e)
  }
}

// ---- table ----
const daysLeft = (ts: number) => Math.floor((ts * 1000 - Date.now()) / 86400000)
function sslCell(s: Site) {
  if (s.ssl_mode === 'none') return h('a', { class: 'warn', onClick: () => open(s, 'ssl') }, t('ws.ssl_none'))
  const d = daysLeft(s.cert_not_after)
  return h('a', { class: d < 0 ? 'bad' : d < 15 ? 'warn' : 'ok', onClick: () => open(s, 'ssl') }, d < 0 ? t('ws.expired') : t('ws.days', { n: d }))
}
function langCell(s: Site) {
  const label = s.type === 'php' ? s.php_version : s.type === 'static' ? t('ws.static') : `${s.type} ${s.runtime_version}`
  return h('a', { class: 'ok', onClick: () => open(s, s.type === 'php' ? 'php' : s.type === 'static' ? 'other' : 'project') }, label)
}
function expiryCell(s: Site) {
  if (!s.expires_at) return h('a', { class: 'plain', onClick: () => open(s, 'alert') }, t('ws.forever'))
  const d = daysLeft(s.expires_at)
  return h('a', { class: d < 0 ? 'bad' : d < 7 ? 'warn' : 'plain', onClick: () => open(s, 'alert') }, datetime(s.expires_at, locale.value).slice(0, 10))
}
const columns = computed<DataTableColumns<Site>>(() => [
  { type: 'selection' },
  {
    title: () => t('ws.name'),
    key: 'domain',
    minWidth: 180,
    sorter: (a, b) => a.domain.localeCompare(b.domain),
    render: (s) =>
      h('span', { class: 'name' }, [
        h('a', { class: 'link', onClick: () => open(s) }, s.domain),
        h('a', { class: 'ext', href: `${s.ssl_mode !== 'none' ? 'https' : 'http'}://${s.domain}`, target: '_blank', rel: 'noopener', title: t('ws.visit') }, '↗'),
        s.aliases.length ? h(NTag, { size: 'tiny', bordered: false, title: s.aliases.join('\n') }, () => `+${s.aliases.length}`) : null,
      ]),
  },
  {
    title: () => t('common.status'),
    key: 'stopped',
    width: 90,
    sorter: (a, b) => Number(a.stopped) - Number(b.stopped),
    render: (s) =>
      h('a', { class: s.stopped ? 'bad' : 'ok', onClick: () => (s.stopped ? start([s.id]) : askStop([s.id])), title: s.stopped ? t('ws.click_start') : t('ws.click_stop') },
        s.stopped ? `${t('site.stopped')} ⏸` : `${t('site.running')} ▶`),
  },
  {
    title: () => t('ws.backup'),
    key: 'backup',
    width: 80,
    render: (s) => {
      const n = overview.value[s.domain]?.backups ?? 0
      return h('a', { class: n ? 'ok' : 'warn', onClick: () => (backupsOf.value = s.domain) }, n ? t('ws.backups', { n }) : t('ws.no_backup'))
    },
  },
  { title: () => t('ws.root'), key: 'root', minWidth: 200, ellipsis: { tooltip: true }, render: (s) => h('a', { class: 'ok', onClick: () => router.push({ path: '/files', query: { path: s.root } }) }, s.root) },
  { title: () => t('ws.traffic'), key: 'bytes', width: 100, sorter: (a, b) => (overview.value[a.domain]?.bytes ?? 0) - (overview.value[b.domain]?.bytes ?? 0), render: (s) => bytes(overview.value[s.domain]?.bytes ?? 0) },
  { title: () => t('ws.expiry'), key: 'expires_at', width: 105, sorter: (a, b) => (a.expires_at || 9e12) - (b.expires_at || 9e12), render: expiryCell },
  {
    title: () => t('site.remark'),
    key: 'remark',
    minWidth: 150,
    render: (s) =>
      remarkEdit.value?.id === s.id
        ? h('input', {
          ref: remarkEl, class: 'remark-input', value: remarkEdit.value.text, maxlength: 200,
          onInput: (e: Event) => (remarkEdit.value!.text = (e.target as HTMLInputElement).value),
          onKeydown: (e: KeyboardEvent) => (e.key === 'Enter' ? saveRemark() : e.key === 'Escape' ? (remarkEdit.value = null) : null),
          onBlur: saveRemark,
        })
        : h('div', { class: ['remark', { empty: !s.remark }], title: s.remark, onClick: () => editRemark(s) }, s.remark || t('fm.add_remark')),
  },
  { title: 'PHP', key: 'php', width: 90, render: langCell },
  { title: () => t('ws.ssl'), key: 'ssl', width: 100, sorter: (a, b) => (a.cert_not_after || 0) - (b.cert_not_after || 0), render: sslCell },
  {
    title: () => t('common.actions'),
    key: 'ops',
    width: 190,
    align: 'right',
    render: (s) =>
      h('span', { class: 'ops' }, [
        h('a', { onClick: () => (statsOf.value = s.domain) }, t('ws.stats')),
        h('a', { onClick: () => open(s, 'security') }, 'WAF'),
        h('a', { onClick: () => open(s) }, t('ws.settings')),
        h('a', { class: 'danger', onClick: () => (delDlg.value = { sites: [s], files: false, dns: true }) }, t('common.delete')),
      ]),
  },
])
const backupsOf = ref<string | null>(null)
const statsOf = ref<string | null>(null)
const showOverview = ref(false)
interface StatRow { domain: string; pv: number; uv: number; bytes: number; s4xx: number; s5xx: number }
const statRows = ref<StatRow[]>([])
async function openOverview() {
  showOverview.value = true
  try {
    const r = await get<{ items: Record<string, Omit<StatRow, 'domain'>> }>('/sitestats/overview')
    statRows.value = Object.entries(r.items ?? {}).map(([domain, p]) => ({ domain, ...p })).sort((a, b) => b.pv - a.pv)
  } catch (e) {
    fail(e)
  }
}
const statCols = computed<DataTableColumns<StatRow>>(() => [
  { title: () => t('ws.name'), key: 'domain', render: (r) => h('a', { class: 'ok', onClick: () => ((showOverview.value = false), (statsOf.value = r.domain)) }, r.domain) },
  { title: 'PV', key: 'pv', width: 90, sorter: (a, b) => a.pv - b.pv },
  { title: 'UV', key: 'uv', width: 90, sorter: (a, b) => a.uv - b.uv },
  { title: () => t('ws.traffic'), key: 'bytes', width: 110, sorter: (a, b) => a.bytes - b.bytes, render: (r) => bytes(r.bytes) },
  { title: '4xx', key: 's4xx', width: 80 },
  { title: '5xx', key: 's5xx', width: 80 },
])
const batchOptions = computed(() =>
  ['start', 'stop', 'backup', 'expiry', 'category', 'delete'].map((k) => ({ label: t(`ws.batch.${k}`), value: k })),
)
const catOptions = computed(() => [
  { label: t('ws.all_categories'), value: null as unknown as string },
  { label: t('ws.uncategorised'), value: '' },
  ...categories.value.map((c) => ({ label: c, value: c })),
])
</script>

<template>
  <n-tabs v-model:value="tab" type="line" animated>
    <n-tab-pane name="sites" :tab="t('site.list')">
      <n-alert v-if="status && !status.status.docker" type="error" :show-icon="false" style="margin-bottom: 12px">{{ t('web.no_docker') }}</n-alert>
      <n-alert v-else-if="status && !status.status.installed" type="info" :show-icon="false" style="margin-bottom: 12px">{{ t('web.not_installed') }}</n-alert>
      <div class="bar">
        <n-button type="primary" :disabled="!status?.status.docker" @click="showCreate = true">{{ t('ws.add') }}</n-button>
        <n-button @click="openOverview">{{ t('ws.overview') }}</n-button>
        <span class="grow" />
        <n-select v-model:value="category" :options="catOptions" size="small" style="width: 160px" />
        <n-input v-model:value="search" size="small" clearable :placeholder="t('ws.search')" style="width: 220px" />
        <n-button size="small" :title="t('common.refresh')" @click="load">⟳</n-button>
      </div>
      <n-data-table
        v-model:checked-row-keys="checked" size="small" :columns="columns" :data="filtered" :loading="loading" :row-key="(r: Site) => r.id"
        :scroll-x="1320" :pagination="{ pageSize: 20, showSizePicker: true, pageSizes: [10, 20, 50, 100] }"
      />
      <n-space align="center" style="margin-top: 8px">
        <n-select v-model:value="batch" :options="batchOptions" size="small" :placeholder="t('rb.batch')" style="width: 180px" />
        <n-button size="small" type="primary" :disabled="!batch || !checked.length" @click="runBatch">{{ t('rb.batch_run') }}</n-button>
        <span v-if="checked.length" class="hint">{{ t('fm.selected_n', { n: checked.length }) }}</span>
      </n-space>
    </n-tab-pane>
    <n-tab-pane name="env" :tab="t('web.tab')">
      <WebEnvPanel @changed="load" />
    </n-tab-pane>
  </n-tabs>

  <SiteCreate
    :show="showCreate" :php-versions="status?.php_versions ?? []" :has-c-f="!!cfg?.has_cf_token" @close="showCreate = false"
    @created="(id: string, d: string) => { showCreate = false; openTask(id, t('site.create') + ' · ' + d) }"
  />
  <SiteSettings
    :site="current" :section="settings?.section" :php-versions="status?.php_versions ?? []" :has-c-f="!!cfg?.has_cf_token"
    @close="settings = null" @changed="load" @task="openTask"
  />
  <SiteBackups :domain="backupsOf" @close="backupsOf = null; load()" @task="openTask" />
  <n-modal :show="!!statsOf" @update:show="(v: boolean) => !v && (statsOf = null)">
    <n-card v-if="statsOf" :title="`${t('ws.stats')} · ${statsOf}`" style="width: min(1100px, calc(100vw - 24px))" closable @close="statsOf = null">
      <SiteStats :domain="statsOf" />
    </n-card>
  </n-modal>
  <n-modal :show="showOverview" @update:show="(v: boolean) => (showOverview = v)">
    <n-card :title="t('ws.overview')" style="width: min(820px, calc(100vw - 24px))" closable @close="showOverview = false">
      <p class="hint">{{ t('ws.overview_hint') }}</p>
      <n-data-table size="small" :columns="statCols" :data="statRows" :scroll-x="560" />
    </n-card>
  </n-modal>

  <n-modal :show="!!stopDlg" @update:show="(v: boolean) => !v && (stopDlg = null)">
    <n-card v-if="stopDlg" :title="t('ws.stop_title', { n: stopDlg.ids.length })" style="width: min(480px, calc(100vw - 32px))" closable @close="stopDlg = null">
      <p class="hint">{{ t('ws.stop_hint') }}</p>
      <n-radio-group v-model:value="stopDlg.mode">
        <n-space vertical>
          <n-radio value="404">{{ t('ws.stop_404') }}</n-radio>
          <n-radio value="page">{{ t('ws.stop_page') }}</n-radio>
        </n-space>
      </n-radio-group>
      <n-select
        v-if="stopDlg.mode === 'page'" v-model:value="stopDlg.page" :options="pages.map((p) => ({ label: pageName(p), value: p.id }))"
        :placeholder="t('ws.pick_page')" style="margin-top: 10px"
      />
      <p v-if="stopDlg.mode === 'page'" class="hint">{{ t('ws.stop_page_hint') }} <a class="ok" @click="router.push('/security?tab=waf&sub=pages')">{{ t('ws.edit_pages') }}</a></p>
      <template #footer>
        <n-space justify="end">
          <n-button @click="stopDlg = null">{{ t('common.cancel') }}</n-button>
          <n-button type="warning" :disabled="stopDlg.mode === 'page' && !stopDlg.page" @click="stopNow">{{ t('site.stop') }}</n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>

  <n-modal :show="!!delDlg" @update:show="(v: boolean) => !v && (delDlg = null)">
    <n-card v-if="delDlg" :title="t('ws.delete_title', { n: delDlg.sites.length })" style="width: min(520px, calc(100vw - 32px))" closable @close="delDlg = null">
      <p>{{ delDlg.sites.map((s) => s.domain).join(', ') }}</p>
      <n-space vertical>
        <n-checkbox v-model:checked="delDlg.files">{{ t('ws.delete_files') }}</n-checkbox>
        <n-checkbox v-model:checked="delDlg.dns">{{ t('site.delete_dns') }}</n-checkbox>
      </n-space>
      <p class="hint">{{ t('ws.delete_hint') }}</p>
      <template #footer>
        <n-space justify="end">
          <n-button @click="delDlg = null">{{ t('common.cancel') }}</n-button>
          <n-popconfirm @positive-click="doDelete">
            <template #trigger><n-button type="error">{{ t('site.delete') }}</n-button></template>
            {{ t('ws.delete_confirm') }}
          </n-popconfirm>
        </n-space>
      </template>
    </n-card>
  </n-modal>

  <n-modal :show="!!batchDlg" @update:show="(v: boolean) => !v && (batchDlg = null)">
    <n-card v-if="batchDlg" :title="t(`ws.batch.${batchDlg.action}`)" style="width: min(420px, calc(100vw - 32px))" closable @close="batchDlg = null">
      <template v-if="batchDlg.action === 'expiry'">
        <input v-model="batchDlg.date" type="date" class="date" />
        <p class="hint">{{ t('ws.expiry_hint') }}</p>
      </template>
      <n-select v-else v-model:value="batchDlg.category" :options="categories.map((c) => ({ label: c, value: c }))" filterable tag :placeholder="t('ws.category_ph')" />
      <template #footer>
        <n-space justify="end"><n-button @click="batchDlg = null">{{ t('common.cancel') }}</n-button><n-button type="primary" @click="applyBatchDlg">{{ t('common.confirm') }}</n-button></n-space>
      </template>
    </n-card>
  </n-modal>
  <TaskModal :task-id="task?.id ?? null" :title="task?.title ?? ''" @close="task = null; load()" @done="load" />
</template>

<style scoped>
.bar {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
  margin-bottom: 10px;
}
.grow {
  flex: 1;
}
.hint {
  font-size: 12px;
  opacity: 0.65;
}
:deep(.name) {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
:deep(.link),
:deep(.ok),
:deep(.ops a) {
  color: #20a53a;
  cursor: pointer;
}
:deep(.ext) {
  opacity: 0.5;
  text-decoration: none;
  color: inherit;
}
:deep(.warn) {
  color: #f0a020;
  cursor: pointer;
}
:deep(.bad),
:deep(.ops a.danger) {
  color: #d03050;
  cursor: pointer;
}
:deep(.plain) {
  cursor: pointer;
}
:deep(.ops) {
  display: inline-flex;
  gap: 10px;
  white-space: nowrap;
}
:deep(.remark) {
  min-height: 20px;
  cursor: text;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
:deep(.remark.empty) {
  opacity: 0;
}
:deep(tr:hover .remark.empty) {
  opacity: 0.35;
}
@media (hover: none) {
  :deep(.remark.empty) {
    opacity: 0.35;
  }
}
:deep(.remark-input) {
  width: 100%;
  font: inherit;
  padding: 2px 6px;
  border: 1px solid #20a53a;
  border-radius: 4px;
  background: transparent;
  color: inherit;
  outline: none;
}
.date {
  font: inherit;
  padding: 4px 8px;
  border: 1px solid rgba(128, 128, 128, 0.35);
  border-radius: 4px;
  background: transparent;
  color: inherit;
}
</style>
