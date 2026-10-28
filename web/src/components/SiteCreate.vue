<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import {
  NModal, NCard, NForm, NFormItem, NInput, NInputNumber, NSelect, NSwitch, NRadioGroup, NRadioButton, NButton, NSpace, NCheckbox, NAlert,
  useMessage,
} from 'naive-ui'
import { get, post, ApiError } from '../api/client'
import type { InstalledApp, SiteType } from '../api/types'

type Preset = Partial<{ domain: string; type: SiteType; proxy_target: string; passthrough: boolean; remark: string }>
const props = defineProps<{ show: boolean; phpVersions: string[]; hasCF: boolean; preset?: Preset }>()
const emit = defineEmits<{ close: []; created: [taskId: string, domain: string] }>()
const { t } = useI18n()
const router = useRouter()
const msg = useMessage()

const blank = () => ({
  domain: '',
  aliases: '',
  type: 'php' as SiteType,
  php_version: '8.3',
  runtime_version: '',
  command: '',
  app_port: null as number | null,
  proxy_target: '',
  passthrough: false,
  dns: false,
  dns_proxied: false,
  ssl: 'http' as 'none' | 'http' | 'dns',
  wildcard: false,
  force_https: true,
  remark: '',
})
const f = ref(blank())
const busy = ref(false)
const runtimes = ref<InstalledApp[]>([])

watch(
  () => props.show,
  async (on) => {
    if (!on) return
    if (props.preset) f.value = { ...blank(), ...props.preset }
    try {
      runtimes.value = (await get<{ items: InstalledApp[] }>('/apps')).items.filter((a) => a.kind === 'runtime')
    } catch {
      runtimes.value = []
    }
  },
  { immediate: true },
)

const isProject = computed(() => ['node', 'python', 'java'].includes(f.value.type))
const runtimeOptions = computed(() =>
  runtimes.value.filter((r) => r.app === f.value.type).map((r) => ({ label: `${r.name} ${r.version}`, value: r.version })),
)
watch(
  () => f.value.type,
  () => {
    if (isProject.value && !runtimeOptions.value.some((o) => o.value === f.value.runtime_version)) {
      f.value.runtime_version = runtimeOptions.value[0]?.value ?? ''
    }
    if (f.value.type === 'proxy' && f.value.passthrough) f.value.ssl = 'none'
  },
)
watch(
  () => f.value.passthrough,
  (p) => p && (f.value.ssl = 'none'),
)
// Mirrors site.DefaultCommands (used when the field is left empty).
const defaultCommand: Record<string, string> = {
  node: '[ -f package.json ] && npm install --omit=dev --no-audit --no-fund; exec npm start',
  python: '[ -d .venv ] || python -m venv .venv; . .venv/bin/activate; [ -f requirements.txt ] && pip install -q -r requirements.txt; exec python app.py',
  java: 'exec java -jar app.jar --server.port=$PORT',
}
const phpOptions = computed(() =>
  props.phpVersions.map((v) => ({ label: `PHP ${v}${['7.4', '8.0', '8.1'].includes(v) ? ' (EOL)' : ''}`, value: v })),
)

