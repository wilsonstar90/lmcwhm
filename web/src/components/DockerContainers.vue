<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NButton, NSpace, NTag, NInput, NModal, NCard, NForm, NFormItem, NSelect, NDropdown, NGrid, NGi, NRadioGroup, NRadioButton, NEmpty,
  NSpin, useMessage,
} from 'naive-ui'
import { get, post, ApiError } from '../api/client'
import type { Container } from '../api/types'
import { bytes, datetime } from '../format'
import DockerContainer from './DockerContainer.vue'
import DockerVolumes from './DockerVolumes.vue'

// Containers as cards with live CPU / memory; click one for its settings.
const emit = defineEmits<{ task: [id: string, title: string]; bind: [port: number, name: string]; changed: [] }>()
const { t, locale } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))

const items = ref<Container[]>([])
const loading = ref(false)
const search = ref('')
const state = ref<'all' | 'running' | 'stopped' | 'paused'>('all')
const open = ref<{ c: Container; tab?: string } | null>(null)
const showCreate = ref(false)
let timer: number | undefined
let busy = false

async function load(quiet = false) {
  if (busy) return
  busy = true
  if (!quiet) loading.value = true
  try {
    items.value = (await get<{ items: Container[] }>('/docker/containers?stats=1')).items
  } catch (e) {
    if (!quiet) fail(e)
  } finally {
    busy = false
    loading.value = false
  }
}
onMounted(() => {
  load()
  timer = window.setInterval(() => document.visibilityState === 'visible' && load(true), 5000)
})
onBeforeUnmount(() => clearInterval(timer))
defineExpose({ load })

function changed() {
  load(true)
  emit('changed')
}

async function action(c: Container, a: string) {
  try {
    await post(`/docker/containers/${c.id}/${a}`)
    msg.success(t('common.done'))
    changed()
  } catch (e) {
    fail(e)
  }
}

const stopped = (s: string) => s === 'exited' || s === 'created' || s === 'dead'
const counts = computed(() => ({
  all: items.value.length,
  running: items.value.filter((c) => c.state === 'running').length,
  stopped: items.value.filter((c) => stopped(c.state)).length,
  paused: items.value.filter((c) => c.state === 'paused').length,
}))
const filtered = computed(() => {
  const q = search.value.trim().toLowerCase()
  return items.value.filter(
    (c) =>
      (state.value === 'all' || (state.value === 'stopped' ? stopped(c.state) : c.state === state.value)) &&
      (!q || [c.name, c.image, c.project].some((x) => x.toLowerCase().includes(q))),
  )
})

const tone = (s: string) => (s === 'running' ? 'ok' : s === 'paused' || s === 'restarting' ? 'warn' : 'off')
const pct = (a: number, b: number) => (b > 0 ? Math.min(100, (a / b) * 100) : 0)

function menu(c: Container) {
  const running = c.state === 'running'
  return [
    { label: t('docker.restart'), key: 'restart', disabled: !running },
    { label: c.state === 'paused' ? t('docker.unpause') : t('docker.pause'), key: c.state === 'paused' ? 'unpause' : 'pause', disabled: !running && c.state !== 'paused' },
    { label: t('docker.terminal'), key: 'terminal', disabled: !running },
    { label: t('app.logs'), key: 'logs' },
    { label: t('docker.bind_domain'), key: 'proxy' },
    { type: 'divider', key: 'd' },
    { label: t('common.delete'), key: 'remove', disabled: c.managed },
  ]
}
function onMenu(c: Container, key: string) {
  if (key === 'terminal' || key === 'logs' || key === 'proxy') return (open.value = { c, tab: key })
  if (key === 'remove' && !confirm(t('docker.remove_confirm', { name: c.name }))) return
  action(c, key)
}

// ---- create ----
const blank = () => ({ name: '', image: '', ports: '', env: '', volumes: [] as string[], restart: 'unless-stopped', network: '', command: '', cpus: '', memory: '' })
const f = ref(blank())
const lines = (s: string) => s.split('\n').map((x) => x.trim()).filter(Boolean)
const restartOptions = ['no', 'always', 'unless-stopped', 'on-failure'].map((v) => ({ label: v, value: v }))

