<script setup lang="ts">
// Upload window (like aaPanel's): drop files or folders, or pick them; a
// queue with progress; 8 MB chunks that resume from what the server has.
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NCard, NDropdown, NModal, NProgress, NSpace, useMessage } from 'naive-ui'
import { get, getCsrf, ApiError } from '../../api/client'
import type { FileEntry } from '../../api/types'
import { bytes } from '../../format'
import { changed, droppedFiles, join, uploadBus, type QueuedFile } from './state'

const { t } = useI18n()
const msg = useMessage()

interface Item extends QueuedFile {
  id: number
  sent: number
  status: 'wait' | 'run' | 'done' | 'skip' | 'error' | 'cancel'
  error?: string
}
const show = ref(false)
const dir = ref('')
const queue = ref<Item[]>([])
const running = ref(false)
let seq = 0
let cancelled = new Set<number>()

watch(() => uploadBus.value?.seq, () => {
  const b = uploadBus.value
  if (!b) return
  if (!running.value && b.dir !== dir.value) queue.value = queue.value.filter((i) => i.status === 'wait')
  dir.value = b.dir
  add(b.files)
  show.value = true
  if (b.files.length && !running.value) start()
})

function add(list: QueuedFile[]) {
  for (const f of list) queue.value.push({ ...f, id: ++seq, sent: 0, status: 'wait' })
}
const fileInput = ref<HTMLInputElement | null>(null)
const dirInput = ref<HTMLInputElement | null>(null)
function picked(ev: Event) {
  const input = ev.target as HTMLInputElement
  add(Array.from(input.files ?? []).map((file) => ({ file, rel: (file as File & { webkitRelativePath?: string }).webkitRelativePath || file.name })))
  input.value = ''
}
const dragging = ref(false)
async function onDrop(ev: DragEvent) {
  dragging.value = false
  if (ev.dataTransfer) add(await droppedFiles(ev.dataTransfer))
}
function onPick(k: string) {
  ;(k === 'dir' ? dirInput : fileInput).value?.click()
}

const CHUNK = 8 << 20
type Choice = 'overwrite' | 'skip' | 'rename' | 'resume'
const conflict = ref<{ item: Item; size: number; resolve: (c: Choice) => void; all: boolean } | null>(null)
let sticky: Choice | null = null
function ask(item: Item, size: number) {
  if (sticky && sticky !== 'resume') return Promise.resolve(sticky)
  return new Promise<Choice>((resolve) => (conflict.value = { item, size, resolve, all: false }))
}
function answer(c: Choice) {
  const cf = conflict.value!
  if (cf.all) sticky = c
  conflict.value = null
  cf.resolve(c)
}

async function uploadOne(it: Item) {
  let dest = join(dir.value, it.rel)
  let offset = 0
  const st = await get<FileEntry>(`/files/stat?path=${encodeURIComponent(dest)}`).catch(() => null)
  if (st) {
    const c = await ask(it, st.is_dir ? -1 : st.size)
    if (c === 'skip') {
      it.status = 'skip'
      return
    }
    if (c === 'resume' && !st.is_dir && st.size < it.file.size) offset = st.size
    if (c === 'rename') {
      const m = it.rel.match(/^(.*?)(\.[^./]+)?$/)!
      dest = join(dir.value, `${m[1]}_${Date.now().toString(36)}${m[2] ?? ''}`)
    }
  }
  it.status = 'run'
  do {
    if (cancelled.has(it.id)) {
      it.status = 'cancel'
      return
    }
    const end = Math.min(offset + CHUNK, it.file.size)
    const last = end >= it.file.size ? '&last=1' : ''
    const res = await fetch(`/api/v1/files/upload?path=${encodeURIComponent(dest)}&offset=${offset}${last}`, {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'X-CSRF-Token': getCsrf() },
      body: it.file.slice(offset, end),
    })
    const data = await res.json().catch(() => ({}))
    if (!res.ok) throw new ApiError(res.status, data.code ?? 'common.internal', data.params)
    offset = data.size
    it.sent = offset
  } while (offset < it.file.size)
  it.status = 'done'
}
async function start() {
  if (running.value) return
  running.value = true
  sticky = null
  cancelled = new Set()
  for (const it of queue.value) {
    if (it.status !== 'wait' && it.status !== 'error') continue
    try {
      await uploadOne(it)
    } catch (e) {
      it.status = 'error'
      it.error = e instanceof ApiError ? e.text : String(e)
    }
  }
  running.value = false
  changed.value++
  const failed = queue.value.filter((i) => i.status === 'error').length
  if (failed) msg.error(t('up.failed_n', { n: failed }))
  else msg.success(t('up.done'))
}
function cancel(it: Item) {
  if (it.status === 'run') cancelled.add(it.id)
  else queue.value = queue.value.filter((x) => x !== it)
}
const total = computed(() => queue.value.reduce((n, i) => n + i.file.size, 0))
const sent = computed(() => queue.value.reduce((n, i) => n + (i.status === 'done' ? i.file.size : i.sent), 0))
const pickMenu = computed(() => [{ label: t('up.files'), key: 'file' }, { label: t('up.folder'), key: 'dir' }])
function close() {
  if (running.value && !confirm(t('up.leave'))) return
  queue.value.forEach((i) => i.status === 'run' && cancelled.add(i.id))
  show.value = false
  if (!running.value) queue.value = []
}
</script>

