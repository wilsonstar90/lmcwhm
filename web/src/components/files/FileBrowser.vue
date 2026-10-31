<script setup lang="ts">
// One file manager tab (like aaPanel): path bar, toolbar, list or grid,
// click to select / double-click to open, context menu, inline remarks,
// directory sizes, clipboard and drop-to-upload.
import { computed, h, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import {
  NButton, NButtonGroup, NCard, NCheckbox, NDataTable, NDropdown, NForm, NFormItem, NInput, NModal, NRadioButton, NRadioGroup,
  NSpace, NTag, useMessage, type DataTableColumns, type DropdownOption,
} from 'naive-ui'
import { get, post, put, ApiError } from '../../api/client'
import type { FileEntry } from '../../api/types'
import { bytes, datetime } from '../../format'
import FilesExtras from '../FilesExtras.vue'
import {
  baseName, changed, clipboard, droppedFiles, iconOf, isArchive, isImage, join, openInEditor, openUpload, parentOf,
} from './state'

const props = defineProps<{ path: string }>()
const emit = defineEmits<{ path: [p: string]; trash: [] }>()
const { t, te, locale } = useI18n()
const router = useRouter()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))

const cwd = ref(props.path || '/www/wwwroot')
const items = ref<FileEntry[]>([])
const loading = ref(false)
const selected = ref<string[]>([])
const anchor = ref('')
const search = ref('')
const view = ref<'list' | 'grid'>((() => {
  try {
    return localStorage.getItem('lmcwhm.files.view') === 'grid' ? 'grid' : 'list'
  } catch {
    return 'list'
  }
})())
watch(view, (v) => {
  try {
    localStorage.setItem('lmcwhm.files.view', v)
  } catch {
    /* private mode */
  }
})
const history = ref<string[]>([])
const sizes = ref<Record<string, { size: number; done: boolean } | 'busy'>>({})
const disk = ref<{ mount: string; free: number; total: number } | null>(null)

async function load(dir = cwd.value, push = true) {
  loading.value = true
  try {
    const r = await get<{ path: string; items: FileEntry[] }>(`/files/list?path=${encodeURIComponent(dir)}`)
    if (push && r.path !== cwd.value) history.value.push(cwd.value)
    cwd.value = r.path
    items.value = r.items
    selected.value = selected.value.filter((p) => r.items.some((i) => i.path === p))
    emit('path', r.path)
    loadDisk()
  } catch (e) {
    fail(e)
  } finally {
    loading.value = false
  }
}
async function loadDisk() {
  try {
    disk.value = await get<{ mount: string; free: number; total: number }>(`/files/disk?path=${encodeURIComponent(cwd.value)}`)
  } catch {
    disk.value = null
  }
}
function back() {
  const p = history.value.pop()
  if (p) load(p, false)
}
onMounted(() => load(cwd.value, false))
watch(changed, () => load(cwd.value, false))
defineExpose({ load: () => load(cwd.value, false), cwd })

// ---- path bar: breadcrumb, or an input after a click on the empty part ----
const editingPath = ref(false)
const pathText = ref('')
const pathEl = ref<InstanceType<typeof NInput> | null>(null)
const crumbs = computed(() => {
  const parts = cwd.value.split('/').filter(Boolean)
  return [{ name: t('fm.root'), path: '/' }, ...parts.map((p, i) => ({ name: p, path: '/' + parts.slice(0, i + 1).join('/') }))]
})
async function editPath() {
  pathText.value = cwd.value
  editingPath.value = true
  await nextTick()
  pathEl.value?.focus()
  pathEl.value?.select()
}
function goPath() {
  editingPath.value = false
  const p = pathText.value.trim()
  if (p && p !== cwd.value) load(p.startsWith('/') ? p : '/' + p)
}

