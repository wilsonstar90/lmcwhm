<script setup lang="ts">
// 網站防護 (as aaPanel's site protection): per website, the checks the edge
// runs before a request reaches it, and how many requests it refused.
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NCard, NCheckbox, NDataTable, NDrawer, NDrawerContent, NDynamicTags, NInput, NInputNumber, NModal, NSelect, NSpace, NSwitch, NTag, NTooltip,
  useMessage, type DataTableColumns,
} from 'naive-ui'
import { get, patch, put, ApiError } from '../api/client'
import SecEvents from './SecEvents.vue'
import TaskModal from './TaskModal.vue'

interface Prot { on: boolean; fake_spider: boolean; cdn: boolean; cdn_headers?: string[] | null; idc: boolean; crawler: boolean; cc: number }
interface Row { domain: string; aliases: string[]; type: string; supported: boolean; proxied: boolean; protect: Prot; blocked: number; blocked_24h: number }
interface Info { rules_on: boolean; spiders: string[]; has_asn: boolean }
interface Settings { rules_on: boolean; crawlers: string[]; idc: { number: number; name: string }[] }

const emit = defineEmits<{ changed: [] }>()
const { t, locale } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))
const rows = ref<Row[]>([])
const info = ref<Info>({ rules_on: false, spiders: [], has_asn: false })
const loading = ref(false)
const q = ref('')
const checked = ref<string[]>([])
const batch = ref<string | null>(null)

async function load() {
  loading.value = true
  try {
    const r = await get<{ sites: Row[]; info: Info }>('/protect/sites')
    rows.value = r.sites
    info.value = { ...r.info, spiders: r.info.spiders ?? [] }
  } catch (e) {
    fail(e)
  } finally {
    loading.value = false
  }
}
onMounted(() => { load(); loadWAF() })

async function save(items: Record<string, Prot>) {
  try {
    await put('/protect/sites', { items })
    await load()
    emit('changed')
  } catch (e) {
    fail(e)
    load()
  }
}
const set = (r: Row, patch: Partial<Prot>) => save({ [r.domain]: { ...r.protect, ...patch } })

const batchOptions = computed(() =>
  ['on', 'off', 'fake_on', 'fake_off', 'crawler_on', 'crawler_off', 'idc_on', 'idc_off'].map((k) => ({ label: t(`prot.batch.${k}`), value: k })),
)
function applyBatch() {
  const k = batch.value
  if (!k || !checked.value.length) return
  const [what, state] = k.includes('_') ? (k.split('_') as [string, string]) : ['on', k]
  const on = state === 'on'
  const key = ({ on: 'on', fake: 'fake_spider', crawler: 'crawler', idc: 'idc' } as Record<string, keyof Prot>)[what]
  const items: Record<string, Prot> = {}
  for (const r of rows.value.filter((x) => checked.value.includes(x.domain))) items[r.domain] = { ...r.protect, [key]: on }
  save(items)
}

// ---- refused requests of one site ----
const logSite = ref<string | null>(null)
const openLog = (d: string) => (logSite.value = d)

// ---- WAF (OWASP) per site ----
interface SiteWAF { id: number; domain: string; waf_off: boolean; waf_exclude: string[] }
const waf = ref<Record<string, SiteWAF>>({})
const wafOn = ref(false)
async function loadWAF() {
  try {
    const r = await get<{ settings: { enabled: boolean }; sites: (Omit<SiteWAF, 'waf_exclude'> & { waf_exclude: string[] | null })[] }>('/waf')
    wafOn.value = r.settings.enabled
    waf.value = Object.fromEntries(r.sites.map((x) => [x.domain, { ...x, waf_exclude: x.waf_exclude ?? [] }]))
  } catch {
    waf.value = {}
  }
}
const task = ref<{ id: string; title: string } | null>(null)
async function saveWAF(x: SiteWAF) {
  try {
    const r = await patch<{ task_id: string }>(`/sites/${x.id}`, { waf_off: x.waf_off, waf_exclude: x.waf_exclude })
    task.value = { id: r.task_id, title: `WAF · ${x.domain}` }
  } catch (e) {
    fail(e)
  }
}
const exclEdit = ref<SiteWAF | null>(null)

