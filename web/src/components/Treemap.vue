<script setup lang="ts">
// Squarified treemap of shares (browsers, operating systems): area is the
// count, one hue stepped by rank, labels inside tiles big enough to hold them.
import { computed } from 'vue'

const props = defineProps<{ items: { key: string; hits: number }[]; height?: number }>()
interface Tile { key: string; hits: number; x: number; y: number; w: number; h: number; share: number }

const total = computed(() => props.items.reduce((a, b) => a + b.hits, 0))
const tiles = computed<Tile[]>(() => {
  const list = props.items.filter((i) => i.hits > 0).slice(0, 10)
  const sum = list.reduce((a, b) => a + b.hits, 0)
  if (!sum) return []
  const out: Tile[] = []
  // Squarify on a 100 x 100 box, rows along the shorter side.
  let x = 0, y = 0, w = 100, h = 100
  let rest = list.map((i) => ({ ...i, area: (i.hits / sum) * 10000 }))
  const worst = (row: { area: number }[], side: number) => {
    const s = row.reduce((a, b) => a + b.area, 0)
    const mx = Math.max(...row.map((r) => r.area)), mn = Math.min(...row.map((r) => r.area))
    return Math.max((side * side * mx) / (s * s), (s * s) / (side * side * mn))
  }
  while (rest.length) {
    const side = Math.min(w, h)
    const row = [rest[0]]
    let i = 1
    while (i < rest.length && worst([...row, rest[i]], side) <= worst(row, side)) row.push(rest[i++])
    rest = rest.slice(i)
    const s = row.reduce((a, b) => a + b.area, 0)
    const thick = s / side
    let off = 0
    for (const r of row) {
      const len = r.area / thick
      out.push(w >= h
        ? { key: r.key, hits: r.hits, x, y: y + off, w: thick, h: len, share: r.hits / total.value }
        : { key: r.key, hits: r.hits, x: x + off, y, w: len, h: thick, share: r.hits / total.value })
      off += len
    }
    if (w >= h) { x += thick; w -= thick } else { y += thick; h -= thick }
  }
  return out
})
const pct = (v: number) => `${(v * 100).toFixed(v < 0.1 ? 1 : 0)}%`
</script>

<template>
  <div class="tm" :style="{ height: `${height ?? 220}px` }">
    <div
      v-for="(t, i) in tiles" :key="t.key" class="tile" :class="{ light: 0.85 - i * 0.08 < 0.5 }" :title="`${t.key}: ${t.hits.toLocaleString()} (${pct(t.share)})`"
      :style="{ left: `${t.x}%`, top: `${t.y}%`, width: `${t.w}%`, height: `${t.h}%`, '--o': String(Math.max(0.18, 0.85 - i * 0.08)) }"
    >
      <template v-if="t.w > 14 && t.h > 16">
        <b>{{ t.key }}</b><span>{{ t.hits.toLocaleString() }}</span><em>{{ pct(t.share) }}</em>
      </template>
    </div>
    <div v-if="!tiles.length" class="empty">—</div>
  </div>
</template>

<style scoped>
.tm {
  --hue: #2a78d6;
  --gap: #fcfcfb;
  position: relative;
}
html[data-theme='dark'] .tm {
  --hue: #3987e5;
  --gap: #1a1a19;
}
.tile {
  position: absolute;
  box-sizing: border-box;
  border: 2px solid var(--gap);
  border-radius: 6px;
  background: color-mix(in srgb, var(--hue) calc(var(--o) * 100%), transparent);
  padding: 6px 8px;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  font-size: 12px;
  color: #fff;
  text-shadow: 0 1px 2px rgba(0, 0, 0, 0.35);
}
.tile.light {
  color: inherit;
  text-shadow: none;
}
.tile b {
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.tile span {
  opacity: 0.9;
}
.tile em {
  margin-top: auto;
  font-style: normal;
  font-size: 15px;
  font-weight: 600;
  text-align: right;
}
.empty {
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
  opacity: 0.5;
}
</style>
