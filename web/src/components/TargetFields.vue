<script setup lang="ts">
// What a domain entry serves: the fields of one target kind.
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NCheckbox, NFormItem, NInput, NRadioButton, NRadioGroup, NSelect, NSpace } from 'naive-ui'
import { get } from '../api/client'
import type { DomainTarget, Site } from '../api/types'

export interface TargetValue {
  target: DomainTarget
  target_site: number | null
  container: string | null
  port: number | null
  proxy_target: string
  redirect: string
  redirect_code: number
  redirect_path: boolean
}

const props = defineProps<{ modelValue: TargetValue; selfId?: number; hasContent?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [v: TargetValue] }>()
const { t } = useI18n()
const v = computed({ get: () => props.modelValue, set: (x) => emit('update:modelValue', x) })
const set = (patch: Partial<TargetValue>) => emit('update:modelValue', { ...props.modelValue, ...patch })

const sites = ref<Site[]>([])
const ports = ref<{ container: string; port: number }[]>([])
onMounted(async () => {
  try {
    sites.value = (await get<{ items: Site[] }>('/sites')).items
  } catch {
    sites.value = []
  }
  try {
    ports.value = (await get<{ containers: { container: string; port: number }[] }>('/forwards')).containers
  } catch {
    ports.value = []
  }
})
const kinds = computed<DomainTarget[]>(() => [...(props.hasContent ? (['site'] as DomainTarget[]) : []), 'link', 'docker', 'url', 'passthrough', 'redirect'])
const siteOptions = computed(() =>
  sites.value.filter((s) => s.type !== 'proxy' && s.id !== props.selfId).map((s) => ({ label: `${s.domain} (${s.type})`, value: s.id })),
)
const containerOptions = computed(() => [...new Set(ports.value.map((p) => p.container))].map((c) => ({ label: c, value: c })))
const portOptions = computed(() => ports.value.filter((p) => p.container === v.value.container).map((p) => ({ label: String(p.port), value: p.port })))
function pickContainer(c: string) {
  const first = ports.value.find((p) => p.container === c)?.port ?? null
  set({ container: c, port: first, proxy_target: first ? `http://127.0.0.1:${first}` : '' })
}
</script>

<template>
  <n-form-item :label="t('dom.target')">
    <n-radio-group :value="v.target" @update:value="(x: DomainTarget) => set({ target: x })">
      <n-radio-button v-for="k in kinds" :key="k" :value="k">{{ t(`dom.targets.${k}`) }}</n-radio-button>
    </n-radio-group>
  </n-form-item>
  <p class="hint">{{ t(`dom.target_hint.${v.target}`) }}</p>
  <n-form-item v-if="v.target === 'link'" :label="t('dom.website')">
    <n-select :value="v.target_site" :options="siteOptions" filterable @update:value="(x: number) => set({ target_site: x })" />
  </n-form-item>
  <template v-else-if="v.target === 'docker'">
    <n-space>
      <n-form-item :label="t('route.container')" style="width: 260px">
        <n-select :value="v.container" :options="containerOptions" filterable :placeholder="t('dom.no_containers')" @update:value="pickContainer" />
      </n-form-item>
      <n-form-item :label="t('route.port')" style="width: 140px">
        <n-select :value="v.port" :options="portOptions" @update:value="(p: number) => set({ port: p, proxy_target: `http://127.0.0.1:${p}` })" />
      </n-form-item>
    </n-space>
  </template>
  <n-form-item v-else-if="v.target === 'url'" :label="t('site.proxy_target')" :feedback="t('site.proxy_target_hint')">
    <n-input :value="v.proxy_target" placeholder="http://127.0.0.1:8081 / https://backend.example.com" @update:value="(x: string) => set({ proxy_target: x })" />
  </n-form-item>
  <n-form-item v-else-if="v.target === 'passthrough'" :label="t('site.proxy_target')" :feedback="t('site.passthrough_hint')">
    <n-input :value="v.proxy_target" placeholder="127.0.0.1:8443" @update:value="(x: string) => set({ proxy_target: x })" />
  </n-form-item>
  <template v-else-if="v.target === 'redirect'">
    <n-form-item :label="t('dom.redirect_to')">
      <n-input :value="v.redirect" placeholder="https://www.example.com" @update:value="(x: string) => set({ redirect: x })" />
    </n-form-item>
    <n-space align="center">
      <n-select
        :value="v.redirect_code" size="small" style="width: 200px"
        :options="[301, 302, 307, 308].map((c) => ({ label: t(`dom.codes.${c}`), value: c }))"
        @update:value="(c: number) => set({ redirect_code: c })"
      />
      <n-checkbox :checked="v.redirect_path" @update:checked="(x: boolean) => set({ redirect_path: x })">{{ t('dom.keep_path') }}</n-checkbox>
    </n-space>
  </template>
</template>

<style scoped>
.hint {
  font-size: 12px;
  opacity: 0.7;
  margin: -8px 0 12px;
}
</style>
