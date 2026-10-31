<script setup lang="ts">
// The online text editor window (like aaPanel's 在線文本編輯器): a floating
// window that can be moved, maximised and minimised, with a folder tree,
// one tab per file, save / save all, search, replace, go to line, font size,
// theme, a status bar and the saved versions of each file.
import { computed, h, nextTick, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NCard, NDataTable, NDropdown, NEmpty, NInput, NModal, NPopconfirm, NSpace, NTree, useMessage, type TreeOption } from 'naive-ui'
import { get, post, put, ApiError } from '../../api/client'
import type { FileEntry } from '../../api/types'
import { bytes, datetime } from '../../format'
import { CodeEditor } from '../code-editor'
import { baseName, editorBus, iconOf, join, parentOf } from './state'

const { t, locale } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))

interface Doc { path: string; content: string; saved: string; versions: number; line: number; col: number; loading: boolean }
const docs = ref<Doc[]>([])
const active = ref('')
const cur = computed(() => docs.value.find((d) => d.path === active.value) ?? null)
const visible = ref(false)
const state = ref<'normal' | 'max' | 'min'>('normal')
const narrow = ref(window.matchMedia('(max-width: 767px)').matches)
window.matchMedia('(max-width: 767px)').addEventListener?.('change', (e) => (narrow.value = e.matches))
const prefs = reactive((() => {
  try {
    return { font: 13, theme: 'dark' as 'auto' | 'light' | 'dark', tree: true, ...JSON.parse(localStorage.getItem('lmcwhm.editor') ?? '{}') }
  } catch {
    return { font: 13, theme: 'dark' as 'auto' | 'light' | 'dark', tree: true }
  }
})())
watch(prefs, (p) => {
  try {
    localStorage.setItem('lmcwhm.editor', JSON.stringify(p))
  } catch {
    /* private mode */
  }
})

// Files the browsers ask to open.
watch(() => editorBus.seq, () => {
  for (const p of editorBus.open.splice(0)) openFile(p)
})
async function openFile(path: string) {
  visible.value = true
  if (state.value === 'min') state.value = 'normal'
  if (!docs.value.some((d) => d.path === path)) {
    const d: Doc = { path, content: '', saved: '', versions: 0, line: 1, col: 1, loading: true }
    docs.value.push(d)
    if (!treeDir.value) treeDir.value = parentOf(path)
    try {
      const r = await get<{ content: string }>(`/files/read?path=${encodeURIComponent(path)}`)
      const doc = docs.value.find((x) => x.path === path)!
      doc.content = doc.saved = r.content
      doc.loading = false
      countVersions(doc)
    } catch (e) {
      fail(e)
      docs.value = docs.value.filter((x) => x.path !== path)
      if (!docs.value.length) visible.value = false
      return
    }
  }
  active.value = path
}
async function countVersions(d: Doc) {
  try {
    d.versions = (await get<{ items: unknown[] }>(`/files/history?path=${encodeURIComponent(d.path)}`)).items.length
  } catch {
    d.versions = 0
  }
}
const dirty = (d: Doc) => d.content !== d.saved
async function save(d = cur.value) {
  if (!d || !dirty(d)) return
  try {
    await put('/files/write', { path: d.path, content: d.content })
    d.saved = d.content
    countVersions(d)
    msg.success(`${t('common.saved')} · ${baseName(d.path)}`)
  } catch (e) {
    fail(e)
  }
}
async function saveAll() {
  for (const d of docs.value.filter(dirty)) await save(d)
}
async function reload() {
  const d = cur.value
  if (!d || (dirty(d) && !confirm(t('fe.discard', { name: baseName(d.path) })))) return
  try {
    const r = await get<{ content: string }>(`/files/read?path=${encodeURIComponent(d.path)}`)
    d.content = d.saved = r.content
  } catch (e) {
    fail(e)
  }
}
function closeDoc(path: string) {
  const d = docs.value.find((x) => x.path === path)
  if (d && dirty(d) && !confirm(t('fe.discard', { name: baseName(d.path) }))) return
  const i = docs.value.findIndex((x) => x.path === path)
  docs.value.splice(i, 1)
  if (active.value === path) active.value = docs.value[Math.max(0, i - 1)]?.path ?? ''
  if (!docs.value.length) visible.value = false
}
function closeAll() {
  if (docs.value.some(dirty) && !confirm(t('fe.discard_all'))) return
  docs.value = []
  visible.value = false
}

