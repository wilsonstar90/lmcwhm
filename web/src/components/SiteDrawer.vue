<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NDrawer, NDrawerContent, NTabs, NTabPane, NForm, NFormItem, NInput, NSelect, NButton, NSpace, NSwitch, NAlert,
  NDescriptions, NDescriptionsItem, NCheckbox, NCode, NPopconfirm, NTag, NInputNumber, useMessage,
} from 'naive-ui'
import { get, patch, post, ApiError } from '../api/client'
import type { InstalledApp, Site } from '../api/types'
import { datetime } from '../format'
import SiteAdvanced from './SiteAdvanced.vue'
import SiteStats from './SiteStats.vue'
import SiteWordPress from './SiteWordPress.vue'
import DomainEditor from './DomainEditor.vue'

const props = defineProps<{ site: Site | null; phpVersions: string[]; hasCF: boolean }>()
const emit = defineEmits<{ close: []; task: [id: string, title: string]; changed: [] }>()
const { t, locale } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))

const php = ref('')
const vhost = ref('')
const delFiles = ref(false)
const delDNS = ref(true)
const runtimeVersion = ref('')
const command = ref('')
const appPort = ref<number | null>(null)
const runtimes = ref<InstalledApp[]>([])
const project = ref<{ state: string; logs: string } | null>(null)
const editingDomain = ref<number | null>(null)
const linked = ref<Site[]>([])
const isProject = computed(() => !!props.site && ['node', 'python', 'java', 'tomcat'].includes(props.site.type))

async function loadProject() {
  if (!isProject.value) return
  try {
    project.value = await get<{ state: string; logs: string }>(`/sites/${props.site!.id}/project?tail=200`)
  } catch {
    project.value = null
  }
}
async function composer(action: string) {
  try {
    const r = await post<{ task_id: string }>(`/sites/${props.site!.id}/composer`, { action })
    emit('task', r.task_id, `composer ${action}`)
  } catch (e) {
    fail(e)
  }
}
async function restartProject() {
  try {
    await post(`/sites/${props.site!.id}/project/restart`)
    msg.success(t('common.done'))
    setTimeout(loadProject, 1500)
  } catch (e) {
    fail(e)
  }
}
const runtimeOptions = computed(() =>
  runtimes.value.filter((r) => r.app === props.site?.type).map((r) => ({ label: `${r.name} ${r.version}`, value: r.version })),
)

watch(
  () => props.site,
  (s) => {
    if (!s) return
    php.value = s.php_version
    vhost.value = ''
    runtimeVersion.value = s.runtime_version
    command.value = s.command
    appPort.value = s.app_port || null
    project.value = null
    get<{ config: string }>(`/sites/${s.id}/vhost`).then((r) => (vhost.value = r.config)).catch(() => {})
    get<{ items: Site[] }>('/sites').then((r) => (linked.value = r.items.filter((x) => x.target === 'link' && x.target_site === s.id))).catch(() => {})
    if (['node', 'python', 'java'].includes(s.type)) {
      loadProject()
      get<{ items: InstalledApp[] }>('/apps').then((r) => (runtimes.value = r.items.filter((a) => a.kind === 'runtime'))).catch(() => {})
    }
  },
  { immediate: true },
)

async function update(body: Record<string, unknown>, title: string) {
  try {
    const r = await patch<{ task_id: string }>(`/sites/${props.site!.id}`, body)
    emit('task', r.task_id, title)
  } catch (e) {
    fail(e)
  }
}

async function remove() {
  try {
    const r = await post<{ task_id: string }>(`/sites/${props.site!.id}/delete`, { files: delFiles.value, dns: delDNS.value })
    emit('task', r.task_id, t('site.delete'))
    emit('close')
  } catch (e) {
    fail(e)
  }
}

const phpOptions = computed(() => props.phpVersions.map((v) => ({ label: `PHP ${v}`, value: v })))
const days = computed(() =>
  props.site?.cert_not_after ? Math.floor((props.site.cert_not_after * 1000 - Date.now()) / 86400000) : 0,
)
</script>

