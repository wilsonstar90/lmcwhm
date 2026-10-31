<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NCard, NGrid, NGi, NDescriptions, NDescriptionsItem } from 'naive-ui'
import { get } from '../api/client'
import type { Overview } from '../api/types'
import DashSummary from '../components/DashSummary.vue'
import StatusCards from '../components/home/StatusCards.vue'
import IoChart from '../components/home/IoChart.vue'

const { t } = useI18n()
const o = ref<Overview | null>(null)
let timer: number | undefined

async function load() {
  try {
    o.value = await get<Overview>('/system/overview')
  } catch {
    /* transient; next tick retries */
  }
}

onMounted(() => {
  load()
  timer = window.setInterval(load, 3000)
})
onBeforeUnmount(() => clearInterval(timer))

const uptime = (s: number) => t('home.days', { d: Math.floor(s / 86400), h: Math.floor((s % 86400) / 3600) })
</script>

<template>
  <!-- aaPanel order: status first, then the overview. -->
  <StatusCards v-if="o" :o="o" style="margin-bottom: 16px" />
  <DashSummary />
  <div v-if="o" class="home">
    <n-grid cols="1 l:3" responsive="screen" :x-gap="16" :y-gap="16">
      <n-gi span="1 l:2"><IoChart :o="o" /></n-gi>
      <n-gi>
        <n-card :title="t('home.system')" size="small" style="height: 100%">
          <n-descriptions :column="1" label-placement="left" size="small">
            <n-descriptions-item label="Hostname">{{ o.hostname }}</n-descriptions-item>
            <n-descriptions-item :label="t('home.os')">{{ o.platform }} ({{ o.arch }})</n-descriptions-item>
            <n-descriptions-item :label="t('home.kernel')">{{ o.kernel }}</n-descriptions-item>
            <n-descriptions-item label="CPU">{{ o.cpu_model }}</n-descriptions-item>
            <n-descriptions-item :label="t('home.uptime')">{{ uptime(o.uptime) }}</n-descriptions-item>
          </n-descriptions>
        </n-card>
      </n-gi>
    </n-grid>
  </div>
</template>
