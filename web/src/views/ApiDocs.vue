<script setup lang="ts">
// OpenAPI reference generated from the router (GET /api/v1/openapi.json).
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NAlert, NButton, NCollapse, NCollapseItem, NInput, NSpace, NTag, useMessage } from 'naive-ui'
import { get, ApiError } from '../api/client'

interface Op { tags: string[]; operationId: string; parameters?: { name: string }[]; requestBody?: unknown }
interface Spec { info: { title: string; version: string; description: string }; paths: Record<string, Record<string, Op>> }

const { t } = useI18n()
const msg = useMessage()
const spec = ref<Spec | null>(null)
const q = ref('')
onMounted(async () => {
  try {
    spec.value = await get<Spec>('/openapi.json')
  } catch (e) {
    msg.error(e instanceof ApiError ? e.text : String(e))
  }
})
const groups = computed(() => {
  const out: Record<string, { method: string; path: string; op: Op }[]> = {}
  const needle = q.value.trim().toLowerCase()
  for (const [path, ops] of Object.entries(spec.value?.paths ?? {})) {
    for (const [method, op] of Object.entries(ops)) {
      if (needle && !path.toLowerCase().includes(needle) && !op.tags[0].includes(needle)) continue
      ;(out[op.tags[0]] ??= []).push({ method: method.toUpperCase(), path, op })
    }
  }
  for (const k in out) out[k].sort((a, b) => a.path.localeCompare(b.path) || a.method.localeCompare(b.method))
  return Object.entries(out).sort(([a], [b]) => a.localeCompare(b))
})
const methodType = (m: string) => (m === 'GET' ? 'info' : m === 'DELETE' ? 'error' : m === 'PUT' || m === 'PATCH' ? 'warning' : 'success')
const curl = (m: string, p: string) =>
  `curl -k -H "Authorization: Bearer $LMCWHM_TOKEN"${m !== 'GET' ? ` -X ${m} -H "Content-Type: application/json" -d '{}'` : ''} ${location.origin}/api/v1${p}`
function download() {
  const a = document.createElement('a')
  a.href = URL.createObjectURL(new Blob([JSON.stringify(spec.value, null, 2)], { type: 'application/json' }))
  a.download = 'lmcwhm-openapi.json'
  a.click()
}
const copy = (s: string) => navigator.clipboard?.writeText(s).then(() => msg.success(t('common.copied')))
</script>

<template>
  <template v-if="spec">
    <h2 style="margin: 0 0 4px">{{ spec.info.title }} <n-tag size="small">{{ spec.info.version }}</n-tag></h2>
    <n-alert type="info" :show-icon="false" style="margin: 8px 0 12px">{{ t('api.intro') }}</n-alert>
    <n-space style="margin-bottom: 12px">
      <n-input v-model:value="q" :placeholder="t('tb.search')" clearable style="width: 260px" />
      <n-button @click="download">openapi.json</n-button>
      <n-button tag="a" href="/ai">{{ t('api.tokens') }}</n-button>
    </n-space>
    <n-collapse>
      <n-collapse-item v-for="[tag, ops] in groups" :key="tag" :title="`${t(`menu.${tag}`, tag)} (${ops.length})`" :name="tag">
        <div v-for="o in ops" :key="o.method + o.path" class="op">
          <n-tag size="small" :type="methodType(o.method)" style="width: 64px; justify-content: center">{{ o.method }}</n-tag>
          <code class="path">/api/v1{{ o.path }}</code>
          <n-button text size="tiny" @click="copy(curl(o.method, o.path))">curl</n-button>
        </div>
      </n-collapse-item>
    </n-collapse>
  </template>
</template>

<style scoped>
.op {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 3px 0;
}
.path {
  flex: 1;
  word-break: break-all;
}
</style>
