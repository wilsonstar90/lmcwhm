<script setup lang="ts">
// 消息盒子 (like aaPanel): a badge with the number of running tasks; the
// window lists them, the finished ones and a task's log.
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { NBadge, NButton, NCard, NDataTable, NEmpty, NModal, NPagination, NPopconfirm, NTag, type DataTableColumns } from 'naive-ui'
import { get, post } from '../api/client'
import { datetime } from '../format'

interface Running { id: string; name: string; kind: 'task' | 'cron'; started: number; job_id?: number }
interface Row { id: string; name: string; status: 'success' | 'failed' | 'running'; error?: string; started: number; finished: number; log?: string }

const { t, te, locale } = useI18n()
const router = useRouter()
const show = ref(false)
const tab = ref<'running' | 'history' | 'log'>('running')
const running = ref<Running[]>([])
const history = ref<Row[]>([])
const total = ref(0)
const page = ref(1)
const size = 10
const sel = ref<{ id: string; live: boolean } | null>(null)
const log = ref('')
const logEl = ref<HTMLElement | null>(null)
let timer: number | undefined

// "app.install:redis 7.4" → "安裝 redis 7.4"
function label(name: string) {
  const i = name.indexOf(':')
  const kind = i < 0 ? name : name.slice(0, i)
  const key = `tbx.names.${kind.replace(/\./g, '_')}`
  return (te(key) ? t(key) : kind) + (i < 0 ? '' : ' ' + name.slice(i + 1))
}
const took = (a: number, b: number) => {
  const s = Math.max(0, b - a)
  return s < 60 ? t('tbx.secs', { n: s }) : t('tbx.mins', { m: Math.floor(s / 60), s: s % 60 })
}
const now = ref(Math.floor(Date.now() / 1000))

async function loadRunning() {
  try {
    running.value = (await get<{ running: Running[] }>('/tasks')).running
  } catch {
    /* offline or restarting */
  }
}
async function loadHistory() {
  const r = await get<{ items: Row[]; total: number }>(`/tasks/history?page=${page.value}&size=${size}`)
  history.value = r.items
  total.value = r.total
}
async function loadLog() {
  if (!sel.value) return
  try {
    if (sel.value.live) {
      const r = await get<{ status: string; log: string[] }>(`/tasks/${sel.value.id}`)
      log.value = r.log.join('\n')
      if (r.status !== 'running') sel.value.live = false
    } else {
      log.value = (await get<Row>(`/tasks/history/${sel.value.id}`)).log ?? ''
    }
  } catch {
    // A finished task leaves memory for the history.
    if (sel.value.live) sel.value.live = false
  }
  requestAnimationFrame(() => logEl.value && (logEl.value.scrollTop = logEl.value.scrollHeight))
}
function openLog(id: string, live: boolean) {
  sel.value = { id, live }
  tab.value = 'log'
  log.value = ''
  loadLog()
}
function openTab(v: typeof tab.value) {
  tab.value = v
  if (v === 'history') loadHistory()
  if (v === 'log' && !sel.value) {
    const r = running.value.find((x) => x.kind === 'task')
    if (r) openLog(r.id, true)
    else if (history.value[0]) openLog(history.value[0].id, false)
  }
}
async function clearHistory() {
  await post('/tasks/history/clear')
  page.value = 1
  loadHistory()
}

function tick() {
  if (document.hidden) return
  now.value = Math.floor(Date.now() / 1000)
  loadRunning()
  if (show.value && tab.value === 'log' && sel.value?.live) loadLog()
}
onMounted(() => {
  loadRunning()
  timer = window.setInterval(tick, 3000)
})
onBeforeUnmount(() => clearInterval(timer))
watch(show, (v) => {
  if (v) {
    loadRunning()
    loadHistory()
  }
})

const cols = computed<DataTableColumns<Row>>(() => [
  { title: t('tbx.name'), key: 'name', minWidth: 180, ellipsis: { tooltip: true }, render: (r) => label(r.name) },
  { title: t('tbx.created'), key: 'started', width: 160, render: (r) => datetime(r.started, locale.value) },
  { title: t('tbx.took'), key: 'took', width: 90, render: (r) => took(r.started, r.finished) },
  { title: t('tbx.result'), key: 'status', width: 90, render: (r) => (r.status === 'success' ? t('task.success') : t('task.failed')) },
])
const rowProps = (r: Row) => ({ style: 'cursor: pointer', onClick: () => openLog(r.id, false) })
</script>

