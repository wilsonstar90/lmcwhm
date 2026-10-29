<script setup lang="ts">
// DNS Zone Editor over Cloudflare: every record type and one-click templates.
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NCard, NDataTable, NForm, NFormItem, NInput, NInputNumber, NModal, NPopconfirm, NSelect, NSpace, NSwitch, NTag, useMessage,
  type DataTableColumns,
} from 'naive-ui'
import { get, post, put, ApiError } from '../api/client'

interface Zone { id: string; name: string }
interface Rec { id?: string; type: string; name: string; content?: string; ttl: number; proxied: boolean; priority?: number; data?: Record<string, string | number>; comment?: string; proxiable?: boolean }
interface Tpl { key: string; name: string; params?: string[]; records: Rec[] }

const { t } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))
const zones = ref<Zone[]>([])
const types = ref<string[]>([])
const zone = ref<string | null>(null)
const recs = ref<Rec[]>([])
const loading = ref(false)
const filter = ref('')
const typeFilter = ref<string | null>(null)
const noToken = ref(false)
const templates = ref<Tpl[]>([])

onMounted(async () => {
  try {
    const r = await get<{ items: Zone[]; types: string[] }>('/dns/zones')
    zones.value = r.items
    types.value = r.types
    if (r.items.length) pickZone(r.items[0].id)
    templates.value = (await get<{ items: Tpl[] }>('/dns/templates')).items
  } catch (e) {
    if (e instanceof ApiError && e.code === 'cf.no_token') noToken.value = true
    else fail(e)
  }
})
const zoneName = computed(() => zones.value.find((z) => z.id === zone.value)?.name ?? '')
async function pickZone(id: string) {
  zone.value = id
  loading.value = true
  try {
    recs.value = (await get<{ items: Rec[] }>(`/dns/zones/${id}/records`)).items
  } catch (e) {
    fail(e)
  } finally {
    loading.value = false
  }
}
const rows = computed(() => {
  const q = filter.value.trim().toLowerCase()
  return recs.value
    .filter((r) => (!typeFilter.value || r.type === typeFilter.value) && (!q || r.name.includes(q) || (r.content ?? '').toLowerCase().includes(q)))
    .sort((a, b) => a.name.localeCompare(b.name) || a.type.localeCompare(b.type))
})
const value = (r: Rec) => {
  if (r.type === 'SRV' && r.data) return `${r.data.priority} ${r.data.weight} ${r.data.port} ${r.data.target}`
  if (r.type === 'CAA' && r.data) return `${r.data.flags} ${r.data.tag} "${r.data.value}"`
  if (r.type === 'MX') return `${r.priority} ${r.content}`
  return r.content ?? ''
}
const short = (n: string) => (n === zoneName.value ? '@' : n.endsWith('.' + zoneName.value) ? n.slice(0, -zoneName.value.length - 1) : n)

// ---- edit ----
const f = ref<Rec | null>(null)
function openNew() {
  f.value = { type: 'A', name: '', content: '', ttl: 1, proxied: false, priority: 10, data: {} }
}
function edit(r: Rec) {
  f.value = { ...JSON.parse(JSON.stringify(r)), name: short(r.name) === '@' ? '' : short(r.name) }
  if (!f.value!.data) f.value!.data = {}
}
function onType(v: string) {
  const x = f.value!
  x.type = v
  if (v === 'SRV') x.data = { priority: 10, weight: 5, port: 443, target: '' }
  if (v === 'CAA') x.data = { flags: 0, tag: 'issue', value: 'letsencrypt.org' }
}
async function save() {
  const x = { ...f.value! }
  x.name = x.name.trim() ? `${x.name.trim().replace(/\.$/, '')}${x.name.endsWith(zoneName.value) ? '' : '.' + zoneName.value}` : zoneName.value
  try {
    if (x.id) await put(`/dns/zones/${zone.value}/records/${x.id}`, x)
    else await post(`/dns/zones/${zone.value}/records`, x)
    msg.success(t('common.saved'))
    f.value = null
    pickZone(zone.value!)
  } catch (e) {
    fail(e)
  }
}
async function del(r: Rec) {
  try {
    await post(`/dns/zones/${zone.value}/records/${r.id}/delete`)
    pickZone(zone.value!)
  } catch (e) {
    fail(e)
  }
}
const proxiable = (ty: string) => ['A', 'AAAA', 'CNAME'].includes(ty)
const cols = computed<DataTableColumns<Rec>>(() => [
  { title: () => t('dns.type'), key: 'type', width: 80, render: (r) => h(NTag, { size: 'small' }, () => r.type) },
  { title: () => t('dns.name'), key: 'name', width: 220, ellipsis: { tooltip: true }, render: (r) => short(r.name) },
  { title: () => t('dns.value'), key: 'content', ellipsis: { tooltip: true }, render: (r) => h('code', value(r)) },
  { title: 'TTL', key: 'ttl', width: 80, render: (r) => (r.ttl === 1 ? t('dns.auto') : r.ttl) },
  { title: () => t('dns.proxy'), key: 'proxied', width: 80, render: (r) => (proxiable(r.type) ? h(NTag, { size: 'small', type: r.proxied ? 'warning' : 'default' }, () => (r.proxied ? '☁' : t('dns.dns_only'))) : '') },
  {
    title: '',
    key: 'a',
    width: 140,
    render: (r) =>
      h(NSpace, { size: 4 }, () => [
        h(NButton, { size: 'tiny', onClick: () => edit(r) }, () => t('common.edit')),
        h(NPopconfirm, { onPositiveClick: () => del(r) }, { trigger: () => h(NButton, { size: 'tiny', type: 'error', quaternary: true }, () => t('common.delete')), default: () => t('common.confirm_delete') }),
      ]),
  },
])

