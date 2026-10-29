<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NTabs, NTabPane, NButton, NDataTable, NTag, NSpace, NAlert, NInput, type DataTableColumns } from 'naive-ui'
import { get } from '../api/client'
import type { Site, WebConfig, WebStatus } from '../api/types'
import SiteCreate from '../components/SiteCreate.vue'
import SiteDrawer from '../components/SiteDrawer.vue'
import TaskModal from '../components/TaskModal.vue'
import WebEnvPanel from '../components/WebEnvPanel.vue'

const { t } = useI18n()
const sites = ref<Site[]>([])
const loading = ref(false)
const status = ref<WebStatus | null>(null)
const cfg = ref<WebConfig | null>(null)
const showCreate = ref(false)
const selected = ref<Site | null>(null)
const task = ref<{ id: string; title: string } | null>(null)
const tab = ref('sites')
const search = ref('')

async function load() {
  loading.value = true
  try {
    const [s, st, c] = await Promise.all([
      get<{ items: Site[] }>('/sites'),
      get<WebStatus>('/web/status'),
      get<WebConfig>('/web/config'),
    ])
    // Domain entries without website files live on the domain page.
    sites.value = s.items.filter((x) => x.type !== 'proxy')
    status.value = st
    cfg.value = c
    if (selected.value) selected.value = sites.value.find((x) => x.id === selected.value!.id) ?? null
  } finally {
    loading.value = false
  }
}
onMounted(load)

function openTask(id: string, title: string) {
  task.value = { id, title }
}

const filtered = computed(() => {
  const q = search.value.trim().toLowerCase()
  return q ? sites.value.filter((s) => [s.domain, ...s.aliases, s.remark].some((x) => x.toLowerCase().includes(q))) : sites.value
})

function sslTag(s: Site) {
  if (s.ssl_mode === 'none') return h(NTag, { size: 'small' }, () => t('site.ssl_none'))
  const days = Math.floor((s.cert_not_after * 1000 - Date.now()) / 86400000)
  const type = days < 0 ? 'error' : days < 15 ? 'warning' : 'success'
  return h(NTag, { size: 'small', type }, () => t('site.days_left', { n: days }))
}

function typeLabel(s: Site) {
  switch (s.type) {
    case 'php':
      return `PHP ${s.php_version}`
    case 'static':
      return t('site.static')
    case 'node':
      return `Node.js ${s.runtime_version}`
    case 'python':
      return `Python ${s.runtime_version}`
    case 'java':
      return `Java ${s.runtime_version}`
  }
  return h('span', { title: s.proxy_target }, s.passthrough ? t('site.passthrough_short') : t('site.proxy'))
}

const columns: DataTableColumns<Site> = [
  {
    title: () => t('site.domain'),
    key: 'domain',
    render: (s) =>
      h('div', [
        h('a', { href: `${s.ssl_mode !== 'none' ? 'https' : 'http'}://${s.domain}`, target: '_blank', rel: 'noopener' }, s.domain),
        s.aliases.length ? h('div', { style: 'font-size:12px;opacity:.6' }, s.aliases.join(', ')) : null,
      ]),
  },
  {
    title: () => t('common.status'),
    key: 'stopped',
    width: 100,
    render: (s) => h(NTag, { size: 'small', type: s.stopped ? 'default' : 'success' }, () => (s.stopped ? t('site.stopped') : t('site.running'))),
  },
  { title: () => t('site.type'), key: 'type', width: 130, render: typeLabel },
  { title: 'SSL', key: 'ssl', width: 120, render: sslTag },
  { title: () => t('site.remark'), key: 'remark', ellipsis: { tooltip: true } },
  {
    title: '',
    key: 'actions',
    width: 90,
    render: (s) => h(NButton, { size: 'small', onClick: () => (selected.value = s) }, () => t('site.manage')),
  },
]
</script>

<template>
  <n-tabs v-model:value="tab" type="line" animated>
    <n-tab-pane name="sites" :tab="t('site.list')">
      <n-alert v-if="status && !status.status.docker" type="error" :show-icon="false" style="margin-bottom: 12px">
        {{ t('web.no_docker') }}
      </n-alert>
      <n-alert v-else-if="status && !status.status.installed" type="info" :show-icon="false" style="margin-bottom: 12px">
        {{ t('web.not_installed') }}
      </n-alert>
      <n-space justify="space-between" style="margin-bottom: 12px">
        <n-button type="primary" :disabled="!status?.status.docker" @click="showCreate = true">{{ t('site.create') }}</n-button>
        <n-input v-model:value="search" size="small" clearable :placeholder="t('site.search')" style="width: 220px" />
      </n-space>
      <n-data-table size="small" :columns="columns" :data="filtered" :loading="loading" :row-key="(r: Site) => r.id" :scroll-x="720" />
    </n-tab-pane>
    <n-tab-pane name="env" :tab="t('web.tab')">
      <WebEnvPanel @changed="load" />
    </n-tab-pane>
  </n-tabs>

  <SiteCreate
    :show="showCreate"
    :php-versions="status?.php_versions ?? []"
    :has-c-f="!!cfg?.has_cf_token"
    @close="showCreate = false"
    @created="(id: string, d: string) => { showCreate = false; openTask(id, t('site.create') + ' · ' + d) }"
  />
  <SiteDrawer
    :site="selected"
    :php-versions="status?.php_versions ?? []"
    :has-c-f="!!cfg?.has_cf_token"
    @close="selected = null"
    @changed="load"
    @task="openTask"
  />
  <TaskModal :task-id="task?.id ?? null" :title="task?.title ?? ''" @close="task = null; load()" @done="load" />
</template>
