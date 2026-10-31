<script setup lang="ts">
// Recycle bin (like aaPanel's): deleted files, and databases dropped through
// the panel (kept as a dump). Either bin can be switched off.
import { computed, h, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NCard, NDataTable, NDatePicker, NInput, NModal, NPopconfirm, NSelect, NSpace, NSwitch, NTab, NTabs, useMessage,
  type DataTableColumns,
} from 'naive-ui'
import { get, post, put, ApiError } from '../../api/client'
import type { TrashItem } from '../../api/types'
import { bytes, datetime } from '../../format'
import TaskModal from '../TaskModal.vue'
import { baseName, changed, iconOf, isDoc, isImage } from './state'

const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{ close: [] }>()
const { t, locale } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))

interface TrashedDB { id: number; engine: string; name: string; username: string; remark: string; size: number; deleted_at: number }
const tab = ref<'files' | 'db'>('files')
const files = ref<TrashItem[]>([])
const dbs = ref<TrashedDB[]>([])
const settings = ref({ enabled: true, db: true })
const kind = ref<'all' | 'dir' | 'file' | 'image' | 'doc'>('all')
const q = ref('')
const range = ref<[number, number] | null>(null)
const checked = ref<(string | number)[]>([])
const batch = ref<string | null>(null)
const loading = ref(false)
const task = ref<{ id: string; title: string } | null>(null)

async function load() {
  loading.value = true
  try {
    const [f, d] = await Promise.all([
      get<{ items: TrashItem[]; settings: { enabled: boolean; db: boolean } }>('/files/trash'),
      get<{ items: TrashedDB[] }>('/databases/trash').catch(() => ({ items: [] as TrashedDB[] })),
    ])
    files.value = f.items
    settings.value = f.settings ?? settings.value
    dbs.value = d.items
    checked.value = []
  } catch (e) {
    fail(e)
  } finally {
    loading.value = false
  }
}
watch(() => props.show, (v) => v && load(), { immediate: true })
watch(tab, () => (checked.value = []))

async function setSetting(key: 'enabled' | 'db', v: boolean) {
  if (!v && !confirm(t(key === 'enabled' ? 'rb.off_confirm' : 'rb.db_off_confirm'))) return
  try {
    settings.value = await put('/files/trash/settings', { ...settings.value, [key]: v })
  } catch (e) {
    fail(e)
  }
}

const kindOf = (i: TrashItem) => (i.is_dir ? 'dir' : isImage(i.path) ? 'image' : isDoc(i.path) ? 'doc' : 'file')
const counts = computed(() => {
  const c: Record<string, number> = { all: files.value.length, dir: 0, file: 0, image: 0, doc: 0 }
  for (const i of files.value) {
    const k = kindOf(i)
    c[k]++
    if (k !== 'dir') c.file += k === 'file' ? 0 : 1
  }
  return c
})
const inRange = (ts: number) => !range.value || (ts * 1000 >= range.value[0] && ts * 1000 < range.value[1] + 86400000)
const shownFiles = computed(() => {
  const s = q.value.trim().toLowerCase()
  return files.value.filter((i) => {
    const k = kindOf(i)
    const kindOK = kind.value === 'all' || (kind.value === 'file' ? k !== 'dir' : k === kind.value)
    return kindOK && inRange(i.deleted_at) && (!s || i.path.toLowerCase().includes(s))
  })
})
const shownDBs = computed(() => {
  const s = q.value.trim().toLowerCase()
  return dbs.value.filter((d) => inRange(d.deleted_at) && (!s || d.name.toLowerCase().includes(s)))
})
const totalSize = computed(() => shownFiles.value.reduce((n, i) => n + i.size, 0))

