<script setup lang="ts">
import { computed, h, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NCard, NDataTable, NGi, NGrid, NPopconfirm, NRadioButton, NRadioGroup, NSpace, useMessage, type DataTableColumns } from 'naive-ui'
import { get, post, ApiError } from '../api/client'
import { bytes } from '../format'
import LineChart from './LineChart.vue'
import WorldMap from './WorldMap.vue'
import Treemap from './Treemap.vue'
import IpGeo from './IpGeo.vue'
import DetailDrawer, { type DetailRow } from './DetailDrawer.vue'

// Traffic overview of every website (aaPanel 防火牆總覽): requests and
// intercepted requests against the day before, the last minute live, the
// day by hour, visitors and intercepted requests on a map, clients and
// status codes. Click a country or an IP for details.
interface Count { key: string; hits: number }
interface Traffic {
  day: number; requests: number; blocked: number; bytes: number; ips: number; prev_requests: number; prev_blocked: number; prev_total: number
  hours: { t: number; pv: number; blocked: number; bytes: number }[]
  status: Count[]; browsers: Count[]; os: Count[]; countries: Count[]; blocked_countries: Count[]; blocked_ips: Count[]
}
interface Live { at: number; steps: number[]; qps: number[]; bps: number[] }

const emit = defineEmits<{ changed: [] }>()
const { t, locale } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))
const day = ref(0)
const tr = ref<Traffic | null>(null)
const live = ref<Live | null>(null)
const mapMode = ref<'visit' | 'block'>('block')
const clients = ref<'browsers' | 'os'>('browsers')
const ip = ref<Count | null>(null)
const country = ref<string | null>(null)
let timer: number | undefined

async function load() {
  try {
    tr.value = await get<Traffic>(`/security/traffic?day=${day.value}`)
  } catch (e) {
    fail(e)
  }
}
async function poll() {
  try {
    live.value = await get<Live>('/security/live')
  } catch {
    /* keep the last sample */
  }
}
onMounted(() => {
  load()
  poll()
  timer = window.setInterval(poll, 5000)
})
onUnmounted(() => clearInterval(timer))
watch(day, load)

const n = (v: number) => v.toLocaleString()
function delta(now: number, before: number) {
  if (!before) return null
  const d = (now - before) / before
  return { up: d >= 0, text: `${(Math.abs(d) * 100).toFixed(1)}%` }
}
const cards = computed(() => {
  const x = tr.value
  if (!x) return []
  return [
    { key: 'requests', value: n(x.requests), delta: delta(x.requests, x.prev_requests), sub: t('tr.prev', { n: n(x.prev_requests) }), tone: '' },
    { key: 'blocked', value: n(x.blocked), delta: delta(x.blocked, x.prev_blocked), sub: x.requests ? t('tr.share', { p: ((x.blocked / x.requests) * 100).toFixed(1) }) : '—', tone: 'bad' },
    { key: 'ips', value: n(x.ips), delta: null, sub: t('tr.ips_sub'), tone: '' },
    { key: 'bytes', value: bytes(x.bytes), delta: null, sub: t('tr.prev_day', { n: n(x.prev_total) }), tone: '' },
  ]
})
const liveNow = computed(() => {
  const l = live.value
  if (!l?.qps.length) return { qps: 0, bps: 0 }
  return { qps: l.qps[l.qps.length - 1], bps: l.bps[l.bps.length - 1] }
})

const mapCounts = computed<Record<string, number>>(() => {
  const list = (mapMode.value === 'block' ? tr.value?.blocked_countries : tr.value?.countries) ?? []
  return Object.fromEntries(list.filter((c) => c.key).map((c) => [c.key, c.hits]))
})
const regionName = computed(() => {
  try {
    return new Intl.DisplayNames([locale.value], { type: 'region' })
  } catch {
    return null
  }
})
const cname = (code: string) => (code ? regionName.value?.of(code) ?? code : t('tr.unknown'))
const flag = (code: string) => (code.length === 2 ? String.fromCodePoint(...[...code.toUpperCase()].map((c) => 0x1f1a5 + c.charCodeAt(0))) : '🌐')
const countryRows = computed(() => ((mapMode.value === 'block' ? tr.value?.blocked_countries : tr.value?.countries) ?? []).slice(0, 12))
const countryTotal = computed(() => countryRows.value.reduce((a, b) => a + b.hits, 0) || 1)
const countryCols = computed<DataTableColumns<Count>>(() => [
  { title: () => t('tr.country'), key: 'key', render: (c) => `${flag(c.key)}  ${cname(c.key)}` },
  { title: () => (mapMode.value === 'block' ? t('tr.blocked_n') : t('tr.requests_n')), key: 'hits', width: 110, align: 'right', render: (c) => n(c.hits) },
])
const ipCols = computed<DataTableColumns<Count>>(() => [
  { title: () => t('common.ip'), key: 'key', render: (c) => h(IpGeo, { ip: c.key }) },
  { title: () => t('tr.blocked_n'), key: 'hits', width: 100, align: 'right', render: (c) => n(c.hits) },
])

