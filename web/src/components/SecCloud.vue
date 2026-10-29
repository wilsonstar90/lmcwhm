<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { NAlert, NButton, NCard, NCheckbox, NDataTable, NSelect, NSpace, NTag, useMessage, type DataTableColumns } from 'naive-ui'
import { fwConfig } from '../api/firewall'
import { get, post, put, ApiError } from '../api/client'
import type { FirewallView, SyncReport } from '../api/types'
import { datetime } from '../format'

// Pushes the manually added block / allow entries (permanent ones) to the
// Linode Cloud Firewall and Cloudflare. API tokens live in Panel settings.
interface LinodeFW { id: number; label: string; status: string; entities: { id: number; type: string; label: string }[]; rules: { inbound: { label: string }[]; inbound_policy: string } }
interface Zone { id: string; name: string; level: string; blocks: number }

const emit = defineEmits<{ changed: [] }>()
const { t, locale } = useI18n()
const router = useRouter()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))
const fw = ref<FirewallView | null>(null)
const firewalls = ref<LinodeFW[]>([])
const instance = ref<{ id: number; label: string } | null>(null)
const linodeErr = ref('')
const verifying = ref(false)
const zones = ref<Zone[]>([])
const cfErr = ref('')
const cfLoading = ref(false)
const syncing = ref(false)
const report = ref<SyncReport | null>(null)
const toSettings = () => router.push({ name: 'settings', query: { tab: 'api' } })

async function load() {
  fw.value = await get<FirewallView>('/firewall')
  report.value = fw.value.last_sync.at ? fw.value.last_sync : null
  if (fw.value.has_linode_token) verify()
  if (fw.value.has_cf_token) loadZones()
}
onMounted(() => load().catch(fail))

async function saveCfg(patch: Partial<FirewallView>) {
  try {
    await put('/firewall', { ...fwConfig(fw.value!), ...patch })
    msg.success(t('common.saved'))
    fw.value = await get<FirewallView>('/firewall')
    emit('changed')
  } catch (e) {
    fail(e)
  }
}

async function verify() {
  verifying.value = true
  linodeErr.value = ''
  try {
    const r = await post<{ ok: boolean; error?: string; firewalls?: LinodeFW[]; linode?: { id: number; label: string }; linode_error?: string }>('/firewall/linode/verify', { token: '' })
    if (!r.ok) {
      linodeErr.value = r.error ?? ''
      return
    }
    firewalls.value = r.firewalls ?? []
    instance.value = r.linode ?? null
    if (r.linode_error) linodeErr.value = r.linode_error
  } catch (e) {
    fail(e)
  } finally {
    verifying.value = false
  }
}
async function attach() {
  const r = await post<{ ok: boolean; error?: string }>('/firewall/linode/attach', { firewall: fw.value!.linode_firewall, linode: instance.value!.id })
  if (r.ok) {
    msg.success(t('common.done'))
    verify()
  } else msg.error(r.error ?? '')
}
const fwOptions = computed(() => firewalls.value.map((f) => ({ label: `${f.label} (#${f.id}, ${f.status}, ${f.rules.inbound_policy})`, value: f.id })))
const chosen = computed(() => firewalls.value.find((f) => f.id === fw.value?.linode_firewall))
const attached = computed(() => !!chosen.value && !!instance.value && chosen.value.entities.some((e) => e.id === instance.value!.id))

async function loadZones() {
  cfLoading.value = true
  try {
    const r = await get<{ items: Zone[]; error?: string }>('/firewall/cloudflare')
    zones.value = r.items
    cfErr.value = r.error ?? ''
  } finally {
    cfLoading.value = false
  }
}
async function setLevel(z: Zone, level: string) {
  const r = await post<{ ok: boolean; error?: string }>('/firewall/cloudflare/level', { zone: z.id, level })
  if (r.ok) z.level = level
  else msg.error(r.error ?? '')
}
async function sync() {
  syncing.value = true
  try {
    report.value = await post<SyncReport>('/firewall/sync')
    emit('changed')
  } catch (e) {
    fail(e)
  } finally {
    syncing.value = false
  }
}
const levels = computed(() => ['essentially_off', 'low', 'medium', 'high', 'under_attack'].map((l) => ({ label: t(`fw.cf_levels.${l}`), value: l })))
const zoneCols: DataTableColumns<Zone> = [
  { title: () => t('fw.zone'), key: 'name', render: (z) => h('b', z.name) },
  { title: () => t('fw.cf_level'), key: 'level', width: 230, render: (z) => h(NSelect, { size: 'small', value: z.level, options: levels.value, 'onUpdate:value': (l: string) => setLevel(z, l) }) },
  { title: () => t('fw.cf_rules'), key: 'blocks', width: 140, render: (z) => h(NTag, { size: 'small' }, () => z.blocks) },
]
</script>

