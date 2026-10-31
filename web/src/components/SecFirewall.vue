<script setup lang="ts">
import { computed, h, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NCard, NCode, NCollapse, NCollapseItem, NDataTable, NForm, NFormItem, NInput, NModal, NPopconfirm, NRadioButton,
  NRadioGroup, NSelect, NSpace, NSwitch, NTag, NTooltip, useMessage, type DataTableColumns,
} from 'naive-ui'
import { get, post, put, ApiError } from '../api/client'
import { fwConfig } from '../api/firewall'
import type { FirewallRule, FirewallView } from '../api/types'
import IpGeo from './IpGeo.vue'
import SecCDN from './SecCDN.vue'

const { t } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))
const v = ref<FirewallView | null>(null)
const draft = ref({ enabled: false, policy: 'accept' as 'accept' | 'drop', ping: true })
const saving = ref(false)

async function load() {
  try {
    v.value = await get<FirewallView>('/firewall')
    draft.value = { enabled: v.value.enabled, policy: v.value.policy, ping: v.value.ping }
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
    const r = await put<{ confirm: boolean }>('/firewall', { ...fwConfig(v.value), ...draft.value })
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
// A rule may name several sources (stored comma separated, edited one per line).
function sourceCell(src: string) {
  if (!src) return t('fw.anywhere')
  const all = src.split(',')
  return h(NSpace, { size: 4, align: 'center', wrap: false }, () => [
    ...all.slice(0, 2).map((ip) => h(IpGeo, { ip })),
    all.length > 2 ? h(NTooltip, null, { trigger: () => h(NTag, { size: 'tiny', bordered: false }, () => `+${all.length - 2}`), default: () => all.slice(2).join('\n') }) : null,
  ])
}
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
  { title: () => t('fw.source'), key: 'source', width: 280, render: (r) => sourceCell(r.source) },
  {
    title: () => t('site.remark'),
    key: 'remark',
    ellipsis: { tooltip: true },
    render: (r) =>
      r.system
        ? h('span', [h(NTag, { size: 'small', type: 'info', bordered: false, style: 'margin-right:6px' }, () => t('fw.system')), t(`fw.sys.${r.system}`), r.remark ? ' · ' + r.remark : ''])
        : r.remark,
  },
  {
    title: () => t('cron.enabled'),
    key: 'enabled',
    width: 80,
    render: (r) =>
      r.system && r.enabled
        ? h(NPopconfirm, { onPositiveClick: () => toggleRule(r, false) }, { trigger: () => h(NSwitch, { size: 'small', value: true }), default: () => t('fw.sys_off_confirm') })
        : h(NSwitch, { size: 'small', value: r.enabled, 'onUpdate:value': (on: boolean) => toggleRule(r, on) }),
  },
  {
    title: '',
    key: 'actions',
    width: 140,
    render: (r) =>
      h(NSpace, { size: 6 }, () => [
        h(NButton, { size: 'tiny', onClick: () => (edit.value = { ...r, source: r.source.replaceAll(',', '\n') }) }, () => t('common.edit')),
        h(NPopconfirm, { onPositiveClick: () => removeRule(r) }, { trigger: () => h(NButton, { size: 'tiny', type: 'error', ghost: true }, () => t('common.delete')), default: () => (r.system ? t('fw.sys_delete_confirm') : t('fw.delete_rule')) }),
      ]),
  },
]
const dirty = computed(() => !!v.value && (['enabled', 'policy', 'ping'] as const).some((k) => draft.value[k] !== v.value![k]))
// Under a drop policy, SSH and the panel must stay reachable.
const sysOpen = computed(() => (['ssh', 'panel'] as const).every((k) => v.value?.rules.some((r) => r.system === k && r.enabled)))
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
    <div class="rows">
      <div class="row">
        <div class="label"><b>{{ t('fw.enable') }}</b><span>{{ t('fw.enable_hint') }}</span></div>
        <n-switch v-model:value="draft.enabled" :disabled="!v.available" />
      </div>
      <div class="row">
        <div class="label"><b>{{ t('fw.policy') }}</b><span>{{ t(`fw.policy_hint.${draft.policy}`) }}</span></div>
        <n-radio-group v-model:value="draft.policy" size="small">
          <n-radio-button value="accept">{{ t('fw.policies.accept') }}</n-radio-button>
          <n-radio-button value="drop">{{ t('fw.policies.drop') }}</n-radio-button>
        </n-radio-group>
      </div>
      <div class="row">
        <div class="label"><b>{{ t('fw.ping') }}</b><span>{{ t('fw.ping_hint') }}</span></div>
        <n-switch v-model:value="draft.ping" />
      </div>
    </div>
    <n-alert v-if="draft.policy === 'drop' && !sysOpen" type="warning" :show-icon="false" style="margin: 12px 0 0">{{ t('fw.drop_unprotected') }}</n-alert>
    <n-button type="primary" size="small" :loading="saving" :disabled="!dirty" style="margin-top: 12px" @click="save">{{ t('common.save') }}</n-button>
  </n-card>

  <n-card size="small" :title="t('fw.rules')" style="margin-top: 16px">
    <template #header-extra><n-button size="small" type="primary" @click="edit = blank()">{{ t('fw.add_rule') }}</n-button></template>
    <p class="hint" style="margin: 0 0 8px">{{ t('fw.rules_hint') }}</p>
    <n-data-table size="small" :columns="columns" :data="v?.rules ?? []" :row-key="(r: FirewallRule) => r.id" :scroll-x="1000" />
  </n-card>

  <SecCDN />

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
        <n-form-item :label="t('fw.ports')" :feedback="edit.system ? t('fw.sys_ports') : t('fw.ports_hint')">
          <n-input v-model:value="edit.ports" :disabled="!!edit.system" placeholder="80,443 / 8000-9000" />
        </n-form-item>
        <n-space v-if="!edit.system" :size="4" style="margin: -8px 0 12px">
          <n-button v-for="p in presets" :key="p.label" size="tiny" quaternary @click="edit.ports = p.ports">{{ p.label }}</n-button>
        </n-space>
        <n-form-item :label="t('fw.source')" :feedback="t('fw.source_hint')">
          <n-input v-model:value="edit.source" type="textarea" :autosize="{ minRows: 2, maxRows: 8 }" :placeholder="t('fw.source_multi')" />
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
.rows {
  display: flex;
  flex-direction: column;
}
.row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 10px 0;
  border-bottom: 1px solid rgba(128, 128, 128, 0.14);
}
.row:last-child {
  border-bottom: 0;
}
.label {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}
.label span {
  font-size: 12px;
  opacity: 0.65;
}
.hint {
  margin-left: 12px;
  font-size: 12px;
  opacity: 0.65;
}
</style>
