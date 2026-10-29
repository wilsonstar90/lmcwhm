<script setup lang="ts">
// Visitor addresses behind a CDN: Cloudflare is built in; another CDN can be
// added with its ranges and the header it puts the visitor's address in.
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NCard, NCollapse, NCollapseItem, NFormItem, NInput, NSpace, NTag, useMessage } from 'naive-ui'
import { get, put, ApiError } from '../api/client'

interface CDNConfig { ranges: string[]; header: string }

const { t } = useI18n()
const msg = useMessage()
const cf = ref<string[]>([])
const saved = ref<CDNConfig>({ ranges: [], header: '' })
const ranges = ref('')
const header = ref('')
const saving = ref(false)

function show(c: CDNConfig) {
  saved.value = c
  ranges.value = c.ranges.join('\n')
  header.value = c.header
}
onMounted(async () => {
  try {
    const r = await get<{ config: CDNConfig; cloudflare: string[] }>('/firewall/cdn')
    cf.value = r.cloudflare
    show(r.config)
  } catch (e) {
    msg.error(e instanceof ApiError ? e.text : String(e))
  }
})
const list = computed(() => ranges.value.split(/[\s,]+/).map((x) => x.trim()).filter(Boolean))
const dirty = computed(() => header.value.trim() !== saved.value.header || list.value.join('\n') !== saved.value.ranges.join('\n'))
async function save() {
  saving.value = true
  try {
    const r = await put<{ config: CDNConfig }>('/firewall/cdn', { ranges: list.value, header: header.value.trim() })
    show(r.config)
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
    <n-form-item :label="t('fw.cdn.header')" :show-feedback="false" style="margin-top: 8px">
      <n-input v-model:value="header" placeholder="X-Forwarded-For / X-Real-IP" style="max-width: 320px" />
    </n-form-item>
    <n-form-item :label="t('fw.cdn.ranges')" :feedback="t('fw.cdn.ranges_hint')" style="margin-top: 8px">
      <n-input v-model:value="ranges" type="textarea" :autosize="{ minRows: 3, maxRows: 10 }" placeholder="203.0.113.0/24&#10;2001:db8::/32" />
    </n-form-item>
    <n-button type="primary" size="small" :loading="saving" :disabled="!dirty" @click="save">{{ t('common.save') }}</n-button>
  </n-card>
</template>

<style scoped>
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
