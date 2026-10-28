<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import QRCode from 'qrcode'
import {
  NTabs, NTabPane, NCard, NForm, NFormItem, NInput, NInputNumber, NSwitch, NButton, NSelect,
  NRadioGroup, NRadio, NSpace, NAlert, NDescriptions, NDescriptionsItem, NTag, useMessage,
} from 'naive-ui'
import { get, post, put, ApiError } from '../api/client'
import type { CertInfo, PanelSettings, SSLMode } from '../api/types'
import { LANGUAGES, setLang, type Lang } from '../i18n'
import { useSession } from '../stores/session'
import SettingsUsers from '../components/SettingsUsers.vue'
import SettingsApi from '../components/SettingsApi.vue'
import { useRoute } from 'vue-router'

const { t } = useI18n()
const msg = useMessage()
const session = useSession()

const form = ref<PanelSettings | null>(null)
const ipsText = ref('')
const saving = ref(false)

const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))

async function load() {
  if (!session.user?.is_admin) return // sub-users only manage their own account
  form.value = await get<PanelSettings>('/settings')
  ipsText.value = (form.value.allowed_ips ?? []).join('\n')
  loadSSL()
}
onMounted(load)

/** After a port / SSL change the listener restarts: send the browser to the new origin. */
function followRestart(s: Pick<PanelSettings, 'panel_port' | 'ssl_mode' | 'security_entry'>) {
  msg.loading(t('settings.restarting'), { duration: 3000 })
  const scheme = s.ssl_mode === 'off' ? 'http:' : 'https:'
  const target = `${scheme}//${location.hostname}:${s.panel_port}${s.security_entry || '/'}`
  setTimeout(() => location.assign(target), 2000)
}

async function save() {
  if (!form.value) return
  saving.value = true
  try {
    const body = {
      ...form.value,
      allowed_ips: ipsText.value.split('\n').map((s) => s.trim()).filter(Boolean),
    }
    const r = await put<{ settings: PanelSettings; restart: boolean }>('/settings', body)
    msg.success(t('common.saved'))
    session.alias = r.settings.panel_alias
    if (r.settings.language !== form.value.language) await setLang(r.settings.language as Lang)
    form.value = r.settings
    if (r.restart) followRestart(r.settings)
  } catch (e) {
    fail(e)
  } finally {
    saving.value = false
  }
}

// ---- 2FA ----
const qr = ref('')
const secret = ref('')
const code = ref('')
const disablePw = ref('')

async function begin2fa() {
  try {
    const r = await post<{ secret: string; url: string }>('/settings/2fa/begin')
    secret.value = r.secret
    qr.value = await QRCode.toDataURL(r.url, { margin: 1, width: 200 })
  } catch (e) {
    fail(e)
  }
}
async function enable2fa() {
  try {
    await post('/settings/2fa/enable', { code: code.value })
    msg.success(t('common.saved'))
    qr.value = secret.value = code.value = ''
    await session.restore()
  } catch (e) {
    fail(e)
  }
}
async function disable2fa() {
  try {
    await post('/settings/2fa/disable', { password: disablePw.value })
    msg.success(t('common.saved'))
    disablePw.value = ''
    await session.restore()
  } catch (e) {
    fail(e)
  }
}

// ---- SSL ----
const sslMode = ref<SSLMode>('selfsigned')
const cert = ref<CertInfo | null>(null)
const certPem = ref('')
const keyPem = ref('')
async function loadSSL() {
  const r = await get<{ mode: SSLMode; cert?: CertInfo }>('/settings/ssl')
  sslMode.value = r.mode
  cert.value = r.cert ?? null
}
async function applySSL() {
  try {
    await post('/settings/ssl', { mode: sslMode.value, cert: certPem.value, key: keyPem.value })
    msg.success(t('common.saved'))
    followRestart({ ...form.value!, ssl_mode: sslMode.value })
  } catch (e) {
    fail(e)
  }
}

// ---- account ----
const newUsername = ref('')
const usernamePw = ref('')
const oldPw = ref('')
const newPw = ref('')
async function changeUsername() {
  try {
    await post('/settings/username', { username: newUsername.value, password: usernamePw.value })
    msg.success(t('common.saved'))
    newUsername.value = usernamePw.value = ''
    await session.restore()
  } catch (e) {
    fail(e)
  }
}
async function changePassword() {
  try {
    await post('/settings/password', { old: oldPw.value, new: newPw.value })
    msg.success(t('common.saved'))
    location.assign('/login') // all sessions were revoked
  } catch (e) {
    fail(e)
  }
}

