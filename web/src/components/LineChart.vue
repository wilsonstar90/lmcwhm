<script setup lang="ts">
// Small hand-written SVG line chart: up to two series on one axis, recessive
// grid, a legend, and a crosshair tooltip. Colors are the validated reference
// palette (slots 1 and 2), stepped separately for light and dark surfaces.
import { computed, ref } from 'vue'

export interface Series {
  name: string
  values: number[]
}
const props = defineProps<{
  times: number[] // unix seconds
  series: Series[]
  format: (v: number) => string
  max?: number // fixed y max (e.g. 100 for %)
  height?: number
}>()

const W = 600
const H = computed(() => props.height ?? 160)
// The SVG covers the plot area only; the y labels sit in a fixed 52px gutter.
const pad = { l: 0, r: 0, t: 10, b: 22 }
const plotW = W - pad.l - pad.r
const plotH = computed(() => H.value - pad.t - pad.b)

const n = computed(() => props.times.length)
const top = computed(() => {
  if (props.max) return props.max
  const m = Math.max(0, ...props.series.flatMap((s) => s.values.filter(Number.isFinite)))
  if (m <= 0) return 1
  // round up to a readable 1/2/5 × 10^k step
  const p = 10 ** Math.floor(Math.log10(m))
  return ([1, 2, 5, 10].find((k) => k * p >= m) ?? 10) * p
})
const x = (i: number) => pad.l + (n.value <= 1 ? plotW : (i / (n.value - 1)) * plotW)
const y = (v: number) => pad.t + plotH.value - (Math.min(v, top.value) / top.value) * plotH.value

const paths = computed(() =>
  // A non-finite value (e.g. a failed check) breaks the line into segments.
  props.series.map((s) =>
    s.values.map((v, i) => (Number.isFinite(v) ? `${i && Number.isFinite(s.values[i - 1]) ? 'L' : 'M'}${x(i).toFixed(1)},${y(v).toFixed(1)}` : '')).join(''),
  ),
)
const ticks = computed(() => [0, 0.5, 1].map((f) => ({ v: top.value * f, y: y(top.value * f) })))
const timeLabel = (t: number) => {
  const span = n.value > 1 ? props.times[n.value - 1] - props.times[0] : 0
  const d = new Date(t * 1000)
  return span > 2 * 86400
    ? d.toLocaleDateString(undefined, { month: 'numeric', day: 'numeric' })
    : d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
}
const xTicks = computed(() => (n.value < 2 ? [] : [0, Math.floor((n.value - 1) / 2), n.value - 1].map((i) => ({ i, x: x(i), label: timeLabel(props.times[i]) }))))

// ---- hover ----
const hover = ref<number | null>(null)
const svg = ref<SVGSVGElement | null>(null)
function move(e: PointerEvent) {
  if (!svg.value || n.value === 0) return
  const r = svg.value.getBoundingClientRect()
  const px = ((e.clientX - r.left) / r.width) * W
  const i = Math.round(((px - pad.l) / plotW) * (n.value - 1))
  hover.value = Math.max(0, Math.min(n.value - 1, i))
}
const tipLeft = computed(() => (hover.value === null ? 0 : (x(hover.value) / W) * 100))
</script>

