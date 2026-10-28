<script setup lang="ts">
import { computed, h, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NCard, NCode, NCollapse, NCollapseItem, NDataTable, NForm, NFormItem, NInput, NModal, NPopconfirm, NRadioButton,
  NRadioGroup, NSelect, NSpace, NSwitch, NTag, useMessage, type DataTableColumns,
} from 'naive-ui'
import { get, post, put, ApiError } from '../api/client'
import type { FirewallRule, FirewallView } from '../api/types'
import IpGeo from './IpGeo.vue'

const { t } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))
const v = ref<FirewallView | null>(null)
const draft = ref({ enabled: false, policy: 'accept' as 'accept' | 'drop', ping: true, protect: true })
const saving = ref(false)

async function load() {
  try {
    v.value = await get<FirewallView>('/firewall')
    draft.value = { enabled: v.value.enabled, policy: v.value.policy, ping: v.value.ping, protect: v.value.protect }
  } catch (e) {
    fail(e)
  }
}
onMounted(load)
defineExpose({ load })

// ---- confirm a drop policy over a fresh connection ----
const countdown = ref(0)
const confirmState = ref<'' | 'waiting' | 'ok' | 'failed'>('')
let timer: number | undefined
onBeforeUnmount(() => clearInterval(timer))
async function afterChange(armed: boolean) {
  if (!armed) return load()
  confirmState.value = 'waiting'
  countdown.value = v.value?.confirm_secs ?? 60
  clearInterval(timer)
  timer = window.setInterval(() => {
    countdown.value--
    if (countdown.value <= 0) clearInterval(timer)
  }, 1000)
  // The change response closed the connection, so this opens a new one through the new rules.
  for (let i = 0; i < 5; i++) {
    await new Promise((r) => setTimeout(r, 1500))
    try {
      await post('/firewall/confirm')
      confirmState.value = 'ok'
      clearInterval(timer)
      return load()
    } catch (e) {
      if (e instanceof ApiError && e.code === 'fw.nothing_to_confirm') break
    }
  }
  confirmState.value = 'failed'
}

async function save() {
  if (!v.value) return
  saving.value = true
  try {
    const { linode_token: _, ...cfg } = v.value
    const r = await put<{ confirm: boolean }>('/firewall', { ...cfg, ...draft.value })
    msg.success(t('common.saved'))
    await afterChange(r.confirm)
  } catch (e) {
    fail(e)
  } finally {
    saving.value = false
  }
}

// ---- port rules ----
const edit = ref<FirewallRule | null>(null)
const blank = (): FirewallRule => ({ id: 0, action: 'accept', proto: 'tcp', ports: '', source: '', remark: '', enabled: true, created_at: 0 })
async function saveRule() {
  const r = edit.value!
  try {
    const res = await (r.id ? put<{ confirm: boolean }>(`/firewall/rules/${r.id}`, r) : post<{ confirm: boolean }>('/firewall/rules', r))
    edit.value = null
    await afterChange(res.confirm)
  } catch (e) {
    fail(e)
  }
}
async function toggleRule(r: FirewallRule, on: boolean) {
  try {
    const res = await put<{ confirm: boolean }>(`/firewall/rules/${r.id}`, { ...r, enabled: on })
    await afterChange(res.confirm)
  } catch (e) {
    fail(e)
  }
}
async function removeRule(r: FirewallRule) {
  try {
    const res = await post<{ confirm: boolean }>(`/firewall/rules/${r.id}/delete`)
    await afterChange(res.confirm)
  } catch (e) {
    fail(e)
  }
}
const columns: DataTableColumns<FirewallRule> = [
  { title: () => t('fw.action'), key: 'action', width: 90, render: (r) => h(NTag, { size: 'small', type: r.action === 'accept' ? 'success' : 'error' }, () => t(`fw.actions.${r.action}`)) },
  { title: () => t('fw.proto'), key: 'proto', width: 90, render: (r) => (r.proto === 'both' ? 'TCP+UDP' : r.proto.toUpperCase()) },
  { title: () => t('fw.ports'), key: 'ports', width: 160, render: (r) => h('code', r.ports) },
  { title: () => t('fw.source'), key: 'source', width: 280, render: (r) => (r.source ? h(IpGeo, { ip: r.source }) : t('fw.anywhere')) },
  { title: () => t('site.remark'), key: 'remark', ellipsis: { tooltip: true } },
  { title: () => t('cron.enabled'), key: 'enabled', width: 80, render: (r) => h(NSwitch, { size: 'small', value: r.enabled, 'onUpdate:value': (on: boolean) => toggleRule(r, on) }) },
  {
    title: '',
    key: 'actions',
    width: 140,
    render: (r) =>
      h(NSpace, { size: 6 }, () => [
        h(NButton, { size: 'tiny', onClick: () => (edit.value = { ...r }) }, () => t('common.edit')),
        h(NPopconfirm, { onPositiveClick: () => removeRule(r) }, { trigger: () => h(NButton, { size: 'tiny', type: 'error', ghost: true }, () => t('common.delete')), default: () => t('fw.delete_rule') }),
      ]),
  },
]
const dirty = computed(() => !!v.value && (['enabled', 'policy', 'ping', 'protect'] as const).some((k) => draft.value[k] !== v.value![k]))
const presets = [
  { label: 'SSH 22', ports: '22' }, { label: 'HTTP/HTTPS', ports: '80,443' }, { label: 'MySQL 3306', ports: '3306' },
  { label: 'PostgreSQL 5432', ports: '5432' }, { label: 'Redis 6379', ports: '6379' }, { label: 'FTP 21,39000-40000', ports: '21,39000-40000' },
]
</script>

