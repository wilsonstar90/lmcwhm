<script setup lang="ts">
// 端口轉發: TCP / UDP from a port of this server to a local port (e.g. a
// container) or another IPv4 host.
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NCard, NDataTable, NForm, NFormItem, NInput, NInputNumber, NModal, NPopconfirm, NRadioButton, NRadioGroup, NSelect,
  NSpace, NSwitch, NTag, useMessage, type DataTableColumns,
} from 'naive-ui'
import { get, post, put, ApiError } from '../api/client'
import type { PortForward } from '../api/types'
import { bytes } from '../format'
import IpGeo from './IpGeo.vue'

const { t } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))
const items = ref<PortForward[]>([])
const containers = ref<{ container: string; port: number }[]>([])
const available = ref(true)
const loading = ref(false)
const edit = ref<PortForward | null>(null)
const where = ref<'local' | 'docker' | 'remote'>('local')

async function load() {
  loading.value = true
  try {
    const r = await get<{ items: PortForward[]; containers: { container: string; port: number }[]; available: boolean }>('/forwards')
    items.value = r.items
    containers.value = r.containers
    available.value = r.available
  } catch (e) {
    fail(e)
  } finally {
    loading.value = false
  }
}
onMounted(load)

const blank = (): PortForward => ({ id: 0, proto: 'tcp', listen: 0, target_host: '', target_port: 0, source: '', remark: '', enabled: true, created_at: 0, packets: 0, bytes: 0 })
function open(f?: PortForward) {
  edit.value = f ? { ...f } : blank()
  where.value = f?.target_host ? 'remote' : containers.value.some((c) => c.port === f?.target_port) ? 'docker' : 'local'
}
async function save() {
  const f = { ...edit.value!, target_host: where.value === 'remote' ? edit.value!.target_host : '' }
  try {
    if (f.id) await put(`/forwards/${f.id}`, f)
    else await post('/forwards', f)
    edit.value = null
    msg.success(t('common.saved'))
    load()
  } catch (e) {
    fail(e)
  }
}
async function toggle(f: PortForward, on: boolean) {
  try {
    await put(`/forwards/${f.id}`, { ...f, enabled: on })
    load()
  } catch (e) {
    fail(e)
  }
}
async function remove(f: PortForward) {
  try {
    await post(`/forwards/${f.id}/delete`)
    load()
  } catch (e) {
    fail(e)
  }
}
const containerOf = (port: number) => containers.value.find((c) => c.port === port)?.container
const dest = (f: PortForward) => (f.target_host ? `${f.target_host}:${f.target_port}` : `127.0.0.1:${f.target_port}`)
const dockerOptions = computed(() => containers.value.map((c) => ({ label: `${c.container} :${c.port}`, value: c.port })))
const protoLabel = (p: string) => (p === 'both' ? 'TCP+UDP' : p.toUpperCase())
const columns: DataTableColumns<PortForward> = [
  { title: () => t('fwd.listen'), key: 'listen', width: 100, render: (f) => h('code', String(f.listen)) },
  { title: () => t('fw.proto'), key: 'proto', width: 90, render: (f) => protoLabel(f.proto) },
  {
    title: () => t('fwd.target'),
    key: 'target',
    minWidth: 220,
    render: (f) => h(NSpace, { size: 4, align: 'center' }, () => [h('code', dest(f)), containerOf(f.target_port) && !f.target_host ? h(NTag, { size: 'tiny', type: 'info' }, () => `🐳 ${containerOf(f.target_port)}`) : null]),
  },
  { title: () => t('fw.source'), key: 'source', width: 240, render: (f) => (f.source ? h(IpGeo, { ip: f.source }) : t('fw.anywhere')) },
  { title: () => t('fwd.traffic'), key: 'bytes', width: 150, render: (f) => `${bytes(f.bytes)} · ${f.packets.toLocaleString()} pkt` },
  { title: () => t('site.remark'), key: 'remark', ellipsis: { tooltip: true } },
  { title: () => t('cron.enabled'), key: 'enabled', width: 80, render: (f) => h(NSwitch, { size: 'small', value: f.enabled, 'onUpdate:value': (v: boolean) => toggle(f, v) }) },
  {
    title: '',
    key: 'actions',
    width: 140,
    render: (f) =>
      h(NSpace, { size: 6 }, () => [
        h(NButton, { size: 'tiny', onClick: () => open(f) }, () => t('common.edit')),
        h(NPopconfirm, { onPositiveClick: () => remove(f) }, { trigger: () => h(NButton, { size: 'tiny', type: 'error', ghost: true }, () => t('common.delete')), default: () => t('fwd.delete_confirm', { port: f.listen }) }),
      ]),
  },
]
const ready = computed(() => !!edit.value && edit.value.listen > 0 && edit.value.target_port > 0 && (where.value !== 'remote' || !!edit.value.target_host.trim()))
</script>

