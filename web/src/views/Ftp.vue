<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NCard, NDataTable, NForm, NFormItem, NInput, NModal, NPopconfirm, NRadioButton, NRadioGroup, NSelect, NSpace,
  NSwitch, NTag, useMessage, type DataTableColumns,
} from 'naive-ui'
import { get, patch, post, ApiError } from '../api/client'
import type { Site } from '../api/types'
import { datetime } from '../format'
import TaskModal from '../components/TaskModal.vue'
import PathInput from '../components/PathInput.vue'

interface Account { id: number; kind: 'ftp' | 'sftp'; username: string; dir: string; site_id: number; enabled: boolean; remark: string; created_at: number }
interface Status { enabled: boolean; state: string; public_ip: string; ports: string; sftp_ready: boolean }

const { t, locale } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))
const items = ref<Account[]>([])
const status = ref<Status | null>(null)
const sites = ref<Site[]>([])
const task = ref<{ id: string; title: string } | null>(null)

async function load() {
  try {
    const r = await get<{ items: Account[]; status: Status }>('/ftp')
    items.value = r.items
    status.value = r.status
  } catch (e) {
    fail(e)
  }
}
onMounted(async () => {
  load()
  sites.value = (await get<{ items: Site[] }>('/sites').catch(() => ({ items: [] as Site[] }))).items
})

async function toggleFTP(on: boolean) {
  try {
    const r = await post<{ task_id: string }>('/ftp/enabled', { on })
    task.value = { id: r.task_id, title: 'FTP' }
  } catch (e) {
    fail(e)
  }
}