const langOptions = LANGUAGES.map((l) => ({ label: l.label, value: l.value }))
</script>

<template>
  <n-tabs type="line" animated :default-value="(session.user?.is_admin && (useRoute().query.tab as string)) || (session.user?.is_admin ? 'panel' : 'account')">
    <n-tab-pane v-if="form" name="panel" :tab="t('settings.tab_panel')">
      <n-card size="small">
        <n-form label-placement="top" style="max-width: 560px">
          <n-form-item :label="t('settings.alias')">
            <n-input v-model:value="form.panel_alias" />
          </n-form-item>
          <n-form-item :label="t('settings.port')" :feedback="t('settings.port_hint')">
            <n-input-number v-model:value="form.panel_port" :min="1" :max="65535" style="width: 200px" />
          </n-form-item>
          <n-form-item :label="t('settings.ipv6')">
            <n-switch v-model:value="form.listen_ipv6" />
          </n-form-item>
          <n-form-item :label="t('common.language')">
            <n-select v-model:value="form.language" :options="langOptions" style="width: 240px" />
          </n-form-item>
          <n-form-item :label="t('settings.timezone')">
            <n-input v-model:value="form.timezone" style="width: 240px" />
          </n-form-item>
          <n-form-item :label="t('settings.session_timeout')">
            <n-input-number v-model:value="form.session_timeout" :min="300" :step="3600" style="width: 200px" />
          </n-form-item>
          <n-button type="primary" :loading="saving" @click="save">{{ t('common.save') }}</n-button>
        </n-form>
      </n-card>
    </n-tab-pane>

    <n-tab-pane v-if="form" name="security" :tab="t('settings.tab_security')">
      <n-space vertical :size="16">
        <n-card size="small">
          <n-form label-placement="top" style="max-width: 560px">
            <n-form-item :label="t('settings.entry')" :feedback="t('settings.entry_hint')">
              <n-input v-model:value="form.security_entry" placeholder="/console" />
            </n-form-item>
            <n-form-item :label="t('settings.bind_domain')" :feedback="t('settings.bind_domain_hint')">
              <n-input v-model:value="form.bind_domain" placeholder="panel.example.com" />
            </n-form-item>
            <n-form-item :label="t('settings.allowed_ips')" :feedback="t('settings.allowed_ips_hint')">
              <n-input v-model:value="ipsText" type="textarea" :rows="4" placeholder="203.0.113.10&#10;2001:db8::/32" />
            </n-form-item>
            <n-form-item :label="t('settings.basic_auth_user')">
              <n-input v-model:value="form.basic_auth_user" />
            </n-form-item>
            <n-form-item v-if="form.basic_auth_user" :label="t('settings.basic_auth_pass')">
              <n-input v-model:value="form.basic_auth_password" type="password" show-password-on="click" />
            </n-form-item>
            <n-space>
              <n-form-item :label="t('settings.login_max_fails')">
                <n-input-number v-model:value="form.login_max_fails" :min="1" style="width: 140px" />
              </n-form-item>
              <n-form-item :label="t('settings.login_lock_mins')">
                <n-input-number v-model:value="form.login_lock_mins" :min="1" style="width: 140px" />
              </n-form-item>
            </n-space>
            <n-button type="primary" :loading="saving" @click="save">{{ t('common.save') }}</n-button>
          </n-form>
        </n-card>

        <n-card size="small" :title="t('settings.twofa')">
          <template #header-extra>
            <n-tag :type="session.user?.totp_enabled ? 'success' : 'default'" size="small">
              {{ session.user?.totp_enabled ? t('common.enabled') : t('common.disabled') }}
            </n-tag>
          </template>
          <div v-if="!session.user?.totp_enabled">
            <n-button v-if="!qr" @click="begin2fa">{{ t('settings.twofa_setup') }}</n-button>
            <div v-else style="max-width: 360px">
              <p>{{ t('settings.twofa_scan') }}</p>
              <img :src="qr" alt="2FA QR code" width="200" height="200" />
              <p><code style="word-break: break-all">{{ secret }}</code></p>
              <n-space>
                <n-input v-model:value="code" maxlength="6" :placeholder="t('login.totp')" style="width: 160px" />
                <n-button type="primary" @click="enable2fa">{{ t('common.confirm') }}</n-button>
              </n-space>
            </div>
          </div>
          <n-space v-else>
            <n-input v-model:value="disablePw" type="password" :placeholder="t('settings.current_password')" style="width: 220px" />
            <n-button type="error" ghost @click="disable2fa">{{ t('settings.twofa_disable') }}</n-button>
          </n-space>
        </n-card>
      </n-space>
    </n-tab-pane>

    <n-tab-pane v-if="form" name="ssl" :tab="t('settings.tab_ssl')">
      <n-card size="small">
        <n-form label-placement="top" style="max-width: 640px">
          <n-form-item :label="t('settings.ssl_mode')">
            <n-radio-group v-model:value="sslMode">
              <n-space>
                <n-radio value="selfsigned">{{ t('settings.ssl_selfsigned') }}</n-radio>
                <n-radio value="custom">{{ t('settings.ssl_custom') }}</n-radio>
                <n-radio value="off">{{ t('settings.ssl_off') }}</n-radio>
              </n-space>
            </n-radio-group>
          </n-form-item>
          <n-alert type="info" :show-icon="false" style="margin-bottom: 16px">{{ t('settings.ssl_acme_note') }}</n-alert>
          <template v-if="sslMode === 'custom'">
            <n-form-item :label="t('settings.ssl_cert')">
              <n-input v-model:value="certPem" type="textarea" :rows="6" placeholder="-----BEGIN CERTIFICATE-----" />
            </n-form-item>
            <n-form-item :label="t('settings.ssl_key')">
              <n-input v-model:value="keyPem" type="textarea" :rows="6" placeholder="-----BEGIN PRIVATE KEY-----" />
            </n-form-item>
          </template>
          <n-button type="primary" @click="applySSL">
            {{ sslMode === 'selfsigned' ? t('settings.ssl_regen') : t('settings.apply_ssl') }}
          </n-button>
        </n-form>
        <n-descriptions v-if="cert" :title="t('settings.ssl_current')" :column="1" label-placement="left" size="small" style="margin-top: 24px">
          <n-descriptions-item label="Subject">{{ cert.subject }}</n-descriptions-item>
          <n-descriptions-item label="Issuer">{{ cert.issuer }}</n-descriptions-item>
          <n-descriptions-item label="SAN">{{ cert.dns_names.join(', ') }}</n-descriptions-item>
          <n-descriptions-item :label="t('settings.ssl_expires')">{{ new Date(cert.not_after).toLocaleString() }}</n-descriptions-item>
        </n-descriptions>
      </n-card>
    </n-tab-pane>

    <n-tab-pane v-if="session.user?.is_admin" name="api" :tab="t('apis.tab')" display-directive="show:lazy"><SettingsApi /></n-tab-pane>
    <n-tab-pane name="account" :tab="t('settings.tab_account')">
      <n-space vertical :size="16" style="max-width: 560px">
        <n-card size="small" :title="t('settings.change_username')">
          <n-form label-placement="top">
            <n-form-item :label="t('settings.username')">
              <n-input v-model:value="newUsername" :placeholder="session.user?.username" />
            </n-form-item>
            <n-form-item :label="t('settings.current_password')">
              <n-input v-model:value="usernamePw" type="password" />
            </n-form-item>
            <n-button type="primary" @click="changeUsername">{{ t('common.save') }}</n-button>
          </n-form>
        </n-card>
        <n-card size="small" :title="t('settings.change_password')">
          <n-form label-placement="top">
            <n-form-item :label="t('settings.old_password')">
              <n-input v-model:value="oldPw" type="password" />
            </n-form-item>
            <n-form-item :label="t('settings.new_password')">
              <n-input v-model:value="newPw" type="password" show-password-on="click" />
            </n-form-item>
            <n-button type="primary" @click="changePassword">{{ t('common.save') }}</n-button>
          </n-form>
        </n-card>
      </n-space>
    </n-tab-pane>
    <n-tab-pane v-if="session.user?.is_admin" name="users" :tab="t('users.tab')" display-directive="show:lazy"><SettingsUsers /></n-tab-pane>
  </n-tabs>
</template>
