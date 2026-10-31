<script setup lang="ts">
// Dedicated rule editor: conditions picked from lists (visual), or the same
// conditions as JSON (code); below, how the edge will read the rule.
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NCard, NCheckbox, NDynamicTags, NForm, NFormItem, NInput, NModal, NRadioButton, NRadioGroup, NSelect, NSpace, NSwitch, NTag,
  useMessage,
} from 'naive-ui'
import { post, put, ApiError } from '../api/client'
import CodeEditor from './CodeEditor.vue'

export interface Cond { field: string; op: string; negate: boolean; values: string[] }
export interface Rule {
  id: number; code: string; cms: string; name: string; description: string; enabled: boolean; builtin: boolean
  match_all: boolean; conditions: Cond[]; modified?: boolean; renamed?: boolean
  level: number; action: 'block' | 'detect'; status: number
}

const props = defineProps<{ rule: Rule | null; beforeSave?: (r: Rule) => Rule }>()
const emit = defineEmits<{ close: []; saved: [] }>()
const { t } = useI18n()
const msg = useMessage()

const r = ref<Rule | null>(null)
const view = ref<'visual' | 'code'>('visual')
const code = ref('')
const codeErr = ref('')
const preview = ref('')
const saving = ref(false)

watch(
  () => props.rule,
  (v) => {
    r.value = v ? { level: 1, action: 'block', status: 403, ...JSON.parse(JSON.stringify(v)) } : null
    view.value = 'visual'
    codeErr.value = ''
    if (r.value && !r.value.conditions.length) r.value.conditions.push(blank())
  },
  { immediate: true },
)
const statusOptions = [400, 401, 403, 404, 405, 406, 410, 418, 429, 444, 451, 500, 502, 503].map((c) => ({ label: c === 444 ? '444 (close)' : String(c), value: c }))
const blank = (): Cond => ({ field: 'path', op: 'contains', negate: false, values: [] })

const fields = ['path', 'url', 'query', 'method', 'ua', 'referer', 'cookie', 'body']
const ops = ['contains', 'equals', 'prefix', 'suffix', 'regex']
const fieldOptions = computed(() => fields.map((f) => ({ label: t(`rules.fields.${f}`), value: f })))
const opOptions = computed(() => ops.map((o) => ({ label: t(`rules.ops.${o}`), value: o })))

// Switching views carries the conditions across.
function toCode() {
  code.value = JSON.stringify(r.value!.conditions, null, 2)
  codeErr.value = ''
}
function fromCode(): boolean {
  try {
    const v = JSON.parse(code.value)
    if (!Array.isArray(v)) throw new Error('[]')
    r.value!.conditions = v.map((c) => ({ field: String(c.field ?? ''), op: String(c.op ?? ''), negate: !!c.negate, values: Array.isArray(c.values) ? c.values.map(String) : [] }))
    codeErr.value = ''
    return true
  } catch (e) {
    codeErr.value = t('rules.bad_json', { err: String((e as Error).message ?? e) })
    return false
  }
}
function switchView(v: 'visual' | 'code') {
  if (v === 'code') toCode()
  else if (!fromCode()) return
  view.value = v
}

let timer: number | undefined
watch(
  () => r.value && JSON.stringify([r.value.match_all, r.value.conditions, r.value.code, r.value.action, r.value.status]),
  () => {
    clearTimeout(timer)
    timer = window.setTimeout(async () => {
      if (!r.value) return
      try {
        preview.value = (await post<{ haproxy: string }>('/protect/rules/preview', r.value)).haproxy
      } catch {
        preview.value = ''
      }
    }, 300)
  },
  { immediate: true },
)

async function save() {
  if (!r.value) return
  if (view.value === 'code' && !fromCode()) return
  saving.value = true
  try {
    const base = props.beforeSave ? props.beforeSave(r.value) : r.value
    const body = { ...base, conditions: r.value.conditions.map((c) => ({ ...c, values: c.values.map((x) => x.trim()).filter(Boolean) })) }
    if (r.value.id) await put(`/protect/rules/${r.value.id}`, body)
    else await post('/protect/rules', body)
    msg.success(t('common.saved'))
    emit('saved')
  } catch (e) {
    msg.error(e instanceof ApiError ? e.text : String(e))
  } finally {
    saving.value = false
  }
}
const valid = computed(() => !!r.value?.name.trim() && r.value.conditions.length > 0 && r.value.conditions.every((c) => c.values.some((x) => x.trim())))
const placeholderFor = (c: Cond) => t(`rules.examples.${c.field}`)
</script>

