<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NCard, NForm, NFormItem, NInput, NSelect, NSpace, NTag, useMessage } from 'naive-ui'
import { get, post, put, ApiError } from '../api/client'
import { fwConfig } from '../api/firewall'
import type { FirewallView, WebConfig } from '../api/types'

// Every third-party API credential in one place: Cloudflare (DNS, DNS-01
// certificates, firewall sync), Linode (Cloud Firewall) and Anthropic (AI).
const { t } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))

const web = ref<WebConfig | null>(null)
const fw = ref<FirewallView | null>(null)
interface AIConfig { model: string; effort: string; base_url: string; has_key: boolean; default_model: string }
const ai = ref<AIConfig | null>(null)
const cf = ref('')
const linode = ref('')
const key = ref('')
const busy = ref('')

async function load() {
  try {
    ;[web.value, fw.value, ai.value] = await Promise.all([get<WebConfig>('/web/config'), get<FirewallView>('/firewall'), get<AIConfig>('/ai/config')])
  } catch (e) {
    fail(e)
  }
}
onMounted(load)

async function run(name: string, fn: () => Promise<void>) {
  busy.value = name
  try {
    await fn()
  } catch (e) {
    fail(e)
  } finally {
    busy.value = ''
  }
}

// ---- Cloudflare ----
const saveCF = (clear = false) =>
  run('cf', async () => {
    web.value = await put<WebConfig>('/web/config', { ...web.value, cf_token: clear ? '' : cf.value, clear_cf_token: clear })
    cf.value = ''
    msg.success(t('common.saved'))
  })
const verifyCF = () =>
  run('cf-verify', async () => {
    const r = await post<{ ok: boolean; error?: string }>('/web/cf/verify', { token: cf.value })
    r.ok ? msg.success(t('web.cf_ok')) : msg.error(r.error ?? 'error')
  })

// ---- Linode ----
async function putFirewall(patch: Record<string, unknown>) {
  await put('/firewall', { ...fwConfig(fw.value!), ...patch })
  fw.value = await get<FirewallView>('/firewall')
}
const saveLinode = (clear = false) =>
  run('linode', async () => {
    if (!clear) {
      const r = await post<{ ok: boolean; error?: string }>('/firewall/linode/verify', { token: linode.value })
      if (!r.ok) {
        msg.error(r.error ?? 'error')
        return
      }
    }
    await putFirewall(clear ? { clear_linode_token: true } : { linode_token: linode.value })
    linode.value = ''
    msg.success(t('common.saved'))
  })
const verifyLinode = () =>
  run('linode-verify', async () => {
    const r = await post<{ ok: boolean; error?: string; linode?: { label: string }; linode_error?: string }>('/firewall/linode/verify', { token: linode.value })
    if (!r.ok) msg.error(r.error ?? 'error')
    else msg.success(r.linode ? t('apis.linode_ok', { name: r.linode.label }) : r.linode_error || t('web.cf_ok'))
  })

// ---- Anthropic ----
const saveAI = (clear = false) =>
  run('ai', async () => {
    await put('/ai/config', { api_key: key.value, model: ai.value!.model, effort: ai.value!.effort, base_url: ai.value!.base_url, clear_key: clear })
    key.value = ''
    ai.value = await get<AIConfig>('/ai/config')
    msg.success(t('common.saved'))
  })
const models = ['claude-opus-5-5', 'claude-sonnet-5-5', 'claude-haiku-5-5', 'claude-fable-5-1'].map((m) => ({ label: m, value: m }))
const efforts = ['low', 'medium', 'high', 'xhigh', 'max'].map((v) => ({ label: v, value: v }))
const state = (on: boolean | undefined) => (on ? { type: 'success' as const, text: t('apis.set') } : { type: 'default' as const, text: t('apis.not_set') })
</script>

