<script setup lang="ts">
import { computed, h, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NAlert, NButton, NCard, NCheckbox, NDataTable, NInput, NInputNumber, NSpace, NStatistic, NTag, useMessage, type DataTableColumns } from 'naive-ui'
import { get, post, put, ApiError } from '../api/client'
import { datetime } from '../format'

interface Feed { id: string; name: string; url: string; info?: string }
interface FeedStatus { last_run: number; entries: number; skipped: number; error?: string }
interface Feeds { enabled: string[]; custom: Feed[]; hours: number; status: Record<string, FeedStatus>; last_run: number; running: boolean; catalog: Feed[]; blocking: number }

const { t, locale } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))
const f = ref<Feeds | null>(null)
const enabled = ref<string[]>([])
const custom = ref<Feed[]>([])
const hours = ref(6)
const fwEnabled = ref(true)
const saving = ref(false)
let timer: number | undefined

async function load() {
  try {
    f.value = await get<Feeds>('/firewall/feeds')
    if (!saving.value) {
      enabled.value = [...f.value.enabled]
      custom.value = f.value.custom.map((x) => ({ ...x }))
      hours.value = f.value.hours
    }
    fwEnabled.value = (await get<{ enabled: boolean }>('/firewall')).enabled
  } catch (e) {
    fail(e)
  }
  clearTimeout(timer)
  if (f.value?.running) timer = window.setTimeout(load, 3000)
}
onMounted(load)
onUnmounted(() => clearTimeout(timer))

const rows = computed<Feed[]>(() => [...(f.value?.catalog ?? []), ...custom.value.filter((c) => c.id)])
const dirty = computed(() => {
  if (!f.value) return false
  return JSON.stringify([...enabled.value].sort()) !== JSON.stringify([...f.value.enabled].sort()) ||
    hours.value !== f.value.hours || JSON.stringify(custom.value) !== JSON.stringify(f.value.custom)
})

function toggle(id: string, on: boolean) {
  enabled.value = on ? [...enabled.value, id] : enabled.value.filter((x) => x !== id)
}
function addCustom() {
  custom.value.push({ id: '', name: '', url: '' })
}
function removeCustom(i: number) {
  custom.value.splice(i, 1)
}

async function save() {
  saving.value = true
  try {
    // Custom lists are always subscribed (the server numbers them custom1..n).
    const list = custom.value.filter((c) => c.url.trim())
    const ids = enabled.value.filter((id) => !id.startsWith('custom'))
    f.value = await put<Feeds>('/firewall/feeds', { enabled: ids, custom: list, hours: hours.value })
    enabled.value = [...f.value.enabled]
    custom.value = f.value.custom.map((x) => ({ ...x }))
    hours.value = f.value.hours
    msg.success(t('fwfeed.saved'))
  } catch (e) {
    fail(e)
  } finally {
    saving.value = false
  }
  setTimeout(load, 1500)
}
async function refresh() {
  try {
    await post('/firewall/feeds/refresh')
    setTimeout(load, 1000)
  } catch (e) {
    fail(e)
  }
}

const cols: DataTableColumns<Feed> = [
  { title: '', key: 'on', width: 50, render: (x) => (x.id.startsWith('custom') ? null : h(NCheckbox, { checked: enabled.value.includes(x.id), 'onUpdate:checked': (v: boolean) => toggle(x.id, v) })) },
  {
    title: () => t('fwfeed.list'), key: 'name',
    render: (x) => h('div', [h('a', { href: x.url, target: '_blank', rel: 'noopener noreferrer', style: 'color: inherit' }, x.name), x.info ? h('div', { class: 'hint' }, x.info) : null]),
  },
  {
    title: () => t('fwfeed.entries'), key: 'entries', width: 150,
    render: (x) => {
      const s = f.value?.status[x.id]
      if (!s) return '—'
      return h(NSpace, { size: 4, vertical: true }, () => [
        h('span', s.entries.toLocaleString()),
        s.skipped ? h('span', { class: 'hint' }, t('fwfeed.skipped', { n: s.skipped })) : null,
      ])
    },
  },
  {
    title: () => t('fwfeed.updated'), key: 'last_run', width: 220,
    render: (x) => {
      const s = f.value?.status[x.id]
      if (!s) return '—'
      return h(NSpace, { size: 4, vertical: true }, () => [
        datetime(s.last_run, locale.value),
        s.error ? h(NTag, { size: 'small', type: 'error', title: s.error }, () => s.error!.slice(0, 60)) : null,
      ])
    },
  },
]
</script>

<template>
  <n-card size="small" :title="t('fwfeed.title')" style="margin-top: 16px">
    <template #header-extra>
      <n-space align="center">
        <n-button size="small" :loading="f?.running" :disabled="!f?.enabled.length" @click="refresh">{{ t('fwfeed.refresh') }}</n-button>
        <n-button size="small" type="primary" :disabled="!dirty" :loading="saving" @click="save">{{ t('common.save') }}</n-button>
      </n-space>
    </template>
    <p class="hint" style="margin-top: 0">{{ t('fwfeed.intro') }}</p>
    <n-alert v-if="!fwEnabled" type="warning" :show-icon="false" style="margin: 8px 0">{{ t('cs.fw_off') }}</n-alert>
    <n-space :size="32" align="end" style="margin: 8px 0 12px">
      <n-statistic :label="t('fwfeed.blocking')" :value="(f?.blocking ?? 0).toLocaleString()" />
      <n-statistic :label="t('fwfeed.last_run')" :value="f?.last_run ? datetime(f.last_run, locale) : '—'" />
      <div>
        <div class="hint" style="margin-bottom: 4px">{{ t('fwfeed.every') }}</div>
        <n-input-number v-model:value="hours" size="small" :min="1" :max="48" style="width: 110px"><template #suffix>h</template></n-input-number>
      </div>
    </n-space>
    <n-data-table size="small" :columns="cols" :data="rows" :row-key="(x: Feed) => x.id" :scroll-x="760" />
    <div style="margin-top: 12px">
      <div class="hint" style="margin-bottom: 6px">{{ t('fwfeed.custom') }}</div>
      <n-space vertical>
        <n-space v-for="(c, i) in custom" :key="i" align="center" :wrap="false">
          <n-input v-model:value="c.name" size="small" :placeholder="t('fwfeed.name')" style="width: 160px" />
          <n-input v-model:value="c.url" size="small" placeholder="https://example.com/blocklist.txt" style="width: 360px" />
          <n-button size="small" quaternary type="error" @click="removeCustom(i)">{{ t('common.delete') }}</n-button>
        </n-space>
        <n-button size="small" dashed @click="addCustom">{{ t('fwfeed.add_custom') }}</n-button>
      </n-space>
    </div>
  </n-card>
</template>

<style scoped>
:deep(.hint),
.hint {
  font-size: 12px;
  opacity: 0.65;
  margin: 8px 0 0;
}
</style>