<template>
  <n-modal v-if="r" :show="true" @update:show="(v: boolean) => !v && emit('close')">
    <n-card :title="r.id ? t('rules.edit') : t('rules.add')" style="width: min(860px, calc(100vw - 32px))" closable @close="emit('close')">
      <n-form label-placement="top" size="small">
        <n-space :wrap="true" :size="12">
          <n-form-item :label="t('rules.name')" style="width: 360px"><n-input v-model:value="r.name" /></n-form-item>
          <n-form-item :label="t('rules.cms')" style="width: 160px"><n-input v-model:value="r.cms" placeholder="wordpress" /></n-form-item>
          <n-form-item :label="t('rules.enabled')"><n-switch v-model:value="r.enabled" /></n-form-item>
        </n-space>
        <n-space :wrap="true" :size="12">
          <n-form-item :label="t('rules.level')" style="width: 300px">
            <n-select v-model:value="r.level" :options="[1, 2, 3, 4].map((n) => ({ label: t(`waf.pl.${n}`), value: n }))" />
          </n-form-item>
          <n-form-item :label="t('rules.action')">
            <n-radio-group v-model:value="r.action">
              <n-radio-button value="block">{{ t('rules.block') }}</n-radio-button>
              <n-radio-button value="detect">{{ t('rules.detect') }}</n-radio-button>
            </n-radio-group>
          </n-form-item>
          <n-form-item v-if="r.action === 'block'" :label="t('rules.status')" style="width: 140px">
            <n-select v-model:value="r.status" :options="statusOptions" />
          </n-form-item>
        </n-space>
        <n-form-item :label="t('rules.description')"><n-input v-model:value="r.description" type="textarea" :autosize="{ minRows: 1, maxRows: 3 }" /></n-form-item>
      </n-form>

      <n-radio-group :value="view" size="small" style="margin-bottom: 12px" @update:value="switchView">
        <n-radio-button value="visual">{{ t('rules.visual') }}</n-radio-button>
        <n-radio-button value="code">{{ t('rules.code') }}</n-radio-button>
      </n-radio-group>

      <template v-if="view === 'visual'">
        <n-space align="center" :size="8" style="margin-bottom: 8px">
          <span>{{ t('rules.block_when') }}</span>
          <n-radio-group v-model:value="r.match_all" size="small">
            <n-radio-button :value="true">{{ t('rules.all') }}</n-radio-button>
            <n-radio-button :value="false">{{ t('rules.any') }}</n-radio-button>
          </n-radio-group>
          <span>{{ t('rules.conditions_match') }}</span>
        </n-space>
        <div v-for="(c, i) in r.conditions" :key="i" class="cond">
          <n-tag size="small" :bordered="false" class="n">{{ i + 1 }}</n-tag>
          <n-select v-model:value="c.field" :options="fieldOptions" size="small" style="width: 150px" />
          <n-checkbox v-model:checked="c.negate">{{ t('rules.negate') }}</n-checkbox>
          <n-select v-model:value="c.op" :options="opOptions" size="small" style="width: 120px" />
          <n-dynamic-tags v-model:value="c.values" size="small" class="values" :input-props="{ placeholder: placeholderFor(c) }" />
          <n-button size="tiny" quaternary :disabled="r.conditions.length === 1" @click="r.conditions.splice(i, 1)">✕</n-button>
        </div>
        <n-button size="small" dashed :disabled="r.conditions.length >= 10" @click="r.conditions.push(blank())">{{ t('rules.add_cond') }}</n-button>
        <p class="hint">{{ t('rules.values_hint') }}</p>
      </template>
      <template v-else>
        <p class="hint">{{ t('rules.code_hint') }}</p>
        <CodeEditor v-model="code" language="json" height="260px" />
        <n-alert v-if="codeErr" type="error" :show-icon="false" style="margin-top: 8px">{{ codeErr }}</n-alert>
      </template>

      <div class="preview">
        <b>{{ t('rules.preview') }}</b>
        <CodeEditor :model-value="preview || t('rules.preview_none')" language="shell" height="140px" readonly />
      </div>

      <template #footer>
        <n-space justify="end">
          <n-button @click="emit('close')">{{ t('common.cancel') }}</n-button>
          <n-button type="primary" :loading="saving" :disabled="!valid" @click="save">{{ t('common.save') }}</n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>
</template>

<style scoped>
.cond {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  padding: 8px;
  margin-bottom: 8px;
  border: 1px solid rgba(128, 128, 128, 0.2);
  border-radius: 6px;
}
.values {
  flex: 1;
  min-width: 220px;
}
.hint {
  font-size: 12px;
  opacity: 0.65;
  margin: 8px 0;
}
.preview {
  margin-top: 16px;
}
.preview b {
  display: block;
  font-size: 13px;
  margin-bottom: 6px;
}
</style>
