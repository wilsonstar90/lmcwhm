<script setup lang="ts">
// The headers a CDN puts the visitor's address in, tried in order (like
// aaPanel's 管理網站CDN-Headers): add, edit, reorder, delete, with remarks.
import { computed, nextTick, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NInput, NSpace, useMessage } from 'naive-ui'
import RemarkCell from './RemarkCell.vue'

export interface CdnHeader { name: string; remark?: string }

const props = defineProps<{ modelValue: CdnHeader[]; defaults?: string[] }>()
const emit = defineEmits<{ 'update:modelValue': [CdnHeader[]] }>()
const { t, te } = useI18n()
const msg = useMessage()

const add = ref('')
const editing = ref<{ i: number; name: string } | null>(null)
const list = computed(() => props.modelValue)
const set = (v: CdnHeader[]) => emit('update:modelValue', v)
const headerOK = (v: string) => /^[A-Za-z0-9-]{1,64}$/.test(v)
const taken = (v: string, except = -1) => list.value.some((x, i) => i !== except && x.name.toLowerCase() === v.toLowerCase())
// Well-known headers carry a note of whose they are.
const note = (name: string) => {
  const k = `cdnh.note.${name.toLowerCase().replace(/-/g, '_')}`
  return te(k) ? t(k) : ''
}

function addOne() {
  const v = add.value.trim()
  if (!headerOK(v) || taken(v)) return msg.warning(t('prot.cdn_bad_header'))
  set([...list.value, { name: v }])
  add.value = ''
}
const nameEl = ref<HTMLInputElement | null>(null)
async function startEdit(i: number) {
  editing.value = { i, name: list.value[i].name }
  await nextTick()
  nameEl.value?.focus()
  nameEl.value?.select()
}
function commitName() {
  const e = editing.value
  if (!e) return
  const v = e.name.trim()
  editing.value = null
  if (v === list.value[e.i]?.name) return
  if (!headerOK(v) || taken(v, e.i)) return msg.warning(t('prot.cdn_bad_header'))
  set(list.value.map((x, i) => (i === e.i ? { ...x, name: v } : x)))
}
function move(i: number, d: number) {
  const j = i + d
  if (j < 0 || j >= list.value.length) return
  const v = [...list.value]
  ;[v[i], v[j]] = [v[j], v[i]]
  set(v)
}
const remove = (i: number) => set(list.value.filter((_, k) => k !== i))
const setRemark = (i: number) => async (r: string) => set(list.value.map((x, k) => (k === i ? { ...x, remark: r } : x)))
</script>

<template>
  <n-space :wrap="false" style="margin-bottom: 10px">
    <n-input v-model:value="add" :placeholder="t('prot.cdn_header_name')" @keyup.enter="addOne" />
    <n-button type="primary" :disabled="!add.trim()" @click="addOne">{{ t('prot.cdn_add') }}</n-button>
  </n-space>
  <div class="hdrs">
    <div class="hdr head">
      <span class="n">#</span><span class="name">Header</span><span class="rm">{{ t('site.remark') }}</span><span class="ops">{{ t('common.actions') }}</span>
    </div>
    <div v-for="(hd, i) in list" :key="hd.name" class="hdr">
      <span class="n">{{ i + 1 }}</span>
      <span class="name">
        <input
          v-if="editing?.i === i" :ref="(el) => (nameEl = el as HTMLInputElement | null)" v-model="editing.name" class="remark-input" maxlength="64"
          @keydown.enter.stop="commitName" @keydown.esc.stop="editing = null" @blur="commitName"
        />
        <code v-else @dblclick="startEdit(i)">{{ hd.name }}</code>
      </span>
      <span class="rm"><RemarkCell :value="hd.remark ?? ''" :auto="note(hd.name)" :maxlength="100" :save="setRemark(i)" /></span>
      <span class="ops">
        <n-button size="tiny" quaternary :disabled="i === 0" :aria-label="t('menucfg.up')" @click="move(i, -1)">↑</n-button>
        <n-button size="tiny" quaternary :disabled="i === list.length - 1" :aria-label="t('menucfg.down')" @click="move(i, 1)">↓</n-button>
        <n-button size="tiny" quaternary @click="startEdit(i)">{{ t('common.edit') }}</n-button>
        <n-button size="tiny" quaternary type="error" :disabled="list.length === 1" @click="remove(i)">{{ t('common.delete') }}</n-button>
      </span>
    </div>
  </div>
  <n-space justify="space-between" align="center" style="margin-top: 8px">
    <span class="note">• {{ t('prot.cdn_note') }}</span>
    <n-button v-if="defaults" size="tiny" quaternary @click="set(defaults.map((name) => ({ name })))">{{ t('prot.cdn_reset') }}</n-button>
  </n-space>
</template>

<style scoped>
.hdrs {
  max-height: 340px;
  overflow: auto;
  border: 1px solid rgba(128, 128, 128, 0.2);
  border-radius: 4px;
}
.hdr {
  display: grid;
  grid-template-columns: 28px minmax(120px, 1fr) minmax(90px, 1fr) auto;
  gap: 8px;
  align-items: center;
  padding: 5px 8px;
  border-top: 1px solid rgba(128, 128, 128, 0.12);
}
.hdr.head {
  border-top: 0;
  font-weight: 600;
  font-size: 12px;
  background: rgba(128, 128, 128, 0.06);
  position: sticky;
  top: 0;
}
.n {
  opacity: 0.5;
  font-size: 12px;
}
.name code {
  word-break: break-all;
  cursor: text;
}
.rm {
  min-width: 0;
  font-size: 13px;
}
.ops {
  white-space: nowrap;
  text-align: right;
}
.note {
  font-size: 12px;
  opacity: 0.65;
}
@media (max-width: 520px) {
  .hdr {
    grid-template-columns: 22px minmax(0, 1fr) auto;
  }
  .n,
  .name,
  .ops {
    grid-row: 1;
  }
  .ops {
    grid-column: 3;
  }
  .rm {
    grid-row: 2;
    grid-column: 2 / 4;
  }
  .hdr.head .rm {
    display: none;
  }
}
</style>
