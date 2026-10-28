<script setup lang="ts">
import { computed, h, ref } from 'vue'
import { useRoute, useRouter, RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { NLayout, NLayoutSider, NLayoutHeader, NLayoutContent, NMenu, NButton, NSpace, type MenuOption } from 'naive-ui'
import { useSession } from '../stores/session'
import LangSwitch from '../components/LangSwitch.vue'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const session = useSession()
const collapsed = ref(window.innerWidth < 768)

const items = ['home', 'website', 'domains', 'database', 'docker', 'appstore', 'files', 'ftp', 'terminal', 'cron', 'processes', 'backup', 'security', 'monitor', 'uptime', 'alerts', 'logs', 'ai', 'settings']
const menu = computed<MenuOption[]>(() =>
  items.map((key) => ({
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
  <n-layout has-sider style="height: 100vh">
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
          <LangSwitch />
          <n-button size="small" quaternary @click="logout">{{ t('menu.logout') }}</n-button>
        </n-space>
      </n-layout-header>
      <n-layout-content content-style="padding: 16px; max-width: 1280px;" :native-scrollbar="false" style="height: calc(100vh - 53px)">
        <router-view />
      </n-layout-content>
    </n-layout>
  </n-layout>
</template>

<style scoped>
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