// ---- selection: click selects, double click opens (touch: tap a selected item) ----
const shown = computed(() => {
  const q = search.value.trim().toLowerCase()
  const list = q ? items.value.filter((i) => i.name.toLowerCase().includes(q) || (i.remark ?? '').toLowerCase().includes(q)) : items.value
  return [...list].sort((a, b) => Number(b.is_dir) - Number(a.is_dir) || a.name.localeCompare(b.name))
})
const byPath = computed(() => new Map(items.value.map((i) => [i.path, i])))
const sel = computed(() => selected.value.map((p) => byPath.value.get(p)).filter((e): e is FileEntry => !!e))
let pointer = 'mouse'
function onPointerDown(ev: PointerEvent) {
  pointer = ev.pointerType
}
function clickItem(e: FileEntry, ev: MouseEvent) {
  if (ev.shiftKey && anchor.value) {
    const list = shown.value.map((i) => i.path)
    const [a, b] = [list.indexOf(anchor.value), list.indexOf(e.path)].sort((x, y) => x - y)
    selected.value = list.slice(a, b + 1)
    return
  }
  if (ev.metaKey || ev.ctrlKey) {
    selected.value = selected.value.includes(e.path) ? selected.value.filter((p) => p !== e.path) : [...selected.value, e.path]
    anchor.value = e.path
    return
  }
  if (pointer === 'touch' && selected.value.length === 1 && selected.value[0] === e.path) return open(e)
  selected.value = [e.path]
  anchor.value = e.path
}
function open(e: FileEntry) {
  if (e.is_dir) return load(e.path)
  if (isImage(e.name)) return (preview.value = e)
  if (isArchive(e.name)) return extract(e)
  openInEditor(e.path)
}
function selectAll() {
  selected.value = shown.value.map((i) => i.path)
}

// Long press on touch screens opens the context menu.
let pressTimer = 0
function onTouchStart(e: FileEntry | null, ev: TouchEvent) {
  const tch = ev.touches[0]
  pressTimer = window.setTimeout(() => showMenu(e, tch.clientX, tch.clientY), 550)
}
const cancelPress = () => clearTimeout(pressTimer)

// ---- context menu ----
const menu = ref<{ x: number; y: number; target: FileEntry | null } | null>(null)
function showMenu(e: FileEntry | null, x: number, y: number) {
  if (e && !selected.value.includes(e.path)) selected.value = [e.path]
  if (!e) selected.value = []
  menu.value = null
  nextTick(() => (menu.value = { x, y, target: e }))
}
function onContext(e: FileEntry | null, ev: MouseEvent) {
  ev.preventDefault()
  ev.stopPropagation()
  showMenu(e, ev.clientX, ev.clientY)
}
const menuOptions = computed<DropdownOption[]>(() => {
  const e = menu.value?.target
  if (!e) {
    return [
      { label: t('files.new_folder'), key: 'mkdir' },
      { label: t('files.new_file'), key: 'mkfile' },
      { label: t('fm.paste'), key: 'paste', disabled: !clipboard.value },
      { label: t('files.upload'), key: 'upload' },
      { type: 'divider', key: 'd1' },
      { label: t('fm.select_all'), key: 'all' },
      { label: t('common.refresh'), key: 'refresh' },
    ]
  }
  const many = selected.value.length > 1
  if (many) {
    return [
      { label: t('fm.copy'), key: 'copy' },
      { label: t('fm.cut'), key: 'cut' },
      { label: t('files.compress'), key: 'compress' },
      { label: t('files.permissions'), key: 'chmod' },
      { type: 'divider', key: 'd' },
      { label: `${t('common.delete')} (${selected.value.length})`, key: 'delete' },
    ]
  }
  return [
    { label: t('fm.open'), key: 'open' },
    ...(e.is_dir
      ? [{ label: t('fm.open_tab'), key: 'tab' }]
      : [{ label: t('fm.open_with'), key: 'with', children: [{ label: t('fm.editor'), key: 'edit' }, { label: t('files.download'), key: 'download' }] }]),
    ...(isArchive(e.name) ? [{ label: t('files.extract'), key: 'extract' }] : []),
    { type: 'divider', key: 'd1' },
    ...(e.is_dir ? [] : [{ label: t('files.download'), key: 'download' }]),
    ...(e.is_dir ? [{ label: t('fx.fav'), key: 'fav' }] : []),
    ...(e.is_dir ? [] : [{ label: t('fx.share'), key: 'share' }]),
    { label: t('files.permissions'), key: 'chmod' },
    { label: t('files.owner'), key: 'chown' },
    { type: 'divider', key: 'd2' },
    { label: t('fm.copy'), key: 'copy' },
    { label: t('fm.cut'), key: 'cut' },
    { label: t('files.rename'), key: 'rename' },
    { label: t('common.delete'), key: 'delete' },
    { type: 'divider', key: 'd3' },
    { label: t('files.compress'), key: 'compress' },
    { label: t('fm.duplicate'), key: 'dup' },
    { label: t('fm.remark'), key: 'remark' },
    { label: t('fm.properties'), key: 'props' },
  ]
})
const emitTab = defineModel<string | null>('openTab', { default: null })
function act(key: string, e: FileEntry | null = menu.value?.target ?? sel.value[0] ?? null) {
  menu.value = null
  const paths = selected.value.length ? selected.value : e ? [e.path] : []
  switch (key) {
    case 'mkdir':
      return newItem(true)
    case 'mkfile':
      return newItem(false)
    case 'paste':
      return paste()
    case 'upload':
      return openUpload(cwd.value)
    case 'all':
      return selectAll()
    case 'refresh':
      return load(cwd.value, false)
    case 'copy':
    case 'cut':
      clipboard.value = { mode: key, paths }
      return msg.info(t(key === 'copy' ? 'fm.copied_n' : 'fm.cut_n', { n: paths.length }))
    case 'compress':
      return compress(paths)
    case 'delete':
      return remove(paths)
    case 'chmod':
      return e && chmod(e, paths)
  }
  if (!e) return
  switch (key) {
    case 'open':
      return open(e)
    case 'tab':
      emitTab.value = e.path
      return
    case 'edit':
      return openInEditor(e.path)
    case 'download':
      return window.open(`/api/v1/files/download?path=${encodeURIComponent(e.path)}`)
    case 'extract':
      return extract(e)
    case 'fav':
      return post('/files/favorites', { path: e.path, on: true }).then(() => msg.success(t('common.done')), fail)
    case 'share':
      return extras.value?.share(e.path)
    case 'chown':
      return chown(e)
    case 'rename':
      return rename(e)
    case 'dup':
      return duplicate(e)
    case 'remark':
      return editRemark(e)
    case 'props':
      return (propsOf.value = e)
  }
}

