<script setup lang="ts">
// A new domain entry without website files of its own: it shows another
// website, a container, a URL, a passthrough backend or a redirect.
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NCard, NCheckbox, NDynamicTags, NForm, NFormItem, NInput, NModal, NRadioButton, NRadioGroup, NSpace, useMessage } from 'naive-ui'
import { post, ApiError } from '../api/client'
import TargetFields, { type TargetValue } from './TargetFields.vue'

export type DomainPreset = Partial<TargetValue> & { domain?: string; remark?: string }

const props = defineProps<{ show: boolean; hasCF: boolean; preset?: DomainPreset }>()
const emit = defineEmits<{ close: []; created: [taskId: string, domain: string] }>()
const { t } = useI18n()
const msg = useMessage()

const blank = (): TargetValue => ({ target: 'link', target_site: null, container: null, port: null, proxy_target: '', redirect: '', redirect_code: 301, redirect_path: true })
const domain = ref('')
const aliases = ref<string[]>([])
const remark = ref('')
const target = ref<TargetValue>(blank())
const ssl = ref<'none' | 'http' | 'dns'>('http')
const forceHTTPS = ref(true)
const dns = ref(false)
const proxied = ref(false)
const saving = ref(false)

watch(
  () => props.show,
  (v) => {
    if (!v) return
    const p = props.preset ?? {}
    domain.value = p.domain ?? ''
    remark.value = p.remark ?? ''
    aliases.value = []
    target.value = { ...blank(), ...p }
    ssl.value = 'http'
    forceHTTPS.value = true
    dns.value = false
    proxied.value = false
  },
)
watch(() => target.value.target, (k) => k === 'passthrough' && (ssl.value = 'none'))

const ready = computed(() => {
  const v = target.value
  if (!domain.value.trim()) return false
  switch (v.target) {
    case 'link': return !!v.target_site
    case 'docker': return !!v.container && !!v.port
    case 'redirect': return !!v.redirect.trim()
  }
  return !!v.proxy_target.trim()
})

async function submit() {
  const v = target.value
  saving.value = true
  try {
    const r = await post<{ task_id: string }>('/sites', {
      domain: domain.value.trim().toLowerCase(), aliases: aliases.value, type: 'proxy', remark: remark.value,
      target: v.target, target_site: v.target_site ?? 0, container: v.container ?? '', proxy_target: v.proxy_target,
      passthrough: v.target === 'passthrough', redirect: v.redirect, redirect_code: v.redirect_code, redirect_path: v.redirect_path,
      ssl: ssl.value, force_https: ssl.value !== 'none' && forceHTTPS.value, dns: dns.value, dns_proxied: dns.value && proxied.value,
    })
    emit('created', r.task_id, domain.value)
  } catch (e) {
    msg.error(e instanceof ApiError ? e.text : String(e))
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <n-modal :show="show" @update:show="(v: boolean) => !v && emit('close')">
    <n-card :title="t('route.add')" style="width: min(640px, calc(100vw - 32px))" closable @close="emit('close')">
      <n-form label-placement="top">
        <n-form-item :label="t('route.host')" :feedback="t('route.host_hint')">
          <n-input v-model:value="domain" placeholder="app.example.com" />
        </n-form-item>
        <n-form-item :label="t('site.aliases')"><n-dynamic-tags v-model:value="aliases" /></n-form-item>
        <TargetFields v-model="target" />
        <n-form-item v-if="target.target !== 'passthrough'" label="SSL">
          <n-space vertical>
            <n-radio-group v-model:value="ssl">
              <n-radio-button value="none">{{ t('site.ssl_none') }}</n-radio-button>
              <n-radio-button value="http">HTTP-01</n-radio-button>
              <n-radio-button value="dns" :disabled="!hasCF">DNS-01 (Cloudflare)</n-radio-button>
            </n-radio-group>
            <n-checkbox v-if="ssl !== 'none'" v-model:checked="forceHTTPS">{{ t('site.force_https') }}</n-checkbox>
          </n-space>
        </n-form-item>
        <n-form-item label="DNS">
          <n-space vertical>
            <n-checkbox v-model:checked="dns" :disabled="!hasCF">{{ t('dom.dns_managed') }}</n-checkbox>
            <n-checkbox v-if="dns" v-model:checked="proxied">{{ t('site.cf_proxied') }}</n-checkbox>
          </n-space>
        </n-form-item>
        <n-form-item :label="t('site.remark')"><n-input v-model:value="remark" /></n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="emit('close')">{{ t('common.cancel') }}</n-button>
          <n-button type="primary" :loading="saving" :disabled="!ready" @click="submit">{{ t('common.confirm') }}</n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>
</template>