async function create() {
  try {
    const r = await post<{ task_id: string }>('/docker/containers', {
      ...f.value,
      ports: lines(f.value.ports),
      env: lines(f.value.env),
    })
    showCreate.value = false
    emit('task', r.task_id, `${t('docker.create')} · ${f.value.image}`)
    f.value = blank()
  } catch (e) {
    fail(e)
  }
}
</script>

<template>
  <div class="bar">
    <n-space align="center">
      <n-button type="primary" @click="showCreate = true">{{ t('docker.create') }}</n-button>
      <n-radio-group v-model:value="state" size="small">
        <n-radio-button v-for="k in (['all', 'running', 'stopped', 'paused'] as const)" :key="k" :value="k">
          {{ t(`docker.filter.${k}`) }} {{ counts[k] }}
        </n-radio-button>
      </n-radio-group>
    </n-space>
    <n-space>
      <n-input v-model:value="search" size="small" clearable :placeholder="t('docker.search')" style="width: 200px" />
      <n-button size="small" @click="load()">{{ t('common.refresh') }}</n-button>
    </n-space>
  </div>

  <n-spin :show="loading && !items.length">
    <n-empty v-if="!filtered.length" :description="t('docker.no_containers')" style="padding: 40px 0" />
    <div v-else class="cards">
      <div v-for="c in filtered" :key="c.id" class="card" :class="tone(c.state)" role="button" tabindex="0" @click="open = { c }" @keyup.enter="open = { c }">
        <div class="top">
          <span class="name"><i class="dot" />{{ c.name }}</span>
          <span class="tags">
            <n-tag v-if="c.managed" size="tiny" :bordered="false">lmcwhm</n-tag>
            <n-tag v-if="c.project" size="tiny" type="info" :bordered="false">{{ c.project }}</n-tag>
          </span>
        </div>
        <div class="img" :title="c.image">{{ c.image }}</div>
        <div class="meta">
          <span>{{ c.created_at ? datetime(c.created_at, locale) : c.created }}</span>
          <span>{{ c.status }}</span>
        </div>
        <template v-if="c.usage">
          <div class="meter">
            <span class="k">CPU</span>
            <span class="track"><span class="fill" :style="{ width: `${Math.min(100, c.usage.cpu)}%` }" /></span>
            <span class="v">{{ c.usage.cpu.toFixed(1) }}%</span>
          </div>
          <div class="meter">
            <span class="k">{{ t('docker.memory') }}</span>
            <span class="track"><span class="fill mem" :style="{ width: `${pct(c.usage.mem_used, c.usage.mem_limit)}%` }" /></span>
            <span class="v">{{ bytes(c.usage.mem_used) }} / {{ bytes(c.usage.mem_limit) }}</span>
          </div>
        </template>
        <div v-else class="idle">{{ c.state === 'running' ? '…' : t('docker.not_running') }}</div>
        <div class="acts" @click.stop @keyup.stop>
          <n-button v-if="c.state === 'running'" size="tiny" @click="action(c, 'stop')">{{ t('docker.stop') }}</n-button>
          <n-button v-else size="tiny" type="primary" @click="action(c, 'start')">{{ t('docker.start') }}</n-button>
          <n-button size="tiny" @click="open = { c }">{{ t('docker.settings') }}</n-button>
          <n-dropdown :options="menu(c)" trigger="click" @select="(k: string) => onMenu(c, k)">
            <n-button size="tiny">⋯</n-button>
          </n-dropdown>
        </div>
      </div>
    </div>
  </n-spin>

  <DockerContainer
    :container="open?.c ?? null"
    :tab="open?.tab"
    @close="open = null"
    @changed="changed"
    @bind="(port: number, name: string) => emit('bind', port, name)"
  />

  <n-modal :show="showCreate" @update:show="(v: boolean) => (showCreate = v)">
    <n-card :title="t('docker.create')" style="width: min(720px, calc(100vw - 32px))" closable @close="showCreate = false">
      <n-form label-placement="top">
        <n-grid cols="1 s:2" responsive="screen" :x-gap="12">
          <n-gi><n-form-item :label="t('docker.image')"><n-input v-model:value="f.image" placeholder="nginx:latest" /></n-form-item></n-gi>
          <n-gi><n-form-item :label="t('docker.name')"><n-input v-model:value="f.name" :placeholder="t('docker.optional')" /></n-form-item></n-gi>
          <n-gi>
            <n-form-item :label="t('docker.ports')" :feedback="t('docker.ports_hint')">
              <n-input v-model:value="f.ports" type="textarea" :rows="3" placeholder="127.0.0.1:8081:80" />
            </n-form-item>
          </n-gi>
          <n-gi span="1 s:2">
            <n-form-item :label="t('docker.volumes')" :feedback="t('docker.volumes_hint')">
              <DockerVolumes v-model:value="f.volumes" />
            </n-form-item>
          </n-gi>
          <n-gi>
            <n-form-item :label="t('docker.env')" :feedback="t('docker.env_hint')">
              <n-input v-model:value="f.env" type="textarea" :rows="3" placeholder="TZ=Asia/Hong_Kong" />
            </n-form-item>
          </n-gi>
          <n-gi>
            <n-form-item :label="t('docker.restart_policy')"><n-select v-model:value="f.restart" :options="restartOptions" /></n-form-item>
            <n-form-item :label="t('docker.network')"><n-input v-model:value="f.network" placeholder="bridge" /></n-form-item>
          </n-gi>
          <n-gi><n-form-item label="CPU"><n-input v-model:value="f.cpus" placeholder="1.5" /></n-form-item></n-gi>
          <n-gi><n-form-item :label="t('docker.memory')"><n-input v-model:value="f.memory" placeholder="512m" /></n-form-item></n-gi>
        </n-grid>
        <n-form-item :label="t('docker.command')"><n-input v-model:value="f.command" :placeholder="t('docker.optional')" /></n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="showCreate = false">{{ t('common.cancel') }}</n-button>
          <n-button type="primary" :disabled="!f.image" @click="create">{{ t('common.confirm') }}</n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>
