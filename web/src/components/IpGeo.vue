<script setup lang="ts">
// An IP address with its country flag, location and network (DB-IP Lite).
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { NTooltip } from 'naive-ui'
import { flag, geo, geoLabel } from '../geo'

const props = defineProps<{ ip: string; compact?: boolean }>()
const { t, locale } = useI18n()
const info = computed(() => geo(props.ip))
const label = computed(() => geoLabel(info.value, locale.value, t('geo.lan')))
const net = computed(() => (info.value?.asn ? `AS${info.value.asn} ${info.value.org ?? ''}` : ''))
</script>

<template>
  <n-tooltip :disabled="!net && !label">
    <template #trigger>
      <span class="ipgeo">
        <code>{{ ip }}</code>
        <span v-if="label" class="loc">{{ flag(info?.country) }} {{ compact ? info?.country : label }}</span>
      </span>
    </template>
    <div>{{ label }}</div>
    <div v-if="net" style="opacity: 0.8">{{ net }}</div>
    <div style="opacity: 0.5; font-size: 11px">IP data © DB-IP.com (CC BY 4.0)</div>
  </n-tooltip>
</template>

<style scoped>
.ipgeo {
  display: inline-flex;
  gap: 6px;
  align-items: baseline;
  white-space: nowrap;
}
.loc {
  font-size: 12px;
  opacity: 0.75;
}
</style>
