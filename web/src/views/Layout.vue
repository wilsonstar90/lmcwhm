<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { useRoute, useRouter, RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { NLayout, NLayoutSider, NLayoutHeader, NLayoutContent, NMenu, NButton, NSpace, type MenuOption } from 'naive-ui'
import { useSession } from '../stores/session'
import LangSwitch from '../components/LangSwitch.vue'
import ThemeSwitch from '../components/ThemeSwitch.vue'
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
      <div class="brand">{{ session.alias }}</div>
      <n-menu :value="(route.name as string)" :options="menu" />
    </n-layout-sider>
    <n-layout>
      <n-layout-header bordered class="header">
        <span class="user">{{ session.user?.username }}</span>
        <n-space :size="8" align="center">
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
  font-weight: 600;
  font-size: 18px;
  padding: 16px 20px;
  color: #20a53a;
}
.header {
  height: 53px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 16px;
}
.user {
  opacity: 0.75;
}
</style>
