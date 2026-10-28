<script setup lang="ts">
// One-click AI analysis (一鍵 AI 分析): opens the assistant with a question.
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { NButton } from 'naive-ui'

const props = defineProps<{ prompt: string; size?: 'tiny' | 'small' | 'medium' }>()
const { t } = useI18n()
const router = useRouter()
</script>

<template>
  <n-button :size="props.size ?? 'small'" secondary type="info" @click="router.push({ name: 'ai', query: { q: props.prompt } })">✨ {{ t('ai.analyze') }}</n-button>
</template>
