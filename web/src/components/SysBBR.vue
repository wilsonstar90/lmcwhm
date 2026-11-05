<script setup lang="ts">
// TCP BBR (App Store → System): Google's congestion control, switched on
// or back to Ubuntu's defaults (cubic + fq_codel) without a reboot.
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NAlert, NButton, NCard, NDescriptions, NDescriptionsItem, NPopconfirm, NSpace, NTag, useMessage } from 'naive-ui'
import { get, post, ApiError } from '../api/client'

export interface BBRStatus {
  kernel: string
  supported: boolean
  enabled: boolean
  persisted: boolean
  current: string
  qdisc: string
  available: string[]
  ifaces: { name: string; qdisc: string }[]
}

const emit = defineEmits<{ changed: [s: BBRStatus] }>()
const { t } = useI18n()
const msg = useMessage()
const st = ref<BBRStatus | null>(null)
const busy = ref(false)

async function load() {
  try {
    st.value = await get<BBRStatus>('/apps/system/bbr')
  } catch (e) {
    msg.error(e instanceof ApiError ? e.text : String(e))
  }
}
onMounted(load)
defineExpose({ load })

async function set(enabled: boolean) {
  busy.value = true
  try {
    st.value = await post<BBRStatus>('/apps/system/bbr', { enabled })
    msg.success(t(enabled ? 'bbr.enabled_ok' : 'bbr.disabled_ok'))
    emit('changed', st.value)
  } catch (e) {
    msg.error(e instanceof ApiError ? e.text : String(e))
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <n-card size="small" title="TCP BBR">
    <template #header-extra>
      <n-tag v-if="st" size="small" :type="st.enabled ? 'success' : st.supported ? 'default' : 'error'" :bordered="false">
        {{ st.enabled ? t('bbr.on') : st.supported ? t('bbr.off') : t('bbr.unsupported') }}
      </n-tag>
    </template>
    <p class="desc">{{ t('bbr.desc') }}</p>
    <n-descriptions v-if="st" :column="1" label-placement="left" size="small" class="facts">
      <n-descriptions-item :label="t('bbr.kernel')"><code>{{ st.kernel || '—' }}</code></n-descriptions-item>
      <n-descriptions-item :label="t('bbr.current')">
        <code>{{ st.current || '—' }}</code>
        <span v-if="st.available.length" class="sub"> · {{ t('bbr.available') }} {{ st.available.join(' / ') }}</span>
      </n-descriptions-item>
      <n-descriptions-item :label="t('bbr.qdisc')"><code>{{ st.qdisc || '—' }}</code></n-descriptions-item>
      <n-descriptions-item :label="t('bbr.ifaces')">
        <span v-if="!st.ifaces.length">—</span>
        <span v-for="(i, n) in st.ifaces" :key="i.name"><code>{{ i.name }}</code> {{ i.qdisc || '?' }}<template v-if="n < st.ifaces.length - 1">、</template></span>
      </n-descriptions-item>
    </n-descriptions>
    <n-alert v-if="st && !st.supported" type="warning" :show-icon="false" class="block">{{ t('bbr.unsupported_hint') }}</n-alert>
    <n-alert v-if="st?.enabled && !st.persisted" type="info" :show-icon="false" class="block">{{ t('bbr.not_persisted') }}</n-alert>
    <ul class="effects">
      <li v-for="k in ['sender', 'stack', 'udp', 'docker', 'revert']" :key="k">{{ t(`bbr.effects.${k}`) }}</li>
    </ul>
    <n-space v-if="st" justify="end">
      <n-button v-if="!st.enabled || !st.persisted" type="primary" size="small" :disabled="!st.supported" :loading="busy" @click="set(true)">
        {{ t('bbr.enable') }}
      </n-button>
      <n-popconfirm v-if="st.enabled || st.persisted" @positive-click="set(false)">
        <template #trigger>
          <n-button size="small" type="error" ghost :loading="busy">{{ t('bbr.disable') }}</n-button>
        </template>
        {{ t('bbr.disable_confirm') }}
      </n-popconfirm>
    </n-space>
  </n-card>
</template>

<style scoped>
.desc {
  margin: 0 0 10px;
  font-size: 12.5px;
  opacity: 0.8;
}
.facts {
  margin-bottom: 10px;
}
.sub {
  font-size: 12px;
  opacity: 0.65;
}
.block {
  margin-bottom: 10px;
}
.effects {
  margin: 0 0 12px;
  padding-left: 18px;
  font-size: 12.5px;
  opacity: 0.8;
  line-height: 1.6;
}
code {
  word-break: break-all;
}
</style>
