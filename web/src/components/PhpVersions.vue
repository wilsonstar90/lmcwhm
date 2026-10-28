<script setup lang="ts">
// PHP versions and their settings (aaPanel 軟件設定 → PHP): extensions,
// php.ini, disable_functions, FPM pool, status and logs.
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NCard, NCheckbox, NDataTable, NDescriptions, NDescriptionsItem, NDynamicTags, NForm, NFormItem, NGi, NGrid,
  NInput, NInputNumber, NModal, NPopconfirm, NSelect, NSpace, NTabPane, NTabs, NTag, useMessage, type DataTableColumns,
} from 'naive-ui'
import { get, post, put, ApiError } from '../api/client'
import TaskModal from './TaskModal.vue'
import LogsModal from './LogsModal.vue'
import { CodeEditor } from './code-editor'

interface Ver { version: string; eol: boolean; state: string; port: number; extensions: string[]; sites: number }
interface Ext { name: string; desc: string; min?: string; max?: string; slow?: boolean }
interface Pool { pm: string; max_children: number; start_servers: number; min_spare: number; max_spare: number; max_requests: number; request_timeout: number; slowlog_timeout: number }
interface Conf { extensions: string[]; ini: Record<string, string>; disable_functions: string[]; custom_ini: string; fpm: Pool }
interface Meta { items: Ver[]; extensions: Ext[]; ini_keys: { Key: string; Default: string }[]; dangerous: string[]; suggest: Pool; mem_mb: number }

const { t } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))
const meta = ref<Meta | null>(null)
const task = ref<{ id: string; title: string } | null>(null)
const logs = ref<{ url: string; title: string } | null>(null)

async function load() {
  try {
    meta.value = await get<Meta>('/php')
  } catch (e) {
    fail(e)
  }
}
onMounted(load)

async function action(v: Ver, a: string) {
  try {
    const r = await post<{ task_id?: string }>(`/php/${v.version}/${a}`)
    if (r.task_id) task.value = { id: r.task_id, title: `PHP ${v.version}` }
    else {
      msg.success(t('common.done'))
      load()
    }
  } catch (e) {
    fail(e)
  }
}

const cols = computed<DataTableColumns<Ver>>(() => [
  { title: () => t('php.version'), key: 'version', width: 120, render: (v) => h('span', [h('b', `PHP ${v.version}`), v.eol ? h(NTag, { size: 'tiny', type: 'warning', style: 'margin-left:6px' }, () => 'EOL') : null]) },
  { title: () => t('common.status'), key: 'state', width: 110, render: (v) => h(NTag, { size: 'small', type: v.state === 'running' ? 'success' : v.state ? 'warning' : 'default' }, () => v.state || t('php.not_installed')) },
  { title: () => t('php.sites'), key: 'sites', width: 80 },
  { title: () => t('php.extensions'), key: 'extensions', ellipsis: { tooltip: true }, render: (v) => v.extensions.join(', ') || '—' },
  {
    title: () => t('common.actions'),
    key: 'a',
    width: 330,
    render: (v) =>
      h(NSpace, { size: 4 }, () =>
        !v.state
          ? [h(NButton, { size: 'tiny', type: 'primary', onClick: () => action(v, 'install') }, () => t('php.install'))]
          : [
              h(NButton, { size: 'tiny', type: 'primary', onClick: () => openSettings(v) }, () => t('php.settings')),
              v.state === 'running'
                ? h(NButton, { size: 'tiny', onClick: () => action(v, 'reload') }, () => t('php.reload'))
                : h(NButton, { size: 'tiny', onClick: () => action(v, 'start') }, () => t('app.start')),
              h(NButton, { size: 'tiny', onClick: () => action(v, 'restart') }, () => t('app.restart')),
              v.state === 'running' ? h(NButton, { size: 'tiny', onClick: () => action(v, 'stop') }, () => t('app.stop')) : null,
              h(NButton, { size: 'tiny', onClick: () => (logs.value = { url: `/php/${v.version}/logs`, title: `PHP ${v.version}` }) }, () => t('app.logs')),
              v.sites === 0
                ? h(NPopconfirm, { onPositiveClick: () => action(v, 'remove') }, { trigger: () => h(NButton, { size: 'tiny', type: 'error', quaternary: true }, () => t('common.delete')), default: () => t('php.remove_confirm') })
                : null,
            ],
      ),
  },
])

