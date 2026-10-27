<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { NSelect } from 'naive-ui'
import { LANGUAGES, setLang, type Lang } from '../i18n'

const { locale } = useI18n()
const options = LANGUAGES.map((l) => ({ label: l.label, value: l.value }))
</script>

<template>
  <n-select
    :value="locale"
    :options="options"
    size="small"
    style="width: 170px"
    @update:value="(v: Lang) => setLang(v)"
  />
</template>
