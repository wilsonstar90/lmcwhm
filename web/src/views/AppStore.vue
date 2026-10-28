<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NTabs, NTabPane, NButton, NDataTable, NTag, NSpace, NSelect, NCard, NGrid, NGi, NModal, NInput, NCheckbox, NPopconfirm,
  NDescriptions, NDescriptionsItem, useMessage, type DataTableColumns,
} from 'naive-ui'
import { get, post, put, ApiError } from '../api/client'
import type { CatalogApp, InstalledApp } from '../api/types'
import TaskModal from '../components/TaskModal.vue'
import LogsModal from '../components/LogsModal.vue'
import PhpVersions from '../components/PhpVersions.vue'

const { t } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))

const catalog = ref<CatalogApp[]>([])
const installed = ref<InstalledApp[]>([])
const loading = ref(false)
const tab = ref('installed')
const pick = ref<Record<string, string>>({})
const task = ref<{ id: string; title: string } | null>(null)
const logs = ref<{ url: string; title: string } | null>(null)
const cfg = ref<{ app: InstalledApp; body: string } | null>(null)
const cred = ref<{ app: InstalledApp; host: string; port: number; username: string; password: string } | null>(null)
const removeData = ref(false)
const hasPMA = computed(() => installed.value.some((i) => i.app === 'phpmyadmin'))

async function load() {
  loading.value = true
  try {
    const [c, i] = await Promise.all([get<{ items: CatalogApp[] }>('/apps/catalog'), get<{ items: InstalledApp[] }>('/apps')])
    catalog.value = c.items
    installed.value = i.items
    for (const a of c.items) if (!pick.value[a.key]) pick.value[a.key] = a.versions[0].version
  } catch (e) {
    fail(e)
  } finally {
    loading.value = false
  }
}
onMounted(load)

const isInstalled = (key: string, v: string) => installed.value.some((i) => i.app === key && i.version === v)

async function install(a: CatalogApp) {
  const v = pick.value[a.key]
  try {
    const r = await post<{ task_id: string }>('/apps/install', { app: a.key, version: v })
    task.value = { id: r.task_id, title: `${t('app.install')} · ${a.name} ${v}` }
  } catch (e) {
    fail(e)
  }
}

async function uninstall(i: InstalledApp) {
  try {
    const r = await post<{ task_id: string }>(`/apps/${i.id}/uninstall`, { remove_data: removeData.value })
    removeData.value = false
    task.value = { id: r.task_id, title: `${t('app.uninstall')} · ${i.name} ${i.version}` }
  } catch (e) {
    fail(e)
  }
}

async function control(i: InstalledApp, action: string) {
  try {
    await post(`/apps/${i.id}/${action}`)
    msg.success(t('common.done'))
    load()
  } catch (e) {
    fail(e)
  }
}

async function openConfig(i: InstalledApp) {
  try {
    cfg.value = { app: i, body: (await get<{ config: string }>(`/apps/${i.id}/config`)).config }
  } catch (e) {
    fail(e)
  }
}
async function saveConfig() {
  const c = cfg.value!
  try {
    const r = await put<{ task_id: string }>(`/apps/${c.app.id}/config`, { config: c.body })
    cfg.value = null
    task.value = { id: r.task_id, title: `${t('app.config')} · ${c.app.name} ${c.app.version}` }
  } catch (e) {
    fail(e)
  }
}

async function openCred(i: InstalledApp) {
  try {
    cred.value = { app: i, ...(await get<{ host: string; port: number; username: string; password: string }>(`/apps/${i.id}/credentials`)) }
  } catch (e) {
    fail(e)
  }
}

function copy(text: string) {
  navigator.clipboard?.writeText(text).then(() => msg.success(t('common.copied')))
}

const stateTag = (s: string) =>
  h(NTag, { size: 'small', type: s === 'running' ? 'success' : s ? 'warning' : 'default' }, () => (s ? t(`app.state.${s}`, s) : t('app.state.missing')))

