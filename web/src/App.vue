<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NConfigProvider, NMessageProvider, NDialogProvider, NGlobalStyle, darkTheme, useOsTheme,
  zhCN, zhTW, enUS, jaJP, dateZhCN, dateZhTW, dateEnUS, dateJaJP,
  type GlobalThemeOverrides,
} from 'naive-ui'

const { locale } = useI18n()
const osTheme = useOsTheme()
const theme = computed(() => (osTheme.value === 'dark' ? darkTheme : null))

const naive = computed(() => {
  switch (locale.value) {
    case 'zh-CN': return [zhCN, dateZhCN] as const
    case 'zh-HK':
    case 'zh-TW': return [zhTW, dateZhTW] as const
    case 'ja': return [jaJP, dateJaJP] as const
    default: return [enUS, dateEnUS] as const
  }
})

const overrides: GlobalThemeOverrides = {
  common: { primaryColor: '#20a53a', primaryColorHover: '#36b84d', primaryColorPressed: '#178a2e', primaryColorSuppl: '#36b84d' },
}
</script>

<template>
  <n-config-provider :theme="theme" :theme-overrides="overrides" :locale="naive[0]" :date-locale="naive[1]">
    <n-global-style />
    <n-message-provider>
      <n-dialog-provider>
        <router-view />
      </n-dialog-provider>
    </n-message-provider>
  </n-config-provider>
</template>
