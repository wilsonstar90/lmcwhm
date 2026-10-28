<script setup lang="ts">
import { NDescriptions, NDescriptionsItem, NDrawer, NDrawerContent, NSpace } from 'naive-ui'
import type { VNodeChild } from 'vue'

// The details of one row (an IP, a list, an alert, a finding…), opened by
// clicking it. Values may be text or rendered nodes; actions go in the footer.
export interface DetailRow { label: string; value: string | number | (() => VNodeChild) | null | undefined; mono?: boolean }

defineProps<{ show: boolean; title: string; rows: DetailRow[]; width?: number }>()
const emit = defineEmits<{ close: [] }>()
</script>

<template>
  <n-drawer :show="show" :width="width ?? 520" placement="right" style="max-width: 100vw" @update:show="(v: boolean) => !v && emit('close')">
    <n-drawer-content :title="title" closable>
      <n-descriptions :column="1" label-placement="left" bordered size="small" label-style="width: 130px; white-space: nowrap">
        <n-descriptions-item v-for="r in rows" :key="r.label" :label="r.label">
          <component :is="r.value" v-if="typeof r.value === 'function'" />
          <span v-else :class="{ mono: r.mono }">{{ r.value === '' || r.value == null ? '—' : r.value }}</span>
        </n-descriptions-item>
      </n-descriptions>
      <slot />
      <template v-if="$slots.actions" #footer>
        <n-space justify="end"><slot name="actions" /></n-space>
      </template>
    </n-drawer-content>
  </n-drawer>
</template>

<style scoped>
.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12.5px;
  word-break: break-all;
  white-space: pre-wrap;
}
</style>
