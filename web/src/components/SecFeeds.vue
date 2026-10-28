<script setup lang="ts">
import { computed, h, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { NAlert, NButton, NCard, NCheckbox, NDataTable, NInput, NSpace, NSwitch, NTag, useMessage, type DataTableColumns } from 'naive-ui'
import { get, post, put, ApiError } from '../api/client'
import { datetime } from '../format'
import DetailDrawer, { type DetailRow } from './DetailDrawer.vue'

// One card per kind: "block" (malicious IPs) or "trust" (Cloudflare and
// crawlers, never banned). Both share one configuration; each kind is
// updated by its own scheduled task.
const props = defineProps<{ kind: 'block' | 'trust' }>()
const emit = defineEmits<{ changed: [] }>()

interface Feed { id: string; name: string; url: string; info?: string; kind: string; group?: string }
interface FeedStatus { last_run: number; entries: number; skipped: number; error?: string }
interface Job { id: number; name: string; schedule: string; enabled: boolean; next: number; last?: { status: string; started_at: number } }
interface Feeds { enabled: string[]; custom: Feed[]; status: Record<string, FeedStatus>; last_run: number; running: boolean; catalog: Feed[]; blocking: number; trusted: number; jobs: Record<string, Job> }

const { t, locale } = useI18n()
const router = useRouter()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))
const f = ref<Feeds | null>(null)
const enabled = ref<string[]>([])
const custom = ref<Feed[]>([])
const saving = ref(false)
const detail = ref<Feed | null>(null)
let timer: number | undefined

const mine = (x: Feed) => x.kind === props.kind
const p = computed(() => (props.kind === 'trust' ? 'fwtrust' : 'fwfeed'))
const job = computed(() => f.value?.jobs?.[props.kind] ?? null)

function reset(v: Feeds) {
  enabled.value = [...v.enabled]
  custom.value = v.custom.filter(mine).map((x) => ({ ...x }))
}
async function load() {
  try {
    f.value = await get<Feeds>('/firewall/feeds')
    if (!saving.value) reset(f.value)
  } catch (e) {
    fail(e)
  }
  clearTimeout(timer)
  if (f.value?.running) timer = window.setTimeout(load, 3000)
}
onMounted(load)
onUnmounted(() => clearTimeout(timer))

const catalog = computed(() => (f.value?.catalog ?? []).filter(mine))
const rows = computed<Feed[]>(() => [...catalog.value, ...(f.value?.custom ?? []).filter(mine)])
const dirty = computed(() => {
  if (!f.value) return false
  return JSON.stringify([...enabled.value].sort()) !== JSON.stringify([...f.value.enabled].sort()) ||
    JSON.stringify(custom.value) !== JSON.stringify(f.value.custom.filter(mine))
})
const count = computed(() => (props.kind === 'trust' ? f.value?.trusted : f.value?.blocking) ?? 0)
const selected = computed(() => rows.value.filter((x) => enabled.value.includes(x.id)).length)

function toggle(id: string, on: boolean) {
  enabled.value = on ? [...new Set([...enabled.value, id])] : enabled.value.filter((x) => x !== id)
}
const groups = computed(() => [...new Set(catalog.value.map((x) => x.group).filter((g): g is string => !!g))])
const groupOn = (g: string) => catalog.value.filter((x) => x.group === g).every((x) => enabled.value.includes(x.id))
function toggleGroup(g: string, on: boolean) {
  const ids = catalog.value.filter((x) => x.group === g).map((x) => x.id)
  enabled.value = on ? [...new Set([...enabled.value, ...ids])] : enabled.value.filter((x) => !ids.includes(x))
}

async function save() {
  saving.value = true
  try {
    // Custom lists are always on (the server numbers them custom1..n); the
    // other card's custom lists are sent back unchanged.
    const others = (f.value?.custom ?? []).filter((x) => !mine(x))
    const list = [...others, ...custom.value.filter((c) => c.url.trim()).map((c) => ({ ...c, kind: props.kind }))]
    const ids = enabled.value.filter((id) => !id.startsWith('custom'))
    f.value = await put<Feeds>('/firewall/feeds', { enabled: ids, custom: list })
    reset(f.value)
    msg.success(t('fwfeed.saved'))
    emit('changed')
  } catch (e) {
    fail(e)
  } finally {
    saving.value = false
  }
  setTimeout(load, 1500)
}
async function refresh() {
  try {
    await post('/firewall/feeds/refresh')
    setTimeout(load, 1000)
  } catch (e) {
    fail(e)
  }
}

