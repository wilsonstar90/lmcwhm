<script setup lang="ts">
// One file manager tab (like aaPanel): path bar, toolbar, list or grid,
// click to select / double-click to open, context menu, inline remarks,
// directory sizes, clipboard and drop-to-upload.
import { computed, h, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAsk } from '../../composables/ask'
import { useRouter } from 'vue-router'
import {
  NButton, NButtonGroup, NCard, NCheckbox, NDataTable, NDropdown, NForm, NFormItem, NInput, NModal, NRadioButton, NRadioGroup,
  NSelect, NSpace, NTag, useDialog, useMessage, type DataTableColumns, type DropdownOption,
} from 'naive-ui'
import { get, post, put, ApiError } from '../../api/client'
import type { FileEntry } from '../../api/types'
import { bytes, datetime } from '../../format'
import FilesExtras from '../FilesExtras.vue'
import FileIcon from './FileIcon.vue'
import RemarkCell from '../RemarkCell.vue'
import { Icon, icon } from '../icons'
import {
  baseName, changed, clipboard, droppedFiles, isArchive, isImage, join, openInEditor, openUpload, parentOf,
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
  // Keyboard shortcuts (copy, paste, delete) work on the list: take focus
  // from the search or path box that may still hold it.
  const active = document.activeElement as HTMLElement | null
  if (active?.closest('input, textarea')) active.blur()
  root.value?.focus({ preventScroll: true })
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
      { label: t('files.new_folder'), key: 'mkdir', icon: icon('folder-plus') },
      { label: t('files.new_file'), key: 'mkfile', icon: icon('file-plus') },
      { label: t('fm.paste'), key: 'paste', disabled: !clipboard.value, icon: icon('paste') },
      { label: t('files.upload'), key: 'upload', icon: icon('upload') },
      { type: 'divider', key: 'd1' },
      { label: t('fm.select_all'), key: 'all', icon: icon('check') },
      { label: t('common.refresh'), key: 'refresh', icon: icon('refresh') },
    ]
  }
  const many = selected.value.length > 1
  if (many) {
    return [
      { label: t('fm.copy'), key: 'copy', icon: icon('copy') },
      { label: t('fm.cut'), key: 'cut', icon: icon('cut') },
      { label: t('files.compress'), key: 'compress', icon: icon('archive') },
      { label: `${t('common.delete')} (${selected.value.length})`, key: 'delete', icon: icon('trash') },
      { type: 'divider', key: 'd' },
      { label: t('fm.properties'), key: 'props', icon: icon('info') },
    ]
  }
  const arc = isArchive(e.name)
  return [
    { label: t('fm.open'), key: 'open', icon: icon(e.is_dir ? 'folder-open' : arc ? 'extract' : 'file') },
    ...(e.is_dir
      ? [{ label: t('fm.open_tab'), key: 'tab', icon: icon('tab') }]
      : [{
          label: t('fm.open_with'), key: 'with', icon: icon('window'),
          children: [{ label: t('fm.editor'), key: 'edit', icon: icon('edit') }, { label: t('files.download'), key: 'download', icon: icon('download') }],
        }]),
    { type: 'divider', key: 'd1' },
    ...(e.is_dir ? [] : [{ label: t('files.download'), key: 'download', icon: icon('download') }]),
    ...(e.is_dir ? [{ label: t('fx.fav'), key: 'fav', icon: icon('star') }] : []),
    ...(e.is_dir ? [] : [{ label: t('fx.share'), key: 'share', icon: icon('link') }]),
    { type: 'divider', key: 'd2' },
    { label: t('fm.copy'), key: 'copy', icon: icon('copy') },
    { label: t('fm.cut'), key: 'cut', icon: icon('cut') },
    { label: t('files.rename'), key: 'rename', icon: icon('rename') },
    { label: t('common.delete'), key: 'delete', icon: icon('trash') },
    { type: 'divider', key: 'd3' },
    arc ? { label: t('files.extract'), key: 'extract', icon: icon('extract') } : { label: t('files.compress'), key: 'compress', icon: icon('archive') },
    { label: t('fm.duplicate'), key: 'dup', icon: icon('duplicate') },
    { label: t('fm.remark'), key: 'remark', icon: icon('remark') },
    { label: t('fm.properties'), key: 'props', icon: icon('info') },
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
    case 'props':
      return openProps(paths)
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
    case 'rename':
      return rename(e)
    case 'dup':
      return duplicate(e)
    case 'remark':
      return editRemark(e)
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
    if (e !== CANCELLED) fail(e)
    return false
  }
}