const statusRows = computed(() => {
  const list = tr.value?.status ?? []
  const sum = list.reduce((a, b) => a + b.hits, 0) || 1
  return list.slice(0, 10).map((s) => ({ ...s, share: s.hits / sum, cls: s.key[0] }))
})

// ---- details ----
const ipRows = computed<DetailRow[]>(() => {
  const x = ip.value
  if (!x) return []
  return [
    { label: t('common.ip'), value: () => h(IpGeo, { ip: x.key }) },
    { label: t('tr.blocked_n'), value: n(x.hits) },
    { label: t('tr.day'), value: day.value ? t('tr.yesterday') : t('tr.today') },
  ]
})
async function blockIP(addr: string, minutes: number) {
  try {
    await post('/firewall/ips', { ip: addr, list: 'block', minutes, remark: t('tr.block_remark') })
    msg.success(t('common.done'))
    ip.value = null
    emit('changed')
  } catch (e) {
    fail(e)
  }
}
const countryDetail = computed<DetailRow[]>(() => {
  const c = country.value
  if (c === null || !tr.value) return []
  const visits = tr.value.countries.find((x) => x.key === c)?.hits ?? 0
  const blocked = tr.value.blocked_countries.find((x) => x.key === c)?.hits ?? 0
  return [
    { label: t('tr.country'), value: `${flag(c)}  ${cname(c)}` },
    { label: t('tr.requests_n'), value: n(visits) },
    { label: t('tr.blocked_n'), value: n(blocked) },
    { label: t('tr.top_share'), value: tr.value.requests ? `${((visits / tr.value.requests) * 100).toFixed(1)}%` : '—' },
  ]
})
</script>