// ---- panel-wide settings ----
const settingsOpen = ref(false)
const crawlers = ref('')
const idc = ref('')
const idcRanges = ref(0)
const cur = ref<Settings | null>(null)
async function openSettings() {
  try {
    const r = await get<{ settings: Settings; idc_ranges: number }>('/protect/settings')
    cur.value = r.settings
    crawlers.value = r.settings.crawlers.join('\n')
    idc.value = r.settings.idc.map((a) => `${a.number} ${a.name}`).join('\n')
    idcRanges.value = r.idc_ranges
    settingsOpen.value = true
  } catch (e) {
    fail(e)
  }
}
async function saveSettings() {
  const asns = idc.value.split('\n').map((l) => l.trim()).filter(Boolean).map((l) => {
    const m = l.match(/^(?:AS)?(\d+)\s*(.*)$/i)
    return m ? { number: Number(m[1]), name: m[2].trim() } : null
  })
  if (asns.some((a) => !a)) return msg.error(t('prot.bad_asn'))
  try {
    await put('/protect/settings', { ...cur.value, crawlers: crawlers.value.split('\n'), idc: asns })
    msg.success(t('common.saved'))
    settingsOpen.value = false
    load()
  } catch (e) {
    fail(e)
  }
}

const shown = computed(() => {
  const s = q.value.trim().toLowerCase()
  return s ? rows.value.filter((r) => r.domain.includes(s) || r.aliases.some((a) => a.includes(s))) : rows.value
})
const usesFake = computed(() => rows.value.some((r) => r.protect.on && r.protect.fake_spider))
const usesIDC = computed(() => rows.value.some((r) => r.protect.on && r.protect.idc))
// CDN (like aaPanel): the site is reached through a CDN; the visitor's
// address comes from the first of its headers that holds one.
const DEFAULT_CDN_HEADERS = ['CF-Connecting-IP', 'True-Client-IP', 'Ali-Cdn-Real-Ip', 'Cdn-Src-Ip', 'Cdn-Real-Ip', 'X-Real-IP',
  'X-Forwarded-For', 'X-Client-IP', 'Client-IP', 'Forwarded-For', 'X-Forwarded', 'Forwarded']
const cdnEdit = ref<{ row: Row; headers: string[]; add: string } | null>(null)
const headerOK = (v: string) => /^[A-Za-z0-9-]{1,64}$/.test(v)
function openCDN(r: Row) {
  cdnEdit.value = { row: r, headers: [...(r.protect.cdn_headers?.length ? r.protect.cdn_headers : DEFAULT_CDN_HEADERS)], add: '' }
}
function addHeader() {
  const e = cdnEdit.value!
  const v = e.add.trim()
  if (!headerOK(v) || e.headers.some((x) => x.toLowerCase() === v.toLowerCase())) return msg.warning(t('prot.cdn_bad_header'))
  e.headers.push(v)
  e.add = ''
}
function moveHeader(i: number, d: number) {
  const hs = cdnEdit.value!.headers
  const j = i + d
  if (j < 0 || j >= hs.length) return
  ;[hs[i], hs[j]] = [hs[j], hs[i]]
}
async function saveCDN() {
  const e = cdnEdit.value!
  const same = e.headers.length === DEFAULT_CDN_HEADERS.length && e.headers.every((x, i) => x === DEFAULT_CDN_HEADERS[i])
  await set(e.row, { cdn_headers: same ? [] : e.headers })
  cdnEdit.value = null
}
const cdnCell = (r: Row) =>
  h(NSpace, { size: 4, align: 'center', wrap: false }, () => [
    h(NCheckbox, { checked: r.protect.cdn, disabled: !r.supported, 'onUpdate:checked': (v: boolean) => set(r, { cdn: v }) }),
    r.protect.cdn ? h(NButton, { size: 'tiny', quaternary: true, title: t('prot.cdn_headers'), onClick: () => openCDN(r) }, () => 'Headers') : null,
  ])

