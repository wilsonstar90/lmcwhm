<script setup lang="ts">
import { computed, h, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NCard, NDataTable, NGi, NGrid, NRadioButton, NRadioGroup, NSpace, NTag, type DataTableColumns } from 'naive-ui'
import { get } from '../api/client'
import { bytes } from '../format'
import LineChart from '../components/LineChart.vue'

interface Point { t: number; cpu: number; mem: number; swap: number; load1: number; disk: number; disk_r: number; disk_w: number; net_rx: number; net_tx: number }
interface Proc { pid: number; name: string; user: string; cpu: number; rss: number; cmdline: string }
interface Svc { name: string; image: string; state: string; status: string }

const { t } = useI18n()
const range = ref('1h')
const points = ref<Point[]>([])
const procs = ref<Proc[]>([])
const services = ref<{ docker: boolean; items: Svc[] } | null>(null)
const procLoading = ref(false)
let timer: number | undefined

async function loadSeries() {
  try {
    points.value = (await get<{ items: Point[] }>(`/monitor/series?range=${range.value}`)).items
  } catch {
    /* next tick retries */
  }
}
async function loadProcs() {
  procLoading.value = true
  try {
    procs.value = (await get<{ items: Proc[] }>('/monitor/processes?n=15')).items
  } finally {
    procLoading.value = false
  }
}
async function loadServices() {
  services.value = await get<{ docker: boolean; items: Svc[] }>('/monitor/services')
}
function schedule() {
  clearInterval(timer)
  timer = window.setInterval(loadSeries, ['15m', '1h'].includes(range.value) ? 5000 : 60000)
}
watch(range, () => {
  loadSeries()
  schedule()
})
onMounted(() => {
  loadSeries()
  loadProcs()
  loadServices()
  schedule()
})
onBeforeUnmount(() => clearInterval(timer))

const times = computed(() => points.value.map((p) => p.t))
const col = (k: keyof Point) => points.value.map((p) => p[k])
const pct = (v: number) => `${v.toFixed(v < 10 ? 1 : 0)}%`
const rate = (v: number) => `${bytes(v)}/s`
const last = computed(() => points.value[points.value.length - 1])

const procCols: DataTableColumns<Proc> = [
  { title: 'PID', key: 'pid', width: 80 },
  { title: () => t('monitor.process'), key: 'name', width: 160 },
  { title: () => t('common.user'), key: 'user', width: 100 },
  { title: 'CPU', key: 'cpu', width: 90, render: (p) => pct(p.cpu), sorter: (a, b) => a.cpu - b.cpu },
  { title: () => t('home.memory'), key: 'rss', width: 110, render: (p) => bytes(p.rss), sorter: (a, b) => a.rss - b.rss },
  { title: () => t('monitor.command'), key: 'cmdline', ellipsis: { tooltip: true } },
]
const svcCols: DataTableColumns<Svc> = [
  { title: () => t('docker.name'), key: 'name' },
  { title: () => t('docker.image'), key: 'image' },
  {
    title: () => t('common.status'),
    key: 'state',
    width: 220,
    render: (s) => h(NTag, { size: 'small', type: s.state === 'running' ? 'success' : 'error' }, () => `${s.state === 'running' ? '●' : '○'} ${s.status}`),
  },
]
</script>

<template>
  <n-space justify="space-between" align="center" style="margin-bottom: 12px">
    <n-radio-group v-model:value="range" size="small">
      <n-radio-button v-for="r in ['15m', '1h', '6h', '24h', '7d', '30d']" :key="r" :value="r">{{ t(`monitor.ranges.${r}`) }}</n-radio-button>
    </n-radio-group>
    <span v-if="last" style="font-size: 12px; opacity: 0.7">{{ t('monitor.disk_used', { p: pct(last.disk) }) }}</span>
  </n-space>
  <n-grid cols="1 l:2" responsive="screen" :x-gap="16" :y-gap="16">
    <n-gi>
      <n-card :title="t('monitor.cpu_mem')" size="small">
        <LineChart :times="times" :series="[{ name: 'CPU', values: col('cpu') }, { name: t('home.memory'), values: col('mem') }]" :format="pct" :max="100" />
      </n-card>
    </n-gi>
    <n-gi>
      <n-card :title="t('home.load')" size="small">
        <LineChart :times="times" :series="[{ name: t('monitor.load1'), values: col('load1') }]" :format="(v: number) => v.toFixed(2)" />
      </n-card>
    </n-gi>
    <n-gi>
      <n-card :title="t('monitor.disk_io')" size="small">
        <LineChart :times="times" :series="[{ name: t('monitor.read'), values: col('disk_r') }, { name: t('monitor.write'), values: col('disk_w') }]" :format="rate" />
      </n-card>
    </n-gi>
    <n-gi>
      <n-card :title="t('home.network')" size="small">
        <LineChart :times="times" :series="[{ name: t('home.recv'), values: col('net_rx') }, { name: t('home.sent'), values: col('net_tx') }]" :format="rate" />
      </n-card>
    </n-gi>
  </n-grid>

  <n-card :title="t('monitor.processes')" size="small" style="margin-top: 16px">
    <template #header-extra><n-button size="tiny" :loading="procLoading" @click="loadProcs">{{ t('common.refresh') }}</n-button></template>
    <n-data-table size="small" :columns="procCols" :data="procs" :loading="procLoading" :row-key="(p: Proc) => p.pid" :scroll-x="800" />
  </n-card>
  <n-card :title="t('monitor.services')" size="small" style="margin-top: 16px">
    <template #header-extra><n-button size="tiny" @click="loadServices">{{ t('common.refresh') }}</n-button></template>
    <p v-if="services && !services.docker" style="color: #d03050">{{ t('web.no_docker') }}</p>
    <n-data-table size="small" :columns="svcCols" :data="services?.items ?? []" :row-key="(s: Svc) => s.name" />
  </n-card>
</template>
