<script setup lang="ts">
// Volume mounts as rows: host path (folder picker) or volume name, container
// path, read-only. The value is docker's "-v" form, one string per mount.
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NCheckbox, NInput } from 'naive-ui'
import PathInput from './PathInput.vue'

interface Row { host: string; dest: string; ro: boolean; opts: string[] }
const props = defineProps<{ value: string[] }>()
const emit = defineEmits<{ 'update:value': [v: string[]] }>()
const { t } = useI18n()

function parse(s: string): Row {
  const [host = '', dest = '', ...rest] = s.split(':')
  const opts = rest.join(':').split(',').filter(Boolean)
  return { host, dest, ro: opts.includes('ro'), opts: opts.filter((o) => o !== 'ro' && o !== 'rw') }
}
function format(r: Row): string {
  const opts = [...(r.ro ? ['ro'] : []), ...r.opts]
  return [r.host.trim(), r.dest.trim(), ...(opts.length ? [opts.join(',')] : [])].join(':')
}

const rows = ref<Row[]>([])
let last = ''
watch(
  () => props.value,
  (v) => {
    if (v.join('\n') === last) return
    rows.value = v.map(parse)
  },
  { immediate: true },
)
function sync() {
  const out = rows.value.filter((r) => r.host.trim() && r.dest.trim()).map(format)
  last = out.join('\n')
  emit('update:value', out)
}
function add() {
  rows.value.push({ host: '', dest: '', ro: false, opts: [] })
}
function remove(i: number) {
  rows.value.splice(i, 1)
  sync()
}
</script>

<template>
  <div class="vols">
    <div v-for="(r, i) in rows" :key="i" class="row">
      <div class="host">
        <PathInput v-model:value="r.host" size="small" :placeholder="t('docker.c.host_or_volume')" @update:value="sync" />
      </div>
      <span class="sep">:</span>
      <n-input v-model:value="r.dest" size="small" class="dest" :placeholder="t('docker.c.container_path')" @update:value="sync" />
      <n-checkbox v-model:checked="r.ro" size="small" @update:checked="sync">{{ t('docker.c.read_only') }}</n-checkbox>
      <n-button size="small" quaternary @click="remove(i)">✕</n-button>
    </div>
    <n-button size="small" dashed @click="add">+ {{ t('docker.c.add_volume') }}</n-button>
  </div>
</template>

<style scoped>
.vols {
  display: flex;
  flex-direction: column;
  gap: 6px;
  width: 100%;
}
.row {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}
.host {
  flex: 3 1 220px;
  min-width: 0;
}
.dest {
  flex: 2 1 140px;
  min-width: 0;
}
.sep {
  opacity: 0.5;
}
</style>
