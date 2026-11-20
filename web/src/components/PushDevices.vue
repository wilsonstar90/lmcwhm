<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NAlert, NButton, NFormItem, NInput, NSelect, NSpace, NTag, useMessage } from 'naive-ui'
import { get, post, ApiError } from '../api/client'

interface Device { id: number; name: string; enabled: boolean; created_at: number; last_used?: number }
const props = defineProps<{ modelValue?: number[] }>()
const emit = defineEmits<{ 'update:modelValue': [number[]] }>()
const { t } = useI18n()
const msg = useMessage()
const devices = ref<Device[]>([])
const vapid = ref('')
const name = ref('')
const busy = ref(false)
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))
const supported = 'serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window
const iOS = /iPhone|iPad|iPod/.test(navigator.userAgent) || (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1)
const standalone = window.matchMedia('(display-mode: standalone)').matches || (navigator as Navigator & { standalone?: boolean }).standalone === true
const needsInstall = computed(() => iOS && !standalone)
const options = computed(() => devices.value.filter((d) => d.enabled).map((d) => ({ label: d.name, value: d.id })))

async function load() {
  try {
    const [config, list] = await Promise.all([get<{ vapid_public_key: string }>('/push/config'), get<{ items: Device[] }>('/push/devices')])
    vapid.value = config.vapid_public_key
    devices.value = list.items
  } catch (e) {
    fail(e)
  }
}
onMounted(load)

function decodeKey(value: string) {
  const padding = '='.repeat((4 - value.length % 4) % 4)
  const raw = atob(value.replace(/-/g, '+').replace(/_/g, '/') + padding)
  return Uint8Array.from(raw, (c) => c.charCodeAt(0))
}

async function subscribe() {
  if (!supported || needsInstall.value || busy.value) return
  busy.value = true
  try {
    if (!name.value.trim()) name.value = iOS ? 'iPhone / iPad' : 'This device'
    const permission = await Notification.requestPermission()
    if (permission !== 'granted') throw new Error(t('push.permission_denied'))
    const registration = await navigator.serviceWorker.ready
    const subscription = await registration.pushManager.getSubscription() ?? await registration.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: decodeKey(vapid.value) })
    const json = subscription.toJSON()
    const device = await post<Device>('/push/subscribe', { name: name.value.trim(), endpoint: json.endpoint, keys: json.keys })
    emit('update:modelValue', [...new Set([...(props.modelValue ?? []), device.id])])
    msg.success(t('push.subscribed'))
    await load()
  } catch (e) {
    fail(e)
  } finally {
    busy.value = false
  }
}
async function revoke(device: Device) {
  try {
    await post(`/push/devices/${device.id}/delete`)
    emit('update:modelValue', (props.modelValue ?? []).filter((id) => id !== device.id))
    await load()
  } catch (e) {
    fail(e)
  }
}
async function test(device: Device) {
  try {
    await post(`/push/devices/${device.id}/test`)
    msg.success(t('alert.test_sent'))
  } catch (e) {
    fail(e)
  }
}
</script>

<template>
  <n-alert v-if="needsInstall" type="info" :show-icon="false" style="margin-bottom: 12px">{{ t('push.ios_install') }}</n-alert>
  <n-alert v-else-if="!supported" type="warning" :show-icon="false" style="margin-bottom: 12px">{{ t('push.unsupported') }}</n-alert>
  <n-form-item :label="t('push.devices')">
    <n-select :value="modelValue ?? []" multiple :options="options" @update:value="(v: number[]) => emit('update:modelValue', v)" />
  </n-form-item>
  <n-form-item :label="t('push.device_name')"><n-input v-model:value="name" :placeholder="t('push.device_placeholder')" /></n-form-item>
  <n-button :disabled="!supported || needsInstall || busy" :loading="busy" @click="subscribe">{{ t('push.subscribe') }}</n-button>
  <n-space vertical style="margin-top: 12px; width: 100%">
    <n-space v-for="d in devices" :key="d.id" justify="space-between" style="width: 100%">
      <span>{{ d.name }} <n-tag size="small" :type="d.enabled ? 'success' : 'default'">{{ d.enabled ? t('push.active') : t('push.revoked') }}</n-tag></span>
      <n-space>
        <n-button size="tiny" @click="test(d)">{{ t('alert.test') }}</n-button>
        <n-button size="tiny" type="error" quaternary @click="revoke(d)">{{ t('common.delete') }}</n-button>
      </n-space>
    </n-space>
  </n-space>
</template>