// ---- keyboard (when the list has focus) ----
function onKey(ev: KeyboardEvent) {
  if ((ev.target as HTMLElement).closest('input, textarea')) return
  const mod = ev.metaKey || ev.ctrlKey
  if (mod && ev.key.toLowerCase() === 'a') {
    ev.preventDefault()
    selectAll()
  } else if (mod && ev.key.toLowerCase() === 'c' && selected.value.length) act('copy')
  else if (mod && ev.key.toLowerCase() === 'x' && selected.value.length) act('cut')
  else if (mod && ev.key.toLowerCase() === 'v' && clipboard.value) paste()
  else if ((ev.key === 'Delete' || (ev.key === 'Backspace' && mod)) && selected.value.length) remove(selected.value)
  else if (ev.key === 'Enter' && sel.value.length === 1) open(sel.value[0])
  else if (ev.key === 'Backspace' && !mod) {
    ev.preventDefault()
    load(parentOf(cwd.value))
  }
}

// ---- operations ----
async function call(fn: () => Promise<unknown>, ok = t('common.done')) {
  try {
    await fn()
    if (ok) msg.success(ok)
    load(cwd.value, false)
    return true
  } catch (e) {
    fail(e)
    return false
  }
}
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
    submit: async (v) => {
      await post('/files/create', { path: join(cwd.value, v.name), dir })
      if (!dir) openInEditor(join(cwd.value, v.name))
    },
  }
}
function rename(e: FileEntry) {
  prompt.value = {
    title: t('files.rename'),
    fields: [{ key: 'name', label: t('files.name'), value: e.name }],
    submit: (v) => post('/files/rename', { from: e.path, to: join(cwd.value, v.name) }),
  }
}
function chmod(e: FileEntry, paths = [e.path]) {
  prompt.value = {
    title: `${t('files.permissions')} · ${paths.length > 1 ? paths.length : e.name}`,
    fields: [{ key: 'mode', label: t('files.mode'), value: e.mode.padStart(3, '0') }],
    recursive: false,
    submit: async (v, p) => {
      for (const path of paths) await post('/files/chmod', { path, mode: v.mode, recursive: p.recursive })
    },
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
  const base = paths.length === 1 ? baseName(paths[0]) : 'archive'
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
    title: `${t('files.extract')} · ${e.name}`,
    fields: [{ key: 'dest', label: t('files.target'), value: cwd.value }],
    submit: (v) => post('/files/extract', { path: e.path, dest: v.dest }),
  }
}
function uniqueName(name: string) {
  const names = new Set(items.value.map((i) => i.name))
  if (!names.has(name)) return name
  const m = name.match(/^(.*?)(\.[^.]+)?$/)!
  for (let i = 1; ; i++) {
    const n = `${m[1]}_${t('fm.copy_suffix')}${i > 1 ? i : ''}${m[2] ?? ''}`
    if (!names.has(n)) return n
  }
}
function duplicate(e: FileEntry) {
  call(() => post('/files/copy', { from: e.path, to: join(cwd.value, uniqueName(e.name)) }))
}
async function paste() {
  const c = clipboard.value
  if (!c) return
  try {
    for (const from of c.paths) {
      let name = baseName(from)
      if (c.mode === 'copy' && parentOf(from) === cwd.value) name = uniqueName(name)
      const to = join(cwd.value, name)
      if (from === to) continue
      await post(c.mode === 'cut' ? '/files/rename' : '/files/copy', { from, to })
    }
    if (c.mode === 'cut') clipboard.value = null
    msg.success(t('common.done'))
  } catch (e) {
    fail(e)
  }
  load(cwd.value, false)
}
async function remove(paths: string[], permanent = false) {
  if (!paths.length) return
  if (!confirm(permanent ? t('files.delete_permanent_confirm', { n: paths.length }) : t('files.delete_confirm', { n: paths.length }))) return
  try {
    await post('/files/delete', { paths, permanent })
    msg.success(t('common.done'))
  } catch (e) {
    if (e instanceof ApiError && e.code === 'files.trash_cross_device' && confirm(t('files.cross_device_confirm'))) return remove(paths, true)
    fail(e)
  }
  selected.value = []
  load(cwd.value, false)
}

