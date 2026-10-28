<script setup lang="ts">
// Website tamper protection (網站防篡改): lock or watch protected files.
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NCard, NCheckbox, NDataTable, NDynamicTags, NForm, NFormItem, NInput, NModal, NPopconfirm, NRadioButton, NRadioGroup,
  NSelect, NSpace, NSwitch, NTag, useMessage, type DataTableColumns,
} from 'naive-ui'
import { get, post, put, ApiError } from '../api/client'
import type { Site } from '../api/types'
import { datetime } from '../format'

interface Rule { id: number; site_id: number; path: string; mode: 'lock' | 'watch'; exts: string[]; exclude: string[]; restore: boolean; enabled: boolean; state: string; error?: string; files: number }
interface Ev { id: number; rule_id: number; path: string; op: string; action: string; created_at: number }

const { t, locale } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))
const rules = ref<Rule[]>([])
const events = ref<Ev[]>([])
const defaults = ref<{ exts: string[]; exclude: string[] }>({ exts: [], exclude: [] })
const sites = ref<Site[]>([])
const f = ref<Rule | null>(null)

async function load() {
  try {
    const r = await get<{ items: Rule[]; default_exts: string[]; default_exclude: string[] }>('/tamper')
    rules.value = r.items
    defaults.value = { exts: r.default_exts, exclude: r.default_exclude }
    events.value = (await get<{ items: Ev[] }>('/tamper/events')).items
  } catch (e) {
    fail(e)
  }
}
onMounted(async () => {
  load()
  sites.value = (await get<{ items: Site[] }>('/sites').catch(() => ({ items: [] as Site[] }))).items.filter((s) => !s.passthrough)
})

function openNew() {
  f.value = { id: 0, site_id: 0, path: '', mode: 'watch', exts: [...defaults.value.exts], exclude: [...defaults.value.exclude], restore: true, enabled: true, state: '', files: 0 }
}
function pickSite(id: number) {
  const s = sites.value.find((x) => x.id === id)
  if (s && f.value) Object.assign(f.value, { site_id: id, path: s.root })
}
async function save() {
  const x = f.value!
  try {
    const r = x.id ? await put<Rule>(`/tamper/${x.id}`, x) : await post<Rule>('/tamper', x)
    if (r.state === 'error') msg.warning(r.error ?? 'error')
    else msg.success(t('common.saved'))
    f.value = null
    load()
  } catch (e) {
    fail(e)
  }
}
async function toggle(r: Rule, on: boolean) {
  try {
    const x = await post<Rule>(`/tamper/${r.id}/enabled`, { on })
    if (x.state === 'error') msg.warning(x.error ?? 'error')
    load()
  } catch (e) {
    fail(e)
  }
}
async function call(r: Rule, path: string) {
  try {
    await post(`/tamper/${r.id}/${path}`)
    msg.success(t('common.done'))
    load()
  } catch (e) {
    fail(e)
  }
}
const stateType = (s: string) => (s === 'locked' || s === 'watching' ? 'success' : s === 'error' ? 'error' : 'default')
const cols = computed<DataTableColumns<Rule>>(() => [
  { title: () => t('tp.path'), key: 'path', render: (r) => h('div', [h('b', r.path), h('div', { class: 'sub' }, r.exclude.length ? `${t('tp.exclude')}: ${r.exclude.join(', ')}` : '')]) },
  { title: () => t('tp.mode'), key: 'mode', width: 130, render: (r) => t(`tp.modes.${r.mode}`) + (r.mode === 'watch' && r.restore ? ` + ${t('tp.restore_short')}` : '') },
  {
    title: () => t('common.status'),
    key: 'state',
    width: 140,
    render: (r) => h(NTag, { size: 'small', type: stateType(r.state), title: r.error }, () => t(`tp.state.${r.state}`) + (r.state === 'watching' ? ` · ${r.files}` : '')),
  },
  { title: () => t('alert.enabled'), key: 'enabled', width: 80, render: (r) => h(NSwitch, { size: 'small', value: r.enabled, 'onUpdate:value': (v: boolean) => toggle(r, v) }) },
  {
    title: () => t('common.actions'),
    key: 'a',
    width: 230,
    render: (r) =>
      h(NSpace, { size: 4 }, () => [
        h(NButton, { size: 'tiny', onClick: () => (f.value = JSON.parse(JSON.stringify(r))) }, () => t('common.edit')),
        r.mode === 'watch' && r.enabled ? h(NButton, { size: 'tiny', onClick: () => call(r, 'baseline') }, () => t('tp.baseline')) : null,
        h(NPopconfirm, { onPositiveClick: () => call(r, 'delete') }, { trigger: () => h(NButton, { size: 'tiny', type: 'error', quaternary: true }, () => t('common.delete')), default: () => t('common.confirm_delete') }),
      ]),
  },
])
const evCols = computed<DataTableColumns<Ev>>(() => [
  { title: () => t('common.time'), key: 'created_at', width: 170, render: (e) => datetime(e.created_at, locale.value) },
  { title: () => t('tp.file'), key: 'path', ellipsis: { tooltip: true } },
  { title: () => t('tp.op'), key: 'op', width: 100, render: (e) => t(`tp.ops.${e.op}`) },
  { title: () => t('tp.action'), key: 'action', width: 110, render: (e) => h(NTag, { size: 'small', type: e.action === 'failed' ? 'error' : e.action === 'logged' ? 'default' : 'success' }, () => t(`tp.actions.${e.action}`)) },
])
</script>