<template>
  <n-drawer :show="!!site" :width="'min(640px, 100vw)'" @update:show="(v: boolean) => !v && emit('close')">
    <n-drawer-content v-if="site" :title="site.domain" closable>
      <n-tabs type="line" animated>
        <n-tab-pane name="domains" :tab="t('site.tab_domains')">
          <n-alert type="default" :show-icon="false" style="margin-bottom: 12px">{{ t('dom.drawer_hint') }}</n-alert>
          <n-descriptions :column="1" label-placement="left" size="small">
            <n-descriptions-item :label="t('route.host')">
              <n-space :size="4"><b>{{ site.domain }}</b><n-tag v-for="a in site.aliases" :key="a" size="tiny">{{ a }}</n-tag></n-space>
            </n-descriptions-item>
            <n-descriptions-item :label="t('dom.target')">
              {{ t(`dom.targets.${site.target || 'site'}`) }}
              <n-tag v-if="site.target && site.target !== 'site'" size="tiny" type="warning" style="margin-left: 6px">{{ t('dom.not_serving') }}</n-tag>
            </n-descriptions-item>
            <n-descriptions-item label="SSL">{{ site.ssl_mode === 'none' ? t('route.cert_none') : `${site.cert_issuer} · ${t('site.days_left', { n: days })}` }}</n-descriptions-item>
            <n-descriptions-item label="DNS">{{ site.dns_managed ? 'Cloudflare' + (site.dns_proxied ? ' ☁' : '') : '—' }}</n-descriptions-item>
            <n-descriptions-item :label="t('dom.linked')">
              <n-space v-if="linked.length" :size="4"><n-tag v-for="l in linked" :key="l.id" size="tiny" type="info">{{ l.domain }}</n-tag></n-space>
              <span v-else>—</span>
            </n-descriptions-item>
            <n-descriptions-item :label="t('site.root')"><code>{{ site.root }}</code></n-descriptions-item>
            <n-descriptions-item :label="t('site.created')">{{ datetime(site.created_at, locale) }}</n-descriptions-item>
          </n-descriptions>
          <n-button type="primary" style="margin-top: 16px" @click="editingDomain = site.id">{{ t('dom.manage_domain') }}</n-button>
          <DomainEditor :site-id="editingDomain" :has-c-f="hasCF" @close="editingDomain = null" @changed="emit('changed')" @task="(id: string, title: string) => emit('task', id, title)" />
        </n-tab-pane>

        <n-tab-pane v-if="isProject" name="project" :tab="t('site.tab_project')">
          <n-space align="center" style="margin-bottom: 12px">
            <n-tag :type="project?.state === 'running' ? 'success' : project?.state ? 'warning' : 'default'" size="small">
              {{ project?.state || t('app.state.missing') }}
            </n-tag>
            <n-button size="small" @click="restartProject">{{ t('app.restart') }}</n-button>
            <n-button size="small" @click="loadProject">{{ t('common.refresh') }}</n-button>
          </n-space>
          <n-form label-placement="top">
            <n-space>
              <n-form-item :label="t('site.runtime_version')">
                <n-select v-model:value="runtimeVersion" :options="runtimeOptions" style="width: 180px" />
              </n-form-item>
              <n-form-item :label="t('site.app_port')">
                <n-input-number v-model:value="appPort" :min="1024" :max="65535" :show-button="false" style="width: 140px" />
              </n-form-item>
            </n-space>
            <n-form-item :label="t('site.command')" :feedback="t('site.command_hint')">
              <n-input v-model:value="command" type="textarea" :rows="2" />
            </n-form-item>
            <n-button
              type="primary"
              @click="update({ runtime_version: runtimeVersion, command, app_port: appPort ?? 0 }, t('site.tab_project'))"
            >{{ t('common.save') }}</n-button>
          </n-form>
          <p class="hint" style="margin-top: 16px">{{ t('site.project_logs') }}</p>
          <pre class="log">{{ project?.logs || '—' }}</pre>
        </n-tab-pane>

        <n-tab-pane v-if="site.type === 'php'" name="php" tab="PHP">
          <n-form label-placement="top">
            <n-form-item :label="t('site.php_version')" :feedback="t('site.php_switch_hint')">
              <n-select v-model:value="php" :options="phpOptions" style="width: 200px" />
            </n-form-item>
            <n-button type="primary" :disabled="php === site.php_version" @click="update({ php_version: php }, 'PHP ' + php)">
              {{ t('common.save') }}
            </n-button>
          </n-form>
          <h4 style="margin: 20px 0 8px">Composer</h4>
          <p class="hint">{{ t('site.composer_hint') }}</p>
          <n-space>
            <n-button v-for="a in ['install', 'update', 'dump-autoload', 'validate', 'outdated']" :key="a" size="small" @click="composer(a)">composer {{ a }}</n-button>
          </n-space>
        </n-tab-pane>

        <n-tab-pane v-if="site.type === 'php'" name="wordpress" tab="WordPress" display-directive="if">
          <SiteWordPress :site="site" @task="(id: string, title: string) => emit('task', id, title)" />
        </n-tab-pane>

        <n-tab-pane v-if="!site.passthrough" name="advanced" :tab="t('adv.title')" display-directive="if">
          <SiteAdvanced :site="site" @task="(id: string, title: string) => emit('task', id, title)" />
        </n-tab-pane>

        <n-tab-pane v-if="!site.passthrough" name="stats" :tab="t('stats.title')" display-directive="if">
          <SiteStats :domain="site.domain" />
        </n-tab-pane>

        <n-tab-pane name="config" :tab="t('site.tab_config')">
          <p class="hint">{{ t('site.config_hint') }}</p>
          <n-code :code="vhost" language="nginx" word-wrap />
        </n-tab-pane>

        <n-tab-pane name="danger" :tab="t('site.tab_danger')">
          <n-space vertical>
            <n-space align="center">
              <n-switch :value="!site.stopped" @update:value="(v: boolean) => update({ stopped: !v }, v ? t('site.start') : t('site.stop'))" />
              <span>{{ site.stopped ? t('site.stopped') : t('site.running') }}</span>
            </n-space>
            <n-checkbox v-model:checked="delFiles">{{ t('site.delete_files', { root: site.root }) }}</n-checkbox>
            <n-checkbox v-if="site.dns_managed" v-model:checked="delDNS">{{ t('site.delete_dns') }}</n-checkbox>
            <n-popconfirm @positive-click="remove">
              <template #trigger><n-button type="error">{{ t('site.delete') }}</n-button></template>
              {{ t('site.delete_confirm', { domain: site.domain }) }}
            </n-popconfirm>
          </n-space>
        </n-tab-pane>
      </n-tabs>
    </n-drawer-content>
  </n-drawer>
</template>

<style scoped>
.hint {
  margin: 0 0 8px;
  font-size: 12px;
  opacity: 0.7;
}
.log {
  margin: 0;
  max-height: 320px;
  overflow: auto;
  font-size: 12px;
  padding: 10px;
  border-radius: 4px;
  background: #1e1e1e;
  color: #ddd;
  white-space: pre-wrap;
  word-break: break-all;
}
</style>
