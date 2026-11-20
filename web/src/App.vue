<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NConfigProvider, NMessageProvider, NDialogProvider, NGlobalStyle, darkTheme,
  zhCN, zhTW, enUS, jaJP, dateZhCN, dateZhTW, dateEnUS, dateJaJP,
  type GlobalThemeOverrides,
} from 'naive-ui'
import { useDark } from './theme'

const { locale } = useI18n()
const dark = useDark()
const theme = computed(() => (dark.value ? darkTheme : null))

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


<style>
html,
body,
#app {
  width: 100%;
  min-height: 100%;
  margin: 0;
  overflow-x: hidden;
}
*,
*::before,
*::after {
  box-sizing: border-box;
}
@media (max-width: 900px) {
  .n-tabs {
    max-width: 100%;
  }
  .n-tabs:not(.n-tabs--left) .n-tabs-nav {
    min-width: 0;
    overflow-x: auto;
    scrollbar-width: thin;
  }
  .n-tabs:not(.n-tabs--left) .n-tab {
    flex-shrink: 0;
    white-space: nowrap;
  }
  .n-modal .n-card {
    max-width: calc(100vw - 16px);
  }
}
</style>
