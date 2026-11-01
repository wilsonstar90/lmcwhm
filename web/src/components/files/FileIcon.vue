<script setup lang="ts">
// File and folder icons: a page with a folded corner and a coloured band
// naming the type (PHP, JS, ZIP, ...), and a flat folder. Drawn here, so they
// stay sharp at any size and need no image files.
import { computed } from 'vue'

const props = withDefaults(defineProps<{ name: string; dir?: boolean; size?: number }>(), { dir: false, size: 20 })

// [band colour, text colour]
const COLORS: Record<string, [string, string]> = {
  php: ['#777bb3', '#fff'], js: ['#f0c419', '#3b3000'], mjs: ['#f0c419', '#3b3000'], ts: ['#3178c6', '#fff'], vue: ['#42b883', '#fff'],
  html: ['#e34f26', '#fff'], htm: ['#e34f26', '#fff'], css: ['#1572b6', '#fff'], scss: ['#c6538c', '#fff'], json: ['#5b6b7f', '#fff'],
  py: ['#3776ab', '#fff'], go: ['#00a7d0', '#fff'], java: ['#e76f00', '#fff'], rb: ['#cc342d', '#fff'], sh: ['#4eaa25', '#fff'],
  sql: ['#f29111', '#fff'], md: ['#1f6feb', '#fff'], txt: ['#6b7280', '#fff'], log: ['#94a3b8', '#fff'], xml: ['#f97316', '#fff'],
  yml: ['#cb171e', '#fff'], yaml: ['#cb171e', '#fff'], ini: ['#64748b', '#fff'], conf: ['#64748b', '#fff'], env: ['#64748b', '#fff'],
  htaccess: ['#64748b', '#fff'], lock: ['#64748b', '#fff'],
  zip: ['#a16207', '#fff'], gz: ['#a16207', '#fff'], tgz: ['#a16207', '#fff'], tar: ['#a16207', '#fff'], rar: ['#a16207', '#fff'], '7z': ['#a16207', '#fff'],
  bz2: ['#a16207', '#fff'], xz: ['#a16207', '#fff'],
  jpg: ['#10b981', '#fff'], jpeg: ['#10b981', '#fff'], png: ['#10b981', '#fff'], gif: ['#10b981', '#fff'], webp: ['#10b981', '#fff'],
  svg: ['#f59e0b', '#fff'], ico: ['#10b981', '#fff'], bmp: ['#10b981', '#fff'],
  pdf: ['#e11d48', '#fff'], doc: ['#2b579a', '#fff'], docx: ['#2b579a', '#fff'], xls: ['#217346', '#fff'], xlsx: ['#217346', '#fff'],
  csv: ['#217346', '#fff'], ppt: ['#d24726', '#fff'], pptx: ['#d24726', '#fff'],
  mp4: ['#8b5cf6', '#fff'], mkv: ['#8b5cf6', '#fff'], mov: ['#8b5cf6', '#fff'], webm: ['#8b5cf6', '#fff'], avi: ['#8b5cf6', '#fff'],
  mp3: ['#ec4899', '#fff'], wav: ['#ec4899', '#fff'], flac: ['#ec4899', '#fff'], ogg: ['#ec4899', '#fff'],
  ttf: ['#475569', '#fff'], woff: ['#475569', '#fff'], woff2: ['#475569', '#fff'], pem: ['#0f766e', '#fff'], key: ['#0f766e', '#fff'], crt: ['#0f766e', '#fff'],
}
const DEFAULT: [string, string] = ['#9ca3af', '#fff']

const ext = computed(() => {
  const n = props.name.toLowerCase()
  if (n.endsWith('.tar.gz')) return 'tgz'
  const i = n.lastIndexOf('.')
  if (i < 0) return ''
  return n.slice(i + 1)
})
const label = computed(() => (ext.value === 'htaccess' ? 'HTA' : ext.value.slice(0, 4).toUpperCase()))
const color = computed(() => COLORS[ext.value] ?? DEFAULT)
// Smaller letters for longer labels so they fit the band.
const fontSize = computed(() => (label.value.length >= 4 ? 6.2 : label.value.length === 3 ? 7.2 : 8.4))
</script>

<template>
  <svg v-if="dir" class="fi" :width="size" :height="size" viewBox="0 0 32 32" aria-hidden="true">
    <path d="M3 8.5A2.5 2.5 0 0 1 5.5 6h7.1l2.6 3H26.5A2.5 2.5 0 0 1 29 11.5v13A2.5 2.5 0 0 1 26.5 27h-21A2.5 2.5 0 0 1 3 24.5z" fill="#f2b33d" />
    <path d="M3 12.5A2.5 2.5 0 0 1 5.5 10h21a2.5 2.5 0 0 1 2.5 2.5v12a2.5 2.5 0 0 1-2.5 2.5h-21A2.5 2.5 0 0 1 3 24.5z" fill="#ffcb5c" />
  </svg>
  <svg v-else class="fi" :width="size" :height="size" viewBox="0 0 32 32" aria-hidden="true">
    <path d="M7.5 2.5h12.2L26.5 9.3V27a2.5 2.5 0 0 1-2.5 2.5H7.5A2.5 2.5 0 0 1 5 27V5a2.5 2.5 0 0 1 2.5-2.5z" fill="#fff" stroke="#c9ced6" stroke-width="1.2" />
    <path d="M19.7 2.5v4.3a2.5 2.5 0 0 0 2.5 2.5h4.3" fill="#e8ebf0" stroke="#c9ced6" stroke-width="1.2" stroke-linejoin="round" />
    <template v-if="label">
      <rect x="2" y="15" width="22" height="10" rx="2" :fill="color[0]" />
      <text x="13" y="22.4" text-anchor="middle" :font-size="fontSize" font-weight="700" :fill="color[1]" font-family="system-ui, -apple-system, Segoe UI, Roboto, sans-serif">{{ label }}</text>
    </template>
    <template v-else>
      <rect x="9" y="14" width="13" height="1.8" rx=".9" fill="#c9ced6" />
      <rect x="9" y="18" width="13" height="1.8" rx=".9" fill="#c9ced6" />
      <rect x="9" y="22" width="9" height="1.8" rx=".9" fill="#c9ced6" />
    </template>
  </svg>
</template>

<style scoped>
.fi {
  display: inline-block;
  flex: none;
  vertical-align: middle;
}
</style>