// ---- settings modal ----
const cur = ref<{ v: Ver; conf: Conf; modules: string[]; status?: Record<string, number | string>; statusError?: string } | null>(null)
const tab = ref('ext')
async function openSettings(v: Ver) {
  try {
    const r = await get<{ config: Conf; modules?: string[]; status?: Record<string, number | string>; status_error?: string }>(`/php/${v.version}`)
    cur.value = { v, conf: r.config, modules: r.modules ?? [], status: r.status, statusError: r.status_error }
    tab.value = 'ext'
  } catch (e) {
    fail(e)
  }
}
const extRows = computed(() => {
  if (!cur.value || !meta.value) return []
  const v = cur.value.v.version
  const num = (s: string) => Number(s.replace('.', ''))
  return meta.value.extensions.map((x) => ({
    ...x,
    supported: (!x.min || num(v) >= num(x.min)) && (!x.max || num(v) <= num(x.max)),
    loaded: cur.value!.modules.includes(x.name.replace('_loader', ' loader').toLowerCase()) || cur.value!.modules.includes(x.name),
  }))
})
const builtin = computed(() => {
  const opt = new Set(meta.value?.extensions.map((x) => x.name) ?? [])
  return (cur.value?.modules ?? []).filter((m) => !opt.has(m))
})
function toggleExt(name: string, on: boolean) {
  const c = cur.value!.conf
  c.extensions = on ? [...c.extensions, name] : c.extensions.filter((x) => x !== name)
}
async function save() {
  const c = cur.value!
  try {
    const r = await put<{ task_id: string }>(`/php/${c.v.version}`, c.conf)
    task.value = { id: r.task_id, title: `PHP ${c.v.version} · ${t('php.settings')}` }
    cur.value = null
  } catch (e) {
    fail(e)
  }
}
function applySuggest() {
  if (meta.value && cur.value) cur.value.conf.fpm = { ...cur.value.conf.fpm, ...meta.value.suggest }
}
const pmOptions = ['dynamic', 'static', 'ondemand'].map((v) => ({ label: v, value: v }))
const statusKeys = ['pool', 'process manager', 'accepted conn', 'listen queue', 'max listen queue', 'idle processes', 'active processes', 'total processes', 'max active processes', 'max children reached', 'slow requests']
</script>