// ---- sizes ----
async function calcSize(e: FileEntry) {
  sizes.value[e.path] = 'busy'
  try {
    sizes.value[e.path] = await get<{ size: number; done: boolean }>(`/files/size?path=${encodeURIComponent(e.path)}`)
  } catch (err) {
    delete sizes.value[e.path]
    fail(err)
  }
}
async function calcAll() {
  for (const e of items.value.filter((i) => i.is_dir)) await calcSize(e)
}
function sizeCell(e: FileEntry) {
  if (!e.is_dir) return bytes(e.size)
  const s = sizes.value[e.path]
  if (s === 'busy') return h('span', { class: 'muted' }, '…')
  if (s) return `${bytes(s.size)}${s.done ? '' : '+'}`
  return h('a', { class: 'calc', onClick: (ev: MouseEvent) => (ev.stopPropagation(), calcSize(e)) }, t('fm.calc'))
}

// ---- remarks: click to type ----
const remarkEdit = ref<{ path: string; text: string } | null>(null)
const remarkEl = ref<HTMLInputElement | null>(null)
const autoText = (e: FileEntry) => (e.auto && te(`fm.auto.${e.auto.code}`) ? t(`fm.auto.${e.auto.code}`, { arg: e.auto.arg ?? '' }) : '')
async function editRemark(e: FileEntry) {
  remarkEdit.value = { path: e.path, text: e.remark ?? '' }
  await nextTick()
  remarkEl.value?.focus()
}
async function saveRemark() {
  const r = remarkEdit.value
  if (!r) return
  remarkEdit.value = null
  const e = byPath.value.get(r.path)
  if (!e || (e.remark ?? '') === r.text.trim()) return
  try {
    await put('/files/remark', { path: r.path, remark: r.text })
    e.remark = r.text.trim()
  } catch (err) {
    fail(err)
  }
}
function remarkCell(e: FileEntry) {
  if (remarkEdit.value?.path === e.path) {
    return h('input', {
      ref: remarkEl, class: 'remark-input', value: remarkEdit.value.text, maxlength: 500,
      onInput: (ev: Event) => (remarkEdit.value!.text = (ev.target as HTMLInputElement).value),
      onKeydown: (ev: KeyboardEvent) => {
        ev.stopPropagation()
        if (ev.key === 'Enter') saveRemark()
        if (ev.key === 'Escape') remarkEdit.value = null
      },
      onBlur: saveRemark, onClick: (ev: MouseEvent) => ev.stopPropagation(), onDblclick: (ev: MouseEvent) => ev.stopPropagation(),
    })
  }
  const text = e.remark || autoText(e)
  return h('div', {
    class: ['remark', { auto: !e.remark && text, empty: !text }], title: text,
    onClick: (ev: MouseEvent) => (ev.stopPropagation(), editRemark(e)), onDblclick: (ev: MouseEvent) => ev.stopPropagation(),
  }, text || t('fm.add_remark'))
}