<template>
  <n-alert type="info" :show-icon="false" style="margin-bottom: 16px">{{ t('fw.sync_scope') }}</n-alert>

  <n-card size="small" :title="t('fw.sync')">
    <template #header-extra>
      <n-button size="small" type="primary" :loading="syncing" :disabled="!fw?.cf_sync && !fw?.linode_sync && !fw?.linode_ports" @click="sync">{{ t('fw.sync_now') }}</n-button>
    </template>
    <template v-if="report">
      <div class="hint" style="margin-top: 0">{{ t('fw.last_sync', { at: datetime(report.at, locale) }) }}</div>
      <div v-if="report.linode" class="line">Linode: {{ report.linode }}</div>
      <div v-for="l in report.cloudflare ?? []" :key="l" class="line">Cloudflare · {{ l }}</div>
      <div v-for="e in report.errors ?? []" :key="e" class="line err">{{ e }}</div>
    </template>
    <p v-else class="hint" style="margin-top: 0">{{ t('fw.never_synced') }}</p>
  </n-card>

  <n-card size="small" title="Linode Cloud Firewall" style="margin-top: 16px">
    <template #header-extra>
      <n-tag size="small" :type="fw?.has_linode_token ? 'success' : 'default'">{{ fw?.has_linode_token ? t('apis.set') : t('apis.not_set') }}</n-tag>
    </template>
    <p class="hint" style="margin-top: 0">{{ t('fw.linode_intro') }}</p>
    <n-space v-if="fw && !fw.has_linode_token" align="center" style="margin-top: 10px">
      <span>{{ t('apis.need_token', { name: 'Linode' }) }}</span>
      <n-button size="small" @click="toSettings">{{ t('apis.open') }} →</n-button>
    </n-space>
    <template v-else-if="fw">
      <n-alert v-if="linodeErr" type="warning" :show-icon="false" style="margin-top: 12px">{{ linodeErr }}</n-alert>
      <n-space align="center" style="margin-top: 12px">
        <n-select :value="fw.linode_firewall || null" :loading="verifying" :options="fwOptions" :placeholder="t('fw.choose_fw')" style="width: 380px" @update:value="(id: number) => saveCfg({ linode_firewall: id })" />
        <n-tag v-if="attached" type="success" size="small">{{ t('fw.attached', { name: instance?.label }) }}</n-tag>
        <n-button v-else-if="chosen && instance" size="small" @click="attach">{{ t('fw.attach', { name: instance.label }) }}</n-button>
      </n-space>
      <n-space vertical style="margin-top: 12px">
        <n-checkbox :checked="fw.linode_sync" :disabled="!fw.linode_firewall" @update:checked="(v: boolean) => saveCfg({ linode_sync: v })">{{ t('fw.linode_sync2') }}</n-checkbox>
        <n-checkbox :checked="fw.linode_ports" :disabled="!fw.linode_firewall" @update:checked="(v: boolean) => saveCfg({ linode_ports: v })">{{ t('fw.linode_ports', { ports: fw.protected_ports.join(', ') }) }}</n-checkbox>
      </n-space>
      <p v-if="chosen" class="hint">{{ t('fw.linode_rules', { n: chosen.rules.inbound.length, policy: chosen.rules.inbound_policy }) }}</p>
    </template>
  </n-card>

  <n-card size="small" title="Cloudflare" style="margin-top: 16px">
    <template #header-extra>
      <n-tag size="small" :type="fw?.has_cf_token ? 'success' : 'default'">{{ fw?.has_cf_token ? t('apis.set') : t('apis.not_set') }}</n-tag>
    </template>
    <n-space v-if="fw && !fw.has_cf_token" align="center">
      <span>{{ t('apis.need_token', { name: 'Cloudflare' }) }}</span>
      <n-button size="small" @click="toSettings">{{ t('apis.open') }} →</n-button>
    </n-space>
    <template v-else-if="fw">
      <n-checkbox :checked="fw.cf_sync" @update:checked="(v: boolean) => saveCfg({ cf_sync: v })">{{ t('fw.cf_sync2') }}</n-checkbox>
      <p class="hint">{{ t('fw.cf_hint') }}</p>
      <n-alert v-if="cfErr" type="warning" :show-icon="false" style="margin: 8px 0">{{ cfErr }}</n-alert>
      <n-data-table size="small" :columns="zoneCols" :data="zones" :loading="cfLoading" :row-key="(z: Zone) => z.id" style="margin-top: 8px" />
    </template>
  </n-card>
</template>

<style scoped>
.hint {
  font-size: 12px;
  opacity: 0.65;
  margin: 8px 0 0;
}
.line {
  font-size: 13px;
  margin-top: 4px;
}
.err {
  color: #d03050;
}
</style>
