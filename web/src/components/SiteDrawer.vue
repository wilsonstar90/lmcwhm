<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NDrawer, NDrawerContent, NTabs, NTabPane, NForm, NFormItem, NInput, NSelect, NButton, NSpace, NSwitch, NAlert,
  NDescriptions, NDescriptionsItem, NRadioGroup, NRadioButton, NCheckbox, NCode, NPopconfirm, useMessage,
} from 'naive-ui'
import { get, patch, post, ApiError } from '../api/client'
import type { Site } from '../api/types'
import { datetime } from '../format'

const props = defineProps<{ site: Site | null; phpVersions: string[]; hasCF: boolean }>()
const emit = defineEmits<{ close: []; task: [id: string, title: string]; changed: [] }>()
const { t, locale } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))

const aliases = ref('')
const php = ref('')
const remark = ref('')
const challenge = ref<'http' | 'dns'>('http')
const wildcard = ref(false)
const certPem = ref('')
const keyPem = ref('')
const vhost = ref('')
const delFiles = ref(false)
const delDNS = ref(true)

watch(
  () => props.site,
  (s) => {
    if (!s) return
    aliases.value = s.aliases.join('\n')
    php.value = s.php_version
    remark.value = s.remark
    challenge.value = s.ssl_challenge
    wildcard.value = s.ssl_wildcard
    vhost.value = ''
    get<{ config: string }>(`/sites/${s.id}/vhost`).then((r) => (vhost.value = r.config)).catch(() => {})
  },
  { immediate: true },
)

async function update(body: Record<string, unknown>, title: string) {
  try {
    const r = await patch<{ task_id: string }>(`/sites/${props.site!.id}`, body)
    emit('task', r.task_id, title)
  } catch (e) {
    fail(e)
  }
}

async function issue() {
  try {
    const r = await post<{ task_id: string }>(`/sites/${props.site!.id}/ssl/issue`, { challenge: challenge.value, wildcard: wildcard.value })
    emit('task', r.task_id, t('site.ssl_issue'))
  } catch (e) {
    fail(e)
  }
}
async function upload() {
  try {
    await post(`/sites/${props.site!.id}/ssl/upload`, { cert: certPem.value, key: keyPem.value })
    certPem.value = keyPem.value = ''
    msg.success(t('common.saved'))
    emit('changed')
  } catch (e) {
    fail(e)
  }
}
async function disableSSL() {
  try {
    await post(`/sites/${props.site!.id}/ssl/disable`)
    emit('changed')
  } catch (e) {
    fail(e)
  }
}
async function remove() {
  try {
    const r = await post<{ task_id: string }>(`/sites/${props.site!.id}/delete`, { files: delFiles.value, dns: delDNS.value })
    emit('task', r.task_id, t('site.delete'))
    emit('close')
  } catch (e) {
    fail(e)
  }
}

const phpOptions = computed(() => props.phpVersions.map((v) => ({ label: `PHP ${v}`, value: v })))
const days = computed(() =>
  props.site?.cert_not_after ? Math.floor((props.site.cert_not_after * 1000 - Date.now()) / 86400000) : 0,
)
</script>

