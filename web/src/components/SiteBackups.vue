<script setup lang="ts">
// The backups of one website (網站備份數據): list, back up now, download, delete.
import { computed, h, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NCard, NDataTable, NModal, NPopconfirm, NSpace, useMessage, type DataTableColumns } from 'naive-ui'
import { get, post, ApiError } from '../api/client'
import { bytes, datetime } from '../format'

const props = defineProps<{ domain: string | null }>()
const emit = defineEmits<{ close: []; task: [id: string, title: string] }>()
const { t, locale } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))

interface Rec { id: number; name: string; size: number; dest: string; dest_name: string; created_at: number; job_id: number }
const items = ref<Rec[]>([])
const loading = ref(false)
async function load() {
  if (!props.domain) return
  loading.value = true
  try {
    const r = await get<{ items: (Rec & { target: string })[] }>('/backups?kind=site')
    items.value = r.items.filter((x) => x.target === props.domain)
  } catch (e) {
    fail(e)
  } finally {
    loading.value = false
  }
}
watch(() => props.domain, load, { immediate: true })
defineExpose({ load })

async function backupNow() {
  try {
    const r = await post<{ task_id: string }>('/backups/run', { kind: 'site', target: props.domain, dests: ['local'], keep: 0 })
    emit('task', r.task_id, `${t('sb.backup_now')} · ${props.domain}`)
  } catch (e) {
    fail(e)
  }
}
async function del(id: number) {
  try {
    await post(`/backups/${id}/delete`)
    load()
  } catch (e) {
    fail(e)
  }
}
const cols = computed<DataTableColumns<Rec>>(() => [
  { title: () => t('sb.file'), key: 'name', minWidth: 220, ellipsis: { tooltip: true } },
  { title: () => t('sb.dest'), key: 'dest', width: 130, render: (r) => r.dest_name || r.dest },
  { title: () => t('files.size'), key: 'size', width: 100, render: (r) => bytes(r.size) },
  { title: () => t('sb.time'), key: 'created_at', width: 170, render: (r) => datetime(r.created_at, locale.value) },
  { title: () => t('sb.remark'), key: 'job_id', width: 100, render: (r) => (r.job_id ? t('sb.auto') : t('sb.manual')) },
  {
    title: () => t('common.actions'),
    key: 'ops',
    width: 120,
    align: 'right',
    render: (r) =>
      h(NSpace, { size: 10, justify: 'end' }, () => [
        h('a', { class: 'op', href: `/api/v1/backups/${r.id}/download` }, t('files.download')),
        h(NPopconfirm, { onPositiveClick: () => del(r.id) }, { trigger: () => h('a', { class: 'op danger' }, t('common.delete')), default: () => t('common.confirm_delete') }),
      ]),
  },
])
</script>

<template>
  <n-modal :show="!!domain" @update:show="(v: boolean) => !v && emit('close')">
    <n-card v-if="domain" :title="t('sb.title', { domain })" style="width: min(900px, calc(100vw - 24px))" closable @close="emit('close')">
      <n-space justify="space-between" style="margin-bottom: 8px">
        <n-button type="primary" @click="backupNow">{{ t('sb.backup_now') }}</n-button>
        <n-button @click="load">⟳</n-button>
      </n-space>
      <n-data-table size="small" :columns="cols" :data="items" :loading="loading" :pagination="{ pageSize: 10 }" :scroll-x="760" />
      <p class="hint">{{ t('sb.hint') }}</p>
    </n-card>
  </n-modal>
</template>

<style scoped>
.hint {
  font-size: 12px;
  opacity: 0.65;
  margin: 8px 0 0;
}
:deep(.op) {
  color: #20a53a;
  cursor: pointer;
}
:deep(.op.danger) {
  color: #d03050;
}
</style>