<template>
  <n-badge :value="running.length" :color="running.length ? '#20a53a' : '#a0a0a0'" show-zero :max="99">
    <n-button size="tiny" quaternary class="box-btn" :aria-label="t('tbx.title')" @click="show = true">✉</n-button>
  </n-badge>

  <n-modal v-model:show="show">
    <n-card class="box" :title="t('tbx.title')" closable content-style="padding: 0" @close="show = false">
      <div class="layout">
        <nav class="side">
          <button :class="{ on: tab === 'running' }" @click="openTab('running')">{{ t('tbx.running') }} ({{ running.length }})</button>
          <button :class="{ on: tab === 'history' }" @click="openTab('history')">{{ t('tbx.history') }} ({{ total }})</button>
          <button :class="{ on: tab === 'log' }" @click="openTab('log')">{{ t('tbx.log') }}</button>
        </nav>
        <section class="main">
          <template v-if="tab === 'running'">
            <n-empty v-if="!running.length" :description="t('tbx.none')" style="margin: 48px 0" />
            <div v-for="r in running" :key="r.id" class="run" @click="r.kind === 'task' ? openLog(r.id, true) : (router.push({ name: 'cron' }), (show = false))">
              <n-tag size="small" :type="r.kind === 'cron' ? 'info' : 'success'">{{ r.kind === 'cron' ? t('tbx.cron') : t('task.running') }}</n-tag>
              <span class="name">{{ label(r.name) }}</span>
              <span class="when">{{ r.started ? took(r.started, now) : '' }}</span>
            </div>
          </template>
          <template v-else-if="tab === 'history'">
            <div class="hist-head">
              <span v-if="history.some((h) => h.status === 'failed')" class="fail-note">{{ t('tbx.failed_note') }}</span>
              <n-popconfirm v-if="total" @positive-click="clearHistory">
                <template #trigger><n-button size="small" quaternary>{{ t('tbx.clear') }}</n-button></template>
                {{ t('tbx.clear_confirm') }}
              </n-popconfirm>
            </div>
            <n-data-table :columns="cols" :data="history" :row-props="rowProps" size="small" :scroll-x="560" :row-class-name="(r: Row) => (r.status === 'failed' ? 'failed' : '')" />
            <n-pagination v-if="total > size" v-model:page="page" :page-size="size" :item-count="total" style="margin-top: 10px; justify-content: flex-end" @update:page="loadHistory" />
          </template>
          <template v-else>
            <n-empty v-if="!sel" :description="t('tbx.no_log')" style="margin: 48px 0" />
            <pre v-else ref="logEl" class="log">{{ log || '…' }}</pre>
          </template>
        </section>
      </div>
    </n-card>
  </n-modal>
</template>

<style scoped>
.box-btn {
  font-size: 14px;
}
.box {
  width: min(760px, calc(100vw - 24px));
}
.layout {
  display: flex;
  min-height: min(520px, 70dvh);
  max-height: 75vh;
  max-height: 75dvh;
}
.side {
  display: flex;
  flex-direction: column;
  width: 128px;
  flex: none;
  background: rgba(128, 128, 128, 0.08);
  padding: 8px 0;
}
.side button {
  all: unset;
  cursor: pointer;
  padding: 10px 16px;
  font-size: 13px;
}
.side button.on {
  background: var(--n-color, rgba(255, 255, 255, 0.9));
  color: #20a53a;
  font-weight: 600;
}
.main {
  flex: 1;
  min-width: 0;
  padding: 12px 16px;
  overflow: auto;
}
.run {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 4px;
  border-bottom: 1px solid rgba(128, 128, 128, 0.15);
  cursor: pointer;
}
.run .name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.run .when {
  font-size: 12px;
  opacity: 0.7;
  font-variant-numeric: tabular-nums;
}
.hist-head {
  display: flex;
  justify-content: flex-end;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}
.fail-note {
  flex: 1;
  font-size: 12px;
  color: #d03050;
}
.log {
  margin: 0;
  background: #111;
  color: #ddd;
  border-radius: 8px;
  padding: 12px;
  font-size: 12px;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-all;
  height: 100%;
  min-height: 300px;
  max-height: 65vh;
  max-height: 65dvh;
  overflow: auto;
}
:deep(.failed td) {
  color: #d03050;
}
@media (max-width: 560px) {
  .layout {
    flex-direction: column;
  }
  .side {
    flex-direction: row;
    width: auto;
    overflow-x: auto;
    padding: 0;
  }
  .side button {
    white-space: nowrap;
  }
}
</style>
