<script setup lang="ts">
import { NSpace } from 'naive-ui'
import SecFeeds from './SecFeeds.vue'

// Auto-updated lists: trusted ranges (never banned) and malicious IPs.
const emit = defineEmits<{ changed: [] }>()
</script>

<template>
  <n-space vertical :size="16">
    <SecFeeds kind="block" @changed="emit('changed')" />
    <SecFeeds kind="trust" @changed="emit('changed')" />
  </n-space>
</template>