const box = (r: Row, key: 'fake_spider' | 'idc' | 'crawler') =>
  h(NCheckbox, { checked: r.protect[key], disabled: !r.supported || !r.protect.on, 'onUpdate:checked': (v: boolean) => set(r, { [key]: v }) })
const head = (key: string) => () => h(NTooltip, null, { trigger: () => h('span', { style: 'border-bottom: 1px dashed; cursor: help' }, t(`prot.cols.${key}`)), default: () => t(`prot.tips.${key}`) })
const columns = computed<DataTableColumns<Row>>(() => [
  { type: 'selection' },
  {
    title: () => t('prot.cols.site'),
    key: 'domain',
    minWidth: 200,
    render: (r) =>
      h(NSpace, { size: 4, align: 'center' }, () => [
        h('span', r.domain),
        r.aliases.length ? h(NTag, { size: 'tiny', bordered: false }, () => `+${r.aliases.length}`) : null,
        r.proxied ? h(NTag, { size: 'tiny', type: 'warning', bordered: false }, () => 'Cloudflare ☁') : null,
        !r.supported ? h(NTag, { size: 'tiny', bordered: false }, () => t('prot.passthrough')) : null,
      ]),
  },
  {
    title: () => t('prot.cols.blocked'),
    key: 'blocked',
    width: 110,
    sorter: (a, b) => a.blocked - b.blocked,
    render: (r) => h(NTooltip, null, { trigger: () => h('span', { style: r.blocked ? 'color: #d03050' : '' }, r.blocked.toLocaleString()), default: () => t('prot.blocked_24h', { n: r.blocked_24h.toLocaleString() }) }),
  },
  { title: head('fake_spider'), key: 'fake_spider', width: 90, render: (r) => box(r, 'fake_spider') },
  { title: head('cdn'), key: 'cdn', width: 120, render: cdnCell },
  { title: head('idc'), key: 'idc', width: 80, render: (r) => box(r, 'idc') },
  { title: head('crawler'), key: 'crawler', width: 90, render: (r) => box(r, 'crawler') },
  {
    title: head('cc'),
    key: 'cc',
    width: 150,
    render: (r) =>
      h(NInputNumber, {
        size: 'tiny', min: 0, max: 100000, step: 10, value: r.protect.cc, disabled: !r.supported || !r.protect.on, placeholder: t('prot.off'), style: 'width: 110px',
        'onUpdate:value': (v: number | null) => (r.protect.cc = v ?? 0),
        onBlur: () => set(r, { cc: r.protect.cc }),
      }),
  },
  {
    title: head('waf'),
    key: 'waf',
    width: 140,
    render: (r) => {
      const x = waf.value[r.domain]
      if (!x || !r.supported) return '—'
      return h(NSpace, { size: 6, align: 'center', wrap: false }, () => [
        h(NSwitch, { size: 'small', value: !x.waf_off, disabled: !wafOn.value, 'onUpdate:value': (on: boolean) => { x.waf_off = !on; saveWAF(x) } }),
        h(NButton, { size: 'tiny', quaternary: true, onClick: () => (exclEdit.value = { ...x, waf_exclude: [...x.waf_exclude] }) }, () => t('prot.excluded', { n: x.waf_exclude.length })),
      ])
    },
  },
  { title: () => t('common.status'), key: 'on', width: 80, render: (r) => h(NSwitch, { size: 'small', value: r.protect.on, disabled: !r.supported, 'onUpdate:value': (v: boolean) => set(r, { on: v }) }) },
  { title: '', key: 'log', width: 100, render: (r) => h(NButton, { size: 'tiny', onClick: () => openLog(r.domain) }, () => t('prot.log')) },
])
</script>

