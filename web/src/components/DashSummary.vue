<script setup lang="ts">
// Dashboard counts, today's traffic and the security check (首頁風險).
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { NButton, NCard, NGi, NGrid, NProgress, NSpace, NTag } from 'naive-ui'
import { get } from '../api/client'
import { bytes, datetime } from '../format'
import AiButton from './AiButton.vue'

interface Risk { code: string; level: 'critical' | 'warning' | 'info'; value?: string }
interface Summary {
  sites: number; sites_stopped: number; ssl_expiring: number; databases: number; containers: number; running: number; cron_jobs: number; cron_failed: number
  processes: number; procs_down: number; probes: number; probes_down: number; backups: number; last_backup: number; pv: number; uv: number; bandwidth: number
  blocked_ips: number; alerts_24h: number; risks: Risk[]; score: number
}

const { t, locale } = useI18n()
const router = useRouter()
const s = ref<Summary | null>(null)
onMounted(async () => {
  try {
    s.value = await get<Summary>('/system/summary')
  } catch {
    /* older backend or transient error */
  }
})
// Where each finding is fixed.
const fixRoute: Record<string, string> = {
  no_entry: 'settings', default_port: 'settings', panel_http: 'settings', panel_open: 'settings', no_2fa: 'settings',
  firewall_off: 'security', waf_off: 'security', crowdsec_off: 'security', no_recent_backup: 'backup', no_alert_channel: 'alerts',
  ssl_expiring: 'website', probes_down: 'uptime', cron_failed: 'cron', procs_down: 'processes',
}
const tiles = computed(() => {
  const x = s.value!
  return [
    { label: t('menu.website'), value: x.sites, sub: x.sites_stopped ? t('dash.stopped', { n: x.sites_stopped }) : '', to: 'website' },
    { label: t('menu.database'), value: x.databases, sub: '', to: 'database' },
    { label: t('menu.docker'), value: `${x.running} / ${x.containers}`, sub: t('dash.running'), to: 'docker' },
    { label: t('menu.cron'), value: x.cron_jobs, sub: x.cron_failed ? t('dash.failed', { n: x.cron_failed }) : '', to: 'cron', bad: x.cron_failed > 0 },
    { label: t('menu.processes'), value: x.processes, sub: x.procs_down ? t('dash.down', { n: x.procs_down }) : '', to: 'processes', bad: x.procs_down > 0 },
    { label: t('menu.uptime'), value: x.probes, sub: x.probes_down ? t('dash.down', { n: x.probes_down }) : '', to: 'uptime', bad: x.probes_down > 0 },
    { label: t('menu.backup'), value: x.backups, sub: x.last_backup ? datetime(x.last_backup, locale.value) : t('dash.never'), to: 'backup' },
    { label: t('dash.today'), value: `${x.pv.toLocaleString(locale.value)} PV`, sub: `${x.uv.toLocaleString(locale.value)} UV · ${bytes(x.bandwidth)}`, to: 'website' },
    { label: t('dash.blocked'), value: x.blocked_ips, sub: '', to: 'security' },
    { label: t('dash.alerts'), value: x.alerts_24h, sub: t('dash.last24h'), to: 'alerts', bad: x.alerts_24h > 0 },
  ]
})
const scoreStatus = computed(() => (!s.value ? 'default' : s.value.score >= 85 ? 'success' : s.value.score >= 60 ? 'warning' : 'error'))
const levelType = (l: string) => (l === 'critical' ? 'error' : l === 'warning' ? 'warning' : 'info')
</script>

<template>
  <template v-if="s">
    <n-grid cols="2 s:3 m:5" responsive="screen" :x-gap="12" :y-gap="12" style="margin-bottom: 16px">
      <n-gi v-for="x in tiles" :key="x.label">
        <n-card size="small" hoverable class="tile" @click="router.push({ name: x.to })">
          <div class="label">{{ x.label }}</div>
          <div :class="['value', { bad: x.bad }]">{{ x.value }}</div>
          <div class="sub">{{ x.sub || ' ' }}</div>
        </n-card>
      </n-gi>
    </n-grid>
    <n-card size="small" :title="t('dash.security')" style="margin-bottom: 16px">
      <template #header-extra><AiButton size="tiny" :prompt="t('ai.quick.security.prompt')" /></template>
      <n-space align="center" :size="24" :wrap="true">
        <n-progress type="circle" :percentage="s.score" :status="scoreStatus" style="width: 84px">{{ s.score }}</n-progress>
        <div style="flex: 1; min-width: 260px">
          <p v-if="!s.risks.length" style="margin: 0">{{ t('dash.no_risks') }}</p>
          <div v-for="r in s.risks" :key="r.code" class="risk">
            <n-tag size="small" :type="levelType(r.level)">{{ t(`dash.level.${r.level}`) }}</n-tag>
            <span>{{ t(`dash.risks.${r.code}`, { n: r.value }) }}</span>
            <n-button v-if="fixRoute[r.code]" text type="primary" size="small" @click="router.push({ name: fixRoute[r.code] })">{{ t('dash.fix') }} →</n-button>
          </div>
        </div>
      </n-space>
    </n-card>
  </template>
</template>

<style scoped>
.tile {
  cursor: pointer;
}
.label {
  font-size: 12px;
  opacity: 0.7;
}
.value {
  font-size: 22px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  margin: 2px 0;
}
.value.bad {
  color: #d03050;
}
.sub {
  font-size: 12px;
  opacity: 0.6;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.risk {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 3px 0;
  flex-wrap: wrap;
}
</style>
