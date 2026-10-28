<script setup lang="ts">
import { computed, h, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NCard, NDataTable, NGi, NGrid, NRadioButton, NRadioGroup, NSpace, NStatistic, NTabPane, NTabs, type DataTableColumns } from 'naive-ui'
import { get } from '../api/client'
import { bytes } from '../format'
import { countryName, flag } from '../geo'
import LineChart from './LineChart.vue'
import AiButton from './AiButton.vue'

interface Point { t: number; pv: number; uv: number; bytes: number; s2xx: number; s3xx: number; s4xx: number; s5xx: number; spider: number }
interface Top { key: string; hits: number; bytes: number }
interface Report { total: Point; series: Point[]; daily: boolean; top: Record<string, Top[]>; countries: Top[] }

const props = defineProps<{ domain: string }>()
const { t, locale } = useI18n()
const days = ref(1)
const rep = ref<Report | null>(null)
async function load() {
  rep.value = await get<Report>(`/sitestats?domain=${encodeURIComponent(props.domain)}&days=${days.value}`)
}
onMounted(load)
watch(() => props.domain, load)
const num = (n: number) => n.toLocaleString(locale.value)
const share = (n: number) => (rep.value?.total.pv ? `${((n / rep.value.total.pv) * 100).toFixed(1)}%` : '')
const topCols = (label: string, withBytes = false): DataTableColumns<Top> => [
  { title: label, key: 'key', ellipsis: { tooltip: true } },
  { title: () => t('stats.hits'), key: 'hits', width: 90, render: (x) => num(x.hits) },
  ...(withBytes ? [{ title: () => t('stats.traffic'), key: 'bytes', width: 100, render: (x: Top) => bytes(x.bytes) }] : []),
]
const countryCols = computed<DataTableColumns<Top>>(() => [
  { title: () => t('stats.country'), key: 'key', render: (x) => (x.key === '??' ? t('stats.unknown') : h('span', `${flag(x.key)} ${countryName(x.key, locale.value)}`)) },
  { title: () => t('stats.hits'), key: 'hits', width: 90, render: (x) => num(x.hits) },
  { title: '%', key: 'p', width: 70, render: (x) => share(x.hits) },
])
</script>

<template>
  <n-space justify="space-between" align="center" style="margin-bottom: 12px">
    <n-radio-group v-model:value="days" size="small" @update:value="load">
      <n-radio-button :value="1">{{ t('stats.today') }}</n-radio-button>
      <n-radio-button :value="2">{{ t('stats.days', { n: 2 }) }}</n-radio-button>
      <n-radio-button :value="7">{{ t('stats.days', { n: 7 }) }}</n-radio-button>
      <n-radio-button :value="30">{{ t('stats.days', { n: 30 }) }}</n-radio-button>
    </n-radio-group>
    <AiButton size="tiny" :prompt="t('ai.prompt_site_traffic', { domain })" />
  </n-space>
  <template v-if="rep">
    <n-grid cols="2 s:4" responsive="screen" :x-gap="12" :y-gap="12">
      <n-gi><n-statistic label="PV" :value="num(rep.total.pv)" /></n-gi>
      <n-gi><n-statistic label="UV" :value="num(rep.total.uv)" /></n-gi>
      <n-gi><n-statistic :label="t('stats.traffic')" :value="bytes(rep.total.bytes)" /></n-gi>
      <n-gi><n-statistic :label="t('stats.errors')" :value="`${num(rep.total.s4xx)} / ${num(rep.total.s5xx)}`" /></n-gi>
    </n-grid>
    <p class="hint">{{ t('stats.spiders_excluded', { n: num(rep.total.spider) }) }}</p>
    <n-card size="small" :title="rep.daily ? t('stats.per_day') : t('stats.per_hour')" style="margin-top: 8px">
      <LineChart :times="rep.series.map((p) => p.t)" :series="[{ name: 'PV', values: rep.series.map((p) => p.pv) }, { name: t('stats.errors_5xx'), values: rep.series.map((p) => p.s5xx) }]" :format="(v: number) => num(Math.round(v))" />
    </n-card>
    <n-tabs type="segment" size="small" style="margin-top: 12px">
      <n-tab-pane name="url" :tab="t('stats.top_urls')"><n-data-table size="small" :columns="topCols(t('stats.url'), true)" :data="rep.top.url" /></n-tab-pane>
      <n-tab-pane name="ip" :tab="t('stats.top_ips')"><n-data-table size="small" :columns="topCols('IP', true)" :data="rep.top.ip" /></n-tab-pane>
      <n-tab-pane name="country" :tab="t('stats.countries')"><n-data-table size="small" :columns="countryCols" :data="rep.countries" /></n-tab-pane>
      <n-tab-pane name="ref" :tab="t('stats.referrers')"><n-data-table size="small" :columns="topCols(t('stats.referrer'))" :data="rep.top.ref" /></n-tab-pane>
      <n-tab-pane name="spider" :tab="t('stats.spiders')"><n-data-table size="small" :columns="topCols(t('stats.spider'), true)" :data="rep.top.spider" /></n-tab-pane>
      <n-tab-pane name="status" :tab="t('stats.status')"><n-data-table size="small" :columns="topCols(t('common.status'))" :data="rep.top.status" /></n-tab-pane>
    </n-tabs>
  </template>
</template>

<style scoped>
.hint {
  font-size: 12px;
  opacity: 0.65;
}
</style>
