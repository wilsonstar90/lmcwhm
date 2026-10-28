<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NCard, NForm, NFormItem, NInput, NSelect, NButton, NSpace, NTag, NAlert, NText, NGrid, NGi, useMessage,
} from 'naive-ui'
import { get, post, put, ApiError } from '../api/client'
import type { WebConfig, WebStatus } from '../api/types'
import TaskModal from './TaskModal.vue'
import VarnishCard from './VarnishCard.vue'

const emit = defineEmits<{ changed: [] }>()
const { t } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))

const status = ref<WebStatus | null>(null)
const cfg = ref<WebConfig | null>(null)
const cfToken = ref('')
const eabHmac = ref('')
const detected = ref<{ ipv4: string; ipv6: string } | null>(null)
const saving = ref(false)
const taskId = ref<string | null>(null)

async function load() {
  ;[status.value, cfg.value] = await Promise.all([get<WebStatus>('/web/status'), get<WebConfig>('/web/config')])
}
onMounted(() => {
  load()
  get<{ ipv4: string; ipv6: string }>('/web/ips').then((r) => (detected.value = r)).catch(() => {})
})

async function save(extra: Record<string, unknown> = {}) {
  if (!cfg.value) return
  saving.value = true
  try {
    cfg.value = await put<WebConfig>('/web/config', { ...cfg.value, cf_token: cfToken.value, eab_hmac: eabHmac.value, ...extra })
    cfToken.value = eabHmac.value = ''
    msg.success(t('common.saved'))
    emit('changed')
  } catch (e) {
    fail(e)
  } finally {
    saving.value = false
  }
}

async function verifyCF() {
  const r = await post<{ ok: boolean; error?: string }>('/web/cf/verify', { token: cfToken.value })
  r.ok ? msg.success(t('web.cf_ok')) : msg.error(r.error ?? 'error')
}

async function install() {
  try {
    taskId.value = (await post<{ task_id: string }>('/web/install')).task_id
  } catch (e) {
    fail(e)
  }
}

const caOptions = [
  { label: "Let's Encrypt", value: 'letsencrypt' },
  { label: "Let's Encrypt (staging / test)", value: 'letsencrypt-staging' },
  { label: 'LiteSSL', value: 'litessl' },
  { label: 'Custom ACME', value: 'custom' },
]
const stateType = (s: string) => (s === 'running' ? 'success' : s ? 'warning' : 'default')
</script>

<template>
  <n-space vertical :size="16">
    <n-card size="small" :title="t('web.stack')">
      <template #header-extra>
        <n-button size="small" type="primary" :disabled="!status?.status.docker" @click="install">
          {{ status?.status.installed ? t('web.repair') : t('web.install') }}
        </n-button>
      </template>
      <n-alert v-if="status && !status.status.docker" type="error" :show-icon="false">{{ t('web.no_docker') }}</n-alert>
      <n-space v-else-if="status" :size="8">
        <n-tag v-for="s in status.status.services ?? []" :key="s.name" :type="stateType(s.state)" size="small">
          {{ s.name }} · {{ s.state || t('web.missing') }}
        </n-tag>
        <n-tag v-for="p in status.status.php ?? []" :key="p.name" :type="stateType(p.state)" size="small">
          PHP {{ p.name }} · {{ p.state }}
        </n-tag>
      </n-space>
      <p class="hint">{{ t('web.stack_hint') }}</p>
    </n-card>

    <VarnishCard v-if="status?.status.installed" />

    <n-grid v-if="cfg" cols="1 l:2" responsive="screen" :x-gap="16" :y-gap="16">
      <n-gi>
        <n-card size="small" title="Cloudflare">
          <n-form label-placement="top">
            <n-form-item :label="t('web.cf_token')" :feedback="t('web.cf_token_hint')">
              <n-input
                v-model:value="cfToken"
                type="password"
                show-password-on="click"
                :placeholder="cfg.has_cf_token ? t('web.secret_set') : ''"
              />
            </n-form-item>
            <n-space>
              <n-button :loading="saving" type="primary" @click="save()">{{ t('common.save') }}</n-button>
              <n-button :disabled="!cfToken && !cfg.has_cf_token" @click="verifyCF">{{ t('web.verify') }}</n-button>
              <n-button v-if="cfg.has_cf_token" quaternary type="error" @click="save({ clear_cf_token: true })">{{ t('web.clear') }}</n-button>
            </n-space>
          </n-form>
        </n-card>
        <n-card size="small" :title="t('web.server_ip')" style="margin-top: 16px">
          <n-form label-placement="top">
            <n-form-item label="IPv4" :feedback="t('web.auto', { ip: detected?.ipv4 || '—' })">
              <n-input v-model:value="cfg.server_ipv4" :placeholder="detected?.ipv4" />
            </n-form-item>
            <n-form-item label="IPv6" :feedback="t('web.auto', { ip: detected?.ipv6 || '—' })">
              <n-input v-model:value="cfg.server_ipv6" :placeholder="detected?.ipv6" />
            </n-form-item>
            <n-button :loading="saving" type="primary" @click="save()">{{ t('common.save') }}</n-button>
          </n-form>
        </n-card>
      </n-gi>
      <n-gi>
        <n-card size="small" :title="t('web.acme')">
          <n-form label-placement="top">
            <n-form-item :label="t('web.acme_email')">
              <n-input v-model:value="cfg.acme_email" placeholder="admin@example.com" />
            </n-form-item>
            <n-form-item :label="t('web.acme_ca')">
              <n-select v-model:value="cfg.acme_ca" :options="caOptions" />
            </n-form-item>
            <template v-if="cfg.acme_ca === 'litessl' || cfg.acme_ca === 'custom'">
              <n-form-item :label="t('web.acme_directory')" :feedback="t('web.acme_directory_hint')">
                <n-input v-model:value="cfg.acme_directory" placeholder="https://…/directory" />
              </n-form-item>
              <n-form-item label="EAB Key ID">
                <n-input v-model:value="cfg.eab_kid" />
              </n-form-item>
              <n-form-item label="EAB HMAC Key">
                <n-input v-model:value="eabHmac" type="password" show-password-on="click" :placeholder="cfg.has_eab_hmac ? t('web.secret_set') : ''" />
              </n-form-item>
            </template>
            <n-text depth="3" style="display: block; margin-bottom: 12px; font-size: 12px">{{ t('web.tos') }}</n-text>
            <n-button :loading="saving" type="primary" @click="save()">{{ t('common.save') }}</n-button>
          </n-form>
        </n-card>
      </n-gi>
    </n-grid>
  </n-space>
  <TaskModal :task-id="taskId" :title="t('web.install')" @close="taskId = null" @done="load" />
</template>

<style scoped>
.hint {
  margin: 12px 0 0;
  font-size: 12px;
  opacity: 0.7;
}
</style>
