<script setup lang="ts">
import { h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NAlert, NButton, NCard, NCheckbox, NDataTable, NInput, NInputNumber, NPopconfirm, NSpace, NStatistic, NTag, useMessage, type DataTableColumns } from 'naive-ui'
import { get, post, ApiError } from '../api/client'
import type { FirewallIP } from '../api/types'
import { datetime } from '../format'
import IpGeo from './IpGeo.vue'
import TaskModal from './TaskModal.vue'
import SecFeeds from './SecFeeds.vue'

interface Status { installed: boolean; state: string; community: boolean; last_poll: number; poll_error?: string; bans: number; community_bans: number }
interface Alert { id: number; scenario: string; created_at: string; events_count: number; source: { ip?: string; range?: string; cn?: string; as_name?: string; scope: string; value: string }; decisions?: { duration: string; type: string }[] | null }

const { t, locale } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))
const st = ref<Status | null>(null)
const alerts = ref<Alert[]>([])
const bans = ref<FirewallIP[]>([])
const community = ref(false)
const fwEnabled = ref(true)
const task = ref<{ id: string; title: string } | null>(null)
const ban = ref({ ip: '', hours: 4, reason: '' })

async function load() {
  try {
    st.value = await get<Status>('/crowdsec')
    community.value = st.value.community
    fwEnabled.value = (await get<{ enabled: boolean }>('/firewall')).enabled
    const ips = await get<{ items: FirewallIP[] }>('/firewall/ips?list=block')
    bans.value = ips.items.filter((x) => x.source === 'crowdsec')
    if (st.value.state === 'running') alerts.value = (await get<{ items: Alert[] | null }>('/crowdsec/alerts?limit=100')).items ?? []
  } catch (e) {
    fail(e)
  }
}
onMounted(load)
defineExpose({ load })

async function install() {
  const r = await post<{ task_id: string }>('/crowdsec/install', { community: community.value })
  task.value = { id: r.task_id, title: t('cs.install') }
}
async function uninstall() {
  const r = await post<{ task_id: string }>('/crowdsec/uninstall')
  task.value = { id: r.task_id, title: t('cs.uninstall') }
}
async function addBan() {
  try {
    await post('/crowdsec/decisions', ban.value)
    ban.value.ip = ''
    load()
  } catch (e) {
    fail(e)
  }
}
async function unban(ip: string) {
  try {
    await post('/crowdsec/decisions/delete', { ip })
    load()
  } catch (e) {
    fail(e)
  }
}

const banCols: DataTableColumns<FirewallIP> = [
  { title: () => t('common.ip'), key: 'ip', width: 320, render: (x) => h(IpGeo, { ip: x.ip }) },
  { title: () => t('cs.scenario'), key: 'remark', ellipsis: { tooltip: true } },
  { title: () => t('fw.expires'), key: 'expires_at', width: 180, render: (x) => datetime(x.expires_at, locale.value) },
  { title: '', key: 'a', width: 90, render: (x) => h(NPopconfirm, { onPositiveClick: () => unban(x.ip) }, { trigger: () => h(NButton, { size: 'tiny', ghost: true, type: 'error' }, () => t('cs.unban')), default: () => t('fw.remove_confirm', { ip: x.ip }) }) },
]
const alertCols: DataTableColumns<Alert> = [
  { title: () => t('common.time'), key: 'created_at', width: 170, render: (a) => datetime(Date.parse(a.created_at) / 1000, locale.value) },
  { title: () => t('cs.scenario'), key: 'scenario', ellipsis: { tooltip: true } },
  { title: () => t('common.ip'), key: 'source', width: 300, render: (a) => (a.source.ip ? h(IpGeo, { ip: a.source.ip }) : a.source.value) },
  { title: () => t('cs.events'), key: 'events_count', width: 80 },
  { title: () => t('cs.decision'), key: 'decisions', width: 130, render: (a) => (a.decisions?.length ? h(NTag, { size: 'small', type: 'error' }, () => `${a.decisions![0].type} ${a.decisions![0].duration}`) : '—') },
]
</script>

<template>
  <n-card size="small" title="CrowdSec">
    <p class="hint" style="margin-top: 0">{{ t('cs.intro') }}</p>
    <template v-if="st && !st.installed">
      <n-checkbox v-model:checked="community">{{ t('cs.community') }}</n-checkbox>
      <div style="margin-top: 12px"><n-button type="primary" @click="install">{{ t('cs.install') }}</n-button></div>
    </template>
    <template v-else-if="st">
      <n-alert v-if="!fwEnabled" type="warning" :show-icon="false" style="margin-bottom: 12px">{{ t('cs.fw_off') }}</n-alert>
      <n-alert v-if="st.poll_error" type="error" :show-icon="false" style="margin-bottom: 12px">{{ st.poll_error }}</n-alert>
      <n-space justify="space-between" align="center">
        <n-space :size="32">
          <n-statistic :label="t('common.status')"><n-tag :type="st.state === 'running' ? 'success' : 'error'">{{ st.state || '—' }}</n-tag></n-statistic>
          <n-statistic :label="t('cs.active_bans')" :value="st.bans" />
          <n-statistic :label="t('cs.community_short')" :value="st.community ? st.community_bans.toLocaleString() : t('fw.off')" />
          <n-statistic :label="t('cs.last_poll')" :value="st.last_poll ? datetime(st.last_poll, locale) : '—'" />
        </n-space>
        <n-space>
          <n-checkbox v-model:checked="community">{{ t('cs.community') }}</n-checkbox>
          <n-button v-if="community !== st.community" size="small" @click="install">{{ t('common.save') }}</n-button>
          <n-popconfirm @positive-click="uninstall">
            <template #trigger><n-button size="small" type="error" ghost>{{ t('cs.uninstall') }}</n-button></template>
            {{ t('cs.uninstall_confirm') }}
          </n-popconfirm>
        </n-space>
      </n-space>
      <n-alert v-if="st.community && !st.community_bans" type="info" :show-icon="false" style="margin-top: 12px">{{ t('cs.community_empty') }}</n-alert>
    </template>
  </n-card>
  <SecFeeds kind="block" />

  <template v-if="st?.installed">
    <n-card size="small" :title="t('cs.bans')" style="margin-top: 16px">
      <template #header-extra>
        <n-space align="center">
          <n-input v-model:value="ban.ip" size="small" :placeholder="t('fw.ip_placeholder')" style="width: 180px" />
          <n-input-number v-model:value="ban.hours" size="small" :min="1" :max="8760" style="width: 110px"><template #suffix>h</template></n-input-number>
          <n-button size="small" type="primary" :disabled="!ban.ip" @click="addBan">{{ t('cs.ban') }}</n-button>
        </n-space>
      </template>
      <n-data-table size="small" :columns="banCols" :data="bans" :pagination="{ pageSize: 20 }" :row-key="(x: FirewallIP) => x.id" :scroll-x="900" />
    </n-card>
    <n-card size="small" :title="t('cs.alerts')" style="margin-top: 16px">
      <template #header-extra><n-button size="small" @click="load">{{ t('common.refresh') }}</n-button></template>
      <n-data-table size="small" :columns="alertCols" :data="alerts" :pagination="{ pageSize: 20 }" :row-key="(a: Alert) => a.id" :scroll-x="1000" />
    </n-card>
  </template>
  <TaskModal :task-id="task?.id ?? null" :title="task?.title ?? ''" @close="task = null; load()" @done="load" />
</template>

<style scoped>
.hint {
  font-size: 12px;
  opacity: 0.65;
  margin: 8px 0 0;
}
</style>
