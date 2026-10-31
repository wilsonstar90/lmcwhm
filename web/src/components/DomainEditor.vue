<script setup lang="ts">
// One domain entry: what it serves, its aliases, SSL and DNS. Used by the
// domain page and by a website's drawer (the website's own entry).
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NCard, NCheckbox, NDescriptions, NDescriptionsItem, NDynamicTags, NForm, NFormItem, NInput, NModal, NPopconfirm,
  NRadioButton, NRadioGroup, NSpace, NSwitch, NTabPane, NTabs, useMessage,
} from 'naive-ui'
import { get, patch, post, ApiError } from '../api/client'
import type { Site } from '../api/types'
import { datetime } from '../format'
import TargetFields, { type TargetValue } from './TargetFields.vue'

const props = defineProps<{ siteId: number | null; hasCF: boolean; initialTab?: string }>()
const emit = defineEmits<{ close: []; task: [id: string, title: string]; changed: [] }>()
const { t, locale } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))

const site = ref<Site | null>(null)
const tab = ref('target')
const target = ref<TargetValue | null>(null)
const aliases = ref<string[]>([])
const remark = ref('')
const challenge = ref<'http' | 'dns'>('http')
const wildcard = ref(false)
const certPem = ref('')
const keyPem = ref('')

async function load() {
  if (!props.siteId) return
  try {
    const s = await get<Site>(`/sites/${props.siteId}`)
    site.value = s
    aliases.value = [...s.aliases]
    remark.value = s.remark
    challenge.value = s.ssl_challenge
    wildcard.value = s.ssl_wildcard
    const port = Number(s.proxy_target.match(/:(\d+)$/)?.[1] ?? 0) || null
    target.value = {
      target: s.target || (s.type === 'proxy' ? (s.passthrough ? 'passthrough' : 'url') : 'site'), target_site: s.target_site || null,
      container: s.container || null, port, proxy_target: s.proxy_target, redirect: s.redirect, redirect_code: s.redirect_code || 301,
      redirect_path: s.redirect_path,
    }
  } catch (e) {
    fail(e)
  }
}
watch(() => props.siteId, (id) => { site.value = null; tab.value = props.initialTab || 'target'; if (id) load() }, { immediate: true })

const run = async (p: Promise<{ task_id: string }>, title: string) => {
  try {
    const r = await p
    emit('task', r.task_id, `${title} · ${site.value!.domain}`)
    emit('changed')
  } catch (e) {
    fail(e)
  }
}
const update = (body: Record<string, unknown>, title: string) => run(patch<{ task_id: string }>(`/sites/${site.value!.id}`, body), title)
function saveTarget() {
  const v = target.value!
  run(post<{ task_id: string }>(`/sites/${site.value!.id}/target`, {
    target: v.target, target_site: v.target_site ?? 0, container: v.container ?? '', proxy_target: v.proxy_target,
    redirect: v.redirect, redirect_code: v.redirect_code, redirect_path: v.redirect_path,
  }), t('dom.target'))
}
const issue = () => run(post<{ task_id: string }>(`/sites/${site.value!.id}/ssl/issue`, { challenge: challenge.value, wildcard: wildcard.value }), t('site.ssl_issue'))
async function upload() {
  try {
    await post(`/sites/${site.value!.id}/ssl/upload`, { cert: certPem.value, key: keyPem.value })
    certPem.value = keyPem.value = ''
    msg.success(t('common.saved'))
    emit('changed')
    load()
  } catch (e) {
    fail(e)
  }
}
async function disableSSL() {
  try {
    await post(`/sites/${site.value!.id}/ssl/disable`)
    emit('changed')
    load()
  } catch (e) {
    fail(e)
  }
}
const passthrough = computed(() => target.value?.target === 'passthrough' || site.value?.passthrough)
const days = computed(() => (site.value?.cert_not_after ? Math.floor((site.value.cert_not_after * 1000 - Date.now()) / 86400000) : 0))
const behindCloudflare = computed(() => /2606:4700:|\b52[0-9]\b|cloudflare/i.test(site.value?.cert_error ?? ''))
const targetReady = computed(() => {
  const v = target.value
  if (!v) return false
  switch (v.target) {
    case 'link': return !!v.target_site
    case 'docker': return !!v.container && !!v.port
    case 'url':
    case 'passthrough': return !!v.proxy_target.trim()
    case 'redirect': return !!v.redirect.trim()
  }
  return true
})
</script>

