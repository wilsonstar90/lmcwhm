<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { NEmpty } from 'naive-ui'

defineProps<{ milestone: string }>()
const { t } = useI18n()
</script>

<template>
  <n-empty :description="t('common.coming', { m: milestone })" style="margin-top: 120px" />
</template>
