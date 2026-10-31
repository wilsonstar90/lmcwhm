<script setup lang="ts">
// 導入 (like aaPanel): upload a dump or pick one already on the server, then
// load it into the database. The list shows every dump in the backup
// directory's database folder (backups and uploads).
import { computed, h, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NCard, NCheckbox, NDataTable, NInput, NModal, NPopconfirm, NProgress, NSpace, useDialog, useMessage, type DataTableColumns } from 'naive-ui'
import { ApiError, get, getCsrf, post } from '../api/client'
import type { Database } from '../api/types'
import { bytes, datetime } from '../format'
import FolderPicker from './FolderPicker.vue'

interface File { path: string; name: string; size: number; mtime: number }

const props = defineProps<{ db: Database | null }>()
const emit = defineEmits<{ close: []; task: [id: string, title: string] }>()
const { t, locale } = useI18n()
const msg = useMessage()
const dialog = useDialog()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))
const files = ref<File[]>([])
const dir = ref('')
const q = ref('')
const loading = ref(false)
const picker = ref(false)
const input = ref<HTMLInputElement | null>(null)
const upload = ref<{ name: string; sent: number; size: number } | null>(null)
const lastTask = ref<string | null>(null)
const sqlEngine = computed(() => ['mysql', 'mariadb', 'postgres'].includes(props.db?.engine ?? ''))

async function load() {
  loading.value = true
  try {
    const r = await get<{ items: File[]; dir: string }>(`/databases/import-files?q=${encodeURIComponent(q.value)}`)
    files.value = r.items
    dir.value = r.dir
  } catch (e) {
    fail(e)
  } finally {
    loading.value = false
  }
}
watch(
  () => props.db,
  (d) => {
    if (d) {
      q.value = ''
      load()
    }
  },
)

const accepted = /\.(sql|sql\.gz|zip|tar\.gz|tgz)$/i
const CHUNK = 8 << 20
async function send(f: globalThis.File) {
  if (!accepted.test(f.name)) return msg.warning(t('dbi.bad_type'))
  // Resume a partial upload of the same file.
  let offset = 0
  try {
    const s = await get<{ size: number }>(`/databases/import-upload?name=${encodeURIComponent(f.name)}`)
    if (s.size > 0 && s.size < f.size) offset = s.size
  } catch {
    /* start over */
  }
  upload.value = { name: f.name, sent: offset, size: f.size }
  try {
    do {
      const end = Math.min(offset + CHUNK, f.size)
      const last = end >= f.size ? '&last=1' : ''
      const res = await fetch(`/api/v1/databases/import-upload?name=${encodeURIComponent(f.name)}&offset=${offset}${last}`, {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'X-CSRF-Token': getCsrf() },
        body: f.slice(offset, end),
      })
      const data = await res.json().catch(() => ({}))
      if (res.status === 409 && typeof data.size === 'number') {
        offset = data.size
        continue
      }
      if (!res.ok) throw new ApiError(res.status, data.code ?? 'common.internal', data.params)
      offset = data.size
      upload.value.sent = offset
    } while (offset < f.size)
    msg.success(t('dbi.uploaded', { name: f.name }))
    await load()
  } catch (e) {
    fail(e)
  } finally {
    upload.value = null
  }
}
function pickLocal(e: Event) {
  const f = (e.target as HTMLInputElement).files?.[0]
  ;(e.target as HTMLInputElement).value = ''
  if (f) send(f)
}

function importFile(path: string) {
  const first = ref(true)
  dialog.warning({
    title: t('dbi.import'),
    content: () =>
      h('div', [
        h('p', t('dbi.confirm', { file: path.split('/').pop(), db: props.db!.name })),
        h(NCheckbox, { checked: first.value, 'onUpdate:checked': (v: boolean) => (first.value = v) }, () => t('dbb.backup_first')),
      ]),
    positiveText: t('dbi.import'),
    negativeText: t('common.cancel'),
    onPositiveClick: async () => {
      try {
        const r = await post<{ task_id: string }>(`/databases/${props.db!.id}/import`, { path, backup_first: first.value })
        lastTask.value = r.task_id
        emit('task', r.task_id, `${t('dbi.import')} · ${props.db!.name}`)
      } catch (e) {
        fail(e)
      }
    },
  })
}
async function remove(f: File) {
  try {
    await post('/databases/import-files/delete', { path: f.path })
    msg.success(t('common.deleted'))
    load()
  } catch (e) {
    fail(e)
  }
}

