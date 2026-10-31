<script setup lang="ts">
// Error pages: a library of HTML pages (nginx SSI placeholders), which page
// each 4xx / 5xx status uses for every site, and per-site overrides.
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NCard, NInput, NInputNumber, NPopconfirm, NSelect, NSpace, NSwitch, NTag, useMessage,
} from 'naive-ui'
import { get, post, put, ApiError } from '../api/client'
import CodeEditor from './CodeEditor.vue'

interface Page { id: number; code: string; name: string; html: string; modified?: boolean }
interface Mapping { global: Record<string, number>; sites: Record<string, Record<string, number>>; intercept: Record<string, boolean> }

const { t } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))

const pages = ref<Page[]>([])
const mapping = ref<Mapping>({ global: {}, sites: {}, intercept: {} })
const sites = ref<string[]>([])
const cur = ref<Page | null>(null)
const html = ref('')
const name = ref('')
const scope = ref<string>('') // '' = every site
const extra = ref<number | null>(null)

async function load(keep = cur.value?.id) {
  try {
    const [r, w] = await Promise.all([get<{ pages: Page[]; mapping: Mapping }>('/errpages'), get<{ sites: { domain: string }[] }>('/waf')])
    pages.value = r.pages
    mapping.value = { global: r.mapping.global ?? {}, sites: r.mapping.sites ?? {}, intercept: r.mapping.intercept ?? {} }
    sites.value = w.sites.map((s) => s.domain)
    select(pages.value.find((p) => p.id === keep) ?? pages.value[0] ?? null)
  } catch (e) {
    fail(e)
  }
}
onMounted(() => load())
function select(p: Page | null) {
  cur.value = p
  html.value = p?.html ?? ''
  name.value = p?.name ?? ''
}
const dirty = computed(() => !!cur.value && (html.value !== cur.value.html || name.value !== cur.value.name))

async function savePage() {
  try {
    const p = await (cur.value!.id ? put<Page>(`/errpages/${cur.value!.id}`, { ...cur.value, name: name.value, html: html.value }) : post<Page>('/errpages', { name: name.value, html: html.value }))
    msg.success(t('common.saved'))
    load(p.id)
  } catch (e) {
    fail(e)
  }
}
async function copyPage() {
  try {
    const p = await post<Page>('/errpages', { name: `${name.value} (2)`, html: html.value })
    load(p.id)
  } catch (e) {
    fail(e)
  }
}
async function act(path: string) {
  try {
    await post(path)
    load()
  } catch (e) {
    fail(e)
  }
}