<template>
  <n-modal :show="show" :mask-closable="false" @update:show="(v: boolean) => !v && close()">
    <n-card :title="t('up.title', { dir })" style="width: min(760px, calc(100vw - 24px))" closable @close="close">
      <n-space justify="space-between" style="margin-bottom: 10px">
        <n-dropdown :options="pickMenu" trigger="click" @select="onPick"><n-button>{{ t('up.files') }} ▾</n-button></n-dropdown>
        <n-button :disabled="running || !queue.length" @click="queue = []">{{ t('up.clear') }}</n-button>
      </n-space>
      <input ref="fileInput" type="file" multiple hidden @change="picked" />
      <input ref="dirInput" type="file" multiple webkitdirectory hidden @change="picked" />
      <div
        class="zone" :class="{ on: dragging, has: queue.length }" @dragenter.prevent="dragging = true" @dragover.prevent
        @dragleave.self="dragging = false" @drop.prevent="onDrop" @click="!queue.length && onPick('file')"
      >
        <p v-if="!queue.length" class="big">{{ t('up.drop') }}</p>
        <table v-else class="q">
          <thead><tr><th>{{ t('files.name') }}</th><th>{{ t('files.size') }}</th><th>{{ t('up.status') }}</th><th /></tr></thead>
          <tbody>
            <tr v-for="it in queue" :key="it.id">
              <td class="n" :title="it.rel">📄 {{ it.rel }}</td>
              <td>{{ bytes(it.file.size) }}</td>
              <td class="s">
                <n-progress v-if="it.status === 'run'" type="line" :percentage="Math.floor((it.sent / Math.max(it.file.size, 1)) * 100)" :height="8" />
                <span v-else :class="it.status" :title="it.error">{{ t(`up.st.${it.status}`) }}</span>
              </td>
              <td><a v-if="it.status === 'wait' || it.status === 'run' || it.status === 'error'" @click.stop="cancel(it)">{{ t('common.cancel') }}</a></td>
            </tr>
          </tbody>
        </table>
      </div>
      <template #footer>
        <n-space justify="space-between" align="center">
          <span class="muted">{{ t('up.summary', { n: queue.length, size: bytes(total) }) }}<template v-if="running"> · {{ bytes(sent) }}</template></span>
          <n-button type="primary" :loading="running" :disabled="!queue.some((i) => i.status === 'wait' || i.status === 'error')" @click="start">{{ t('up.start') }}</n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>

  <n-modal :show="!!conflict" :mask-closable="false">
    <n-card v-if="conflict" :title="t('up.exists_title')" style="width: min(460px, calc(100vw - 32px))">
      <p style="word-break: break-all">{{ t('up.exists', { name: conflict.item.rel }) }}</p>
      <p v-if="conflict.size >= 0 && conflict.size < conflict.item.file.size" class="muted">{{ t('up.partial', { have: bytes(conflict.size), want: bytes(conflict.item.file.size) }) }}</p>
      <label class="muted"><input v-model="conflict.all" type="checkbox" /> {{ t('up.apply_all') }}</label>
      <template #footer>
        <n-space justify="end" :wrap="true">
          <n-button v-if="conflict.size >= 0 && conflict.size < conflict.item.file.size" type="primary" @click="answer('resume')">{{ t('up.resume') }}</n-button>
          <n-button @click="answer('overwrite')">{{ t('up.overwrite') }}</n-button>
          <n-button @click="answer('rename')">{{ t('up.rename') }}</n-button>
          <n-button @click="answer('skip')">{{ t('up.skip') }}</n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>
</template>

<style scoped>
.zone {
  min-height: 300px;
  max-height: 55dvh;
  overflow: auto;
  border: 2px dashed rgba(128, 128, 128, 0.35);
  border-radius: 8px;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
}
.zone.has {
  align-items: flex-start;
  cursor: default;
  border-style: solid;
  border-width: 1px;
}
.zone.on {
  border-color: #20a53a;
  background: rgba(32, 165, 58, 0.06);
}
.big {
  font-size: clamp(16px, 3vw, 24px);
  opacity: 0.55;
  text-align: center;
  padding: 16px;
}
.q {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}
.q th,
.q td {
  text-align: left;
  padding: 8px 10px;
  border-bottom: 1px solid rgba(128, 128, 128, 0.15);
}
.q .n {
  max-width: 320px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.q .s {
  width: 150px;
}
.q a {
  color: #20a53a;
  cursor: pointer;
}
.done {
  color: #20a53a;
}
.error {
  color: #d03050;
}
.skip,
.cancel,
.muted {
  opacity: 0.6;
  font-size: 12px;
}
</style>