<template>
  <n-alert type="info" :show-icon="false" style="margin-bottom: 12px">{{ t('tp.intro') }}</n-alert>
  <n-space style="margin-bottom: 12px"><n-button type="primary" @click="openNew">{{ t('tp.new') }}</n-button></n-space>
  <n-data-table size="small" :columns="cols" :data="rules" :row-key="(r: Rule) => r.id" />
  <h4 style="margin: 20px 0 8px">{{ t('tp.events') }}</h4>
  <n-data-table size="small" :columns="evCols" :data="events" :row-key="(e: Ev) => e.id" :max-height="360" />

  <n-modal :show="!!f" @update:show="(v: boolean) => !v && (f = null)">
    <n-card v-if="f" :title="f.id ? t('common.edit') : t('tp.new')" style="width: min(620px, calc(100vw - 32px))" closable @close="f = null">
      <n-form label-placement="top">
        <n-form-item v-if="!f.id" :label="t('route.site')"><n-select :value="f.site_id || null" :options="sites.map((s) => ({ label: s.domain, value: s.id }))" filterable @update:value="pickSite" /></n-form-item>
        <n-form-item :label="t('tp.path')"><n-input v-model:value="f.path" placeholder="/www/wwwroot/example.com" /></n-form-item>
        <n-form-item :label="t('tp.mode')">
          <n-radio-group v-model:value="f.mode">
            <n-radio-button value="watch">{{ t('tp.modes.watch') }}</n-radio-button>
            <n-radio-button value="lock">{{ t('tp.modes.lock') }}</n-radio-button>
          </n-radio-group>
        </n-form-item>
        <n-alert type="default" :show-icon="false" style="margin-bottom: 12px">{{ t(`tp.mode_hint.${f.mode}`) }}</n-alert>
        <n-checkbox v-if="f.mode === 'watch'" v-model:checked="f.restore" style="margin-bottom: 12px">{{ t('tp.restore') }}</n-checkbox>
        <n-form-item :label="t('tp.exts')"><n-dynamic-tags v-model:value="f.exts" /></n-form-item>
        <n-form-item :label="t('tp.exclude')" :feedback="t('tp.exclude_hint')"><n-dynamic-tags v-model:value="f.exclude" /></n-form-item>
        <n-checkbox v-model:checked="f.enabled">{{ t('alert.enabled') }}</n-checkbox>
      </n-form>
      <template #footer>
        <n-space justify="end"><n-button @click="f = null">{{ t('common.cancel') }}</n-button><n-button type="primary" :disabled="!f.path" @click="save">{{ t('common.save') }}</n-button></n-space>
      </template>
    </n-card>
  </n-modal>
</template>

<style scoped>
.sub {
  font-size: 12px;
  opacity: 0.65;
}
</style>