// SSI placeholders and a preview with sample values.
const vars = ['status', 'host', 'request_uri', 'remote_addr', 'time_local', 'request_id', 'lmcwhm_block_reason']
const sample: Record<string, string> = {
  status: '403', host: 'example.com', request_uri: '/wp-login.php?x=1', remote_addr: '203.0.113.9', time_local: '31/Oct/2026:10:00:00 +0800',
  request_id: '5f2b9c0d7e1a4b3c', lmcwhm_block_reason: 'rule-wp-1002',
}
function insert(v: string) {
  html.value += `<!--# echo var="${v}" -->`
}
const preview = computed(() => html.value.replace(/<!--#\s*echo\s+var="(\w+)"(?:\s+default="([^"]*)")?\s*-->/g, (_, v: string, d?: string) => sample[v] ?? d ?? ''))

// ---- status → page ----
const common = [400, 401, 403, 404, 405, 406, 408, 410, 413, 429, 451, 500, 502, 503, 504]
const table = computed(() => (scope.value ? mapping.value.sites[scope.value] ?? {} : mapping.value.global))
const codes = computed(() => [...new Set([...common, ...Object.keys(table.value).map(Number)])].sort((a, b) => a - b))
const pageOptions = computed(() => [
  ...(scope.value ? [{ label: t('ep.inherit'), value: -1 }] : []),
  { label: t('ep.nginx_default'), value: 0 },
  ...pages.value.map((p) => ({ label: p.code ? t(`ep.builtin.${p.code}`) : p.name, value: p.id })),
])
function valueOf(code: number) {
  const v = table.value[String(code)]
  return v === undefined ? (scope.value ? -1 : 0) : v
}
function setCode(code: number, v: number) {
  const m = JSON.parse(JSON.stringify(mapping.value)) as Mapping
  const tgt = scope.value ? (m.sites[scope.value] ??= {}) : m.global
  if (v === -1 || (!scope.value && v === 0)) delete tgt[String(code)]
  else tgt[String(code)] = v
  mapping.value = m
}
function addCode() {
  if (!extra.value || extra.value < 400 || extra.value > 599) return
  setCode(extra.value, pages.value[0]?.id ?? 0)
  extra.value = null
}
const usedBy = (id: number) => Object.entries(mapping.value.global).filter(([, v]) => v === id).map(([c]) => c)
async function saveMapping() {
  try {
    await put('/errpages/mapping', mapping.value)
    msg.success(t('common.saved'))
    load()
  } catch (e) {
    fail(e)
  }
}
watch(scope, () => (extra.value = null))
</script>

<template>
  <div class="grid">
    <n-card size="small" :title="t('ep.library')" class="lib">
      <div class="pages">
        <button v-for="p in pages" :key="p.id" type="button" class="page" :class="{ on: cur?.id === p.id }" @click="select(p)">
          <span class="pname">{{ p.code ? t(`ep.builtin.${p.code}`) : p.name }}</span>
          <span class="codes">{{ usedBy(p.id).join(' ') || '—' }}</span>
          <n-tag v-if="p.modified" size="tiny" type="warning" :bordered="false">{{ t('rules.modified') }}</n-tag>
        </button>
      </div>
      <n-button size="small" dashed block style="margin-top: 8px" @click="select({ id: 0, code: '', name: t('ep.new_name'), html: pages[0]?.html ?? '' })">{{ t('ep.add') }}</n-button>
    </n-card>

    <n-card v-if="cur" size="small" class="edit">
      <template #header>
        <n-input v-model:value="name" size="small" :disabled="!!cur.code" style="max-width: 320px" />
      </template>
      <n-space :size="4" style="margin-bottom: 8px" :wrap="true">
        <n-button v-for="v in vars" :key="v" size="tiny" secondary @click="insert(v)">{{ t(`ep.vars.${v}`) }}</n-button>
      </n-space>
      <div class="split">
        <CodeEditor v-model="html" language="html" height="360px" />
        <iframe class="frame" :srcdoc="preview" sandbox="" :title="t('ep.preview')" />
      </div>
      <n-space style="margin-top: 8px">
        <n-button type="primary" size="small" :disabled="!dirty && !!cur.id" @click="savePage">{{ t('common.save') }}</n-button>
        <n-button v-if="cur.id" size="small" @click="copyPage">{{ t('ep.copy') }}</n-button>
        <n-popconfirm v-if="cur.code && cur.modified" @positive-click="act(`/errpages/${cur.id}/reset`)">
          <template #trigger><n-button size="small">{{ t('rules.reset') }}</n-button></template>
          {{ t('ep.reset_confirm') }}
        </n-popconfirm>
        <n-popconfirm v-if="cur.id && !cur.code" @positive-click="act(`/errpages/${cur.id}/delete`)">
          <template #trigger><n-button size="small" type="error" ghost>{{ t('common.delete') }}</n-button></template>
          {{ t('ep.delete_confirm') }}
        </n-popconfirm>
      </n-space>
    </n-card>
  </div>

  <n-card size="small" :title="t('ep.mapping')" style="margin-top: 16px">
    <n-space align="center" style="margin-bottom: 8px" :wrap="true">
      <n-select v-model:value="scope" :options="[{ label: t('ep.all_sites'), value: '' }, ...sites.map((d) => ({ label: d, value: d }))]" size="small" filterable style="width: 240px" />
      <template v-if="scope">
        <n-switch
          size="small" :value="!!mapping.intercept[scope]"
          @update:value="(v: boolean) => (mapping = { ...mapping, intercept: { ...mapping.intercept, [scope]: v } })"
        />
        <span class="hint">{{ t('ep.intercept') }}</span>
      </template>
    </n-space>
    <n-alert type="default" :show-icon="false" style="margin-bottom: 8px">{{ scope ? t('ep.site_hint') : t('ep.global_hint') }}</n-alert>
    <div class="codes-grid">
      <div v-for="c in codes" :key="c" class="code-row">
        <b>{{ c }}</b>
        <n-select :value="valueOf(c)" :options="pageOptions" size="small" @update:value="(v: number) => setCode(c, v)" />
      </div>
    </div>
    <n-space align="center" style="margin-top: 8px">
      <n-input-number v-model:value="extra" :min="400" :max="599" size="small" :placeholder="t('ep.other_code')" style="width: 140px" />
      <n-button size="small" :disabled="!extra" @click="addCode">{{ t('ep.add_code') }}</n-button>
      <n-button type="primary" size="small" @click="saveMapping">{{ t('common.save') }}</n-button>
    </n-space>
  </n-card>
</template>

<style scoped>
.grid {
  display: grid;
  grid-template-columns: minmax(200px, 260px) minmax(0, 1fr);
  gap: 16px;
  align-items: start;
}
@media (max-width: 900px) {
  .grid {
    grid-template-columns: minmax(0, 1fr);
  }
}
.pages {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.page {
  display: flex;
  align-items: center;
  gap: 6px;
  text-align: left;
  font: inherit;
  color: inherit;
  background: transparent;
  border: 1px solid rgba(128, 128, 128, 0.2);
  border-radius: 6px;
  padding: 6px 8px;
  cursor: pointer;
}
.page.on {
  border-color: #20a53a;
  box-shadow: inset 3px 0 0 #20a53a;
}
.pname {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.codes {
  font-size: 12px;
  opacity: 0.6;
}
.split {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: 12px;
}
@media (max-width: 1100px) {
  .split {
    grid-template-columns: minmax(0, 1fr);
  }
}
.frame {
  width: 100%;
  height: 360px;
  border: 1px solid rgba(128, 128, 128, 0.25);
  border-radius: 6px;
  background: #fff;
}
.codes-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 260px), 1fr));
  gap: 8px 16px;
}
.code-row {
  display: grid;
  grid-template-columns: 44px minmax(0, 1fr);
  align-items: center;
  gap: 8px;
}
.hint {
  font-size: 12px;
  opacity: 0.65;
}
</style>
