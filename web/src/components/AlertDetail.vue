<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NCard, NDescriptions, NDescriptionsItem, NModal, NTag } from 'naive-ui'
import type { AlertEvent } from '../api/types'
import { datetime } from '../format'

const props = defineProps<{ show: boolean; event: AlertEvent | null }>()
const emit = defineEmits<{ 'update:show': [boolean] }>()
const { t, te, locale } = useI18n()
const kind = computed(() => {
  const key = `alert.rule_kinds.${props.event?.kind ?? ''}`
  return te(key) ? t(key) : (props.event?.kind ?? '')
})
const severityType = (severity: string) => severity === 'critical' ? 'error' : severity === 'warning' ? 'warning' : severity === 'resolved' ? 'success' : 'info'
</script>

<template>
  <n-modal :show="show" @update:show="(v: boolean) => emit('update:show', v)">
    <n-card v-if="event" :title="event.title" closable class="alert-detail" @close="emit('update:show', false)">
      <n-descriptions :column="1" label-placement="top" size="small">
        <n-descriptions-item :label="t('alert.event_id')">#{{ event.id }}</n-descriptions-item>
        <n-descriptions-item :label="t('tbx.created')">{{ datetime(event.created_at, locale) }}</n-descriptions-item>
        <n-descriptions-item :label="t('alert.severity')">
          <n-tag size="small" :type="severityType(event.severity)">{{ t(`alert.sev.${event.severity}`) }}</n-tag>
        </n-descriptions-item>
        <n-descriptions-item :label="t('alert.kind')">{{ kind }}</n-descriptions-item>
        <n-descriptions-item v-if="event.rule_name" :label="t('alert.rule')">{{ event.rule_name }}</n-descriptions-item>
        <n-descriptions-item v-if="event.key" :label="t('alert.target')">{{ event.key }}</n-descriptions-item>
        <n-descriptions-item :label="t('alert.message')"><pre class="body">{{ event.body || '-' }}</pre></n-descriptions-item>
        <n-descriptions-item v-if="event.result" :label="t('alert.delivery')"><pre class="body">{{ event.result }}</pre></n-descriptions-item>
      </n-descriptions>
      <template #footer><n-button @click="emit('update:show', false)">{{ t('common.close') }}</n-button></template>
    </n-card>
  </n-modal>
</template>

<style scoped>
.alert-detail {
  width: min(620px, calc(100vw - 24px));
  max-height: calc(100dvh - 32px);
  overflow: auto;
}
.body {
  margin: 0;
  font: inherit;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  user-select: text;
}
</style>
