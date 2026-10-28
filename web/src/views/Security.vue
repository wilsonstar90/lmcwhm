<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { NMenu, NSelect, type MenuOption } from 'naive-ui'
import { get } from '../api/client'
import { datetime } from '../format'
import SecTraffic from '../components/SecTraffic.vue'
import SecFirewall from '../components/SecFirewall.vue'
import SecIPs from '../components/SecIPs.vue'
import SecLists from '../components/SecLists.vue'
import SecCloud from '../components/SecCloud.vue'
import SecWAF from '../components/SecWAF.vue'
import SecCrowdSec from '../components/SecCrowdSec.vue'
import SecScan from '../components/SecScan.vue'
import SecMalware from '../components/SecMalware.vue'
import SecTamper from '../components/SecTamper.vue'

// Security: an overview of every module on top (click a tile to open it),
// then the modules grouped in a side menu: network, intrusion, checks.
interface Overview {
  firewall: { available: boolean; enabled: boolean; policy: string; rules: number; pending: boolean }
  ips: { block: number; allow: number; bans: number }
  lists: { trusted: number; blocking: number; enabled: number }
  cloud: { linode: boolean; cf: boolean; syncing: boolean; last_sync: number; errors: number }
  waf: { enabled: boolean; blocked: number }
  crowdsec: { installed: boolean; state: string; bans: number }
  scan: { score: number; at: number }
  malware: { at: number; state: string; findings: number; files: number }
  tamper: { rules: number; enabled: number; events: number }
}

const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const sections = ['overview', 'firewall', 'ips', 'lists', 'cloud', 'waf', 'crowdsec', 'scan', 'malware', 'tamper'] as const
type Section = (typeof sections)[number]
const groups: { key: string; items: Section[] }[] = [
  { key: 'overview', items: ['overview'] },
  { key: 'network', items: ['firewall', 'ips', 'lists', 'cloud'] },
  { key: 'intrusion', items: ['waf', 'crowdsec'] },
  { key: 'checks', items: ['scan', 'malware', 'tamper'] },
]
const initial = route.query.tab as Section
const tab = ref<Section>(sections.includes(initial) ? initial : 'overview')
watch(tab, (v) => router.replace({ query: { ...route.query, tab: v } }))

const o = ref<Overview | null>(null)
async function loadOverview() {
  try {
    o.value = await get<Overview>('/security/overview')
  } catch {
    o.value = null
  }
}
onMounted(loadOverview)
watch(tab, loadOverview)

type Tone = 'ok' | 'warn' | 'bad' | 'off'
interface Tile { key: Section; tone: Tone; status: string; value: string; sub: string }
const n = (x: number) => x.toLocaleString()
const tiles = computed<Tile[]>(() => {
  const v = o.value
  if (!v) return []
  const on = t('sec.on'), off = t('sec.off')
  const ago = (at: number) => (at ? datetime(at, locale.value) : t('sec.never'))
  return [
    {
      key: 'firewall', tone: !v.firewall.available ? 'bad' : v.firewall.pending ? 'warn' : v.firewall.enabled ? 'ok' : 'off',
      status: v.firewall.enabled ? on : off, value: v.firewall.enabled ? t(`sec.policy.${v.firewall.policy}`) : '—', sub: t('sec.ov.rules', { n: v.firewall.rules }),
    },
    { key: 'ips', tone: 'ok', status: t('sec.ov.manual'), value: n(v.ips.block), sub: t('sec.ov.ips', { allow: n(v.ips.allow), bans: n(v.ips.bans) }) },
    {
      key: 'lists', tone: v.lists.enabled ? 'ok' : 'off', status: v.lists.enabled ? t('sec.ov.lists_on', { n: v.lists.enabled }) : off,
      value: n(v.lists.blocking), sub: t('sec.ov.trusted', { n: n(v.lists.trusted) }),
    },
    {
      key: 'cloud', tone: v.cloud.errors ? 'bad' : v.cloud.syncing ? 'ok' : 'off', status: v.cloud.syncing ? on : off,
      value: [v.cloud.linode && 'Linode', v.cloud.cf && 'Cloudflare'].filter(Boolean).join(' · ') || '—', sub: t('sec.ov.synced', { at: ago(v.cloud.last_sync) }),
    },
    { key: 'waf', tone: v.waf.enabled ? 'ok' : 'off', status: v.waf.enabled ? on : off, value: n(v.waf.blocked), sub: t('sec.ov.blocked_24h') },
    {
      key: 'crowdsec', tone: !v.crowdsec.installed ? 'off' : v.crowdsec.state === 'running' ? 'ok' : 'bad',
      status: v.crowdsec.installed ? v.crowdsec.state || '—' : t('sec.not_installed'), value: v.crowdsec.installed ? n(v.crowdsec.bans) : '—', sub: t('sec.ov.bans'),
    },
    {
      key: 'scan', tone: !v.scan.at ? 'off' : v.scan.score >= 80 ? 'ok' : v.scan.score >= 60 ? 'warn' : 'bad',
      status: v.scan.at ? t('sec.ov.score') : t('sec.never'), value: v.scan.at ? String(v.scan.score) : '—', sub: ago(v.scan.at),
    },
    {
      key: 'malware', tone: !v.malware.at ? 'off' : v.malware.findings ? 'bad' : 'ok',
      status: v.malware.at ? t('sec.ov.suspicious') : t('sec.never'), value: v.malware.at ? n(v.malware.findings) : '—', sub: ago(v.malware.at),
    },
    {
      key: 'tamper', tone: v.tamper.enabled ? (v.tamper.events ? 'warn' : 'ok') : 'off', status: v.tamper.enabled ? on : off,
      value: n(v.tamper.enabled), sub: t('sec.ov.tamper', { n: v.tamper.events }),
    },
  ]
})

