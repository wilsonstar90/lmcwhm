<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { NCard, NForm, NFormItem, NInput, NButton, NAlert } from 'naive-ui'
import { useSession } from '../stores/session'
import { ApiError } from '../api/client'
import LangSwitch from '../components/LangSwitch.vue'
import ThemeSwitch from '../components/ThemeSwitch.vue'

const { t } = useI18n()
const session = useSession()
const router = useRouter()
session.loadMeta().catch(() => {})

const username = ref('')
const password = ref('')
const totp = ref('')
const needTotp = ref(false)
const loading = ref(false)
const error = ref('')

async function submit() {
  error.value = ''
  loading.value = true
  try {
    const r = await session.login(username.value, password.value, totp.value)
    if (r === 'totp') {
      needTotp.value = true
      return
    }
    router.push({ name: 'home' })
  } catch (e) {
    error.value = e instanceof ApiError ? e.text : String(e)
    if (needTotp.value) totp.value = ''
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="wrap">
    <div class="lang"><ThemeSwitch /> <LangSwitch /></div>
    <n-card class="card" :title="session.alias">
      <p class="sub">{{ t('login.title') }}</p>
      <n-form @submit.prevent="submit">
        <template v-if="!needTotp">
          <n-form-item :label="t('login.username')" :show-feedback="false" style="margin-bottom: 12px">
            <n-input v-model:value="username" autofocus :input-props="{ autocomplete: 'username' }" />
          </n-form-item>
          <n-form-item :label="t('login.password')" :show-feedback="false" style="margin-bottom: 16px">
            <n-input
              v-model:value="password"
              type="password"
              show-password-on="click"
              :input-props="{ autocomplete: 'current-password' }"
              @keyup.enter="submit"
            />
          </n-form-item>
        </template>
        <n-form-item v-else :label="t('login.totp')" :feedback="t('login.totp_hint')" style="margin-bottom: 16px">
          <n-input
            v-model:value="totp"
            autofocus
            maxlength="6"
            :input-props="{ inputmode: 'numeric', autocomplete: 'one-time-code' }"
            @keyup.enter="submit"
          />
        </n-form-item>
        <n-alert v-if="error" type="error" :show-icon="false" style="margin-bottom: 12px">{{ error }}</n-alert>
        <n-button type="primary" block :loading="loading" attr-type="submit" @click="submit">
          {{ t('login.submit') }}
        </n-button>
      </n-form>
    </n-card>
  </div>
</template>

<style scoped>
.wrap {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 16px;
  position: relative;
}
.lang {
  position: absolute;
  top: 16px;
  right: 16px;
  display: flex;
  gap: 8px;
  align-items: center;
}
.card {
  width: 100%;
  max-width: 380px;
}
.sub {
  margin: -8px 0 20px;
  opacity: 0.7;
}
</style>
