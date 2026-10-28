<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NCheckbox, NDivider, NDynamicInput, NDynamicTags, NForm, NFormItem, NInput, NInputNumber, NSelect, NSpace, useMessage } from 'naive-ui'
import { patch, post, ApiError } from '../api/client'
import type { Site, SiteAdvanced } from '../api/types'

const props = defineProps<{ site: Site }>()
const emit = defineEmits<{ task: [id: string, title: string] }>()
const { t } = useI18n()
const msg = useMessage()

const blank = (): SiteAdvanced => ({
  autoindex: false, allow_ips: [], deny_ips: [], deny_ua: [], rate_limit: 0, rate_burst: 0, conn_limit: 0, error_pages: false,
  block_xmlrpc: false, open_basedir: false, hotlink: [], max_body_mb: 0, redirects: [], varnish: false, varnish_ttl: 120, varnish_exclude: [],
})
const a = ref<SiteAdvanced>(blank())
watch(
  () => props.site,
  (s) => (a.value = { ...blank(), ...JSON.parse(JSON.stringify(s.advanced ?? {})) }),
  { immediate: true },
)
async function save() {
  try {
    const r = await patch<{ task_id: string }>(`/sites/${props.site.id}`, { advanced: a.value })
    emit('task', r.task_id, `${t('adv.title')} · ${props.site.domain}`)
  } catch (e) {
    msg.error(e instanceof ApiError ? e.text : String(e))
  }
}
async function purge() {
  try {
    await post('/varnish/purge', { site_id: props.site.id })
    msg.success(t('common.done'))
  } catch (e) {
    msg.error(e instanceof ApiError ? e.text : String(e))
  }
}
</script>

<template>
  <n-form label-placement="left" label-width="auto" size="small">
    <n-divider title-placement="left">{{ t('adv.access') }}</n-divider>
    <n-form-item :label="t('adv.allow_ips')" :feedback="t('adv.allow_ips_hint')"><n-dynamic-tags v-model:value="a.allow_ips" /></n-form-item>
    <n-form-item :label="t('adv.deny_ips')"><n-dynamic-tags v-model:value="a.deny_ips" /></n-form-item>
    <n-form-item :label="t('adv.deny_ua')" :feedback="t('adv.deny_ua_hint')"><n-dynamic-tags v-model:value="a.deny_ua" /></n-form-item>
    <n-form-item :label="t('adv.hotlink')" :feedback="t('adv.hotlink_hint')"><n-dynamic-tags v-model:value="a.hotlink" :disabled="site.type === 'proxy' || ['node', 'python', 'java', 'tomcat'].includes(site.type)" /></n-form-item>

    <n-divider title-placement="left">{{ t('adv.limits') }}</n-divider>
    <n-form-item :label="t('adv.rate')" :feedback="t('adv.rate_hint')">
      <n-space align="center">
        <n-input-number v-model:value="a.rate_limit" :min="0" :max="10000" style="width: 120px"><template #suffix>r/s</template></n-input-number>
        <span>{{ t('adv.burst') }}</span><n-input-number v-model:value="a.rate_burst" :min="0" :max="100000" style="width: 110px" />
      </n-space>
    </n-form-item>
    <n-form-item :label="t('adv.conn')"><n-input-number v-model:value="a.conn_limit" :min="0" :max="100000" style="width: 120px" /></n-form-item>
    <n-form-item :label="t('adv.max_body')"><n-input-number v-model:value="a.max_body_mb" :min="0" :max="10240" style="width: 130px"><template #suffix>MB</template></n-input-number></n-form-item>

    <n-divider title-placement="left">{{ t('adv.behaviour') }}</n-divider>
    <n-space vertical>
      <n-checkbox v-model:checked="a.autoindex">{{ t('adv.autoindex') }}</n-checkbox>
      <n-checkbox v-model:checked="a.error_pages">{{ t('adv.error_pages') }}</n-checkbox>
      <n-checkbox v-if="site.type === 'php'" v-model:checked="a.open_basedir">{{ t('adv.open_basedir') }}</n-checkbox>
      <n-checkbox v-model:checked="a.block_xmlrpc">{{ t('adv.block_xmlrpc') }}</n-checkbox>
    </n-space>

    <template v-if="!site.passthrough">
      <n-divider title-placement="left">{{ t('adv.varnish') }}</n-divider>
      <n-space vertical>
        <n-checkbox v-model:checked="a.varnish">{{ t('adv.varnish_on') }}</n-checkbox>
        <n-form-item v-if="a.varnish" :label="t('adv.varnish_ttl')" :feedback="t('adv.varnish_ttl_hint')">
          <n-input-number v-model:value="a.varnish_ttl" :min="0" :max="2592000" style="width: 150px"><template #suffix>s</template></n-input-number>
        </n-form-item>
        <n-form-item v-if="a.varnish" :label="t('adv.varnish_exclude')" :feedback="t('adv.varnish_exclude_hint')"><n-dynamic-tags v-model:value="a.varnish_exclude" /></n-form-item>
        <n-button v-if="site.advanced?.varnish" size="small" @click="purge">{{ t('adv.varnish_purge') }}</n-button>
      </n-space>
    </template>

    <n-divider title-placement="left">{{ t('adv.redirects') }}</n-divider>
    <n-dynamic-input v-model:value="a.redirects" :on-create="() => ({ from: '/', to: 'https://', code: 301 })">
      <template #default="{ value }">
        <n-space :wrap="false" style="width: 100%">
          <n-input v-model:value="value.from" placeholder="/old-path" style="width: 180px" />
          <n-input v-model:value="value.to" placeholder="https://example.com/new" />
          <n-select v-model:value="value.code" :options="[{ label: '301', value: 301 }, { label: '302', value: 302 }]" style="width: 90px" />
        </n-space>
      </template>
    </n-dynamic-input>
    <n-space justify="end" style="margin-top: 16px"><n-button type="primary" @click="save">{{ t('common.save') }}</n-button></n-space>
  </n-form>
</template>
