<script setup lang="ts">
import { computed, h, nextTick, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import {
  NAlert, NButton, NCard, NCode, NCollapse, NCollapseItem, NDataTable, NDynamicTags, NForm, NFormItem, NInput, NInputNumber, NModal,
  NPopconfirm, NSelect, NSpace, NSpin, NTabPane, NTabs, NTag, useMessage, type DataTableColumns,
} from 'naive-ui'
import { get, post, put, ApiError } from '../api/client'
import { datetime } from '../format'

interface Step { tool: string; input: string; error?: string }
interface Turn { role: 'user' | 'assistant'; text: string; steps?: Step[] }
interface Answer { text: string; steps: Step[]; model: string; usage: { input: number; output: number } }
interface Token { id: number; name: string; hint: string; scope: string; ips: string[]; expires_at: number; last_used: number; last_ip: string; created_at: number }
interface Tool { name: string; title: string; description: string; scope: string; destructive: boolean }
interface Info { tools: Tool[]; prompts: { name: string; title: string; description: string }[]; port: number; tls: boolean }

const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))
const tab = ref('chat')

// ---- settings ----
const cfg = ref<{ model: string; effort: string; base_url: string; has_key: boolean; default_model: string } | null>(null)
const apiKey = ref('')
async function loadCfg() {
  cfg.value = await get('/ai/config')
}
async function saveCfg(clear = false) {
  try {
    await put('/ai/config', { api_key: apiKey.value, model: cfg.value!.model, effort: cfg.value!.effort, base_url: cfg.value!.base_url, clear_key: clear })
    apiKey.value = ''
    msg.success(t('common.saved'))
    loadCfg()
  } catch (e) {
    fail(e)
  }
}

// ---- chat ----
const turns = ref<Turn[]>([])
const input = ref('')
const busy = ref(false)
const usage = ref('')
const box = ref<HTMLElement | null>(null)
const render = (md: string) => DOMPurify.sanitize(marked.parse(md, { async: false }) as string)
async function send(text?: string) {
  const q = (text ?? input.value).trim()
  if (!q || busy.value) return
  turns.value.push({ role: 'user', text: q })
  input.value = ''
  busy.value = true
  scroll()
  try {
    const a = await post<Answer>('/ai/ask', { turns: turns.value.map(({ role, text }) => ({ role, text })) })
    turns.value.push({ role: 'assistant', text: a.text, steps: a.steps })
    usage.value = t('ai.usage', { model: a.model, input: a.usage.input, output: a.usage.output })
  } catch (e) {
    turns.value.pop()
    input.value = q
    if (e instanceof ApiError && e.code === 'ai.no_key') tab.value = 'settings'
    fail(e)
  } finally {
    busy.value = false
    scroll()
  }
}
function scroll() {
  nextTick(() => box.value?.scrollTo({ top: box.value.scrollHeight, behavior: 'smooth' }))
}
const quick = computed(() => ['security', 'load', 'backups', 'errors'].map((k) => ({ label: t(`ai.quick.${k}.label`), prompt: t(`ai.quick.${k}.prompt`) })))
// Other pages open the assistant with ?q=<question> (一鍵 AI 分析).
watch(
  () => route.query.q,
  (q) => {
    if (typeof q === 'string' && q) {
      router.replace({ query: {} })
      tab.value = 'chat'
      send(q)
    }
  },
  { immediate: true },
)

// ---- MCP ----
const info = ref<Info | null>(null)
const tokens = ref<Token[]>([])
const tf = ref<{ name: string; scope: string; ips: string[]; ttl_days: number } | null>(null)
const secret = ref('')
async function loadMCP() {
  info.value = await get<Info>('/mcp/info')
  tokens.value = (await get<{ items: Token[] }>('/tokens')).items
}
async function createToken() {
  try {
    const r = await post<{ secret: string }>('/tokens', tf.value)
    secret.value = r.secret
    tf.value = null
    loadMCP()
  } catch (e) {
    fail(e)
  }
}
async function delToken(tk: Token) {
  await post(`/tokens/${tk.id}/delete`).catch(fail)
  loadMCP()
}
const panelURL = computed(() => `${info.value?.tls ? 'https' : 'http'}://${location.hostname}:${info.value?.port ?? location.port}/mcp`)
const httpCmd = computed(() => `claude mcp add --transport http lmcwhm ${panelURL.value} --header "Authorization: Bearer ${secret.value || '<token>'}"`)
const sshCmd = computed(() => `claude mcp add lmcwhm -- ssh root@${location.hostname} lmcwhm mcp --stdio`)
const scopeType = (s: string) => (s === 'admin' ? 'error' : s === 'operate' ? 'warning' : 'success')
const tokenCols = computed<DataTableColumns<Token>>(() => [
  { title: () => t('alert.name'), key: 'name', render: (x) => h('div', [h('b', x.name), h('div', { style: 'font-size:12px;opacity:.6' }, x.hint + '…')]) },
  { title: () => t('ai.scope'), key: 'scope', width: 100, render: (x) => h(NTag, { size: 'small', type: scopeType(x.scope) }, () => t(`ai.scopes.${x.scope}`)) },
  { title: () => t('ai.ips'), key: 'ips', render: (x) => (x.ips.length ? x.ips.join(', ') : t('ai.any_ip')) },
  { title: () => t('ai.expires'), key: 'expires_at', width: 170, render: (x) => (x.expires_at ? datetime(x.expires_at, locale.value) : t('ai.never')) },
  { title: () => t('ai.last_used'), key: 'last_used', width: 200, render: (x) => (x.last_used ? `${datetime(x.last_used, locale.value)} · ${x.last_ip}` : '—') },
  { title: '', key: 'a', width: 80, render: (x) => h(NPopconfirm, { onPositiveClick: () => delToken(x) }, { trigger: () => h(NButton, { size: 'tiny', type: 'error', quaternary: true }, () => t('ai.revoke')), default: () => t('ai.revoke_confirm') }) },
])
const toolCols = computed<DataTableColumns<Tool>>(() => [
  { title: () => t('ai.tool'), key: 'name', width: 200, render: (x) => h('code', x.name) },
  { title: () => t('ai.scope'), key: 'scope', width: 100, render: (x) => h(NTag, { size: 'small', type: scopeType(x.scope) }, () => t(`ai.scopes.${x.scope}`)) },
  { title: () => t('ai.description'), key: 'description' },
])

