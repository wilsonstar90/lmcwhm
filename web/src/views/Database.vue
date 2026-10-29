<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import {
  NButton, NDataTable, NSpace, NSelect, NModal, NCard, NForm, NFormItem, NInput, NAlert, NPopconfirm, NTag, NTabs, NTab,
  useMessage, type DataTableColumns,
} from 'naive-ui'
import { get, post, patch, ApiError } from '../api/client'
import type { Database, InstalledApp } from '../api/types'
import TaskModal from '../components/TaskModal.vue'
import AppSettings from '../components/AppSettings.vue'
import RedisKeys from '../components/RedisKeys.vue'
import UpdateBadge from '../components/UpdateBadge.vue'
import { useUpdates } from '../stores/updates'

const { t } = useI18n()
const router = useRouter()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))

const dbs = ref<Database[]>([])
// Installed database services, one tab each (only installed engines show).
const services = ref<InstalledApp[]>([])
const engines = computed(() => services.value.filter((i) => i.databases))
const settings = ref<InstalledApp | null>(null)
const adminer = ref(false)
// phpMyAdmin server number per engine app id (empty = phpMyAdmin not installed)
const pma = ref<Record<number, number>>({})
const loading = ref(false)
const engine = ref<number | null>(null)
const current = computed(() => services.value.find((e) => e.id === engine.value) ?? null)
const show = ref(false)
const task = ref<{ id: string; title: string } | null>(null)
const updates = useUpdates()
updates.load().catch(() => {})
async function updateEngine(e: InstalledApp) {
  try {
    const r = await post<{ task_id: string }>(`/apps/${e.id}/update`)
    task.value = { id: r.task_id, title: `${t('app.update')} · ${e.name} ${e.version}` }
  } catch (err) {
    msg.error(err instanceof ApiError ? err.text : String(err))
  }
}
const revealed = ref<Set<number>>(new Set())
const pw = ref<{ db: Database; value: string } | null>(null)

const blank = () => ({ app_id: 0, name: '', username: '', password: '', remark: '' })
const f = ref(blank())

async function load() {
  loading.value = true
  try {
    const [d, a] = await Promise.all([get<{ items: Database[]; adminer: boolean; phpmyadmin: Record<number, number> }>('/databases'), get<{ items: InstalledApp[] }>('/apps')])
    dbs.value = d.items
    adminer.value = d.adminer
    pma.value = d.phpmyadmin ?? {}
    services.value = a.items.filter((i) => i.kind === 'database')
    if (!services.value.some((e) => e.id === engine.value)) engine.value = services.value[0]?.id ?? null
  } catch (e) {
    fail(e)
  } finally {
    loading.value = false
  }
}
onMounted(load)

const engineOptions = computed(() => engines.value.map((e) => ({ label: `${e.name} ${e.version} (127.0.0.1:${e.port})`, value: e.id })))
const isMySQL = (app: string) => app === 'mysql' || app === 'mariadb'
const hasMySQL = computed(() => engines.value.some((e) => isMySQL(e.app)))
const hasPMA = computed(() => Object.keys(pma.value).length > 0)
const filtered = computed(() => dbs.value.filter((d) => d.app_id === engine.value))

// phpMyAdmin opens through a one-time sign-on link: from a database row as
// that database's own user (only it is shown), from the toolbar as root.
async function openPMA(path: string) {
  const win = window.open('about:blank', '_blank')
  try {
    const r = await get<{ url: string }>(path)
    if (win) win.location.href = r.url
    else window.location.href = r.url
  } catch (e) {
    win?.close()
    fail(e)
  }
}

function openCreate() {
  f.value = blank()
  f.value.app_id = current.value?.databases ? current.value.id : (engines.value[0]?.id ?? 0)
  show.value = true
}

async function create() {
  try {
    const r = await post<{ task_id: string }>('/databases', f.value)
    show.value = false
    task.value = { id: r.task_id, title: `${t('db.create')} · ${f.value.name}` }
  } catch (e) {
    fail(e)
  }
}

async function remove(d: Database) {
  try {
    const r = await post<{ task_id: string }>(`/databases/${d.id}/delete`)
    task.value = { id: r.task_id, title: `${t('db.delete')} · ${d.name}` }
  } catch (e) {
    fail(e)
  }
}

async function setPassword() {
  const p = pw.value!
  try {
    await post(`/databases/${p.db.id}/password`, { password: p.value })
    pw.value = null
    msg.success(t('common.saved'))
    load()
  } catch (e) {
    fail(e)
  }
}

async function setRemark(d: Database, remark: string) {
  try {
    await patch(`/databases/${d.id}`, { remark })
    d.remark = remark
  } catch (e) {
    fail(e)
  }
}

function copy(text: string) {
  navigator.clipboard?.writeText(text).then(() => msg.success(t('common.copied')))
}

function toggle(id: number) {
  const s = new Set(revealed.value)
  s.has(id) ? s.delete(id) : s.add(id)
  revealed.value = s
}

