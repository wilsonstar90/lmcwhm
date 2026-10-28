<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { NAlert } from 'naive-ui'
import { get } from '../api/client'
import type { DockerOverview, WebConfig, WebStatus } from '../api/types'
import DockerContainers from '../components/DockerContainers.vue'
import DockerCompose from '../components/DockerCompose.vue'
import DockerResources from '../components/DockerResources.vue'
import DockerRegistries from '../components/DockerRegistries.vue'
import SiteCreate from '../components/SiteCreate.vue'
import TaskModal from '../components/TaskModal.vue'

// Docker: a tile per kind of resource on top (click one to open its list),
// containers as cards, each with its own settings window.
const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const sections = ['containers', 'compose', 'images', 'networks', 'volumes', 'registries'] as const
type Section = (typeof sections)[number]
const initial = route.query.tab as Section
const tab = ref<Section>(sections.includes(initial) ? initial : 'containers')
watch(tab, (v) => router.replace({ query: { ...route.query, tab: v } }))

const status = ref<{ available: boolean; version: string } | null>(null)
const o = ref<DockerOverview | null>(null)
const web = ref<WebStatus | null>(null)
const cfg = ref<WebConfig | null>(null)
const task = ref<{ id: string; title: string } | null>(null)
const bind = ref<{ proxy_target: string; remark: string } | null>(null)
const list = ref<{ load: () => void } | null>(null)

async function loadOverview() {
  try {
    o.value = await get<DockerOverview>('/docker/overview')
  } catch {
    o.value = null
  }
}
onMounted(async () => {
  status.value = await get<{ available: boolean; version: string }>('/docker/status')
  if (status.value.available) loadOverview()
})

function openTask(id: string, title: string) {
  task.value = { id, title }
}
function reload() {
  list.value?.load()
  loadOverview()
}

async function openBind(port: number, name: string) {
  if (!web.value) {
    ;[web.value, cfg.value] = await Promise.all([get<WebStatus>('/web/status'), get<WebConfig>('/web/config')])
  }
  bind.value = { proxy_target: `http://127.0.0.1:${port}`, remark: `docker: ${name}` }
}

interface Tile { key: Section; value: string; sub: string; on: boolean }
const tiles = computed<Tile[]>(() => {
  const v = o.value
  const dash = { value: '—', sub: '', on: false }
  if (!v) return sections.map((key) => ({ key, ...dash }))
  return [
    { key: 'containers', value: `${v.containers.running} / ${v.containers.total}`, sub: t('docker.ov.disk', { size: v.containers.size || '0B' }), on: v.containers.running > 0 },
    { key: 'compose', value: String(v.compose.total), sub: t('docker.ov.running', { n: v.compose.running }), on: v.compose.running > 0 },
    { key: 'images', value: String(v.images.total), sub: v.images.size || '0B', on: v.images.total > 0 },
    { key: 'networks', value: String(v.networks.total), sub: t('docker.ov.networks'), on: v.networks.total > 0 },
    { key: 'volumes', value: String(v.volumes.total), sub: v.volumes.size || '0B', on: v.volumes.total > 0 },
    { key: 'registries', value: String(v.registries.total), sub: t('docker.ov.logged_in'), on: v.registries.total > 0 },
  ]
})
</script>

<template>
  <n-alert v-if="status && !status.available" type="error" :show-icon="false" style="margin-bottom: 12px">{{ t('web.no_docker') }}</n-alert>
  <div class="tiles">
    <button v-for="x in tiles" :key="x.key" type="button" class="tile" :class="{ active: tab === x.key, on: x.on }" @click="tab = x.key">
      <div class="tile-name">{{ t(`docker.s.${x.key}`) }}</div>
      <div class="tile-value">{{ x.value }}</div>
      <div class="tile-sub">{{ x.sub }}</div>
    </button>
  </div>

  <header class="head">
    <h2>{{ t(`docker.s.${tab}`) }}</h2>
    <span v-if="status?.version" class="ver">Docker {{ status.version }}</span>
  </header>
  <DockerContainers v-if="tab === 'containers'" ref="list" @task="openTask" @bind="openBind" @changed="loadOverview" />
  <DockerCompose v-else-if="tab === 'compose'" ref="list" @task="openTask" />
  <DockerRegistries v-else-if="tab === 'registries'" ref="list" @changed="loadOverview" />
  <DockerResources v-else :key="tab" ref="list" :kind="tab" @task="openTask" />

  <SiteCreate
    :show="!!bind"
    :php-versions="web?.php_versions ?? []"
    :has-c-f="!!cfg?.has_cf_token"
    :preset="bind ? { type: 'proxy', ...bind } : undefined"
    @close="bind = null"
    @created="(id: string, d: string) => { bind = null; openTask(id, t('docker.bind_domain') + ' · ' + d) }"
  />
  <TaskModal :task-id="task?.id ?? null" :title="task?.title ?? ''" @close="task = null; reload()" @done="reload" />
</template>

<style scoped>
.tiles {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(100px, 1fr));
  gap: 10px;
  margin-bottom: 16px;
}
.tile {
  --tone: #8a8f98;
  text-align: left;
  font: inherit;
  color: inherit;
  cursor: pointer;
  background: var(--card, rgba(128, 128, 128, 0.06));
  border: 1px solid rgba(128, 128, 128, 0.18);
  border-radius: 8px;
  padding: 10px 12px;
  transition: border-color 0.15s, box-shadow 0.15s;
}
.tile.on {
  --tone: #18a058;
}
.tile:hover {
  border-color: rgba(128, 128, 128, 0.45);
}
.tile.active {
  border-color: var(--tone);
  box-shadow: inset 3px 0 0 var(--tone);
}
.tile-name {
  font-size: 12.5px;
  font-weight: 600;
}
.tile-value {
  font-size: 22px;
  font-weight: 600;
  margin: 6px 0 2px;
  white-space: nowrap;
}
.tile-sub {
  font-size: 12px;
  opacity: 0.6;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  margin-bottom: 12px;
}
.head h2 {
  font-size: 17px;
  margin: 0;
}
.ver {
  font-size: 12px;
  opacity: 0.6;
}
</style>
