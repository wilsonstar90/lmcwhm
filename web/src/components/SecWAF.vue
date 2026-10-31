<script setup lang="ts">
// WAF: OWASP CRS (ModSecurity) and the dedicated rules at the edge in one
// place: settings, the dedicated rules, the CRS rule list, every blocked or
// detected request, and the error pages refusals answer with.
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { NAlert, NButton, NCard, NInputNumber, NRadioButton, NRadioGroup, NSelect, NSwitch, NTabPane, NTabs, useMessage } from 'naive-ui'
import { get, put, ApiError } from '../api/client'
import TaskModal from './TaskModal.vue'
import SecRules from './SecRules.vue'
import SecCRS from './SecCRS.vue'
import SecEvents from './SecEvents.vue'
import SecErrPages from './SecErrPages.vue'

interface Settings { enabled: boolean; engine: 'On' | 'DetectionOnly'; paranoia: number; anomaly: number; disabled: string[] | null }
interface ProtSettings { rules_on: boolean; block_status: number; crawlers: string[]; idc: unknown[] }
interface Rule { enabled: boolean; level: number }

const emit = defineEmits<{ changed: [] }>()
const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))

const tabs = ['settings', 'rules', 'crs', 'events', 'pages'] as const
type Tab = (typeof tabs)[number]
const tab = ref<Tab>(tabs.includes(route.query.sub as Tab) ? (route.query.sub as Tab) : 'settings')
watch(tab, (v) => router.replace({ query: { ...route.query, sub: v } }))

const s = ref<Settings | null>(null)
const ps = ref<ProtSettings | null>(null)
const image = ref('')
const rules = ref<Rule[]>([])
const task = ref<{ id: string; title: string } | null>(null)
const focusRule = ref('')

async function load() {
  try {
    const [w, p, r] = await Promise.all([
      get<{ settings: Settings; image: string }>('/waf'),
      get<{ settings: ProtSettings }>('/protect/settings'),
      get<{ rules: Rule[] }>('/protect/rules'),
    ])
    s.value = w.settings
    image.value = w.image
    ps.value = p.settings
    rules.value = r.rules
  } catch (e) {
    fail(e)
  }
}
onMounted(load)
defineExpose({ load })

async function save() {
  try {
    await put('/protect/settings', ps.value)
    const r = await put<{ task_id: string }>('/waf', s.value)
    task.value = { id: r.task_id, title: t('waf.apply') }
    emit('changed')
  } catch (e) {
    fail(e)
  }
}
const activeRules = computed(() => rules.value.filter((r) => r.enabled && r.level <= (s.value?.paranoia ?? 1)).length)
const statusOptions = [400, 401, 403, 404, 405, 406, 410, 418, 429, 444, 451, 500, 502, 503].map((c) => ({ label: c === 444 ? '444 (close)' : String(c), value: c }))
function openRule(id: string) {
  focusRule.value = id
  tab.value = 'crs'
}
</script>

<template>
  <n-tabs v-model:value="tab" type="line" animated>
    <n-tab-pane name="settings" :tab="t('waf.tabs.settings')">
      <n-card v-if="s && ps" size="small">
        <div class="rows">
          <div class="row">
            <div class="label"><b>{{ t('waf.enable') }}</b><span>{{ t('waf.enable_hint', { image }) }}</span></div>
            <n-switch v-model:value="s.enabled" />
          </div>
          <div class="row">
            <div class="label"><b>{{ t('waf.engine') }}</b><span>{{ t('waf.engine_hint') }}</span></div>
            <n-radio-group v-model:value="s.engine" size="small">
              <n-radio-button value="On">{{ t('waf.engines.On') }}</n-radio-button>
              <n-radio-button value="DetectionOnly">{{ t('waf.engines.DetectionOnly') }}</n-radio-button>
            </n-radio-group>
          </div>
          <div class="row">
            <div class="label"><b>{{ t('waf.paranoia') }}</b><span>{{ t(`waf.pl_hint.${s.paranoia}`) }} {{ t('waf.pl_rules', { n: activeRules }) }}</span></div>
            <n-select v-model:value="s.paranoia" :options="[1, 2, 3, 4].map((n) => ({ label: t(`waf.pl.${n}`), value: n }))" size="small" class="ctl" />
          </div>
          <div class="row">
            <div class="label"><b>{{ t('waf.anomaly') }}</b><span>{{ t('waf.anomaly_hint') }}</span></div>
            <n-input-number v-model:value="s.anomaly" :min="1" :max="100" size="small" style="width: 110px" />
          </div>
          <div class="row">
            <div class="label"><b>{{ t('rules.title') }}</b><span>{{ t('waf.rules_switch_hint') }}</span></div>
            <n-switch v-model:value="ps.rules_on" />
          </div>
          <div class="row">
            <div class="label"><b>{{ t('waf.block_status') }}</b><span>{{ t('waf.block_status_hint') }}</span></div>
            <n-select v-model:value="ps.block_status" :options="statusOptions" size="small" style="width: 130px" />
          </div>
        </div>
        <n-button type="primary" style="margin-top: 12px" @click="save">{{ t('waf.save_apply') }}</n-button>
      </n-card>
      <n-alert type="default" :show-icon="false" style="margin-top: 12px">{{ t('waf.layers') }}</n-alert>
    </n-tab-pane>
    <n-tab-pane name="rules" :tab="t('waf.tabs.rules')" display-directive="show:lazy">
      <SecRules :paranoia="s?.paranoia ?? 1" @changed="load(); emit('changed')" />
    </n-tab-pane>
    <n-tab-pane name="crs" :tab="t('waf.tabs.crs')" display-directive="show:lazy">
      <SecCRS :focus="focusRule" @task="(id: string, title: string) => (task = { id, title })" />
    </n-tab-pane>
    <n-tab-pane name="events" :tab="t('waf.tabs.events')" display-directive="show:lazy">
      <SecEvents @crs="openRule" @changed="emit('changed')" @task="(id: string, title: string) => (task = { id, title })" />
    </n-tab-pane>
    <n-tab-pane name="pages" :tab="t('waf.tabs.pages')" display-directive="show:lazy">
      <SecErrPages />
    </n-tab-pane>
  </n-tabs>
  <TaskModal :task-id="task?.id ?? null" :title="task?.title ?? ''" @close="task = null; load()" @done="load" />
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
  flex-wrap: wrap;
  gap: 8px 16px;
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
  flex: 1 1 280px;
}
.label span {
  font-size: 12px;
  opacity: 0.65;
}
.ctl {
  width: min(320px, 100%);
}
</style>