const columns: DataTableColumns<InstalledApp> = [
  { title: () => t('app.name'), key: 'name', render: (i) => h('span', [h('b', i.name), ` ${i.version}`]) },
  { title: () => t('app.kind'), key: 'kind', width: 100, render: (i) => t(`app.kinds.${i.kind}`) },
  { title: () => t('common.status'), key: 'state', width: 110, render: (i) => (i.kind === 'runtime' ? h(NTag, { size: 'small', type: 'success' }, () => t('app.ready')) : stateTag(i.state)) },
  { title: () => t('app.port'), key: 'port', width: 120, render: (i) => (i.port ? `127.0.0.1:${i.port}` : '—') },
  { title: () => t('app.image'), key: 'image', ellipsis: { tooltip: true } },
  {
    title: '',
    key: 'actions',
    width: 340,
    render: (i) =>
      h(NSpace, { size: 6 }, () => [
        ...(i.container
          ? [
              i.state === 'running'
                ? h(NButton, { size: 'tiny', onClick: () => control(i, 'restart') }, () => t('app.restart'))
                : h(NButton, { size: 'tiny', type: 'primary', onClick: () => control(i, 'start') }, () => t('app.start')),
              i.state === 'running' ? h(NButton, { size: 'tiny', onClick: () => control(i, 'stop') }, () => t('app.stop')) : null,
              h(NButton, { size: 'tiny', onClick: () => (logs.value = { url: `/apps/${i.id}/logs`, title: `${i.name} ${i.version}` }) }, () => t('app.logs')),
            ]
          : []),
        i.has_config ? h(NButton, { size: 'tiny', onClick: () => openConfig(i) }, () => t('app.config')) : null,
        i.kind === 'database' ? h(NButton, { size: 'tiny', onClick: () => openCred(i) }, () => t('app.credentials')) : null,
        i.app === 'adminer' ? h(NButton, { size: 'tiny', tag: 'a', href: '/adminer/', target: '_blank' }, () => t('app.open')) : null,
        i.app === 'phpmyadmin' ? h(NButton, { size: 'tiny', tag: 'a', href: '/phpmyadmin/', target: '_blank' }, () => t('app.open')) : null,
        (i.app === 'mysql' || i.app === 'mariadb') && hasPMA.value
          ? h(NButton, { size: 'tiny', tag: 'a', href: '/phpmyadmin/', target: '_blank' }, () => 'phpMyAdmin')
          : null,
        h(
          NPopconfirm,
          { onPositiveClick: () => uninstall(i), onAfterLeave: () => (removeData.value = false) },
          {
            trigger: () => h(NButton, { size: 'tiny', type: 'error', ghost: true }, () => t('app.uninstall')),
            default: () =>
              h('div', [
                h('div', t('app.uninstall_confirm', { name: `${i.name} ${i.version}` })),
                h(NCheckbox, { checked: removeData.value, 'onUpdate:checked': (v: boolean) => (removeData.value = v), style: 'margin-top:8px' }, () =>
                  t('app.remove_data'),
                ),
              ]),
          },
        ),
      ]),
  },
]

const kinds = ['runtime', 'database', 'tool'] as const
const byKind = computed(() => Object.fromEntries(kinds.map((k) => [k, catalog.value.filter((a) => a.kind === k)])))
const versionOptions = (a: CatalogApp) =>
  a.versions.map((v) => ({
    label: `${v.version}${v.eol ? ' (EOL)' : ''}${isInstalled(a.key, v.version) ? ' ✓' : ''}`,
    value: v.version,
  }))
</script>

