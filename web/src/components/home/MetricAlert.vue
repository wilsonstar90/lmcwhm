<script setup lang="ts">
// The 告警 switch and 設置 of a dashboard card: the alert centre's rule of
// that kind (cpu / load / mem / disk), created on first use with every channel.
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { NButton, NInputNumber, NPopover, NSpace, NSwitch, useMessage } from 'naive-ui'
import { ApiError, get, post, put } from '../../api/client'

interface Rule { id: number; name: string; kind: string; params: { threshold?: number; minutes?: number }; channels: number[]; enabled: boolean; cooldown: number }

const props = defineProps<{ kind: 'cpu' | 'load' | 'mem' | 'disk' }>()
const { t } = useI18n()
const msg = useMessage()
const router = useRouter()
const rule = ref<Rule | null>(null)
const channels = ref<number[]>([])
const edit = ref<{ threshold: number; minutes: number }>({ threshold: 80, minutes: 5 })
const showEdit = ref(false)
const timed = computed(() => props.kind !== 'disk')

async function load() {
  try {
    const [rs, cs] = await Promise.all([get<{ items: Rule[] }>('/alerts/rules'), get<{ items: { id: number }[] }>('/alerts/channels')])
    rule.value = rs.items.find((r) => r.kind === props.kind) ?? null
    channels.value = cs.items.map((c) => c.id)
    if (rule.value) edit.value = { threshold: rule.value.params.threshold ?? 80, minutes: rule.value.params.minutes ?? 5 }
  } catch {
    /* no access to the alert centre */
  }
}
onMounted(load)

async function save(enabled: boolean) {
  if (!rule.value && !channels.value.length) {
    msg.warning(t('hc.need_channel'))
    router.push({ name: 'alerts' })
    return
  }
  const params = timed.value ? { threshold: edit.value.threshold, minutes: edit.value.minutes } : { threshold: edit.value.threshold }
  try {
    if (rule.value) {
      await put(`/alerts/rules/${rule.value.id}`, { ...rule.value, enabled, params })
    } else {
      await post('/alerts/rules', { name: t(`hc.alert_name.${props.kind}`), kind: props.kind, params, channels: channels.value, enabled, cooldown: 30 })
    }
    msg.success(t('common.saved'))
    showEdit.value = false
    load()
  } catch (e) {
    msg.error(e instanceof ApiError ? e.text : String(e))
  }
}
</script>

<template>
  <n-space align="center" :size="8" class="alert">
    <span>{{ t('hc.alert') }}</span>
    <n-switch size="small" :value="!!rule?.enabled" @update:value="save" />
    <span class="sep">|</span>
    <n-popover v-model:show="showEdit" trigger="click" placement="bottom">
      <template #trigger><a class="link">{{ t('hc.settings') }}</a></template>
      <n-space vertical :size="8" style="width: 220px">
        <div>
          {{ props.kind === 'load' ? t('hc.threshold_load') : t('hc.threshold') }}
          <n-input-number v-model:value="edit.threshold" size="small" :min="1" :max="props.kind === 'load' ? 1000 : 100" />
        </div>
        <div v-if="timed">
          {{ t('hc.minutes') }}
          <n-input-number v-model:value="edit.minutes" size="small" :min="1" :max="60" />
        </div>
        <n-button size="small" type="primary" @click="save(true)">{{ t('common.save') }}</n-button>
      </n-space>
    </n-popover>
  </n-space>
</template>

<style scoped>
.alert {
  font-size: 13px;
}
.sep {
  opacity: 0.3;
}
.link {
  color: #20a53a;
  cursor: pointer;
}
</style>