// ---- toolbar ----
const ed = ref<{ search: () => void; gotoLine: () => void; focus: () => void } | null>(null)
const fonts = [12, 13, 14, 15, 16, 18, 20].map((n) => ({ label: `${n}px`, key: n }))
const themes = computed(() => [
  { label: t('theme.auto'), key: 'auto' }, { label: t('theme.light'), key: 'light' }, { label: t('theme.dark'), key: 'dark' },
])
const showKeys = ref(false)
const keys = computed(() => [
  ['Ctrl/⌘ + S', t('common.save')], ['Ctrl/⌘ + F', t('fe.search')], ['Ctrl/⌘ + Alt + F / Ctrl+H', t('fe.replace')],
  ['Ctrl/⌘ + Alt + G', t('fe.goto')], ['Ctrl/⌘ + Z / Shift+Z', t('fe.undo_redo')], ['Ctrl/⌘ + /', t('fe.comment')],
  ['Tab / Shift+Tab', t('fe.indent')], ['Alt + ↑ / ↓', t('fe.move_line')],
])
const eol = computed(() => (cur.value?.content.includes('\r\n') ? 'CRLF' : 'LF'))
const indent = computed(() => {
  const m = cur.value?.content.match(/^( +|\t)\S/m)
  return m ? (m[1] === '\t' ? 'Tab' : `${t('fe.spaces')}: ${Math.min(m[1].length, 8)}`) : `${t('fe.spaces')}: 4`
})
const langName = computed(() => {
  const n = cur.value ? baseName(cur.value.path) : ''
  const ext = n.includes('.') ? n.split('.').pop()!.toUpperCase() : 'TEXT'
  return ({ JS: 'JavaScript', TS: 'TypeScript', PY: 'Python', MD: 'Markdown', YML: 'YAML', CONF: 'Nginx', SH: 'Shell' } as Record<string, string>)[ext] ?? ext
})

// ---- history ----
interface Version { id: string; time: number; size: number }
const hist = ref<{ path: string; items: Version[]; view?: { id: string; content: string } } | null>(null)
async function openHistory() {
  const d = cur.value
  if (!d) return
  try {
    hist.value = { path: d.path, items: (await get<{ items: Version[] }>(`/files/history?path=${encodeURIComponent(d.path)}`)).items }
  } catch (e) {
    fail(e)
  }
}
async function viewVersion(v: Version) {
  try {
    const r = await get<{ content: string }>(`/files/history/read?path=${encodeURIComponent(hist.value!.path)}&id=${v.id}`)
    hist.value!.view = { id: v.id, content: r.content }
  } catch (e) {
    fail(e)
  }
}
async function restoreVersion(id: string) {
  const path = hist.value!.path
  try {
    await post('/files/history/restore', { path, id })
    const d = docs.value.find((x) => x.path === path)
    if (d) {
      const r = await get<{ content: string }>(`/files/read?path=${encodeURIComponent(path)}`)
      d.content = d.saved = r.content
      countVersions(d)
    }
    hist.value = null
    msg.success(t('common.done'))
  } catch (e) {
    fail(e)
  }
}
const histCols = computed(() => [
  { title: t('fe.saved_at'), key: 'time', render: (v: Version) => datetime(v.time, locale.value) },
  { title: t('files.size'), key: 'size', width: 100, render: (v: Version) => bytes(v.size) },
  {
    title: '',
    key: 'a',
    width: 150,
    render: (v: Version) =>
      h(NSpace, { size: 4, justify: 'end' }, () => [
        h(NButton, { size: 'tiny', onClick: () => viewVersion(v) }, () => t('fe.view')),
        h(NPopconfirm, { onPositiveClick: () => restoreVersion(v.id) }, { trigger: () => h(NButton, { size: 'tiny', type: 'primary', ghost: true }, () => t('files.restore')), default: () => t('fe.restore_confirm') }),
      ]),
  },
])