<template>
  <n-drawer :show="!!site" :width="'min(640px, 100vw)'" @update:show="(v: boolean) => !v && emit('close')">
    <n-drawer-content v-if="site" :title="site.domain" closable>
      <n-tabs type="line" animated>
        <n-tab-pane name="domains" :tab="t('site.tab_domains')">
          <n-form label-placement="top">
            <n-form-item :label="t('site.aliases')" :feedback="t('site.aliases_hint')">
              <n-input v-model:value="aliases" type="textarea" :rows="4" />
            </n-form-item>
            <n-form-item :label="t('site.remark')">
              <n-input v-model:value="remark" />
            </n-form-item>
            <n-button
              type="primary"
              @click="update({ aliases: aliases.split(/[\s,]+/).filter(Boolean), remark }, t('site.tab_domains'))"
            >{{ t('common.save') }}</n-button>
          </n-form>
          <n-descriptions :column="1" label-placement="left" size="small" style="margin-top: 24px">
            <n-descriptions-item :label="t('site.root')"><code>{{ site.root }}</code></n-descriptions-item>
            <n-descriptions-item :label="t('site.dns')">
              {{ site.dns_managed ? 'Cloudflare' + (site.dns_proxied ? ' (proxied)' : '') : '—' }}
            </n-descriptions-item>
            <n-descriptions-item :label="t('site.created')">{{ datetime(site.created_at, locale) }}</n-descriptions-item>
          </n-descriptions>
          <n-space v-if="site.dns_managed" style="margin-top: 12px">
            <n-switch :value="site.dns_proxied" @update:value="(v: boolean) => update({ dns_proxied: v }, 'DNS')" />
            <span>{{ t('site.cf_proxied') }}</span>
          </n-space>
        </n-tab-pane>

        <n-tab-pane name="ssl" tab="SSL">
          <n-alert v-if="site.cert_error" type="error" :title="t('site.ssl_failed')" style="margin-bottom: 16px">
            {{ site.cert_error }}
          </n-alert>
          <n-descriptions v-if="site.ssl_mode !== 'none'" :column="1" label-placement="left" size="small" style="margin-bottom: 16px">
            <n-descriptions-item :label="t('site.ssl_source')">{{ site.ssl_mode === 'acme' ? 'ACME' : t('site.ssl_custom') }}</n-descriptions-item>
            <n-descriptions-item :label="t('site.ssl_issuer')">{{ site.cert_issuer }}</n-descriptions-item>
            <n-descriptions-item :label="t('site.ssl_expires')">
              {{ datetime(site.cert_not_after, locale) }} ({{ t('site.days_left', { n: days }) }})
            </n-descriptions-item>
          </n-descriptions>
          <n-space vertical :size="12">
            <n-space align="center">
              <n-switch
                :value="site.force_https"
                :disabled="site.ssl_mode === 'none'"
                @update:value="(v: boolean) => update({ force_https: v }, t('site.force_https'))"
              />
              <span>{{ t('site.force_https') }}</span>
              <n-switch
                :value="site.hsts"
                :disabled="site.ssl_mode === 'none'"
                style="margin-left: 16px"
                @update:value="(v: boolean) => update({ hsts: v }, 'HSTS')"
              />
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
        </n-tab-pane>

        <n-tab-pane v-if="site.type === 'php'" name="php" tab="PHP">
          <n-form label-placement="top">
            <n-form-item :label="t('site.php_version')" :feedback="t('site.php_switch_hint')">
              <n-select v-model:value="php" :options="phpOptions" style="width: 200px" />
            </n-form-item>
            <n-button type="primary" :disabled="php === site.php_version" @click="update({ php_version: php }, 'PHP ' + php)">
              {{ t('common.save') }}
            </n-button>
          </n-form>
        </n-tab-pane>

        <n-tab-pane name="config" :tab="t('site.tab_config')">
          <p class="hint">{{ t('site.config_hint') }}</p>
          <n-code :code="vhost" language="nginx" word-wrap />
        </n-tab-pane>

        <n-tab-pane name="danger" :tab="t('site.tab_danger')">
          <n-space vertical>
            <n-space align="center">
              <n-switch :value="!site.stopped" @update:value="(v: boolean) => update({ stopped: !v }, v ? t('site.start') : t('site.stop'))" />
              <span>{{ site.stopped ? t('site.stopped') : t('site.running') }}</span>
            </n-space>
            <n-checkbox v-model:checked="delFiles">{{ t('site.delete_files', { root: site.root }) }}</n-checkbox>
            <n-checkbox v-if="site.dns_managed" v-model:checked="delDNS">{{ t('site.delete_dns') }}</n-checkbox>
            <n-popconfirm @positive-click="remove">
              <template #trigger><n-button type="error">{{ t('site.delete') }}</n-button></template>
              {{ t('site.delete_confirm', { domain: site.domain }) }}
            </n-popconfirm>
          </n-space>
        </n-tab-pane>
      </n-tabs>
    </n-drawer-content>
  </n-drawer>
</template>

<style scoped>
.hint {
  margin: 0 0 8px;
  font-size: 12px;
  opacity: 0.7;
}
</style>
