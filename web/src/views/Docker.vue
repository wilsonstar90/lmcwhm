<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NTabs, NTabPane, NAlert } from 'naive-ui'
import { get } from '../api/client'
import type { WebConfig, WebStatus } from '../api/types'
import DockerContainers from '../components/DockerContainers.vue'
import DockerCompose from '../components/DockerCompose.vue'
import DockerResources from '../components/DockerResources.vue'
import SiteCreate from '../components/SiteCreate.vue'
import TaskModal from '../components/TaskModal.vue'

const { t } = useI18n()
const status = ref<{ available: boolean; version: string } | null>(null)
const web = ref<WebStatus | null>(null)
const cfg = ref<WebConfig | null>(null)
const task = ref<{ id: string; title: string } | null>(null)
const bind = ref<{ proxy_target: string; remark: string } | null>(null)
const tab = ref('containers')
const refs: Record<string, { load: () => void } | null> = { containers: null, compose: null, images: null, volumes: null, networks: null }

onMounted(async () => {
  status.value = await get<{ available: boolean; version: string }>('/docker/status')
})

function openTask(id: string, title: string) {
  task.value = { id, title }
}
function reload() {
  refs[tab.value]?.load()
}

async function openBind(port: number, name: string) {
  if (!web.value) {
    ;[web.value, cfg.value] = await Promise.all([get<WebStatus>('/web/status'), get<WebConfig>('/web/config')])
  }
  bind.value = { proxy_target: `http://127.0.0.1:${port}`, remark: `docker: ${name}` }
}
</script>

<template>
  <n-alert v-if="status && !status.available" type="error" :show-icon="false" style="margin-bottom: 12px">{{ t('web.no_docker') }}</n-alert>
  <n-tabs v-model:value="tab" type="line" animated>
    <template #suffix>
      <span v-if="status?.version" style="font-size: 12px; opacity: 0.6">Docker {{ status.version }}</span>
    </template>
    <n-tab-pane name="containers" :tab="t('docker.containers')">
      <DockerContainers :ref="(el) => (refs.containers = el as never)" @task="openTask" @bind="openBind" />
    </n-tab-pane>
    <n-tab-pane name="compose" tab="Compose">
      <DockerCompose :ref="(el) => (refs.compose = el as never)" @task="openTask" />
    </n-tab-pane>
    <n-tab-pane name="images" :tab="t('docker.images')">
      <DockerResources :ref="(el) => (refs.images = el as never)" kind="images" @task="openTask" />
    </n-tab-pane>
    <n-tab-pane name="volumes" :tab="t('docker.volumes')">
      <DockerResources :ref="(el) => (refs.volumes = el as never)" kind="volumes" @task="openTask" />
    </n-tab-pane>
    <n-tab-pane name="networks" :tab="t('docker.networks')">
      <DockerResources :ref="(el) => (refs.networks = el as never)" kind="networks" @task="openTask" />
    </n-tab-pane>
  </n-tabs>

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
