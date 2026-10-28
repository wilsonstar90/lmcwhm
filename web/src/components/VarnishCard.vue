<script setup lang="ts">
// Varnish page cache: memory, hit rate, sites using it, purge.
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NCard, NInputNumber, NSpace, NTag, useMessage } from 'naive-ui'
import { get, post, put, ApiError } from '../api/client'
import { bytes } from '../format'
import TaskModal from './TaskModal.vue'

interface V {
  settings: { mem_mb: number }
  stats: { state: string; hits: number; misses: number; objects: number; bytes: number; uptime: number }
  sites: { id: number; domain: string; ttl: number }[]
  image: string
}
const { t } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))
const v = ref<V | null>(null)
const mem = ref(256)
const taskId = ref<string | null>(null)
async function load() {
  try {
    v.value = await get<V>('/varnish')
    mem.value = v.value.settings.mem_mb
  } catch (e) {
    fail(e)
  }
}
onMounted(load)
const hitRate = computed(() => {
  const s = v.value?.stats
  return s && s.hits + s.misses > 0 ? `${((s.hits / (s.hits + s.misses)) * 100).toFixed(1)}%` : '—'
})
async function save() {
  try {
    taskId.value = (await put<{ task_id: string }>('/varnish', { mem_mb: mem.value })).task_id
  } catch (e) {
    fail(e)
  }
}
async function purgeAll() {
  try {
    await post('/varnish/purge', { site_id: 0 })
    msg.success(t('common.done'))
    load()
  } catch (e) {
    fail(e)
  }
}
</script>

<template>
  <n-card v-if="v" size="small" title="Varnish">
    <template #header-extra>
      <n-tag size="small" :type="v.stats.state === 'running' ? 'success' : 'default'">{{ v.stats.state || t('varnish.idle') }}</n-tag>
    </template>
    <n-space align="center" :size="24" :wrap="true">
      <span>{{ t('varnish.hit_rate') }}: <b>{{ hitRate }}</b></span>
      <span>{{ t('varnish.objects') }}: <b>{{ v.stats.objects }}</b></span>
      <span>{{ t('varnish.storage') }}: <b>{{ bytes(v.stats.bytes) }}</b></span>
      <span>{{ t('varnish.sites') }}: {{ v.sites.map((s) => s.domain).join(', ') || '—' }}</span>
    </n-space>
    <n-space align="center" style="margin-top: 12px">
      {{ t('varnish.memory') }}
      <n-input-number v-model:value="mem" :min="32" :max="65536" size="small" style="width: 140px"><template #suffix>MB</template></n-input-number>
      <n-button size="small" type="primary" @click="save">{{ t('common.save') }}</n-button>
      <n-button size="small" :disabled="v.stats.state !== 'running'" @click="purgeAll">{{ t('varnish.purge_all') }}</n-button>
    </n-space>
    <p class="hint">{{ t('varnish.hint') }}</p>
  </n-card>
  <TaskModal :task-id="taskId" title="Varnish" @close="taskId = null; load()" @done="load" />
</template>

<style scoped>
.hint {
  margin: 12px 0 0;
  font-size: 12px;
  opacity: 0.7;
}
</style>
