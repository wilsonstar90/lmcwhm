<script setup lang="ts">
// A remark in a table cell: click to type, Enter or leaving the field saves,
// Esc cancels. Used by every list that has remarks, so they all behave alike.
import { nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useMessage } from 'naive-ui'
import { ApiError } from '../api/client'

const props = withDefaults(
  defineProps<{
    value?: string
    save: (text: string) => Promise<unknown>
    auto?: string // shown greyed when there is no remark of the user's
    maxlength?: number
    open?: boolean // start typing (e.g. from a context menu)
  }>(),
  { value: '', auto: '', maxlength: 200, open: false },
)
const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()
const msg = useMessage()

const editing = ref(false)
const busy = ref(false)
const text = ref('')
const shown = ref(props.value)
const el = ref<HTMLInputElement | null>(null)
watch(() => props.value, (v) => (shown.value = v))

async function start() {
  if (busy.value) return
  text.value = shown.value
  editing.value = true
  await nextTick()
  el.value?.focus()
  el.value?.select()
}
watch(() => props.open, (o) => o && start(), { immediate: true })

function stop() {
  editing.value = false
  emit('close')
}
async function commit() {
  if (!editing.value) return
  const v = text.value.trim()
  stop()
  if (v === shown.value) return
  const old = shown.value
  shown.value = v
  busy.value = true
  try {
    await props.save(v)
  } catch (e) {
    shown.value = old
    msg.error(e instanceof ApiError ? e.text : String(e))
  } finally {
    busy.value = false
  }
}
function key(e: KeyboardEvent) {
  e.stopPropagation()
  if (e.key === 'Enter') commit()
  if (e.key === 'Escape') stop()
}
</script>

<template>
  <input
    v-if="editing" ref="el" v-model="text" class="remark-input" :maxlength="maxlength"
    @keydown="key" @blur="commit" @click.stop @dblclick.stop
  />
  <div
    v-else class="remark-cell" :class="{ auto: !shown && auto, empty: !shown && !auto, busy }"
    :title="shown || auto" @click.stop="start" @dblclick.stop
  >{{ shown || auto || t('fm.add_remark') }}</div>
</template>

<style>
.remark-cell {
  min-height: 20px;
  cursor: text;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.remark-cell.auto,
.remark-cell.busy {
  opacity: 0.55;
}
.remark-cell.empty {
  opacity: 0;
}
tr:hover .remark-cell.empty,
.remark-cell.empty:focus-within {
  opacity: 0.35;
}
@media (hover: none) {
  .remark-cell.empty {
    opacity: 0.35;
  }
}
.remark-input {
  width: 100%;
  min-width: 80px;
  box-sizing: border-box;
  font: inherit;
  padding: 2px 6px;
  border: 1px solid #20a53a;
  border-radius: 4px;
  background: transparent;
  color: inherit;
  outline: none;
}
</style>
