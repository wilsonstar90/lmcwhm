<script setup lang="ts">
import { computed, h, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NButton, NCard, NDataTable, NDrawer, NDrawerContent, NForm, NFormItem, NInput, NInputNumber, NModal, NPopconfirm, NRadioButton,
  NRadioGroup, NSpace, NSwitch, NTag, NTooltip, useMessage, type DataTableColumns,
} from 'naive-ui'
import { get, post, put, ApiError } from '../api/client'
import { datetime } from '../format'
import LineChart from '../components/LineChart.vue'

interface Probe {
  id: number; name: string; type: 'http' | 'tcp' | 'ping'; target: string; interval: number; timeout: number; keyword: string; expect_status: number; fails: number; enabled: boolean
  state: string; last_check: number; latency: number; last_error: string; cert_days: number; uptime_24h: number; uptime_7d: number; since: number
}
interface Result { t: number; ok: boolean; latency: number; error?: string }

const { t, locale } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))
const probes = ref<Probe[]>([])
let timer: number | undefined

async function load() {
  try {
    probes.value = (await get<{ items: Probe[] }>('/probes')).items
  } catch (e) {
    fail(e)
  }
}
onMounted(() => {
  load()
  timer = window.setInterval(load, 15000)
})
onBeforeUnmount(() => clearInterval(timer))

const f = ref<Probe | null>(null)
function openNew() {
  f.value = { id: 0, name: '', type: 'http', target: 'https://', interval: 60, timeout: 10, keyword: '', expect_status: 0, fails: 2, enabled: true } as Probe
}
async function save() {
  const p = f.value!
  try {
    if (p.id) await put(`/probes/${p.id}`, p)
    else await post('/probes', p)
    f.value = null
    load()
  } catch (e) {
    fail(e)
  }
}
async function checkNow(p: Probe) {
  try {
    const r = await post<Result>(`/probes/${p.id}/check`)
    if (r.ok) msg.success(`OK · ${r.latency} ms`)
    else msg.error(r.error ?? 'failed')
  } catch (e) {
    fail(e)
  }
  load()
}
async function del(p: Probe) {
  await post(`/probes/${p.id}/delete`).catch(fail)
  load()
}

// ---- history ----
const hist = ref<Probe | null>(null)
const range = ref('24h')
const results = ref<Result[]>([])
async function openHist(p: Probe) {
  hist.value = p
  loadHist()
}
async function loadHist() {
  results.value = (await get<{ items: Result[] }>(`/probes/${hist.value!.id}/results?range=${range.value}`)).items
}
const failures = computed(() => results.value.filter((r) => !r.ok).slice(-50).reverse())

const pct = (v: number) => (v < 0 ? '—' : `${v.toFixed(v === 100 ? 0 : 2)}%`)
const stateType = (s: string) => (s === 'up' ? 'success' : s === 'down' ? 'error' : 'default')
const cols = computed<DataTableColumns<Probe>>(() => [
  { title: () => t('common.status'), key: 'state', width: 90, render: (p) => h(NTag, { size: 'small', type: p.enabled ? stateType(p.state) : 'default' }, () => (p.enabled ? t(`probe.state.${p.state}`) : t('probe.paused'))) },
  { title: () => t('probe.name'), key: 'name', render: (p) => h('div', [h('b', p.name), h('div', { style: 'font-size:12px;opacity:.65' }, `${p.type.toUpperCase()} · ${p.target}`)]) },
  { title: () => t('probe.latency'), key: 'latency', width: 100, render: (p) => (p.last_check ? `${p.latency} ms` : '—') },
  { title: '24h', key: 'uptime_24h', width: 90, render: (p) => pct(p.uptime_24h) },
  { title: '7d', key: 'uptime_7d', width: 90, render: (p) => pct(p.uptime_7d) },
  { title: 'SSL', key: 'cert_days', width: 100, render: (p) => (p.cert_days >= 0 ? h(NTag, { size: 'small', type: p.cert_days < 7 ? 'error' : p.cert_days < 21 ? 'warning' : 'default' }, () => t('probe.days', { n: p.cert_days })) : '—') },
  {
    title: () => t('probe.last_error'), key: 'last_error', ellipsis: { tooltip: true },
    render: (p) => (p.last_error ? h(NTooltip, null, { trigger: () => h('span', { style: 'color:#d03050' }, p.last_error), default: () => (p.last_check ? datetime(p.last_check, locale.value) : '') }) : ''),
  },
  {
    title: () => t('common.actions'),
    key: 'a',
    width: 250,
    render: (p) =>
      h(NSpace, { size: 4 }, () => [
        h(NButton, { size: 'tiny', onClick: () => checkNow(p) }, () => t('probe.check')),
        h(NButton, { size: 'tiny', onClick: () => openHist(p) }, () => t('probe.history')),
        h(NButton, { size: 'tiny', onClick: () => (f.value = { ...p }) }, () => t('common.edit')),
        h(NPopconfirm, { onPositiveClick: () => del(p) }, { trigger: () => h(NButton, { size: 'tiny', type: 'error', quaternary: true }, () => t('common.delete')), default: () => t('common.confirm_delete') }),
      ]),
  },
])
</script>