<template>
  <n-alert v-if="!available" type="error" :show-icon="false" style="margin-bottom: 12px">{{ t('fw.no_nft') }}</n-alert>
  <n-space justify="space-between" style="margin-bottom: 12px">
    <n-button type="primary" :disabled="!available" @click="open()">{{ t('fwd.add') }}</n-button>
    <n-button @click="load">{{ t('common.refresh') }}</n-button>
  </n-space>
  <n-data-table size="small" :columns="columns" :data="items" :loading="loading" :row-key="(f: PortForward) => f.id" :scroll-x="1100" />
  <ul class="notes">
    <li>{{ t('fwd.note_proto') }}</li>
    <li>{{ t('fwd.note_firewall') }}</li>
    <li>{{ t('fwd.note_remote') }}</li>
  </ul>

  <n-modal :show="!!edit" @update:show="(v: boolean) => !v && (edit = null)">
    <n-card v-if="edit" :title="edit.id ? t('common.edit') : t('fwd.add')" style="width: min(560px, calc(100vw - 32px))" closable @close="edit = null">
      <n-form label-placement="top">
        <n-space>
          <n-form-item :label="t('fw.proto')">
            <n-radio-group v-model:value="edit.proto">
              <n-radio-button value="tcp">TCP</n-radio-button>
              <n-radio-button value="udp">UDP</n-radio-button>
              <n-radio-button value="both">TCP+UDP</n-radio-button>
            </n-radio-group>
          </n-form-item>
          <n-form-item :label="t('fwd.listen')">
            <n-input-number v-model:value="edit.listen" :min="1" :max="65535" :show-button="false" style="width: 120px" />
          </n-form-item>
        </n-space>
        <n-form-item :label="t('fwd.to')">
          <n-radio-group v-model:value="where">
            <n-radio-button value="local">{{ t('fwd.where.local') }}</n-radio-button>
            <n-radio-button value="docker" :disabled="!containers.length">{{ t('fwd.where.docker') }}</n-radio-button>
            <n-radio-button value="remote">{{ t('fwd.where.remote') }}</n-radio-button>
          </n-radio-group>
        </n-form-item>
        <n-space>
          <n-form-item v-if="where === 'remote'" :label="t('fwd.host')">
            <n-input v-model:value="edit.target_host" placeholder="10.0.0.5" style="width: 200px" />
          </n-form-item>
          <n-form-item v-if="where === 'docker'" :label="t('route.container')">
            <n-select :value="edit.target_port || null" :options="dockerOptions" style="width: 260px" @update:value="(p: number) => (edit!.target_port = p)" />
          </n-form-item>
          <n-form-item v-else :label="t('route.port')">
            <n-input-number v-model:value="edit.target_port" :min="1" :max="65535" :show-button="false" style="width: 120px" />
          </n-form-item>
        </n-space>
        <n-form-item :label="t('fw.source')" :feedback="t('fw.source_hint')">
          <n-input v-model:value="edit.source" :placeholder="t('fw.anywhere')" />
        </n-form-item>
        <n-form-item :label="t('site.remark')"><n-input v-model:value="edit.remark" /></n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="edit = null">{{ t('common.cancel') }}</n-button>
          <n-button type="primary" :disabled="!ready" @click="save">{{ t('common.save') }}</n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>
</template>

<style scoped>
.notes {
  font-size: 12px;
  opacity: 0.65;
  margin: 10px 0 0;
  padding-left: 18px;
}
</style>
