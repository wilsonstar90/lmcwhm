<script setup lang="ts">
import { computed, h, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NCard, NDataTable, NDynamicTags, NForm, NFormItem, NInput, NInputNumber, NModal, NPopconfirm, NRadioButton,
  NRadioGroup, NSelect, NSpace, NTabPane, NTabs, NTag, useMessage, type DataTableColumns,
} from 'naive-ui'
import { get, post, put, ApiError } from '../api/client'
import { bytes, datetime } from '../format'
import TaskModal from '../components/TaskModal.vue'

interface Rec { id: number; kind: string; target: string; target_id: number; name: string; size: number; dest: string; dest_name: string; job_id: number; created_at: number }
type Opt = { label: string; value: string }
interface Targets { site: Opt[]; database: Opt[]; volume: Opt[]; dest: Opt[] }
interface Device { user_code: string; url: string; interval: number; expires_at: number }
interface Remote { id: number; name: string; kind: string; client_id: string; account: string; authorized: boolean; device?: Device; created_at: number }
interface Job { id: number; name: string; type: string; schedule: string; command: string; enabled: boolean; next: number; last?: { status: string; started_at: number } }
interface Plan { kind: string; target: string; dests: string[]; keep: number; exclude?: string[] }

const { t, locale } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))
const tab = ref('list')
const recs = ref<Rec[]>([])
const kindFilter = ref<string | null>(null)
const dir = ref('')
const localBytes = ref(0)
const targets = ref<Targets>({ site: [], database: [], volume: [], dest: [] })
const remotes = ref<Remote[]>([])
const jobs = ref<Job[]>([])
const task = ref<{ id: string; title: string } | null>(null)
const kinds = ['site', 'database', 'panel', 'volume', 'path']
const kindOptions = computed(() => kinds.map((k) => ({ label: t(`bk.kinds.${k}`), value: k })))

async function loadRecs() {
  try {
    const r = await get<{ items: Rec[]; dir: string; local_bytes: number }>(`/backups${kindFilter.value ? `?kind=${kindFilter.value}` : ''}`)
    recs.value = r.items
    dir.value = r.dir
    localBytes.value = r.local_bytes
  } catch (e) {
    fail(e)
  }
}
async function loadTargets() {
  targets.value = await get<Targets>('/backups/targets')
}
async function loadRemotes() {
  remotes.value = (await get<{ items: Remote[] }>('/backups/remotes')).items
}
async function loadJobs() {
  jobs.value = (await get<{ items: Job[] }>('/cron')).items.filter((j) => j.type === 'backup')
}
function loadAll() {
  loadRecs()
  loadTargets().catch(fail)
  loadRemotes().catch(fail)
  loadJobs().catch(fail)
}
onMounted(loadAll)

const destLabel = (d: string) => (d === 'local' ? t('bk.local') : targets.value.dest.find((x) => x.value === d)?.label ?? d)
function targetOptions(kind: string, all: boolean): Opt[] {
  const list = kind === 'site' ? targets.value.site : kind === 'database' ? targets.value.database : kind === 'volume' ? targets.value.volume : []
  return all && (kind === 'site' || kind === 'database') ? [{ label: t('bk.all'), value: '*' }, ...list] : list
}
function targetLabel(p: Plan) {
  if (p.kind === 'panel') return t('bk.kinds.panel')
  if (p.target === '*') return t('bk.all')
  return targetOptions(p.kind, false).find((o) => o.value === p.target)?.label ?? p.target
}