</template>

<style scoped>
.bar {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  justify-content: space-between;
  margin-bottom: 14px;
}
.cards {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: 12px;
}
.card {
  --tone: #8a8f98;
  cursor: pointer;
  border: 1px solid rgba(128, 128, 128, 0.2);
  border-radius: 8px;
  padding: 12px 14px;
  background: var(--card, rgba(128, 128, 128, 0.04));
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
  transition: border-color 0.15s;
}
.card:hover,
.card:focus-visible {
  border-color: rgba(128, 128, 128, 0.5);
  outline: none;
}
.card.ok { --tone: #18a058; }
.card.warn { --tone: #f0a020; }
.top {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  align-items: center;
}
.name {
  font-weight: 600;
  display: inline-flex;
  align-items: center;
  gap: 7px;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.dot {
  flex: none;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--tone);
}
.card.ok .dot {
  box-shadow: 0 0 0 3px rgba(24, 160, 88, 0.18);
}
.tags {
  display: inline-flex;
  gap: 4px;
  flex: none;
}
.img {
  font-size: 12.5px;
  opacity: 0.7;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}
.meta {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  font-size: 12px;
  opacity: 0.6;
}
.meta span:first-child {
  white-space: nowrap;
}
.meta span:last-child {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.meter {
  display: grid;
  grid-template-columns: 42px 1fr auto;
  align-items: center;
  gap: 8px;
  font-size: 12px;
}
.meter .k {
  opacity: 0.65;
}
.meter .v {
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.track {
  height: 6px;
  border-radius: 3px;
  background: rgba(128, 128, 128, 0.18);
  overflow: hidden;
}
.fill {
  display: block;
  height: 100%;
  background: #2080f0;
  transition: width 0.4s;
}
.fill.mem {
  background: #18a058;
}
.idle {
  font-size: 12px;
  opacity: 0.5;
  min-height: 42px;
  display: flex;
  align-items: center;
}
.acts {
  display: flex;
  gap: 6px;
  margin-top: auto;
  padding-top: 4px;
  cursor: default;
}
</style>
