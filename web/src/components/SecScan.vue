<script setup lang="ts">
// Security baseline scan (安全風險掃描) with one-click fixes and an HTML report.
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { NAlert, NButton, NCard, NDataTable, NEmpty, NPopconfirm, NProgress, NSelect, NSpace, NTag, useMessage, type DataTableColumns } from 'naive-ui'
import { get, post, ApiError } from '../api/client'
import { datetime } from '../format'
import TaskModal from './TaskModal.vue'
import AiButton from './AiButton.vue'
import DetailDrawer, { type DetailRow } from './DetailDrawer.vue'

interface Finding { code: string; level: 'critical' | 'warning' | 'info'; category: string; value?: string; fix?: string; route?: string }
interface Report { id: number; created_at: number; score: number; checks: number; findings: Finding[]; hostname: string; millis: number }

const { t, te, locale } = useI18n()
const router = useRouter()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))
const reports = ref<Report[]>([])
const cur = ref<Report | null>(null)
const task = ref<{ id: string; title: string } | null>(null)

async function load(pick?: number) {
  try {
    reports.value = (await get<{ items: Report[] }>('/secscan')).items
    cur.value = reports.value.find((r) => r.id === pick) ?? reports.value[0] ?? null
  } catch (e) {
    fail(e)
  }
}
onMounted(() => load())

async function run() {
  try {
    task.value = { id: (await post<{ task_id: string }>('/secscan/run')).task_id, title: t('scan.run') }
  } catch (e) {
    fail(e)
  }
}
async function fix(f: Finding) {
  try {
    const r = await post<{ task_id?: string }>('/secscan/fix', { code: f.fix })
    if (r.task_id) task.value = { id: r.task_id, title: `${t('scan.fix')} · ${riskText(f)}` }
    else {
      msg.success(t('common.done'))
      run()
    }
  } catch (e) {
    fail(e)
  }
}

const riskText = (f: Finding) => {
  for (const k of [`scan.risks.${f.code}`, `dash.risks.${f.code}`]) if (te(k)) return t(k, { n: f.value })
  return f.code
}
const levelType = (l: string) => (l === 'critical' ? 'error' : l === 'warning' ? 'warning' : 'info')
const scoreStatus = computed(() => (!cur.value ? 'default' : cur.value.score >= 85 ? 'success' : cur.value.score >= 60 ? 'warning' : 'error'))
const counts = computed(() => {
  const c = { critical: 0, warning: 0, info: 0 }
  for (const f of cur.value?.findings ?? []) c[f.level]++
  return c
})

const finding = ref<Finding | null>(null)
const findingRows = computed<DetailRow[]>(() => {
  const f = finding.value
  if (!f) return []
  return [
    { label: t('scan.level'), value: t(`dash.level.${f.level}`) },
    { label: t('scan.category'), value: t(`scan.cat.${f.category}`) },
    { label: t('scan.finding'), value: riskText(f) },
    { label: t('scan.detail'), value: f.value, mono: true },
    { label: t('scan.how'), value: f.fix ? t(`scan.fix_confirm.${f.fix}`) : f.route ? t('scan.how_route') : t('scan.how_manual') },
  ]
})
const cols = computed<DataTableColumns<Finding>>(() => [
  { title: () => t('scan.level'), key: 'level', width: 90, render: (f) => h(NTag, { size: 'small', type: levelType(f.level) }, () => t(`dash.level.${f.level}`)) },
  { title: () => t('scan.category'), key: 'category', width: 110, render: (f) => t(`scan.cat.${f.category}`) },
  {
    title: () => t('scan.finding'),
    key: 'code',
    render: (f) => h('div', [h('div', riskText(f)), f.value ? h('code', { style: 'font-size:12px;opacity:.7;word-break:break-all' }, f.value) : null]),
  },
  {
    title: '',
    key: 'a',
    width: 140,
    render: (f) =>
      h(NSpace, { size: 4, onClick: (e: Event) => e.stopPropagation() }, () => [
        f.fix
          ? h(NPopconfirm, { onPositiveClick: () => fix(f) }, { trigger: () => h(NButton, { size: 'tiny', type: 'primary' }, () => t('scan.fix')), default: () => t(`scan.fix_confirm.${f.fix}`) })
          : null,
        f.route ? h(NButton, { size: 'tiny', onClick: () => router.push({ name: f.route! }) }, () => t('dash.fix') + ' →') : null,
      ]),
  },
])

