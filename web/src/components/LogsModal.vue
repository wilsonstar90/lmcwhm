<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NModal, NCard, NButton, NSpace, NSwitch } from 'naive-ui'
import { get } from '../api/client'

// url returns { logs: string }; empty url closes the modal.
const props = defineProps<{ url: string | null; title: string }>()
const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()

const logs = ref('')
const follow = ref(false)
const el = ref<HTMLElement | null>(null)
let timer: number | undefined

async function load() {
  if (!props.url) return
  try {
    logs.value = (await get<{ logs: string }>(props.url)).logs || '—'
  } catch (e) {
    logs.value = String(e)
  }
  await nextTick()
  el.value?.scrollTo({ top: el.value.scrollHeight })
}

watch(
  () => props.url,
  (u) => {
    logs.value = ''
    follow.value = false
    if (u) load()
  },
  { immediate: true },
)
watch(follow, (on) => {
  clearInterval(timer)
  if (on) timer = window.setInterval(load, 2000)
})
onBeforeUnmount(() => clearInterval(timer))

function close() {
  clearInterval(timer)
  follow.value = false
  emit('close')
}
</script>

<template>
  <n-modal :show="!!url" @update:show="(v: boolean) => !v && close()">
    <n-card :title="title" style="width: min(960px, calc(100vw - 32px))" closable @close="close">
      <pre ref="el" class="log">{{ logs || '…' }}</pre>
      <template #footer>
        <n-space justify="space-between" align="center">
          <n-space align="center" :size="8">
            <n-switch v-model:value="follow" size="small" />
            <span>{{ t('common.follow') }}</span>
          </n-space>
          <n-space>
            <n-button @click="load">{{ t('common.refresh') }}</n-button>
            <n-button @click="close">{{ t('common.close') }}</n-button>
          </n-space>
        </n-space>
      </template>
    </n-card>
  </n-modal>
</template>

<style scoped>
.log {
  margin: 0;
  height: 60vh;
  overflow: auto;
  font-size: 12px;
  line-height: 1.5;
  padding: 12px;
  border-radius: 4px;
  background: #1e1e1e;
  color: #ddd;
  white-space: pre-wrap;
  word-break: break-all;
}
</style>
