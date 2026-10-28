<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NAlert, NButton, NCard, NCheckbox, NDataTable, NInput, NSelect, NSpace, NTag, useMessage, type DataTableColumns } from 'naive-ui'
import { get, post, put, ApiError } from '../api/client'
import type { FirewallView } from '../api/types'

interface LinodeFW { id: number; label: string; status: string; entities: { id: number; type: string; label: string }[]; rules: { inbound: { label: string }[]; inbound_policy: string } }
interface Zone { id: string; name: string; level: string; blocks: number }

const { t } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))
const fw = ref<FirewallView | null>(null)
const token = ref('')
const firewalls = ref<LinodeFW[]>([])
const instance = ref<{ id: number; label: string } | null>(null)
const linodeErr = ref('')
const verifying = ref(false)
const zones = ref<Zone[]>([])
const cfErr = ref('')
const cfLoading = ref(false)

async function load() {
  fw.value = await get<FirewallView>('/firewall')
  if (fw.value.has_linode_token) verify()
  if (fw.value.has_cf_token) loadZones()
}
onMounted(() => load().catch(fail))
defineExpose({ load })

async function saveCfg(patch: Partial<FirewallView> & { linode_token?: string; clear_linode_token?: boolean }) {
  const { linode_token: _, ...cfg } = fw.value!
  try {
    await put('/firewall', { ...cfg, ...patch })
    msg.success(t('common.saved'))
    fw.value = await get<FirewallView>('/firewall')
  } catch (e) {
    fail(e)
  }
}

async function verify() {
  verifying.value = true
  linodeErr.value = ''
  try {
    const r = await post<{ ok: boolean; error?: string; firewalls?: LinodeFW[]; linode?: { id: number; label: string }; linode_error?: string }>('/firewall/linode/verify', { token: token.value })
    if (!r.ok) {
      linodeErr.value = r.error ?? ''
      return
    }
    firewalls.value = r.firewalls ?? []
    instance.value = r.linode ?? null
    if (r.linode_error) linodeErr.value = r.linode_error
    if (token.value) {
      await saveCfg({ linode_token: token.value })
      token.value = ''
    }
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
const levels = computed(() => ['essentially_off', 'low', 'medium', 'high', 'under_attack'].map((l) => ({ label: t(`fw.cf_levels.${l}`), value: l })))
const zoneCols: DataTableColumns<Zone> = [
  { title: () => t('fw.zone'), key: 'name', render: (z) => h('b', z.name) },
  { title: () => t('fw.cf_level'), key: 'level', width: 230, render: (z) => h(NSelect, { size: 'small', value: z.level, options: levels.value, 'onUpdate:value': (l: string) => setLevel(z, l) }) },
  { title: () => t('fw.cf_blocks'), key: 'blocks', width: 140, render: (z) => h(NTag, { size: 'small' }, () => z.blocks) },
]
</script>

<template>
  <n-card size="small" title="Linode Cloud Firewall">
    <p class="hint" style="margin-top: 0">{{ t('fw.linode_intro') }}</p>
    <n-space align="center">
      <n-input v-model:value="token" type="password" show-password-on="click" :placeholder="fw?.has_linode_token ? t('fw.token_saved') : t('fw.linode_token')" style="width: 320px" />
      <n-button :loading="verifying" :disabled="!token && !fw?.has_linode_token" @click="verify">{{ t('fw.verify') }}</n-button>
      <n-button v-if="fw?.has_linode_token" quaternary type="error" @click="saveCfg({ clear_linode_token: true }); firewalls = []">{{ t('fw.forget') }}</n-button>
    </n-space>
    <n-alert v-if="linodeErr" type="warning" :show-icon="false" style="margin-top: 12px">{{ linodeErr }}</n-alert>
    <template v-if="firewalls.length && fw">
      <n-space align="center" style="margin-top: 12px">
        <n-select :value="fw.linode_firewall || null" :options="fwOptions" :placeholder="t('fw.choose_fw')" style="width: 380px" @update:value="(id: number) => saveCfg({ linode_firewall: id })" />
        <n-tag v-if="attached" type="success" size="small">{{ t('fw.attached', { name: instance?.label }) }}</n-tag>
        <n-button v-else-if="chosen && instance" size="small" @click="attach">{{ t('fw.attach', { name: instance.label }) }}</n-button>
      </n-space>
      <n-space vertical style="margin-top: 12px">
        <n-checkbox :checked="fw.linode_sync" :disabled="!fw.linode_firewall" @update:checked="(v: boolean) => saveCfg({ linode_sync: v })">{{ t('fw.linode_sync') }}</n-checkbox>
        <n-checkbox :checked="fw.linode_ports" :disabled="!fw.linode_firewall" @update:checked="(v: boolean) => saveCfg({ linode_ports: v })">{{ t('fw.linode_ports', { ports: fw.protected_ports.join(', ') }) }}</n-checkbox>
      </n-space>
      <p v-if="chosen" class="hint">{{ t('fw.linode_rules', { n: chosen.rules.inbound.length, policy: chosen.rules.inbound_policy }) }}</p>
    </template>
  </n-card>

  <n-card size="small" title="Cloudflare" style="margin-top: 16px">
    <n-alert v-if="fw && !fw.has_cf_token" type="info" :show-icon="false">{{ t('fw.cf_no_token') }}</n-alert>
    <template v-else-if="fw">
      <n-checkbox :checked="fw.cf_sync" @update:checked="(v: boolean) => saveCfg({ cf_sync: v })">{{ t('fw.cf_sync') }}</n-checkbox>
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
</style>