const columns: DataTableColumns<Database> = [
  { title: () => t('db.name'), key: 'name', render: (d) => h('b', d.name) },
  { title: () => t('db.username'), key: 'username', width: 140 },
  {
    title: () => t('db.password'),
    key: 'password',
    width: 260,
    render: (d) =>
      h(NSpace, { size: 6, align: 'center' }, () => [
        h('code', revealed.value.has(d.id) ? d.password : '••••••••'),
        h(NButton, { text: true, type: 'primary', onClick: () => toggle(d.id) }, () => (revealed.value.has(d.id) ? t('db.hide') : t('db.show'))),
        h(NButton, { text: true, type: 'primary', onClick: () => copy(d.password) }, () => t('common.copy')),
      ]),
  },
  { title: () => t('db.host'), key: 'host', width: 140, render: (d) => `${d.host}:${d.port}` },
  {
    title: () => t('site.remark'),
    key: 'remark',
    render: (d) =>
      h(NInput, { size: 'small', value: d.remark, placeholder: '—', onChange: (v: string) => setRemark(d, v), 'onUpdate:value': (v: string) => (d.remark = v) }),
  },
  {
    title: '',
    key: 'actions',
    width: 260,
    render: (d) =>
      h(NSpace, { size: 6 }, () => [
        pma.value[d.app_id] ? h(NButton, { size: 'tiny', type: 'primary', ghost: true, onClick: () => openPMA(`/databases/${d.id}/phpmyadmin`) }, () => 'phpMyAdmin') : null,
        h(NButton, { size: 'tiny', onClick: () => (pw.value = { db: d, value: '' }) }, () => t('db.change_password')),
        h(
          NPopconfirm,
          { onPositiveClick: () => remove(d) },
          { trigger: () => h(NButton, { size: 'tiny', type: 'error', ghost: true }, () => t('common.delete')), default: () => t('db.delete_confirm', { name: d.name }) },
        ),
      ]),
  },
]
</script>

<template>
  <n-alert v-if="!loading && !services.length" type="info" :show-icon="false" style="margin-bottom: 12px">
    {{ t('db.no_engine') }}
    <n-button text type="primary" @click="router.push({ name: 'appstore' })">{{ t('menu.appstore') }} →</n-button>
  </n-alert>
  <n-alert v-if="!loading && current && isMySQL(current.app) && !hasPMA" type="default" :show-icon="false" style="margin-bottom: 12px">
    {{ t('db.pma_hint') }}
    <n-button text type="primary" @click="router.push({ name: 'appstore' })">{{ t('menu.appstore') }} →</n-button>
  </n-alert>
  <n-tabs v-if="services.length" :value="engine ?? undefined" type="line" @update:value="(v: number) => (engine = v)" style="margin-bottom: 12px">
    <n-tab v-for="e in services" :key="e.id" :name="e.id">{{ e.name }} {{ e.version }}</n-tab>
  </n-tabs>
  <template v-if="current">
    <n-space justify="space-between" align="center" style="margin-bottom: 12px">
      <n-space align="center">
        <n-button v-if="current.databases" type="primary" @click="openCreate">{{ t('db.create') }}</n-button>
        <n-button v-if="isMySQL(current.app) && pma[current.id]" @click="openPMA(`/apps/${current.id}/phpmyadmin`)">{{ t('db.pma_root') }}</n-button>
        <n-button v-if="adminer && current.databases" tag="a" href="/adminer/" target="_blank">Adminer</n-button>
        <n-button @click="settings = current">{{ t('db.settings') }}</n-button>
      </n-space>
      <n-space align="center">
        <span class="hint">{{ current.name }} {{ current.full_version || current.version }} · 127.0.0.1:{{ current.port }}</span>
        <UpdateBadge :upd="updates.forApp(current.id)" :name="`${current.name} ${current.version}`" :databases="filtered.length > 0" @update="updateEngine(current)" />
        <n-tag size="small" :type="current.state === 'running' ? 'success' : 'error'">{{ current.state || '—' }}</n-tag>
        <n-button size="small" @click="load">{{ t('common.refresh') }}</n-button>
      </n-space>
    </n-space>
    <RedisKeys v-if="current.app === 'redis'" :key="current.id" :app="current" />
    <n-data-table v-else size="small" :columns="columns" :data="filtered" :loading="loading" :row-key="(r: Database) => r.id" :scroll-x="1000" />
  </template>

  <n-modal :show="show" @update:show="(v: boolean) => (show = v)">
    <n-card :title="t('db.create')" style="width: min(520px, calc(100vw - 32px))" closable @close="show = false">
      <n-form label-placement="top">
        <n-form-item :label="t('db.engine')">
          <n-select v-model:value="f.app_id" :options="engineOptions" />
        </n-form-item>
        <n-form-item :label="t('db.name')" :feedback="t('db.name_hint')">
          <n-input v-model:value="f.name" placeholder="wordpress" />
        </n-form-item>
        <n-form-item :label="t('db.username')">
          <n-input v-model:value="f.username" :placeholder="f.name || t('db.same_as_name')" />
        </n-form-item>
        <n-form-item :label="t('db.password')" :feedback="t('db.password_hint')">
          <n-input v-model:value="f.password" :placeholder="t('db.generate')" />
        </n-form-item>
        <n-form-item :label="t('site.remark')">
          <n-input v-model:value="f.remark" />
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="show = false">{{ t('common.cancel') }}</n-button>
          <n-button type="primary" :disabled="!f.name || !f.app_id" @click="create">{{ t('common.confirm') }}</n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>

  <n-modal :show="!!pw" @update:show="(v: boolean) => !v && (pw = null)">
    <n-card v-if="pw" :title="`${t('db.change_password')} · ${pw.db.username}`" style="width: min(440px, calc(100vw - 32px))" closable @close="pw = null">
      <n-input v-model:value="pw.value" :placeholder="t('db.generate')" />
      <p style="font-size: 12px; opacity: 0.7">{{ t('db.password_hint') }}</p>
      <template #footer>
        <n-space justify="end">
          <n-button @click="pw = null">{{ t('common.cancel') }}</n-button>
          <n-button type="primary" @click="setPassword">{{ t('common.confirm') }}</n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>

  <AppSettings :app="settings" @close="settings = null" @changed="load" />
  <TaskModal :task-id="task?.id ?? null" :title="task?.title ?? ''" @close="task = null; load()" @done="load" />
</template>

<style scoped>
.hint {
  font-size: 12px;
  opacity: 0.65;
}
</style>