// ---- table ----
const rowOps = (e: FileEntry) => [
  ...(e.is_dir ? [] : [['edit', t('files.edit')]]),
  ['copy', t('fm.copy')], ['cut', t('fm.cut')], ['rename', t('files.rename')], ['chmod', t('files.permissions')],
  ['compress', t('files.compress')], ['delete', t('common.delete')],
]
const moreOps = (e: FileEntry): DropdownOption[] => [
  ...(e.is_dir ? [{ label: t('fm.open_tab'), key: 'tab' }] : [{ label: t('files.download'), key: 'download' }, { label: t('fx.share'), key: 'share' }]),
  ...(isArchive(e.name) ? [{ label: t('files.extract'), key: 'extract' }] : []),
  { label: t('files.owner'), key: 'chown' }, { label: t('fm.duplicate'), key: 'dup' }, { label: t('fm.properties'), key: 'props' },
]
const columns = computed<DataTableColumns<FileEntry>>(() => [
  {
    title: () => h(NCheckbox, {
      checked: !!shown.value.length && selected.value.length === shown.value.length,
      indeterminate: !!selected.value.length && selected.value.length < shown.value.length,
      'onUpdate:checked': (v: boolean) => (selected.value = v ? shown.value.map((i) => i.path) : []),
    }),
    key: 'sel',
    width: 40,
    render: (e) => h(NCheckbox, {
      checked: selected.value.includes(e.path),
      onClick: (ev: MouseEvent) => ev.stopPropagation(),
      'onUpdate:checked': (v: boolean) => (selected.value = v ? [...selected.value, e.path] : selected.value.filter((p) => p !== e.path)),
    }),
  },
  {
    title: () => t('files.name'),
    key: 'name',
    minWidth: 220,
    sorter: (a, b) => a.name.localeCompare(b.name),
    render: (e) =>
      h('span', { class: 'name' }, [
        h('span', { class: 'icon' }, iconOf(e.name, e.is_dir)),
        h('a', { class: 'fname', onClick: (ev: MouseEvent) => { if (pointer !== 'touch') { ev.stopPropagation(); open(e) } } }, e.name),
        e.link ? h('span', { class: 'muted' }, ` → ${e.link}`) : null,
      ]),
  },
  {
    title: () => t('fm.tamper'),
    key: 'protected',
    width: 96,
    render: (e) => h('a', { class: ['tamper', { on: e.protected }], onClick: (ev: MouseEvent) => (ev.stopPropagation(), router.push('/security?tab=tamper')) },
      e.protected ? `🔒 ${t('fm.protected')}` : t('fm.unprotected')),
  },
  { title: () => `${t('files.mode')} / ${t('files.owner')}`, key: 'mode', width: 120, render: (e) => `${e.mode.padStart(3, '0')}/${e.owner}` },
  { title: () => t('files.size'), key: 'size', width: 100, sorter: (a, b) => a.size - b.size, render: sizeCell },
  { title: () => t('files.mtime'), key: 'mtime', width: 160, sorter: (a, b) => a.mtime - b.mtime, render: (e) => datetime(e.mtime, locale.value) },
  { title: () => t('fm.remark'), key: 'remark', minWidth: 160, render: remarkCell },
  {
    title: () => t('common.actions'),
    key: 'ops',
    width: 330,
    align: 'right',
    render: (e) =>
      h('div', { class: 'ops' }, [
        ...rowOps(e).map(([k, label]) => h('a', { onClick: (ev: MouseEvent) => (ev.stopPropagation(), k === 'copy' || k === 'cut' || k === 'delete' || k === 'compress' || k === 'chmod' ? actOne(k, e) : act(k, e)) }, label)),
        h(NDropdown, { options: moreOps(e), trigger: 'click', onSelect: (k: string) => act(k, e) }, () => h('a', { onClick: (ev: MouseEvent) => ev.stopPropagation() }, `${t('fm.more')} ▾`)),
      ]),
  },
])
// Row buttons act on that row only.
function actOne(k: string, e: FileEntry) {
  selected.value = [e.path]
  act(k, e)
}
const rowProps = (e: FileEntry) => ({
  class: selected.value.includes(e.path) ? 'row-on' : '',
  style: 'cursor: default',
  onPointerdown: onPointerDown,
  onClick: (ev: MouseEvent) => clickItem(e, ev),
  onDblclick: () => open(e),
  onContextmenu: (ev: MouseEvent) => onContext(e, ev),
  onTouchstart: (ev: TouchEvent) => onTouchStart(e, ev),
  onTouchend: cancelPress,
  onTouchmove: cancelPress,
})

// ---- drop to upload ----
const dragging = ref(false)
let dragDepth = 0
function onDragEnter(ev: DragEvent) {
  if (!ev.dataTransfer?.types.includes('Files')) return
  dragDepth++
  dragging.value = true
}
function onDragLeave() {
  if (--dragDepth <= 0) {
    dragDepth = 0
    dragging.value = false
  }
}
async function onDrop(ev: DragEvent) {
  dragDepth = 0
  dragging.value = false
  if (!ev.dataTransfer) return
  const list = await droppedFiles(ev.dataTransfer)
  if (list.length) openUpload(cwd.value, list)
}

