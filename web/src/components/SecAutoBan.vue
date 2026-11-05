<script setup lang="ts">
// Auto-ban: an address refused N times within a window is blocked for a
// while. The IP allow list, the server's own addresses and trusted ranges
// (Cloudflare, crawlers) are never banned; requests that were only detected
// do not count.
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NCheckbox, NCheckboxGroup, NInputNumber, NSpace, NSwitch, useMessage } from 'naive-ui'
import { get, put, ApiError } from '../api/client'

interface AutoBan { enabled: boolean; window: number; threshold: number; ban_minutes: number; sources: string[] }

const emit = defineEmits<{ changed: [] }>()
const { t } = useI18n()
const msg = useMessage()
const a = ref<AutoBan | null>(null)
const saved = ref('')
const saving = ref(false)
const sources = ['waf', 'rule', 'protection', 'list', 'cc']

onMounted(async () => {
  try {
    a.value = await get<AutoBan>('/security/autoban')
    saved.value = JSON.stringify(a.value)
  } catch (e) {
    msg.error(e instanceof ApiError ? e.text : String(e))
  }
})
const dirty = computed(() => !!a.value && JSON.stringify(a.value) !== saved.value)
async function save() {
  if (!a.value) return
  saving.value = true
  try {
    a.value = await put<AutoBan>('/security/autoban', a.value)
    saved.value = JSON.stringify(a.value)
    msg.success(t('common.saved'))
    emit('changed')
  } catch (e) {
    msg.error(e instanceof ApiError ? e.text : String(e))
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div v-if="a" class="ab" :class="{ on: a.enabled }">
    <div class="ab-head">
      <n-switch v-model:value="a.enabled" size="small" />
      <b>{{ t('ab.title') }}</b>
      <span class="ab-sum">{{ a.enabled ? t('ab.summary', { w: a.window, n: a.threshold, m: a.ban_minutes }) : t('ab.off') }}</span>
    </div>
    <div class="ab-rule">
      <span>{{ t('ab.within') }}</span>
      <n-input-number v-model:value="a.window" size="small" :min="1" :max="1440" style="width: 100px"><template #suffix>{{ t('ab.min') }}</template></n-input-number>
      <span>{{ t('ab.refused') }}</span>
      <n-input-number v-model:value="a.threshold" size="small" :min="2" :max="100000" style="width: 100px"><template #suffix>{{ t('ab.times') }}</template></n-input-number>
      <span>{{ t('ab.ban_for') }}</span>
      <n-input-number v-model:value="a.ban_minutes" size="small" :min="1" :max="525600" style="width: 120px"><template #suffix>{{ t('ab.min') }}</template></n-input-number>
    </div>
    <div class="ab-rule">
      <span>{{ t('ab.counts') }}</span>
      <n-checkbox-group v-model:value="a.sources">
        <n-space :size="[12, 4]">
          <n-checkbox v-for="s in sources" :key="s" :value="s" :label="t(`evt.sources.${s}`)" />
        </n-space>
      </n-checkbox-group>
    </div>
    <div class="ab-foot">
      <span class="ab-note">{{ t('ab.note') }}</span>
      <n-button size="small" type="primary" :disabled="!dirty" :loading="saving" @click="save">{{ t('common.save') }}</n-button>
    </div>
  </div>
</template>

<style scoped>
.ab {
  border: 1px solid rgba(128, 128, 128, 0.2);
  border-radius: 8px;
  padding: 10px 12px;
  margin-bottom: 14px;
}
.ab.on {
  border-color: rgba(24, 160, 88, 0.5);
}
.ab-head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.ab-sum {
  font-size: 12.5px;
  opacity: 0.7;
}
.ab-rule {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px 8px;
  margin-top: 10px;
  font-size: 13px;
}
.ab-foot {
  display: flex;
  justify-content: space-between;
  align-items: flex-end;
  gap: 12px;
  margin-top: 10px;
}
.ab-note {
  font-size: 12px;
  opacity: 0.6;
}
@media (max-width: 600px) {
  .ab-foot {
    flex-direction: column;
    align-items: stretch;
  }
}
</style>