// ---- templates ----
const tp = ref<{ key: string; domain: string; params: Record<string, string>; preview: Rec[] } | null>(null)
const tpl = computed(() => templates.value.find((x) => x.key === tp.value?.key))
function openTpl() {
  tp.value = { key: templates.value[0]?.key ?? '', domain: zoneName.value, params: { mailhost: `mail.${zoneName.value}` }, preview: [] }
  previewTpl()
}
async function previewTpl() {
  const x = tp.value!
  try {
    x.preview = (await post<{ records: Rec[] }>(`/dns/zones/${zone.value}/template`, { template: x.key, domain: x.domain, params: x.params, preview: true })).records
  } catch (e) {
    fail(e)
  }
}
async function applyTpl() {
  const x = tp.value!
  try {
    const r = await post<{ added: number }>(`/dns/zones/${zone.value}/template`, { template: x.key, domain: x.domain, params: x.params })
    msg.success(t('dns.applied', { n: r.added }))
    tp.value = null
    pickZone(zone.value!)
  } catch (e) {
    fail(e)
  }
}
</script>

<template>
  <n-alert v-if="noToken" type="warning" :show-icon="false">{{ t('dns.no_token') }}</n-alert>
  <template v-else>
    <n-space justify="space-between" style="margin-bottom: 12px">
      <n-space>
        <n-select :value="zone" :options="zones.map((z) => ({ label: z.name, value: z.id }))" filterable style="width: 220px" @update:value="pickZone" />
        <n-button type="primary" :disabled="!zone" @click="openNew">{{ t('dns.add') }}</n-button>
        <n-button :disabled="!zone" @click="openTpl">{{ t('dns.templates') }}</n-button>
      </n-space>
      <n-space>
        <n-select v-model:value="typeFilter" clearable :options="types.map((x) => ({ label: x, value: x }))" :placeholder="t('dns.type')" style="width: 110px" />
        <n-input v-model:value="filter" clearable :placeholder="t('tb.search')" style="width: 200px" />
      </n-space>
    </n-space>
    <n-data-table size="small" :columns="cols" :data="rows" :loading="loading" :row-key="(r: Rec) => r.id!" :scroll-x="860" />
  </template>

  <n-modal :show="!!f" @update:show="(v: boolean) => !v && (f = null)">
    <n-card v-if="f" :title="f.id ? t('common.edit') : t('dns.add')" style="width: min(560px, calc(100vw - 32px))" closable @close="f = null">
      <n-form label-placement="top">
        <n-form-item :label="t('dns.type')"><n-select :value="f.type" :options="types.map((x) => ({ label: x, value: x }))" :disabled="!!f.id" @update:value="onType" /></n-form-item>
        <n-form-item :label="t('dns.name')" :feedback="t('dns.name_hint', { zone: zoneName })"><n-input v-model:value="f.name" placeholder="www">
          <template #suffix><span class="sub">.{{ zoneName }}</span></template></n-input></n-form-item>
        <template v-if="f.type === 'SRV' && f.data">
          <n-space>
            <n-form-item :label="t('dns.priority')"><n-input-number v-model:value="(f.data.priority as number)" :min="0" :max="65535" style="width: 110px" /></n-form-item>
            <n-form-item :label="t('dns.weight')"><n-input-number v-model:value="(f.data.weight as number)" :min="0" :max="65535" style="width: 110px" /></n-form-item>
            <n-form-item :label="t('dns.port')"><n-input-number v-model:value="(f.data.port as number)" :min="0" :max="65535" style="width: 110px" /></n-form-item>
          </n-space>
          <n-form-item :label="t('dns.target')"><n-input v-model:value="(f.data.target as string)" placeholder="sip.example.com" /></n-form-item>
        </template>
        <template v-else-if="f.type === 'CAA' && f.data">
          <n-space>
            <n-form-item label="Flags"><n-input-number v-model:value="(f.data.flags as number)" :min="0" :max="255" style="width: 90px" /></n-form-item>
            <n-form-item label="Tag"><n-select v-model:value="(f.data.tag as string)" :options="['issue', 'issuewild', 'iodef'].map((x) => ({ label: x, value: x }))" style="width: 130px" /></n-form-item>
          </n-space>
          <n-form-item :label="t('dns.value')"><n-input v-model:value="(f.data.value as string)" placeholder="letsencrypt.org" /></n-form-item>
        </template>
        <template v-else>
          <n-form-item v-if="f.type === 'MX'" :label="t('dns.priority')"><n-input-number v-model:value="f.priority" :min="0" :max="65535" /></n-form-item>
          <n-form-item :label="t('dns.value')"><n-input v-model:value="f.content" :type="f.type === 'TXT' ? 'textarea' : 'text'" :placeholder="t(`dns.ph.${f.type}`)" /></n-form-item>
        </template>
        <n-space align="center" :size="24">
          <n-form-item label="TTL"><n-select v-model:value="f.ttl" :options="[1, 60, 300, 3600, 86400].map((v) => ({ label: v === 1 ? t('dns.auto') : `${v}s`, value: v }))" style="width: 120px" /></n-form-item>
          <n-space v-if="proxiable(f.type)" align="center"><n-switch v-model:value="f.proxied" /> {{ t('dns.proxy') }}</n-space>
        </n-space>
      </n-form>
      <template #footer>
        <n-space justify="end"><n-button @click="f = null">{{ t('common.cancel') }}</n-button><n-button type="primary" @click="save">{{ t('common.save') }}</n-button></n-space>
      </template>
    </n-card>
  </n-modal>

  <n-modal :show="!!tp" @update:show="(v: boolean) => !v && (tp = null)">
    <n-card v-if="tp" :title="t('dns.templates')" style="width: min(720px, calc(100vw - 32px))" closable @close="tp = null">
      <n-form label-placement="left" label-width="140">
        <n-form-item :label="t('dns.template')"><n-select v-model:value="tp.key" :options="templates.map((x) => ({ label: x.name, value: x.key }))" @update:value="previewTpl" /></n-form-item>
        <n-form-item :label="t('dns.domain')"><n-input v-model:value="tp.domain" @blur="previewTpl" /></n-form-item>
        <n-form-item v-for="p in tpl?.params ?? []" :key="p" :label="t(`dns.param.${p}`)"><n-input v-model:value="tp.params[p]" @blur="previewTpl" /></n-form-item>
      </n-form>
      <div v-for="(r, i) in tp.preview" :key="i" class="pv"><n-tag size="small">{{ r.type }}</n-tag> <b>{{ short(r.name) }}</b> <code>{{ value(r) }}</code></div>
      <p class="sub">{{ t('dns.template_hint') }}</p>
      <template #footer>
        <n-space justify="end"><n-button @click="tp = null">{{ t('common.cancel') }}</n-button><n-button type="primary" :disabled="!tp.preview.length" @click="applyTpl">{{ t('dns.apply') }}</n-button></n-space>
      </template>
    </n-card>
  </n-modal>
</template>

<style scoped>
.sub {
  font-size: 12px;
  opacity: 0.65;
}
.pv {
  padding: 3px 0;
  word-break: break-all;
}
</style>