// ---- misc ----
const extras = ref<InstanceType<typeof FilesExtras> | null>(null)
function openFromExtras(p: string, dir: boolean) {
  if (dir) load(p)
  else openInEditor(p)
}
const preview = ref<FileEntry | null>(null)
const propsOf = ref<FileEntry | null>(null)
const images = computed(() => shown.value.filter((i) => !i.is_dir && isImage(i.name)))
function step(d: number) {
  const i = images.value.findIndex((x) => x.path === preview.value?.path)
  preview.value = images.value[(i + d + images.value.length) % images.value.length] ?? preview.value
}
const thumb = (e: FileEntry) => (isImage(e.name) && e.size < 4 << 20 ? `/api/v1/files/download?inline=1&path=${encodeURIComponent(e.path)}` : '')
const newMenu = computed(() => [{ label: t('files.new_folder'), key: 'mkdir' }, { label: t('files.new_file'), key: 'mkfile' }])
const upMenu = computed(() => [{ label: t('fm.upload_files'), key: 'upload' }, { label: t('fx.remote'), key: 'remote' }])
const moreMenu = computed(() => [
  { label: t('fm.select_all'), key: 'all' },
  { label: t('fm.paste'), key: 'paste', disabled: !clipboard.value },
  { label: t('fm.calc_all'), key: 'calc' },
  { type: 'divider', key: 'd' },
  { label: t('files.compress'), key: 'compress', disabled: !selected.value.length },
  { label: t('files.permissions'), key: 'chmod', disabled: !selected.value.length },
  { label: t('common.delete'), key: 'delete', disabled: !selected.value.length },
])
function onToolbar(k: string) {
  if (k === 'remote') return extras.value?.remote()
  if (k === 'calc') return calcAll()
  act(k, sel.value[0] ?? null)
}
onBeforeUnmount(cancelPress)
</script>

