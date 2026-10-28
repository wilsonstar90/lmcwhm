<script setup lang="ts">
// A list of paths (tags) with a folder button that adds one from the picker.
import { ref } from 'vue'
import { NButton, NDynamicTags, NSpace } from 'naive-ui'
import { useI18n } from 'vue-i18n'
import FolderPicker from './FolderPicker.vue'

const props = defineProps<{ value: string[] }>()
const emit = defineEmits<{ 'update:value': [v: string[]] }>()
const { t } = useI18n()
const show = ref(false)
const add = (p: string) => !props.value.includes(p) && emit('update:value', [...props.value, p])
</script>

<template>
  <n-space vertical :size="6" style="width: 100%">
    <n-dynamic-tags :value="props.value" @update:value="(v: string[]) => emit('update:value', v)" />
    <n-button size="small" dashed @click="show = true">📁 {{ t('fp.add_folder') }}</n-button>
  </n-space>
  <FolderPicker :show="show" :value="props.value[props.value.length - 1]" @pick="add" @close="show = false" />
</template>