const statusTag = (x: Feed) => {
  const s = f.value?.status[x.id]
  if (!enabled.value.includes(x.id) && !x.id.startsWith('custom')) return h('span', { class: 'hint' }, t('fwfeed.not_selected'))
  if (!s) return h(NTag, { size: 'small' }, () => t('fwfeed.pending'))
  if (s.error) return h(NTag, { size: 'small', type: 'error' }, () => t('fwfeed.failed'))
  return h(NTag, { size: 'small', type: 'success' }, () => s.entries.toLocaleString())
}
const cols = computed<DataTableColumns<Feed>>(() => [
  {
    title: '', key: 'on', width: 44,
    render: (x) => (x.id.startsWith('custom') ? null : h('span', { onClick: (e: Event) => e.stopPropagation() }, [h(NCheckbox, { checked: enabled.value.includes(x.id), 'onUpdate:checked': (v: boolean) => toggle(x.id, v) })])),
  },
  { title: () => t('fwfeed.list'), key: 'name', render: (x) => h('div', [h('div', { class: 'name' }, x.name), x.info ? h('div', { class: 'hint' }, x.info) : null]) },
  ...(props.kind === 'trust' ? [{ title: () => t('fwtrust.group'), key: 'group', width: 120, render: (x: Feed) => (x.group ? t(`fwtrust.groups.${x.group}`) : t('fwfeed.custom_short')) }] : []),
  { title: () => t('fwfeed.entries'), key: 'entries', width: 120, render: statusTag },
  { title: () => t('fwfeed.updated'), key: 'last_run', width: 170, render: (x) => { const s = f.value?.status[x.id]; return s ? datetime(s.last_run, locale.value) : '—' } },
])
const rowProps = (x: Feed) => ({ style: 'cursor: pointer', onClick: () => (detail.value = x) })
const detailRows = computed<DetailRow[]>(() => {
  const x = detail.value
  if (!x) return []
  const s = f.value?.status[x.id]
  return [
    { label: t('fwfeed.list'), value: x.name },
    { label: t('fwfeed.about'), value: x.info },
    { label: 'URL', value: () => h('a', { href: x.url, target: '_blank', rel: 'noopener noreferrer', style: 'word-break: break-all' }, x.url) },
    { label: t('fwfeed.state'), value: x.id.startsWith('custom') || enabled.value.includes(x.id) ? t('fwfeed.selected') : t('fwfeed.not_selected') },
    { label: t('fwfeed.entries'), value: s ? s.entries.toLocaleString() : '' },
    { label: t('fwfeed.skipped_label'), value: s ? s.skipped : '' },
    { label: t('fwfeed.updated'), value: s ? datetime(s.last_run, locale.value) : '' },
    { label: t('fwfeed.error'), value: s?.error, mono: true },
  ]
})
</script>

<template>
  <n-card size="small" :title="t(`${p}.title`)">
    <template #header-extra>
      <n-space align="center">
        <n-button size="small" :loading="f?.running" :disabled="!selected" @click="refresh">{{ t('fwfeed.refresh') }}</n-button>
        <n-button size="small" type="primary" :disabled="!dirty" :loading="saving" @click="save">{{ t('common.save') }}</n-button>
      </n-space>
    </template>
    <p class="hint" style="margin-top: 0">{{ t(`${p}.intro`) }}</p>
    <div class="facts">
      <div><span class="k">{{ t(`${p}.count`) }}</span><b>{{ count.toLocaleString() }}</b></div>
      <div><span class="k">{{ t('fwfeed.selected_n') }}</span><b>{{ selected }}</b></div>
      <div>
        <span class="k">{{ t('fwfeed.schedule') }}</span>
        <template v-if="job">
          <a class="link" @click="router.push({ name: 'cron' })">{{ job.name }}</a>
          <span class="hint"> · <code>{{ job.schedule }}</code> · {{ job.enabled && job.next ? t('fwfeed.next', { at: datetime(job.next, locale) }) : t('fwfeed.job_off') }}</span>
        </template>
        <span v-else class="hint">{{ t('fwfeed.no_job') }}</span>
      </div>
    </div>
    <n-alert v-if="job && !job.enabled" type="warning" :show-icon="false" style="margin-bottom: 8px">{{ t('fwfeed.job_disabled') }}</n-alert>
    <n-space v-if="groups.length" style="margin-bottom: 8px">
      <n-checkbox v-for="g in groups" :key="g" :checked="groupOn(g)" @update:checked="(v: boolean) => toggleGroup(g, v)">{{ t(`fwtrust.groups.${g}`) }}</n-checkbox>
    </n-space>
    <n-data-table size="small" :columns="cols" :data="rows" :row-key="(x: Feed) => x.id" :row-props="rowProps" :scroll-x="640" />
    <details class="custom" :open="custom.length > 0">
      <summary>{{ t('fwfeed.custom') }}</summary>
      <n-space vertical style="margin-top: 8px">
        <n-space v-for="(c, i) in custom" :key="i" align="center" :wrap="false">
          <n-input v-model:value="c.name" size="small" :placeholder="t('fwfeed.name')" style="width: 160px" />
          <n-input v-model:value="c.url" size="small" placeholder="https://example.com/list.txt" style="width: 340px" />
          <n-button size="small" quaternary type="error" @click="custom.splice(i, 1)">{{ t('common.delete') }}</n-button>
        </n-space>
        <n-button size="small" dashed @click="custom.push({ id: '', name: '', url: '', kind })">{{ t('fwfeed.add_custom') }}</n-button>
      </n-space>
    </details>
  </n-card>

  <DetailDrawer :show="!!detail" :title="detail?.name ?? ''" :rows="detailRows" @close="detail = null">
    <template v-if="detail && !detail.id.startsWith('custom')" #actions>
      <n-space align="center">
        <span>{{ t('fwfeed.selected') }}</span>
        <n-switch :value="enabled.includes(detail.id)" @update:value="(v: boolean) => toggle(detail!.id, v)" />
        <n-button type="primary" :disabled="!dirty" :loading="saving" @click="save(); detail = null">{{ t('common.save') }}</n-button>
      </n-space>
    </template>
  </DetailDrawer>
</template>

<style scoped>
:deep(.hint),
.hint {
  font-size: 12px;
  opacity: 0.65;
  margin: 6px 0 0;
}
:deep(.name) {
  font-weight: 500;
}
.facts {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 28px;
  margin: 10px 0 12px;
  font-size: 13px;
}
.facts .k {
  opacity: 0.6;
  margin-right: 8px;
}
.link {
  cursor: pointer;
  color: var(--primary, #18a058);
}
.custom {
  margin-top: 12px;
  font-size: 13px;
}
.custom summary {
  cursor: pointer;
  opacity: 0.75;
}
</style>