// ---- name conflicts (paste, move, copy, extract) ----
const dialog = useDialog()
const root = ref<HTMLElement | null>(null)
const ask = useAsk()
const CANCELLED = new Error('cancelled')
type Choice = 'overwrite' | 'skip' | 'both'
/** Asks what to do with paths that already exist; null = cancel. */
function askConflict(paths: string[], keepBoth: boolean): Promise<Choice | null> {
  return new Promise((resolve) => {
    let done = false
    const finish = (c: Choice | null) => {
      if (done) return
      done = true
      resolve(c)
      d.destroy()
    }
    const d = dialog.warning({
      title: t('fm.conflict.title', { n: paths.length }),
      style: 'width: min(520px, calc(100vw - 32px))',
      content: () =>
        h('div', [
          h('p', { style: 'margin: 0 0 8px' }, t('fm.conflict.desc')),
          h('ul', { class: 'conflicts' }, paths.slice(0, 8).map((p) => h('li', { title: p }, p))),
          paths.length > 8 ? h('p', { style: 'margin: 6px 0 0; opacity: .7' }, t('fm.conflict.more', { n: paths.length - 8 })) : null,
        ]),
      action: () =>
        h(NSpace, { justify: 'end', size: 8 }, () => [
          h(NButton, { onClick: () => finish(null) }, () => t('common.cancel')),
          h(NButton, { onClick: () => finish('skip') }, () => t('fm.conflict.skip')),
          keepBoth ? h(NButton, { onClick: () => finish('both') }, () => t('fm.conflict.both')) : null,
          h(NButton, { type: 'warning', onClick: () => finish('overwrite') }, () => t('fm.conflict.overwrite')),
        ]),
      onClose: () => finish(null),
      onMaskClick: () => finish(null),
    })
  })
}
type Prompt = {
  title: string
  busy?: string // message shown while submit runs (compress / extract)
  fields: { key: string; label: string; value: string }[]
  recursive?: boolean
  format?: ArchiveFormat
  submit: (v: Record<string, string>, p: Prompt) => Promise<unknown>
}
type ArchiveFormat = 'zip' | 'tar.gz' | '7z' | 'rar'
const FORMATS: ArchiveFormat[] = ['zip', 'tar.gz', '7z', 'rar']
const prompt = ref<Prompt | null>(null)
const running = ref(false)
async function submitPrompt() {
  const p = prompt.value!
  if (running.value) return
  const v = Object.fromEntries(p.fields.map((f) => [f.key, f.value.trim()]))
  running.value = true
  // Big archives take a while: say so until the server answers.
  const note = p.busy ? msg.loading(p.busy, { duration: 0 }) : null
  try {
    if (await call(() => p.submit(v, p))) prompt.value = null
  } finally {
    note?.destroy()
    running.value = false
  }
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
function compress(paths: string[]) {
  const base = paths.length === 1 ? baseName(paths[0]) : 'archive'
  prompt.value = {
    title: t('files.compress'),
    busy: t('fm.compressing'),
    fields: [{ key: 'dest', label: t('files.target'), value: join(cwd.value, `${base}.zip`) }],
    format: 'zip',
    submit: (v, p) => post('/files/compress', { paths, dest: v.dest, format: p.format }),
  }
}
function setFormat(f: ArchiveFormat) {
  const p = prompt.value!
  p.format = f
  p.fields[0].value = p.fields[0].value.replace(/\.(zip|tar\.gz|7z|rar)$/, '') + '.' + f
}
function extract(e: FileEntry) {
  prompt.value = {
    title: `${t('files.extract')} · ${e.name}`,
    busy: t('fm.extracting'),
    fields: [{ key: 'dest', label: t('files.target'), value: cwd.value }],
    submit: async (v) => {
      const { items } = await post<{ items: string[] }>('/files/extract/conflicts', { path: e.path, dest: v.dest })
      let skip = false
      if (items.length) {
        const c = await askConflict(items, false)
        if (!c) throw CANCELLED
        skip = c === 'skip'
      }
      return post('/files/extract', { path: e.path, dest: v.dest, skip_existing: skip })
    },
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
    const moves = c.paths
      .map((from) => {
        let name = baseName(from)
        if (c.mode === 'copy' && parentOf(from) === cwd.value) name = uniqueName(name)
        return { from, to: join(cwd.value, name) }
      })
      .filter((m) => m.from !== m.to)
    const { items } = await post<{ items: string[] }>('/files/conflicts', { paths: moves.map((m) => m.to) })
    const taken = new Set(items)
    let choice: Choice = 'overwrite'
    if (taken.size) {
      const a = await askConflict(items, true)
      if (!a) return
      choice = a
    }
    let skipped = 0
    for (const m of moves) {
      let to = m.to
      let overwrite = false
      if (taken.has(to)) {
        if (choice === 'skip') {
          skipped++
          continue
        }
        if (choice === 'both') to = join(cwd.value, uniqueName(baseName(to)))
        else overwrite = true
      }
      await post(c.mode === 'cut' ? '/files/rename' : '/files/copy', { from: m.from, to, overwrite })
    }
    if (c.mode === 'cut') clipboard.value = null
    msg.success(skipped ? t('fm.conflict.skipped', { n: skipped }) : t('common.done'))
  } catch (e) {
    fail(e)
  }
  load(cwd.value, false)
}
async function remove(paths: string[], permanent = false) {
  if (!paths.length) return
  if (!(await ask.confirm(permanent ? t('files.delete_permanent_confirm', { n: paths.length }) : t('files.delete_confirm', { n: paths.length }), { danger: true }))) return
  try {
    await post('/files/delete', { paths, permanent })
    msg.success(t('common.done'))
  } catch (e) {
    if (e instanceof ApiError && e.code === 'files.trash_cross_device' && (await ask.confirm(t('files.cross_device_confirm'), { danger: true }))) return remove(paths, true)
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
const remarkOpen = ref('')
const autoText = (e: FileEntry) => (e.auto && te(`fm.auto.${e.auto.code}`) ? t(`fm.auto.${e.auto.code}`, { arg: e.auto.arg ?? '' }) : '')
function editRemark(e: FileEntry) {
  remarkOpen.value = e.path
}
function remarkCell(e: FileEntry) {
  return h(RemarkCell, {
    value: e.remark ?? '', auto: autoText(e), maxlength: 500, open: remarkOpen.value === e.path,
    onClose: () => (remarkOpen.value = ''),
    save: (v: string) => put('/files/remark', { path: e.path, remark: v }).then(() => (e.remark = v)),
  })
}

// ---- table ----
// Archives offer 解壓 where other entries offer 壓縮.
const rowOps = (e: FileEntry) => [
  ...(e.is_dir || isArchive(e.name) ? [] : [['edit', t('files.edit')]]),
  ['copy', t('fm.copy')], ['cut', t('fm.cut')], ['rename', t('files.rename')],
  isArchive(e.name) ? ['extract', t('files.extract')] : ['compress', t('files.compress')],
  ['props', t('fm.properties')], ['delete', t('common.delete')],
]
const moreOps = (e: FileEntry): DropdownOption[] => [
  ...(e.is_dir
    ? [{ label: t('fm.open_tab'), key: 'tab', icon: icon('tab') }, { label: t('fx.fav'), key: 'fav', icon: icon('star') }]
    : [{ label: t('files.download'), key: 'download', icon: icon('download') }, { label: t('fx.share'), key: 'share', icon: icon('link') }]),
  ...(isArchive(e.name) ? [{ label: t('files.compress'), key: 'compress', icon: icon('archive') }] : []),
  ...(!e.is_dir && isArchive(e.name) ? [{ label: t('fm.editor'), key: 'edit', icon: icon('edit') }] : []),
  { label: t('fm.duplicate'), key: 'dup', icon: icon('duplicate') }, { label: t('fm.remark'), key: 'remark', icon: icon('remark') },
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
        h(FileIcon, { class: 'icon', name: e.name, dir: e.is_dir, size: 20 }),
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
        ...rowOps(e).map(([k, label]) => h('a', { onClick: (ev: MouseEvent) => (ev.stopPropagation(), k === 'copy' || k === 'cut' || k === 'delete' || k === 'compress' || k === 'props' ? actOne(k, e) : act(k, e)) }, label)),
        h(NDropdown, { options: moreOps(e), trigger: 'click', onSelect: (k: string) => (k === 'compress' ? actOne(k, e) : act(k, e)) }, () =>
          h('a', { class: 'more', onClick: (ev: MouseEvent) => ev.stopPropagation() }, [t('fm.more'), h(Icon, { name: 'chevron', class: 'caret' })])),
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

// ---- properties: details, permissions and owner (one or several entries) ----
type Props = { items: FileEntry[]; mode: string; owner: string; group: string; start: string; recursive: boolean; saving: boolean }
const propsOf = ref<Props | null>(null)
function openProps(paths: string[]) {
  const list = paths.map((p) => byPath.value.get(p)).filter((e): e is FileEntry => !!e)
  if (!list.length) return
  const e = list[0]
  const mode = e.mode.padStart(3, '0')
  propsOf.value = { items: list, mode, owner: e.owner, group: e.group, start: `${mode}|${e.owner}|${e.group}`, recursive: false, saving: false }
}
const propOne = computed(() => (propsOf.value?.items.length === 1 ? propsOf.value.items[0] : null))
const propDirs = computed(() => !!propsOf.value?.items.some((i) => i.is_dir))
const propBytes = computed(() => propsOf.value?.items.reduce((n, i) => n + (i.is_dir ? 0 : i.size), 0) ?? 0)
const modeOk = computed(() => /^[0-7]{3,4}$/.test(propsOf.value?.mode ?? ''))
const PERM_WHO = [['fm.p_user', 6], ['fm.p_group', 3], ['fm.p_other', 0]] as const
const PERM_BITS = [['fm.p_read', 4], ['fm.p_write', 2], ['fm.p_exec', 1]] as const
const modeVal = () => (modeOk.value ? parseInt(propsOf.value!.mode, 8) : 0)
const hasBit = (who: number, b: number) => !!((modeVal() >> who) & b)
function setBit(who: number, b: number, on: boolean) {
  const p = propsOf.value!
  const v = on ? modeVal() | (b << who) : modeVal() & ~(b << who)
  p.mode = v.toString(8).padStart(p.mode.length === 4 ? 4 : 3, '0')
}
const ownerOptions = ['www-data', 'root', 'nobody'].map((v) => ({ label: v, value: v }))
async function saveProps() {
  const p = propsOf.value
  if (!p || !modeOk.value) return
  const [mode0, owner0, group0] = p.start.split('|')
  const chmod = p.mode !== mode0 || p.recursive
  const chown = !!p.owner && (p.owner !== owner0 || p.group !== group0 || p.recursive)
  if (!chmod && !chown) return (propsOf.value = null)
  p.saving = true
  const ok = await call(async () => {
    for (const e of p.items) {
      const recursive = p.recursive && e.is_dir
      if (chmod) await post('/files/chmod', { path: e.path, mode: p.mode, recursive })
      if (chown) await post('/files/chown', { path: e.path, owner: p.owner, group: p.group || p.owner, recursive })
    }
  })
  p.saving = false
  if (ok) propsOf.value = null
}
const images = computed(() => shown.value.filter((i) => !i.is_dir && isImage(i.name)))
function step(d: number) {
  const i = images.value.findIndex((x) => x.path === preview.value?.path)
  preview.value = images.value[(i + d + images.value.length) % images.value.length] ?? preview.value
}
const thumb = (e: FileEntry) => (isImage(e.name) && e.size < 4 << 20 ? `/api/v1/files/download?inline=1&path=${encodeURIComponent(e.path)}` : '')
const newMenu = computed(() => [
  { label: t('files.new_folder'), key: 'mkdir', icon: icon('folder-plus') },
  { label: t('files.new_file'), key: 'mkfile', icon: icon('file-plus') },
])
const upMenu = computed(() => [
  { label: t('fm.upload_files'), key: 'upload', icon: icon('upload') },
  { label: t('fx.remote'), key: 'remote', icon: icon('globe') },
])
const moreMenu = computed(() => [
  { label: t('fm.select_all'), key: 'all', icon: icon('check') },
  { label: t('fm.paste'), key: 'paste', disabled: !clipboard.value, icon: icon('paste') },
  { label: t('fm.calc_all'), key: 'calc', icon: icon('calc') },
  { type: 'divider', key: 'd' },
  { label: t('files.compress'), key: 'compress', disabled: !selected.value.length, icon: icon('archive') },
  { label: t('fm.properties'), key: 'props', disabled: !selected.value.length, icon: icon('info') },
  { label: t('common.delete'), key: 'delete', disabled: !selected.value.length, icon: icon('trash') },
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
    ref="root" class="browser" tabindex="0" @keydown="onKey" @dragenter.prevent="onDragEnter" @dragover.prevent @dragleave="onDragLeave" @drop.prevent="onDrop"
  >
    <div class="pathbar">
      <n-button size="small" quaternary :disabled="!history.length" :title="t('fm.back')" @click="back"><Icon name="back" /></n-button>
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
      <n-button size="small" quaternary :title="t('common.refresh')" @click="load(cwd, false)"><Icon name="refresh" /></n-button>
      <n-input v-model:value="search" size="small" clearable :placeholder="t('fm.search')" class="search" />
    </div>

    <div class="toolbar">
      <n-dropdown :options="upMenu" trigger="click" @select="onToolbar">
        <n-button size="small"><template #icon><Icon name="updown" /></template>{{ t('fm.updown') }}<Icon name="chevron" class="caret" /></n-button>
      </n-dropdown>
      <n-dropdown :options="newMenu" trigger="click" @select="onToolbar">
        <n-button size="small" type="primary" ghost><template #icon><Icon name="plus" /></template>{{ t('fm.new') }}<Icon name="chevron" class="caret" /></n-button>
      </n-dropdown>
      <FilesExtras ref="extras" :cwd="cwd" @open="openFromExtras" @changed="load(cwd, false)" />
      <n-button size="small" @click="router.push({ path: '/terminal', query: { cwd } })"><template #icon><Icon name="terminal" /></template>{{ t('fm.terminal') }}</n-button>
      <n-button v-if="disk" size="small" quaternary :title="disk.mount"><template #icon><Icon name="disk" /></template>{{ disk.mount }} {{ bytes(disk.free) }}</n-button>
      <n-dropdown :options="moreMenu" trigger="click" @select="onToolbar">
        <n-button size="small"><template #icon><Icon name="more" /></template>{{ t('fm.more_functions') }}<Icon name="chevron" class="caret" /></n-button>
      </n-dropdown>
      <n-tag v-if="clipboard" size="small" closable type="info" @close="clipboard = null">
        <a @click="paste">{{ t(clipboard.mode === 'copy' ? 'fm.paste_copy' : 'fm.paste_cut', { n: clipboard.paths.length }) }}</a>
      </n-tag>
      <span class="grow" />
      <span v-if="selected.length" class="muted">{{ t('fm.selected_n', { n: selected.length }) }}</span>
      <n-button size="small" @click="emit('trash')"><template #icon><Icon name="trash" /></template>{{ t('files.trash') }}</n-button>
      <n-button-group size="small">
        <n-button :type="view === 'list' ? 'primary' : 'default'" :title="t('fm.list')" @click="view = 'list'"><Icon name="list" /></n-button>
        <n-button :type="view === 'grid' ? 'primary' : 'default'" :title="t('fm.grid')" @click="view = 'grid'"><Icon name="grid" /></n-button>
      </n-button-group>
    </div>

    <div class="area" @contextmenu="onContext(null, $event)" @click.self="selected = []" @touchstart.self="onTouchStart(null, $event)" @touchend="cancelPress">
      <n-data-table
        v-if="view === 'list'" size="small" class="list" flex-height :columns="columns" :data="shown" :loading="loading"
        :row-key="(r: FileEntry) => r.path" :row-props="rowProps" :scroll-x="1180" :pagination="shown.length > 200 ? { pageSize: 200 } : false"
      />
      <div v-else class="grid" @click.self="selected = []">
        <div
          v-for="e in shown" :key="e.path" class="cell" :class="{ on: selected.includes(e.path) }" :title="e.remark || autoText(e) || e.name"
          @pointerdown="onPointerDown" @click="clickItem(e, $event)" @dblclick="open(e)" @contextmenu="onContext(e, $event)"
          @touchstart="onTouchStart(e, $event)" @touchend="cancelPress" @touchmove="cancelPress"
        >
          <img v-if="thumb(e)" :src="thumb(e)" loading="lazy" alt="" class="thumb" />
          <FileIcon v-else class="big" :name="e.name" :dir="e.is_dir" :size="48" />
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

    <n-modal :show="!!prompt" :mask-closable="!running" @update:show="(v: boolean) => !v && !running && (prompt = null)">
      <n-card v-if="prompt" :title="prompt.title" style="width: min(520px, calc(100vw - 32px))" :closable="!running" @close="prompt = null">
        <n-form label-placement="top" @submit.prevent="submitPrompt">
          <n-form-item v-for="f in prompt.fields" :key="f.key" :label="f.label">
            <n-input v-model:value="f.value" @keyup.enter="submitPrompt" />
          </n-form-item>
          <n-radio-group v-if="prompt.format" :value="prompt.format" @update:value="setFormat">
            <n-radio-button v-for="x in FORMATS" :key="x" :value="x" :disabled="running">{{ x }}</n-radio-button>
          </n-radio-group>
          <n-checkbox v-if="prompt.recursive !== undefined" v-model:checked="prompt.recursive">{{ t('files.recursive') }}</n-checkbox>
        </n-form>
        <template #footer>
          <n-space justify="end">
            <n-button :disabled="running" @click="prompt = null">{{ t('common.cancel') }}</n-button>
            <n-button type="primary" :loading="running" @click="submitPrompt">{{ t('common.confirm') }}</n-button>
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
      <n-card
        v-if="propsOf" :title="`${t('fm.properties')} · ${propOne ? propOne.name : t('fm.n_items', { n: propsOf.items.length })}`"
        style="width: min(560px, calc(100vw - 32px))" closable @close="propsOf = null"
      >
        <dl v-if="propOne" class="props">
          <dt>{{ t('fm.location') }}</dt><dd>{{ propOne.path }}</dd>
          <dt>{{ t('fm.type') }}</dt><dd>{{ propOne.is_dir ? t('fm.folder') : t('fm.file') }}{{ propOne.link ? ` → ${propOne.link}` : '' }}</dd>
          <dt>{{ t('files.size') }}</dt>
          <dd>
            <template v-if="!propOne.is_dir">{{ bytes(propOne.size) }} ({{ propOne.size.toLocaleString() }} B)</template>
            <component :is="() => sizeCell(propOne!)" v-else />
          </dd>
          <dt>{{ t('files.mtime') }}</dt><dd>{{ datetime(propOne.mtime, locale) }}</dd>
          <dt>{{ t('fm.remark') }}</dt><dd>{{ propOne.remark || autoText(propOne) || '—' }}</dd>
        </dl>
        <dl v-else class="props">
          <dt>{{ t('fm.location') }}</dt><dd>{{ cwd }}</dd>
          <dt>{{ t('files.size') }}</dt><dd>{{ bytes(propBytes) }}</dd>
        </dl>

        <h4 class="sec"><Icon name="lock" /> {{ t('files.permissions') }}</h4>
        <div class="perm">
          <table class="bits">
            <thead>
              <tr><th /><th v-for="[k] in PERM_BITS" :key="k">{{ t(k) }}</th></tr>
            </thead>
            <tbody>
              <tr v-for="[wk, who] in PERM_WHO" :key="wk">
                <th>{{ t(wk) }}</th>
                <td v-for="[bk, b] in PERM_BITS" :key="bk">
                  <n-checkbox :checked="hasBit(who, b)" :disabled="!modeOk" @update:checked="(v: boolean) => setBit(who, b, v)" />
                </td>
              </tr>
            </tbody>
          </table>
          <n-input v-model:value="propsOf.mode" size="small" :maxlength="4" class="mode" :status="modeOk ? undefined : 'error'" />
        </div>

        <h4 class="sec"><Icon name="user" /> {{ t('files.owner') }}</h4>
        <div class="own">
          <n-select v-model:value="propsOf.owner" size="small" filterable tag :options="ownerOptions" :placeholder="t('files.owner')" />
          <span>:</span>
          <n-select v-model:value="propsOf.group" size="small" filterable tag :options="ownerOptions" :placeholder="t('files.group')" />
        </div>
        <n-checkbox v-if="propDirs" v-model:checked="propsOf.recursive" style="margin-top: 12px">{{ t('files.recursive') }}</n-checkbox>

        <template #footer>
          <n-space justify="end">
            <n-button @click="propsOf = null">{{ t('common.cancel') }}</n-button>
            <n-button type="primary" :loading="propsOf.saving" :disabled="!modeOk" @click="saveProps">{{ t('common.save') }}</n-button>
          </n-space>
        </template>
      </n-card>
    </n-modal>
  </div>
</template>

<style>
/* The conflict dialog renders outside this component. */
ul.conflicts {
  margin: 0;
  padding: 8px 12px 8px 28px;
  max-height: 180px;
  overflow: auto;
  background: rgba(128, 128, 128, 0.08);
  border-radius: 6px;
  font-size: 12px;
  word-break: break-all;
}
</style>

<style scoped>
.browser {
  display: flex;
  flex-direction: column;
  gap: 8px;
  outline: none;
  min-width: 0;
  flex: 1;
  min-height: 0;
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
  flex: 1;
  min-height: 200px;
  display: flex;
  flex-direction: column;
}
.list {
  flex: 1;
  min-height: 0;
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
.muted,
.list :deep(.muted) {
  opacity: 0.55;
  font-size: 12px;
}
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(104px, 1fr));
  gap: 8px;
  flex: 1;
  min-height: 0;
  overflow: auto;
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
  margin: 4px 0;
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
.sec {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 18px 0 8px;
  font-size: 14px;
  font-weight: 600;
}
.perm {
  display: flex;
  align-items: center;
  gap: 16px;
  flex-wrap: wrap;
}
.bits {
  border-collapse: collapse;
  font-size: 13px;
}
.bits th,
.bits td {
  padding: 4px 10px;
  text-align: center;
  font-weight: normal;
}
.bits thead th {
  opacity: 0.6;
}
.bits tbody th {
  text-align: left;
  padding-left: 0;
}
.mode {
  width: 80px;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}
.own {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto minmax(0, 1fr);
  gap: 8px;
  align-items: center;
}
.caret,
.list :deep(.caret) {
  margin-left: 4px;
  font-size: 12px;
  opacity: 0.7;
}
.list :deep(.more) {
  display: inline-flex;
  align-items: center;
}
</style>