<template>
  <div class="viz-root">
    <div class="legend">
      <span v-for="(s, k) in series" :key="s.name" class="key"><i :class="`s${k + 1}`" />{{ s.name }}</span>
    </div>
    <div class="plot">
      <span v-for="tk in ticks" :key="'y' + tk.v" class="ylab" :style="{ top: tk.y + 'px' }">{{ format(tk.v) }}</span>
      <div class="area">
        <svg ref="svg" :viewBox="`0 0 ${W} ${H}`" preserveAspectRatio="none" :style="{ height: H + 'px' }" @pointermove="move" @pointerleave="hover = null">
          <g class="grid">
            <line v-for="tk in ticks" :key="tk.v" :x1="0" :x2="W" :y1="tk.y" :y2="tk.y" />
          </g>
          <path v-for="(d, k) in paths" :key="k" :d="d" :class="`line s${k + 1}`" vector-effect="non-scaling-stroke" />
          <line v-if="hover !== null" class="cross" :x1="x(hover)" :x2="x(hover)" :y1="pad.t" :y2="pad.t + plotH" vector-effect="non-scaling-stroke" />
        </svg>
        <!-- Text and markers are HTML so preserveAspectRatio="none" does not stretch them. -->
        <span v-for="(tk, j) in xTicks" :key="'x' + tk.i" class="xlab" :style="{ left: (tk.x / W) * 100 + '%', top: H - 16 + 'px', transform: j === 0 ? 'none' : j === 2 ? 'translateX(-100%)' : 'translateX(-50%)' }">{{ tk.label }}</span>
        <template v-if="hover !== null">
          <i v-for="(s, k) in series.filter((x) => Number.isFinite(x.values[hover!]))" :key="'d' + k" :class="`dot s${series.indexOf(s) + 1}`" :style="{ left: tipLeft + '%', top: y(s.values[hover] ?? 0) + 'px' }" />
          <div class="tip" :style="{ left: tipLeft + '%', transform: tipLeft > 60 ? 'translateX(calc(-100% - 12px))' : 'translateX(12px)' }">
            <div class="t">{{ new Date(times[hover] * 1000).toLocaleString() }}</div>
            <div v-for="(s, k) in series" :key="'v' + k" class="row"><i :class="`s${k + 1}`" />{{ s.name }} <b>{{ Number.isFinite(s.values[hover]) ? format(s.values[hover]) : '—' }}</b></div>
          </div>
        </template>
        <div v-if="!n" class="empty">—</div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.viz-root {
  --surface-1: #fcfcfb;
  --text-primary: #0b0b0b;
  --text-secondary: #52514e;
  --grid: #e7e6e2;
  --series-1: #2a78d6;
  --series-2: #eb6834;
}
@media (prefers-color-scheme: dark) {
  .viz-root {
    --surface-1: #1a1a19;
    --text-primary: #f0efec;
    --text-secondary: #c3c2b7;
    --grid: #33322f;
    --series-1: #3987e5;
    --series-2: #d95926;
  }
}
.legend {
  display: flex;
  gap: 14px;
  font-size: 12px;
  color: var(--text-secondary);
  margin-bottom: 4px;
}
.key i,
.row i {
  display: inline-block;
  width: 12px;
  height: 2px;
  border-radius: 1px;
  margin-right: 6px;
  vertical-align: middle;
}
i.s1 {
  background: var(--series-1);
}
i.s2 {
  background: var(--series-2);
}
.plot {
  position: relative;
}
.area {
  position: relative;
  margin-left: 52px;
}
svg {
  display: block;
  width: 100%;
  touch-action: none;
}
.grid line {
  stroke: var(--grid);
  stroke-width: 1;
  vector-effect: non-scaling-stroke;
}
.line {
  fill: none;
  stroke-width: 2;
  stroke-linejoin: round;
  stroke-linecap: round;
}
.line.s1 {
  stroke: var(--series-1);
}
.line.s2 {
  stroke: var(--series-2);
}
.cross {
  stroke: var(--text-secondary);
  stroke-width: 1;
  opacity: 0.5;
}
.ylab,
.xlab {
  position: absolute;
  font-size: 11px;
  color: var(--text-secondary);
  pointer-events: none;
  white-space: nowrap;
}
.ylab {
  left: 0;
  width: 46px;
  text-align: right;
  transform: translateY(-50%);
}
.dot {
  position: absolute;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  box-shadow: 0 0 0 2px var(--surface-1);
  transform: translate(-50%, -50%);
  pointer-events: none;
}
.dot.s1 {
  background: var(--series-1);
}
.dot.s2 {
  background: var(--series-2);
}
.tip {
  position: absolute;
  top: 4px;
  background: var(--surface-1);
  color: var(--text-primary);
  border: 1px solid var(--grid);
  border-radius: 6px;
  padding: 6px 8px;
  font-size: 12px;
  pointer-events: none;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.12);
  white-space: nowrap;
}
.tip .t {
  color: var(--text-secondary);
  margin-bottom: 2px;
}
.tip .row {
  display: flex;
  align-items: center;
  gap: 4px;
}
.tip b {
  margin-left: auto;
  padding-left: 12px;
  font-variant-numeric: tabular-nums;
}
.empty {
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
  color: var(--text-secondary);
}
</style>