// ---- run now / plan form ----
interface Form { mode: 'now' | 'plan'; id: number; name: string; kind: string; target: string; dests: string[]; keep: number; exclude: string[]; schedule: string; preset: string; enabled: boolean }
const f = ref<Form | null>(null)
const presets = computed(() => [
  { label: t('bk.presets.daily'), value: '0 3 * * *' },
  { label: t('bk.presets.twice'), value: '0 3,15 * * *' },
  { label: t('bk.presets.hourly6'), value: '0 */6 * * *' },
  { label: t('bk.presets.weekly'), value: '0 3 * * 0' },
  { label: t('bk.presets.custom'), value: 'custom' },
])
function openForm(mode: 'now' | 'plan', j?: Job) {
  const x: Form = { mode, id: 0, name: '', kind: 'site', target: '*', dests: ['local'], keep: 7, exclude: [], schedule: '0 3 * * *', preset: '0 3 * * *', enabled: true }
  if (j) {
    const p = JSON.parse(j.command) as Plan
    Object.assign(x, { id: j.id, name: j.name, kind: p.kind, target: p.target, dests: p.dests, keep: p.keep, exclude: p.exclude ?? [], schedule: j.schedule, enabled: j.enabled })
    x.preset = presets.value.some((o) => o.value === j.schedule) ? j.schedule : 'custom'
  }
  if (mode === 'now') x.target = ''
  f.value = x
}
const plan = (x: Form): Plan => ({ kind: x.kind, target: x.kind === 'panel' ? '' : x.target, dests: x.dests, keep: x.keep, exclude: x.exclude })
async function submit() {
  const x = f.value!
  try {
    if (x.mode === 'now') {
      const r = await post<{ task_id: string }>('/backups/run', plan(x))
      task.value = { id: r.task_id, title: t('bk.run_now') }
    } else {
      const body = { name: x.name || `${t(`bk.kinds.${x.kind}`)} ${targetLabel(plan(x))}`, type: 'backup', schedule: x.preset === 'custom' ? x.schedule : x.preset,
        command: JSON.stringify(plan(x)), enabled: x.enabled, timeout: 43200 }
      if (x.id) await put(`/cron/${x.id}`, body)
      else await post('/cron', body)
      msg.success(t('common.saved'))
      loadJobs()
    }
    f.value = null
  } catch (e) {
    fail(e)
  }
}
async function runJob(j: Job) {
  try {
    await post(`/cron/${j.id}/run`)
    msg.success(t('bk.started'))
    setTimeout(loadJobs, 2000)
  } catch (e) {
    fail(e)
  }
}
async function delJob(j: Job) {
  await post(`/cron/${j.id}/delete`).catch(fail)
  loadJobs()
}

// ---- restore / delete / import ----
const restoreOf = ref<Rec | null>(null)
const into = ref<string | null>(null)
async function restore() {
  const r = restoreOf.value!
  try {
    const res = await post<{ task_id: string }>(`/backups/${r.id}/restore`, { into: into.value ? Number(into.value) : 0 })
    task.value = { id: res.task_id, title: `${t('bk.restore')} · ${r.name}` }
    restoreOf.value = null
  } catch (e) {
    fail(e)
  }
}
async function del(r: Rec) {
  try {
    await post(`/backups/${r.id}/delete`)
    loadRecs()
  } catch (e) {
    fail(e)
  }
}
const imp = ref<{ kind: string; target: string; target_id: string | null; path: string } | null>(null)
async function doImport() {
  const x = imp.value!
  try {
    await post('/backups/import', { kind: x.kind, target: x.target, target_id: x.target_id ? Number(x.target_id) : 0, path: x.path })
    imp.value = null
    msg.success(t('common.done'))
    loadRecs()
  } catch (e) {
    fail(e)
  }
}

const recCols = computed<DataTableColumns<Rec>>(() => [
  { title: () => t('common.time'), key: 'created_at', width: 170, render: (r) => datetime(r.created_at, locale.value) },
  { title: () => t('bk.kind'), key: 'kind', width: 110, render: (r) => h(NTag, { size: 'small' }, () => t(`bk.kinds.${r.kind}`)) },
  { title: () => t('bk.target'), key: 'target', ellipsis: { tooltip: true } },
  { title: () => t('bk.file'), key: 'name', ellipsis: { tooltip: true } },
  { title: () => t('bk.size'), key: 'size', width: 100, render: (r) => bytes(r.size) },
  { title: () => t('bk.dest'), key: 'dest', width: 140, render: (r) => h(NTag, { size: 'small', type: r.dest === 'local' ? 'default' : 'info' }, () => (r.dest === 'local' ? t('bk.local') : r.dest_name)) },
  {
    title: () => t('common.actions'),
    key: 'a',
    width: 230,
    render: (r) =>
      h(NSpace, { size: 4 }, () => [
        h(NButton, { size: 'tiny', tag: 'a', href: `/api/v1/backups/${r.id}/download` }, () => t('bk.download')),
        r.kind !== 'panel' ? h(NButton, { size: 'tiny', onClick: () => ((restoreOf.value = r), (into.value = null)) }, () => t('bk.restore')) : null,
        h(NPopconfirm, { onPositiveClick: () => del(r) }, { trigger: () => h(NButton, { size: 'tiny', type: 'error', quaternary: true }, () => t('common.delete')), default: () => t('bk.delete_confirm') }),
      ]),
  },
])
const jobCols = computed<DataTableColumns<Job>>(() => [
  { title: () => t('cron.name'), key: 'name' },
  { title: () => t('bk.what'), key: 'command', render: (j) => { const p = JSON.parse(j.command) as Plan; return `${t(`bk.kinds.${p.kind}`)} · ${targetLabel(p)}` } },
  { title: () => t('bk.dest'), key: 'dests', render: (j) => (JSON.parse(j.command) as Plan).dests.map(destLabel).join(', ') },
  { title: () => t('bk.keep'), key: 'keep', width: 80, render: (j) => (JSON.parse(j.command) as Plan).keep || '∞' },
  { title: () => t('cron.schedule'), key: 'schedule', width: 130, render: (j) => h('code', j.schedule) },
  { title: () => t('cron.next'), key: 'next', width: 170, render: (j) => (j.enabled && j.next ? datetime(j.next, locale.value) : '—') },
  { title: () => t('cron.last'), key: 'last', width: 100, render: (j) => (j.last ? h(NTag, { size: 'small', type: j.last.status === 'success' ? 'success' : j.last.status === 'running' ? 'info' : 'error' }, () => t(`cron.status.${j.last!.status}`)) : '—') },
  {
    title: () => t('common.actions'),
    key: 'a',
    width: 200,
    render: (j) =>
      h(NSpace, { size: 4 }, () => [
        h(NButton, { size: 'tiny', onClick: () => runJob(j) }, () => t('cron.run')),
        h(NButton, { size: 'tiny', onClick: () => openForm('plan', j) }, () => t('common.edit')),
        h(NPopconfirm, { onPositiveClick: () => delJob(j) }, { trigger: () => h(NButton, { size: 'tiny', type: 'error', quaternary: true }, () => t('common.delete')), default: () => t('common.confirm_delete') }),
      ]),
  },
])

