<script setup lang="ts">
// The execution period of a scheduled task (計劃任務, backup plans): every N
// seconds / minutes / hours, hourly, daily, weekly, monthly or a cron
// expression. v-model is the cron expression; an existing one opens in the
// mode that produced it.
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NInput, NInputNumber, NSelect, NSpace, NTimePicker } from 'naive-ui'
import { get } from '../api/client'
import { datetime } from '../format'

const props = defineProps<{ modelValue: string }>()
const emit = defineEmits<{ 'update:modelValue': [string]; invalid: [boolean] }>()
const { t, locale } = useI18n()

type Mode = 'seconds' | 'minutes' | 'hours' | 'hourly' | 'daily' | 'weekly' | 'monthly' | 'custom'
interface State { mode: Mode; n: number; minute: number; time: number; dow: number; dom: number; custom: string }
const MODES: Mode[] = ['seconds', 'minutes', 'hours', 'hourly', 'daily', 'weekly', 'monthly', 'custom']

// time pickers hold milliseconds since midnight (UTC-based value)
const ms = (hr: number, mi: number) => (hr * 60 + mi) * 60000
const hm = (v: number) => [Math.floor(v / 60000) % 60, Math.floor(v / 3600000) % 24]

function parseSchedule(expr: string): State {
  const s: State = { mode: 'custom', n: 5, minute: 0, time: ms(3, 0), dow: 1, dom: 1, custom: expr.trim() }
  const e = expr.trim().replace(/\s+/g, ' ')
  let m: RegExpMatchArray | null
  if ((m = e.match(/^\*\/(\d+) \* \* \* \* \*$/))) return { ...s, mode: 'seconds', n: +m[1] }
  if ((m = e.match(/^\*\/(\d+) \* \* \* \*$/))) return { ...s, mode: 'minutes', n: +m[1] }
  if ((m = e.match(/^(\d+) \*\/(\d+) \* \* \*$/))) return { ...s, mode: 'hours', minute: +m[1], n: +m[2] }
  if ((m = e.match(/^(\d+) \* \* \* \*$/))) return { ...s, mode: 'hourly', minute: +m[1] }
  if ((m = e.match(/^(\d+) (\d+) \* \* \*$/))) return { ...s, mode: 'daily', time: ms(+m[2], +m[1]) }
  if ((m = e.match(/^(\d+) (\d+) \* \* ([0-6])$/))) return { ...s, mode: 'weekly', time: ms(+m[2], +m[1]), dow: +m[3] }
  if ((m = e.match(/^(\d+) (\d+) (\d+) \* \*$/))) return { ...s, mode: 'monthly', time: ms(+m[2], +m[1]), dom: +m[3] }
  return s
}

function expr(x: State): string {
  const [mi, hr] = hm(x.time)
  switch (x.mode) {
    case 'seconds': return `*/${x.n} * * * * *`
    case 'minutes': return `*/${x.n} * * * *`
    case 'hours': return `${x.minute} */${x.n} * * *`
    case 'hourly': return `${x.minute} * * * *`
    case 'daily': return `${mi} ${hr} * * *`
    case 'weekly': return `${mi} ${hr} * * ${x.dow}`
    case 'monthly': return `${mi} ${hr} ${x.dom} * *`
  }
  return x.custom.trim()
}

const st = ref<State>(parseSchedule(props.modelValue))
watch(() => props.modelValue, (v) => v !== expr(st.value) && (st.value = parseSchedule(v)))
const current = computed(() => expr(st.value))
watch(current, (v) => v !== props.modelValue && emit('update:modelValue', v), { immediate: true })
function setMode(m: Mode) {
  if (m === 'custom') st.value.custom = current.value
  if (m === 'hours' && st.value.n > 23) st.value.n = 6
  st.value.mode = m
}

const preview = ref<number[]>([])
const bad = ref(false)
let timer: number | undefined
watch(current, (s) => {
  clearTimeout(timer)
  timer = window.setTimeout(async () => {
    if (!s) return
    try {
      preview.value = (await get<{ next: number[] }>(`/cron/preview?schedule=${encodeURIComponent(s)}`)).next
      bad.value = false
    } catch {
      preview.value = []
      bad.value = true
    }
    emit('invalid', bad.value)
  }, 250)
}, { immediate: true })

const modeOptions = computed(() => MODES.map((m) => ({ label: t(`cron.modes.${m}`), value: m })))
const dowOptions = computed(() => [0, 1, 2, 3, 4, 5, 6].map((d) => ({ label: new Intl.DateTimeFormat(locale.value, { weekday: 'long' }).format(new Date(2026, 0, 4 + d)), value: d })))
const unit = computed(() => (st.value.mode === 'seconds' ? t('cron.unit_s') : st.value.mode === 'minutes' ? t('cron.unit_m') : t('cron.unit_h')))
</script>

<template>
  <div class="sched">
    <n-space :size="8" align="center">
      <n-select :value="st.mode" :options="modeOptions" style="width: 150px" @update:value="setMode" />
      <template v-if="['seconds', 'minutes', 'hours'].includes(st.mode)">
        <span>{{ t('cron.every') }}</span>
        <n-input-number v-model:value="st.n" :min="1" :max="st.mode === 'hours' ? 23 : 59" style="width: 100px" />
        <span>{{ unit }}</span>
      </template>
      <template v-if="st.mode === 'hourly' || st.mode === 'hours'">
        <span>{{ t('cron.at_minute') }}</span><n-input-number v-model:value="st.minute" :min="0" :max="59" style="width: 100px" />
      </template>
      <n-select v-if="st.mode === 'weekly'" v-model:value="st.dow" :options="dowOptions" style="width: 130px" />
      <template v-if="st.mode === 'monthly'">
        <n-input-number v-model:value="st.dom" :min="1" :max="31" style="width: 90px" /><span>{{ t('cron.day') }}</span>
      </template>
      <n-time-picker v-if="['daily', 'weekly', 'monthly'].includes(st.mode)" v-model:value="st.time" format="HH:mm" time-zone="UTC" style="width: 110px" />
      <n-input v-if="st.mode === 'custom'" v-model:value="st.custom" placeholder="*/5 * * * *" style="width: 220px" />
    </n-space>
    <p class="preview" :class="{ bad }">
      <code>{{ current }}</code> · {{ bad ? t('errors.cron.invalid_schedule') : `${t('cron.preview')}: ${preview.slice(0, 3).map((x) => datetime(x, locale)).join(' · ') || '—'}` }}
    </p>
  </div>
</template>

<style scoped>
.sched {
  width: 100%;
}
.preview {
  font-size: 12px;
  opacity: 0.7;
  margin: 6px 0 0;
}
.preview.bad {
  color: #d03050;
  opacity: 1;
}
</style>
