<script setup lang="ts">
import { computed, h, nextTick, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import {
  NAlert, NButton, NCard, NCode, NCollapse, NCollapseItem, NDataTable, NDatePicker, NDynamicTags, NForm, NFormItem, NInput, NInputNumber, NModal,
  NPopconfirm, NSelect, NSpace, NSpin, NTabPane, NTabs, NTag, useMessage, type DataTableColumns,
} from 'naive-ui'
import { get, post, put, ApiError } from '../api/client'
import { datetime } from '../format'

interface Step { tool: string; input: string; error?: string }
interface Turn { role: 'user' | 'assistant'; text: string; steps?: Step[] }
interface Answer { text: string; steps: Step[]; model: string; usage: { input: number; output: number } }
interface Token { id: number; name: string; hint: string; scope: string; ips: string[]; expires_at: number; last_used: number; last_ip: string; created_at: number; disabled: boolean; recoverable: boolean }
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
async function loadCfg() {
  cfg.value = await get('/ai/config')
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
    if (e instanceof ApiError && e.code === 'ai.no_key') router.push({ name: 'settings', query: { tab: 'api' } })
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
const selectedToken = ref<Token | null>(null)
const selectedSecret = ref('')
const promptClient = ref<'kimi' | 'claude' | 'codex'>('kimi')
const editingExpiry = ref<{ token: Token; value: number | null } | null>(null)
async function loadMCP() {
  info.value = await get<Info>('/mcp/info')
  tokens.value = (await get<{ items: Token[] }>('/tokens')).items
  if (selectedToken.value) selectedToken.value = tokens.value.find((x) => x.id === selectedToken.value?.id) ?? null
}
async function createToken() {
  try {
    const r = await post<{ token: Token; secret: string }>('/tokens', tf.value)
    selectedToken.value = r.token
    selectedSecret.value = r.secret
    promptClient.value = 'kimi'
    tf.value = null
    await loadMCP()
  } catch (e) {
    fail(e)
  }
}
async function openToken(tk: Token) {
  selectedToken.value = tk
  selectedSecret.value = ''
  promptClient.value = 'kimi'
  if (tk.recoverable) {
    try {
      selectedSecret.value = (await get<{ secret: string }>(`/tokens/${tk.id}/secret`)).secret
    } catch (e) {
      fail(e)
    }
  }
}
async function delToken(tk: Token) {
  await post(`/tokens/${tk.id}/delete`).catch(fail)
  if (selectedToken.value?.id === tk.id) selectedToken.value = null
  loadMCP()
}
async function setDisabled(tk: Token, disabled: boolean) {
  try {
    await put(`/tokens/${tk.id}`, { disabled })
    await loadMCP()
  } catch (e) {
    fail(e)
  }
}
async function saveExpiry() {
  if (!editingExpiry.value) return
  try {
    await put(`/tokens/${editingExpiry.value.token.id}`, { expires_at: editingExpiry.value.value ? Math.floor(editingExpiry.value.value / 1000) : 0 })
    editingExpiry.value = null
    await loadMCP()
  } catch (e) {
    fail(e)
  }
}
const panelURL = computed(() => `${info.value?.tls ? 'https' : 'http'}://${location.hostname}:${info.value?.port ?? location.port}/mcp`)
const sshCmd = computed(() => `ssh root@${location.hostname} lmcwhm mcp --stdio`)
function tokenPrompt() {
  const tk = selectedToken.value
  if (!tk) return ''
  const client = {
    kimi: `Kimi Code: add this to mcp.json, then start a new session and check /mcp:\n${JSON.stringify({ mcpServers: { lmcwhm: { url: panelURL.value, bearerTokenEnvVar: 'LMCWHM_TOKEN' } } }, null, 2)}`,
    claude: `Claude Code: run this command, then check with claude mcp list:\nclaude mcp add --transport http lmcwhm ${panelURL.value} --header "Authorization: Bearer $LMCWHM_TOKEN"`,
    codex: `Codex: add this to ~/.codex/config.toml, then check with codex mcp list:\n[mcp_servers.lmcwhm]\nurl = "${panelURL.value}"\nbearer_token_env_var = "LMCWHM_TOKEN"`,
  }[promptClient.value]
  const scope = {
    read: 'This token has read scope only: query information but never create, modify, delete, restart, run shell commands or make any other state change.',
    operate: 'This token has operate scope: normal website, Docker, database, security and backup operations are allowed, but panel administration and root shell are not. Confirm high-impact actions first.',
    admin: 'This token has admin scope: it can change panel administration and run root shell commands. Before every modification, deletion, update, restart or high-impact action, explain the impact and obtain confirmation.',
  }[tk.scope]
  return `Configure and connect lmcwhm MCP for the token named ${tk.name}. Set LMCWHM_TOKEN in the environment used to start the AI client (use the full token above), and never commit it to Git.\n\n${client}\n\n${scope}\n\nHTTPS access goes through Cloudflare. Keep the Cloudflare proxy and TLS verification enabled. For 403, check WAF, Bot Fight Mode, Cloudflare Access and source IP restrictions. For 520/522/524, check the origin, Cloudflare's connection to the panel port and proxy timeouts. Use background tasks for long operations and get_task for progress; do not disable security rules. Finally, list tools and call server_overview to verify.`
}
const promptClientOptions = [{ label: 'Kimi', value: 'kimi' }, { label: 'Claude', value: 'claude' }, { label: 'Codex', value: 'codex' }]
const copy = (text: string) => navigator.clipboard?.writeText(text).then(() => msg.success(t('common.copied')))
const scopeType = (s: string) => (s === 'admin' ? 'error' : s === 'operate' ? 'warning' : 'success')
const expired = (tk: Token) => tk.expires_at > 0 && tk.expires_at <= Date.now() / 1000
const tokenCols = computed<DataTableColumns<Token>>(() => [
  { title: () => t('alert.name'), key: 'name', render: (x) => h('div', [h('b', x.name), h('div', { style: 'font-size:12px;opacity:.6' }, x.hint + '…')]) },
  { title: () => t('ai.scope'), key: 'scope', width: 100, render: (x) => h(NTag, { size: 'small', type: scopeType(x.scope) }, () => t(`ai.scopes.${x.scope}`)) },
  { title: () => t('common.status'), key: 'disabled', width: 100, render: (x) => h(NTag, { size: 'small', type: x.disabled || expired(x) ? 'error' : 'success' }, () => t(`ai.status.${x.disabled ? 'paused' : expired(x) ? 'expired' : 'active'}`)) },
  { title: () => t('ai.ips'), key: 'ips', render: (x) => (x.ips.length ? x.ips.join(', ') : t('ai.any_ip')) },
  { title: () => t('ai.expires'), key: 'expires_at', width: 170, render: (x) => (x.expires_at ? datetime(x.expires_at, locale.value) : t('ai.never')) },
  { title: () => t('ai.last_used'), key: 'last_used', width: 180, render: (x) => (x.last_used ? `${datetime(x.last_used, locale.value)} · ${x.last_ip}` : '—') },
  {
    title: () => t('common.actions'), key: 'a', width: 360, render: (x) => h(NSpace, { size: 4 }, () => [
      h(NButton, { size: 'tiny', type: 'primary', onClick: () => openToken(x) }, () => t('ai.token_prompt')),
      h(NButton, { size: 'tiny', onClick: () => editingExpiry.value = { token: x, value: x.expires_at ? x.expires_at * 1000 : null } }, () => t('ai.edit_expiry')),
      h(NButton, { size: 'tiny', type: x.disabled ? 'success' : 'warning', onClick: () => setDisabled(x, !x.disabled) }, () => t(`ai.${x.disabled ? 'resume' : 'pause'}`)),
      h(NPopconfirm, { onPositiveClick: () => delToken(x) }, { trigger: () => h(NButton, { size: 'tiny', type: 'error', quaternary: true }, () => t('ai.revoke')), default: () => t('ai.revoke_confirm') }),
    ]),
  },
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
        {{ t('ai.no_key') }} <n-button text type="primary" @click="router.push({ name: 'settings', query: { tab: 'api' } })">{{ t('apis.open') }} →</n-button>
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
        <p class="hint" style="margin-top: 0">{{ t('ai.connect_clients') }}</p>
        <n-code :code="panelURL" word-wrap />
        <p class="hint">{{ t('ai.connect_ssh') }}</p>
        <n-code :code="sshCmd" language="bash" word-wrap />
        <n-space style="margin-top: 12px">
          <n-button type="primary" @click="tf = { name: 'Kimi / Claude / Codex', scope: 'read', ips: [], ttl_days: 90 }">{{ t('ai.new_token') }}</n-button>
          <n-button tag="a" href="/api/v1/mcp/skill">{{ t('ai.download_skill') }}</n-button>
        </n-space>
      </n-card>
      <n-card size="small" :title="t('ai.tokens')" style="margin-top: 16px">
        <template #header-extra><router-link :to="{ name: 'apidocs' }">{{ t('api.docs') }} →</router-link></template>
        <n-data-table size="small" :columns="tokenCols" :data="tokens" :row-key="(x: Token) => x.id" :scroll-x="900" />
      </n-card>
      <n-card v-if="info" size="small" :title="t('ai.tools', { n: info.tools.length })" style="margin-top: 16px">
        <n-data-table size="small" :columns="toolCols" :data="info.tools" :row-key="(x: Tool) => x.name" :max-height="420" />
        <p class="hint">{{ t('ai.prompts') }}: <code v-for="p in info.prompts" :key="p.name" style="margin-right: 8px">{{ p.name }}</code></p>
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
  <n-modal :show="!!selectedToken" @update:show="(v: boolean) => !v && (selectedToken = null)">
    <n-card v-if="selectedToken" :title="`${selectedToken.name} · ${t('ai.token_prompt')}`" style="width: min(780px, calc(100vw - 32px))" closable @close="selectedToken = null">
      <n-space align="center">
        <n-tag :type="scopeType(selectedToken.scope)">{{ t(`ai.scopes.${selectedToken.scope}`) }}</n-tag>
        <span class="hint">{{ t(`ai.scope_hint.${selectedToken.scope}`) }}</span>
      </n-space>
      <n-alert v-if="!selectedToken.recoverable" type="warning" :show-icon="false" style="margin-top: 16px">{{ t('ai.not_recoverable') }}</n-alert>
      <template v-else>
        <h4>{{ t('ai.full_token') }}</h4>
        <n-code :code="selectedSecret || '…'" style="display: block" word-wrap />
        <n-button size="small" style="margin-top: 8px" :disabled="!selectedSecret" @click="copy(selectedSecret)">{{ t('ai.copy_token') }}</n-button>
      </template>
      <h4>{{ t('ai.connection_prompt') }}</h4>
      <n-select v-model:value="promptClient" :options="promptClientOptions" style="width: 180px" />
      <pre style="margin: 12px 0 0; padding: 12px; max-height: 420px; overflow: auto; white-space: pre-wrap; word-break: break-word; background: rgba(128, 128, 128, .12); border-radius: 6px">{{ tokenPrompt() }}</pre>
      <n-alert type="info" :show-icon="false" style="margin-top: 12px">{{ t('ai.cloudflare_hint') }}</n-alert>
      <n-button type="primary" style="margin-top: 12px" @click="copy(tokenPrompt())">{{ t('ai.copy_prompt') }}</n-button>
    </n-card>
  </n-modal>
  <n-modal :show="!!editingExpiry" @update:show="(v: boolean) => !v && (editingExpiry = null)">
    <n-card v-if="editingExpiry" :title="t('ai.edit_expiry')" style="width: min(480px, calc(100vw - 32px))" closable @close="editingExpiry = null">
      <n-form label-placement="top" @submit.prevent="saveExpiry">
        <n-form-item :label="t('ai.expiry_date')" :feedback="t('ai.expiry_never')">
          <n-date-picker v-model:value="editingExpiry.value" type="datetime" clearable style="width: 100%" />
        </n-form-item>
      </n-form>
      <template #footer><n-space justify="end"><n-button type="primary" @click="saveExpiry">{{ t('common.save') }}</n-button></n-space></template>
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
