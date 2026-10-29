<script setup lang="ts">
import { h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NDataTable, NSpace, NTag, NModal, NCard, NForm, NFormItem, NInput, NCheckbox, NPopconfirm, useMessage, type DataTableColumns } from 'naive-ui'
import { get, post, put, ApiError } from '../api/client'
import type { ComposeProject } from '../api/types'
import LogsModal from './LogsModal.vue'
import { CodeEditor } from './code-editor'

const emit = defineEmits<{ task: [id: string, title: string] }>()
const { t } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))

const items = ref<ComposeProject[]>([])
const loading = ref(false)
const logs = ref<{ url: string; title: string } | null>(null)
const edit = ref<{ name: string; compose: string; env: string; isNew: boolean; up: boolean } | null>(null)
const delVolumes = ref(false)

const sample = `services:
  app:
    image: nginx:stable
    restart: unless-stopped
    ports:
      - "127.0.0.1:8081:80"
`

async function load() {
  loading.value = true
  try {
    items.value = (await get<{ items: ComposeProject[] }>('/docker/compose')).items
  } catch (e) {
    fail(e)
  } finally {
    loading.value = false
  }
}
onMounted(load)
defineExpose({ load })

async function openEdit(p?: ComposeProject) {
  if (!p) {
    edit.value = { name: '', compose: sample, env: '', isNew: true, up: true }
    return
  }
  try {
    const f = await get<{ compose: string; env: string }>(`/docker/compose/${p.name}`)
    edit.value = { name: p.name, ...f, isNew: false, up: false }
  } catch (e) {
    fail(e)
  }
}

async function save() {
  const e = edit.value!
  try {
    if (e.isNew) {
      const r = await post<{ task_id?: string }>('/docker/compose', { name: e.name, compose: e.compose, env: e.env, up: e.up })
      if (r.task_id) emit('task', r.task_id, `compose up · ${e.name}`)
    } else {
      await put(`/docker/compose/${e.name}`, { compose: e.compose, env: e.env })
      msg.success(t('common.saved'))
    }
    edit.value = null
    load()
  } catch (err) {
    fail(err)
  }
}

async function action(p: ComposeProject, a: string) {
  try {
    const r = await post<{ task_id: string }>(`/docker/compose/${p.name}/${a}`)
    emit('task', r.task_id, `compose ${a} · ${p.name}`)
  } catch (e) {
    fail(e)
  }
}

async function remove(p: ComposeProject) {
  try {
    const r = await post<{ task_id: string }>(`/docker/compose/${p.name}/delete`, { volumes: delVolumes.value })
    delVolumes.value = false
    emit('task', r.task_id, `${t('common.delete')} · ${p.name}`)
  } catch (e) {
    fail(e)
  }
}

const columns: DataTableColumns<ComposeProject> = [
  { title: () => t('docker.project'), key: 'name', render: (p) => h('div', [h('b', p.name), h('div', { style: 'font-size:12px;opacity:.6' }, p.dir)]) },
  {
    title: () => t('common.status'),
    key: 'status',
    width: 200,
    render: (p) =>
      h(NTag, { size: 'small', type: p.running && p.running === p.services ? 'success' : p.running ? 'warning' : 'default' }, () =>
        t('docker.services_running', { n: p.running, total: p.services }),
      ),
  },
  {
    title: '',
    key: 'actions',
    width: 380,
    render: (p) =>
      h(NSpace, { size: 6 }, () => [
        h(NButton, { size: 'tiny', type: 'primary', onClick: () => action(p, 'up') }, () => 'up'),
        h(NButton, { size: 'tiny', onClick: () => action(p, 'down') }, () => 'down'),
        h(NButton, { size: 'tiny', onClick: () => action(p, 'restart') }, () => 'restart'),
        h(NButton, { size: 'tiny', onClick: () => action(p, 'pull') }, () => 'pull'),
        h(NButton, { size: 'tiny', onClick: () => openEdit(p) }, () => t('docker.edit')),
        h(NButton, { size: 'tiny', onClick: () => (logs.value = { url: `/docker/compose/${p.name}/logs`, title: p.name }) }, () => t('app.logs')),
        h(
          NPopconfirm,
          { onPositiveClick: () => remove(p), onAfterLeave: () => (delVolumes.value = false) },
          {
            trigger: () => h(NButton, { size: 'tiny', type: 'error', ghost: true }, () => t('common.delete')),
            default: () =>
              h('div', [
                h('div', t('docker.project_delete_confirm', { name: p.name })),
                h(NCheckbox, { checked: delVolumes.value, 'onUpdate:checked': (v: boolean) => (delVolumes.value = v), style: 'margin-top:8px' }, () =>
                  t('docker.delete_volumes'),
                ),
              ]),
          },
        ),
      ]),
  },
]
</script>

<template>
  <n-space justify="space-between" style="margin-bottom: 12px">
    <n-button type="primary" @click="openEdit()">{{ t('docker.compose_create') }}</n-button>
    <n-button size="small" @click="load">{{ t('common.refresh') }}</n-button>
  </n-space>
  <n-data-table size="small" :columns="columns" :data="items" :loading="loading" :row-key="(r: ComposeProject) => r.name" :scroll-x="800" />

  <n-modal :show="!!edit" @update:show="(v: boolean) => !v && (edit = null)">
    <n-card v-if="edit" :title="edit.isNew ? t('docker.compose_create') : edit.name" style="width: min(860px, calc(100vw - 32px))" closable @close="edit = null">
      <n-form label-placement="top">
        <n-form-item v-if="edit.isNew" :label="t('docker.project')" :feedback="t('docker.project_hint')">
          <n-input v-model:value="edit.name" placeholder="blog" />
        </n-form-item>
        <n-form-item label="docker-compose.yml">
          <CodeEditor v-model="edit.compose" language="docker-compose.yml" height="360px" @save="save" />
        </n-form-item>
        <n-form-item label=".env">
          <CodeEditor v-model="edit.env" language=".env" height="120px" placeholder="KEY=value" @save="save" />
        </n-form-item>
        <n-checkbox v-if="edit.isNew" v-model:checked="edit.up">{{ t('docker.start_now') }}</n-checkbox>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="edit = null">{{ t('common.cancel') }}</n-button>
          <n-button type="primary" :disabled="!edit.name || !edit.compose" @click="save">{{ t('common.save') }}</n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>
  <LogsModal :url="logs?.url ?? null" :title="logs?.title ?? ''" @close="logs = null" />
</template>