// ---- folder tree ----
const treeDir = ref('')
const tree = ref<TreeOption[]>([])
const treeFilter = ref('')
async function children(dir: string): Promise<TreeOption[]> {
  const r = await get<{ items: FileEntry[] }>(`/files/list?path=${encodeURIComponent(dir)}`)
  return r.items
    .sort((a, b) => Number(b.is_dir) - Number(a.is_dir) || a.name.localeCompare(b.name))
    .map((e) => ({ key: e.path, label: e.name, isLeaf: !e.is_dir, prefix: () => iconOf(e.name, e.is_dir) }))
}
async function loadTree() {
  if (!treeDir.value) return
  try {
    tree.value = await children(treeDir.value)
  } catch (e) {
    fail(e)
  }
}
watch(treeDir, loadTree)
async function onLoad(node: TreeOption) {
  node.children = await children(String(node.key))
}
const treeProps = ({ option }: { option: TreeOption }) => ({
  onClick: () => option.isLeaf && openFile(String(option.key)),
})
async function newInTree() {
  const name = prompt(t('files.new_file'))?.trim()
  if (!name) return
  const p = join(treeDir.value, name)
  try {
    await post('/files/create', { path: p, dir: false })
    await loadTree()
    openFile(p)
  } catch (e) {
    fail(e)
  }
}

// ---- moving the window ----
const pos = reactive({ x: Math.max(16, (window.innerWidth - 1100) / 2), y: 48 })
let drag: { dx: number; dy: number } | null = null
function startDrag(ev: PointerEvent) {
  if (state.value !== 'normal' || narrow.value || (ev.target as HTMLElement).closest('button')) return
  drag = { dx: ev.clientX - pos.x, dy: ev.clientY - pos.y }
  ;(ev.currentTarget as HTMLElement).setPointerCapture(ev.pointerId)
}
function onDrag(ev: PointerEvent) {
  if (!drag) return
  pos.x = Math.min(Math.max(0, ev.clientX - drag.dx), window.innerWidth - 120)
  pos.y = Math.min(Math.max(0, ev.clientY - drag.dy), window.innerHeight - 40)
}
const endDrag = () => (drag = null)
const winStyle = computed(() =>
  state.value === 'max' || narrow.value ? {} : { left: `${pos.x}px`, top: `${pos.y}px` },
)
watch(visible, (v) => v && nextTick(() => ed.value?.focus()))
const editorHeight = computed(() => '100%')
</script>

