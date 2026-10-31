<script setup lang="ts">
// 數據庫【name】備份 (like aaPanel): back up now to a chosen place, and
// download, restore or delete the database's backups.
import { computed, h, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import {
  NButton, NButtonGroup, NCard, NCheckbox, NDataTable, NDropdown, NModal, NPopconfirm, NSpace, NTag, useDialog, useMessage, type DataTableColumns,
} from 'naive-ui'
import { ApiError, get, post } from '../api/client'
import type { Database } from '../api/types'
import { bytes, datetime } from '../format'

interface Rec { id: number; name: string; size: number; dest: string; dest_name: string; job_id: number; created_at: number }
interface Dest { value: string; name: string; kind: string }

const props = defineProps<{ db: Database | null }>()
const emit = defineEmits<{ close: []; task: [id: string, title: string] }>()
const { t, locale } = useI18n()
const msg = useMessage()
const dialog = useDialog()
const router = useRouter()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))
const items = ref<Rec[]>([])
const dests = ref<Dest[]>([])
const checked = ref<number[]>([])
const loading = ref(false)

async function load() {
  if (!props.db) return
  loading.value = true
  try {
    items.value = (await get<{ items: Rec[] }>(`/databases/${props.db.id}/backups`)).items
    checked.value = []
  } catch (e) {
    fail(e)
  } finally {
    loading.value = false
  }
}
watch(
  () => props.db,
  async (d) => {
    if (!d) return
    load()
    try {
      dests.value = (await get<{ items: Dest[] }>('/databases/backup-dests')).items
    } catch {
      dests.value = []
    }
  },
)
defineExpose({ load })

const destLabel = (d: Dest) => (d.value === 'local' ? t('dbb.local') : `${d.name} (${d.kind})`)
const destOptions = computed(() => dests.value.map((d) => ({ key: d.value, label: destLabel(d) })))
async function run(dest = 'local') {
  try {
    const r = await post<{ task_id: string }>(`/databases/${props.db!.id}/backups/run`, { dests: [dest] })
    emit('task', r.task_id, `${t('dbb.backup')} · ${props.db!.name}`)
  } catch (e) {
    fail(e)
  }
}
function restore(r: Rec) {
  const first = ref(true)
  dialog.warning({
    title: t('dbb.restore'),
    content: () =>
      h('div', [
        h('p', t('dbb.restore_confirm', { file: r.name, db: props.db!.name })),
        h(NCheckbox, { checked: first.value, 'onUpdate:checked': (v: boolean) => (first.value = v) }, () => t('dbb.backup_first')),
      ]),
    positiveText: t('dbb.restore'),
    negativeText: t('common.cancel'),
    onPositiveClick: async () => {
      try {
        const res = await post<{ task_id: string }>(`/databases/${props.db!.id}/backups/${r.id}/restore`, { backup_first: first.value })
        emit('task', res.task_id, `${t('dbb.restore')} · ${props.db!.name}`)
      } catch (e) {
        fail(e)
      }
    },
  })
}
async function remove(ids: number[]) {
  try {
    await post(`/databases/${props.db!.id}/backups/delete`, { ids })
    msg.success(t('common.deleted'))
    load()
  } catch (e) {
    fail(e)
  }
}
const download = (r: Rec) => window.open(`/api/v1/databases/${props.db!.id}/backups/${r.id}/download`, '_blank')

const columns = computed<DataTableColumns<Rec>>(() => [
  { type: 'selection' },
  { title: t('dbb.file'), key: 'name', minWidth: 220, ellipsis: { tooltip: true } },
  { title: t('dbb.dest'), key: 'dest', width: 120, render: (r) => (r.dest === 'local' ? t('dbb.local') : r.dest_name || r.dest) },
  { title: t('dbb.size'), key: 'size', width: 90, render: (r) => bytes(r.size) },
  { title: t('dbb.time'), key: 'created_at', width: 160, render: (r) => datetime(r.created_at, locale.value) },
  { title: t('dbb.note'), key: 'job_id', width: 110, render: (r) => h(NTag, { size: 'small', bordered: false, type: r.job_id ? 'info' : 'default' }, () => (r.job_id ? t('dbb.by_cron') : t('dbb.by_hand'))) },
  {
    title: t('dbb.ops'),
    key: 'ops',
    width: 150,
    render: (r) =>
      h(NSpace, { size: 10, wrap: false }, () => [
        h('a', { class: 'link', onClick: () => download(r) }, t('dbb.download')),
        h('a', { class: 'link', onClick: () => restore(r) }, t('dbb.restore')),
        h(NPopconfirm, { onPositiveClick: () => remove([r.id]) }, { trigger: () => h('a', { class: 'link danger' }, t('common.delete')), default: () => t('dbb.delete_confirm', { file: r.name }) }),
      ]),
  },
])
</script>

<template>
  <n-modal :show="!!db" @update:show="(v: boolean) => !v && emit('close')">
    <n-card v-if="db" :title="t('dbb.title', { name: db.name })" style="width: min(880px, calc(100vw - 24px))" closable @close="emit('close')">
      <n-space justify="space-between" align="center" style="margin-bottom: 12px">
        <n-button-group>
          <n-button type="primary" @click="run()">{{ t('dbb.backup') }}</n-button>
          <n-dropdown v-if="destOptions.length > 1" :options="destOptions" trigger="click" @select="run">
            <n-button type="primary" :aria-label="t('dbb.backup_to')">▾</n-button>
          </n-dropdown>
        </n-button-group>
        <n-space align="center">
          <n-popconfirm v-if="checked.length" @positive-click="remove(checked)">
            <template #trigger><n-button size="small" type="error" ghost>{{ t('dbb.delete_n', { n: checked.length }) }}</n-button></template>
            {{ t('dbb.delete_n_confirm', { n: checked.length }) }}
          </n-popconfirm>
          <n-button size="small" @click="load">{{ t('common.refresh') }}</n-button>
        </n-space>
      </n-space>
      <n-data-table
        v-model:checked-row-keys="checked"
        :columns="columns"
        :data="items"
        :loading="loading"
        :row-key="(r: Rec) => r.id"
        size="small"
        :scroll-x="820"
        :max-height="420"
        :pagination="items.length > 20 ? { pageSize: 20 } : false"
      />
      <p class="note">• {{ t('dbb.cron_hint') }} <a class="link" @click="router.push({ name: 'cron' }); emit('close')">{{ t('dbb.cron_link') }}</a></p>
    </n-card>
  </n-modal>
</template>

<style scoped>
.note {
  font-size: 12px;
  opacity: 0.75;
  margin: 12px 0 0;
}
:deep(.link) {
  color: #20a53a;
  cursor: pointer;
}
:deep(.link.danger) {
  color: #d03050;
}
</style>