<template>
  <n-space justify="space-between" style="margin-bottom: 12px">
    <n-button type="primary" @click="openNew">{{ t('probe.new') }}</n-button>
    <span class="hint">{{ t('probe.alert_hint') }}</span>
  </n-space>
  <n-data-table size="small" :columns="cols" :data="probes" :row-key="(p: Probe) => p.id" :scroll-x="1100" />

  <n-modal :show="!!f" @update:show="(v: boolean) => !v && (f = null)">
    <n-card v-if="f" :title="f.id ? t('common.edit') : t('probe.new')" style="width: min(560px, calc(100vw - 32px))" closable @close="f = null">
      <n-form label-placement="top">
        <n-form-item :label="t('probe.type')">
          <n-radio-group v-model:value="f.type" @update:value="(v: string) => (f!.target = v === 'http' ? 'https://' : '')">
            <n-radio-button value="http">HTTP(S)</n-radio-button><n-radio-button value="tcp">TCP</n-radio-button><n-radio-button value="ping">Ping</n-radio-button>
          </n-radio-group>
        </n-form-item>
        <n-form-item :label="t('probe.name')"><n-input v-model:value="f.name" /></n-form-item>
        <n-form-item :label="t('probe.target')" :feedback="t(`probe.target_hint.${f.type}`)"><n-input v-model:value="f.target" /></n-form-item>
        <template v-if="f.type === 'http'">
          <n-form-item :label="t('probe.keyword')" :feedback="t('probe.keyword_hint')"><n-input v-model:value="f.keyword" /></n-form-item>
          <n-form-item :label="t('probe.expect')" :feedback="t('probe.expect_hint')"><n-input-number v-model:value="f.expect_status" :min="0" :max="599" style="width: 120px" /></n-form-item>
        </template>
        <n-space>
          <n-form-item :label="t('probe.interval')"><n-input-number v-model:value="f.interval" :min="30" :max="86400" style="width: 130px"><template #suffix>s</template></n-input-number></n-form-item>
          <n-form-item :label="t('probe.timeout')"><n-input-number v-model:value="f.timeout" :min="1" :max="60" style="width: 110px"><template #suffix>s</template></n-input-number></n-form-item>
          <n-form-item :label="t('probe.fails')"><n-input-number v-model:value="f.fails" :min="1" :max="10" style="width: 100px" /></n-form-item>
          <n-form-item :label="t('alert.enabled')"><n-switch v-model:value="f.enabled" /></n-form-item>
        </n-space>
      </n-form>
      <template #footer>
        <n-space justify="end"><n-button @click="f = null">{{ t('common.cancel') }}</n-button><n-button type="primary" :disabled="!f.name || !f.target" @click="save">{{ t('common.save') }}</n-button></n-space>
      </template>
    </n-card>
  </n-modal>

  <n-drawer :show="!!hist" :width="760" @update:show="(v: boolean) => !v && (hist = null)">
    <n-drawer-content v-if="hist" :title="`${hist.name} · ${hist.target}`" closable>
      <n-radio-group v-model:value="range" size="small" style="margin-bottom: 12px" @update:value="loadHist">
        <n-radio-button v-for="r in ['1h', '24h', '7d', '30d']" :key="r" :value="r">{{ t(`monitor.ranges.${r}`) }}</n-radio-button>
      </n-radio-group>
      <div class="strip" :aria-label="t('probe.availability')">
        <span v-for="(r, i) in results" :key="i" :class="['cell', r.ok ? 'ok' : 'bad']" :title="`${datetime(r.t, locale)} · ${r.ok ? r.latency + ' ms' : r.error}`" />
      </div>
      <n-card size="small" :title="t('probe.latency')" style="margin-top: 12px">
        <LineChart :times="results.map((r) => r.t)" :series="[{ name: t('probe.latency'), values: results.map((r) => (r.ok ? r.latency : NaN)) }]" :format="(v: number) => `${Math.round(v)} ms`" />
      </n-card>
      <n-card size="small" :title="t('probe.failures')" style="margin-top: 12px">
        <p v-if="!failures.length" class="hint">{{ t('probe.no_failures') }}</p>
        <div v-for="(r, i) in failures" :key="i" class="fail"><span>{{ datetime(r.t, locale) }}</span> {{ r.error }}</div>
      </n-card>
    </n-drawer-content>
  </n-drawer>
</template>

<style scoped>
.hint {
  font-size: 12px;
  opacity: 0.65;
}
.strip {
  display: flex;
  gap: 1px;
  height: 28px;
}
.cell {
  flex: 1;
  border-radius: 2px;
  min-width: 1px;
}
.cell.ok {
  background: #18a058;
}
.cell.bad {
  background: #d03050;
}
.fail {
  font-size: 12px;
  font-family: monospace;
  padding: 2px 0;
}
.fail span {
  opacity: 0.6;
  margin-right: 8px;
}
</style>