<template>
  <teleport to="body">
    <div v-if="visible && state !== 'min'" class="win" :class="[state, { narrow }]" :style="winStyle" @keydown.stop>
      <div class="titlebar" @pointerdown="startDrag" @pointermove="onDrag" @pointerup="endDrag" @dblclick="state = state === 'max' ? 'normal' : 'max'">
        <span class="title">{{ t('fe.title') }}</span>
        <span class="grow" />
        <button type="button" :title="t('fe.minimise')" @click="state = 'min'">—</button>
        <button v-if="!narrow" type="button" :title="t('fe.maximise')" @click="state = state === 'max' ? 'normal' : 'max'">{{ state === 'max' ? '❐' : '□' }}</button>
        <button type="button" :title="t('common.close')" @click="closeAll">✕</button>
      </div>
      <div class="tools">
        <button type="button" :disabled="!cur || !dirty(cur)" @click="save()">✓ {{ t('common.save') }}</button>
        <button type="button" :disabled="!docs.some(dirty)" @click="saveAll">⧉ {{ t('fe.save_all') }}</button>
        <button type="button" :disabled="!cur" @click="reload">⟳ {{ t('common.refresh') }}</button>
        <button type="button" :disabled="!cur" @click="ed?.search()">⌕ {{ t('fe.search') }}</button>
        <button type="button" :disabled="!cur" @click="ed?.search()">⇄ {{ t('fe.replace') }}</button>
        <button type="button" :disabled="!cur" @click="ed?.gotoLine()">↧ {{ t('fe.goto') }}</button>
        <n-dropdown :options="fonts" trigger="click" @select="(k: number) => (prefs.font = k)"><button type="button">A {{ t('fe.font') }}</button></n-dropdown>
        <n-dropdown :options="themes" trigger="click" @select="(k: 'auto' | 'light' | 'dark') => (prefs.theme = k)"><button type="button">◐ {{ t('fe.theme') }}</button></n-dropdown>
        <button type="button" :disabled="!cur" @click="openHistory">🕘 {{ t('fe.history') }}</button>
        <button type="button" @click="showKeys = true">⌨ {{ t('fe.keys') }}</button>
      </div>
      <div class="body">
        <aside v-if="prefs.tree" class="tree">
          <div class="dir" :title="treeDir">{{ t('fe.dir') }}: {{ treeDir }}</div>
          <div class="treetools">
            <button type="button" @click="treeDir = parentOf(treeDir)">↑ {{ t('fe.up') }}</button>
            <button type="button" @click="loadTree">⟳</button>
            <button type="button" @click="newInTree">＋ {{ t('fm.new') }}</button>
          </div>
          <n-input v-model:value="treeFilter" size="tiny" clearable :placeholder="t('fe.filter')" style="margin: 4px 0" />
          <n-tree
            block-line :data="tree" :pattern="treeFilter" :on-load="onLoad" :node-props="treeProps" :selected-keys="cur ? [cur.path] : []"
            expand-on-click virtual-scroll style="height: calc(100% - 92px)"
          />
        </aside>
        <button type="button" class="toggle" :class="{ closed: !prefs.tree }" :title="t('fe.toggle_tree')" @click="prefs.tree = !prefs.tree">{{ prefs.tree ? '‹' : '›' }}</button>
        <section class="main">
          <div class="tabs">
            <div v-for="d in docs" :key="d.path" class="tab" :class="{ on: d.path === active }" :title="d.path" @click="active = d.path" @auxclick.middle="closeDoc(d.path)">
              <span>{{ iconOf(baseName(d.path), false) }} {{ baseName(d.path) }}<b v-if="dirty(d)" class="dot">●</b></span>
              <a class="x" @click.stop="closeDoc(d.path)">✕</a>
            </div>
          </div>
          <div class="editor">
            <CodeEditor
              v-if="cur && !cur.loading" :key="cur.path" ref="ed" v-model="cur.content" :language="baseName(cur.path)" :height="editorHeight"
              :font-size="prefs.font" :theme="prefs.theme" @save="save(cur)" @cursor="(l: number, c: number) => ((cur!.line = l), (cur!.col = c))"
            />
            <n-empty v-if="!cur" :description="t('fe.none')" style="margin-top: 80px" />
          </div>
          <div v-if="cur" class="status">
            <span class="path" :title="cur.path">{{ t('fe.location') }}: {{ cur.path }}</span>
            <span>{{ eol }}</span>
            <span>{{ t('fe.pos', { l: cur.line, c: cur.col }) }}</span>
            <a @click="openHistory">{{ t('fe.versions', { n: cur.versions }) }}</a>
            <span>{{ indent }}</span>
            <span>UTF-8</span>
            <span>{{ langName }}</span>
          </div>
        </section>
      </div>
    </div>
    <div v-if="visible && state === 'min'" class="minbar" @click="state = 'normal'">
      📝 {{ t('fe.title') }} · {{ docs.length }}<b v-if="docs.some(dirty)" class="dot">●</b>
    </div>

    <n-modal :show="showKeys" @update:show="(v: boolean) => (showKeys = v)">
      <n-card :title="t('fe.keys')" style="width: min(480px, calc(100vw - 32px))" closable @close="showKeys = false">
        <dl class="keys"><template v-for="k in keys" :key="k[0]"><dt><kbd>{{ k[0] }}</kbd></dt><dd>{{ k[1] }}</dd></template></dl>
      </n-card>
    </n-modal>
    <n-modal :show="!!hist" @update:show="(v: boolean) => !v && (hist = null)">
      <n-card v-if="hist" :title="`${t('fe.history')} · ${baseName(hist.path)}`" style="width: min(860px, calc(100vw - 32px))" closable @close="hist = null">
        <p class="muted">{{ t('fe.history_hint') }}</p>
        <n-data-table size="small" :columns="histCols" :data="hist.items" :row-key="(v: Version) => v.id" :max-height="240" />
        <template v-if="hist.view">
          <p class="muted">{{ datetime(Number(hist.view.id.slice(0, -9)), locale) }}</p>
          <CodeEditor :model-value="hist.view.content" :language="baseName(hist.path)" height="300px" readonly />
        </template>
      </n-card>
    </n-modal>
  </teleport>
</template>