async function submit() {
  busy.value = true
  try {
    const r = await post<{ task_id: string }>('/sites', {
      ...f.value,
      app_port: f.value.app_port ?? 0,
      aliases: f.value.aliases.split(/[\s,]+/).filter(Boolean),
      wildcard: f.value.ssl === 'dns' && f.value.wildcard,
    })
    emit('created', r.task_id, f.value.domain)
    f.value = blank()
  } catch (e) {
    msg.error(e instanceof ApiError ? e.text : String(e))
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <n-modal :show="show" @update:show="(v: boolean) => !v && emit('close')">
    <n-card :title="t('site.create')" style="width: min(640px, calc(100vw - 32px))" closable @close="emit('close')">
      <n-form label-placement="top">
        <n-form-item :label="t('site.domain')">
          <n-input v-model:value="f.domain" placeholder="example.com" />
        </n-form-item>
        <n-form-item :label="t('site.aliases')" :feedback="t('site.aliases_hint')">
          <n-input v-model:value="f.aliases" type="textarea" :rows="2" placeholder="www.example.com" />
        </n-form-item>
        <n-space>
          <n-form-item :label="t('site.type')">
            <n-radio-group v-model:value="f.type">
              <n-radio-button value="php">PHP</n-radio-button>
              <n-radio-button value="static">{{ t('site.static') }}</n-radio-button>
              <n-radio-button value="node">Node.js</n-radio-button>
              <n-radio-button value="python">Python</n-radio-button>
              <n-radio-button value="java">Java</n-radio-button>
              <n-radio-button value="proxy">{{ t('site.proxy') }}</n-radio-button>
            </n-radio-group>
          </n-form-item>
          <n-form-item v-if="f.type === 'php'" :label="t('site.php_version')">
            <n-select v-model:value="f.php_version" :options="phpOptions" style="width: 160px" />
          </n-form-item>
        </n-space>
        <template v-if="isProject">
          <n-alert v-if="!runtimeOptions.length" type="warning" :show-icon="false" style="margin-bottom: 12px">
            {{ t('site.need_runtime') }}
            <n-button text type="primary" @click="emit('close'); router.push({ name: 'appstore' })">{{ t('menu.appstore') }} →</n-button>
          </n-alert>
          <n-space>
            <n-form-item :label="t('site.runtime_version')">
              <n-select v-model:value="f.runtime_version" :options="runtimeOptions" style="width: 180px" />
            </n-form-item>
            <n-form-item :label="t('site.app_port')" :feedback="t('site.app_port_hint')">
              <n-input-number v-model:value="f.app_port" :min="1024" :max="65535" :show-button="false" :placeholder="t('site.auto')" style="width: 140px" />
            </n-form-item>
          </n-space>
          <n-form-item :label="t('site.command')" :feedback="t('site.command_hint')">
            <n-input v-model:value="f.command" :placeholder="defaultCommand[f.type]" />
          </n-form-item>
        </template>
        <template v-if="f.type === 'proxy'">
          <n-form-item :label="t('site.proxy_target')" :feedback="f.passthrough ? t('site.passthrough_hint') : t('site.proxy_target_hint')">
            <n-input v-model:value="f.proxy_target" :placeholder="f.passthrough ? '127.0.0.1:8443' : 'http://127.0.0.1:8081'" />
          </n-form-item>
          <n-checkbox v-model:checked="f.passthrough" style="margin-bottom: 12px">{{ t('site.passthrough') }}</n-checkbox>
        </template>
        <n-form-item :label="t('site.dns')" :feedback="hasCF ? t('site.dns_hint') : t('site.need_cf')">
          <n-space align="center">
            <n-switch v-model:value="f.dns" :disabled="!hasCF" />
            <n-checkbox v-if="f.dns" v-model:checked="f.dns_proxied">{{ t('site.cf_proxied') }}</n-checkbox>
          </n-space>
        </n-form-item>
        <n-form-item label="SSL" :feedback="f.ssl === 'http' ? t('site.ssl_http_hint') : f.ssl === 'dns' ? t('site.ssl_dns_hint') : ''">
          <n-radio-group v-model:value="f.ssl" :disabled="f.type === 'proxy' && f.passthrough">
            <n-radio-button value="none">{{ t('site.ssl_none') }}</n-radio-button>
            <n-radio-button value="http">HTTP-01</n-radio-button>
            <n-radio-button value="dns" :disabled="!hasCF">DNS-01 (Cloudflare)</n-radio-button>
          </n-radio-group>
        </n-form-item>
        <n-space v-if="f.ssl !== 'none'">
          <n-checkbox v-model:checked="f.force_https">{{ t('site.force_https') }}</n-checkbox>
          <n-checkbox v-if="f.ssl === 'dns'" v-model:checked="f.wildcard">{{ t('site.wildcard') }}</n-checkbox>
        </n-space>
        <n-form-item :label="t('site.remark')" style="margin-top: 12px">
          <n-input v-model:value="f.remark" />
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="emit('close')">{{ t('common.cancel') }}</n-button>
          <n-button
            type="primary"
            :loading="busy"
            :disabled="!f.domain || (isProject && !f.runtime_version) || (f.type === 'proxy' && !f.proxy_target)"
            @click="submit"
          >{{ t('common.confirm') }}</n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>
</template>
