<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { useRoute, useRouter, RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { NLayout, NLayoutSider, NLayoutHeader, NLayoutContent, NMenu, NButton, NSpace, type MenuOption } from 'naive-ui'
import { useSession } from '../stores/session'
import LangSwitch from '../components/LangSwitch.vue'
import ThemeSwitch from '../components/ThemeSwitch.vue'
import BrandLogo from '../components/BrandLogo.vue'
import TaskBox from '../components/TaskBox.vue'
import MaintenanceBar from '../components/MaintenanceBar.vue'
import { arrange, loadMenu, menuConfig } from '../menu'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const session = useSession()
// The menu folds away on phones and iPads in portrait.
const collapsed = ref(window.innerWidth < 900)

const items = ['home', 'website', 'domains', 'database', 'docker', 'appstore', 'files', 'ftp', 'terminal', 'cron', 'processes', 'backup', 'security', 'monitor', 'uptime', 'alerts', 'logs', 'mail', 'toolbox', 'ai', 'settings']
// Sub-users see the modules they were granted (the API enforces it too).
const can = (key: string) => {
  const u = session.user
  if (!u || u.is_admin || key === 'home') return true
  if (key === 'settings') return true // the account tab
  return (u.perms ?? []).includes(key)
}
onMounted(loadMenu)
const menu = computed<MenuOption[]>(() =>
  arrange(items, menuConfig.value).filter((k) => can(k) && !menuConfig.value.hidden.includes(k)).map((key) => ({
    key,
    label: () => h(RouterLink, { to: { name: key } }, { default: () => t(`menu.${key}`) }),
  })),
)

async function logout() {
  await session.logout()
  router.push({ name: 'login' })
}
</script>

<template>
  <n-layout has-sider class="shell">
    <n-layout-sider
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
          <!-- The menu is folded away on phones: the logo and messages move here. -->
          <template v-if="collapsed">
            <BrandLogo />
            <TaskBox v-if="session.user?.is_admin" />
          </template>
          <span v-else class="user">{{ session.user?.username }}</span>
        </div>
        <n-space :size="8" align="center" :wrap="false">
          <MaintenanceBar v-if="session.user?.is_admin" compact />
          <ThemeSwitch />
          <LangSwitch />
          <n-button size="small" quaternary @click="logout">{{ t('menu.logout') }}</n-button>
        </n-space>
      </n-layout-header>
      <n-layout-content content-style="padding: 16px;" :native-scrollbar="false" class="content">
        <router-view />
      </n-layout-content>
    </n-layout>
  </n-layout>
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
</style>