onMounted(() => {
  loadCfg().catch(fail)
  loadMCP().catch(fail)
})
</script>

<template>
  <n-tabs v-model:value="tab" type="line" animated>
    <n-tab-pane name="chat" :tab="t('ai.tab_chat')">
      <n-alert v-if="cfg && !cfg.has_key" type="info" :show-icon="false" style="margin-bottom: 12px">
        {{ t('ai.no_key') }} <n-button text type="primary" @click="tab = 'settings'">{{ t('ai.tab_settings') }} →</n-button>
      </n-alert>
      <div ref="box" class="chat">
        <div v-if="!turns.length" class="empty">
          <p>{{ t('ai.intro') }}</p>
          <n-space justify="center"><n-button v-for="q in quick" :key="q.label" @click="send(q.prompt)">{{ q.label }}</n-button></n-space>
        </div>
        <div v-for="(m, i) in turns" :key="i" :class="['msg', m.role]">
          <div v-if="m.role === 'user'" class="bubble">{{ m.text }}</div>
          <template v-else>
            <n-collapse v-if="m.steps?.length" style="margin-bottom: 6px">
              <n-collapse-item :title="t('ai.steps', { n: m.steps.length })" name="s">
                <div v-for="(s, k) in m.steps" :key="k" class="step"><code>{{ s.tool }}</code> {{ s.input !== '{}' ? s.input : '' }} <span v-if="s.error" class="err">{{ s.error }}</span></div>
              </n-collapse-item>
            </n-collapse>
            <!-- eslint-disable-next-line vue/no-v-html -->
            <div class="md" v-html="render(m.text)" />
          </template>
        </div>
        <div v-if="busy" class="msg assistant"><n-spin size="small" /> {{ t('ai.thinking') }}</div>
      </div>
      <n-space vertical style="margin-top: 12px">
        <n-input v-model:value="input" type="textarea" :autosize="{ minRows: 2, maxRows: 6 }" :placeholder="t('ai.placeholder')" @keydown.enter.exact.prevent="send()" />
        <n-space justify="space-between" align="center">
          <span class="hint">{{ usage }}</span>
          <n-space>
            <n-button :disabled="!turns.length || busy" @click="turns = []; usage = ''">{{ t('ai.clear') }}</n-button>
            <n-button type="primary" :loading="busy" :disabled="!input.trim()" @click="send()">{{ t('ai.send') }}</n-button>
          </n-space>
        </n-space>
      </n-space>
    </n-tab-pane>

    <n-tab-pane name="mcp" tab="MCP">
      <n-card size="small" :title="t('ai.connect_title')">
        <p class="hint" style="margin-top: 0">{{ t('ai.connect_ssh') }}</p>
        <n-code :code="sshCmd" language="bash" word-wrap />
        <p class="hint">{{ t('ai.connect_http') }}</p>
        <n-code :code="httpCmd" language="bash" word-wrap />
        <n-space style="margin-top: 12px">
          <n-button type="primary" @click="tf = { name: 'Claude Code', scope: 'read', ips: [], ttl_days: 90 }">{{ t('ai.new_token') }}</n-button>
          <n-button tag="a" href="/api/v1/mcp/skill">{{ t('ai.download_skill') }}</n-button>
        </n-space>
      </n-card>
      <n-card size="small" :title="t('ai.tokens')" style="margin-top: 16px">
        <n-data-table size="small" :columns="tokenCols" :data="tokens" :row-key="(x: Token) => x.id" :scroll-x="900" />
      </n-card>
      <n-card v-if="info" size="small" :title="t('ai.tools', { n: info.tools.length })" style="margin-top: 16px">
        <n-data-table size="small" :columns="toolCols" :data="info.tools" :row-key="(x: Tool) => x.name" :max-height="420" />
        <p class="hint">{{ t('ai.prompts') }}: <code v-for="p in info.prompts" :key="p.name" style="margin-right: 8px">{{ p.name }}</code></p>
      </n-card>
    </n-tab-pane>

    <n-tab-pane name="settings" :tab="t('ai.tab_settings')">
      <n-card v-if="cfg" size="small" style="max-width: 640px">
        <n-form label-placement="top">
          <n-form-item :label="t('ai.api_key')" :feedback="t('ai.api_key_hint')">
            <n-input v-model:value="apiKey" type="password" show-password-on="click" :placeholder="cfg.has_key ? t('fw.token_saved') : 'sk-ant-…'" />
          </n-form-item>
          <n-form-item :label="t('ai.model')" :feedback="t('ai.model_hint', { model: cfg.default_model })">
            <n-select v-model:value="cfg.model" filterable tag :options="['claude-opus-5-5', 'claude-sonnet-5-5', 'claude-haiku-5-5', 'claude-fable-5-1'].map((m) => ({ label: m, value: m }))" />
          </n-form-item>
          <n-form-item :label="t('ai.effort')" :feedback="t('ai.effort_hint')">
            <n-select v-model:value="cfg.effort" :options="['low', 'medium', 'high', 'xhigh', 'max'].map((v) => ({ label: v, value: v }))" style="width: 160px" />
          </n-form-item>
          <n-form-item :label="t('ai.base_url')" :feedback="t('ai.base_url_hint')"><n-input v-model:value="cfg.base_url" placeholder="https://api.anthropic.com" /></n-form-item>
          <n-space>
            <n-button type="primary" @click="saveCfg()">{{ t('common.save') }}</n-button>
            <n-button v-if="cfg.has_key" type="error" quaternary @click="saveCfg(true)">{{ t('fw.forget') }}</n-button>
          </n-space>
        </n-form>
      </n-card>
    </n-tab-pane>
  </n-tabs>

  <n-modal :show="!!tf" @update:show="(v: boolean) => !v && (tf = null)">
    <n-card v-if="tf" :title="t('ai.new_token')" style="width: min(520px, calc(100vw - 32px))" closable @close="tf = null">
      <n-form label-placement="top">
        <n-form-item :label="t('alert.name')"><n-input v-model:value="tf.name" /></n-form-item>
        <n-form-item :label="t('ai.scope')" :feedback="t(`ai.scope_hint.${tf.scope}`)">
          <n-select v-model:value="tf.scope" :options="['read', 'operate', 'admin'].map((s) => ({ label: t(`ai.scopes.${s}`), value: s }))" />
        </n-form-item>
        <n-form-item :label="t('ai.ips')" :feedback="t('ai.ips_hint')"><n-dynamic-tags v-model:value="tf.ips" /></n-form-item>
        <n-form-item :label="t('ai.ttl')"><n-input-number v-model:value="tf.ttl_days" :min="0" :max="3650" style="width: 140px" /></n-form-item>
      </n-form>
      <template #footer><n-space justify="end"><n-button type="primary" :disabled="!tf.name" @click="createToken">{{ t('common.create') }}</n-button></n-space></template>
    </n-card>
  </n-modal>
  <n-modal :show="!!secret" @update:show="(v: boolean) => !v && (secret = '')">
    <n-card :title="t('ai.token_created')" style="width: min(620px, calc(100vw - 32px))" closable @close="secret = ''">
      <n-alert type="warning" :show-icon="false">{{ t('ai.token_once') }}</n-alert>
      <n-code :code="secret" style="margin: 12px 0; display: block" word-wrap />
      <n-code :code="httpCmd" language="bash" word-wrap />
    </n-card>
  </n-modal>
