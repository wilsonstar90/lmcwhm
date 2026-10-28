<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NInput, NSpace, NAlert } from 'naive-ui'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { post, ApiError } from '../api/client'

// container: "" opens a host shell, otherwise `docker exec` into it.
const props = defineProps<{ container?: string }>()
const { t } = useI18n()

const password = ref('')
const error = ref('')
const busy = ref(false)
const connected = ref(false)
const closed = ref(false)
const host = ref<HTMLElement | null>(null)
let term: Terminal | null = null
let ws: WebSocket | null = null
let ro: ResizeObserver | null = null

function send(msg: object) {
  if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify(msg))
}

async function connect() {
  busy.value = true
  error.value = ''
  try {
    const r = await post<{ ticket: string }>('/terminal/ticket', { password: password.value, container: props.container ?? '' })
    password.value = ''
    open(r.ticket)
  } catch (e) {
    error.value = e instanceof ApiError ? e.text : String(e)
  } finally {
    busy.value = false
  }
}

function open(ticket: string) {
  closed.value = false
  connected.value = true
  term?.dispose()
  term = new Terminal({ cursorBlink: true, fontSize: 13, fontFamily: 'Menlo, Consolas, "DejaVu Sans Mono", monospace', theme: { background: '#1e1e1e' } })
  const fit = new FitAddon()
  term.loadAddon(fit)
  requestAnimationFrame(() => {
    term!.open(host.value!)
    fit.fit()
    term!.focus()
    const scheme = location.protocol === 'https:' ? 'wss' : 'ws'
    ws = new WebSocket(`${scheme}://${location.host}/api/v1/terminal/ws?ticket=${encodeURIComponent(ticket)}`)
    ws.binaryType = 'arraybuffer'
    ws.onopen = () => send({ t: 'r', c: term!.cols, r: term!.rows })
    ws.onmessage = (ev) => term!.write(new Uint8Array(ev.data as ArrayBuffer))
    ws.onclose = () => {
      closed.value = true
      term?.write(`\r\n\x1b[33m${t('term.closed')}\x1b[0m\r\n`)
    }
    term!.onData((d) => send({ t: 'i', d }))
    term!.onResize(({ cols, rows }) => send({ t: 'r', c: cols, r: rows }))
    ro = new ResizeObserver(() => fit.fit())
    ro.observe(host.value!)
  })
}

function disconnect() {
  ws?.close()
  ro?.disconnect()
  term?.dispose()
  term = null
  connected.value = false
}
onBeforeUnmount(disconnect)
</script>

<template>
  <div class="wrap">
    <div v-if="!connected" class="login">
      <p>{{ container ? t('term.verify_container', { name: container }) : t('term.verify') }}</p>
      <n-alert v-if="error" type="error" :show-icon="false" style="margin-bottom: 12px">{{ error }}</n-alert>
      <n-space>
        <n-input
          v-model:value="password"
          type="password"
          show-password-on="click"
          :placeholder="t('login.password')"
          style="width: 240px"
          @keyup.enter="connect"
        />
        <n-button type="primary" :loading="busy" :disabled="!password" @click="connect">{{ t('term.connect') }}</n-button>
      </n-space>
    </div>
    <template v-else>
      <n-space style="margin-bottom: 8px" justify="end">
        <n-button v-if="closed" size="small" @click="connected = false">{{ t('term.reconnect') }}</n-button>
        <n-button size="small" @click="disconnect">{{ t('term.disconnect') }}</n-button>
      </n-space>
      <div ref="host" class="term" />
    </template>
  </div>
</template>

<style scoped>
.wrap {
  height: 100%;
  display: flex;
  flex-direction: column;
}
.login p {
  margin: 0 0 12px;
  opacity: 0.8;
}
.term {
  flex: 1;
  min-height: 360px;
  background: #1e1e1e;
  padding: 6px;
  border-radius: 4px;
}
</style>