<template>
  <n-space justify="space-between" align="center" style="margin-bottom: 12px">
    <n-space align="center" :size="8">
      <n-tag :type="info.rules_on ? 'success' : 'default'" size="small" :bordered="false">{{ t('prot.rules_state', { s: info.rules_on ? t('sec.on') : t('sec.off') }) }}</n-tag>
      <n-tag size="small" :bordered="false">{{ t('prot.spiders', { n: info.spiders.length }) }}</n-tag>
    </n-space>
    <n-space :size="8">
      <n-input v-model:value="q" size="small" clearable :placeholder="t('prot.search')" style="width: 200px" />
      <n-button size="small" @click="openSettings">{{ t('prot.settings') }}</n-button>
      <n-button size="small" @click="load">{{ t('common.refresh') }}</n-button>
    </n-space>
  </n-space>
  <n-alert v-if="usesFake && !info.spiders.length" type="warning" :show-icon="false" style="margin-bottom: 12px">{{ t('prot.no_spiders') }}</n-alert>
  <n-alert v-if="usesIDC && !info.has_asn" type="warning" :show-icon="false" style="margin-bottom: 12px">{{ t('prot.no_asn') }}</n-alert>

  <n-data-table
    v-model:checked-row-keys="checked" size="small" :columns="columns" :data="shown" :loading="loading"
    :row-key="(r: Row) => r.domain" :pagination="{ pageSize: 20 }" :scroll-x="1100"
  />
  <n-space align="center" :size="8" style="margin-top: 10px">
    <n-select v-model:value="batch" :options="batchOptions" size="small" :placeholder="t('prot.batch_pick')" style="width: 200px" />
    <n-button size="small" type="primary" :disabled="!batch || !checked.length" @click="applyBatch">{{ t('prot.batch_apply', { n: checked.length }) }}</n-button>
  </n-space>
  <ul class="notes">
    <li>{{ t('prot.note_order') }}</li>
    <li>{{ t('prot.note_cdn') }}</li>
    <li>{{ t('prot.note_count') }}</li>
  </ul>

  <n-drawer :show="!!logSite" :width="1000" style="max-width: 100vw" @update:show="(v: boolean) => !v && (logSite = null)">
    <n-drawer-content :title="t('prot.log_title', { domain: logSite })" closable>
      <SecEvents v-if="logSite" :site="logSite" @task="(id: string, title: string) => (task = { id, title })" @changed="load" />
    </n-drawer-content>
  </n-drawer>
  <n-modal :show="!!exclEdit" @update:show="(v: boolean) => !v && (exclEdit = null)">
    <n-card v-if="exclEdit" :title="t('prot.excl_title', { domain: exclEdit.domain })" style="width: min(520px, calc(100vw - 32px))" closable @close="exclEdit = null">
      <p class="hint">{{ t('prot.excl_hint') }}</p>
      <n-dynamic-tags v-model:value="exclEdit.waf_exclude" />
      <template #footer>
        <n-space justify="end">
          <n-button @click="exclEdit = null">{{ t('common.cancel') }}</n-button>
          <n-button type="primary" @click="saveWAF({ ...exclEdit!, waf_exclude: exclEdit!.waf_exclude.filter((id) => /^\d{1,7}$/.test(id)) }); exclEdit = null; loadWAF()">{{ t('common.save') }}</n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>
  <TaskModal :task-id="task?.id ?? null" :title="task?.title ?? ''" @close="task = null; loadWAF()" @done="loadWAF" />

  <n-modal :show="settingsOpen" @update:show="(v: boolean) => (settingsOpen = v)">
    <n-card :title="t('prot.settings')" style="width: min(640px, calc(100vw - 32px))" closable @close="settingsOpen = false">
      <n-space vertical :size="12">
        <div>
          <b>{{ t('prot.cols.crawler') }}</b>
          <p class="hint">{{ t('prot.crawlers_hint') }}</p>
          <n-input v-model:value="crawlers" type="textarea" :autosize="{ minRows: 5, maxRows: 12 }" />
        </div>
        <div>
          <b>{{ t('prot.cols.idc') }}</b>
          <p class="hint">{{ t('prot.idc_hint', { n: idcRanges.toLocaleString() }) }}</p>
          <n-input v-model:value="idc" type="textarea" :autosize="{ minRows: 5, maxRows: 12 }" placeholder="16509 Amazon AWS" />
        </div>
      </n-space>
      <template #footer>
        <n-space justify="end">
          <n-button @click="settingsOpen = false">{{ t('common.cancel') }}</n-button>
          <n-button type="primary" @click="saveSettings">{{ t('common.save') }}</n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>

  <n-modal :show="!!cdnEdit" @update:show="(v: boolean) => !v && (cdnEdit = null)">
    <n-card v-if="cdnEdit" :title="t('prot.cdn_headers_of', { site: cdnEdit.row.domain })" style="width: min(520px, calc(100vw - 32px))" closable @close="cdnEdit = null">
      <n-space :wrap="false" style="margin-bottom: 12px">
        <n-input v-model:value="cdnEdit.add" :placeholder="t('prot.cdn_header_name')" @keyup.enter="addHeader" />
        <n-button type="primary" @click="addHeader">{{ t('prot.cdn_add') }}</n-button>
      </n-space>
      <div class="hdrs">
        <div v-for="(hd, i) in cdnEdit.headers" :key="hd" class="hdr">
          <span class="n">{{ i + 1 }}</span>
          <code>{{ hd }}</code>
          <n-button size="tiny" quaternary :disabled="i === 0" :aria-label="t('menucfg.up')" @click="moveHeader(i, -1)">↑</n-button>
          <n-button size="tiny" quaternary :disabled="i === cdnEdit.headers.length - 1" :aria-label="t('menucfg.down')" @click="moveHeader(i, 1)">↓</n-button>
          <n-button size="tiny" quaternary type="error" :disabled="cdnEdit.headers.length === 1" @click="cdnEdit.headers.splice(i, 1)">{{ t('common.delete') }}</n-button>
        </div>
      </div>
      <p class="note">• {{ t('prot.cdn_note') }}</p>
      <n-alert type="warning" :show-icon="false" style="margin-top: 8px; font-size: 12px">{{ t('prot.cdn_risk') }}</n-alert>
      <template #footer>
        <n-space justify="space-between">
          <n-button quaternary @click="cdnEdit.headers = [...DEFAULT_CDN_HEADERS]">{{ t('prot.cdn_reset') }}</n-button>
          <n-space>
            <n-button @click="cdnEdit = null">{{ t('common.cancel') }}</n-button>
            <n-button type="primary" @click="saveCDN">{{ t('common.save') }}</n-button>
          </n-space>
        </n-space>
      </template>
    </n-card>
  </n-modal>
</template>

<style scoped>
.hdrs {
  max-height: 46vh;
  overflow: auto;
  border: 1px solid rgba(128, 128, 128, 0.2);
  border-radius: 6px;
}
.hdr {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 10px;
  border-bottom: 1px solid rgba(128, 128, 128, 0.12);
}
.hdr code {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
}
.hdr .n {
  width: 18px;
  font-size: 12px;
  opacity: 0.5;
}
.note {
  font-size: 12px;
  opacity: 0.75;
  margin: 10px 0 0;
}
.notes {
  font-size: 12px;
  opacity: 0.65;
  margin: 10px 0 0;
  padding-left: 18px;
}
.hint {
  font-size: 12px;
  opacity: 0.65;
  margin: 2px 0 6px;
}
</style>