<template>
  <n-tabs v-model:value="tab" type="line" animated>
    <n-tab-pane name="installed" :tab="t('app.installed')">
      <n-space justify="end" style="margin-bottom: 12px">
        <n-button size="small" @click="load">{{ t('common.refresh') }}</n-button>
      </n-space>
      <n-data-table size="small" :columns="columns" :data="installed" :loading="loading" :row-key="(r: InstalledApp) => r.id" :scroll-x="980" />
    </n-tab-pane>
    <n-tab-pane name="php" tab="PHP" display-directive="show:lazy"><PhpVersions /></n-tab-pane>
    <n-tab-pane v-for="k in kinds" :key="k" :name="k" :tab="t(`app.kinds.${k}`)">
      <n-grid cols="1 s:2 m:3" responsive="screen" :x-gap="12" :y-gap="12">
        <n-gi v-for="a in byKind[k]" :key="a.key">
          <n-card size="small" :title="a.name">
            <p class="desc">{{ t(`app.desc.${a.key}`) }}</p>
            <p v-if="a.min_mem_mb || a.amd64_only" class="desc warn">
              {{ [a.min_mem_mb ? t('app.min_mem', { n: a.min_mem_mb }) : '', a.amd64_only ? t('app.amd64_only') : ''].filter(Boolean).join(' · ') }}
            </p>
            <n-space align="center">
              <n-select v-model:value="pick[a.key]" :options="versionOptions(a)" size="small" style="width: 150px" />
              <n-button size="small" type="primary" :disabled="isInstalled(a.key, pick[a.key])" @click="install(a)">
                {{ isInstalled(a.key, pick[a.key]) ? t('app.installed_tag') : t('app.install') }}
              </n-button>
            </n-space>
          </n-card>
        </n-gi>
      </n-grid>
    </n-tab-pane>
  </n-tabs>

  <n-modal :show="!!cfg" @update:show="(v: boolean) => !v && (cfg = null)">
    <n-card v-if="cfg" :title="`${t('app.config')} · ${cfg.app.name} ${cfg.app.version}`" style="width: min(800px, calc(100vw - 32px))" closable @close="cfg = null">
      <p class="desc">{{ t('app.config_hint') }}</p>
      <n-input v-model:value="cfg.body" type="textarea" :rows="18" class="mono" />
      <template #footer>
        <n-space justify="end">
          <n-button @click="cfg = null">{{ t('common.cancel') }}</n-button>
          <n-button type="primary" @click="saveConfig">{{ t('app.save_restart') }}</n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>

  <n-modal :show="!!cred" @update:show="(v: boolean) => !v && (cred = null)">
    <n-card v-if="cred" :title="`${cred.app.name} ${cred.app.version}`" style="width: min(520px, calc(100vw - 32px))" closable @close="cred = null">
      <n-descriptions :column="1" label-placement="left" size="small">
        <n-descriptions-item :label="t('db.host')"><code>{{ cred.host }}:{{ cred.port }}</code></n-descriptions-item>
        <n-descriptions-item v-if="cred.username" :label="t('db.username')"><code>{{ cred.username }}</code></n-descriptions-item>
        <n-descriptions-item :label="t('db.password')">
          <code>{{ cred.password }}</code>
          <n-button text type="primary" style="margin-left: 8px" @click="copy(cred.password)">{{ t('common.copy') }}</n-button>
        </n-descriptions-item>
      </n-descriptions>
      <p class="desc" style="margin-top: 12px">{{ t('app.loopback_hint') }}</p>
    </n-card>
  </n-modal>

  <TaskModal :task-id="task?.id ?? null" :title="task?.title ?? ''" @close="task = null; load()" @done="load" />
  <LogsModal :url="logs?.url ?? null" :title="logs?.title ?? ''" @close="logs = null" />
</template>

<style scoped>
.desc {
  margin: 0 0 10px;
  font-size: 12px;
  opacity: 0.75;
  min-height: 32px;
}
.desc.warn {
  min-height: 0;
  color: #d03050;
  opacity: 1;
}
.mono :deep(textarea) {
  font-family: Menlo, Consolas, monospace;
  font-size: 12px;
}
</style>