async function fileAction(url: string, ids: string[]) {
  try {
    for (const id of ids) await post(url, { id })
    msg.success(t('common.done'))
    changed.value++
  } catch (e) {
    fail(e)
  }
  load()
}
async function dbRestore(d: TrashedDB) {
  try {
    const r = await post<{ task_id: string }>(`/databases/trash/${d.id}/restore`)
    task.value = { id: r.task_id, title: `${t('files.restore')} · ${d.name}` }
  } catch (e) {
    fail(e)
  }
}
async function dbDelete(ids: number[]) {
  try {
    for (const id of ids) await post(`/databases/trash/${id}/delete`)
    msg.success(t('common.done'))
  } catch (e) {
    fail(e)
  }
  load()
}
async function emptyAll() {
  try {
    if (tab.value === 'files') await post('/files/trash/empty', { id: '' })
    else await post('/databases/trash/empty')
    msg.success(t('common.done'))
  } catch (e) {
    fail(e)
  }
  load()
}
function runBatch() {
  const ids = checked.value
  if (!batch.value || !ids.length) return
  if (batch.value === 'delete' && !confirm(t('rb.delete_n', { n: ids.length }))) return
  if (tab.value === 'files') fileAction(batch.value === 'restore' ? '/files/trash/restore' : '/files/trash/empty', ids as string[])
  else if (batch.value === 'delete') dbDelete(ids as number[])
  else for (const id of ids) dbRestore(dbs.value.find((d) => d.id === id)!)
}

const link = (label: string, onClick: () => void, danger = false) => h('a', { class: ['op', { danger }], onClick }, label)
const fileCols = computed<DataTableColumns<TrashItem>>(() => [
  { type: 'selection' },
  { title: () => t('rb.name'), key: 'name', minWidth: 180, ellipsis: { tooltip: true }, render: (i) => `${iconOf(i.path, i.is_dir)} ${baseName(i.path)}` },
  { title: () => t('rb.origin'), key: 'path', minWidth: 220, ellipsis: { tooltip: true } },
  { title: () => t('files.size'), key: 'size', width: 100, sorter: (a, b) => a.size - b.size, render: (i) => (i.is_dir ? '—' : bytes(i.size)) },
  { title: () => t('rb.deleted_at'), key: 'deleted_at', width: 170, sorter: (a, b) => a.deleted_at - b.deleted_at, render: (i) => datetime(i.deleted_at, locale.value) },
  {
    title: () => t('common.actions'),
    key: 'ops',
    width: 120,
    align: 'right',
    render: (i) =>
      h(NSpace, { size: 10, justify: 'end' }, () => [
        link(t('files.restore'), () => fileAction('/files/trash/restore', [i.id])),
        h(NPopconfirm, { onPositiveClick: () => fileAction('/files/trash/empty', [i.id]) }, { trigger: () => link(t('common.delete'), () => {}, true), default: () => t('rb.delete_one') }),
      ]),
  },
])
const dbCols = computed<DataTableColumns<TrashedDB>>(() => [
  { type: 'selection' },
  { title: () => t('rb.db_name'), key: 'name', minWidth: 160 },
  { title: () => t('rb.engine'), key: 'engine', width: 110 },
  { title: () => t('rb.user'), key: 'username', width: 130 },
  { title: () => t('files.size'), key: 'size', width: 100, render: (d) => bytes(d.size) },
  { title: () => t('rb.deleted_at'), key: 'deleted_at', width: 170, render: (d) => datetime(d.deleted_at, locale.value) },
  {
    title: () => t('common.actions'),
    key: 'ops',
    width: 120,
    align: 'right',
    render: (d) =>
      h(NSpace, { size: 10, justify: 'end' }, () => [
        link(t('files.restore'), () => dbRestore(d)),
        h(NPopconfirm, { onPositiveClick: () => dbDelete([d.id]) }, { trigger: () => link(t('common.delete'), () => {}, true), default: () => t('rb.delete_one') }),
      ]),
  },
])
const kinds = computed(() => (['all', 'dir', 'file', 'image', 'doc'] as const).map((k) => ({ k, n: k === 'file' ? files.value.filter((i) => !i.is_dir).length : counts.value[k] })))
</script>

