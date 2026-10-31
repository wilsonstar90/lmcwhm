<script setup lang="ts">
// The OWASP CRS rules of the WAF image: level, hits in 24 hours, switched off
// for every site, or excluded for one site.
import { computed, h, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NCard, NDataTable, NInput, NModal, NSelect, NSpace, NSwitch, NTag, useMessage, type DataTableColumns,
} from 'naive-ui'
import { get, patch, put, ApiError } from '../api/client'

interface CRSRule { id: string; message: string; severity: string; level: number; phase: number; hits: number }
interface Category { code: string; file: string; rules: CRSRule[] }
interface SiteWAF { id: number; domain: string; waf_off: boolean; waf_exclude: string[] | null }

const props = defineProps<{ focus?: string }>()
const emit = defineEmits<{ task: [id: string, title: string] }>()
const { t, te } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))

const available = ref(true)
const paranoia = ref(1)
const disabled = ref<Set<string>>(new Set())
const cats = ref<Category[]>([])
const sites = ref<SiteWAF[]>([])
const cat = ref<string>('')
const q = ref('')
const level = ref(0)
const hitOnly = ref(false)
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    const [c, w] = await Promise.all([
      get<{ available: boolean; paranoia: number; disabled: string[] | null; categories: Category[] }>('/waf/crs'),
      get<{ sites: SiteWAF[] }>('/waf'),
    ])
    available.value = c.available
    paranoia.value = c.paranoia
    disabled.value = new Set(c.disabled ?? [])
    cats.value = c.categories
    sites.value = w.sites.map((x) => ({ ...x, waf_exclude: x.waf_exclude ?? [] }))
  } catch (e) {
    fail(e)
  } finally {
    loading.value = false
  }
}
onMounted(load)
watch(() => props.focus, (id) => {
  if (!id) return
  q.value = id
  cat.value = ''
})
if (props.focus) q.value = props.focus

const catName = (c: Category) => (te(`crs.cats.${c.code}`) ? t(`crs.cats.${c.code}`) : c.file)
const all = computed(() => cats.value.flatMap((c) => c.rules.map((r) => ({ ...r, cat: c.code }))))
const shown = computed(() => {
  const s = q.value.trim().toLowerCase()
  return all.value.filter((r) =>
    (!cat.value || r.cat === cat.value) && (!level.value || r.level === level.value) && (!hitOnly.value || r.hits > 0) &&
    (!s || r.id.includes(s) || r.message.toLowerCase().includes(s)))
})
const catOptions = computed(() => [
  { label: `${t('db.all')} (${all.value.length})`, value: '' },
  ...cats.value.map((c) => ({ label: `${c.code} · ${catName(c)} (${c.rules.length}${hitsOf(c) ? ` · ${hitsOf(c)} ⚑` : ''})`, value: c.code })),
])
const hitsOf = (c: Category) => c.rules.reduce((n, r) => n + r.hits, 0)

async function setDisabled(id: string, off: boolean) {
  const d = new Set(disabled.value)
  off ? d.add(id) : d.delete(id)
  try {
    const r = await put<{ task_id: string }>('/waf/crs/disabled', { ids: [...d] })
    disabled.value = d
    emit('task', r.task_id, `${off ? t('crs.disable') : t('crs.enable')} · ${id}`)
  } catch (e) {
    fail(e)
  }
}

// ---- exclude for one site ----
const excl = ref<{ id: string; site: number | null } | null>(null)
const siteOptions = computed(() => sites.value.map((x) => ({ label: x.domain, value: x.id })))
const excludedOn = (id: string) => sites.value.filter((x) => x.waf_exclude!.includes(id)).map((x) => x.domain)
async function exclude() {
  const x = sites.value.find((s) => s.id === excl.value?.site)
  if (!x || !excl.value) return
  try {
    const r = await patch<{ task_id: string }>(`/sites/${x.id}`, { waf_exclude: [...new Set([...x.waf_exclude!, excl.value.id])] })
    emit('task', r.task_id, `WAF · ${x.domain}`)
    excl.value = null
    load()
  } catch (e) {
    fail(e)
  }
}

