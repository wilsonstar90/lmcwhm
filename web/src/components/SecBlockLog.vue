<script setup lang="ts">
// The block log page: auto-ban settings on top, then every refused or
// flagged request of the chosen days.
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import SecAutoBan from './SecAutoBan.vue'
import SecEvents from './SecEvents.vue'
import TaskModal from './TaskModal.vue'

const emit = defineEmits<{ changed: [] }>()
const router = useRouter()
const task = ref<{ id: string; title: string } | null>(null)
// A CRS rule id opens the WAF's OWASP rule list on that rule.
const openCRS = (id: string) => router.push({ query: { tab: 'waf', sub: 'crs', crs: id } })
</script>

<template>
  <SecAutoBan @changed="emit('changed')" />
  <SecEvents @crs="openCRS" @changed="emit('changed')" @task="(id: string, title: string) => (task = { id, title })" />
  <TaskModal :task-id="task?.id ?? null" :title="task?.title ?? ''" @close="task = null" />
</template>