<template>
  <n-alert v-if="v && !v.available" type="error" :show-icon="false" style="margin-bottom: 12px">{{ t('fw.no_nft') }}</n-alert>
  <n-alert v-if="confirmState === 'waiting'" type="warning" style="margin-bottom: 12px">{{ t('fw.confirming', { n: countdown }) }}</n-alert>
  <n-alert v-else-if="confirmState === 'ok'" type="success" closable style="margin-bottom: 12px" @close="confirmState = ''">{{ t('fw.confirmed') }}</n-alert>
  <n-alert v-else-if="confirmState === 'failed'" type="error" closable style="margin-bottom: 12px" @close="confirmState = ''">{{ t('fw.confirm_failed', { n: v?.confirm_secs ?? 60 }) }}</n-alert>

  <n-card v-if="v" size="small" :title="t('fw.title')">
    <template #header-extra>
      <n-tag :type="v.enabled ? 'success' : 'default'" size="small">{{ v.enabled ? t('fw.on') : t('fw.off') }}</n-tag>
    </template>
    <n-form label-placement="left" label-width="auto">
      <n-form-item :label="t('fw.enable')">
        <n-switch v-model:value="draft.enabled" :disabled="!v.available" />
        <span class="hint">{{ t('fw.enable_hint') }}</span>
      </n-form-item>
      <n-form-item :label="t('fw.policy')">
        <n-radio-group v-model:value="draft.policy">
          <n-radio-button value="accept">{{ t('fw.policies.accept') }}</n-radio-button>
          <n-radio-button value="drop">{{ t('fw.policies.drop') }}</n-radio-button>
        </n-radio-group>
        <span class="hint">{{ t(`fw.policy_hint.${draft.policy}`) }}</span>
      </n-form-item>
      <n-form-item :label="t('fw.protect')">
        <n-switch v-model:value="draft.protect" />
        <span class="hint">{{ t('fw.protect_hint', { ports: v.protected_ports.join(', ') }) }}</span>
      </n-form-item>
      <n-form-item :label="t('fw.ping')"><n-switch v-model:value="draft.ping" /></n-form-item>
      <n-alert v-if="draft.policy === 'drop' && !draft.protect" type="warning" :show-icon="false" style="margin-bottom: 12px">{{ t('fw.drop_unprotected') }}</n-alert>
      <n-button type="primary" :loading="saving" :disabled="!dirty" @click="save">{{ t('common.save') }}</n-button>
    </n-form>
  </n-card>

  <n-card size="small" :title="t('fw.rules')" style="margin-top: 16px">
    <template #header-extra><n-button size="small" type="primary" @click="edit = blank()">{{ t('fw.add_rule') }}</n-button></template>
    <p class="hint" style="margin: 0 0 8px">{{ t('fw.rules_hint') }}</p>
    <n-data-table size="small" :columns="columns" :data="v?.rules ?? []" :row-key="(r: FirewallRule) => r.id" :scroll-x="1000" />
  </n-card>

  <n-collapse style="margin-top: 16px">
    <n-collapse-item :title="t('fw.live')" name="live">
      <n-code :code="v?.live || t('fw.no_table')" language="bash" />
    </n-collapse-item>
  </n-collapse>

  <n-modal :show="!!edit" @update:show="(x: boolean) => !x && (edit = null)">
    <n-card v-if="edit" :title="edit.id ? t('common.edit') : t('fw.add_rule')" style="width: min(560px, calc(100vw - 32px))" closable @close="edit = null">
      <n-form label-placement="top">
        <n-space>
          <n-form-item :label="t('fw.action')">
            <n-radio-group v-model:value="edit.action">
              <n-radio-button value="accept">{{ t('fw.actions.accept') }}</n-radio-button>
              <n-radio-button value="drop">{{ t('fw.actions.drop') }}</n-radio-button>
            </n-radio-group>
          </n-form-item>
          <n-form-item :label="t('fw.proto')">
            <n-select v-model:value="edit.proto" :options="[{ label: 'TCP', value: 'tcp' }, { label: 'UDP', value: 'udp' }, { label: 'TCP+UDP', value: 'both' }]" style="width: 130px" />
          </n-form-item>
        </n-space>
        <n-form-item :label="t('fw.ports')" :feedback="t('fw.ports_hint')">
          <n-input v-model:value="edit.ports" placeholder="80,443 / 8000-9000" />
        </n-form-item>
        <n-space :size="4" style="margin: -8px 0 12px">
          <n-button v-for="p in presets" :key="p.label" size="tiny" quaternary @click="edit.ports = p.ports">{{ p.label }}</n-button>
        </n-space>
        <n-form-item :label="t('fw.source')" :feedback="t('fw.source_hint')">
          <n-input v-model:value="edit.source" :placeholder="t('fw.anywhere')" />
        </n-form-item>
        <n-form-item :label="t('site.remark')"><n-input v-model:value="edit.remark" /></n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="edit = null">{{ t('common.cancel') }}</n-button>
          <n-button type="primary" :disabled="!edit.ports" @click="saveRule">{{ t('common.save') }}</n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>
</template>

<style scoped>
.hint {
  margin-left: 12px;
  font-size: 12px;
  opacity: 0.65;
}
</style>
