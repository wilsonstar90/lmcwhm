<script setup lang="ts">
// Panel settings → Interface: the main menu's order (drag, or ↑↓ on touch
// screens) and which entries it shows.
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NAlert, NButton, NCard, NSpace, NSwitch, useMessage } from 'naive-ui'
import { ApiError } from '../api/client'
import { arrange, loadMenu, menuConfig, menuKeys, saveMenu } from '../menu'

const { t } = useI18n()
const msg = useMessage()
const order = ref<string[]>([])
const hidden = ref<Set<string>>(new Set())
const saving = ref(false)
const pinned = new Set(['home', 'settings'])

function reset(from = menuConfig.value) {
  order.value = arrange(menuKeys.value, from)
  hidden.value = new Set(from.hidden)
}
onMounted(async () => {
  await loadMenu()
  reset()
})

function move(i: number, d: number) {
  const j = i + d
  if (j < 0 || j >= order.value.length) return
  const o = [...order.value]
  ;[o[i], o[j]] = [o[j], o[i]]
  order.value = o
}
const dragging = ref<number | null>(null)
function drop(i: number) {
  const from = dragging.value
  dragging.value = null
  if (from === null || from === i) return
  const o = [...order.value]
  const [k] = o.splice(from, 1)
  o.splice(i, 0, k)
  order.value = o
}
function toggle(k: string, on: boolean) {
  const h = new Set(hidden.value)
  on ? h.delete(k) : h.add(k)
  hidden.value = h
}
const dirty = computed(() => order.value.join() !== arrange(menuKeys.value, menuConfig.value).join() || [...hidden.value].sort().join() !== [...menuConfig.value.hidden].sort().join())
async function save() {
  saving.value = true
  try {
    await saveMenu({ order: order.value, hidden: [...hidden.value] })
    msg.success(t('common.saved'))
  } catch (e) {
    msg.error(e instanceof ApiError ? e.text : String(e))
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <n-card size="small" :title="t('menucfg.title')" style="max-width: 560px">
    <n-alert type="default" :show-icon="false" style="margin-bottom: 12px">{{ t('menucfg.hint') }}</n-alert>
    <div class="list">
      <div
        v-for="(k, i) in order" :key="k" class="row" :class="{ off: hidden.has(k), drag: dragging === i }" draggable="true"
        @dragstart="dragging = i" @dragover.prevent @drop="drop(i)" @dragend="dragging = null"
      >
        <span class="grip" aria-hidden="true">⋮⋮</span>
        <span class="name">{{ t(`menu.${k}`) }}</span>
        <n-button size="tiny" quaternary :disabled="i === 0" :aria-label="t('menucfg.up')" @click="move(i, -1)">↑</n-button>
        <n-button size="tiny" quaternary :disabled="i === order.length - 1" :aria-label="t('menucfg.down')" @click="move(i, 1)">↓</n-button>
        <n-switch size="small" :value="!hidden.has(k)" :disabled="pinned.has(k)" @update:value="(on: boolean) => toggle(k, on)" />
      </div>
    </div>
    <n-space style="margin-top: 12px">
      <n-button type="primary" :loading="saving" :disabled="!dirty" @click="save">{{ t('common.save') }}</n-button>
      <n-button @click="reset({ order: [], hidden: [] })">{{ t('menucfg.default') }}</n-button>
    </n-space>
  </n-card>
</template>

<style scoped>
.list {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 8px;
  border: 1px solid rgba(128, 128, 128, 0.2);
  border-radius: 6px;
  background: rgba(128, 128, 128, 0.04);
  cursor: grab;
  touch-action: manipulation;
}
.row.off .name {
  opacity: 0.45;
  text-decoration: line-through;
}
.row.drag {
  opacity: 0.5;
}
.grip {
  opacity: 0.4;
  font-size: 12px;
}
.name {
  flex: 1;
  min-width: 0;
}
</style>
