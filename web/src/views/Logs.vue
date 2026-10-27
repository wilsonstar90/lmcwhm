<script setup lang="ts">
import { h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NTabs, NTabPane, NDataTable, NTag, NButton, type DataTableColumns } from 'naive-ui'
import { get } from '../api/client'
import { datetime } from '../format'

interface LoginRow { id: number; username: string; ip: string; user_agent: string; success: boolean; reason: string; created_at: number }
interface OpRow { id: number; username: string; ip: string; category: string; action: string; detail: string; created_at: number }

const { t, locale } = useI18n()
const PAGE = 20

function usePaged<T>(path: string) {
  const rows = ref<T[]>([])
  const loading = ref(false)
  const pagination = ref({ page: 1, pageSize: PAGE, itemCount: 0 })
  async function load(page = pagination.value.page) {
    loading.value = true
    try {
      const r = await get<{ items: T[]; total: number }>(`${path}?limit=${PAGE}&offset=${(page - 1) * PAGE}`)
      rows.value = r.items
      pagination.value = { ...pagination.value, page, itemCount: r.total }
    } finally {
      loading.value = false
    }
  }
  return { rows, loading, pagination, load }
}

const logins = usePaged<LoginRow>('/logs/login')
const ops = usePaged<OpRow>('/logs/operation')
onMounted(() => {
  logins.load()
  ops.load()
})

const time = (r: { created_at: number }) => datetime(r.created_at, locale.value)

const loginCols: DataTableColumns<LoginRow> = [
  { title: () => t('common.time'), key: 'created_at', width: 180, render: time },
  { title: () => t('common.user'), key: 'username', width: 140 },
  { title: () => t('common.ip'), key: 'ip', width: 200 },
  {
    title: () => t('common.status'), key: 'success', width: 100,
    render: (r) => h(NTag, { type: r.success ? 'success' : 'error', size: 'small' }, () => (r.success ? t('common.success') : t('common.failed'))),
  },
  { title: () => t('logs.reason'), key: 'reason', width: 140 },
  { title: 'User-Agent', key: 'user_agent', ellipsis: { tooltip: true } },
]

const opCols: DataTableColumns<OpRow> = [
  { title: () => t('common.time'), key: 'created_at', width: 180, render: time },
  { title: () => t('common.user'), key: 'username', width: 120 },
  { title: () => t('common.ip'), key: 'ip', width: 180 },
  { title: () => t('logs.category'), key: 'category', width: 100 },
  { title: () => t('logs.action'), key: 'action', width: 160 },
  { title: () => t('common.detail'), key: 'detail', ellipsis: { tooltip: true } },
]
</script>

<template>
  <n-tabs type="line" animated>
    <template #suffix>
      <n-button size="small" @click="() => { logins.load(); ops.load() }">{{ t('common.refresh') }}</n-button>
    </template>
    <n-tab-pane name="login" :tab="t('logs.tab_login')">
      <n-data-table
        remote
        size="small"
        :columns="loginCols"
        :data="logins.rows.value"
        :loading="logins.loading.value"
        :pagination="logins.pagination.value"
        :scroll-x="900"
        @update:page="logins.load"
      />
    </n-tab-pane>
    <n-tab-pane name="operation" :tab="t('logs.tab_operation')">
      <n-data-table
        remote
        size="small"
        :columns="opCols"
        :data="ops.rows.value"
        :loading="ops.loading.value"
        :pagination="ops.pagination.value"
        :scroll-x="900"
        @update:page="ops.load"
      />
    </n-tab-pane>
  </n-tabs>
</template>
