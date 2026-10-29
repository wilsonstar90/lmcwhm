<script setup lang="ts">
// "Update available" button of an installed app or PHP version; confirming emits update.
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NPopconfirm } from 'naive-ui'
import type { AppUpdate } from '../api/types'

const props = defineProps<{ upd?: AppUpdate; name: string; databases?: boolean }>()
const emit = defineEmits<{ update: [] }>()
const { t } = useI18n()
const target = computed(() => (props.upd?.latest && props.upd.latest !== props.upd.current ? props.upd.latest : t('app.new_build')))
</script>

<template>
  <n-popconfirm v-if="upd?.available" :positive-text="t('app.update')" @positive-click="emit('update')">
    <template #trigger>
      <n-button size="tiny" type="success" secondary>{{ t('app.update_to', { v: target }) }}</n-button>
    </template>
    <div style="max-width: 320px">
      <div>{{ t('app.update_confirm', { name, from: upd.current || '?', to: target }) }}</div>
      <div v-if="databases" style="margin-top: 6px">{{ t('app.update_backup') }}</div>
    </div>
  </n-popconfirm>
</template>