<template>
  <n-modal :show="!!siteId" @update:show="(v: boolean) => !v && emit('close')">
    <n-card :title="site ? site.domain : '…'" style="width: min(720px, calc(100vw - 32px))" closable @close="emit('close')">
      <n-tabs v-if="site && target" v-model:value="tab" type="line">
        <n-tab-pane name="target" :tab="t('dom.target')">
          <n-form label-placement="top">
            <TargetFields v-model="target" :self-id="site.id" :has-content="site.type !== 'proxy'" />
          </n-form>
          <n-alert v-if="site.type !== 'proxy' && target.target !== 'site'" type="info" :show-icon="false" style="margin: 8px 0">{{ t('dom.retarget_note', { root: site.root }) }}</n-alert>
          <n-button type="primary" :disabled="!targetReady" style="margin-top: 8px" @click="saveTarget">{{ t('common.save') }}</n-button>
        </n-tab-pane>

        <n-tab-pane name="domains" :tab="t('site.tab_domains')">
          <n-form label-placement="top">
            <n-form-item :label="t('site.aliases')" :feedback="t('dom.aliases_hint')">
              <n-dynamic-tags v-model:value="aliases" />
            </n-form-item>
            <n-form-item :label="t('site.remark')"><n-input v-model:value="remark" /></n-form-item>
            <n-button type="primary" @click="update({ aliases, remark }, t('site.tab_domains'))">{{ t('common.save') }}</n-button>
          </n-form>
          <n-descriptions :column="1" label-placement="left" size="small" style="margin-top: 20px">
            <n-descriptions-item v-if="site.type !== 'proxy'" :label="t('site.root')"><code>{{ site.root }}</code></n-descriptions-item>
            <n-descriptions-item :label="t('site.created')">{{ datetime(site.created_at, locale) }}</n-descriptions-item>
          </n-descriptions>
          <n-space align="center" style="margin-top: 12px">
            <n-switch :value="!site.stopped" @update:value="(v: boolean) => update({ stopped: !v }, v ? t('site.start') : t('site.stop'))" />
            <span>{{ site.stopped ? t('site.stopped') : t('site.running') }}</span>
          </n-space>
        </n-tab-pane>

        <n-tab-pane name="ssl" tab="SSL">
          <n-alert v-if="passthrough" type="info" :show-icon="false">{{ t('dom.ssl_passthrough') }}</n-alert>
          <template v-else>
            <n-alert v-if="site.cert_error" type="error" :title="t('site.ssl_failed')" style="margin-bottom: 16px">{{ site.cert_error }}</n-alert>
            <n-alert v-if="behindCloudflare" type="warning" :title="t('site.cf_hint_title')" style="margin-bottom: 16px">
              {{ hasCF ? t('site.cf_hint_dns') : t('site.cf_hint_token') }}
            </n-alert>
            <n-descriptions v-if="site.ssl_mode !== 'none'" :column="1" label-placement="left" size="small" style="margin-bottom: 16px">
              <n-descriptions-item :label="t('site.ssl_source')">{{ site.ssl_mode === 'acme' ? 'ACME' : t('site.ssl_custom') }}</n-descriptions-item>
              <n-descriptions-item :label="t('site.ssl_issuer')">{{ site.cert_issuer }}</n-descriptions-item>
              <n-descriptions-item :label="t('site.ssl_expires')">{{ datetime(site.cert_not_after, locale) }} ({{ t('site.days_left', { n: days }) }})</n-descriptions-item>
            </n-descriptions>
            <n-space vertical :size="12">
              <n-space align="center">
                <n-switch :value="site.force_https" :disabled="site.ssl_mode === 'none'" @update:value="(v: boolean) => update({ force_https: v }, t('site.force_https'))" />
                <span>{{ t('site.force_https') }}</span>
                <n-switch :value="site.hsts" :disabled="site.ssl_mode === 'none'" style="margin-left: 16px" @update:value="(v: boolean) => update({ hsts: v }, 'HSTS')" />
                <span>HSTS</span>
              </n-space>
              <n-form label-placement="top">
                <n-form-item :label="t('site.ssl_issue')">
                  <n-space vertical>
                    <n-radio-group v-model:value="challenge">
                      <n-radio-button value="http">HTTP-01</n-radio-button>
                      <n-radio-button value="dns" :disabled="!hasCF">DNS-01 (Cloudflare)</n-radio-button>
                    </n-radio-group>
                    <n-checkbox v-if="challenge === 'dns'" v-model:checked="wildcard">{{ t('site.wildcard') }}</n-checkbox>
                    <n-button type="primary" @click="issue">{{ site.ssl_mode === 'acme' ? t('site.ssl_renew') : t('site.ssl_issue') }}</n-button>
                  </n-space>
                </n-form-item>
                <n-form-item :label="t('site.ssl_upload')">
                  <n-space vertical style="width: 100%">
                    <n-input v-model:value="certPem" type="textarea" :rows="4" placeholder="-----BEGIN CERTIFICATE-----" />
                    <n-input v-model:value="keyPem" type="textarea" :rows="4" placeholder="-----BEGIN PRIVATE KEY-----" />
                    <n-button :disabled="!certPem || !keyPem" @click="upload">{{ t('site.ssl_upload') }}</n-button>
                  </n-space>
                </n-form-item>
              </n-form>
              <n-popconfirm v-if="site.ssl_mode !== 'none'" @positive-click="disableSSL">
                <template #trigger><n-button type="error" ghost>{{ t('site.ssl_disable') }}</n-button></template>
                {{ t('site.ssl_disable_confirm') }}
              </n-popconfirm>
            </n-space>
          </template>
        </n-tab-pane>

        <n-tab-pane name="dns" tab="DNS">
          <p class="hint">{{ t('dom.dns_hint') }}</p>
          <n-space vertical :size="12">
            <n-space align="center">
              <n-switch :value="site.dns_managed" :disabled="!hasCF" @update:value="(v: boolean) => update({ dns_managed: v }, 'DNS')" />
              <span>{{ t('dom.dns_managed') }}</span>
            </n-space>
            <n-space align="center">
              <n-switch :value="site.dns_proxied" :disabled="!site.dns_managed" @update:value="(v: boolean) => update({ dns_proxied: v }, 'DNS')" />
              <span>{{ t('site.cf_proxied') }}</span>
            </n-space>
            <n-alert v-if="!hasCF" type="warning" :show-icon="false">{{ t('site.cf_hint_token') }}</n-alert>
          </n-space>
        </n-tab-pane>
      </n-tabs>
    </n-card>
  </n-modal>
</template>

<style scoped>
.hint {
  font-size: 12px;
  opacity: 0.7;
  margin: 0 0 12px;
}
</style>
