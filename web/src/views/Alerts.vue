<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NButton, NCard, NDataTable, NDynamicTags, NForm, NFormItem, NInput, NInputNumber, NModal, NPopconfirm, NSelect, NSpace,
  NSwitch, NTabPane, NTabs, NTag, useMessage, type DataTableColumns,
} from 'naive-ui'
import { get, post, put, ApiError } from '../api/client'
import { datetime } from '../format'

interface ChannelConfig { host?: string; port?: number; security?: string; username?: string; password?: string; from?: string; to?: string[]; bot_token?: string; chat_id?: string; url?: string; secret?: string }
interface Channel { id: number; name: string; kind: string; config: ChannelConfig; enabled: boolean }
interface Params { threshold?: number; minutes?: number; days?: number; count?: number }
interface Rule { id: number; name: string; kind: string; params: Params; channels: number[]; enabled: boolean; cooldown: number; active: number }
interface Ev { id: number; rule_name: string; kind: string; severity: string; title: string; body: string; result: string; created_at: number }

const { t, locale } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))
const tab = ref('rules')
const channels = ref<Channel[]>([])
const rules = ref<Rule[]>([])
const events = ref<Ev[]>([])
const total = ref(0)
const page = ref(1)

const channelKinds = ['email', 'telegram', 'webhook', 'wecom', 'dingtalk', 'slack', 'discord']
const ruleKinds = ['cpu', 'mem', 'load', 'disk', 'service_down', 'ssl_expiry', 'probe_down', 'cron_failed', 'login_failed', 'panel_login', 'ssh_login']
// Which parameters a rule kind takes.
const ruleParams: Record<string, (keyof Params)[]> = { cpu: ['threshold', 'minutes'], mem: ['threshold', 'minutes'], load: ['threshold', 'minutes'], disk: ['threshold'], ssl_expiry: ['days'], login_failed: ['count', 'minutes'] }
const defaults: Record<string, Params> = { cpu: { threshold: 90, minutes: 5 }, mem: { threshold: 90, minutes: 5 }, load: { threshold: 4, minutes: 5 }, disk: { threshold: 90 }, ssl_expiry: { days: 14 }, login_failed: { count: 5, minutes: 10 } }

async function load() {
  try {
    channels.value = (await get<{ items: Channel[] }>('/alerts/channels')).items
    rules.value = (await get<{ items: Rule[] }>('/alerts/rules')).items
    loadEvents()
  } catch (e) {
    fail(e)
  }
}
async function loadEvents() {
  const r = await get<{ items: Ev[]; total: number }>(`/alerts/events?limit=20&offset=${(page.value - 1) * 20}`)
  events.value = r.items
  total.value = r.total
}
onMounted(load)

// ---- channels ----
const cf = ref<Channel | null>(null)
function newChannel(kind = 'telegram') {
  cf.value = { id: 0, name: t(`alert.channel_kinds.${kind}`), kind, enabled: true, config: kind === 'email' ? { port: 587, security: 'starttls', to: [] } : {} }
}
async function saveChannel() {
  const c = cf.value!
  try {
    if (c.id) await put(`/alerts/channels/${c.id}`, c)
    else await post('/alerts/channels', c)
    cf.value = null
    load()
  } catch (e) {
    fail(e)
  }
}
async function testChannel(c: Channel) {
  const r = await post<{ ok: boolean; error?: string }>(`/alerts/channels/${c.id}/test`)
  if (r.ok) msg.success(t('alert.test_sent'))
  else msg.error(r.error ?? '')
}
async function delChannel(c: Channel) {
  await post(`/alerts/channels/${c.id}/delete`).catch(fail)
  load()
}
const channelCols = computed<DataTableColumns<Channel>>(() => [
  { title: () => t('alert.name'), key: 'name', render: (c) => h('b', c.name) },
  { title: () => t('alert.kind'), key: 'kind', width: 140, render: (c) => t(`alert.channel_kinds.${c.kind}`) },
  { title: () => t('alert.enabled'), key: 'enabled', width: 90, render: (c) => h(NTag, { size: 'small', type: c.enabled ? 'success' : 'default' }, () => (c.enabled ? t('fw.on') : t('fw.off'))) },
  {
    title: () => t('common.actions'),
    key: 'a',
    width: 220,
    render: (c) =>
      h(NSpace, { size: 4 }, () => [
        h(NButton, { size: 'tiny', onClick: () => testChannel(c) }, () => t('alert.test')),
        h(NButton, { size: 'tiny', onClick: () => (cf.value = JSON.parse(JSON.stringify(c))) }, () => t('common.edit')),
        h(NPopconfirm, { onPositiveClick: () => delChannel(c) }, { trigger: () => h(NButton, { size: 'tiny', type: 'error', quaternary: true }, () => t('common.delete')), default: () => t('common.confirm_delete') }),
      ]),
  },
])

