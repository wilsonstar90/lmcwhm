<script setup lang="ts">
// Choose how a version is installed (like aaPanel): 極速安裝 (the official
// pre-built image) or 編譯安裝 (compiled on this server, with extra configure
// options and compiler flags). Also used to switch an installed version.
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NAlert, NButton, NCard, NCollapse, NCollapseItem, NFormItem, NInput, NModal, NRadio, NRadioGroup, NSpace } from 'naive-ui'

export interface SourceInfo { no?: string; minutes?: number; min_mem_mb?: number; defaults?: string }
export interface BuildChoice { build_mode: 'fast' | 'source'; build_args: string; build_cflags: string }

const props = defineProps<{
  show: boolean
  title: string
  info: SourceInfo | null
  current?: BuildChoice | null // installed: its present build
  confirmText?: string
}>()
const emit = defineEmits<{ close: []; confirm: [c: BuildChoice] }>()
const { t } = useI18n()

const c = ref<BuildChoice>({ build_mode: 'fast', build_args: '', build_cflags: '' })
watch(() => props.show, (v) => {
  if (v) c.value = { build_mode: 'fast', build_args: '', build_cflags: '', ...(props.current ?? {}) }
})
const can = computed(() => !!props.info && !props.info.no)
const bad = (s: string) => !/^[A-Za-z0-9 _=+,./:@%-]{0,1000}$/.test(s)
const invalid = computed(() => c.value.build_mode === 'source' && (bad(c.value.build_args) || bad(c.value.build_cflags)))
const unchanged = computed(() =>
  !!props.current && props.current.build_mode === c.value.build_mode &&
  (c.value.build_mode === 'fast' || (props.current.build_args === c.value.build_args && props.current.build_cflags === c.value.build_cflags)),
)
</script>

<template>
  <n-modal :show="show" @update:show="(v: boolean) => !v && emit('close')">
    <n-card :title="title" style="width: min(560px, calc(100vw - 32px))" closable @close="emit('close')">
      <n-radio-group v-model:value="c.build_mode" class="modes">
        <label class="mode" :class="{ on: c.build_mode === 'fast' }">
          <n-radio value="fast" />
          <div><b>{{ t('bm.fast') }}</b><p>{{ t('bm.fast_hint') }}</p></div>
        </label>
        <label class="mode" :class="{ on: c.build_mode === 'source', off: !can }">
          <n-radio value="source" :disabled="!can" />
          <div>
            <b>{{ t('bm.source') }}</b>
            <p v-if="can">{{ t('bm.source_hint', { n: info?.minutes ?? 0, mem: info?.min_mem_mb ?? 0 }) }}</p>
            <p v-else>{{ t(`bm.no.${info?.no ?? 'none'}`) }}</p>
          </div>
        </label>
      </n-radio-group>
      <template v-if="c.build_mode === 'source'">
        <n-alert type="warning" :show-icon="false" style="margin: 10px 0">{{ t('bm.warn') }}</n-alert>
        <n-collapse :default-expanded-names="current?.build_args || current?.build_cflags ? ['adv'] : []">
          <n-collapse-item :title="t('bm.advanced')" name="adv">
            <p class="hint">{{ t('bm.defaults') }} <code>{{ info?.defaults }}</code></p>
            <n-form-item :label="t('bm.args')" :feedback="t('bm.args_hint')" :validation-status="bad(c.build_args) ? 'error' : undefined">
              <n-input v-model:value="c.build_args" placeholder="--enable-xxx --with-yyy" />
            </n-form-item>
            <n-form-item label="CFLAGS" :feedback="t('bm.cflags_hint')" :validation-status="bad(c.build_cflags) ? 'error' : undefined">
              <n-input v-model:value="c.build_cflags" placeholder="-O2 -march=native" />
            </n-form-item>
          </n-collapse-item>
        </n-collapse>
      </template>
      <p v-if="current" class="hint">{{ t('bm.switch_hint') }}</p>
      <template #footer>
        <n-space justify="end">
          <n-button @click="emit('close')">{{ t('common.cancel') }}</n-button>
          <n-button type="primary" :disabled="invalid || unchanged" @click="emit('confirm', c)">
            {{ confirmText || (current ? (c.build_mode === 'source' && current.build_mode === 'source' ? t('bm.rebuild') : t('bm.switch')) : t('app.install')) }}
          </n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>
</template>

<style scoped>
.modes {
  display: grid;
  gap: 8px;
  width: 100%;
}
.mode {
  display: flex;
  gap: 10px;
  align-items: flex-start;
  border: 1px solid rgba(128, 128, 128, 0.25);
  border-radius: 8px;
  padding: 10px 12px;
  cursor: pointer;
}
.mode.on {
  border-color: #20a53a;
  background: rgba(32, 165, 58, 0.06);
}
.mode.off {
  opacity: 0.6;
  cursor: not-allowed;
}
.mode p {
  margin: 4px 0 0;
  font-size: 12px;
  opacity: 0.75;
}
.hint {
  font-size: 12px;
  opacity: 0.7;
  margin: 8px 0;
  word-break: break-word;
}
</style>
