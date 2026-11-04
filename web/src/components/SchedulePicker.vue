<script setup lang="ts">
// The execution period of a scheduled task (計劃任務, backup plans): every N
// seconds / minutes / hours, hourly, daily, weekly, monthly or a cron
// expression. v-model is the cron expression; an existing one opens in the
// mode that produced it.
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NInput, NInputNumber, NSelect, NTimePicker } from 'naive-ui'
import { get } from '../api/client'

const props = defineProps<{ modelValue: string }>()
const emit = defineEmits<{ 'update:modelValue': [string]; invalid: [boolean] }>()
const { t, locale } = useI18n()

type Mode = 'seconds' | 'minutes' | 'hours' | 'hourly' | 'daily' | 'weekly' | 'monthly' | 'custom'
interface State { mode: Mode; n: number; minute: number; time: string; dow: number; dom: number; custom: string }
const MODES: Mode[] = ['seconds', 'minutes', 'hours', 'hourly', 'daily', 'weekly', 'monthly', 'custom']

// Times are "HH:mm" strings: the picker's numeric value is a local-time
// timestamp, which would shift by the browser's UTC offset.
const pad = (x: number) => String(x).padStart(2, '0')
const clock = (hr: number, mi: number) => `${pad(hr)}:${pad(mi)}`
const hm = (v: string) => {
  const [hr, mi] = (v || '03:00').split(':').map(Number)
  return [mi || 0, hr || 0]
}

function parseSchedule(expr: string): State {
  const s: State = { mode: 'custom', n: 5, minute: 0, time: '03:00', dow: 1, dom: 1, custom: expr.trim() }
  const e = expr.trim().replace(/\s+/g, ' ')
  let m: RegExpMatchArray | null
  if ((m = e.match(/^\*\/(\d+) \* \* \* \* \*$/))) return { ...s, mode: 'seconds', n: +m[1] }
  if ((m = e.match(/^\*\/(\d+) \* \* \* \*$/))) return { ...s, mode: 'minutes', n: +m[1] }
  if ((m = e.match(/^(\d+) \*\/(\d+) \* \* \*$/))) return { ...s, mode: 'hours', minute: +m[1], n: +m[2] }
  if ((m = e.match(/^(\d+) \* \* \* \*$/))) return { ...s, mode: 'hourly', minute: +m[1] }
  if ((m = e.match(/^(\d+) (\d+) \* \* \*$/))) return { ...s, mode: 'daily', time: clock(+m[2], +m[1]) }
  if ((m = e.match(/^(\d+) (\d+) \* \* ([0-6])$/))) return { ...s, mode: 'weekly', time: clock(+m[2], +m[1]), dow: +m[3] }
  if ((m = e.match(/^(\d+) (\d+) (\d+) \* \*$/))) return { ...s, mode: 'monthly', time: clock(+m[2], +m[1]), dom: +m[3] }
  return s
}

function expr(x: State): string {
  const [mi, hr] = hm(x.time)
  switch (x.mode) {
    case 'seconds': return `*/${x.n || 1} * * * * *`
    case 'minutes': return `*/${x.n || 1} * * * *`
    case 'hours': return `${x.minute ?? 0} */${x.n || 1} * * *`
    case 'hourly': return `${x.minute ?? 0} * * * *`
    case 'daily': return `${mi} ${hr} * * *`
    case 'weekly': return `${mi} ${hr} * * ${x.dow}`
    case 'monthly': return `${mi} ${hr} ${x.dom || 1} * *`
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
const tz = ref('')
const bad = ref(false)
let timer: number | undefined
watch(current, (s) => {
  clearTimeout(timer)
  timer = window.setTimeout(async () => {
    if (!s) return
    try {
      const r = await get<{ next: number[]; tz?: string }>(`/cron/preview?schedule=${encodeURIComponent(s)}`)
      preview.value = r.next
      tz.value = r.tz ?? ''
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
// Run times are shown in the panel's time zone, the one the jobs run in.
const when = (x: number) => {
  const o: Intl.DateTimeFormatOptions = { dateStyle: 'short', timeStyle: 'short' }
  try {
    return new Intl.DateTimeFormat(locale.value, { ...o, timeZone: tz.value || undefined }).format(x * 1000)
  } catch {
    return new Intl.DateTimeFormat(locale.value, o).format(x * 1000)
  }
}
const unit = computed(() => (st.value.mode === 'seconds' ? t('cron.unit_s') : st.value.mode === 'minutes' ? t('cron.unit_m') : t('cron.unit_h')))
</script>

<template>
  <div class="sched">
    <div class="row">
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
      <n-time-picker v-if="['daily', 'weekly', 'monthly'].includes(st.mode)" v-model:formatted-value="st.time" format="HH:mm" value-format="HH:mm" :clearable="false" style="width: 110px" />
      <n-input v-if="st.mode === 'custom'" v-model:value="st.custom" placeholder="*/5 * * * *" style="width: 220px" />
    </div>
    <p class="preview" :class="{ bad }">
      <code>{{ current }}</code> · {{ bad ? t('errors.cron.invalid_schedule') : `${t('cron.preview')}: ${preview.slice(0, 3).map(when).join(' · ') || '—'}${tz ? ` (${tz})` : ''}` }}
    </p>
  </div>
</template>

<style scoped>
.sched {
  width: 100%;
}
.row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
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
