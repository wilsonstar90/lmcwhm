<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NModal, NCard, NForm, NFormItem, NInput, NSelect, NSwitch, NRadioGroup, NRadioButton, NButton, NSpace, NCheckbox, useMessage,
} from 'naive-ui'
import { post, ApiError } from '../api/client'

const props = defineProps<{ show: boolean; phpVersions: string[]; hasCF: boolean }>()
const emit = defineEmits<{ close: []; created: [taskId: string, domain: string] }>()
const { t } = useI18n()
const msg = useMessage()

const blank = () => ({
  domain: '',
  aliases: '',
  type: 'php' as 'php' | 'static',
  php_version: '8.3',
  dns: false,
  dns_proxied: false,
  ssl: 'http' as 'none' | 'http' | 'dns',
  wildcard: false,
  force_https: true,
  remark: '',
})
const f = ref(blank())
const busy = ref(false)
const phpOptions = computed(() =>
  props.phpVersions.map((v) => ({ label: `PHP ${v}${['7.4', '8.0', '8.1'].includes(v) ? ' (EOL)' : ''}`, value: v })),
)

async function submit() {
  busy.value = true
  try {
    const r = await post<{ task_id: string }>('/sites', {
      ...f.value,
      aliases: f.value.aliases.split(/[\s,]+/).filter(Boolean),
      wildcard: f.value.ssl === 'dns' && f.value.wildcard,
    })
    emit('created', r.task_id, f.value.domain)
    f.value = blank()
  } catch (e) {
    msg.error(e instanceof ApiError ? e.text : String(e))
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <n-modal :show="show" @update:show="(v: boolean) => !v && emit('close')">
    <n-card :title="t('site.create')" style="width: min(560px, calc(100vw - 32px))" closable @close="emit('close')">
      <n-form label-placement="top">
        <n-form-item :label="t('site.domain')">
          <n-input v-model:value="f.domain" placeholder="example.com" />
        </n-form-item>
        <n-form-item :label="t('site.aliases')" :feedback="t('site.aliases_hint')">
          <n-input v-model:value="f.aliases" type="textarea" :rows="2" placeholder="www.example.com" />
        </n-form-item>
        <n-space>
          <n-form-item :label="t('site.type')">
            <n-radio-group v-model:value="f.type">
              <n-radio-button value="php">PHP</n-radio-button>
              <n-radio-button value="static">{{ t('site.static') }}</n-radio-button>
            </n-radio-group>
          </n-form-item>
          <n-form-item v-if="f.type === 'php'" :label="t('site.php_version')">
            <n-select v-model:value="f.php_version" :options="phpOptions" style="width: 160px" />
          </n-form-item>
        </n-space>
        <n-form-item :label="t('site.dns')" :feedback="hasCF ? t('site.dns_hint') : t('site.need_cf')">
          <n-space align="center">
            <n-switch v-model:value="f.dns" :disabled="!hasCF" />
            <n-checkbox v-if="f.dns" v-model:checked="f.dns_proxied">{{ t('site.cf_proxied') }}</n-checkbox>
          </n-space>
        </n-form-item>
        <n-form-item label="SSL" :feedback="f.ssl === 'http' ? t('site.ssl_http_hint') : f.ssl === 'dns' ? t('site.ssl_dns_hint') : ''">
          <n-radio-group v-model:value="f.ssl">
            <n-radio-button value="none">{{ t('site.ssl_none') }}</n-radio-button>
            <n-radio-button value="http">HTTP-01</n-radio-button>
            <n-radio-button value="dns" :disabled="!hasCF">DNS-01 (Cloudflare)</n-radio-button>
          </n-radio-group>
        </n-form-item>
        <n-space v-if="f.ssl !== 'none'">
          <n-checkbox v-model:checked="f.force_https">{{ t('site.force_https') }}</n-checkbox>
          <n-checkbox v-if="f.ssl === 'dns'" v-model:checked="f.wildcard">{{ t('site.wildcard') }}</n-checkbox>
        </n-space>
        <n-form-item :label="t('site.remark')" style="margin-top: 12px">
          <n-input v-model:value="f.remark" />
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="emit('close')">{{ t('common.cancel') }}</n-button>
          <n-button type="primary" :loading="busy" :disabled="!f.domain" @click="submit">{{ t('common.confirm') }}</n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>
</template>
