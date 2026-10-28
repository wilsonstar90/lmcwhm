<script setup lang="ts">
// Folder (or file) picker over the file manager API, after aaPanel's
// 選擇文件夾: breadcrumb, back, refresh, filter, new file / folder, quick
// places on the left, and the chosen path at the bottom.
import { computed, h, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NCard, NDataTable, NInput, NModal, NSpace, useMessage, type DataTableColumns } from 'naive-ui'
import { get, post, ApiError } from '../api/client'
import type { FileEntry } from '../api/types'
import { datetime } from '../format'

const props = defineProps<{ show: boolean; value?: string; mode?: 'dir' | 'file' }>()
const emit = defineEmits<{ pick: [path: string]; close: [] }>()
const { t, locale } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))

const dir = ref('/')
const items = ref<FileEntry[]>([])
const loading = ref(false)
const q = ref('')
const chosen = ref('')
const history = ref<string[]>([])
const fileMode = computed(() => props.mode === 'file')
const places = ['/', '/www/wwwroot', '/www/wwwlogs', '/opt/lmcwhm', '/root', '/home', '/tmp']

const parent = (p: string) => (p === '/' ? '/' : p.replace(/\/[^/]+\/?$/, '') || '/')
const join = (a: string, b: string) => (a.endsWith('/') ? a + b : `${a}/${b}`)

async function open(p: string, push = true) {
  loading.value = true
  try {
    const r = await get<{ path: string; items: FileEntry[] }>(`/files/list?path=${encodeURIComponent(p)}`)
    if (push && dir.value !== r.path) history.value.push(dir.value)
    dir.value = r.path
    items.value = r.items.filter((x) => x.is_dir || fileMode.value).sort((a, b) => Number(b.is_dir) - Number(a.is_dir) || a.name.localeCompare(b.name))
    q.value = ''
    if (!fileMode.value) chosen.value = r.path
  } catch (e) {
    fail(e)
  } finally {
    loading.value = false
  }
}

// Start in the current value's folder (or its parent when it is a file or missing).
watch(() => props.show, async (on) => {
  if (!on) return
  history.value = []
  const start = (props.value || '').trim() || '/www/wwwroot'
  chosen.value = fileMode.value ? start : ''
  // The nearest folder that exists: the value, then each parent up to /.
  for (let p = start; ; p = parent(p)) {
    try {
      await get(`/files/list?path=${encodeURIComponent(p)}`)
      await open(p, false)
      return
    } catch {
      if (p === '/') return
    }
  }
}, { immediate: true })

function back() {
  const p = history.value.pop()
  if (p !== undefined) open(p, false)
}
const crumbs = computed(() => {
  const parts = dir.value.split('/').filter(Boolean)
  return [{ name: t('fp.root'), path: '/' }, ...parts.map((name, i) => ({ name, path: '/' + parts.slice(0, i + 1).join('/') }))]
})
const shown = computed(() => {
  const s = q.value.trim().toLowerCase()
  return s ? items.value.filter((x) => x.name.toLowerCase().includes(s)) : items.value
})

const naming = ref<{ dir: boolean; name: string } | null>(null)
async function create() {
  const isDir = naming.value!.dir
  const name = naming.value!.name.trim()
  if (!name) return
  if (name.includes('/')) {
    msg.error(t('fp.bad_name'))
    return
  }
  try {
    await post('/files/create', { path: join(dir.value, name), dir: isDir })
    naming.value = null
    await open(dir.value, false)
    if (isDir && !fileMode.value) chosen.value = join(dir.value, name)
    if (!isDir && fileMode.value) chosen.value = join(dir.value, name)
  } catch (e) {
    fail(e)
  }
}

function click(x: FileEntry) {
  if (x.is_dir) {
    if (fileMode.value) open(x.path)
    else chosen.value = x.path
  } else chosen.value = x.path
}
function dbl(x: FileEntry) {
  if (x.is_dir) open(x.path)
  else confirm()
}
function confirm() {
  const p = chosen.value.trim() || dir.value
  emit('pick', p)
  emit('close')
}

const cols = computed<DataTableColumns<FileEntry>>(() => [
  {
    title: () => t('fp.name'), key: 'name',
    render: (x) => h('span', { class: 'name' }, [h('span', { class: x.is_dir ? 'ico dir' : 'ico file' }, x.is_dir ? '📁' : '📄'), x.name]),
  },
  { title: () => t('fp.modified'), key: 'mtime', width: 180, render: (x) => datetime(x.mtime, locale.value) },
  { title: () => t('fp.perm'), key: 'mode', width: 130, render: (x) => `${x.mode} / ${x.owner}` },
])
const rowProps = (x: FileEntry) => ({
  style: 'cursor: pointer',
  class: chosen.value === x.path ? 'picked' : '',
  onClick: () => click(x),
  onDblclick: () => dbl(x),
})
</script>