// ---- rules ----
const rf = ref<Rule | null>(null)
function newRule(kind = 'cpu') {
  rf.value = { id: 0, name: t(`alert.rule_kinds.${kind}`), kind, params: { ...(defaults[kind] ?? {}) }, channels: channels.value.map((c) => c.id), enabled: true, cooldown: 60, active: 0 }
}
function setKind(k: string) {
  const r = rf.value!
  r.kind = k
  r.name = t(`alert.rule_kinds.${k}`)
  r.params = { ...(defaults[k] ?? {}) }
}
async function saveRule() {
  const r = rf.value!
  try {
    if (r.id) await put(`/alerts/rules/${r.id}`, r)
    else await post('/alerts/rules', r)
    rf.value = null
    load()
  } catch (e) {
    fail(e)
  }
}
async function toggleRule(r: Rule, on: boolean) {
  try {
    await put(`/alerts/rules/${r.id}`, { ...r, enabled: on })
    r.enabled = on
  } catch (e) {
    fail(e)
  }
}
async function delRule(r: Rule) {
  await post(`/alerts/rules/${r.id}/delete`).catch(fail)
  load()
}
function describe(r: Rule) {
  const p = r.params
  return t(`alert.rule_desc.${r.kind}`, { threshold: p.threshold, minutes: p.minutes, days: p.days, count: p.count })
}
const ruleCols = computed<DataTableColumns<Rule>>(() => [
  { title: () => t('alert.name'), key: 'name', render: (r) => h('b', r.name) },
  { title: () => t('alert.condition'), key: 'kind', render: (r) => describe(r) },
  { title: () => t('alert.channels'), key: 'channels', render: (r) => r.channels.map((id) => channels.value.find((c) => c.id === id)?.name ?? `#${id}`).join(', ') },
  { title: () => t('common.status'), key: 'active', width: 110, render: (r) => (r.active ? h(NTag, { size: 'small', type: 'error' }, () => t('alert.firing', { n: r.active })) : h(NTag, { size: 'small' }, () => t('alert.ok'))) },
  { title: () => t('alert.enabled'), key: 'enabled', width: 80, render: (r) => h(NSwitch, { size: 'small', value: r.enabled, 'onUpdate:value': (v: boolean) => toggleRule(r, v) }) },
  {
    title: () => t('common.actions'),
    key: 'a',
    width: 160,
    render: (r) =>
      h(NSpace, { size: 4 }, () => [
        h(NButton, { size: 'tiny', onClick: () => (rf.value = JSON.parse(JSON.stringify(r))) }, () => t('common.edit')),
        h(NPopconfirm, { onPositiveClick: () => delRule(r) }, { trigger: () => h(NButton, { size: 'tiny', type: 'error', quaternary: true }, () => t('common.delete')), default: () => t('common.confirm_delete') }),
      ]),
  },
])
const sevType = (s: string) => (s === 'critical' ? 'error' : s === 'warning' ? 'warning' : s === 'resolved' ? 'success' : 'info')
const evCols = computed<DataTableColumns<Ev>>(() => [
  { title: () => t('common.time'), key: 'created_at', width: 170, render: (e) => datetime(e.created_at, locale.value) },
  { title: () => t('alert.severity'), key: 'severity', width: 100, render: (e) => h(NTag, { size: 'small', type: sevType(e.severity) }, () => t(`alert.sev.${e.severity}`)) },
  { title: () => t('alert.name'), key: 'rule_name', width: 160 },
  { title: () => t('alert.message'), key: 'title', render: (e) => h('div', [h('div', e.title), h('div', { style: 'font-size:12px;opacity:.65' }, e.body)]) },
  { title: () => t('alert.delivery'), key: 'result', width: 220, ellipsis: { tooltip: true } },
])
</script>

