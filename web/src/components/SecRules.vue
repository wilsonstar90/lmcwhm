<script setup lang="ts">
// 專屬規則 (as aaPanel): rules for known CMS weaknesses, on every website
// with protection on. Each can be switched, edited and (custom ones) deleted.
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NAlert, NButton, NDataTable, NPopconfirm, NRadioButton, NRadioGroup, NSpace, NSwitch, NTag, useMessage, type DataTableColumns } from 'naive-ui'
import { get, post, put, ApiError } from '../api/client'
import RuleEditor, { type Rule } from './RuleEditor.vue'

const props = defineProps<{ paranoia: number }>()
const emit = defineEmits<{ changed: [] }>()
const { t, te } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))
const rules = ref<Rule[]>([])
const rulesOn = ref(false)
const loading = ref(false)
const editing = ref<Rule | null>(null)

async function load() {
  loading.value = true
  try {
    const r = await get<{ rules: Rule[]; rules_on: boolean }>('/protect/rules')
    rules.value = r.rules
    rulesOn.value = r.rules_on
  } catch (e) {
    fail(e)
  } finally {
    loading.value = false
  }
}
onMounted(load)

async function setRulesOn(on: boolean) {
  try {
    const cur = await get<{ settings: Record<string, unknown> }>('/protect/settings')
    await put('/protect/settings', { ...cur.settings, rules_on: on })
    rulesOn.value = on
    emit('changed')
  } catch (e) {
    fail(e)
  }
}
const act = async (path: string, body?: unknown) => {
  try {
    await post(path, body)
    await load()
    emit('changed')
  } catch (e) {
    fail(e)
  }
}
// Built-in rules show their name and description in the panel's language.
const local = (r: Rule) => r.builtin && !r.renamed && te(`rules.builtin.${r.code}.name`)
const name = (r: Rule) => (local(r) ? t(`rules.builtin.${r.code}.name`) : r.name)
const desc = (r: Rule) => (local(r) ? t(`rules.builtin.${r.code}.desc`) : r.description)
// Editing a built-in rule keeps its stored (English) text unless it was changed.
function edit(r: Rule) {
  editing.value = { ...r, name: name(r), description: desc(r) }
  shown = { name: name(r), description: desc(r), stored: { name: r.name, description: r.description } }
}
let shown: { name: string; description: string; stored: { name: string; description: string } } | null = null
function beforeSave(x: Rule): Rule {
  if (x.builtin && shown && x.name === shown.name && x.description === shown.description) return { ...x, ...shown.stored }
  return x
}
function blank(): Rule {
  shown = null
  return { id: 0, code: '', cms: '', name: '', description: '', enabled: true, builtin: false, match_all: true, conditions: [], level: 1, action: 'block', status: 403 }
}
const columns = computed<DataTableColumns<Rule>>(() => [
  { title: () => t('rules.id'), key: 'code', width: 100, render: (r) => h('code', r.code) },
  { title: 'CMS', key: 'cms', width: 110 },
  {
    title: () => t('rules.name'),
    key: 'name',
    minWidth: 260,
    render: (r) => h('span', [name(r), r.modified ? h(NTag, { size: 'tiny', type: 'warning', bordered: false, style: 'margin-left:6px' }, () => t('rules.modified')) : null, !r.builtin ? h(NTag, { size: 'tiny', bordered: false, style: 'margin-left:6px' }, () => t('rules.custom')) : null]),
  },
  { title: () => t('rules.description'), key: 'description', minWidth: 260, ellipsis: { tooltip: true }, render: desc },
  {
    title: () => t('rules.level'),
    key: 'level',
    width: 110,
    render: (r) =>
      h(NSpace, { size: 4, align: 'center', wrap: false }, () => [
        h(NTag, { size: 'small', bordered: false, type: r.level <= props.paranoia ? 'info' : 'default' }, () => `PL${r.level}`),
        r.level > props.paranoia ? h('span', { style: 'font-size:12px;opacity:.6' }, t('rules.inactive')) : null,
      ]),
  },
  {
    title: () => t('rules.action'),
    key: 'action',
    width: 190,
    render: (r) =>
      h(NRadioGroup, { size: 'small', value: r.action, 'onUpdate:value': (a: string) => act(`/protect/rules/${r.id}/action`, { action: a }) }, () => [
        h(NRadioButton, { value: 'block' }, () => (r.status && r.status !== 403 ? `${t('rules.block')} ${r.status}` : t('rules.block'))),
        h(NRadioButton, { value: 'detect' }, () => t('rules.detect')),
      ]),
  },
  { title: () => t('common.status'), key: 'enabled', width: 80, render: (r) => h(NSwitch, { size: 'small', value: r.enabled, 'onUpdate:value': (on: boolean) => act(`/protect/rules/${r.id}/enabled`, { enabled: on }) }) },
  {
    title: '',
    key: 'actions',
    width: 170,
    render: (r) =>
      h(NSpace, { size: 6 }, () => [
        h(NButton, { size: 'tiny', onClick: () => edit(r) }, () => t('common.edit')),
        r.builtin && r.modified
          ? h(NPopconfirm, { onPositiveClick: () => act(`/protect/rules/${r.id}/reset`) }, { trigger: () => h(NButton, { size: 'tiny' }, () => t('rules.reset')), default: () => t('rules.reset_confirm') })
          : null,
        !r.builtin
          ? h(NPopconfirm, { onPositiveClick: () => act(`/protect/rules/${r.id}/delete`) }, { trigger: () => h(NButton, { size: 'tiny', type: 'error', ghost: true }, () => t('common.delete')), default: () => t('rules.delete_confirm') })
          : null,
      ]),
  },
])
</script>

<template>
  <n-space justify="space-between" align="center" style="margin-bottom: 12px">
    <n-space align="center" :size="10">
      <b>{{ t('rules.title') }}</b>
      <n-switch :value="rulesOn" @update:value="setRulesOn" />
    </n-space>
    <n-button type="primary" size="small" @click="editing = blank()">{{ t('rules.add') }}</n-button>
  </n-space>
  <n-alert v-if="!rulesOn" type="info" :show-icon="false" style="margin-bottom: 12px">{{ t('rules.off_hint') }}</n-alert>
  <n-data-table size="small" :columns="columns" :data="rules" :loading="loading" :row-key="(r: Rule) => r.id" :row-class-name="(r: Rule) => (r.level > paranoia ? 'dim' : '')" :pagination="{ pageSize: 100 }" :scroll-x="1300" />
  <ul class="notes">
    <li>{{ t('rules.note_scope') }}</li>
    <li>{{ t('rules.note_log') }}</li>
  </ul>
  <RuleEditor :rule="editing" :before-save="beforeSave" @close="editing = null" @saved="editing = null; load(); emit('changed')" />
</template>

<style scoped>
:deep(.dim td) {
  opacity: 0.55;
}
.notes {
  font-size: 12px;
  opacity: 0.65;
  margin: 10px 0 0;
  padding-left: 18px;
}
</style>