<template>
  <n-modal :show="show" @update:show="(v: boolean) => !v && emit('close')">
    <n-card :title="fileMode ? t('fp.title_file') : t('fp.title')" style="width: min(820px, calc(100vw - 32px))" closable content-style="padding: 0" @close="emit('close')">
      <div class="bar">
        <n-button size="small" :disabled="!history.length" @click="back">←</n-button>
        <div class="crumbs">
          <a v-for="c in crumbs" :key="c.path" @click="open(c.path)">{{ c.name }}</a>
        </div>
        <n-button size="small" @click="open(dir, false)">⟳</n-button>
        <n-input v-model:value="q" size="small" clearable :placeholder="t('fp.filter')" style="width: 180px" />
      </div>
      <div class="bar" style="padding-top: 0">
        <n-button size="small" @click="naming = { dir: false, name: '' }">{{ t('fp.new_file') }}</n-button>
        <n-button size="small" @click="naming = { dir: true, name: '' }">{{ t('fp.new_folder') }}</n-button>
        <template v-if="naming">
          <n-input v-model:value="naming.name" size="small" autofocus :placeholder="naming.dir ? t('fp.folder_name') : t('fp.file_name')" style="width: 200px" @keyup.enter="create" />
          <n-button size="small" type="primary" :disabled="!naming.name.trim()" @click="create">{{ t('common.confirm') }}</n-button>
          <n-button size="small" quaternary @click="naming = null">{{ t('common.cancel') }}</n-button>
        </template>
        <n-button size="small" quaternary :disabled="dir === '/'" @click="open(parent(dir))">{{ t('fp.up') }}</n-button>
      </div>
      <div class="body">
        <aside>
          <a v-for="p in places" :key="p" :class="{ on: dir === p }" @click="open(p)">{{ p === '/' ? t('fp.root') : p }}</a>
        </aside>
        <n-data-table
          size="small" :columns="cols" :data="shown" :loading="loading" :row-key="(x: FileEntry) => x.path" :row-props="rowProps"
          :max-height="380" class="list"
        />
      </div>
      <p class="hint">{{ fileMode ? t('fp.hint_file') : t('fp.hint') }}</p>
      <template #footer>
        <n-space justify="space-between" align="center" :wrap="false">
          <n-space align="center" :wrap="false">
            <span class="label">{{ fileMode ? t('fp.chosen_file') : t('fp.chosen') }}</span>
            <n-input v-model:value="chosen" size="small" style="width: min(420px, 50vw)" />
          </n-space>
          <n-space>
            <n-button @click="emit('close')">{{ t('common.cancel') }}</n-button>
            <n-button type="primary" :disabled="fileMode && !chosen" @click="confirm">{{ t('common.confirm') }}</n-button>
          </n-space>
        </n-space>
      </template>
    </n-card>
  </n-modal>
</template>

<style scoped>
.bar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 16px;
}
.crumbs {
  flex: 1;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  border: 1px solid rgba(128, 128, 128, 0.25);
  border-radius: 6px;
  padding: 3px 8px;
  min-height: 28px;
  font-size: 13px;
}
.crumbs a {
  cursor: pointer;
}
.crumbs a + a::before {
  content: '›';
  margin: 0 8px;
  opacity: 0.5;
}
.body {
  display: grid;
  grid-template-columns: 150px minmax(0, 1fr);
  border-top: 1px solid rgba(128, 128, 128, 0.18);
}
aside {
  display: flex;
  flex-direction: column;
  padding: 8px 0;
  border-right: 1px solid rgba(128, 128, 128, 0.18);
  font-size: 13px;
}
aside a {
  cursor: pointer;
  padding: 6px 14px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
aside a.on,
aside a:hover {
  background: rgba(128, 128, 128, 0.12);
}
.list :deep(.picked td) {
  background: rgba(24, 160, 88, 0.12) !important;
}
:deep(.name) {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}
.hint {
  font-size: 12px;
  opacity: 0.6;
  margin: 8px 16px 0;
}
.label {
  font-size: 13px;
  white-space: nowrap;
}
@media (max-width: 640px) {
  .body {
    grid-template-columns: minmax(0, 1fr);
  }
  aside {
    display: none;
  }
}
</style>