</template>

<style scoped>
.chat {
  height: calc(100vh - 300px);
  min-height: 320px;
  overflow: auto;
  border: 1px solid rgba(128, 128, 128, 0.2);
  border-radius: 6px;
  padding: 12px;
}
.empty {
  text-align: center;
  opacity: 0.85;
  margin-top: 60px;
}
.msg {
  margin: 10px 0;
}
.msg.user {
  display: flex;
  justify-content: flex-end;
}
.bubble {
  background: rgba(24, 160, 88, 0.12);
  padding: 8px 12px;
  border-radius: 8px;
  max-width: 80%;
  white-space: pre-wrap;
}
.md {
  line-height: 1.6;
  overflow-x: auto;
}
.md :deep(pre) {
  background: rgba(128, 128, 128, 0.12);
  padding: 8px;
  border-radius: 4px;
  overflow-x: auto;
}
.md :deep(table) {
  border-collapse: collapse;
}
.md :deep(td),
.md :deep(th) {
  border: 1px solid rgba(128, 128, 128, 0.3);
  padding: 4px 8px;
}
.step {
  font-size: 12px;
  font-family: monospace;
  word-break: break-all;
}
.err {
  color: #d03050;
}
.hint {
  font-size: 12px;
  opacity: 0.65;
}
</style>
