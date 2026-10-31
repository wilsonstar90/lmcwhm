<script setup lang="ts">
// 流量 / 磁盤IO (like aaPanel): live rates from the dashboard's 3-second
// samples, for all interfaces / disks or one of them.
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NCard, NSelect, NTabPane, NTabs } from 'naive-ui'
import type { Overview } from '../../api/types'
import { bytes } from '../../format'
import LineChart from '../LineChart.vue'

const props = defineProps<{ o: Overview }>()
const { t } = useI18n()
const tab = ref<'net' | 'disk'>('net')
const nic = ref('')
const disk = ref('')
const keep = 60 // points (3 minutes)

interface Point { t: number; a: number; b: number; ops: number; lat: number }
const net = ref<Point[]>([])
const io = ref<Point[]>([])
let prev: Overview | null = null

function pick<T extends { name: string }>(list: T[] | null, name: string) {
  return (list ?? []).filter((x) => !name || x.name === name)
}
function sum<T>(list: T[], f: (x: T) => number) {
  return list.reduce((a, x) => a + f(x), 0)
}
function sample(cur: Overview) {
  if (prev && cur.sampled_at > prev.sampled_at) {
    const dt = cur.sampled_at - prev.sampled_at
    const n0 = pick(prev.nics, nic.value), n1 = pick(cur.nics, nic.value)
    net.value = [...net.value, { t: cur.sampled_at, a: Math.max(0, (sum(n1, (x) => x.sent) - sum(n0, (x) => x.sent)) / dt), b: Math.max(0, (sum(n1, (x) => x.recv) - sum(n0, (x) => x.recv)) / dt), ops: 0, lat: 0 }].slice(-keep)
    const d0 = pick(prev.disk_io, disk.value), d1 = pick(cur.disk_io, disk.value)
    const ops = sum(d1, (x) => x.read_count + x.write_count) - sum(d0, (x) => x.read_count + x.write_count)
    const busy = sum(d1, (x) => x.read_time + x.write_time) - sum(d0, (x) => x.read_time + x.write_time)
    io.value = [...io.value, {
      t: cur.sampled_at,
      a: Math.max(0, (sum(d1, (x) => x.read_bytes) - sum(d0, (x) => x.read_bytes)) / dt),
      b: Math.max(0, (sum(d1, (x) => x.write_bytes) - sum(d0, (x) => x.write_bytes)) / dt),
      ops: Math.max(0, ops / dt),
      lat: ops > 0 ? Math.max(0, busy / ops) : 0,
    }].slice(-keep)
  }
  prev = cur
}
watch(() => props.o, sample, { immediate: true })
// Another interface or disk: start the chart again.
watch(nic, () => (net.value = []))
watch(disk, () => (io.value = []))

const nicOptions = computed(() => [{ label: t('io.all'), value: '' }, ...(props.o.nics ?? []).map((n) => ({ label: n.name, value: n.name }))])
const diskOptions = computed(() => [{ label: t('io.all'), value: '' }, ...(props.o.disk_io ?? []).map((d) => ({ label: d.name, value: d.name }))])
const lastNet = computed(() => net.value[net.value.length - 1])
const lastIO = computed(() => io.value[io.value.length - 1])
const totals = computed(() => {
  const n = pick(props.o.nics, nic.value)
  return { sent: sum(n, (x) => x.sent), recv: sum(n, (x) => x.recv) }
})
const rate = (v: number) => bytes(v) + '/s'
</script>

<template>
  <n-card size="small" class="io">
    <n-tabs v-model:value="tab" type="line" size="small">
      <template #suffix>
        <n-select v-if="tab === 'net'" v-model:value="nic" :options="nicOptions" size="small" class="sel" :aria-label="t('io.nic')" />
        <n-select v-else v-model:value="disk" :options="diskOptions" size="small" class="sel" :aria-label="t('io.disk')" />
      </template>
      <n-tab-pane name="net" :tab="t('io.traffic')">
        <div class="stats">
          <div><span>{{ t('io.up') }}</span><b>{{ rate(lastNet?.a ?? 0) }}</b></div>
          <div><span>{{ t('io.down') }}</span><b>{{ rate(lastNet?.b ?? 0) }}</b></div>
          <div><span>{{ t('io.sent') }}</span><b>{{ bytes(totals.sent) }}</b></div>
          <div><span>{{ t('io.recv') }}</span><b>{{ bytes(totals.recv) }}</b></div>
        </div>
        <LineChart :times="net.map((p) => p.t)" :series="[{ name: t('io.up'), values: net.map((p) => p.a) }, { name: t('io.down'), values: net.map((p) => p.b) }]" :format="rate" :height="200" seconds />
      </n-tab-pane>
      <n-tab-pane name="disk" :tab="t('io.disk_io')">
        <div class="stats">
          <div><span>{{ t('io.read') }}</span><b>{{ rate(lastIO?.a ?? 0) }}</b></div>
          <div><span>{{ t('io.write') }}</span><b>{{ rate(lastIO?.b ?? 0) }}</b></div>
          <div><span>{{ t('io.ops') }}</span><b>{{ t('io.ops_n', { n: Math.round(lastIO?.ops ?? 0) }) }}</b></div>
          <div><span>{{ t('io.latency') }}</span><b :class="{ slow: (lastIO?.lat ?? 0) > 50 }">{{ (lastIO?.lat ?? 0).toFixed(1) }} ms</b></div>
        </div>
        <LineChart :times="io.map((p) => p.t)" :series="[{ name: t('io.read'), values: io.map((p) => p.a) }, { name: t('io.write'), values: io.map((p) => p.b) }]" :format="rate" :height="200" seconds />
      </n-tab-pane>
    </n-tabs>
  </n-card>
</template>

<style scoped>
.sel {
  width: 130px;
}
.stats {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 8px;
  padding: 10px 12px;
  margin: 4px 0 12px;
  background: rgba(128, 128, 128, 0.06);
  border-radius: 8px;
}
.stats div {
  display: flex;
  flex-direction: column;
  min-width: 0;
}
.stats span {
  font-size: 12px;
  opacity: 0.7;
}
.stats b {
  font-size: 14px;
  font-variant-numeric: tabular-nums;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.slow {
  color: #d03050;
}
@media (max-width: 560px) {
  .stats {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .sel {
    width: 100px;
  }
}
</style>