<template>
  <div
    class="browser" tabindex="0" @keydown="onKey" @dragenter.prevent="onDragEnter" @dragover.prevent @dragleave="onDragLeave" @drop.prevent="onDrop"
  >
    <div class="pathbar">
      <n-button size="small" quaternary :disabled="!history.length" :title="t('fm.back')" @click="back">←</n-button>
      <div v-if="!editingPath" class="crumbs" @click.self="editPath">
        <template v-for="(c, i) in crumbs" :key="c.path">
          <a class="crumb" @click="load(c.path)">{{ c.name }}</a>
          <span v-if="i < crumbs.length - 1 || true" class="sep" @click="editPath">›</span>
        </template>
        <span class="fill" @click="editPath" />
      </div>
      <n-input
        v-else ref="pathEl" v-model:value="pathText" size="small" class="crumbs-input"
        @keyup.enter="goPath" @keyup.esc="editingPath = false" @blur="editingPath = false"
      />
      <n-button size="small" quaternary :title="t('common.refresh')" @click="load(cwd, false)">⟳</n-button>
      <n-input v-model:value="search" size="small" clearable :placeholder="t('fm.search')" class="search" />
    </div>

    <div class="toolbar">
      <n-dropdown :options="upMenu" trigger="click" @select="onToolbar"><n-button size="small">⬆ {{ t('fm.updown') }} ▾</n-button></n-dropdown>
      <n-dropdown :options="newMenu" trigger="click" @select="onToolbar"><n-button size="small" type="primary" ghost>＋ {{ t('fm.new') }} ▾</n-button></n-dropdown>
      <FilesExtras ref="extras" :cwd="cwd" @open="openFromExtras" @changed="load(cwd, false)" />
      <n-button size="small" @click="router.push({ path: '/terminal', query: { cwd } })">⌨ {{ t('fm.terminal') }}</n-button>
      <n-button v-if="disk" size="small" quaternary :title="disk.mount">💽 {{ disk.mount }} {{ bytes(disk.free) }}</n-button>
      <n-dropdown :options="moreMenu" trigger="click" @select="onToolbar"><n-button size="small">{{ t('fm.more_functions') }} ▾</n-button></n-dropdown>
      <n-tag v-if="clipboard" size="small" closable type="info" @close="clipboard = null">
        <a @click="paste">{{ t(clipboard.mode === 'copy' ? 'fm.paste_copy' : 'fm.paste_cut', { n: clipboard.paths.length }) }}</a>
      </n-tag>
      <span class="grow" />
      <span v-if="selected.length" class="muted">{{ t('fm.selected_n', { n: selected.length }) }}</span>
      <n-button size="small" @click="emit('trash')">🗑 {{ t('files.trash') }}</n-button>
      <n-button-group size="small">
        <n-button :type="view === 'list' ? 'primary' : 'default'" :title="t('fm.list')" @click="view = 'list'">☰</n-button>
        <n-button :type="view === 'grid' ? 'primary' : 'default'" :title="t('fm.grid')" @click="view = 'grid'">▦</n-button>
      </n-button-group>
    </div>

    <div class="area" @contextmenu="onContext(null, $event)" @click.self="selected = []" @touchstart.self="onTouchStart(null, $event)" @touchend="cancelPress">
      <n-data-table
        v-if="view === 'list'" size="small" class="list" :columns="columns" :data="shown" :loading="loading"
        :row-key="(r: FileEntry) => r.path" :row-props="rowProps" :scroll-x="1180" :pagination="shown.length > 200 ? { pageSize: 200 } : false"
      />
      <div v-else class="grid" @click.self="selected = []">
        <div
          v-for="e in shown" :key="e.path" class="cell" :class="{ on: selected.includes(e.path) }" :title="e.remark || autoText(e) || e.name"
          @pointerdown="onPointerDown" @click="clickItem(e, $event)" @dblclick="open(e)" @contextmenu="onContext(e, $event)"
          @touchstart="onTouchStart(e, $event)" @touchend="cancelPress" @touchmove="cancelPress"
        >
          <img v-if="thumb(e)" :src="thumb(e)" loading="lazy" alt="" class="thumb" />
          <span v-else class="big">{{ iconOf(e.name, e.is_dir) }}</span>
          <span class="cname">{{ e.name }}</span>
        </div>
        <p v-if="!shown.length && !loading" class="muted">{{ t('fm.empty') }}</p>
      </div>
      <div v-if="dragging" class="drop">{{ t('fm.drop_here', { dir: cwd }) }}</div>
    </div>
    <p class="hint">{{ t('fm.hint') }}</p>

    <n-dropdown
      v-if="menu" :show="true" trigger="manual" placement="bottom-start" :x="menu.x" :y="menu.y" :options="menuOptions"
      @select="(k: string) => act(k)" @clickoutside="menu = null"
    />

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

    <n-modal :show="!!preview" @update:show="(v: boolean) => !v && (preview = null)">
      <n-card v-if="preview" :title="preview.name" style="width: min(1000px, calc(100vw - 32px))" closable @close="preview = null">
        <div class="viewer">
          <img :src="`/api/v1/files/download?inline=1&path=${encodeURIComponent(preview.path)}`" :alt="preview.name" />
        </div>
        <template #footer>
          <n-space justify="space-between" align="center">
            <span class="muted">{{ bytes(preview.size) }} · {{ preview.path }}</span>
            <n-space>
              <n-button :disabled="images.length < 2" @click="step(-1)">‹</n-button>
              <n-button :disabled="images.length < 2" @click="step(1)">›</n-button>
              <n-button @click="act('download', preview)">{{ t('files.download') }}</n-button>
            </n-space>
          </n-space>
        </template>
      </n-card>
    </n-modal>

    <n-modal :show="!!propsOf" @update:show="(v: boolean) => !v && (propsOf = null)">
      <n-card v-if="propsOf" :title="`${t('fm.properties')} · ${propsOf.name}`" style="width: min(520px, calc(100vw - 32px))" closable @close="propsOf = null">
        <dl class="props">
          <dt>{{ t('fm.location') }}</dt><dd>{{ propsOf.path }}</dd>
          <dt>{{ t('fm.type') }}</dt><dd>{{ propsOf.is_dir ? t('fm.folder') : t('fm.file') }}{{ propsOf.link ? ` → ${propsOf.link}` : '' }}</dd>
          <dt>{{ t('files.size') }}</dt>
          <dd>
            <template v-if="!propsOf.is_dir">{{ bytes(propsOf.size) }} ({{ propsOf.size.toLocaleString() }} B)</template>
            <component :is="() => sizeCell(propsOf!)" v-else />
          </dd>
          <dt>{{ t('files.mode') }}</dt><dd>{{ propsOf.mode.padStart(3, '0') }}</dd>
          <dt>{{ t('files.owner') }}</dt><dd>{{ propsOf.owner }}:{{ propsOf.group }}</dd>
          <dt>{{ t('files.mtime') }}</dt><dd>{{ datetime(propsOf.mtime, locale) }}</dd>
          <dt>{{ t('fm.remark') }}</dt><dd>{{ propsOf.remark || autoText(propsOf) || '—' }}</dd>
        </dl>
      </n-card>
    </n-modal>
  </div>
