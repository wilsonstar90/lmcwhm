<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted } from 'vue'
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

const mobileQuery = window.matchMedia('(max-width: 900px)')
const syncMobileClass = () => document.documentElement.classList.toggle('mobile-ui', mobileQuery.matches)
syncMobileClass()
onMounted(() => mobileQuery.addEventListener('change', syncMobileClass))
onBeforeUnmount(() => {
  mobileQuery.removeEventListener('change', syncMobileClass)
  document.documentElement.classList.remove('mobile-ui')
})
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
html.mobile-ui,
html.mobile-ui body {
  font-size: 16px !important;
}
html.mobile-ui .n-card,
html.mobile-ui .n-card .n-card__header,
html.mobile-ui .n-alert,
html.mobile-ui .n-descriptions,
html.mobile-ui .n-descriptions-item,
html.mobile-ui .n-descriptions-item-content,
html.mobile-ui .n-descriptions-item-label,
html.mobile-ui .n-form-item,
html.mobile-ui .n-modal .n-card {
  font-size: 16px !important;
}
html.mobile-ui .n-button {
  min-height: 44px !important;
  font-size: 16px !important;
}
html.mobile-ui .n-data-table,
html.mobile-ui .n-data-table-th,
html.mobile-ui .n-data-table-td,
html.mobile-ui .n-pagination {
  font-size: 16px !important;
}
html.mobile-ui .n-form-item-label,
html.mobile-ui .n-tabs .n-tab,
html.mobile-ui .n-menu .n-menu-item-content {
  font-size: 16px !important;
}
html.mobile-ui .n-tag {
  font-size: 14px !important;
}
html.mobile-ui .n-input,
html.mobile-ui .n-input-number,
html.mobile-ui .n-base-selection {
  min-height: 44px !important;
}
html.mobile-ui input,
html.mobile-ui textarea,
html.mobile-ui .n-base-selection-label {
  font-size: 16px !important;
}
html.mobile-ui .n-tabs {
  max-width: 100%;
}
html.mobile-ui .n-tabs:not(.n-tabs--left) .n-tabs-nav {
  min-width: 0;
  overflow-x: auto;
  scrollbar-width: thin;
}
html.mobile-ui .n-tabs:not(.n-tabs--left) .n-tab {
  flex-shrink: 0;
  white-space: nowrap;
}
html.mobile-ui .n-modal .n-card {
  max-width: calc(100vw - 16px);
}
</style>
