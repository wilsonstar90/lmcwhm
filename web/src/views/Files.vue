<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import {
  NButton, NDataTable, NSpace, NInput, NModal, NCard, NForm, NFormItem, NCheckbox, NDropdown, NBreadcrumb, NBreadcrumbItem,
  NProgress, NDrawer, NDrawerContent, NPopconfirm, NRadioGroup, NRadioButton, useMessage, type DataTableColumns,
} from 'naive-ui'
import { get, post, put, getCsrf, ApiError } from '../api/client'
import type { FileEntry, TrashItem } from '../api/types'
import { bytes, datetime } from '../format'

const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))

const cwd = ref('/www/wwwroot')
const pathInput = ref('')
const items = ref<FileEntry[]>([])
const loading = ref(false)
const checked = ref<string[]>([])
const search = ref('')

const join = (dir: string, name: string) => (dir === '/' ? '' : dir) + '/' + name
const parent = (p: string) => p.replace(/\/[^/]+\/?$/, '') || '/'

async function load(dir = cwd.value) {
  loading.value = true
  try {
    const r = await get<{ path: string; items: FileEntry[] }>(`/files/list?path=${encodeURIComponent(dir)}`)
    cwd.value = pathInput.value = r.path
    items.value = r.items
    checked.value = []
    if (route.query.path !== r.path) router.replace({ query: { path: r.path } })
  } catch (e) {
    fail(e)
    pathInput.value = cwd.value
  } finally {
    loading.value = false
  }
}
onMounted(() => load((route.query.path as string) || cwd.value))

const crumbs = computed(() => {
  const parts = cwd.value.split('/').filter(Boolean)
  return [{ name: '/', path: '/' }, ...parts.map((p, i) => ({ name: p, path: '/' + parts.slice(0, i + 1).join('/') }))]
})
const filtered = computed(() => {
  const q = search.value.trim().toLowerCase()
  return q ? items.value.filter((i) => i.name.toLowerCase().includes(q)) : items.value
})

async function call(fn: () => Promise<unknown>, ok = t('common.done')) {
  try {
    await fn()
    if (ok) msg.success(ok)
    load()
    return true
  } catch (e) {
    fail(e)
    return false
  }
}

// ---- prompt dialog (rename / copy / move / new / chmod / chown / compress) ----
type Prompt = {
  title: string
  fields: { key: string; label: string; value: string }[]
  recursive?: boolean
  format?: 'zip' | 'tar.gz'
  submit: (v: Record<string, string>, p: Prompt) => Promise<unknown>
}
const prompt = ref<Prompt | null>(null)
async function submitPrompt() {
  const p = prompt.value!
  const v = Object.fromEntries(p.fields.map((f) => [f.key, f.value.trim()]))
  if (await call(() => p.submit(v, p))) prompt.value = null
}

function newItem(dir: boolean) {
  prompt.value = {
    title: dir ? t('files.new_folder') : t('files.new_file'),
    fields: [{ key: 'name', label: t('files.name'), value: '' }],
    submit: (v) => post('/files/create', { path: join(cwd.value, v.name), dir }),
  }
}
function rename(e: FileEntry) {
  prompt.value = {
    title: t('files.rename'),
    fields: [{ key: 'name', label: t('files.name'), value: e.name }],
    submit: (v) => post('/files/rename', { from: e.path, to: join(cwd.value, v.name) }),
  }
}
function copyMove(e: FileEntry, move: boolean) {
  prompt.value = {
    title: move ? t('files.move') : t('files.copy'),
    fields: [{ key: 'to', label: t('files.target'), value: e.path + (move ? '' : '.copy') }],
    submit: (v) => post(move ? '/files/rename' : '/files/copy', { from: e.path, to: v.to }),
  }
}
function chmod(e: FileEntry) {
  prompt.value = {
    title: `${t('files.permissions')} · ${e.name}`,
    fields: [{ key: 'mode', label: t('files.mode'), value: e.mode.padStart(3, '0') }],
    recursive: false,
    submit: (v, p) => post('/files/chmod', { path: e.path, mode: v.mode, recursive: p.recursive }),
  }
}
function chown(e: FileEntry) {
  prompt.value = {
    title: `${t('files.owner')} · ${e.name}`,
    fields: [
      { key: 'owner', label: t('files.owner'), value: e.owner },
      { key: 'group', label: t('files.group'), value: e.group },
    ],
    recursive: false,
    submit: (v, p) => post('/files/chown', { path: e.path, owner: v.owner, group: v.group, recursive: p.recursive }),
  }
}
function compress(paths: string[]) {
  const base = paths.length === 1 ? paths[0].split('/').pop() : 'archive'
  prompt.value = {
    title: t('files.compress'),
    fields: [{ key: 'dest', label: t('files.target'), value: join(cwd.value, `${base}.zip`) }],
    format: 'zip',
    submit: (v, p) => post('/files/compress', { paths, dest: v.dest, format: p.format }),
  }
}
function setFormat(f: 'zip' | 'tar.gz') {
  const p = prompt.value!
  p.format = f
  p.fields[0].value = p.fields[0].value.replace(/\.(zip|tar\.gz)$/, '') + '.' + f
}
function extract(e: FileEntry) {
  prompt.value = {
    title: t('files.extract'),
    fields: [{ key: 'dest', label: t('files.target'), value: cwd.value }],
    submit: (v) => post('/files/extract', { path: e.path, dest: v.dest }),
  }
}
async function remove(paths: string[], permanent = false) {
  if (!confirm(permanent ? t('files.delete_permanent_confirm', { n: paths.length }) : t('files.delete_confirm', { n: paths.length }))) return
  try {
    await post('/files/delete', { paths, permanent })
    msg.success(t('common.done'))
  } catch (e) {
    if (e instanceof ApiError && e.code === 'files.trash_cross_device' && confirm(t('files.cross_device_confirm'))) {
      return remove(paths, true)
    }
    fail(e)
  }
  load()
}

