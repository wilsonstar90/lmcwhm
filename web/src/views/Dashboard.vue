<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NCard, NGrid, NGi, NProgress, NDescriptions, NDescriptionsItem, NStatistic } from 'naive-ui'
import { get } from '../api/client'
import type { Overview } from '../api/types'
import { bytes } from '../format'
import MaintenanceBar from '../components/MaintenanceBar.vue'

const { t } = useI18n()
const o = ref<Overview | null>(null)
const rate = ref({ sent: 0, recv: 0 })
let timer: number | undefined
let last: Overview | null = null

async function load() {
  try {
    const cur = await get<Overview>('/system/overview')
    if (last && cur.sampled_at > last.sampled_at) {
      const dt = cur.sampled_at - last.sampled_at
      rate.value = { sent: (cur.net_sent - last.net_sent) / dt, recv: (cur.net_recv - last.net_recv) / dt }
    }
    last = cur
    o.value = cur
  } catch {
    /* transient; next tick retries */
  }
}

onMounted(() => {
  load()
  timer = window.setInterval(load, 3000)
})
onBeforeUnmount(() => clearInterval(timer))

const pct = (used: number, total: number) => (total ? Math.round((used / total) * 100) : 0)
const status = (p: number) => (p >= 90 ? 'error' : p >= 70 ? 'warning' : 'success')
const uptime = (s: number) => t('home.days', { d: Math.floor(s / 86400), h: Math.floor((s % 86400) / 3600) })
</script>

<template>
  <MaintenanceBar />
  <div v-if="o">
    <n-grid cols="1 s:2 m:4" responsive="screen" :x-gap="16" :y-gap="16">
      <n-gi>
        <n-card :title="t('home.cpu')" size="small">
          <div class="ring">
            <n-progress type="dashboard" :percentage="Math.round(o.cpu_percent)" :status="status(o.cpu_percent)">{{ Math.round(o.cpu_percent) }}%</n-progress>
            <div class="meta">{{ t('home.cores', { n: o.cpu_cores }) }}</div>
          </div>
        </n-card>
      </n-gi>
      <n-gi>
        <n-card :title="t('home.memory')" size="small">
          <div class="ring">
            <n-progress type="dashboard" :percentage="pct(o.mem_used, o.mem_total)" :status="status(pct(o.mem_used, o.mem_total))">{{ pct(o.mem_used, o.mem_total) }}%</n-progress>
            <div class="meta">{{ bytes(o.mem_used) }} / {{ bytes(o.mem_total) }}</div>
          </div>
        </n-card>
      </n-gi>
      <n-gi>
        <n-card :title="t('home.load')" size="small">
          <div class="ring">
            <n-progress
              type="dashboard"
              :percentage="Math.min(100, Math.round((o.load[0] / Math.max(o.cpu_cores, 1)) * 100))"
              :status="status((o.load[0] / Math.max(o.cpu_cores, 1)) * 100)"
            >{{ Math.round((o.load[0] / Math.max(o.cpu_cores, 1)) * 100) }}%</n-progress>
            <div class="meta">{{ o.load.map((l) => l.toFixed(2)).join(' / ') }}</div>
          </div>
        </n-card>
      </n-gi>
      <n-gi>
        <n-card :title="t('home.network')" size="small">
          <n-statistic :label="t('home.sent')" :value="bytes(rate.sent) + '/s'" />
          <n-statistic :label="t('home.recv')" :value="bytes(rate.recv) + '/s'" style="margin-top: 12px" />
        </n-card>
      </n-gi>
    </n-grid>

    <n-grid cols="1 m:2" responsive="screen" :x-gap="16" :y-gap="16" style="margin-top: 16px">
      <n-gi>
        <n-card :title="t('home.system')" size="small">
          <n-descriptions :column="1" label-placement="left" size="small">
            <n-descriptions-item label="Hostname">{{ o.hostname }}</n-descriptions-item>
            <n-descriptions-item :label="t('home.os')">{{ o.platform }} ({{ o.arch }})</n-descriptions-item>
            <n-descriptions-item :label="t('home.kernel')">{{ o.kernel }}</n-descriptions-item>
            <n-descriptions-item label="CPU">{{ o.cpu_model }}</n-descriptions-item>
            <n-descriptions-item :label="t('home.uptime')">{{ uptime(o.uptime) }}</n-descriptions-item>
          </n-descriptions>
        </n-card>
      </n-gi>
      <n-gi>
        <n-card :title="t('home.disk')" size="small">
          <div v-for="d in o.disks ?? []" :key="d.mount" class="disk">
            <div class="disk-head">
              <span>{{ d.mount }} <small>({{ d.fs }})</small></span>
              <span>{{ bytes(d.used) }} / {{ bytes(d.total) }}</span>
            </div>
            <n-progress type="line" :percentage="pct(d.used, d.total)" :status="status(pct(d.used, d.total))" :show-indicator="false" />
          </div>
        </n-card>
      </n-gi>
    </n-grid>
  </div>
</template>

<style scoped>
.ring {
  display: flex;
  flex-direction: column;
  align-items: center;
}
.meta {
  margin-top: 4px;
  font-size: 13px;
  opacity: 0.75;
  font-variant-numeric: tabular-nums;
}
.disk + .disk {
  margin-top: 12px;
}
.disk-head {
  display: flex;
  justify-content: space-between;
  font-size: 13px;
  margin-bottom: 4px;
  gap: 8px;
}
</style>