// ---- remotes ----
const rf = ref<{ kind: 'gdrive' | 'onedrive'; name: string; client_id: string; client_secret: string; tenant: string } | null>(null)
const authorizing = ref<Remote | null>(null)
let pollTimer: number | undefined
async function addRemote() {
  try {
    const r = await post<Remote>('/backups/remotes', rf.value)
    rf.value = null
    startPoll(r)
  } catch (e) {
    fail(e)
  }
}
async function reauth(r: Remote) {
  try {
    startPoll(await post<Remote>(`/backups/remotes/${r.id}/reauthorize`))
  } catch (e) {
    fail(e)
  }
}
function startPoll(r: Remote) {
  authorizing.value = r
  clearInterval(pollTimer)
  pollTimer = window.setInterval(async () => {
    try {
      const res = await post<{ pending?: boolean; expired?: boolean; remote: Remote }>(`/backups/remotes/${r.id}/poll`)
      if (res.pending) return
      clearInterval(pollTimer)
      authorizing.value = null
      if (res.expired) msg.warning(t('bk.auth_expired'))
      else msg.success(t('bk.authorized', { account: res.remote.account }))
      loadRemotes()
      loadTargets()
    } catch (e) {
      clearInterval(pollTimer)
      authorizing.value = null
      fail(e)
    }
  }, Math.max(r.device?.interval ?? 5, 3) * 1000)
}
onBeforeUnmount(() => clearInterval(pollTimer))
async function delRemote(r: Remote) {
  await post(`/backups/remotes/${r.id}/delete`).catch(fail)
  loadRemotes()
  loadTargets()
}
const remoteCols = computed<DataTableColumns<Remote>>(() => [
  { title: () => t('bk.remote_name'), key: 'name', render: (r) => h('b', r.name) },
  { title: () => t('bk.provider'), key: 'kind', width: 140, render: (r) => (r.kind === 'gdrive' ? 'Google Drive' : 'OneDrive') },
  { title: () => t('bk.account'), key: 'account' },
  { title: () => t('common.status'), key: 'authorized', width: 120, render: (r) => h(NTag, { size: 'small', type: r.authorized ? 'success' : 'warning' }, () => (r.authorized ? t('bk.connected') : t('bk.not_connected'))) },
  {
    title: () => t('common.actions'),
    key: 'a',
    width: 200,
    render: (r) =>
      h(NSpace, { size: 4 }, () => [
        h(NButton, { size: 'tiny', onClick: () => reauth(r) }, () => t('bk.reauthorize')),
        h(NPopconfirm, { onPositiveClick: () => delRemote(r) }, { trigger: () => h(NButton, { size: 'tiny', type: 'error', quaternary: true }, () => t('common.delete')), default: () => t('bk.remote_delete_confirm') }),
      ]),
  },
])
</script>