// ---- editor ----
const editor = ref<{ path: string; content: string; saving: boolean } | null>(null)
async function edit(e: FileEntry) {
  try {
    const r = await get<{ content: string }>(`/files/read?path=${encodeURIComponent(e.path)}`)
    editor.value = { path: e.path, content: r.content, saving: false }
  } catch (err) {
    fail(err)
  }
}
async function saveEditor() {
  const ed = editor.value!
  ed.saving = true
  try {
    await put('/files/write', { path: ed.path, content: ed.content })
    msg.success(t('common.saved'))
  } catch (e) {
    fail(e)
  } finally {
    ed.saving = false
  }
}
function editorKey(ev: KeyboardEvent) {
  if ((ev.ctrlKey || ev.metaKey) && ev.key === 's') {
    ev.preventDefault()
    saveEditor()
  }
}

// ---- upload (8 MB chunks, resumable from the size already on the server) ----
const fileInput = ref<HTMLInputElement | null>(null)
const upload = ref<{ name: string; percent: number } | null>(null)
const CHUNK = 8 << 20
async function uploadFiles(ev: Event) {
  const list = Array.from((ev.target as HTMLInputElement).files ?? [])
  ;(ev.target as HTMLInputElement).value = ''
  for (const file of list) {
    const dest = join(cwd.value, file.name)
    let offset = 0
    try {
      const st = await get<FileEntry>(`/files/stat?path=${encodeURIComponent(dest)}`).catch(() => null)
      if (st && !st.is_dir && st.size > 0 && st.size < file.size && confirm(t('files.resume_confirm', { name: file.name }))) offset = st.size
      else if (st && !confirm(t('files.overwrite_confirm', { name: file.name }))) continue
      do {
        upload.value = { name: file.name, percent: Math.floor((offset / Math.max(file.size, 1)) * 100) }
        const end = Math.min(offset + CHUNK, file.size)
        const last = end >= file.size ? '&last=1' : ''
        const res = await fetch(`/api/v1/files/upload?path=${encodeURIComponent(dest)}&offset=${offset}${last}`, {
          method: 'POST',
          credentials: 'same-origin',
          headers: { 'X-CSRF-Token': getCsrf() },
          body: file.slice(offset, end),
        })
        const data = await res.json().catch(() => ({}))
        if (!res.ok) throw new ApiError(res.status, data.code ?? 'common.internal', data.params)
        offset = data.size
      } while (offset < file.size)
    } catch (e) {
      fail(e)
      break
    }
  }
  upload.value = null
  load()
}

// ---- trash ----
const trash = ref<TrashItem[] | null>(null)
const trashDir = ref('')
async function openTrash() {
  try {
    const r = await get<{ items: TrashItem[]; dir: string }>('/files/trash')
    trash.value = r.items
    trashDir.value = r.dir
  } catch (e) {
    fail(e)
  }
}
async function trashAction(url: string, id: string) {
  try {
    await post(url, { id })
    msg.success(t('common.done'))
    openTrash()
    load()
  } catch (e) {
    fail(e)
  }
}