const columns = computed<DataTableColumns<File>>(() => [
  { title: t('dbi.file'), key: 'name', minWidth: 240, ellipsis: { tooltip: true }, render: (f) => h('span', { title: f.path }, f.name) },
  { title: t('dbi.mtime'), key: 'mtime', width: 160, render: (f) => datetime(f.mtime, locale.value) },
  { title: t('dbb.size'), key: 'size', width: 100, render: (f) => bytes(f.size) },
  {
    title: t('dbb.ops'),
    key: 'ops',
    width: 110,
    render: (f) =>
      h(NSpace, { size: 10, wrap: false }, () => [
        h('a', { class: 'link', onClick: () => importFile(f.path) }, t('dbi.import')),
        h(NPopconfirm, { onPositiveClick: () => remove(f) }, { trigger: () => h('a', { class: 'link danger' }, t('common.delete')), default: () => t('dbb.delete_confirm', { file: f.name }) }),
      ]),
  },
])
</script>

<template>
  <n-modal :show="!!db" @update:show="(v: boolean) => !v && emit('close')">
    <n-card v-if="db" :title="t('dbi.title', { name: db.name })" style="width: min(860px, calc(100vw - 24px))" closable @close="emit('close')">
      <p v-if="!sqlEngine" class="note">{{ t('dbi.unsupported') }}</p>
      <template v-else>
        <n-space justify="space-between" align="center" style="margin-bottom: 12px">
          <n-space>
            <n-button :disabled="!!upload" @click="input?.click()">{{ t('dbi.upload') }}</n-button>
            <n-button @click="picker = true">{{ t('dbi.from_server') }}</n-button>
            <n-button :disabled="!lastTask" @click="lastTask && emit('task', lastTask, `${t('dbi.import')} · ${db.name}`)">{{ t('dbi.show_log') }}</n-button>
            <input ref="input" type="file" accept=".sql,.gz,.zip,.tgz" hidden @change="pickLocal" />
          </n-space>
          <n-input v-model:value="q" size="small" clearable :placeholder="t('dbi.search')" style="width: 220px" @keyup.enter="load" @clear="() => ((q = ''), load())" />
        </n-space>
        <div v-if="upload" class="up">
          <span>{{ t('dbi.uploading', { name: upload.name }) }}</span>
          <n-progress type="line" :percentage="Math.floor((upload.sent / Math.max(upload.size, 1)) * 100)" :height="8" />
        </div>
        <n-data-table :columns="columns" :data="files" :loading="loading" size="small" :scroll-x="620" :max-height="420" :pagination="{ pageSize: 50 }" :row-key="(f: File) => f.path" />
        <ul class="note">
          <li>{{ t('dbi.note_formats') }}</li>
          <li>{{ t('dbi.note_archive') }}</li>
          <li>{{ t('dbi.note_large', { dir }) }}</li>
        </ul>
      </template>
    </n-card>
  </n-modal>
  <FolderPicker :show="picker" mode="file" :value="dir" @pick="(p: string) => ((picker = false), importFile(p))" @close="picker = false" />
</template>

<style scoped>
.note {
  font-size: 12px;
  opacity: 0.75;
  margin: 12px 0 0;
  padding-left: 18px;
  word-break: break-all;
}
.up {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 10px;
  font-size: 13px;
}
.up span {
  white-space: nowrap;
}
:deep(.link) {
  color: #20a53a;
  cursor: pointer;
}
:deep(.link.danger) {
  color: #d03050;
}
</style>
