<script setup lang="ts">
import { computed, h, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NDataTable, NDescriptions, NDescriptionsItem, NForm, NFormItem, NInput, NPopconfirm, NSelect, NSpace, NSpin, NSwitch,
  NTag, useMessage, type DataTableColumns,
} from 'naive-ui'
import { get, post, ApiError } from '../api/client'
import type { InstalledApp, Site } from '../api/types'

interface Plugin { name: string; title: string; status: string; version: string; update: string; update_version: string }
interface Info { version: string; site_url: string; core_updates: string[]; plugins: Plugin[]; themes: Plugin[]; file_edit: boolean; debug: boolean; admins: string[] }

const props = defineProps<{ site: Site }>()
const emit = defineEmits<{ task: [id: string, title: string] }>()
const { t, locale } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))
const isWP = ref<boolean | null>(null)
const info = ref<Info | null>(null)
const loading = ref(false)
const engines = ref<InstalledApp[]>([])

async function load() {
  isWP.value = null
  info.value = null
  const list = (await get<{ items: { site_id: number }[] }>('/wordpress')).items
  isWP.value = list.some((x) => x.site_id === props.site.id)
  if (isWP.value) {
    loading.value = true
    try {
      info.value = await get<Info>(`/wordpress/${props.site.id}`)
    } catch (e) {
      fail(e)
    } finally {
      loading.value = false
    }
  } else {
    engines.value = (await get<{ items: InstalledApp[] }>('/apps')).items.filter((a) => a.app === 'mysql' || a.app === 'mariadb')
    if (engines.value.length && !form.value.db_app_id) form.value.db_app_id = engines.value[0].id
  }
}
onMounted(() => load().catch(fail))
watch(() => props.site.id, () => load().catch(fail))

