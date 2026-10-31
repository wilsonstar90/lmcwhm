<script setup lang="ts">
import { useRoute } from 'vue-router'
import TerminalView from '../components/TerminalView.vue'

const route = useRoute()
</script>

<template>
  <div style="height: calc(100dvh - 90px)">
    <TerminalView :cwd="typeof route.query.cwd === 'string' ? route.query.cwd : ''" />
  </div>
</template>
