<script setup lang="ts">
// Mail server (docker-mailserver): domains, mailboxes, aliases, DNS, deliverability.
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NCard, NDataTable, NForm, NFormItem, NInput, NModal, NPopconfirm, NSpace, NSwitch, NTabPane, NTabs, NTag, useMessage,
  type DataTableColumns,
} from 'naive-ui'
import { get, post, put, ApiError } from '../api/client'
import { datetime } from '../format'
import TaskModal from '../components/TaskModal.vue'
import LogsModal from '../components/LogsModal.vue'

interface Domain { name: string; created_at: number; dkim: string; accounts: number }
interface Status { enabled: boolean; hostname: string; state: string; image: string; has_cert: boolean; domains: Domain[]; ports: number[] }
interface Account { address: string; quota: string }
interface Alias { from: string; to: string[] }
interface DNSRec { type: string; name: string; content: string; priority?: number }
interface Check { key: string; ok: boolean; level: string; detail: string }

const { t, locale } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))
const st = ref<Status | null>(null)
const aliases = ref<Alias[]>([])
const accounts = ref<Account[]>([])
const host = ref('')
const task = ref<{ id: string; title: string } | null>(null)
const logs = ref<{ url: string; title: string } | null>(null)

async function load() {
  try {
    const r = await get<{ status: Status; aliases: Alias[] }>('/mail')
    st.value = r.status
    aliases.value = r.aliases
    if (!host.value) host.value = r.status.hostname
    accounts.value = (await get<{ items: Account[] }>('/mail/accounts')).items
  } catch (e) {
    fail(e)
  }
}
onMounted(load)
const running = computed(() => st.value?.state === 'running')

async function apply(enabled: boolean) {
  try {
    task.value = { id: (await put<{ task_id: string }>('/mail', { enabled, hostname: host.value })).task_id, title: t('menu.mail') }
  } catch (e) {
    fail(e)
  }
}
async function call(url: string, body: unknown, ok = t('common.done')) {
  try {
    await post(url, body)
    msg.success(ok)
    load()
    return true
  } catch (e) {
    fail(e)
    return false
  }
}

// ---- domains ----
const newDomain = ref('')
const dns = ref<{ domain: string; recs: DNSRec[]; checks: Check[] | null } | null>(null)
async function openDNS(d: string) {
  try {
    dns.value = { domain: d, recs: (await get<{ records: DNSRec[] }>(`/mail/domains/${d}/dns`)).records, checks: null }
  } catch (e) {
    fail(e)
  }
}
async function applyDNS() {
  try {
    const r = await post<{ added: number; zone: string }>(`/mail/domains/${dns.value!.domain}/dns`)
    msg.success(t('dns.applied', { n: r.added }))
  } catch (e) {
    fail(e)
  }
}
async function check(d: string) {
  if (!dns.value || dns.value.domain !== d) await openDNS(d)
  try {
    dns.value!.checks = (await get<{ items: Check[] }>(`/mail/domains/${d}/check`)).items
  } catch (e) {
    fail(e)
  }
}
const domCols = computed<DataTableColumns<Domain>>(() => [
  { title: () => t('mail.domain'), key: 'name', render: (d) => h('b', d.name) },
  { title: () => t('mail.mailboxes'), key: 'accounts', width: 100 },
  { title: 'DKIM', key: 'dkim', width: 90, render: (d) => h(NTag, { size: 'small', type: d.dkim ? 'success' : 'warning' }, () => (d.dkim ? '✓' : '—')) },
  { title: () => t('common.created'), key: 'created_at', width: 170, render: (d) => datetime(d.created_at, locale.value) },
  {
    title: '',
    key: 'a',
    width: 230,
    render: (d) =>
      h(NSpace, { size: 4 }, () => [
        h(NButton, { size: 'tiny', type: 'primary', onClick: () => openDNS(d.name) }, () => 'DNS'),
        h(NButton, { size: 'tiny', onClick: () => check(d.name) }, () => t('mail.check')),
        h(NPopconfirm, { onPositiveClick: () => call(`/mail/domains/${d.name}/delete`, {}) }, { trigger: () => h(NButton, { size: 'tiny', type: 'error', quaternary: true }, () => t('common.delete')), default: () => t('mail.domain_delete') }),
      ]),
  },
])