<style scoped>
.win {
  position: fixed;
  z-index: 1800;
  width: min(1100px, calc(100vw - 32px));
  height: min(760px, calc(100dvh - 64px));
  display: flex;
  flex-direction: column;
  background: #1e1f22;
  color: #d4d4d4;
  border-radius: 8px;
  box-shadow: 0 12px 48px rgba(0, 0, 0, 0.45);
  overflow: hidden;
  font-size: 13px;
}
.win.max,
.win.narrow {
  inset: 0;
  width: auto;
  height: auto;
  border-radius: 0;
}
.titlebar {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 6px 8px 6px 14px;
  background: #f4f5f7;
  color: #333;
  cursor: move;
  user-select: none;
  touch-action: none;
}
.win.max .titlebar,
.win.narrow .titlebar {
  cursor: default;
}
.titlebar button {
  border: 0;
  background: transparent;
  color: inherit;
  font-size: 16px;
  width: 32px;
  height: 28px;
  cursor: pointer;
  border-radius: 4px;
}
.titlebar button:hover {
  background: rgba(0, 0, 0, 0.08);
}
.title {
  font-weight: 500;
}
.grow {
  flex: 1;
}
.tools {
  display: flex;
  flex-wrap: wrap;
  background: #3a3b3e;
}
.tools button,
.treetools button {
  border: 0;
  background: transparent;
  color: #eee;
  padding: 7px 12px;
  cursor: pointer;
  font: inherit;
  white-space: nowrap;
}
.tools button:hover:not(:disabled),
.treetools button:hover {
  background: rgba(255, 255, 255, 0.1);
}
.tools button:disabled {
  opacity: 0.4;
  cursor: default;
}
.body {
  flex: 1;
  display: flex;
  min-height: 0;
  position: relative;
}
.tree {
  width: 240px;
  flex: none;
  display: flex;
  flex-direction: column;
  background: #26272a;
  padding: 0 4px;
  min-height: 0;
}
.win.narrow .tree {
  position: absolute;
  inset: 0 auto 0 0;
  z-index: 2;
  width: min(260px, 80vw);
}
.tree :deep(.n-tree) {
  --n-node-text-color: #ddd;
  --n-node-color-hover: rgba(255, 255, 255, 0.08);
  --n-node-color-active: rgba(32, 165, 58, 0.25);
  --n-node-color-pressed: rgba(32, 165, 58, 0.25);
}
.dir {
  padding: 8px 6px 4px;
  font-weight: 500;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.treetools {
  display: flex;
  background: #303134;
}
.treetools button {
  padding: 5px 8px;
  font-size: 12px;
}
.toggle {
  position: absolute;
  left: 240px;
  top: 50%;
  z-index: 3;
  transform: translateY(-50%);
  border: 0;
  width: 14px;
  height: 48px;
  border-radius: 0 6px 6px 0;
  background: #f4f5f7;
  color: #333;
  cursor: pointer;
  padding: 0;
}
.win.narrow .toggle {
  left: min(260px, 80vw);
}
.toggle.closed {
  left: 0 !important;
}
.main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
}
.tabs {
  display: flex;
  overflow-x: auto;
  background: #111;
  scrollbar-width: thin;
}
.tab {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 7px 10px 7px 14px;
  white-space: nowrap;
  cursor: pointer;
  color: #aaa;
  border-bottom: 2px solid transparent;
}
.tab.on {
  background: #1e1f22;
  color: #fff;
  border-bottom-color: #20a53a;
}
.x {
  opacity: 0.6;
  font-size: 11px;
}
.x:hover {
  opacity: 1;
}
.dot {
  color: #e8b339;
  margin-left: 4px;
  font-size: 10px;
}
.editor {
  flex: 1;
  min-height: 0;
  position: relative;
}
.editor :deep(.code-editor),
.editor :deep(.cm-editor) {
  height: 100%;
}
.editor :deep(.cm-editor) {
  border: 0 !important;
  border-radius: 0 !important;
}
.status {
  display: flex;
  gap: 18px;
  padding: 5px 14px;
  background: #20a53a;
  color: #fff;
  font-size: 12px;
  white-space: nowrap;
  overflow-x: auto;
}
.status .path {
  flex: 1;
  min-width: 120px;
  overflow: hidden;
  text-overflow: ellipsis;
}
.status a {
  color: #fff;
  cursor: pointer;
  text-decoration: underline dotted;
}
.minbar {
  position: fixed;
  z-index: 1800;
  right: 16px;
  bottom: 16px;
  padding: 8px 14px;
  background: #3a3b3e;
  color: #fff;
  border-radius: 8px;
  cursor: pointer;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.3);
}
.keys {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 8px 16px;
  margin: 0;
}
.keys dd {
  margin: 0;
}
.muted {
  font-size: 12px;
  opacity: 0.65;
}
</style>
