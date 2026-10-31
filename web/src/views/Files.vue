<script setup lang="ts">
// File manager: several directory tabs (multi-window like aaPanel), the
// editor window, the upload dialog and the recycle bin.
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import FileBrowser from '../components/files/FileBrowser.vue'
import FileEditor from '../components/files/FileEditor.vue'
import UploadDialog from '../components/files/UploadDialog.vue'
import RecycleBin from '../components/files/RecycleBin.vue'
import { baseName } from '../components/files/state'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()

interface Tab { id: number; path: string }
const KEY = 'lmcwhm.files.tabs'
function restore(): { tabs: Tab[]; active: number } {
  try {
    const s = JSON.parse(localStorage.getItem(KEY) ?? 'null')
    if (s && Array.isArray(s.tabs) && s.tabs.length) return s
  } catch {
    /* private mode or bad data */
  }
  return { tabs: [{ id: 1, path: '/www/wwwroot' }], active: 1 }
}
const saved = restore()
const tabs = ref<Tab[]>(saved.tabs)
const active = ref(saved.active)
let seq = Math.max(...tabs.value.map((x) => x.id))

function addTab(path = cur.value?.path ?? '/www/wwwroot') {
  if (tabs.value.length >= 20) return
  tabs.value.push({ id: ++seq, path })
  active.value = seq
}

// /files?path=… (links from other pages) opens that directory in a tab.
const q = route.query.path
if (typeof q === 'string' && q) {
  const found = tabs.value.find((x) => x.path === q)
  if (found) active.value = found.id
  else addTab(q)
}
if (!tabs.value.some((x) => x.id === active.value)) active.value = tabs.value[0].id

watch([tabs, active], () => {
  try {
    localStorage.setItem(KEY, JSON.stringify({ tabs: tabs.value, active: active.value }))
  } catch {
    /* private mode */
  }
}, { deep: true })

const cur = computed(() => tabs.value.find((x) => x.id === active.value)!)
watch(() => cur.value?.path, (p) => p && route.query.path !== p && router.replace({ query: { path: p } }))

function closeTab(id: number) {
  if (tabs.value.length === 1) return
  const i = tabs.value.findIndex((x) => x.id === id)
  tabs.value.splice(i, 1)
  if (active.value === id) active.value = tabs.value[Math.max(0, i - 1)].id
}
const label = (p: string) => (p === '/' ? t('fm.root') : baseName(p))
const openTab = ref<string | null>(null)
watch(openTab, (p) => {
  if (p) addTab(p)
  openTab.value = null
})
const trash = ref(false)
</script>

<template>
  <div class="files">
    <div class="tabbar" role="tablist">
      <div
        v-for="x in tabs" :key="x.id" class="tab" :class="{ on: x.id === active }" role="tab" :aria-selected="x.id === active" :title="x.path"
        @click="active = x.id" @auxclick.middle="closeTab(x.id)"
      >
        <span>📁 {{ label(x.path) }}</span>
        <a v-if="tabs.length > 1" class="x" :aria-label="t('common.close')" @click.stop="closeTab(x.id)">✕</a>
      </div>
      <button type="button" class="add" :title="t('fm.new_tab')" @click="addTab()">＋</button>
    </div>
    <KeepAlive :max="20">
      <FileBrowser :key="cur.id" v-model:open-tab="openTab" :path="cur.path" @path="(p: string) => (cur.path = p)" @trash="trash = true" />
    </KeepAlive>
    <FileEditor />
    <UploadDialog />
    <RecycleBin :show="trash" @close="trash = false" />
  </div>
</template>

<style scoped>
.files {
  display: flex;
  flex-direction: column;
  gap: 10px;
  min-width: 0;
}
.tabbar {
  display: flex;
  align-items: stretch;
  overflow-x: auto;
  border-bottom: 1px solid rgba(128, 128, 128, 0.25);
  scrollbar-width: thin;
}
.tab {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 0 0 auto;
  max-width: 180px;
  padding: 6px 10px 6px 12px;
  border: 1px solid rgba(128, 128, 128, 0.25);
  border-bottom: 0;
  margin-right: -1px;
  background: rgba(128, 128, 128, 0.06);
  cursor: pointer;
  font-size: 13px;
  user-select: none;
}
.tab span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.tab.on {
  background: transparent;
  color: #20a53a;
  box-shadow: inset 0 2px 0 #20a53a;
}
.x {
  opacity: 0.5;
  font-size: 11px;
}
.x:hover {
  opacity: 1;
}
.add {
  border: 0;
  background: transparent;
  color: inherit;
  font-size: 16px;
  padding: 0 12px;
  cursor: pointer;
}
</style>