// The report is rendered here, where the translations are.
function download() {
  const r = cur.value!
  const esc = (s: string) => s.replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' })[c]!)
  const rows = r.findings
    .map((f) => `<tr><td class="${f.level}">${esc(t(`dash.level.${f.level}`))}</td><td>${esc(t(`scan.cat.${f.category}`))}</td><td>${esc(riskText(f))}${f.value ? `<br><code>${esc(f.value)}</code>` : ''}</td></tr>`)
    .join('')
  const html = `<!doctype html><html lang="${locale.value}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>${esc(t('scan.report'))} · ${esc(r.hostname)}</title>
<style>:root{--bg:#fff;--fg:#1f2329;--mute:#6b7280;--line:#e5e7eb;--c:#c0392b;--w:#b7791f;--i:#2563eb}@media (prefers-color-scheme:dark){:root{--bg:#16181d;--fg:#e5e7eb;--mute:#9ca3af;--line:#2d3139;--c:#f87171;--w:#fbbf24;--i:#60a5fa}}
body{background:var(--bg);color:var(--fg);font:14px/1.6 system-ui,sans-serif;max-width:900px;margin:0 auto;padding:24px 16px}.m{color:var(--mute)}.s{font-size:48px;font-weight:700}
table{width:100%;border-collapse:collapse;margin-top:16px}td,th{text-align:left;padding:8px;border-bottom:1px solid var(--line);vertical-align:top}.critical{color:var(--c);font-weight:600}.warning{color:var(--w);font-weight:600}.info{color:var(--i);font-weight:600}code{font-size:12px;word-break:break-all}</style></head>
<body><h1>${esc(t('scan.report'))}</h1><div class="m">${esc(r.hostname)} · ${esc(datetime(r.created_at, locale.value))}</div>
<p><span class="s">${r.score}</span><span class="m"> / 100 · ${esc(t('scan.summary', { c: counts.value.critical, w: counts.value.warning, i: counts.value.info }))}</span></p>
${r.findings.length ? `<table><tr><th>${esc(t('scan.level'))}</th><th>${esc(t('scan.category'))}</th><th>${esc(t('scan.finding'))}</th></tr>${rows}</table>` : `<p>${esc(t('scan.clean'))}</p>`}</body></html>`
  const a = document.createElement('a')
  a.href = URL.createObjectURL(new Blob([html], { type: 'text/html' }))
  a.download = `security-report-${r.hostname}-${new Date(r.created_at * 1000).toISOString().slice(0, 10)}.html`
  a.click()
  URL.revokeObjectURL(a.href)
}
const aiPrompt = computed(() => (cur.value ? `${t('scan.ai_prompt')}\n${cur.value.findings.map((f) => `- [${f.level}] ${f.code}${f.value ? `: ${f.value}` : ''}`).join('\n')}` : ''))
</script>

<template>
  <n-space justify="space-between" align="center" style="margin-bottom: 12px">
    <n-space align="center">
      <n-button type="primary" @click="run">{{ t('scan.run') }}</n-button>
      <n-select
        v-if="reports.length"
        :value="cur?.id"
        :options="reports.map((r) => ({ label: `${datetime(r.created_at, locale)} · ${r.score}`, value: r.id }))"
        style="width: 240px"
        @update:value="(id: number) => (cur = reports.find((r) => r.id === id) ?? null)"
      />
    </n-space>
    <n-space v-if="cur">
      <AiButton size="small" :prompt="aiPrompt" />
      <n-button size="small" @click="download">{{ t('scan.download') }}</n-button>
    </n-space>
  </n-space>
  <n-empty v-if="!cur" :description="t('scan.none')" style="margin: 40px 0" />
  <template v-else>
    <n-card size="small" style="margin-bottom: 12px">
      <n-space align="center" :size="24">
        <n-progress type="circle" :percentage="cur.score" :status="scoreStatus" style="width: 84px">{{ cur.score }}</n-progress>
        <div>
          <div>{{ t('scan.summary', { c: counts.critical, w: counts.warning, i: counts.info }) }}</div>
          <div class="hint">{{ cur.hostname }} · {{ datetime(cur.created_at, locale) }} · {{ (cur.millis / 1000).toFixed(1) }}s</div>
        </div>
      </n-space>
    </n-card>
    <n-alert v-if="!cur.findings.length" type="success" :show-icon="false">{{ t('scan.clean') }}</n-alert>
    <n-data-table v-else size="small" :columns="cols" :data="cur.findings" :row-key="(f: Finding) => f.code" :row-props="(f: Finding) => ({ style: 'cursor: pointer', onClick: () => (finding = f) })" />
    <p class="hint">{{ t('scan.weekly') }}</p>
  </template>
  <TaskModal :task-id="task?.id ?? null" :title="task?.title ?? ''" @close="task = null; load()" @done="load()" />
  <DetailDrawer :show="!!finding" :title="finding ? riskText(finding) : ''" :rows="findingRows" @close="finding = null">
    <template v-if="finding && (finding.fix || finding.route)" #actions>
      <n-popconfirm v-if="finding.fix" @positive-click="fix(finding!); finding = null">
        <template #trigger><n-button type="primary">{{ t('scan.fix') }}</n-button></template>
        {{ t(`scan.fix_confirm.${finding.fix}`) }}
      </n-popconfirm>
      <n-button v-if="finding.route" @click="router.push({ name: finding!.route! })">{{ t('dash.fix') }} →</n-button>
    </template>
  </DetailDrawer>
</template>

<style scoped>
.hint {
  font-size: 12px;
  opacity: 0.65;
  margin-top: 8px;
}
</style>
