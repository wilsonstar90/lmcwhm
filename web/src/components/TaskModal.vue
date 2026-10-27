<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NModal, NCard, NTag, NButton, NSpin } from 'naive-ui'
import { get } from '../api/client'
import type { Task } from '../api/types'

const props = defineProps<{ taskId: string | null; title: string }>()
const emit = defineEmits<{ close: []; done: [ok: boolean] }>()
const { t } = useI18n()

const task = ref<Task | null>(null)
const logEl = ref<HTMLElement | null>(null)
let timer: number | undefined

async function poll() {
  if (!props.taskId) return
  try {
    task.value = await get<Task>(`/tasks/${props.taskId}`)
    await nextTick()
    logEl.value?.scrollTo({ top: logEl.value.scrollHeight })
    if (task.value.status !== 'running') {
      clearInterval(timer)
      emit('done', task.value.status === 'success')
    }
  } catch {
    /* keep polling */
  }
}

watch(
  () => props.taskId,
  (id) => {
    clearInterval(timer)
    task.value = null
    if (id) {
      poll()
      timer = window.setInterval(poll, 1000)
    }
  },
  { immediate: true },
)
onBeforeUnmount(() => clearInterval(timer))

const tag = { running: 'info', success: 'success', failed: 'error' } as const
</script>

<template>
  <n-modal :show="!!taskId" :mask-closable="false" @update:show="(v: boolean) => !v && emit('close')">
    <n-card :title="title" style="width: min(720px, calc(100vw - 32px))" closable @close="emit('close')">
      <template #header-extra>
        <n-tag v-if="task" :type="tag[task.status]" size="small">
          <n-spin v-if="task.status === 'running'" :size="12" style="margin-right: 6px" />
          {{ t(`task.${task.status}`) }}
        </n-tag>
      </template>
      <pre ref="logEl" class="log">{{ task?.log.join('\n') || '…' }}</pre>
      <template #footer>
        <n-button :disabled="task?.status === 'running'" @click="emit('close')">{{ t('common.close') }}</n-button>
      </template>
    </n-card>
  </n-modal>
</template>

<style scoped>
.log {
  margin: 0;
  max-height: 50vh;
  min-height: 160px;
  overflow: auto;
  font-size: 12px;
  line-height: 1.5;
  padding: 12px;
  border-radius: 4px;
  background: rgba(127, 127, 127, 0.1);
  white-space: pre-wrap;
  word-break: break-all;
}
</style>