// ---- mailboxes ----
const genPass = () => Array.from(crypto.getRandomValues(new Uint8Array(14)), (b) => 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789'[b % 56]).join('')
const af = ref<{ edit: boolean; address: string; password: string; quota: string } | null>(null)
async function saveAccount() {
  const x = af.value!
  if (await call(x.edit ? '/mail/accounts/update' : '/mail/accounts', x, t('common.saved'))) af.value = null
}
const accCols = computed<DataTableColumns<Account>>(() => [
  { title: () => t('mail.address'), key: 'address', render: (a) => h('b', a.address) },
  { title: () => t('mail.quota'), key: 'quota', width: 180, render: (a) => a.quota || '—' },
  {
    title: '',
    key: 'x',
    width: 160,
    render: (a) =>
      h(NSpace, { size: 4 }, () => [
        h(NButton, { size: 'tiny', onClick: () => (af.value = { edit: true, address: a.address, password: '', quota: '' }) }, () => t('common.edit')),
        h(NPopconfirm, { onPositiveClick: () => call('/mail/accounts/delete', { address: a.address }) }, { trigger: () => h(NButton, { size: 'tiny', type: 'error', quaternary: true }, () => t('common.delete')), default: () => t('mail.account_delete') }),
      ]),
  },
])

// ---- aliases ----
const al = ref<{ from: string; to: string }>({ from: '', to: '' })
const aliasRows = computed(() => aliases.value.flatMap((a) => a.to.map((to) => ({ from: a.from, to }))))
const aliasCols = computed<DataTableColumns<{ from: string; to: string }>>(() => [
  { title: () => t('mail.alias_from'), key: 'from' },
  { title: () => t('mail.alias_to'), key: 'to' },
  {
    title: '',
    key: 'x',
    width: 90,
    render: (a) => h(NPopconfirm, { onPositiveClick: () => call('/mail/aliases/delete', a) }, { trigger: () => h(NButton, { size: 'tiny', type: 'error', quaternary: true }, () => t('common.delete')), default: () => t('common.confirm_delete') }),
  },
])
const copy = (s: string) => navigator.clipboard?.writeText(s).then(() => msg.success(t('common.copied')))
</script>

<template>
  <n-card v-if="st" size="small" style="margin-bottom: 16px">
    <n-space align="center" justify="space-between">
      <n-space align="center">
        <b>{{ t('menu.mail') }}</b>
        <n-tag size="small" :type="running ? 'success' : st.enabled ? 'error' : 'default'">{{ st.state || t('mail.off') }}</n-tag>
        <n-tag v-if="st.enabled" size="small" :type="st.has_cert ? 'success' : 'warning'">{{ st.has_cert ? 'TLS ✓' : t('mail.no_tls') }}</n-tag>
      </n-space>
      <n-space align="center">
        <n-input v-model:value="host" placeholder="mail.example.com" style="width: 220px" />
        <n-button type="primary" :disabled="!host" @click="apply(true)">{{ st.enabled ? t('mail.reapply') : t('mail.enable') }}</n-button>
        <n-popconfirm v-if="st.enabled" @positive-click="apply(false)">
          <template #trigger><n-button>{{ t('mail.disable') }}</n-button></template>
          {{ t('mail.disable_confirm') }}
        </n-popconfirm>
        <n-button v-if="st.enabled" quaternary @click="logs = { url: '/mail/logs', title: 'mail.log' }">{{ t('app.logs') }}</n-button>
      </n-space>
    </n-space>
    <p class="hint">{{ t('mail.intro', { ports: st.ports.join(', ') }) }}</p>
  </n-card>

  <n-tabs v-if="st?.enabled" type="line" animated>
    <n-tab-pane name="domains" :tab="t('mail.domains')">
      <n-space style="margin-bottom: 12px">
        <n-input v-model:value="newDomain" placeholder="example.com" style="width: 220px" />
        <n-button type="primary" :disabled="!running || !newDomain" @click="call('/mail/domains', { domain: newDomain }).then((ok) => ok && (newDomain = ''))">{{ t('mail.add_domain') }}</n-button>
      </n-space>
      <n-data-table size="small" :columns="domCols" :data="st.domains" :row-key="(d: Domain) => d.name" />
    </n-tab-pane>
    <n-tab-pane name="accounts" :tab="t('mail.mailboxes')">
      <n-space style="margin-bottom: 12px">
        <n-button type="primary" :disabled="!running" @click="af = { edit: false, address: '', password: genPass(), quota: '' }">{{ t('mail.add_account') }}</n-button>
      </n-space>
      <n-data-table size="small" :columns="accCols" :data="accounts" :row-key="(a: Account) => a.address" />
      <n-alert type="default" :show-icon="false" style="margin-top: 12px">{{ t('mail.client_hint', { host: st.hostname }) }}</n-alert>
    </n-tab-pane>
    <n-tab-pane name="aliases" :tab="t('mail.aliases')">
      <n-space style="margin-bottom: 12px">
        <n-input v-model:value="al.from" placeholder="info@example.com / @example.com" style="width: 240px" />
        <n-input v-model:value="al.to" placeholder="me@example.com" style="width: 220px" />
        <n-button type="primary" :disabled="!running || !al.from || !al.to" @click="call('/mail/aliases', al).then((ok) => ok && (al = { from: '', to: '' }))">{{ t('mail.add_alias') }}</n-button>
      </n-space>
      <n-data-table size="small" :columns="aliasCols" :data="aliasRows" />
    </n-tab-pane>
  </n-tabs>

  <n-modal :show="!!af" @update:show="(v: boolean) => !v && (af = null)">
    <n-card v-if="af" :title="af.edit ? af.address : t('mail.add_account')" style="width: min(480px, calc(100vw - 32px))" closable @close="af = null">
      <n-form label-placement="top">
        <n-form-item v-if="!af.edit" :label="t('mail.address')"><n-input v-model:value="af.address" placeholder="user@example.com" /></n-form-item>
        <n-form-item :label="t('ftp.password')" :feedback="af.edit ? t('users.keep_password') : ''">
          <n-input v-model:value="af.password"><template #suffix><n-button text size="tiny" @click="af.password = genPass()">↻</n-button></template></n-input>
        </n-form-item>
        <n-form-item :label="t('mail.quota')" :feedback="t('mail.quota_hint')"><n-input v-model:value="af.quota" placeholder="2G" /></n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end"><n-button @click="af = null">{{ t('common.cancel') }}</n-button><n-button type="primary" @click="saveAccount">{{ t('common.save') }}</n-button></n-space>
      </template>
    </n-card>
  </n-modal>

  <n-modal :show="!!dns" @update:show="(v: boolean) => !v && (dns = null)">
    <n-card v-if="dns" :title="`DNS · ${dns.domain}`" style="width: min(820px, calc(100vw - 32px))" closable @close="dns = null">
      <div v-for="(r, i) in dns.recs" :key="i" class="rec">
        <n-tag size="small">{{ r.type }}</n-tag>
        <b>{{ r.name }}</b>
        <code>{{ r.priority ? `${r.priority} ` : '' }}{{ r.content }}</code>
        <n-button text size="tiny" @click="copy(r.content)">{{ t('common.copy') }}</n-button>
      </div>
      <n-space style="margin-top: 12px">
        <n-button type="primary" @click="applyDNS">{{ t('mail.dns_apply') }}</n-button>
        <n-button @click="check(dns.domain)">{{ t('mail.check') }}</n-button>
      </n-space>
      <p class="hint">{{ t('mail.ptr_hint') }}</p>
      <template v-if="dns.checks">
        <div v-for="c in dns.checks" :key="c.key" class="rec">
          <n-tag size="small" :type="c.ok ? 'success' : c.level === 'error' ? 'error' : 'warning'">{{ c.ok ? '✓' : '✗' }}</n-tag>
          <b>{{ t(`mail.checks.${c.key}`) }}</b>
          <code>{{ c.detail }}</code>
        </div>
      </template>
    </n-card>
  </n-modal>
  <TaskModal :task-id="task?.id ?? null" :title="task?.title ?? ''" @close="task = null; load()" @done="load" />
  <LogsModal :url="logs?.url ?? null" :title="logs?.title ?? ''" @close="logs = null" />
</template>

<style scoped>
.hint {
  font-size: 12px;
  opacity: 0.65;
  margin: 10px 0 0;
}
.rec {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 4px 0;
  word-break: break-all;
}
.rec code {
  flex: 1;
  font-size: 12px;
}
</style>
