<script setup lang="ts">
// Trusted visitor addresses for websites and the panel: Cloudflare is built
// in; Tunnel and other CDN peers are explicit, source-validated settings.
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NAlert, NButton, NCard, NCollapse, NCollapseItem, NFormItem, NInput, NSpace, NSwitch, NTag, useMessage } from 'naive-ui'
import { get, put, ApiError } from '../api/client'
import CdnHeaderList, { type CdnHeader } from './CdnHeaderList.vue'
import TaskModal from './TaskModal.vue'

interface CDNConfig { ranges: string[]; headers: CdnHeader[]; cloudflare_tunnel: boolean }
interface RequestAddress { peer_ip: string; client_ip: string; trusted_proxy: boolean; external_tls: boolean }

const { t } = useI18n()
const msg = useMessage()
const cf = ref<string[]>([])
const saved = ref<CDNConfig>({ ranges: [], headers: [], cloudflare_tunnel: false })
const ranges = ref('')
const headers = ref<CdnHeader[]>([])
const tunnel = ref(false)
const request = ref<RequestAddress | null>(null)
const saving = ref(false)
const task = ref<{ id: string; title: string } | null>(null)

function show(c: CDNConfig) {
  saved.value = c
  ranges.value = c.ranges.join('\n')
  headers.value = c.headers.map((x) => ({ ...x }))
  tunnel.value = c.cloudflare_tunnel
}
onMounted(async () => {
  try {
    const r = await get<{ config: CDNConfig; cloudflare: string[]; request: RequestAddress }>('/firewall/cdn')
    cf.value = r.cloudflare
    request.value = r.request
    show(r.config)
  } catch (e) {
    msg.error(e instanceof ApiError ? e.text : String(e))
  }
})
const list = computed(() => ranges.value.split(/[\s,]+/).map((x) => x.trim()).filter(Boolean))
const dirty = computed(() => tunnel.value !== saved.value.cloudflare_tunnel || JSON.stringify(headers.value) !== JSON.stringify(saved.value.headers) || list.value.join('\n') !== saved.value.ranges.join('\n'))
async function save() {
  saving.value = true
  try {
    const r = await put<{ config: CDNConfig; request: RequestAddress; task_id?: string }>('/firewall/cdn', { ranges: list.value, headers: headers.value, cloudflare_tunnel: tunnel.value })
    request.value = r.request
    show(r.config)
    if (r.task_id) task.value = { id: r.task_id, title: t('fw.cdn.title') }
    msg.success(t('common.saved'))
  } catch (e) {
    msg.error(e instanceof ApiError ? e.text : String(e))
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <n-card size="small" :title="t('fw.cdn.title')" style="margin-top: 16px">
    <p class="hint">{{ t('fw.cdn.hint') }}</p>
    <n-alert v-if="request" type="info" :show-icon="false" style="margin: 10px 0">
      {{ t('fw.cdn.request', { peer: request.peer_ip, client: request.client_ip, tls: request.external_tls ? 'HTTPS' : 'HTTP' }) }}
    </n-alert>
    <n-form-item :label="t('fw.cdn.tunnel')" :feedback="t('fw.cdn.tunnel_hint')">
      <n-switch v-model:value="tunnel" />
    </n-form-item>
    <n-alert v-if="tunnel" type="warning" :show-icon="false" style="margin-bottom: 12px">{{ t('fw.cdn.tunnel_warn') }}</n-alert>
    <n-alert type="info" :show-icon="false" style="margin-bottom: 12px">{{ t('fw.cdn.scope') }}</n-alert>
    <n-collapse style="margin: 8px 0 12px">
      <n-collapse-item name="cf">
        <template #header>
          <n-space :size="8" align="center">
            <b>Cloudflare</b><n-tag size="small" type="success" :bordered="false">{{ t('fw.cdn.builtin') }}</n-tag>
            <span class="hint">CF-Connecting-IP · {{ t('fw.cdn.ranges_n', { n: cf.length }) }}</span>
          </n-space>
        </template>
        <code class="ranges">{{ cf.join('  ') }}</code>
      </n-collapse-item>
    </n-collapse>
    <b>{{ t('fw.cdn.other') }}</b>
    <div class="lbl">{{ t('fw.cdn.headers') }}</div>
    <div style="max-width: 640px"><CdnHeaderList v-model="headers" /></div>
    <n-form-item :label="t('fw.cdn.ranges')" :feedback="t('fw.cdn.ranges_hint')" style="margin-top: 8px">
      <n-input v-model:value="ranges" type="textarea" :autosize="{ minRows: 3, maxRows: 10 }" placeholder="203.0.113.0/24&#10;2001:db8::/32" />
    </n-form-item>
    <n-button type="primary" size="small" :loading="saving" :disabled="!dirty" @click="save">{{ t('common.save') }}</n-button>
  </n-card>
  <TaskModal :task-id="task?.id ?? null" :title="task?.title ?? ''" @close="task = null" @done="task = null" />
</template>

<style scoped>
.lbl {
  margin: 10px 0 6px;
  font-size: 14px;
}
.hint {
  margin: 0;
  font-size: 12px;
  opacity: 0.65;
}
.ranges {
  font-size: 12px;
  word-break: break-all;
  opacity: 0.8;
}
</style>