const menu = computed<MenuOption[]>(() =>
  groups.map((g) => ({
    type: 'group', key: g.key, label: t(`sec.groups.${g.key}`),
    children: g.items.map((k) => ({ key: k, label: t(`sec.s.${k}.title`) })),
  })),
)
const flat = computed(() => groups.flatMap((g) => g.items.map((k) => ({ label: `${t(`sec.groups.${g.key}`)} · ${t(`sec.s.${k}.title`)}`, value: k }))))
const comps = { overview: SecTraffic, firewall: SecFirewall, ips: SecIPs, lists: SecLists, cloud: SecCloud, waf: SecWAF, crowdsec: SecCrowdSec, scan: SecScan, malware: SecMalware, tamper: SecTamper }
</script>

<template>
  <div class="tiles">
    <button v-for="x in tiles" :key="x.key" type="button" class="tile" :class="[x.tone, { active: tab === x.key }]" @click="tab = x.key">
      <div class="tile-head">
        <span class="tile-name">{{ t(`sec.s.${x.key}.title`) }}</span>
        <span class="tile-status"><i class="dot" />{{ x.status }}</span>
      </div>
      <div class="tile-value">{{ x.value }}</div>
      <div class="tile-sub">{{ x.sub }}</div>
    </button>
  </div>

  <div class="layout">
    <aside class="side">
      <n-menu :value="tab" :options="menu" :indent="16" @update:value="(k: Section) => (tab = k)" />
    </aside>
    <n-select class="side-select" :value="tab" :options="flat" @update:value="(k: Section) => (tab = k)" />
    <section class="main">
      <header class="head">
        <h2>{{ t(`sec.s.${tab}.title`) }}</h2>
        <p>{{ t(`sec.s.${tab}.desc`) }}</p>
      </header>
      <KeepAlive>
        <component :is="comps[tab]" :key="tab" @changed="loadOverview" />
      </KeepAlive>
    </section>
  </div>
</template>

<style scoped>
.tiles {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(118px, 1fr));
  gap: 10px;
  margin-bottom: 16px;
}
.tile {
  --tone: #8a8f98;
  text-align: left;
  font: inherit;
  color: inherit;
  cursor: pointer;
  background: var(--card, rgba(128, 128, 128, 0.06));
  border: 1px solid rgba(128, 128, 128, 0.18);
  border-radius: 8px;
  padding: 10px 12px;
  transition: border-color 0.15s, box-shadow 0.15s;
}
.tile:hover {
  border-color: rgba(128, 128, 128, 0.45);
}
.tile.active {
  border-color: var(--tone);
  box-shadow: inset 3px 0 0 var(--tone);
}
.tile.ok { --tone: #18a058; }
.tile.warn { --tone: #f0a020; }
.tile.bad { --tone: #d03050; }
.tile-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
  font-size: 12.5px;
}
.tile-name {
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.tile-status {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 12px;
  opacity: 0.8;
  white-space: nowrap;
}
.dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--tone);
}
.tile-value {
  font-size: 22px;
  font-weight: 600;
  margin: 6px 0 2px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.tile-sub {
  font-size: 12px;
  opacity: 0.6;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.layout {
  display: grid;
  grid-template-columns: 190px minmax(0, 1fr);
  gap: 20px;
  align-items: start;
}
.side {
  position: sticky;
  top: 12px;
  border-right: 1px solid rgba(128, 128, 128, 0.15);
}
.side-select {
  display: none;
}
.head h2 {
  font-size: 17px;
  margin: 2px 0 4px;
}
.head p {
  margin: 0 0 14px;
  font-size: 13px;
  opacity: 0.65;
}
@media (max-width: 900px) {
  .layout {
    grid-template-columns: minmax(0, 1fr);
  }
  .side {
    display: none;
  }
  .side-select {
    display: block;
  }
}
</style>
