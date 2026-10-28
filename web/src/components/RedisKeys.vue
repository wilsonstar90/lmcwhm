<script setup lang="ts">
import { computed, h, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NButton, NCard, NDataTable, NForm, NFormItem, NInput, NInputNumber, NModal, NPopconfirm, NSpace, NTab, NTabs, NTag, useMessage,
  type DataTableColumns, type DataTableRowKey,
} from 'naive-ui'
import { get, post, ApiError } from '../api/client'
import type { InstalledApp } from '../api/types'

// Key browser of one Redis instance (aaPanel Redis 管理).
const props = defineProps<{ app: InstalledApp }>()
const { t } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))

interface RedisKey { key: string; type: string; ttl: number; length: number; preview: string; binary?: boolean }
interface RedisValue extends RedisKey { value?: string; items?: string[]; fields?: string[][] }

const dbs = ref<{ index: number; keys: number }[]>([])
const db = ref(0)
const keys = ref<RedisKey[]>([])
const cursor = ref('0')
const total = ref(0)
const match = ref('')
const loading = ref(false)
const checked = ref<DataTableRowKey[]>([])
const base = computed(() => `/apps/${props.app.id}/redis`)

async function loadDBs() {
  try {
    dbs.value = (await get<{ items: { index: number; keys: number }[] }>(`${base.value}/dbs`)).items
  } catch (e) {
    fail(e)
  }
}
async function scan(more = false) {
  loading.value = true
  try {
    const q = new URLSearchParams({ db: String(db.value), cursor: more ? cursor.value : '0', match: match.value.trim(), count: '50' })
    const r = await get<{ cursor: string; total: number; keys: RedisKey[] }>(`${base.value}/keys?${q}`)
    keys.value = more ? [...keys.value, ...r.keys.filter((k) => !keys.value.some((x) => x.key === k.key))] : r.keys
    cursor.value = r.cursor
    total.value = r.total
    if (!more) checked.value = []
  } catch (e) {
    fail(e)
  } finally {
    loading.value = false
  }
}
function reload() {
  loadDBs()
  scan()
}
watch(() => props.app.id, reload, { immediate: true })
watch(db, () => scan())

function ttl(s: number) {
  if (s < 0) return t('rk.forever')
  const d = Math.floor(s / 86400)
  const hh = Math.floor((s % 86400) / 3600)
  const m = Math.floor((s % 3600) / 60)
  return t('rk.ttl_fmt', { d, h: hh, m, s: s % 60 })
}

// ---- edit / add ----
const edit = ref<{ isNew: boolean; key: string; type: string; value: string; ttl: number; view?: RedisValue } | null>(null)
function add() {
  edit.value = { isNew: true, key: '', type: 'string', value: '', ttl: 0 }
}
async function open(k: RedisKey) {
  try {
    const v = await get<RedisValue>(`${base.value}/key?${new URLSearchParams({ db: String(db.value), key: k.key })}`)
    edit.value = { isNew: false, key: k.key, type: v.type, value: v.value ?? '', ttl: v.ttl > 0 ? v.ttl : 0, view: v }
  } catch (e) {
    fail(e)
  }
}
async function save() {
  const e = edit.value
  if (!e) return
  try {
    if (e.type === 'string' && !e.view?.binary) {
      await post(`${base.value}/key`, { db: db.value, key: e.key, value: e.value, ttl: e.ttl })
    } else {
      await post(`${base.value}/expire`, { db: db.value, key: e.key, ttl: e.ttl })
    }
    edit.value = null
    msg.success(t('common.saved'))
    reload()
  } catch (err) {
    fail(err)
  }
}
async function remove(list: string[]) {
  try {
    await post(`${base.value}/delete`, { db: db.value, keys: list })
    keys.value = keys.value.filter((k) => !list.includes(k.key))
    checked.value = []
    loadDBs()
  } catch (e) {
    fail(e)
  }
}
async function flush() {
  try {
    await post(`${base.value}/flush`, { db: db.value })
    reload()
  } catch (e) {
    fail(e)
  }
}

