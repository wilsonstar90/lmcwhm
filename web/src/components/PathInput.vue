<script setup lang="ts">
// A path field with a folder button that opens the folder (or file) picker.
import { ref } from 'vue'
import { NButton, NInput, NInputGroup } from 'naive-ui'
import FolderPicker from './FolderPicker.vue'

const props = defineProps<{ value: string; mode?: 'dir' | 'file'; placeholder?: string; size?: 'small' | 'medium' }>()
const emit = defineEmits<{ 'update:value': [v: string] }>()
const show = ref(false)
</script>

<template>
  <n-input-group>
    <n-input :value="props.value" :placeholder="placeholder" :size="size" @update:value="(v: string) => emit('update:value', v)" />
    <n-button :size="size" :title="mode === 'file' ? 'File' : 'Folder'" @click="show = true">📁</n-button>
  </n-input-group>
  <FolderPicker :show="show" :value="props.value" :mode="mode" @pick="(p: string) => emit('update:value', p)" @close="show = false" />
</template>