// ---- table ----
const isArchive = (n: string) => /\.(zip|tar\.gz|tgz|tar)$/i.test(n)
function open(e: FileEntry) {
  if (e.is_dir) load(e.path)
  else edit(e)
}
function menu(e: FileEntry) {
  return [
    ...(e.is_dir ? [] : [{ label: t('files.edit'), key: 'edit' }, { label: t('files.download'), key: 'download' }]),
    { label: t('files.rename'), key: 'rename' },
    { label: t('files.copy'), key: 'copy' },
    { label: t('files.move'), key: 'move' },
    { label: t('files.permissions'), key: 'chmod' },
    { label: t('files.owner'), key: 'chown' },
    { label: t('files.compress'), key: 'compress' },
    ...(isArchive(e.name) ? [{ label: t('files.extract'), key: 'extract' }] : []),
    { type: 'divider', key: 'd' },
    { label: t('common.delete'), key: 'delete' },
  ]
}
function onMenu(e: FileEntry, k: string) {
  switch (k) {
    case 'edit':
      return edit(e)
    case 'download':
      return window.open(`/api/v1/files/download?path=${encodeURIComponent(e.path)}`)
    case 'rename':
      return rename(e)
    case 'copy':
      return copyMove(e, false)
    case 'move':
      return copyMove(e, true)
    case 'chmod':
      return chmod(e)
    case 'chown':
      return chown(e)
    case 'compress':
      return compress([e.path])
    case 'extract':
      return extract(e)
    case 'delete':
      return remove([e.path])
  }
}

const columns: DataTableColumns<FileEntry> = [
  { type: 'selection' },
  {
    title: () => t('files.name'),
    key: 'name',
    sorter: (a, b) => a.name.localeCompare(b.name),
    render: (e) =>
      h('a', { class: 'name', onClick: () => open(e) }, [
        h('span', { class: 'icon' }, e.is_dir ? '📁' : '📄'),
        e.name,
        e.link ? h('span', { class: 'link' }, ` → ${e.link}`) : null,
      ]),
  },
  { title: () => t('files.size'), key: 'size', width: 100, sorter: (a, b) => a.size - b.size, render: (e) => (e.is_dir ? '—' : bytes(e.size)) },
  { title: () => t('files.mode'), key: 'mode', width: 80, render: (e) => h('code', e.mode.padStart(3, '0')) },
  { title: () => t('files.owner'), key: 'owner', width: 130, render: (e) => `${e.owner}:${e.group}` },
  { title: () => t('files.mtime'), key: 'mtime', width: 170, sorter: (a, b) => a.mtime - b.mtime, render: (e) => datetime(e.mtime, locale.value) },
  {
    title: '',
    key: 'actions',
    width: 60,
    render: (e) => h(NDropdown, { options: menu(e), trigger: 'click', onSelect: (k: string) => onMenu(e, k) }, () => h(NButton, { size: 'tiny' }, () => '⋯')),
  },
]
</script>

