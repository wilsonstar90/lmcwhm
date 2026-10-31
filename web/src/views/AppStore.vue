<script setup lang="ts">
import { computed, h, nextTick, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NTabs, NTabPane, NButton, NDataTable, NTag, NSpace, NSelect, NCard, NGrid, NGi, NModal, NCheckbox, NPopconfirm,
  NDescriptions, NDescriptionsItem, useMessage, type DataTableColumns,
} from 'naive-ui'
import { get, post, ApiError } from '../api/client'
import type { CatalogApp, InstalledApp } from '../api/types'
import TaskModal from '../components/TaskModal.vue'
import LogsModal from '../components/LogsModal.vue'
import PhpVersions from '../components/PhpVersions.vue'
import AppSettings from '../components/AppSettings.vue'
import UpdateBadge from '../components/UpdateBadge.vue'
import BuildModeDialog, { type BuildChoice, type SourceInfo } from '../components/BuildModeDialog.vue'
import { useUpdates } from '../stores/updates'
import { datetime } from '../format'

const { t, locale } = useI18n()
const updates = useUpdates()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))

const catalog = ref<CatalogApp[]>([])
const installed = ref<InstalledApp[]>([])
const loading = ref(false)
const tab = ref('installed')
const pick = ref<Record<string, string>>({})
const task = ref<{ id: string; title: string } | null>(null)
const logs = ref<{ url: string; title: string } | null>(null)
const settings = ref<InstalledApp | null>(null)
const phpRef = ref<InstanceType<typeof PhpVersions> | null>(null)
const cred = ref<{ app: InstalledApp; host: string; port: number; username: string; password: string } | null>(null)
const removeData = ref(false)
const hasPMA = computed(() => installed.value.some((i) => i.app === 'phpmyadmin'))

interface PhpVer { version: string; state: string; port: number; sites: number; full_version?: string; build?: { build_mode: 'fast' | 'source'; build_args: string; build_cflags: string } }
const source = ref<Record<string, SourceInfo>>({})
const phps = ref<PhpVer[]>([])

async function load() {
  loading.value = true
  try {
    const [c, i, p] = await Promise.all([
      get<{ items: CatalogApp[]; source: Record<string, SourceInfo> }>('/apps/catalog'),
      get<{ items: InstalledApp[] }>('/apps'),
      get<{ items: PhpVer[] }>('/php').catch(() => ({ items: [] as PhpVer[] })),
      updates.load().catch(() => {}),
    ])
    catalog.value = c.items
    source.value = c.source ?? {}
    installed.value = i.items
    phps.value = p.items.filter((v) => v.state)
    for (const a of c.items) if (!pick.value[a.key]) pick.value[a.key] = a.versions[0].version
  } catch (e) {
    fail(e)
  } finally {
    loading.value = false
  }
}
onMounted(load)

const isInstalled = (key: string, v: string) => installed.value.some((i) => i.app === key && i.version === v)

// Install and switch go through the 極速 / 編譯 dialog.
const bm = ref<{ title: string; key: string; current?: BuildChoice; run: (c: BuildChoice) => Promise<{ task_id: string }> } | null>(null)
function install(a: CatalogApp) {
  const v = pick.value[a.key]
  bm.value = { title: `${t('app.install')} · ${a.name} ${v}`, key: `${a.key}:${v}`, run: (c) => post('/apps/install', { app: a.key, version: v, ...c }) }
}
function switchBuild(i: InstalledApp) {
  bm.value = {
    title: `${t('bm.title')} · ${i.name} ${i.version}`, key: `${i.app}:${i.version}`,
    current: { build_mode: i.build_mode ?? 'fast', build_args: i.build_args ?? '', build_cflags: i.build_cflags ?? '' },
    run: (c) => post(`/apps/${i.id}/build`, c),
  }
}
function switchPHP(v: PhpVer) {
  bm.value = {
    title: `${t('bm.title')} · PHP ${v.version}`, key: `php:${v.version}`,
    current: v.build ?? { build_mode: 'fast', build_args: '', build_cflags: '' },
    run: (c) => post(`/php/${v.version}/build`, c),
  }
}
async function runBuild(c: BuildChoice) {
  const b = bm.value!
  try {
    const r = await b.run(c)
    task.value = { id: r.task_id, title: b.title }
    bm.value = null
  } catch (e) {
    fail(e)
  }
}
const modeTag = (mode: string | undefined, onClick: () => void, can: boolean) =>
  h(NTag, { size: 'small', bordered: false, type: mode === 'source' ? 'warning' : 'info', style: can ? 'cursor:pointer' : '', onClick: can ? onClick : undefined, title: can ? t('bm.click_switch') : '' },
    () => `${mode === 'source' ? t('bm.source_short') : t('bm.fast_short')}${can ? ' ⇄' : ''}`)

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

