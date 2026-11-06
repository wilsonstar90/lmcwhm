<script setup lang="ts">
// Database tools (MySQL / MariaDB, like aaPanel's 工具箱): tables with their
// engine, collation, rows and size; repair, optimize, engine conversion,
// comments and the table structure, one at a time or in bulk.
import { computed, h, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NCard, NDataTable, NModal, NSelect, NSpace, useMessage, type DataTableColumns } from 'naive-ui'
import { get, post, put, ApiError } from '../api/client'
import type { Database } from '../api/types'
import { bytes } from '../format'
import RemarkCell from './RemarkCell.vue'

interface Table { name: string; engine: string; collation: string; rows: number; size: number; free: number; comment: string }

const props = defineProps<{ db: Database | null }>()
const emit = defineEmits<{ close: []; task: [id: string, title: string] }>()
const { t } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))

const tables = ref<Table[]>([])
const size = ref(0)
const loading = ref(false)
const checked = ref<string[]>([])
const bulk = ref<string | null>(null)

async function load() {
  if (!props.db) return
  loading.value = true
  try {
    const r = await get<{ items: Table[]; size: number }>(`/databases/${props.db.id}/tables`)
    tables.value = r.items
    size.value = r.size
    checked.value = checked.value.filter((n) => r.items.some((x) => x.name === n))
  } catch (e) {
    fail(e)
  } finally {
    loading.value = false
  }
}
watch(() => props.db?.id, (id) => {
  tables.value = []
  checked.value = []
  bulk.value = null
  if (id) load()
}, { immediate: true })
defineExpose({ load })

const ACTION_LABEL: Record<string, string> = { repair: 'dbt.repair', optimize: 'dbt.optimize', innodb: 'dbt.to_innodb', myisam: 'dbt.to_myisam' }
async function run(action: string, names: string[]) {
  const d = props.db
  if (!d || !names.length) return
  try {
    const r = await post<{ task_id: string }>(`/databases/${d.id}/tables/action`, { action, tables: names })
    emit('task', r.task_id, `${t(ACTION_LABEL[action])} · ${d.name}${names.length > 1 ? ` (${names.length})` : ` · ${names[0]}`}`)
  } catch (e) {
    fail(e)
  }
}

async function exportSchema(tb: Table) {
  try {
    const r = await get<{ schema: string }>(`/databases/${props.db!.id}/tables/schema?name=${encodeURIComponent(tb.name)}`)
    const a = document.createElement('a')
    a.href = URL.createObjectURL(new Blob([r.schema], { type: 'text/plain' }))
    a.download = `${props.db!.name}.${tb.name}.sql`
    a.click()
    setTimeout(() => URL.revokeObjectURL(a.href), 1000)
  } catch (e) {
    fail(e)
  }
}

const bulkOptions = computed(() => ['repair', 'optimize', 'innodb', 'myisam'].map((k) => ({ label: t(ACTION_LABEL[k]), value: k })))
const otherEngine = (e: string) => (e.toLowerCase() === 'innodb' ? 'myisam' : 'innodb')

const columns = computed<DataTableColumns<Table>>(() => [
  { type: 'selection' },
  { title: () => t('dbt.table'), key: 'name', width: 180, ellipsis: { tooltip: true } },
  {
    title: () => t('site.remark'),
    key: 'comment',
    width: 130,
    render: (tb) =>
      h(RemarkCell, {
        value: tb.comment, maxlength: 1024,
        save: (v: string) => put(`/databases/${props.db!.id}/tables/comment`, { name: tb.name, comment: v }).then(() => (tb.comment = v)),
      }),
  },
  { title: () => t('dbt.engine'), key: 'engine', width: 90 },
  { title: () => t('dbt.collation'), key: 'collation', width: 150, ellipsis: { tooltip: true } },
  { title: () => t('dbt.rows'), key: 'rows', width: 80, sorter: (a, b) => a.rows - b.rows },
  { title: () => t('files.size'), key: 'size', width: 100, sorter: (a, b) => a.size - b.size, render: (tb) => bytes(tb.size) },
  {
    title: () => t('common.actions'),
    key: 'ops',
    width: 270,
    align: 'right',
    render: (tb) =>
      h('span', { class: 'ops' }, [
        h('a', { onClick: () => exportSchema(tb) }, t('dbt.export_schema')),
        h('a', { onClick: () => run('repair', [tb.name]) }, t('dbt.repair')),
        h('a', { onClick: () => run('optimize', [tb.name]) }, t('dbt.optimize')),
        h('a', { onClick: () => run(otherEngine(tb.engine), [tb.name]) }, t(ACTION_LABEL[otherEngine(tb.engine)])),
      ]),
  },
])
</script>

<template>
  <n-modal :show="!!db" @update:show="(v: boolean) => !v && emit('close')">
    <n-card v-if="db" :title="t('dbt.title', { name: db.name })" style="width: min(1120px, calc(100vw - 24px))" closable @close="emit('close')">
      <div class="head">
        <span>{{ t('dbt.summary', { name: db.name, size: bytes(size) }) }}</span>
        <n-button size="small" :loading="loading" @click="load">{{ t('common.refresh') }}</n-button>
      </div>
      <n-data-table
        v-model:checked-row-keys="checked" size="small" :columns="columns" :data="tables" :loading="loading"
        :row-key="(r: Table) => r.name" :scroll-x="1040" :max-height="'min(52dvh, 460px)'"
      />
      <n-space align="center" style="margin-top: 12px">
        <n-select v-model:value="bulk" :options="bulkOptions" :placeholder="t('dbt.bulk_pick')" size="small" style="width: 180px" />
        <n-button size="small" type="primary" :disabled="!bulk || !checked.length" @click="run(bulk!, checked)">
          {{ checked.length ? t('dbt.bulk_run_n', { n: checked.length }) : t('dbt.bulk_run') }}
        </n-button>
      </n-space>
      <ul class="notes">
        <li>{{ t('dbt.note_optimize') }}</li>
        <li>{{ t('dbt.note_repair') }}</li>
        <li>{{ t('dbt.note_engine') }}</li>
        <li>{{ t('dbt.note_size') }}</li>
      </ul>
    </n-card>
  </n-modal>
</template>

<style scoped>
.head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  padding: 10px 14px;
  margin-bottom: 12px;
  border-radius: 6px;
  background: rgba(128, 128, 128, 0.1);
  font-weight: 600;
}
:deep(.ops) {
  display: inline-flex;
  gap: 10px;
  white-space: nowrap;
}
:deep(.ops a) {
  color: #20a53a;
  cursor: pointer;
  font-size: 12px;
}
.notes {
  margin: 12px 0 0;
  padding-left: 18px;
  font-size: 12px;
  opacity: 0.7;
  line-height: 1.6;
}
</style>