<template>
  <n-space justify="space-between" align="center" style="margin-bottom: 12px">
    <n-radio-group v-model:value="day" size="small">
      <n-radio-button :value="0">{{ t('tr.today') }}</n-radio-button>
      <n-radio-button :value="1">{{ t('tr.yesterday') }}</n-radio-button>
    </n-radio-group>
    <n-button size="small" @click="load">{{ t('common.refresh') }}</n-button>
  </n-space>

  <div class="cards">
    <div v-for="c in cards" :key="c.key" class="card" :class="c.tone">
      <div class="k">{{ t(`tr.cards.${c.key}`) }}</div>
      <div class="v">{{ c.value }}</div>
      <div class="s">
        <span v-if="c.delta" class="d" :class="{ up: c.delta.up }">{{ c.delta.up ? '↑' : '↓' }} {{ c.delta.text }}</span>
        <span>{{ c.sub }}</span>
      </div>
    </div>
  </div>

  <n-grid cols="1 l:3" responsive="screen" :x-gap="12" :y-gap="12" style="margin-top: 12px">
    <n-gi>
      <n-card size="small" :title="t('tr.live_qps', { v: liveNow.qps.toFixed(1) })">
        <LineChart v-if="live" :times="live.steps" :series="[{ name: t('tr.qps'), values: live.qps }]" :format="(v: number) => `${v.toFixed(1)}/s`" :height="130" />
      </n-card>
      <n-card size="small" :title="t('tr.live_bw', { v: bytes(liveNow.bps) })" style="margin-top: 12px">
        <LineChart v-if="live" :times="live.steps" :series="[{ name: t('tr.bandwidth'), values: live.bps }]" :format="(v: number) => `${bytes(v)}/s`" :height="130" />
      </n-card>
    </n-gi>
    <n-gi span="1 l:2">
      <n-card size="small" :title="t('tr.hourly')" style="height: 100%">
        <LineChart
          v-if="tr" :times="tr.hours.map((x) => x.t)"
          :series="[{ name: t('tr.cards.requests'), values: tr.hours.map((x) => x.pv) }, { name: t('tr.cards.blocked'), values: tr.hours.map((x) => x.blocked) }]"
          :format="(v: number) => n(Math.round(v))" :height="330"
        />
      </n-card>
    </n-gi>
  </n-grid>

  <n-card size="small" style="margin-top: 12px">
    <template #header>{{ t('tr.map') }}</template>
    <template #header-extra>
      <n-radio-group v-model:value="mapMode" size="small">
        <n-radio-button value="block">{{ t('tr.blocked_n') }}</n-radio-button>
        <n-radio-button value="visit">{{ t('tr.requests_n') }}</n-radio-button>
      </n-radio-group>
    </template>
    <n-grid cols="1 l:3" responsive="screen" :x-gap="16" :y-gap="12">
      <n-gi span="1 l:2">
        <WorldMap :counts="mapCounts" :tone="mapMode" :locale="locale" :label="mapMode === 'block' ? t('tr.blocked_n') : t('tr.requests_n')" @pick="(c: string) => (country = c)" />
      </n-gi>
      <n-gi>
        <n-data-table
          v-if="mapMode === 'block'" size="small" :columns="ipCols" :data="(tr?.blocked_ips ?? []).slice(0, 12)" :max-height="380"
          :row-key="(c: Count) => c.key" :row-props="(c: Count) => ({ style: 'cursor: pointer', onClick: () => (ip = c) })"
        />
        <n-data-table
          v-else size="small" :columns="countryCols" :data="countryRows" :max-height="380"
          :row-key="(c: Count) => c.key" :row-props="(c: Count) => ({ style: 'cursor: pointer', onClick: () => (country = c.key) })"
        />
        <p class="hint">{{ mapMode === 'block' ? t('tr.blocked_hint') : t('tr.visit_hint', { n: n(countryTotal) }) }}</p>
      </n-gi>
    </n-grid>
  </n-card>

  <n-grid cols="1 l:2" responsive="screen" :x-gap="12" :y-gap="12" style="margin-top: 12px">
    <n-gi>
      <n-card size="small" :title="t('tr.clients')">
        <template #header-extra>
          <n-radio-group v-model:value="clients" size="small">
            <n-radio-button value="browsers">{{ t('tr.browsers') }}</n-radio-button>
            <n-radio-button value="os">{{ t('tr.os') }}</n-radio-button>
          </n-radio-group>
        </template>
        <Treemap :items="(clients === 'os' ? tr?.os : tr?.browsers) ?? []" :height="240" />
      </n-card>
    </n-gi>
    <n-gi>
      <n-card size="small" :title="t('tr.status')">
        <div v-for="s in statusRows" :key="s.key" class="bar-row">
          <span class="code" :class="`c${s.cls}`">{{ s.key }}</span>
          <span class="bar"><i :style="{ width: `${Math.max(1, s.share * 100)}%` }" /></span>
          <span class="num">{{ n(s.hits) }}</span>
          <span class="pct">{{ (s.share * 100).toFixed(1) }}%</span>
        </div>
        <p v-if="!statusRows.length" class="hint">—</p>
        <p class="hint">{{ t('tr.status_hint') }}</p>
      </n-card>
    </n-gi>
  </n-grid>

  <DetailDrawer :show="!!ip" :title="ip?.key ?? ''" :rows="ipRows" @close="ip = null">
    <template v-if="ip" #actions>
      <n-button @click="blockIP(ip!.key, 1440)">{{ t('tr.block_day') }}</n-button>
      <n-popconfirm @positive-click="blockIP(ip!.key, 0)">
        <template #trigger><n-button type="error">{{ t('tr.block_forever') }}</n-button></template>
        {{ t('tr.block_confirm', { ip: ip.key }) }}
      </n-popconfirm>
    </template>
  </DetailDrawer>
  <DetailDrawer :show="country !== null" :title="country !== null ? cname(country) : ''" :rows="countryDetail" @close="country = null" />
</template>

<style scoped>
.cards {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 12px;
}
.card {
  border: 1px solid rgba(128, 128, 128, 0.18);
  border-radius: 8px;
  padding: 12px 14px;
  box-shadow: inset 3px 0 0 #2a78d6;
}
.card.bad {
  box-shadow: inset 3px 0 0 #d03050;
}
.card .k {
  font-size: 13px;
  opacity: 0.7;
}
.card .v {
  font-size: 26px;
  font-weight: 600;
  margin: 4px 0;
}
.card .s {
  font-size: 12px;
  opacity: 0.7;
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}
.d {
  font-weight: 600;
}
.hint {
  font-size: 12px;
  opacity: 0.65;
  margin: 8px 0 0;
}
.bar-row {
  display: grid;
  grid-template-columns: 44px 1fr 80px 56px;
  align-items: center;
  gap: 10px;
  font-size: 13px;
  padding: 4px 0;
}
.code {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-weight: 600;
}
.code.c4,
.code.c5 {
  opacity: 0.85;
}
.bar {
  height: 8px;
  background: rgba(128, 128, 128, 0.12);
  border-radius: 4px;
  overflow: hidden;
}
.bar i {
  display: block;
  height: 100%;
  background: #2a78d6;
  border-radius: 4px;
}
.num,
.pct {
  text-align: right;
  font-variant-numeric: tabular-nums;
}
.pct {
  opacity: 0.65;
}
</style>