<template>
  <n-modal :show="show" @update:show="(v: boolean) => !v && emit('close')">
    <n-card :title="t('files.trash')" style="width: min(1100px, calc(100vw - 24px))" closable @close="emit('close')">
      <n-tabs v-model:value="tab" type="card" size="small">
        <n-tab name="files">{{ t('rb.files') }}</n-tab>
        <n-tab name="db">{{ t('rb.db') }}</n-tab>
      </n-tabs>
      <div class="head">
        <span class="sw">
          {{ tab === 'files' ? t('rb.files') : t('rb.db') }}
          <n-switch :value="tab === 'files' ? settings.enabled : settings.db" @update:value="(v: boolean) => setSetting(tab === 'files' ? 'enabled' : 'db', v)" />
        </span>
        <n-alert type="warning" :show-icon="true" class="warn">{{ tab === 'files' ? t('rb.warn') : t('rb.db_warn') }}</n-alert>
        <span class="grow" />
        <n-popconfirm @positive-click="emptyAll">
          <template #trigger><n-button size="small" :disabled="tab === 'files' ? !files.length : !dbs.length">{{ t('files.empty_trash') }}</n-button></template>
          {{ t('files.empty_trash_confirm') }}
        </n-popconfirm>
        <n-date-picker v-model:value="range" type="daterange" size="small" clearable class="dates" />
        <n-input v-model:value="q" size="small" clearable :placeholder="t('rb.search')" class="q" />
      </div>
      <div class="body">
        <nav v-if="tab === 'files'" class="kinds">
          <button v-for="k in kinds" :key="k.k" type="button" :class="{ on: kind === k.k }" @click="kind = k.k">{{ t(`rb.kinds.${k.k}`) }}({{ k.n }})</button>
        </nav>
        <div class="table">
          <p v-if="tab === 'files'" class="muted">{{ t('rb.total', { n: shownFiles.length, size: bytes(totalSize) }) }}</p>
          <p v-else class="muted">{{ t('rb.db_hint') }}</p>
          <n-data-table
            v-if="tab === 'files'" v-model:checked-row-keys="checked" size="small" :columns="fileCols" :data="shownFiles" :loading="loading"
            :row-key="(i: TrashItem) => i.id" :pagination="{ pageSize: 10, showSizePicker: true, pageSizes: [10, 20, 50, 100] }" :scroll-x="860"
          />
          <n-data-table
            v-else v-model:checked-row-keys="checked" size="small" :columns="dbCols" :data="shownDBs" :loading="loading"
            :row-key="(d: TrashedDB) => d.id" :pagination="{ pageSize: 10 }" :scroll-x="820"
          />
          <n-space align="center" style="margin-top: 8px">
            <n-select
              v-model:value="batch" size="small" style="width: 180px" :placeholder="t('rb.batch')"
              :options="[{ label: t('files.restore'), value: 'restore' }, { label: t('common.delete'), value: 'delete' }]"
            />
            <n-button size="small" type="primary" :disabled="!batch || !checked.length" @click="runBatch">{{ t('rb.batch_run') }}</n-button>
          </n-space>
        </div>
      </div>
    </n-card>
  </n-modal>
  <TaskModal :task-id="task?.id ?? null" :title="task?.title ?? ''" @close="task = null; load()" @done="load" />
</template>

<style scoped>
.head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  margin: 8px 0 12px;
}
.sw {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  white-space: nowrap;
}
.warn {
  width: auto;
  padding: 0;
}
.warn :deep(.n-alert-body) {
  padding: 4px 10px;
}
.grow {
  flex: 1;
}
.dates {
  width: min(260px, 100%);
}
.q {
  width: min(200px, 100%);
}
.body {
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr);
  gap: 12px;
}
.body:has(> .table:only-child) {
  grid-template-columns: minmax(0, 1fr);
}
@media (max-width: 700px) {
  .body {
    grid-template-columns: minmax(0, 1fr);
  }
  .kinds {
    flex-direction: row !important;
    overflow-x: auto;
  }
}
.kinds {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.kinds button {
  border: 0;
  background: transparent;
  color: inherit;
  font: inherit;
  text-align: left;
  padding: 8px 14px;
  border-radius: 6px;
  cursor: pointer;
  white-space: nowrap;
}
.kinds button.on {
  background: rgba(32, 165, 58, 0.12);
  color: #20a53a;
}
.muted {
  font-size: 12px;
  opacity: 0.65;
  margin: 0 0 6px;
}
:deep(.op) {
  color: #20a53a;
  cursor: pointer;
}
:deep(.op.danger) {
  color: #d03050;
}
</style>