const genPass = () => Array.from(crypto.getRandomValues(new Uint8Array(14)), (b) => 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789'[b % 56]).join('')
const f = ref<{ kind: 'ftp' | 'sftp'; username: string; password: string; site_id: number | null; dir: string; remark: string } | null>(null)
function openNew() {
  f.value = { kind: 'sftp', username: '', password: genPass(), site_id: null, dir: '', remark: '' }
}
function pickSite(id: number) {
  const s = sites.value.find((x) => x.id === id)
  if (!s || !f.value) return
  f.value.site_id = id
  f.value.dir = s.root
  if (!f.value.username) f.value.username = s.domain.replace(/[^a-z0-9]/g, '').slice(0, 20) || 'web'
}
const created = ref<{ username: string; password: string; kind: string } | null>(null)
async function create() {
  const x = f.value!
  try {
    await post('/ftp/accounts', { ...x, site_id: x.site_id ?? 0 })
    created.value = { username: x.username, password: x.password, kind: x.kind }
    f.value = null
    load()
  } catch (e) {
    fail(e)
  }
}
async function setEnabled(a: Account, on: boolean) {
  try {
    await patch(`/ftp/accounts/${a.id}`, { enabled: on })
    a.enabled = on
  } catch (e) {
    fail(e)
  }
}
const pw = ref<{ a: Account; value: string } | null>(null)
async function showPassword(a: Account) {
  pw.value = { a, value: (await get<{ password: string }>(`/ftp/accounts/${a.id}/password`)).password }
}
async function changePassword() {
  try {
    await patch(`/ftp/accounts/${pw.value!.a.id}`, { password: pw.value!.value })
    msg.success(t('common.saved'))
    pw.value = null
  } catch (e) {
    fail(e)
  }
}
async function del(a: Account) {
  await post(`/ftp/accounts/${a.id}/delete`).catch(fail)
  load()
}
const host = computed(() => status.value?.public_ip || location.hostname)
const cols = computed<DataTableColumns<Account>>(() => [
  { title: () => t('ftp.kind'), key: 'kind', width: 80, render: (a) => h(NTag, { size: 'small', type: a.kind === 'sftp' ? 'success' : 'info' }, () => a.kind.toUpperCase()) },
  { title: () => t('ftp.username'), key: 'username', render: (a) => h('b', a.username) },
  { title: () => t('ftp.dir'), key: 'dir', ellipsis: { tooltip: true } },
  { title: () => t('ftp.connect'), key: 'c', render: (a) => h('code', a.kind === 'sftp' ? `sftp://${a.username}@${host.value}` : `ftp://${a.username}@${host.value}`) },
  { title: () => t('alert.enabled'), key: 'enabled', width: 80, render: (a) => h(NSwitch, { size: 'small', value: a.enabled, 'onUpdate:value': (v: boolean) => setEnabled(a, v) }) },
  { title: () => t('common.created'), key: 'created_at', width: 170, render: (a) => datetime(a.created_at, locale.value) },
  {
    title: () => t('common.actions'),
    key: 'x',
    width: 170,
    render: (a) =>
      h(NSpace, { size: 4 }, () => [
        h(NButton, { size: 'tiny', onClick: () => showPassword(a) }, () => t('ftp.password')),
        h(NPopconfirm, { onPositiveClick: () => del(a) }, { trigger: () => h(NButton, { size: 'tiny', type: 'error', quaternary: true }, () => t('common.delete')), default: () => t('common.confirm_delete') }),
      ]),
  },
])
</script>

<template>
  <n-card v-if="status" size="small" style="margin-bottom: 16px">
    <n-space justify="space-between" align="center">
      <n-space align="center">
        <b>FTP (Pure-FTPd)</b>
        <n-switch :value="status.enabled" @update:value="toggleFTP" />
        <n-tag v-if="status.enabled" size="small" :type="status.state === 'running' ? 'success' : 'error'">{{ status.state || '—' }}</n-tag>
        <span class="hint">{{ t('ftp.ftp_hint', { ports: status.ports, ip: status.public_ip || '?' }) }}</span>
      </n-space>
      <span class="hint">{{ t('ftp.sftp_hint') }}</span>
    </n-space>
  </n-card>
  <n-space style="margin-bottom: 12px"><n-button type="primary" @click="openNew">{{ t('ftp.new') }}</n-button></n-space>
  <n-data-table size="small" :columns="cols" :data="items" :row-key="(a: Account) => a.id" :scroll-x="1000" />

  <n-modal :show="!!f" @update:show="(v: boolean) => !v && (f = null)">
    <n-card v-if="f" :title="t('ftp.new')" style="width: min(560px, calc(100vw - 32px))" closable @close="f = null">
      <n-form label-placement="top">
        <n-form-item :label="t('ftp.kind')">
          <n-radio-group v-model:value="f.kind">
            <n-radio-button value="sftp">SFTP ({{ t('ftp.recommended') }})</n-radio-button>
            <n-radio-button value="ftp">FTP</n-radio-button>
          </n-radio-group>
        </n-form-item>
        <n-alert v-if="f.kind === 'ftp' && !status?.enabled" type="warning" :show-icon="false" style="margin-bottom: 12px">{{ t('ftp.ftp_off') }}</n-alert>
        <n-form-item :label="t('route.site')"><n-select :value="f.site_id" :options="sites.filter((s) => !s.passthrough).map((s) => ({ label: s.domain, value: s.id }))" filterable @update:value="pickSite" /></n-form-item>
        <n-form-item :label="t('ftp.dir')"><PathInput v-model:value="f.dir" placeholder="/www/wwwroot/example.com" /></n-form-item>
        <n-form-item :label="t('ftp.username')" :feedback="t('ftp.username_hint')"><n-input v-model:value="f.username" /></n-form-item>
        <n-form-item :label="t('ftp.password')"><n-input v-model:value="f.password"><template #suffix><n-button text size="tiny" @click="f.password = genPass()">↻</n-button></template></n-input></n-form-item>
        <n-form-item :label="t('site.remark')"><n-input v-model:value="f.remark" /></n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end"><n-button @click="f = null">{{ t('common.cancel') }}</n-button><n-button type="primary" :disabled="!f.username || !f.dir" @click="create">{{ t('common.create') }}</n-button></n-space>
      </template>
    </n-card>
  </n-modal>

  <n-modal :show="!!created" @update:show="(v: boolean) => !v && (created = null)">
    <n-card v-if="created" :title="t('ftp.created')" style="width: min(480px, calc(100vw - 32px))" closable @close="created = null">
      <p>{{ t('ftp.host') }}: <code>{{ host }}</code> ({{ created.kind === 'sftp' ? t('ftp.port_ssh') : t('ftp.port_21') }})</p>
      <p>{{ t('ftp.username') }}: <code>{{ created.username }}</code></p>
      <p>{{ t('ftp.password') }}: <code>{{ created.password }}</code></p>
    </n-card>
  </n-modal>

  <n-modal :show="!!pw" @update:show="(v: boolean) => !v && (pw = null)">
    <n-card v-if="pw" :title="`${t('ftp.password')} · ${pw.a.username}`" style="width: min(440px, calc(100vw - 32px))" closable @close="pw = null">
      <n-input v-model:value="pw.value"><template #suffix><n-button text size="tiny" @click="pw.value = genPass()">↻</n-button></template></n-input>
      <template #footer><n-space justify="end"><n-button type="primary" @click="changePassword">{{ t('ftp.change_password') }}</n-button></n-space></template>
    </n-card>
  </n-modal>
  <TaskModal :task-id="task?.id ?? null" :title="task?.title ?? ''" @close="task = null; load()" @done="load" />
</template>

<style scoped>
.hint {
  font-size: 12px;
  opacity: 0.65;
}
</style>
