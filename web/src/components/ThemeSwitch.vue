<script setup lang="ts">
import { computed, h } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NPopselect } from 'naive-ui'
import { setTheme, themeMode, type ThemeMode } from '../theme'

const { t } = useI18n()
const icons: Record<ThemeMode, string> = { auto: '◐', light: '☀', dark: '☾' }
const options = computed(() =>
  (['auto', 'light', 'dark'] as const).map((m) => ({ value: m, label: () => h('span', [h('span', { style: 'display:inline-block;width:18px' }, icons[m]), t(`theme.${m}`)]) })),
)
</script>

<template>
  <n-popselect :value="themeMode" :options="options" trigger="click" @update:value="(m: ThemeMode) => setTheme(m)">
    <n-button size="small" quaternary :title="t('theme.title')">{{ icons[themeMode] }} {{ t(`theme.${themeMode}`) }}</n-button>
  </n-popselect>
</template>