<template>
  <n-tabs v-model:value="tab" type="line" animated>
    <n-tab-pane name="rules" :tab="t('alert.tab_rules')">
      <p v-if="!channels.length" class="hint">{{ t('alert.need_channel') }}</p>
      <n-space style="margin-bottom: 12px"><n-button type="primary" :disabled="!channels.length" @click="newRule()">{{ t('alert.new_rule') }}</n-button></n-space>
      <n-data-table size="small" :columns="ruleCols" :data="rules" :row-key="(r: Rule) => r.id" :scroll-x="900" />
    </n-tab-pane>
    <n-tab-pane name="channels" :tab="t('alert.tab_channels')">
      <n-space style="margin-bottom: 12px">
        <n-button v-for="k in channelKinds" :key="k" @click="newChannel(k)">+ {{ t(`alert.channel_kinds.${k}`) }}</n-button>
      </n-space>
      <n-data-table size="small" :columns="channelCols" :data="channels" :row-key="(c: Channel) => c.id" />
    </n-tab-pane>
    <n-tab-pane name="history" :tab="t('alert.tab_history')">
      <n-data-table
        size="small" remote :columns="evCols" :data="events" :row-key="(e: Ev) => e.id" :scroll-x="900"
        :pagination="{ page, pageSize: 20, itemCount: total, onUpdatePage: (p: number) => { page = p; loadEvents() } }"
      />
    </n-tab-pane>
  </n-tabs>

  <n-modal :show="!!cf" @update:show="(v: boolean) => !v && (cf = null)">
    <n-card v-if="cf" :title="t(`alert.channel_kinds.${cf.kind}`)" style="width: min(560px, calc(100vw - 32px))" closable @close="cf = null">
      <n-form label-placement="top">
        <n-form-item :label="t('alert.name')"><n-input v-model:value="cf.name" /></n-form-item>
        <template v-if="cf.kind === 'email'">
          <n-space>
            <n-form-item :label="t('alert.smtp_host')"><n-input v-model:value="cf.config.host" placeholder="smtp.gmail.com" /></n-form-item>
            <n-form-item :label="t('alert.port')"><n-input-number v-model:value="cf.config.port" :min="1" :max="65535" style="width: 110px" /></n-form-item>
            <n-form-item :label="t('alert.security')">
              <n-select v-model:value="cf.config.security" :options="['starttls', 'tls', 'none'].map((v) => ({ label: v.toUpperCase(), value: v }))" style="width: 120px" />
            </n-form-item>
          </n-space>
          <n-form-item :label="t('alert.username')"><n-input v-model:value="cf.config.username" /></n-form-item>
          <n-form-item :label="t('alert.password')"><n-input v-model:value="cf.config.password" type="password" show-password-on="click" /></n-form-item>
          <n-form-item :label="t('alert.from')"><n-input v-model:value="cf.config.from" placeholder="panel@example.com" /></n-form-item>
          <n-form-item :label="t('alert.to')"><n-dynamic-tags v-model:value="cf.config.to" /></n-form-item>
        </template>
        <template v-else-if="cf.kind === 'telegram'">
          <n-form-item label="Bot token" :feedback="t('alert.tg_hint')"><n-input v-model:value="cf.config.bot_token" type="password" show-password-on="click" placeholder="123456:ABC..." /></n-form-item>
          <n-form-item label="Chat ID"><n-input v-model:value="cf.config.chat_id" /></n-form-item>
        </template>
        <template v-else>
          <n-form-item :label="t('alert.webhook_url')" :feedback="cf.kind === 'webhook' ? t('alert.webhook_hint') : ''"><n-input v-model:value="cf.config.url" placeholder="https://" /></n-form-item>
          <n-form-item v-if="cf.kind === 'dingtalk'" :label="t('alert.sign_secret')"><n-input v-model:value="cf.config.secret" type="password" show-password-on="click" placeholder="SEC..." /></n-form-item>
        </template>
        <n-form-item :label="t('alert.enabled')"><n-switch v-model:value="cf.enabled" /></n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end"><n-button @click="cf = null">{{ t('common.cancel') }}</n-button><n-button type="primary" @click="saveChannel">{{ t('common.save') }}</n-button></n-space>
      </template>
    </n-card>
  </n-modal>

  <n-modal :show="!!rf" @update:show="(v: boolean) => !v && (rf = null)">
    <n-card v-if="rf" :title="rf.id ? t('common.edit') : t('alert.new_rule')" style="width: min(560px, calc(100vw - 32px))" closable @close="rf = null">
      <n-form label-placement="top">
        <n-form-item :label="t('alert.condition')">
          <n-select :value="rf.kind" :options="ruleKinds.map((k) => ({ label: t(`alert.rule_kinds.${k}`), value: k }))" @update:value="setKind" />
        </n-form-item>
        <n-form-item :label="t('alert.name')"><n-input v-model:value="rf.name" /></n-form-item>
        <n-space>
          <n-form-item v-for="p in ruleParams[rf.kind] ?? []" :key="p" :label="t(`alert.params.${p}`)">
            <n-input-number v-model:value="rf.params[p]" :min="p === 'threshold' && rf.kind === 'load' ? 0.1 : 1" :step="p === 'threshold' && rf.kind === 'load' ? 0.5 : 1" style="width: 130px" />
          </n-form-item>
        </n-space>
        <p class="hint" style="margin-top: -8px">{{ describe(rf) }}</p>
        <n-form-item :label="t('alert.channels')"><n-select v-model:value="rf.channels" multiple :options="channels.map((c) => ({ label: c.name, value: c.id }))" /></n-form-item>
        <n-form-item :label="t('alert.cooldown')" :feedback="t('alert.cooldown_hint')"><n-input-number v-model:value="rf.cooldown" :min="1" :max="10080" style="width: 140px" /></n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end"><n-button @click="rf = null">{{ t('common.cancel') }}</n-button><n-button type="primary" :disabled="!rf.channels.length" @click="saveRule">{{ t('common.save') }}</n-button></n-space>
      </template>
    </n-card>
  </n-modal>
</template>

<style scoped>
.hint {
  font-size: 12px;
  opacity: 0.65;
}
</style>