<template>
  <p class="hint" style="margin: 0 0 12px">{{ t('apis.intro') }}</p>
  <n-space vertical :size="16" style="max-width: 760px">
    <n-card size="small" title="Cloudflare">
      <template #header-extra><n-tag size="small" :type="state(web?.has_cf_token).type">{{ state(web?.has_cf_token).text }}</n-tag></template>
      <p class="hint" style="margin-top: 0">{{ t('apis.cf_used') }}</p>
      <n-form label-placement="top">
        <n-form-item :label="t('web.cf_token')" :feedback="t('web.cf_token_hint')">
          <n-input v-model:value="cf" type="password" show-password-on="click" :placeholder="web?.has_cf_token ? t('web.secret_set') : ''" />
        </n-form-item>
      </n-form>
      <n-space>
        <n-button type="primary" :disabled="!cf" :loading="busy === 'cf'" @click="saveCF()">{{ t('common.save') }}</n-button>
        <n-button :disabled="!cf && !web?.has_cf_token" :loading="busy === 'cf-verify'" @click="verifyCF">{{ t('web.verify') }}</n-button>
        <n-button v-if="web?.has_cf_token" quaternary type="error" @click="saveCF(true)">{{ t('web.clear') }}</n-button>
      </n-space>
    </n-card>

    <n-card size="small" title="Linode">
      <template #header-extra><n-tag size="small" :type="state(fw?.has_linode_token).type">{{ state(fw?.has_linode_token).text }}</n-tag></template>
      <p class="hint" style="margin-top: 0">{{ t('apis.linode_used') }}</p>
      <n-form label-placement="top">
        <n-form-item :label="t('fw.linode_token')" :feedback="t('apis.linode_hint')">
          <n-input v-model:value="linode" type="password" show-password-on="click" :placeholder="fw?.has_linode_token ? t('web.secret_set') : ''" />
        </n-form-item>
      </n-form>
      <n-space>
        <n-button type="primary" :disabled="!linode" :loading="busy === 'linode'" @click="saveLinode()">{{ t('common.save') }}</n-button>
        <n-button :disabled="!linode && !fw?.has_linode_token" :loading="busy === 'linode-verify'" @click="verifyLinode">{{ t('web.verify') }}</n-button>
        <n-button v-if="fw?.has_linode_token" quaternary type="error" @click="saveLinode(true)">{{ t('web.clear') }}</n-button>
      </n-space>
    </n-card>

    <n-card v-if="ai" size="small" title="Anthropic (Claude)">
      <template #header-extra><n-tag size="small" :type="state(ai.has_key).type">{{ state(ai.has_key).text }}</n-tag></template>
      <p class="hint" style="margin-top: 0">{{ t('apis.ai_used') }}</p>
      <n-form label-placement="top">
        <n-form-item :label="t('ai.api_key')" :feedback="t('ai.api_key_hint')">
          <n-input v-model:value="key" type="password" show-password-on="click" :placeholder="ai.has_key ? t('web.secret_set') : 'sk-ant-…'" />
        </n-form-item>
        <n-form-item :label="t('ai.model')" :feedback="t('ai.model_hint', { model: ai.default_model })">
          <n-select v-model:value="ai.model" filterable tag :options="models" />
        </n-form-item>
        <n-form-item :label="t('ai.effort')" :feedback="t('ai.effort_hint')">
          <n-select v-model:value="ai.effort" :options="efforts" style="width: 160px" />
        </n-form-item>
        <n-form-item :label="t('ai.base_url')" :feedback="t('ai.base_url_hint')"><n-input v-model:value="ai.base_url" placeholder="https://api.anthropic.com" /></n-form-item>
      </n-form>
      <n-space>
        <n-button type="primary" :loading="busy === 'ai'" @click="saveAI()">{{ t('common.save') }}</n-button>
        <n-button v-if="ai.has_key" quaternary type="error" @click="saveAI(true)">{{ t('web.clear') }}</n-button>
      </n-space>
    </n-card>
  </n-space>
</template>

<style scoped>
.hint {
  font-size: 12px;
  opacity: 0.65;
  margin: 8px 0 0;
}
</style>