<template>
  <n-data-table v-if="meta" size="small" :columns="cols" :data="meta.items" :row-key="(v: Ver) => v.version" :scroll-x="900" />
  <p class="hint">{{ t('php.hint') }}</p>

  <n-modal :show="!!cur" @update:show="(v: boolean) => !v && (cur = null)">
    <n-card v-if="cur && meta" :title="`PHP ${cur.v.version} · ${t('php.settings')}`" style="width: min(900px, calc(100vw - 32px))" closable @close="cur = null">
      <n-tabs v-model:value="tab" type="line" animated>
        <n-tab-pane name="ext" :tab="t('php.extensions')">
          <n-alert type="info" :show-icon="false" style="margin-bottom: 12px">{{ t('php.ext_hint') }}</n-alert>
          <n-grid cols="1 s:2 m:3" responsive="screen" :x-gap="8" :y-gap="6">
            <n-gi v-for="x in extRows" :key="x.name">
              <n-checkbox :checked="cur.conf.extensions.includes(x.name)" :disabled="!x.supported" @update:checked="(v: boolean) => toggleExt(x.name, v)">
                <b>{{ x.name }}</b>
                <n-tag v-if="x.loaded" size="tiny" type="success" style="margin-left: 4px">{{ t('php.loaded') }}</n-tag>
                <n-tag v-if="x.slow" size="tiny" style="margin-left: 4px">{{ t('php.slow') }}</n-tag>
                <div class="sub">{{ x.supported ? x.desc : t('php.unsupported') }}</div>
              </n-checkbox>
            </n-gi>
          </n-grid>
          <p class="sub" style="margin-top: 12px">{{ t('php.builtin') }}: {{ builtin.join(', ') || '—' }}</p>
        </n-tab-pane>
        <n-tab-pane name="ini" :tab="t('php.ini')">
          <n-form label-placement="left" label-width="220" size="small">
            <n-form-item v-for="k in meta.ini_keys" :key="k.Key" :label="k.Key">
              <n-input v-model:value="cur.conf.ini[k.Key]" :placeholder="k.Default || t('php.panel_tz')" />
            </n-form-item>
          </n-form>
        </n-tab-pane>
        <n-tab-pane name="fn" :tab="t('php.disable_functions')">
          <p class="sub">{{ t('php.disable_hint') }}</p>
          <n-dynamic-tags v-model:value="cur.conf.disable_functions" />
          <n-space style="margin-top: 12px">
            <n-button size="small" @click="cur.conf.disable_functions = [...new Set([...cur.conf.disable_functions, ...meta.dangerous])]">{{ t('php.add_dangerous') }}</n-button>
            <n-button size="small" quaternary @click="cur.conf.disable_functions = []">{{ t('php.clear') }}</n-button>
          </n-space>
        </n-tab-pane>
        <n-tab-pane name="custom" :tab="t('php.custom')">
          <p class="sub">{{ t('php.custom_hint') }}</p>
          <CodeEditor v-model="cur.conf.custom_ini" language="ini" height="300px" placeholder="xdebug.mode = debug" />
        </n-tab-pane>
        <n-tab-pane name="fpm" :tab="t('php.fpm')">
          <n-form label-placement="left" label-width="200" size="small">
            <n-form-item label="pm"><n-select v-model:value="cur.conf.fpm.pm" :options="pmOptions" style="width: 160px" /></n-form-item>
            <n-form-item label="pm.max_children"><n-input-number v-model:value="cur.conf.fpm.max_children" :min="1" :max="2000" /></n-form-item>
            <template v-if="cur.conf.fpm.pm === 'dynamic'">
              <n-form-item label="pm.start_servers"><n-input-number v-model:value="cur.conf.fpm.start_servers" :min="1" /></n-form-item>
              <n-form-item label="pm.min_spare_servers"><n-input-number v-model:value="cur.conf.fpm.min_spare" :min="1" /></n-form-item>
              <n-form-item label="pm.max_spare_servers"><n-input-number v-model:value="cur.conf.fpm.max_spare" :min="1" /></n-form-item>
            </template>
            <n-form-item label="pm.max_requests"><n-input-number v-model:value="cur.conf.fpm.max_requests" :min="0" /></n-form-item>
            <n-form-item label="request_terminate_timeout"><n-input-number v-model:value="cur.conf.fpm.request_timeout" :min="0"><template #suffix>s</template></n-input-number></n-form-item>
            <n-form-item label="request_slowlog_timeout"><n-input-number v-model:value="cur.conf.fpm.slowlog_timeout" :min="0"><template #suffix>s</template></n-input-number></n-form-item>
          </n-form>
          <n-space align="center">
            <n-button size="small" @click="applySuggest">{{ t('php.suggest', { mem: meta.mem_mb }) }}</n-button>
            <n-button size="small" :disabled="!cur.conf.fpm.slowlog_timeout" @click="logs = { url: `/php/${cur.v.version}/logs?kind=slow`, title: `PHP ${cur.v.version} slow log` }">{{ t('php.slowlog') }}</n-button>
          </n-space>
        </n-tab-pane>
        <n-tab-pane name="status" :tab="t('php.status')">
          <n-alert v-if="!cur.status" type="warning" :show-icon="false">{{ cur.statusError || t('php.not_running') }}</n-alert>
          <n-descriptions v-else :column="2" label-placement="left" bordered size="small">
            <n-descriptions-item v-for="k in statusKeys" :key="k" :label="k">{{ cur.status[k] }}</n-descriptions-item>
          </n-descriptions>
        </n-tab-pane>
      </n-tabs>
      <template #footer>
        <n-space justify="end">
          <n-button @click="cur = null">{{ t('common.cancel') }}</n-button>
          <n-button type="primary" @click="save">{{ t('php.save') }}</n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>
  <TaskModal :task-id="task?.id ?? null" :title="task?.title ?? ''" @close="task = null; load()" @done="load" />
  <LogsModal :url="logs?.url ?? null" :title="logs?.title ?? ''" @close="logs = null" />
</template>

<style scoped>
.hint,
.sub {
  font-size: 12px;
  opacity: 0.65;
}
.hint {
  margin-top: 12px;
}
.mono :deep(textarea) {
  font-family: Menlo, Consolas, monospace;
  font-size: 12px;
}
</style>
