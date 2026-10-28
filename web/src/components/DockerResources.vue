<script setup lang="ts">
import { h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NDataTable, NSpace, NInput, NPopconfirm, useMessage, type DataTableColumns } from 'naive-ui'
import { get, post, ApiError } from '../api/client'
import type { DockerImage, DockerNetwork, DockerVolume } from '../api/types'

// One table for images, volumes or networks.
const props = defineProps<{ kind: 'images' | 'volumes' | 'networks' }>()
const emit = defineEmits<{ task: [id: string, title: string] }>()
const { t } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))

type Row = DockerImage | DockerVolume | DockerNetwork
const items = ref<Row[]>([])
const loading = ref(false)
const input = ref('')
const subnet = ref('')

async function load() {
  loading.value = true
  try {
    items.value = (await get<{ items: Row[] }>(`/docker/${props.kind}`)).items
  } catch (e) {
    fail(e)
  } finally {
    loading.value = false
  }
}
onMounted(load)
defineExpose({ load })

async function run(fn: () => Promise<unknown>) {
  try {
    await fn()
    msg.success(t('common.done'))
    load()
  } catch (e) {
    fail(e)
  }
}

async function add() {
  const v = input.value.trim()
  if (!v) return
  if (props.kind === 'images') {
    try {
      const r = await post<{ task_id: string }>('/docker/images/pull', { image: v })
      emit('task', r.task_id, `docker pull ${v}`)
      input.value = ''
    } catch (e) {
      fail(e)
    }
    return
  }
  await run(() => post(`/docker/${props.kind}`, { name: v, subnet: subnet.value }))
  input.value = subnet.value = ''
}

async function prune() {
  try {
    const r = await post<{ output: string }>(`/docker/prune/${props.kind}`)
    msg.success(r.output.split('\n').pop() || t('common.done'))
    load()
  } catch (e) {
    fail(e)
  }
}

function del(row: Row) {
  if (props.kind === 'images') return run(() => post('/docker/images/remove', { id: (row as DockerImage).id }))
  return run(() => post(`/docker/${props.kind}/${encodeURIComponent((row as DockerVolume).name)}/remove`))
}

const delBtn = (row: Row) =>
  h(
    NPopconfirm,
    { onPositiveClick: () => del(row) },
    { trigger: () => h(NButton, { size: 'tiny', type: 'error', ghost: true }, () => t('common.delete')), default: () => t('docker.remove_resource') },
  )

const columns: Record<string, DataTableColumns<Row>> = {
  images: [
    { title: () => t('docker.repository'), key: 'repository', render: (r) => { const i = r as DockerImage; return h('b', `${i.repository}:${i.tag}`) } },
    { title: 'ID', key: 'id', width: 150, render: (r) => (r as DockerImage).id.replace('sha256:', '').slice(0, 12) },
    { title: () => t('docker.size'), key: 'size', width: 110 },
    { title: () => t('docker.created'), key: 'created', width: 150 },
    { title: '', key: 'a', width: 80, render: delBtn },
  ],
  volumes: [
    { title: () => t('docker.name'), key: 'name', ellipsis: { tooltip: true } },
    { title: () => t('docker.driver'), key: 'driver', width: 100 },
    { title: () => t('docker.mountpoint'), key: 'mountpoint', ellipsis: { tooltip: true } },
    { title: '', key: 'a', width: 80, render: delBtn },
  ],
  networks: [
    { title: () => t('docker.name'), key: 'name' },
    { title: 'ID', key: 'id', width: 140 },
    { title: () => t('docker.driver'), key: 'driver', width: 100 },
    { title: 'Scope', key: 'scope', width: 90 },
    { title: '', key: 'a', width: 80, render: (r) => (['bridge', 'host', 'none'].includes((r as DockerNetwork).name) ? null : delBtn(r)) },
  ],
}
</script>

<template>
  <n-space justify="space-between" style="margin-bottom: 12px">
    <n-space>
      <n-input
        v-model:value="input"
        size="small"
        :placeholder="kind === 'images' ? 'redis:7 / ghcr.io/org/app:tag' : t('docker.name')"
        style="width: 260px"
        @keyup.enter="add"
      />
      <n-input v-if="kind === 'networks'" v-model:value="subnet" size="small" :placeholder="t('docker.subnet')" style="width: 160px" />
      <n-button size="small" type="primary" :disabled="!input.trim()" @click="add">
        {{ kind === 'images' ? t('docker.pull') : t('docker.add') }}
      </n-button>
    </n-space>
    <n-space>
      <n-popconfirm @positive-click="prune">
        <template #trigger><n-button size="small">{{ t('docker.prune') }}</n-button></template>
        {{ t(`docker.prune_${kind}`) }}
      </n-popconfirm>
      <n-button size="small" @click="load">{{ t('common.refresh') }}</n-button>
    </n-space>
  </n-space>
  <n-data-table size="small" :columns="columns[kind]" :data="items" :loading="loading" :scroll-x="700" />
</template>
