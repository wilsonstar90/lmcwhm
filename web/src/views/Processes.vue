<script setup lang="ts">
import { computed, h, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NButton, NCard, NCode, NDataTable, NDynamicTags, NForm, NFormItem, NInput, NModal, NPopconfirm, NSpace, NTag, useMessage, type DataTableColumns,
} from 'naive-ui'
import { get, post, put, ApiError } from '../api/client'
import { bytes, datetime } from '../format'
import { CodeEditor } from '../components/code-editor'
import PathInput from '../components/PathInput.vue'

interface Proc { id: number; name: string; command: string; dir: string; user: string; env: string[]; remark: string; state: string; sub_state: string; pid: number; since: number; restarts: number; mem_bytes: number }

const { t, locale } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))
const procs = ref<Proc[]>([])
let timer: number | undefined
async function load() {
  try {
    procs.value = (await get<{ items: Proc[] }>('/processes')).items
  } catch (e) {
    fail(e)
  }
}
onMounted(() => {
  load()
  timer = window.setInterval(load, 10000)
})
onBeforeUnmount(() => clearInterval(timer))

const f = ref<Proc | null>(null)
const saving = ref(false)
async function save() {
  const p = f.value!
  saving.value = true
  try {
    if (p.id) await put(`/processes/${p.id}`, p)
    else await post('/processes', p)
    f.value = null
    load()
  } catch (e) {
    fail(e)
  } finally {
    saving.value = false
  }
}
async function act(p: Proc, a: string) {
  try {
    await post(`/processes/${p.id}/${a}`)
    setTimeout(load, 800)
  } catch (e) {
    fail(e)
  }
}
async function del(p: Proc) {
  await post(`/processes/${p.id}/delete`).catch(fail)
  load()
}
const logs = ref<{ name: string; log: string } | null>(null)
async function showLogs(p: Proc) {
  try {
    logs.value = { name: p.name, log: (await get<{ log: string }>(`/processes/${p.id}/logs?lines=300`)).log }
  } catch (e) {
    fail(e)
  }
}
const stateType = (p: Proc) => (p.state === 'active' ? 'success' : p.state === 'failed' ? 'error' : p.state === 'activating' ? 'warning' : 'default')
const cols = computed<DataTableColumns<Proc>>(() => [
  { title: () => t('common.status'), key: 'state', width: 130, render: (p) => h(NTag, { size: 'small', type: stateType(p) }, () => `${p.state || '—'} ${p.sub_state ? '· ' + p.sub_state : ''}`) },
  { title: () => t('proc.name'), key: 'name', render: (p) => h('div', [h('b', p.name), p.remark ? h('div', { style: 'font-size:12px;opacity:.65' }, p.remark) : null]) },
  { title: () => t('proc.command'), key: 'command', ellipsis: { tooltip: true }, render: (p) => h('code', p.command.split('\n')[0]) },
  { title: 'PID', key: 'pid', width: 80, render: (p) => p.pid || '—' },
  { title: () => t('home.memory'), key: 'mem_bytes', width: 100, render: (p) => (p.mem_bytes ? bytes(p.mem_bytes) : '—') },
  { title: () => t('proc.restarts'), key: 'restarts', width: 90 },
  { title: () => t('proc.since'), key: 'since', width: 170, render: (p) => (p.since ? datetime(p.since, locale.value) : '—') },
  {
    title: () => t('common.actions'),
    key: 'a',
    width: 300,
    render: (p) =>
      h(NSpace, { size: 4 }, () => [
        p.state === 'active'
          ? h(NButton, { size: 'tiny', onClick: () => act(p, 'stop') }, () => t('docker.stop'))
          : h(NButton, { size: 'tiny', onClick: () => act(p, 'start') }, () => t('docker.start')),
        h(NButton, { size: 'tiny', onClick: () => act(p, 'restart') }, () => t('docker.restart')),
        h(NButton, { size: 'tiny', onClick: () => showLogs(p) }, () => t('proc.logs')),
        h(NButton, { size: 'tiny', onClick: () => (f.value = { ...p, env: [...p.env] }) }, () => t('common.edit')),
        h(NPopconfirm, { onPositiveClick: () => del(p) }, { trigger: () => h(NButton, { size: 'tiny', type: 'error', quaternary: true }, () => t('common.delete')), default: () => t('common.confirm_delete') }),
      ]),
  },
])
</script>

<template>
  <n-space justify="space-between" style="margin-bottom: 12px">
    <n-button type="primary" @click="f = { id: 0, name: '', command: '', dir: '/', user: 'root', env: [], remark: '' } as unknown as Proc">{{ t('proc.new') }}</n-button>
    <span class="hint">{{ t('proc.intro') }}</span>
  </n-space>
  <n-data-table size="small" :columns="cols" :data="procs" :row-key="(p: Proc) => p.id" :scroll-x="1200" />

  <n-modal :show="!!f" @update:show="(v: boolean) => !v && (f = null)">
    <n-card v-if="f" :title="f.id ? t('common.edit') : t('proc.new')" style="width: min(620px, calc(100vw - 32px))" closable @close="f = null">
      <n-form label-placement="top">
        <n-form-item :label="t('proc.name')" :feedback="t('proc.name_hint')"><n-input v-model:value="f.name" placeholder="queue-worker" /></n-form-item>
        <n-form-item :label="t('proc.command')" :feedback="t('proc.command_hint')">
          <CodeEditor v-model="f.command" language="shell" height="140px" placeholder="exec php artisan queue:work" />
        </n-form-item>
        <n-space>
          <n-form-item :label="t('proc.dir')"><div style="width: 320px"><PathInput v-model:value="f.dir" /></div></n-form-item>
          <n-form-item :label="t('proc.user')"><n-input v-model:value="f.user" style="width: 140px" /></n-form-item>
        </n-space>
        <n-form-item :label="t('proc.env')" :feedback="t('proc.env_hint')"><n-dynamic-tags v-model:value="f.env" /></n-form-item>
        <n-form-item :label="t('site.remark')"><n-input v-model:value="f.remark" /></n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end"><n-button @click="f = null">{{ t('common.cancel') }}</n-button><n-button type="primary" :loading="saving" :disabled="!f.name || !f.command" @click="save">{{ t('proc.save_start') }}</n-button></n-space>
      </template>
    </n-card>
  </n-modal>

  <n-modal :show="!!logs" @update:show="(v: boolean) => !v && (logs = null)">
    <n-card v-if="logs" :title="`${t('proc.logs')} · ${logs.name}`" style="width: min(900px, calc(100vw - 32px))" closable @close="logs = null">
      <div style="max-height: 65vh; overflow: auto"><n-code :code="logs.log || '—'" word-wrap /></div>
    </n-card>
  </n-modal>
</template>

<style scoped>
.hint {
  font-size: 12px;
  opacity: 0.65;
}
</style>