const cols = computed<DataTableColumns<RedisKey>>(() => [
  { type: 'selection' },
  { title: () => t('rk.key'), key: 'key', minWidth: 260, render: (k) => h('span', { class: 'mono', style: 'word-break: break-all' }, k.key) },
  { title: () => t('rk.value'), key: 'preview', minWidth: 240, ellipsis: { tooltip: true }, render: (k) => (k.binary ? h(NTag, { size: 'small' }, () => t('rk.binary')) : k.preview) },
  { title: () => t('rk.type'), key: 'type', width: 90 },
  { title: () => t('rk.length'), key: 'length', width: 100 },
  { title: () => t('rk.ttl'), key: 'ttl', width: 170, render: (k) => ttl(k.ttl) },
  {
    title: '', key: 'a', width: 130,
    render: (k) =>
      h(NSpace, { size: 6 }, () => [
        h(NButton, { size: 'tiny', onClick: () => open(k) }, () => t('rk.edit')),
        h(NPopconfirm, { onPositiveClick: () => remove([k.key]) }, {
          trigger: () => h(NButton, { size: 'tiny', ghost: true, type: 'error' }, () => t('common.delete')),
          default: () => t('rk.delete_confirm', { n: 1 }),
        }),
      ]),
  },
])
const viewRows = computed(() => {
  const v = edit.value?.view
  if (!v) return []
  if (v.fields) return v.fields.map(([a, b]) => ({ a, b }))
  return (v.items ?? []).map((a) => ({ a, b: '' }))
})
const viewCols = computed<DataTableColumns<{ a: string; b: string }>>(() => {
  const ty = edit.value?.type
  if (ty === 'hash') return [{ title: () => t('rk.field'), key: 'a' }, { title: () => t('rk.value'), key: 'b' }]
  if (ty === 'zset') return [{ title: () => t('rk.member'), key: 'a' }, { title: () => t('rk.score'), key: 'b', width: 120 }]
  return [{ title: () => t('rk.value'), key: 'a' }]
})
</script>

<template>
  <n-space justify="space-between" align="center" style="margin-bottom: 8px">
    <n-space>
      <n-button type="primary" @click="add">{{ t('rk.add') }}</n-button>
      <n-popconfirm @positive-click="flush">
        <template #trigger><n-button>{{ t('rk.flush') }}</n-button></template>
        {{ t('rk.flush_confirm', { db: db }) }}
      </n-popconfirm>
    </n-space>
    <n-space>
      <n-input v-model:value="match" size="small" clearable :placeholder="t('rk.search')" style="width: 220px" @keyup.enter="scan()" @clear="scan()" />
      <n-button size="small" @click="scan()">{{ t('rk.find') }}</n-button>
      <n-button size="small" @click="reload">{{ t('common.refresh') }}</n-button>
    </n-space>
  </n-space>
  <n-tabs v-model:value="db" type="card" size="small" style="margin-bottom: 8px">
    <n-tab v-for="d in dbs" :key="d.index" :name="d.index">DB{{ d.index }} [{{ d.keys }}]</n-tab>
  </n-tabs>
  <n-data-table
    v-model:checked-row-keys="checked" size="small" :columns="cols" :data="keys" :loading="loading"
    :row-key="(k: RedisKey) => k.key" :scroll-x="1100" :max-height="560"
  />
  <n-space justify="space-between" align="center" style="margin-top: 8px">
    <n-popconfirm :disabled="!checked.length" @positive-click="remove(checked.map(String))">
      <template #trigger><n-button size="small" :disabled="!checked.length">{{ t('rk.delete_selected', { n: checked.length }) }}</n-button></template>
      {{ t('rk.delete_confirm', { n: checked.length }) }}
    </n-popconfirm>
    <n-space align="center">
      <span class="hint">{{ t('rk.shown', { n: keys.length, total }) }}</span>
      <n-button size="small" :disabled="cursor === '0'" :loading="loading" @click="scan(true)">{{ t('rk.more') }}</n-button>
    </n-space>
  </n-space>

  <n-modal :show="!!edit" @update:show="(x: boolean) => !x && (edit = null)">
    <n-card v-if="edit" :title="edit.isNew ? t('rk.add') : `${t('rk.edit')} · ${edit.type}`" style="width: min(640px, calc(100vw - 32px))" closable @close="edit = null">
      <n-form label-placement="top">
        <n-form-item :label="t('rk.key')"><n-input v-model:value="edit.key" :disabled="!edit.isNew" /></n-form-item>
        <n-form-item v-if="edit.type === 'string'" :label="t('rk.value')">
          <n-input v-if="!edit.view?.binary" v-model:value="edit.value" type="textarea" :rows="8" class="mono" />
          <n-tag v-else>{{ t('rk.binary_readonly') }}</n-tag>
        </n-form-item>
        <n-data-table v-else size="small" :columns="viewCols" :data="viewRows" :max-height="300" style="margin-bottom: 12px" />
        <n-form-item :label="t('rk.ttl_sec')" :feedback="t('rk.ttl_hint')">
          <n-input-number v-model:value="edit.ttl" :min="0" style="width: 200px" />
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="edit = null">{{ t('common.cancel') }}</n-button>
          <n-button type="primary" :disabled="!edit.key" @click="save">{{ t('common.save') }}</n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>
</template>

<style scoped>
.hint {
  font-size: 12px;
  opacity: 0.65;
}
.mono,
:deep(.mono textarea) {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12.5px;
}
</style>
