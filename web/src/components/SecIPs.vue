<script setup lang="ts">
// 黑白名單 (as aaPanel's WAF): IP, User-Agent and URL white / black lists,
// only what was added by hand. Automatic entries are under 雲端黑白名單.
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NAlert, NTabPane, NTabs } from 'naive-ui'
import { get } from '../api/client'
import type { FirewallView } from '../api/types'
import SecListTab from './SecListTab.vue'

const emit = defineEmits<{ changed: [] }>()
const { t } = useI18n()
const tabs = [
  { kind: 'ip', list: 'allow' }, { kind: 'ip', list: 'block' }, { kind: 'ua', list: 'allow' },
  { kind: 'ua', list: 'block' }, { kind: 'url', list: 'allow' }, { kind: 'url', list: 'block' },
] as const
const tab = ref('ip_block')
const fwOn = ref(true)
onMounted(async () => {
  try {
    fwOn.value = (await get<FirewallView>('/firewall')).enabled
  } catch {
    /* shown by the firewall section */
  }
})
</script>

<template>
  <n-alert v-if="!fwOn && tab.startsWith('ip')" type="warning" :show-icon="false" style="margin-bottom: 12px">{{ t('fw.lists_inactive') }}</n-alert>
  <n-tabs v-model:value="tab" type="card" size="small">
    <n-tab-pane v-for="x in tabs" :key="`${x.kind}_${x.list}`" :name="`${x.kind}_${x.list}`" :tab="t(`fwl.tabs.${x.kind}_${x.list}`)" display-directive="show:lazy">
      <SecListTab :kind="x.kind" :list="x.list" @changed="emit('changed')" />
    </n-tab-pane>
  </n-tabs>
</template>
