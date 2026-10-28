<script setup lang="ts">
import { h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NCard, NDataTable, NForm, NFormItem, NInput, NModal, NPopconfirm, NSpace, useMessage, type DataTableColumns } from 'naive-ui'
import { get, post, ApiError } from '../api/client'

// The registries docker is logged in to (private images pull from these).
interface Registry { server: string; username: string }
const emit = defineEmits<{ changed: [] }>()
const { t } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))

const items = ref<Registry[]>([])
const loading = ref(false)
const form = ref<{ server: string; username: string; password: string } | null>(null)
const busy = ref(false)

async function load() {
  loading.value = true
  try {
    items.value = (await get<{ items: Registry[] }>('/docker/registries')).items
  } catch (e) {
    fail(e)
  } finally {
    loading.value = false
  }
}
onMounted(load)
defineExpose({ load })

async function login() {
  busy.value = true
  try {
    await post('/docker/registries', form.value)
    msg.success(t('common.done'))
    form.value = null
    load()
    emit('changed')
  } catch (e) {
    fail(e)
  } finally {
    busy.value = false
  }
}

async function logout(r: Registry) {
  try {
    await post('/docker/registries/logout', { server: r.server })
    load()
    emit('changed')
  } catch (e) {
    fail(e)
  }
}

const columns: DataTableColumns<Registry> = [
  { title: () => t('docker.reg.server'), key: 'server', render: (r) => h('b', r.server) },
  { title: () => t('docker.reg.username'), key: 'username', render: (r) => r.username || '—' },
  {
    title: '', key: 'a', width: 100,
    render: (r) =>
      h(NPopconfirm, { onPositiveClick: () => logout(r) }, {
        trigger: () => h(NButton, { size: 'tiny' }, () => t('docker.reg.logout')),
        default: () => t('docker.reg.logout_confirm', { server: r.server }),
      }),
  },
]
</script>

<template>
  <n-space justify="space-between" style="margin-bottom: 12px">
    <n-button type="primary" size="small" @click="form = { server: '', username: '', password: '' }">{{ t('docker.reg.login') }}</n-button>
    <n-button size="small" @click="load">{{ t('common.refresh') }}</n-button>
  </n-space>
  <n-data-table size="small" :columns="columns" :data="items" :loading="loading" />

  <n-modal :show="!!form" @update:show="(v: boolean) => !v && (form = null)">
    <n-card v-if="form" :title="t('docker.reg.login')" style="width: min(480px, calc(100vw - 32px))" closable @close="form = null">
      <n-form label-placement="top">
        <n-form-item :label="t('docker.reg.server')" :feedback="t('docker.reg.server_hint')">
          <n-input v-model:value="form.server" placeholder="ghcr.io" />
        </n-form-item>
        <n-form-item :label="t('docker.reg.username')"><n-input v-model:value="form.username" /></n-form-item>
        <n-form-item :label="t('docker.reg.password')">
          <n-input v-model:value="form.password" type="password" show-password-on="click" @keyup.enter="login" />
        </n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="form = null">{{ t('common.cancel') }}</n-button>
          <n-button type="primary" :loading="busy" :disabled="!form.username || !form.password" @click="login">{{ t('docker.reg.login') }}</n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>
</template>
