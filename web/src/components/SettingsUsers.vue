<script setup lang="ts">
// Panel sub-users (面板子用戶): operator accounts with per-module access.
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NCard, NCheckbox, NCheckboxGroup, NDataTable, NForm, NFormItem, NGi, NGrid, NInput, NModal, NPopconfirm, NSelect, NSpace,
  NSwitch, NTag, useMessage, type DataTableColumns,
} from 'naive-ui'
import { get, post, put, ApiError } from '../api/client'
import type { Site, User } from '../api/types'
import { datetime } from '../format'
import { useSession } from '../stores/session'

const { t, locale } = useI18n()
const msg = useMessage()
const session = useSession()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))
const users = ref<User[]>([])
const modules = ref<string[]>([])
const sites = ref<Site[]>([])
interface Form { id: number; username: string; password: string; is_admin: boolean; perms: string[]; sites: number[]; enabled: boolean; remark: string }
const f = ref<Form | null>(null)

async function load() {
  try {
    const r = await get<{ items: User[]; modules: string[] }>('/users')
    users.value = r.items
    modules.value = r.modules
  } catch (e) {
    fail(e)
  }
}
onMounted(async () => {
  load()
  sites.value = (await get<{ items: Site[] }>('/sites').catch(() => ({ items: [] as Site[] }))).items
})
const genPass = () => Array.from(crypto.getRandomValues(new Uint8Array(14)), (b) => 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789'[b % 56]).join('')
function openNew() {
  f.value = { id: 0, username: '', password: genPass(), is_admin: false, perms: ['website', 'files'], sites: [], enabled: true, remark: '' }
}
function edit(u: User) {
  f.value = { id: u.id, username: u.username, password: '', is_admin: u.is_admin, perms: [...(u.perms ?? [])], sites: [...(u.sites ?? [])], enabled: u.enabled ?? true, remark: u.remark ?? '' }
}
async function save() {
  const x = f.value!
  try {
    if (x.id) await put(`/users/${x.id}`, x)
    else await post('/users', x)
    msg.success(t('common.saved'))
    f.value = null
    load()
  } catch (e) {
    fail(e)
  }
}
async function del(u: User) {
  try {
    await post(`/users/${u.id}/delete`)
    load()
  } catch (e) {
    fail(e)
  }
}
// Site-scoped users cannot get the modules that reach every file on the server.
const scopedOff = ['files', 'terminal', 'docker', 'cron', 'processes', 'toolbox', 'database', 'appstore', 'backup']
const cols = computed<DataTableColumns<User>>(() => [
  { title: () => t('settings.username'), key: 'username', render: (u) => h('span', [h('b', u.username), u.id === session.user?.id ? h(NTag, { size: 'tiny', style: 'margin-left:6px' }, () => t('users.you')) : null]) },
  { title: () => t('users.role'), key: 'is_admin', width: 110, render: (u) => h(NTag, { size: 'small', type: u.is_admin ? 'warning' : 'info' }, () => (u.is_admin ? t('users.admin') : t('users.operator'))) },
  {
    title: () => t('users.perms'),
    key: 'perms',
    render: (u) => (u.is_admin ? t('users.all') : (u.perms ?? []).map((p) => t(`menu.${p}`)).join(', ') + ((u.sites ?? []).length ? ` · ${t('users.sites_n', { n: u.sites!.length })}` : '')),
  },
  { title: () => t('alert.enabled'), key: 'enabled', width: 80, render: (u) => h(NTag, { size: 'small', type: u.enabled ? 'success' : 'default' }, () => (u.enabled ? '✓' : '—')) },
  { title: '2FA', key: 'totp_enabled', width: 60, render: (u) => (u.totp_enabled ? '✓' : '—') },
  { title: () => t('common.created'), key: 'created_at', width: 170, render: (u) => datetime(u.created_at ?? 0, locale.value) },
  {
    title: '',
    key: 'a',
    width: 150,
    render: (u) =>
      h(NSpace, { size: 4 }, () => [
        h(NButton, { size: 'tiny', onClick: () => edit(u) }, () => t('common.edit')),
        u.id !== session.user?.id
          ? h(NPopconfirm, { onPositiveClick: () => del(u) }, { trigger: () => h(NButton, { size: 'tiny', type: 'error', quaternary: true }, () => t('common.delete')), default: () => t('common.confirm_delete') })
          : null,
      ]),
  },
])
</script>

<template>
  <n-alert type="info" :show-icon="false" style="margin-bottom: 12px">{{ t('users.intro') }}</n-alert>
  <n-space style="margin-bottom: 12px"><n-button type="primary" @click="openNew">{{ t('users.new') }}</n-button></n-space>
  <n-data-table size="small" :columns="cols" :data="users" :row-key="(u: User) => u.id" :scroll-x="900" />

  <n-modal :show="!!f" @update:show="(v: boolean) => !v && (f = null)">
    <n-card v-if="f" :title="f.id ? t('common.edit') : t('users.new')" style="width: min(680px, calc(100vw - 32px))" closable @close="f = null">
      <n-form label-placement="top">
        <n-form-item :label="t('settings.username')"><n-input v-model:value="f.username" /></n-form-item>
        <n-form-item :label="t('settings.new_password')" :feedback="f.id ? t('users.keep_password') : ''">
          <n-input v-model:value="f.password"><template #suffix><n-button text size="tiny" @click="f.password = genPass()">↻</n-button></template></n-input>
        </n-form-item>
        <n-space :size="24" style="margin-bottom: 12px">
          <n-space align="center"><n-switch v-model:value="f.is_admin" :disabled="f.id === session.user?.id" /> {{ t('users.admin') }}</n-space>
          <n-space align="center"><n-switch v-model:value="f.enabled" :disabled="f.id === session.user?.id" /> {{ t('alert.enabled') }}</n-space>
        </n-space>
        <template v-if="!f.is_admin">
          <n-form-item :label="t('users.sites')" :feedback="t('users.sites_hint')">
            <n-select v-model:value="f.sites" multiple filterable clearable :options="sites.map((s) => ({ label: s.domain, value: s.id }))" :placeholder="t('users.all_sites')" />
          </n-form-item>
          <n-form-item :label="t('users.perms')">
            <n-checkbox-group v-model:value="f.perms">
              <n-grid cols="2 s:3" responsive="screen" :y-gap="6">
                <n-gi v-for="m in modules" :key="m"><n-checkbox :value="m" :disabled="f.sites.length > 0 && scopedOff.includes(m)">{{ t(`menu.${m}`) }}</n-checkbox></n-gi>
              </n-grid>
            </n-checkbox-group>
          </n-form-item>
          <n-alert v-if="f.perms.includes('terminal') || f.perms.includes('files')" type="warning" :show-icon="false" style="margin-bottom: 12px">{{ t('users.root_warning') }}</n-alert>
        </template>
        <n-form-item :label="t('site.remark')"><n-input v-model:value="f.remark" /></n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="f = null">{{ t('common.cancel') }}</n-button>
          <n-button type="primary" :disabled="!f.username || (!f.id && f.password.length < 8)" @click="save">{{ t('common.save') }}</n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>
</template>