async function update(i: InstalledApp) {
  try {
    const r = await post<{ task_id: string }>(`/apps/${i.id}/update`)
    task.value = { id: r.task_id, title: `${t('app.update')} · ${i.name} ${i.version}` }
  } catch (e) {
    fail(e)
  }
}

async function phpAction(v: PhpVer, a: 'update' | 'remove') {
  try {
    const r = await post<{ task_id?: string }>(`/php/${v.version}/${a}`)
    if (r.task_id) task.value = { id: r.task_id, title: `${t(a === 'update' ? 'app.update' : 'app.uninstall')} · PHP ${v.version}` }
    else {
      msg.success(t('common.done'))
      load()
    }
  } catch (e) {
    fail(e)
  }
}

async function phpSettings(v: PhpVer) {
  tab.value = 'php'
  await nextTick()
  phpRef.value?.openVersion(v.version)
}

async function checkUpdates() {
  try {
    await updates.check()
    msg.success(updates.available.length ? t('app.updates_banner', { n: updates.available.length }) : t('app.all_up_to_date'))
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

// The installed list holds App Store apps and the PHP versions of the web stack.
type Row = { key: string; app?: InstalledApp; php?: PhpVer }
const rows = computed<Row[]>(() => [
  ...installed.value.map((a) => ({ key: `app:${a.id}`, app: a })),
  ...phps.value.map((p) => ({ key: `php:${p.version}`, php: p })),
])

const versionCell = (name: string, version: string, full: string | undefined, upd: ReturnType<typeof updates.forApp>, onUpdate: () => void, databases = false) =>
  h(NSpace, { size: 6, align: 'center', wrap: false }, () => [
    h('span', [h('b', name), ` ${version}`, full ? h('span', { class: 'full' }, ` · ${full}`) : null]),
    h(UpdateBadge, { upd, name: `${name} ${version}`, databases, onUpdate }),
  ])

const uninstallButton = (name: string, onConfirm: () => void, withData: boolean, extra?: string) =>
  h(
    NPopconfirm,
    { onPositiveClick: onConfirm, onAfterLeave: () => (removeData.value = false) },
    {
      trigger: () => h(NButton, { size: 'tiny', type: 'error', ghost: true }, () => t('app.uninstall')),
      default: () =>
        h('div', { style: 'max-width:320px' }, [
          h('div', t('app.uninstall_confirm', { name })),
          extra ? h('div', { class: 'hint' }, extra) : null,
          withData
            ? h(NCheckbox, { checked: removeData.value, 'onUpdate:checked': (v: boolean) => (removeData.value = v), style: 'margin-top:8px' }, () => t('app.remove_data'))
            : null,
        ]),
    },
  )

const columns: DataTableColumns<Row> = [
  {
    title: () => t('app.name'),
    key: 'name',
    width: 300,
    render: (r) =>
      r.app
        ? versionCell(r.app.name, r.app.version, r.app.full_version, updates.forApp(r.app.id), () => update(r.app!), r.app.databases)
        : versionCell('PHP', r.php!.version, r.php!.full_version, updates.forPHP(r.php!.version), () => phpAction(r.php!, 'update')),
  },
  { title: () => t('app.kind'), key: 'kind', width: 100, render: (r) => t(`app.kinds.${r.app ? r.app.kind : 'runtime'}`) },
  {
    title: () => t('bm.column'),
    key: 'build',
    width: 110,
    render: (r) =>
      r.app
        ? modeTag(r.app.build_mode, () => switchBuild(r.app!), !r.app.source_no || r.app.build_mode === 'source')
        : modeTag(r.php!.build?.build_mode, () => switchPHP(r.php!), true),
  },
  {
    title: () => t('common.status'),
    key: 'state',
    width: 110,
    render: (r) => (r.app?.kind === 'runtime' ? h(NTag, { size: 'small', type: 'success' }, () => t('app.ready')) : stateTag(r.app ? r.app.state : r.php!.state)),
  },
  { title: () => t('app.port'), key: 'port', width: 130, render: (r) => { const p = r.app ? r.app.port : r.php!.port; return p ? `127.0.0.1:${p}` : '—' } },
  {
    title: '',
    key: 'actions',
    width: 380,
    render: (r) => {
      if (r.php) {
        const v = r.php
        return h(NSpace, { size: 6 }, () => [
          h(NButton, { size: 'tiny', type: 'primary', onClick: () => phpSettings(v) }, () => t('app.settings')),
          h(NButton, { size: 'tiny', onClick: () => (logs.value = { url: `/php/${v.version}/logs`, title: `PHP ${v.version}` }) }, () => t('app.logs')),
          v.sites > 0
            ? h(NButton, { size: 'tiny', type: 'error', ghost: true, disabled: true, title: t('php.in_use_count', { n: v.sites }) }, () => t('app.uninstall'))
            : uninstallButton(`PHP ${v.version}`, () => phpAction(v, 'remove'), false, t('php.remove_confirm')),
        ])
      }
      const i = r.app!
      return h(NSpace, { size: 6 }, () => [
        h(NButton, { size: 'tiny', type: 'primary', onClick: () => (settings.value = i) }, () => t('app.settings')),
        ...(i.container
          ? [
              i.state === 'running'
                ? h(NButton, { size: 'tiny', onClick: () => control(i, 'restart') }, () => t('app.restart'))
                : h(NButton, { size: 'tiny', type: 'primary', ghost: true, onClick: () => control(i, 'start') }, () => t('app.start')),
              h(NButton, { size: 'tiny', onClick: () => (logs.value = { url: `/apps/${i.id}/logs`, title: `${i.name} ${i.version}` }) }, () => t('app.logs')),
            ]
          : []),
        i.kind === 'database' ? h(NButton, { size: 'tiny', onClick: () => openCred(i) }, () => t('app.credentials')) : null,
        i.app === 'adminer' ? h(NButton, { size: 'tiny', tag: 'a', href: '/adminer/', target: '_blank' }, () => t('app.open')) : null,
        i.app === 'phpmyadmin' ? h(NButton, { size: 'tiny', tag: 'a', href: '/phpmyadmin/', target: '_blank' }, () => t('app.open')) : null,
        (i.app === 'mysql' || i.app === 'mariadb') && hasPMA.value
          ? h(NButton, { size: 'tiny', tag: 'a', href: '/phpmyadmin/', target: '_blank' }, () => 'phpMyAdmin')
          : null,
        uninstallButton(`${i.name} ${i.version}`, () => uninstall(i), i.kind !== 'runtime', t('app.uninstall_hint')),
      ])
    },
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
      <n-alert v-if="updates.available.length" type="success" :show-icon="true" style="margin-bottom: 12px">
        {{ t('app.updates_banner', { n: updates.available.length }) }}: {{ updates.available.map((u) => `${u.name} ${u.current} → ${u.latest && u.latest !== u.current ? u.latest : t('app.new_build')}`).join('、') }}
      </n-alert>
      <n-space justify="space-between" align="center" style="margin-bottom: 12px">
        <span class="hint">{{ updates.checkedAt ? t('app.checked_at', { t: datetime(updates.checkedAt, locale) }) : t('app.never_checked') }}</span>
        <n-space>
          <n-button size="small" :loading="updates.checking" @click="checkUpdates">{{ t('app.check_updates') }}</n-button>
          <n-button size="small" @click="load">{{ t('common.refresh') }}</n-button>
        </n-space>
      </n-space>
      <n-data-table size="small" :columns="columns" :data="rows" :loading="loading" :row-key="(r: Row) => r.key" :scroll-x="1130" />
      <p class="hint">{{ t('app.updates_hint') }}</p>
    </n-tab-pane>
    <n-tab-pane name="php" tab="PHP" display-directive="show:lazy"><PhpVersions ref="phpRef" @changed="load" /></n-tab-pane>
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

  <BuildModeDialog
    :show="!!bm" :title="bm?.title ?? ''" :info="bm ? source[bm.key] ?? null : null" :current="bm?.current ?? null"
    @close="bm = null" @confirm="runBuild"
  />
  <AppSettings :app="settings" @close="settings = null" @changed="load" />
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
.hint {
  font-size: 12px;
  opacity: 0.7;
}
:deep(.full) {
  opacity: 0.65;
  font-size: 12px;
}
</style>
