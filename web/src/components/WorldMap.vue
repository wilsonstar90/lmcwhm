<script setup lang="ts">
// Choropleth of requests per country: one hue, light to dark on a log
// scale, no-data in the grid grey. Hover a country for its name and count.
import { computed, ref } from 'vue'
import { geoNaturalEarth1, geoPath } from 'd3-geo'
import { feature } from 'topojson-client'
import type { FeatureCollection, Geometry } from 'geojson'
import world from 'world-atlas/countries-110m.json'
import countries from 'i18n-iso-countries'

const props = defineProps<{ counts: Record<string, number>; tone: 'visit' | 'block'; locale: string; label: string }>()
const emit = defineEmits<{ pick: [code: string] }>()

const W = 960
const H = 470
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const fc = feature(world as any, (world as any).objects.countries) as unknown as FeatureCollection<Geometry, { name: string }>
const shapes = fc.features.filter((f) => f.properties.name !== 'Antarctica')
const projection = geoNaturalEarth1().fitExtent([[4, 4], [W - 4, H - 4]], { type: 'FeatureCollection', features: shapes })
const path = geoPath(projection)
const items = shapes.map((f) => ({ code: countries.numericToAlpha2(String(f.id ?? '')) ?? '', d: path(f) ?? '', name: f.properties.name }))

const max = computed(() => Math.max(1, ...Object.values(props.counts)))
const level = (n: number) => (n > 0 ? 0.18 + 0.82 * (Math.log(n + 1) / Math.log(max.value + 1)) : 0)
const regionName = computed(() => {
  try {
    return new Intl.DisplayNames([props.locale], { type: 'region' })
  } catch {
    return null
  }
})
const nameOf = (code: string, fallback: string) => (code && regionName.value?.of(code)) || fallback

const hover = ref<{ code: string; name: string; n: number; x: number; y: number } | null>(null)
const box = ref<HTMLElement | null>(null)
function move(e: MouseEvent, it: (typeof items)[number]) {
  const r = box.value!.getBoundingClientRect()
  hover.value = { code: it.code, name: nameOf(it.code, it.name), n: props.counts[it.code] ?? 0, x: e.clientX - r.left, y: e.clientY - r.top }
}
</script>

<template>
  <div ref="box" class="map" :class="tone" @mouseleave="hover = null">
    <svg :viewBox="`0 0 ${W} ${H}`" role="img" :aria-label="label">
      <path
        v-for="it in items" :key="it.code + it.name" :d="it.d" class="c" :class="{ has: (counts[it.code] ?? 0) > 0 }"
        :style="(counts[it.code] ?? 0) > 0 ? { fillOpacity: level(counts[it.code]) } : undefined"
        @mousemove="move($event, it)" @click="counts[it.code] && emit('pick', it.code)"
      />
    </svg>
    <div v-if="hover" class="tip" :style="{ left: `${hover.x + 12}px`, top: `${hover.y + 12}px` }">
      <b>{{ hover.name }}</b><span>{{ label }}: {{ hover.n.toLocaleString() }}</span>
    </div>
    <div class="legend"><span>0</span><i class="ramp" /><span>{{ max.toLocaleString() }}</span></div>
  </div>
</template>

<style scoped>
.map {
  --grid: #e7e6e2;
  --edge: #fcfcfb;
  --hue: #2a78d6;
  position: relative;
}
.map.block {
  --hue: #eb6834;
}
@media (prefers-color-scheme: dark) {
  .map {
    --grid: #33322f;
    --edge: #1a1a19;
    --hue: #3987e5;
  }
  .map.block {
    --hue: #d95926;
  }
}
svg {
  width: 100%;
  height: auto;
  display: block;
}
.c {
  fill: var(--grid);
  stroke: var(--edge);
  stroke-width: 0.6;
}
.c.has {
  fill: var(--hue);
  cursor: pointer;
}
.c:hover {
  stroke: currentColor;
  stroke-width: 1;
}
.tip {
  position: absolute;
  pointer-events: none;
  background: rgba(20, 20, 20, 0.88);
  color: #fff;
  font-size: 12px;
  padding: 6px 8px;
  border-radius: 6px;
  display: flex;
  flex-direction: column;
  gap: 2px;
  white-space: nowrap;
  z-index: 2;
}
.legend {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 11px;
  opacity: 0.7;
  margin-top: 4px;
}
.ramp {
  width: 90px;
  height: 6px;
  border-radius: 3px;
  background: linear-gradient(90deg, color-mix(in srgb, var(--hue) 18%, transparent), var(--hue));
}
</style>