<template>
  <n-space vertical :size="12">
    <n-space align="center" :wrap="false">
      <n-button size="small" :disabled="cwd === '/'" @click="load(parent(cwd))">↑</n-button>
      <n-input v-model:value="pathInput" size="small" style="min-width: 260px; flex: 1" @keyup.enter="load(pathInput)" />
      <n-button size="small" @click="load()">{{ t('common.refresh') }}</n-button>
    </n-space>
    <n-breadcrumb separator="›">
      <n-breadcrumb-item v-for="c in crumbs" :key="c.path" @click="load(c.path)">{{ c.name }}</n-breadcrumb-item>
    </n-breadcrumb>
    <n-space justify="space-between">
      <n-space>
        <n-button size="small" type="primary" @click="fileInput?.click()">{{ t('files.upload') }}</n-button>
        <n-button size="small" @click="newItem(false)">{{ t('files.new_file') }}</n-button>
        <n-button size="small" @click="newItem(true)">{{ t('files.new_folder') }}</n-button>
        <template v-if="checked.length">
          <n-button size="small" @click="compress(checked)">{{ t('files.compress') }} ({{ checked.length }})</n-button>
          <n-button size="small" type="error" ghost @click="remove(checked)">{{ t('common.delete') }} ({{ checked.length }})</n-button>
        </template>
      </n-space>
      <n-space>
        <n-input v-model:value="search" size="small" clearable :placeholder="t('files.filter')" style="width: 180px" />
        <n-button size="small" @click="openTrash">{{ t('files.trash') }}</n-button>
      </n-space>
    </n-space>
    <input ref="fileInput" type="file" multiple style="display: none" @change="uploadFiles" />
    <div v-if="upload">
      {{ t('files.uploading', { name: upload.name }) }}
      <n-progress type="line" :percentage="upload.percent" />
    </div>
    <n-data-table
      v-model:checked-row-keys="checked"
      size="small"
      :columns="columns"
      :data="filtered"
      :loading="loading"
      :row-key="(r: FileEntry) => r.path"
      :scroll-x="820"
      :pagination="{ pageSize: 100 }"
    />
  </n-space>

  <n-modal :show="!!prompt" @update:show="(v: boolean) => !v && (prompt = null)">
    <n-card v-if="prompt" :title="prompt.title" style="width: min(520px, calc(100vw - 32px))" closable @close="prompt = null">
      <n-form label-placement="top" @submit.prevent="submitPrompt">
        <n-form-item v-for="f in prompt.fields" :key="f.key" :label="f.label">
          <n-input v-model:value="f.value" @keyup.enter="submitPrompt" />
        </n-form-item>
        <n-radio-group v-if="prompt.format" :value="prompt.format" @update:value="setFormat">
          <n-radio-button value="zip">zip</n-radio-button>
          <n-radio-button value="tar.gz">tar.gz</n-radio-button>
        </n-radio-group>
        <n-checkbox v-if="prompt.recursive !== undefined" v-model:checked="prompt.recursive">{{ t('files.recursive') }}</n-checkbox>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="prompt = null">{{ t('common.cancel') }}</n-button>
          <n-button type="primary" @click="submitPrompt">{{ t('common.confirm') }}</n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>

  <n-modal :show="!!editor" @update:show="(v: boolean) => !v && (editor = null)">
    <n-card v-if="editor" :title="editor.path" style="width: min(1100px, calc(100vw - 32px))" closable @close="editor = null">
      <n-input v-model:value="editor.content" type="textarea" class="mono" :autosize="{ minRows: 20, maxRows: 32 }" @keydown="editorKey" />
      <template #footer>
        <n-space justify="space-between" align="center">
          <span style="font-size: 12px; opacity: 0.6">Ctrl/⌘ + S</span>
          <n-space>
            <n-button @click="editor = null">{{ t('common.close') }}</n-button>
            <n-button type="primary" :loading="editor.saving" @click="saveEditor">{{ t('common.save') }}</n-button>
          </n-space>
        </n-space>
      </template>
    </n-card>
  </n-modal>

  <n-drawer :show="!!trash" :width="'min(640px, 100vw)'" @update:show="(v: boolean) => !v && (trash = null)">
    <n-drawer-content :title="t('files.trash')" closable>
      <p style="font-size: 12px; opacity: 0.7; margin-top: 0">{{ trashDir }}</p>
      <n-space vertical>
        <div v-for="it in trash" :key="it.id" class="trash-row">
          <div>
            <div>{{ it.is_dir ? '📁' : '📄' }} {{ it.path }}</div>
            <div style="font-size: 12px; opacity: 0.6">{{ datetime(it.deleted_at, locale) }}{{ it.is_dir ? '' : ' · ' + bytes(it.size) }}</div>
          </div>
          <n-space :size="6">
            <n-button size="tiny" @click="trashAction('/files/trash/restore', it.id)">{{ t('files.restore') }}</n-button>
            <n-button size="tiny" type="error" ghost @click="trashAction('/files/trash/empty', it.id)">{{ t('common.delete') }}</n-button>
          </n-space>
        </div>
        <p v-if="trash && !trash.length" style="opacity: 0.6">{{ t('files.trash_empty') }}</p>
      </n-space>
      <template #footer>
        <n-popconfirm @positive-click="trashAction('/files/trash/empty', '')">
          <template #trigger><n-button type="error" :disabled="!trash?.length">{{ t('files.empty_trash') }}</n-button></template>
          {{ t('files.empty_trash_confirm') }}
        </n-popconfirm>
      </template>
    </n-drawer-content>
  </n-drawer>
</template>

<style scoped>
.name {
  cursor: pointer;
}
.icon {
  margin-right: 6px;
}
.link {
  opacity: 0.5;
  font-size: 12px;
}
.mono :deep(textarea) {
  font-family: Menlo, Consolas, monospace;
  font-size: 12px;
  line-height: 1.5;
}
.trash-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 6px 0;
  border-bottom: 1px solid rgba(127, 127, 127, 0.15);
  word-break: break-all;
}
</style>
