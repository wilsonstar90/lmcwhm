<script setup lang="ts">
import { computed, h, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter, RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { NLayout, NLayoutSider, NLayoutHeader, NLayoutContent, NMenu, NButton, NSpace, NDrawer, NDrawerContent, NPopover, useMessage, type MenuOption } from 'naive-ui'
import { useSession } from '../stores/session'
import LangSwitch from '../components/LangSwitch.vue'
import ThemeSwitch from '../components/ThemeSwitch.vue'
import BrandLogo from '../components/BrandLogo.vue'
import TaskBox from '../components/TaskBox.vue'
import MaintenanceBar from '../components/MaintenanceBar.vue'
import { arrange, loadMenu, menuConfig } from '../menu'
import { get } from '../api/client'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const session = useSession()
const msg = useMessage()
const viewportWidth = ref(window.innerWidth)
const isMobile = computed(() => viewportWidth.value < 900)
const collapsed = ref(isMobile.value)
const mobileOpen = ref(false)
const resize = () => { viewportWidth.value = window.innerWidth }
watch(isMobile, (mobile) => {
  collapsed.value = mobile
  if (!mobile) mobileOpen.value = false
})

const items = ['home', 'website', 'domains', 'database', 'docker', 'appstore', 'files', 'ftp', 'terminal', 'cron', 'processes', 'backup', 'security', 'monitor', 'uptime', 'alerts', 'logs', 'mail', 'toolbox', 'ai', 'settings']
// Sub-users see the modules they were granted (the API enforces it too).
const can = (key: string) => {
  const u = session.user
  if (!u || u.is_admin || key === 'home') return true
  if (key === 'settings') return true // the account tab
  return (u.perms ?? []).includes(key)
}
const ips = ref({ ipv4: '', ipv6: '' })
onMounted(() => {
  loadMenu()
  window.addEventListener('resize', resize, { passive: true })
  if (session.user?.is_admin) {
    get<{ ipv4: string; ipv6: string }>('/web/ips').then((r) => { ips.value = r }).catch(() => {})
  }
})
onBeforeUnmount(() => window.removeEventListener('resize', resize))
const menu = computed<MenuOption[]>(() =>
  arrange(items, menuConfig.value).filter((k) => can(k) && !menuConfig.value.hidden.includes(k)).map((key) => ({
    key,
    label: () => h(RouterLink, { to: { name: key }, onClick: () => { mobileOpen.value = false } }, { default: () => t(`menu.${key}`) }),
  })),
)

async function copyIP(ip: string) {
  try {
    await navigator.clipboard.writeText(ip)
    msg.success(t('common.copied'))
  } catch (e) {
    msg.error(String(e))
  }
}
async function logout() {
  await session.logout()
  router.push({ name: 'login' })
}
</script>

<template>
  <n-layout has-sider class="shell">
    <n-layout-sider
      class="desktop-sider"
      bordered
      collapse-mode="width"
      :collapsed-width="0"
      :width="200"
      :collapsed="collapsed"
      show-trigger="bar"
      @collapse="collapsed = true"
      @expand="collapsed = false"
    >
      <div class="brand"><BrandLogo /><TaskBox v-if="session.user?.is_admin" /></div>
      <n-menu :value="(route.name as string)" :options="menu" />
    </n-layout-sider>
    <n-layout>
      <n-layout-header bordered class="header">
        <div class="left">
          <n-button v-if="isMobile" quaternary class="menu-button" aria-label="Menu" @click="mobileOpen = true">☰</n-button>
          <!-- Mobile navigation uses the drawer below. -->
          <template v-if="collapsed">
            <BrandLogo />
            <TaskBox v-if="session.user?.is_admin" />
          </template>
          <template v-else>
            <span class="user">{{ session.user?.username }}</span>
            <div v-if="session.user?.is_admin" class="ips">
              <n-button v-if="ips.ipv4" size="tiny" quaternary class="server-ip" :title="`${t('common.copy')}: ${ips.ipv4}`" @click="copyIP(ips.ipv4)">IPv4 {{ ips.ipv4 }}</n-button>
              <n-button v-if="ips.ipv6" size="tiny" quaternary class="server-ip v6" :title="`${t('common.copy')}: ${ips.ipv6}`" @click="copyIP(ips.ipv6)">IPv6 {{ ips.ipv6 }}</n-button>
            </div>
          </template>
        </div>
        <n-space :size="8" align="center" :wrap="false">
          <n-popover v-if="isMobile && session.user?.is_admin" trigger="click" placement="bottom-end" style="max-width: calc(100vw - 24px)">
            <template #trigger><n-button size="small" quaternary aria-label="Server IP">IP</n-button></template>
            <div class="mobile-ips">
              <n-button v-if="ips.ipv4" size="small" quaternary :title="`${t('common.copy')}: ${ips.ipv4}`" @click="copyIP(ips.ipv4)">IPv4 {{ ips.ipv4 }}</n-button>
              <n-button v-if="ips.ipv6" size="small" quaternary :title="`${t('common.copy')}: ${ips.ipv6}`" @click="copyIP(ips.ipv6)">IPv6 {{ ips.ipv6 }}</n-button>
              <span v-if="!ips.ipv4 && !ips.ipv6">—</span>
            </div>
          </n-popover>
          <n-popover v-if="isMobile" trigger="click" placement="bottom-end" style="max-width: calc(100vw - 24px)">
            <template #trigger><n-button size="small" quaternary :aria-label="`${t('theme.title')} / ${t('common.language')}`">Aa</n-button></template>
            <div class="mobile-display"><ThemeSwitch /><LangSwitch /></div>
          </n-popover>
          <MaintenanceBar v-if="session.user?.is_admin" compact />
          <ThemeSwitch v-if="!isMobile" />
          <LangSwitch v-if="!isMobile" />
          <n-button size="small" quaternary @click="logout">{{ t('menu.logout') }}</n-button>
        </n-space>
      </n-layout-header>
      <n-layout-content content-style="padding: 16px;" :native-scrollbar="false" class="content">
        <router-view />
      </n-layout-content>
    </n-layout>
  </n-layout>
  <n-drawer :show="mobileOpen" placement="left" :width="280" @update:show="(v: boolean) => { mobileOpen = v }">
    <n-drawer-content body-content-style="padding: 0;" class="mobile-menu">
      <div class="brand"><BrandLogo /><TaskBox v-if="session.user?.is_admin" /></div>
      <n-menu :value="(route.name as string)" :options="menu" />
    </n-drawer-content>
  </n-drawer>
</template>

<style scoped>
/* dvh follows the iOS address bar; vh is the fallback. */
.shell {
  height: 100vh;
  height: 100dvh;
}
.content {
  height: calc(100vh - 53px);
  height: calc(100dvh - 53px);
}
.brand {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 14px 16px 14px 20px;
  min-height: 32px;
}
.left {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}
.header {
  height: 53px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 0 16px;
}
.user {
  opacity: 0.75;
}
.ips {
  display: flex;
  align-items: center;
  gap: 2px;
  margin-left: 4px;
}
.server-ip {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 11px;
  opacity: 0.65;
}
:deep(.v6 .n-button__content) {
  overflow: visible;
  white-space: nowrap;
}
@media (max-width: 1200px) {
  .ips {
    display: none;
  }
}

.menu-button {
  font-size: 22px;
  min-width: 44px;
  min-height: 44px;
}
.mobile-menu :deep(.n-menu-item-content) {
  min-height: 44px;
}
@media (max-width: 899px) {
  .desktop-sider {
    display: none;
  }
  .header {
    height: 56px;
    padding-left: max(10px, env(safe-area-inset-left));
    padding-right: max(10px, env(safe-area-inset-right));
  }
  .content {
    height: calc(100vh - 56px);
    height: calc(100dvh - 56px);
  }
  .left,
  .header > .n-space {
    min-width: 0;
  }
  .header :deep(.n-button) {
    white-space: nowrap;
  }
}

.mobile-ips {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 4px;
  max-width: calc(100vw - 64px);
}
.mobile-ips :deep(.n-button__content) {
  white-space: normal;
  overflow-wrap: anywhere;
}
.mobile-display {
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: 10px;
  min-width: 220px;
}
@media (max-width: 899px) {
  .content :deep(.n-layout-scroll-container) {
    padding: 12px !important;
  }
}
</style>