const wpLocale = () => ({ 'zh-HK': 'zh_HK', 'zh-TW': 'zh_TW', 'zh-CN': 'zh_CN', ja: 'ja', en: 'en_US' })[locale.value] ?? 'en_US'
const genPass = () => Array.from(crypto.getRandomValues(new Uint8Array(16)), (b) => 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789'[b % 56]).join('')
const form = ref({ title: props.site.domain, admin_user: 'admin', admin_pass: genPass(), admin_email: '', locale: wpLocale(), db_app_id: 0 })
async function install() {
  try {
    const r = await post<{ task_id: string }>(`/wordpress/install/${props.site.id}`, form.value)
    emit('task', r.task_id, `WordPress · ${props.site.domain}`)
  } catch (e) {
    fail(e)
  }
}
async function action(a: string, extra: Record<string, unknown> = {}) {
  try {
    const r = await post<{ task_id: string }>(`/wordpress/${props.site.id}/action`, { action: a, ...extra })
    emit('task', r.task_id, `WordPress · ${t(`wp.actions.${a}`)}`)
  } catch (e) {
    fail(e)
  }
}
const newPass = ref('')
const adminName = ref<string | null>(null)
const pluginCols = computed<DataTableColumns<Plugin>>(() => [
  { title: () => t('wp.plugin'), key: 'title', render: (p) => h('div', [h('b', p.title || p.name), h('div', { style: 'font-size:12px;opacity:.6' }, p.name)]) },
  { title: () => t('wp.version'), key: 'version', width: 140, render: (p) => (p.update === 'available' ? h(NTag, { size: 'small', type: 'warning' }, () => `${p.version} → ${p.update_version}`) : p.version) },
  { title: () => t('common.status'), key: 'status', width: 100, render: (p) => h(NTag, { size: 'small', type: p.status === 'active' ? 'success' : 'default' }, () => p.status) },
  {
    title: () => t('common.actions'),
    key: 'a',
    width: 210,
    render: (p) =>
      h(NSpace, { size: 4 }, () => [
        p.update === 'available' ? h(NButton, { size: 'tiny', type: 'warning', onClick: () => action('plugin_update', { name: p.name }) }, () => t('wp.update')) : null,
        p.status === 'active'
          ? h(NButton, { size: 'tiny', onClick: () => action('plugin_deactivate', { name: p.name }) }, () => t('wp.deactivate'))
          : h(NButton, { size: 'tiny', onClick: () => action('plugin_activate', { name: p.name }) }, () => t('wp.activate')),
        p.status !== 'active' ? h(NPopconfirm, { onPositiveClick: () => action('plugin_delete', { name: p.name }) }, { trigger: () => h(NButton, { size: 'tiny', type: 'error', quaternary: true }, () => t('common.delete')), default: () => t('common.confirm_delete') }) : null,
      ]),
  },
])
const themeCols = computed<DataTableColumns<Plugin>>(() => [
  { title: () => t('wp.theme'), key: 'title', render: (p) => p.title || p.name },
  { title: () => t('wp.version'), key: 'version', width: 140, render: (p) => (p.update === 'available' ? h(NTag, { size: 'small', type: 'warning' }, () => `${p.version} → ${p.update_version}`) : p.version) },
  { title: () => t('common.status'), key: 'status', width: 100 },
  { title: '', key: 'a', width: 120, render: (p) => (p.status !== 'active' ? h(NButton, { size: 'tiny', onClick: () => action('theme_activate', { name: p.name }) }, () => t('wp.activate')) : null) },
])
const updates = computed(() => (info.value ? info.value.plugins.filter((p) => p.update === 'available').length + info.value.themes.filter((p) => p.update === 'available').length : 0))
</script>

<template>
  <n-spin :show="isWP === null || loading">
    <template v-if="isWP === false">
      <n-alert type="info" :show-icon="false" style="margin-bottom: 12px">{{ t('wp.install_intro') }}</n-alert>
      <n-alert v-if="!engines.length" type="warning" :show-icon="false">{{ t('wp.need_mysql') }}</n-alert>
      <n-form v-else label-placement="left" label-width="auto">
        <n-form-item :label="t('wp.site_title')"><n-input v-model:value="form.title" /></n-form-item>
        <n-form-item :label="t('wp.admin_user')"><n-input v-model:value="form.admin_user" /></n-form-item>
        <n-form-item :label="t('wp.admin_pass')"><n-input v-model:value="form.admin_pass" /></n-form-item>
        <n-form-item :label="t('wp.admin_email')"><n-input v-model:value="form.admin_email" placeholder="you@example.com" /></n-form-item>
        <n-form-item :label="t('wp.language')"><n-select v-model:value="form.locale" filterable tag :options="['en_US', 'zh_HK', 'zh_TW', 'zh_CN', 'ja'].map((v) => ({ label: v, value: v }))" style="width: 160px" /></n-form-item>
        <n-form-item :label="t('wp.db_engine')"><n-select v-model:value="form.db_app_id" :options="engines.map((e) => ({ label: `${e.name} ${e.version}`, value: e.id }))" style="width: 220px" /></n-form-item>
        <n-button type="primary" :disabled="!form.admin_email || form.admin_pass.length < 8" @click="install">{{ t('wp.install') }}</n-button>
      </n-form>
    </template>
    <template v-else-if="info">
      <n-descriptions :column="2" label-placement="left" bordered size="small">
        <n-descriptions-item :label="t('wp.version')">
          {{ info.version }} <n-tag v-if="info.core_updates.length" size="small" type="warning">→ {{ info.core_updates[0] }}</n-tag>
        </n-descriptions-item>
        <n-descriptions-item label="URL"><a :href="info.site_url + '/wp-admin/'" target="_blank" rel="noopener">{{ info.site_url }}/wp-admin/</a></n-descriptions-item>
        <n-descriptions-item :label="t('wp.admins')">{{ info.admins.join(', ') }}</n-descriptions-item>
        <n-descriptions-item :label="t('wp.updates')">{{ updates }}</n-descriptions-item>
      </n-descriptions>
      <n-space style="margin: 12px 0">
        <n-button :type="info.core_updates.length ? 'warning' : 'default'" @click="action('core_update')">{{ t('wp.actions.core_update') }}</n-button>
        <n-button @click="action('plugins_update')">{{ t('wp.actions.plugins_update') }}</n-button>
        <n-button @click="action('themes_update')">{{ t('wp.actions.themes_update') }}</n-button>
        <n-button @click="action('verify')">{{ t('wp.actions.verify') }}</n-button>
        <n-button @click="action('cache_flush')">{{ t('wp.actions.cache_flush') }}</n-button>
        <n-button quaternary @click="load">{{ t('common.refresh') }}</n-button>
      </n-space>
      <n-space vertical style="margin-bottom: 12px">
        <n-space align="center"><n-switch :value="!info.file_edit" @update:value="(v: boolean) => action('file_edit', { on: !v })" /> {{ t('wp.no_file_edit') }}</n-space>
        <n-space align="center"><n-switch :value="!!site.advanced?.block_xmlrpc" disabled /> {{ t('wp.xmlrpc_hint') }}</n-space>
        <n-space align="center"><n-switch :value="info.debug" @update:value="(v: boolean) => action('debug', { on: v })" /> WP_DEBUG</n-space>
        <n-space align="center">
          {{ t('wp.reset_password') }}
          <n-select v-model:value="adminName" :options="info.admins.map((a) => ({ label: a, value: a }))" style="width: 160px" size="small" />
          <n-input v-model:value="newPass" size="small" style="width: 200px" :placeholder="t('wp.new_password')" />
          <n-button size="small" :disabled="!adminName || newPass.length < 8" @click="action('admin_password', { name: adminName, password: newPass }); newPass = ''">{{ t('common.save') }}</n-button>
        </n-space>
      </n-space>
      <n-data-table size="small" :columns="pluginCols" :data="info.plugins" :row-key="(p: Plugin) => p.name" />
      <n-data-table size="small" :columns="themeCols" :data="info.themes" :row-key="(p: Plugin) => p.name" style="margin-top: 12px" />
    </template>
  </n-spin>
</template>