<template>
  <n-tabs v-model:value="tab" type="line" animated>
    <n-tab-pane name="list" :tab="t('bk.tab_list')">
      <n-space justify="space-between" align="center" style="margin-bottom: 12px">
        <n-space>
          <n-button type="primary" @click="openForm('now')">{{ t('bk.run_now') }}</n-button>
          <n-button @click="imp = { kind: 'site', target: '', target_id: null, path: '' }">{{ t('bk.import') }}</n-button>
          <n-select v-model:value="kindFilter" :options="kindOptions" clearable :placeholder="t('bk.all_kinds')" style="width: 160px" @update:value="loadRecs" />
        </n-space>
        <span class="hint">{{ t('bk.local_usage', { dir, size: bytes(localBytes) }) }}</span>
      </n-space>
      <n-data-table size="small" :columns="recCols" :data="recs" :row-key="(r: Rec) => r.id" :pagination="{ pageSize: 20 }" :scroll-x="1000" />
    </n-tab-pane>

    <n-tab-pane name="plans" :tab="t('bk.tab_plans')">
      <n-space style="margin-bottom: 12px"><n-button type="primary" @click="openForm('plan')">{{ t('bk.new_plan') }}</n-button></n-space>
      <n-data-table size="small" :columns="jobCols" :data="jobs" :row-key="(j: Job) => j.id" :scroll-x="1100" />
    </n-tab-pane>

    <n-tab-pane name="remotes" :tab="t('bk.tab_remotes')">
      <n-alert type="info" :show-icon="false" style="margin-bottom: 12px">{{ t('bk.remote_intro') }}</n-alert>
      <n-space style="margin-bottom: 12px">
        <n-button type="primary" @click="rf = { kind: 'gdrive', name: 'Google Drive', client_id: '', client_secret: '', tenant: '' }">+ Google Drive</n-button>
        <n-button type="primary" @click="rf = { kind: 'onedrive', name: 'OneDrive', client_id: '', client_secret: '', tenant: 'common' }">+ OneDrive</n-button>
      </n-space>
      <n-data-table size="small" :columns="remoteCols" :data="remotes" :row-key="(r: Remote) => r.id" />
    </n-tab-pane>
  </n-tabs>

  <n-modal :show="!!f" @update:show="(v: boolean) => !v && (f = null)">
    <n-card v-if="f" :title="f.mode === 'now' ? t('bk.run_now') : f.id ? t('common.edit') : t('bk.new_plan')" style="width: min(620px, calc(100vw - 32px))" closable @close="f = null">
      <n-form label-placement="top">
        <n-form-item v-if="f.mode === 'plan'" :label="t('cron.name')"><n-input v-model:value="f.name" :placeholder="t('bk.plan_name_hint')" /></n-form-item>
        <n-form-item :label="t('bk.kind')">
          <n-radio-group v-model:value="f.kind" @update:value="f.target = f.mode === 'plan' ? '*' : ''">
            <n-radio-button v-for="k in kinds" :key="k" :value="k">{{ t(`bk.kinds.${k}`) }}</n-radio-button>
          </n-radio-group>
        </n-form-item>
        <n-form-item v-if="['site', 'database', 'volume'].includes(f.kind)" :label="t('bk.target')">
          <n-select v-model:value="f.target" :options="targetOptions(f.kind, true)" filterable />
        </n-form-item>
        <n-form-item v-if="f.kind === 'path'" :label="t('bk.path')"><n-input v-model:value="f.target" placeholder="/opt/data" /></n-form-item>
        <n-form-item :label="t('bk.dest')">
          <n-select v-model:value="f.dests" :options="targets.dest.map((d) => ({ label: d.value === 'local' ? t('bk.local') : d.label, value: d.value }))" multiple />
        </n-form-item>
        <n-form-item v-if="f.kind === 'site' || f.kind === 'path'" :label="t('bk.exclude')" :feedback="t('bk.exclude_hint')">
          <n-dynamic-tags v-model:value="f.exclude" />
        </n-form-item>
        <n-form-item :label="t('bk.keep')" :feedback="t('bk.keep_hint')"><n-input-number v-model:value="f.keep" :min="0" :max="1000" /></n-form-item>
        <template v-if="f.mode === 'plan'">
          <n-form-item :label="t('cron.schedule')">
            <n-space><n-select v-model:value="f.preset" :options="presets" style="width: 220px" /><n-input v-if="f.preset === 'custom'" v-model:value="f.schedule" style="width: 180px" /></n-space>
          </n-form-item>
        </template>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="f = null">{{ t('common.cancel') }}</n-button>
          <n-button type="primary" :disabled="!f.dests.length || (f.kind !== 'panel' && !f.target)" @click="submit">{{ f.mode === 'now' ? t('bk.start') : t('common.save') }}</n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>

  <n-modal :show="!!restoreOf" @update:show="(v: boolean) => !v && (restoreOf = null)">
    <n-card v-if="restoreOf" :title="`${t('bk.restore')} · ${restoreOf.name}`" style="width: min(520px, calc(100vw - 32px))" closable @close="restoreOf = null">
      <n-alert type="warning" :show-icon="false">{{ t(`bk.restore_warn.${restoreOf.kind}`, { target: restoreOf.target }) }}</n-alert>
      <n-form-item v-if="restoreOf.kind === 'database'" :label="t('bk.restore_into')" style="margin-top: 12px">
        <n-select v-model:value="into" :options="targets.database" clearable :placeholder="t('bk.restore_original')" />
      </n-form-item>
      <template #footer>
        <n-space justify="end">
          <n-button @click="restoreOf = null">{{ t('common.cancel') }}</n-button>
          <n-button type="warning" @click="restore">{{ t('bk.restore') }}</n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>

  <n-modal :show="!!imp" @update:show="(v: boolean) => !v && (imp = null)">
    <n-card v-if="imp" :title="t('bk.import')" style="width: min(560px, calc(100vw - 32px))" closable @close="imp = null">
      <p class="hint" style="margin-top: 0">{{ t('bk.import_hint') }}</p>
      <n-form label-placement="top">
        <n-form-item :label="t('bk.kind')"><n-select v-model:value="imp.kind" :options="kindOptions" /></n-form-item>
        <n-form-item v-if="imp.kind === 'database'" :label="t('bk.target')"><n-select v-model:value="imp.target_id" :options="targets.database" /></n-form-item>
        <n-form-item v-else-if="imp.kind === 'site'" :label="t('bk.target')"><n-select v-model:value="imp.target" :options="targets.site" /></n-form-item>
        <n-form-item v-else-if="imp.kind !== 'panel'" :label="t('bk.target')"><n-input v-model:value="imp.target" :placeholder="imp.kind === 'path' ? '/opt/data' : 'volume'" /></n-form-item>
        <n-form-item :label="t('bk.file_path')"><n-input v-model:value="imp.path" placeholder="/root/site_example.com_20261028_030000.tar.gz" /></n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end"><n-button type="primary" :disabled="!imp.path" @click="doImport">{{ t('bk.import') }}</n-button></n-space>
      </template>
    </n-card>
  </n-modal>

  <n-modal :show="!!rf" @update:show="(v: boolean) => !v && (rf = null)">
    <n-card v-if="rf" :title="rf.kind === 'gdrive' ? 'Google Drive' : 'OneDrive'" style="width: min(560px, calc(100vw - 32px))" closable @close="rf = null">
      <n-alert type="default" :show-icon="false" style="margin-bottom: 12px">{{ t(`bk.howto.${rf.kind}`) }}</n-alert>
      <n-form label-placement="top">
        <n-form-item :label="t('bk.remote_name')"><n-input v-model:value="rf.name" /></n-form-item>
        <n-form-item label="Client ID"><n-input v-model:value="rf.client_id" /></n-form-item>
        <n-form-item v-if="rf.kind === 'gdrive'" label="Client secret"><n-input v-model:value="rf.client_secret" type="password" show-password-on="click" /></n-form-item>
        <n-form-item v-else :label="t('bk.tenant')"><n-input v-model:value="rf.tenant" placeholder="common / consumers / <tenant id>" /></n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end"><n-button type="primary" :disabled="!rf.client_id || !rf.name" @click="addRemote">{{ t('bk.connect') }}</n-button></n-space>
      </template>
    </n-card>
  </n-modal>

  <n-modal :show="!!authorizing" :mask-closable="false">
    <n-card v-if="authorizing?.device" :title="t('bk.authorize_title')" style="width: min(480px, calc(100vw - 32px))" closable @close="authorizing = null">
      <p>{{ t('bk.authorize_step1') }}</p>
      <p><a :href="authorizing.device.url" target="_blank" rel="noopener">{{ authorizing.device.url }}</a></p>
      <p>{{ t('bk.authorize_step2') }}</p>
      <p class="code">{{ authorizing.device.user_code }}</p>
      <p class="hint">{{ t('bk.authorize_wait') }}</p>
    </n-card>
  </n-modal>
  <TaskModal :task-id="task?.id ?? null" :title="task?.title ?? ''" @close="task = null; loadRecs()" @done="loadRecs" />
</template>

<style scoped>
.hint {
  font-size: 12px;
  opacity: 0.65;
}
.code {
  font-size: 28px;
  font-weight: 600;
  letter-spacing: 4px;
  text-align: center;
  font-family: monospace;
}
</style>