</template>

<style scoped>
.browser {
  display: flex;
  flex-direction: column;
  gap: 8px;
  outline: none;
  min-width: 0;
}
.pathbar {
  display: flex;
  align-items: center;
  gap: 4px;
  border: 1px solid rgba(128, 128, 128, 0.25);
  border-radius: 6px;
  padding: 2px 4px;
  min-width: 0;
}
.crumbs {
  flex: 1;
  display: flex;
  align-items: center;
  min-width: 0;
  overflow-x: auto;
  white-space: nowrap;
  cursor: text;
  scrollbar-width: none;
}
.crumbs-input {
  flex: 1;
  min-width: 0;
}
.crumb {
  padding: 2px 6px;
  border-radius: 4px;
  cursor: pointer;
  font-size: 13px;
}
.crumb:hover {
  background: rgba(32, 165, 58, 0.12);
}
.sep {
  opacity: 0.45;
  padding: 0 1px;
}
.fill {
  flex: 1;
  align-self: stretch;
  min-width: 24px;
}
.search {
  width: min(220px, 34vw);
}
.toolbar {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: center;
}
.grow {
  flex: 1;
}
.area {
  position: relative;
  min-height: 320px;
}
.list :deep(.row-on td) {
  background: rgba(32, 165, 58, 0.1) !important;
}
.list :deep(.ops) {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  white-space: nowrap;
  visibility: hidden;
}
.list :deep(tr:hover .ops),
.list :deep(.row-on .ops) {
  visibility: visible;
}
@media (hover: none) {
  .list :deep(.ops) {
    visibility: visible;
  }
}
.list :deep(.ops a),
.calc,
.tamper,
.list :deep(.calc),
.list :deep(.tamper) {
  color: #20a53a;
  cursor: pointer;
  font-size: 12px;
}
.list :deep(.tamper:not(.on)) {
  color: inherit;
  opacity: 0.55;
}
.list :deep(.name) {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  user-select: none;
}
.list :deep(.fname) {
  cursor: pointer;
}
.list :deep(.remark) {
  min-height: 20px;
  cursor: text;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.list :deep(.remark.auto) {
  opacity: 0.55;
}
.list :deep(.remark.empty) {
  opacity: 0;
}
.list :deep(tr:hover .remark.empty) {
  opacity: 0.35;
}
.list :deep(.remark-input) {
  width: 100%;
  font: inherit;
  padding: 2px 6px;
  border: 1px solid #20a53a;
  border-radius: 4px;
  background: transparent;
  color: inherit;
  outline: none;
}
.muted,
.list :deep(.muted) {
  opacity: 0.55;
  font-size: 12px;
}
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(104px, 1fr));
  gap: 8px;
  min-height: 320px;
  align-content: start;
}
.cell {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  padding: 10px 6px;
  border-radius: 8px;
  border: 1px solid transparent;
  cursor: default;
  user-select: none;
  -webkit-user-select: none;
  -webkit-touch-callout: none;
}
.cell:hover {
  background: rgba(128, 128, 128, 0.08);
}
.cell.on {
  border-color: #20a53a;
  background: rgba(32, 165, 58, 0.1);
}
.big {
  font-size: 40px;
  line-height: 56px;
}
.thumb {
  width: 64px;
  height: 56px;
  object-fit: cover;
  border-radius: 4px;
}
.cname {
  font-size: 12px;
  text-align: center;
  word-break: break-all;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.drop {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  border: 2px dashed #20a53a;
  border-radius: 8px;
  background: rgba(32, 165, 58, 0.08);
  font-size: 18px;
  pointer-events: none;
  text-align: center;
  padding: 16px;
}
.hint {
  font-size: 12px;
  opacity: 0.55;
  margin: 0;
}
.viewer {
  display: flex;
  justify-content: center;
  background: repeating-conic-gradient(rgba(128, 128, 128, 0.15) 0 25%, transparent 0 50%) 0 0 / 16px 16px;
  border-radius: 6px;
}
.viewer img {
  max-width: 100%;
  max-height: 70dvh;
  object-fit: contain;
}
.props {
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr);
  gap: 6px 16px;
  margin: 0;
}
.props dt {
  opacity: 0.6;
}
.props dd {
  margin: 0;
  word-break: break-all;
}
</style>
