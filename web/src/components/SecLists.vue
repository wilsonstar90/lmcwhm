<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NSpace, NTabPane, NTabs } from 'naive-ui'
import SecAutoIPs from './SecAutoIPs.vue'
import SecFeeds from './SecFeeds.vue'

// 雲端黑白名單: what came in automatically (lists, CrowdSec, AI), and the
// lists it comes from: trusted ranges (never banned) and malicious IPs.
const emit = defineEmits<{ changed: [] }>()
const { t } = useI18n()
const tab = ref('entries')
</script>

<template>
  <n-tabs v-model:value="tab" type="line" size="small">
    <n-tab-pane name="entries" :tab="t('fwl.entries')" display-directive="show:lazy">
      <SecAutoIPs @changed="emit('changed')" />
    </n-tab-pane>
    <n-tab-pane name="sources" :tab="t('fwl.sources')" display-directive="show:lazy">
      <n-space vertical :size="16">
        <SecFeeds kind="block" @changed="emit('changed')" />
        <SecFeeds kind="trust" @changed="emit('changed')" />
      </n-space>
    </n-tab-pane>
  </n-tabs>
</template>
