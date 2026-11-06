<script setup lang="ts">
// Who may connect to a database (MySQL / MariaDB, like aaPanel's 权限):
// this server only, everyone, or listed IPv4 / IPv6 addresses. Remote access
// publishes the engine's port and adds a firewall rule; back to local, the
// rule the panel added goes.
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { NAlert, NButton, NCard, NForm, NFormItem, NInput, NModal, NSelect, NSpace, useMessage } from 'naive-ui'
import { get, put, ApiError } from '../api/client'
import type { Database } from '../api/types'

interface Access { mode: 'local' | 'all' | 'ips'; ips: string[]; rule_id: number; rule_owned: boolean; public: boolean }

const props = defineProps<{ db: Database | null; engine: string }>()
const emit = defineEmits<{ close: []; task: [id: string, title: string] }>()
const { t } = useI18n()
const router = useRouter()
const msg = useMessage()

const cur = ref<Access | null>(null)
const fw = ref({ available: true, enabled: true })
const mode = ref<Access['mode']>('local')
const ipsText = ref('')
const saving = ref(false)

watch(() => props.db?.id, async (id) => {
  cur.value = null
  if (!id) return
  try {
    const r = await get<{ access: Access; firewall: { available: boolean; enabled: boolean } }>(`/databases/${id}/access`)
    cur.value = r.access
    fw.value = r.firewall
    mode.value = r.access.mode
    ipsText.value = r.access.ips.join('\n')
  } catch (e) {
    msg.error(e instanceof ApiError ? e.text : String(e))
  }
}, { immediate: true })

const modeOptions = computed(() => [
  { label: t('dba.local'), value: 'local' },
  { label: t('dba.all'), value: 'all' },
  { label: t('dba.ips'), value: 'ips' },
])
const ips = computed(() => ipsText.value.split(/[\s,]+/).map((x) => x.trim()).filter(Boolean))
const changed = computed(() => !!cur.value && (mode.value !== cur.value.mode || (mode.value === 'ips' && ips.value.join(',') !== cur.value.ips.join(','))))
// The engine restarts when its port starts or may stop being published.
const restarts = computed(() => !!cur.value && ((mode.value !== 'local' && !cur.value.public) || (mode.value === 'local' && cur.value.mode !== 'local')))

async function save() {
  const d = props.db
  if (!d) return
  saving.value = true
  try {
    const r = await put<{ task_id: string }>(`/databases/${d.id}/access`, { mode: mode.value, ips: mode.value === 'ips' ? ips.value : [] })
    emit('task', r.task_id, `${t('dba.title')} · ${d.name}`)
    emit('close')
  } catch (e) {
    msg.error(e instanceof ApiError ? e.text : String(e))
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <n-modal :show="!!db" @update:show="(v: boolean) => !v && emit('close')">
    <n-card v-if="db" :title="`${t('dba.title')} · ${db.name}`" style="width: min(520px, calc(100vw - 24px))" closable @close="emit('close')">
      <n-form label-placement="top">
        <n-form-item :label="t('dba.mode')">
          <n-select v-model:value="mode" :options="modeOptions" />
        </n-form-item>
        <n-form-item v-if="mode === 'ips'" :label="t('dba.ip_list')" :feedback="t('dba.ip_hint')">
          <n-input v-model:value="ipsText" type="textarea" :autosize="{ minRows: 3, maxRows: 8 }" placeholder="203.0.113.7&#10;2001:db8::7&#10;10.0.0.0/24" />
        </n-form-item>
      </n-form>
      <p class="hint">{{ t(`dba.explain_${mode}`, { port: db.port, engine }) }}</p>
      <n-alert v-if="mode !== 'local' && (!fw.available || !fw.enabled)" type="warning" :show-icon="false" class="gap">
        {{ t('dba.fw_off') }}
        <n-button text type="primary" @click="emit('close'); router.push({ path: '/security', query: { tab: 'firewall' } })">{{ t('dba.fw_open') }} →</n-button>
      </n-alert>
      <n-alert v-if="restarts && changed" type="info" :show-icon="false" class="gap">{{ t('dba.restart', { engine }) }}</n-alert>
      <p v-if="cur && cur.rule_id && cur.mode !== 'local'" class="hint">
        {{ t(cur.rule_owned ? 'dba.rule_owned' : 'dba.rule_existing', { id: cur.rule_id }) }}
      </p>
      <template #footer>
        <n-space justify="end">
          <n-button @click="emit('close')">{{ t('common.cancel') }}</n-button>
          <n-button type="primary" :loading="saving" :disabled="!changed || (mode === 'ips' && !ips.length)" @click="save">{{ t('common.confirm') }}</n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>
</template>

<style scoped>
.hint {
  font-size: 12px;
  opacity: 0.7;
  margin: 0 0 10px;
  line-height: 1.6;
}
.gap {
  margin-bottom: 10px;
}
</style>