const sevType = (sv: string) => (['EMERGENCY', 'ALERT', 'CRITICAL'].includes(sv) ? 'error' : sv === 'ERROR' || sv === 'WARNING' ? 'warning' : 'default')
const columns = computed<DataTableColumns<CRSRule & { cat: string }>>(() => [
  { title: 'ID', key: 'id', width: 90, render: (r) => h('code', r.id) },
  {
    title: () => t('rules.level'),
    key: 'level',
    width: 110,
    render: (r) =>
      h(NSpace, { size: 4, align: 'center', wrap: false }, () => [
        h(NTag, { size: 'small', bordered: false, type: r.level <= paranoia.value ? 'info' : 'default' }, () => `PL${r.level}`),
        r.level > paranoia.value ? h('span', { style: 'font-size:12px;opacity:.6' }, t('rules.inactive')) : null,
      ]),
  },
  { title: () => t('crs.severity'), key: 'severity', width: 110, render: (r) => (r.severity ? h(NTag, { size: 'tiny', type: sevType(r.severity) }, () => r.severity) : '—') },
  { title: () => t('rules.description'), key: 'message', minWidth: 300, ellipsis: { tooltip: true } },
  { title: () => t('crs.hits'), key: 'hits', width: 90, sorter: (a, b) => a.hits - b.hits, render: (r) => (r.hits ? h('b', { style: 'color:#d03050' }, String(r.hits)) : '0') },
  {
    title: () => t('crs.excluded_on'),
    key: 'excl',
    width: 180,
    ellipsis: { tooltip: true },
    render: (r) => excludedOn(r.id).join(', ') || '—',
  },
  { title: () => t('crs.all_sites'), key: 'on', width: 90, render: (r) => h(NSwitch, { size: 'small', value: !disabled.value.has(r.id), 'onUpdate:value': (on: boolean) => setDisabled(r.id, !on) }) },
  { title: '', key: 'act', width: 120, render: (r) => h(NButton, { size: 'tiny', onClick: () => (excl.value = { id: r.id, site: null }) }, () => t('crs.exclude_site')) },
])
</script>

<template>
  <n-alert v-if="!available" type="info" :show-icon="false" style="margin-bottom: 12px">{{ t('crs.unavailable') }}</n-alert>
  <template v-else>
    <div class="bar">
      <n-select v-model:value="cat" :options="catOptions" size="small" class="cat" />
      <n-select v-model:value="level" :options="[{ label: t('crs.any_level'), value: 0 }, ...[1, 2, 3, 4].map((n) => ({ label: `PL${n}`, value: n }))]" size="small" style="width: 120px" />
      <n-input v-model:value="q" size="small" clearable :placeholder="t('crs.search')" style="width: 220px" />
      <n-button size="small" :type="hitOnly ? 'primary' : 'default'" @click="hitOnly = !hitOnly">{{ t('crs.hit_only') }}</n-button>
      <n-button size="small" @click="load">{{ t('common.refresh') }}</n-button>
    </div>
    <p class="hint">{{ t('crs.hint', { n: disabled.size }) }}</p>
    <n-data-table size="small" :columns="columns" :data="shown" :loading="loading" :row-key="(r: CRSRule) => r.id" :pagination="{ pageSize: 50 }" :scroll-x="1150" />
  </template>

  <n-modal :show="!!excl" @update:show="(v: boolean) => !v && (excl = null)">
    <n-card v-if="excl" :title="t('crs.exclude_title', { id: excl.id })" style="width: min(440px, calc(100vw - 32px))" closable @close="excl = null">
      <n-select v-model:value="excl.site" :options="siteOptions" filterable :placeholder="t('route.site')" />
      <template #footer>
        <n-space justify="end">
          <n-button @click="excl = null">{{ t('common.cancel') }}</n-button>
          <n-button type="primary" :disabled="!excl.site" @click="exclude">{{ t('common.confirm') }}</n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>
</template>

<style scoped>
.bar {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
  margin-bottom: 8px;
}
.cat {
  width: min(360px, 100%);
}
.hint {
  font-size: 12px;
  opacity: 0.65;
  margin: 0 0 8px;
}
</style>
