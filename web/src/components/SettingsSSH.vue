<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NAlert, NButton, NCard, NForm, NFormItem, NInput, NInputNumber, NPopconfirm, NSelect, NSwitch, NTag, useMessage } from 'naive-ui'
import { get, post, put, ApiError } from '../api/client'

interface Key { type: string; comment: string; fingerprint: string; line: number }
interface SSH { port: number; permit_root_login: string; password_auth: boolean; pubkey_auth: boolean; client_alive_interval: number; max_auth_tries: number; running: boolean; authorized_keys: Key[] }

const { t } = useI18n()
const msg = useMessage()
const ssh = ref<SSH | null>(null)
const newKey = ref('')
const sshLockout = computed(() => !!ssh.value && !ssh.value.password_auth && (!ssh.value.pubkey_auth || !ssh.value.authorized_keys.length))

async function load() {
  try {
    ssh.value = await get<SSH>('/toolbox/ssh')
  } catch (e) {
    msg.error(e instanceof ApiError ? e.text : String(e))
  }
}
async function act(p: Promise<unknown>, reload: () => void = load) {
  try {
    await p
    msg.success(t('common.saved'))
    reload()
  } catch (e) {
    msg.error(e instanceof ApiError ? e.text : String(e))
  }
}
onMounted(load)
</script>

<template>
  <template v-if="ssh">
    <n-card size="small" :title="t('tb.ssh_settings')">
      <template #header-extra><n-tag size="small" :type="ssh.running ? 'success' : 'error'">{{ ssh.running ? t('tb.running') : t('tb.stopped') }}</n-tag></template>
      <n-form label-placement="top" style="max-width: 620px">
        <n-form-item :label="t('tb.ssh_port')" :feedback="t('tb.ssh_port_hint')"><n-input-number v-model:value="ssh.port" :min="1" :max="65535" /></n-form-item>
        <n-form-item :label="t('tb.root_login')">
          <n-select v-model:value="ssh.permit_root_login" :options="['prohibit-password', 'yes', 'no'].map((v) => ({ label: t(`tb.root.${v}`), value: v }))" style="width: 260px" />
        </n-form-item>
        <n-form-item :label="t('tb.password_auth')"><n-switch v-model:value="ssh.password_auth" /></n-form-item>
        <n-form-item :label="t('tb.pubkey_auth')"><n-switch v-model:value="ssh.pubkey_auth" /></n-form-item>
        <n-form-item :label="t('tb.max_tries')"><n-input-number v-model:value="ssh.max_auth_tries" :min="0" :max="100" /></n-form-item>
        <n-form-item :label="t('tb.keepalive')"><n-input-number v-model:value="ssh.client_alive_interval" :min="0" :max="3600"><template #suffix>s</template></n-input-number></n-form-item>
      </n-form>
      <n-alert v-if="sshLockout" type="error" :show-icon="false" style="margin-bottom: 12px">{{ t('tb.lockout') }}</n-alert>
      <n-popconfirm @positive-click="act(put('/toolbox/ssh', ssh))">
        <template #trigger><n-button type="primary" :disabled="sshLockout">{{ t('common.save') }}</n-button></template>
        {{ t('tb.ssh_confirm') }}
      </n-popconfirm>
    </n-card>
    <n-card size="small" :title="t('tb.keys')" style="margin-top: 16px">
      <div v-for="k in ssh.authorized_keys" :key="k.line" class="key">
        <span><b>{{ k.type }}</b> {{ k.comment }} <code class="hint">{{ k.fingerprint }}</code></span>
        <n-popconfirm @positive-click="act(post('/toolbox/ssh/keys/delete', { line: k.line }))">
          <template #trigger><n-button size="tiny" type="error" quaternary>{{ t('common.delete') }}</n-button></template>
          {{ t('common.confirm_delete') }}
        </n-popconfirm>
      </div>
      <p v-if="!ssh.authorized_keys.length" class="hint">{{ t('tb.no_keys') }}</p>
      <n-input v-model:value="newKey" type="textarea" :rows="3" placeholder="ssh-ed25519 AAAA… user@laptop" style="margin-top: 8px" />
      <n-button style="margin-top: 8px" :disabled="!newKey.trim()" @click="act(post('/toolbox/ssh/keys', { key: newKey }), () => ((newKey = ''), load()))">{{ t('tb.add_key') }}</n-button>
    </n-card>
  </template>
</template>

<style scoped>
.hint {
  font-size: 12px;
  opacity: 0.65;
}
.key {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
  padding: 6px 0;
  border-bottom: 1px solid rgba(128, 128, 128, 0.15);
  word-break: break-all;
}
</style>
