<script setup lang="ts">
// File manager extras: favourites, search (names / contents), remote
// download and share links. Files.vue opens these through the exposed methods.
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NButton, NCard, NCheckbox, NDataTable, NDropdown, NForm, NFormItem, NInput, NInputNumber, NModal, NPopconfirm, NSpace, NTag, useMessage,
  type DataTableColumns,
} from 'naive-ui'
import { get, post, ApiError } from '../api/client'
import { bytes, datetime } from '../format'
import TaskModal from './TaskModal.vue'

const props = defineProps<{ cwd: string }>()
const emit = defineEmits<{ open: [path: string, dir: boolean]; changed: [] }>()
const { t, locale } = useI18n()
const msg = useMessage()
const fail = (e: unknown) => msg.error(e instanceof ApiError ? e.text : String(e))

// ---- favourites ----
const favs = ref<{ id: number; path: string }[]>([])
async function loadFavs() {
  favs.value = (await get<{ items: { id: number; path: string }[] }>('/files/favorites').catch(() => ({ items: [] }))).items
}
onMounted(loadFavs)
const isFav = computed(() => favs.value.some((f) => f.path === props.cwd))
async function toggleFav() {
  await post('/files/favorites', { path: props.cwd, on: !isFav.value }).catch(fail)
  loadFavs()
}
const favMenu = computed(() => [
  { label: isFav.value ? t('fx.unfav') : t('fx.fav'), key: '__toggle' },
  ...(favs.value.length ? [{ type: 'divider', key: 'd' }] : []),
  ...favs.value.map((f) => ({ label: f.path, key: f.path })),
])
function onFav(k: string) {
  if (k === '__toggle') toggleFav()
  else emit('open', k, true)
}

// ---- search ----
interface Hit { path: string; is_dir: boolean; size: number; line?: number; text?: string }
const search = ref<{ q: string; content: boolean; ext: string; hits: Hit[]; truncated: boolean; loading: boolean } | null>(null)
function openSearch() {
  search.value = { q: '', content: false, ext: '', hits: [], truncated: false, loading: false }
}
async function runSearch() {
  const s = search.value!
  s.loading = true
  try {
    const r = await get<{ items: Hit[]; truncated: boolean }>(
      `/files/search?dir=${encodeURIComponent(props.cwd)}&q=${encodeURIComponent(s.q)}&content=${s.content ? 1 : 0}&ext=${encodeURIComponent(s.ext)}`,
    )
    s.hits = r.items
    s.truncated = r.truncated
  } catch (e) {
    fail(e)
  } finally {
    s.loading = false
  }
}
const hitCols = computed<DataTableColumns<Hit>>(() => [
  {
    title: () => t('files.name'),
    key: 'path',
    render: (x) =>
      h('div', [
        h('a', { class: 'link', onClick: () => ((search.value = null), emit('open', x.path, x.is_dir)) }, (x.is_dir ? '📁 ' : '📄 ') + x.path),
        x.text ? h('div', { class: 'sub' }, `${x.line}: ${x.text}`) : null,
      ]),
  },
  { title: () => t('files.size'), key: 'size', width: 100, render: (x) => (x.is_dir ? '—' : bytes(x.size)) },
])

// ---- remote download ----
const fetchForm = ref<{ url: string; name: string } | null>(null)
const task = ref<{ id: string; title: string } | null>(null)
async function startFetch() {
  const f = fetchForm.value!
  try {
    const r = await post<{ task_id: string }>('/files/fetch', { url: f.url, dir: props.cwd, name: f.name })
    fetchForm.value = null
    task.value = { id: r.task_id, title: t('fx.remote') }
  } catch (e) {
    fail(e)
  }
}

// ---- share links ----
interface Share { id: number; token: string; path: string; has_password: boolean; expires_at: number; max_downloads: number; downloads: number; created_at: number }
const shareForm = ref<{ path: string; password: string; hours: number; max: number; created?: Share } | null>(null)
const shares = ref<Share[] | null>(null)
function share(path: string) {
  shareForm.value = { path, password: '', hours: 72, max: 0 }
}
async function createShare() {
  const f = shareForm.value!
  try {
    f.created = await post<Share>('/files/shares', { path: f.path, password: f.password, hours: f.hours, max_downloads: f.max })
  } catch (e) {
    fail(e)
  }
}
const link = (s: Share) => `${location.origin}/s/${s.token}`
async function openShares() {
  shares.value = (await get<{ items: Share[] }>('/files/shares').catch(() => ({ items: [] as Share[] }))).items
}
async function delShare(s: Share) {
  await post(`/files/shares/${s.id}/delete`).catch(fail)
  openShares()
}
const copy = (s: string) => navigator.clipboard?.writeText(s).then(() => msg.success(t('common.copied')))
const now = () => Date.now() / 1000
const shareCols = computed<DataTableColumns<Share>>(() => [
  { title: () => t('files.name'), key: 'path', ellipsis: { tooltip: true } },
  {
    title: () => t('fx.expires'),
    key: 'expires_at',
    width: 170,
    render: (s) =>
      s.expires_at && s.expires_at < now()
        ? h(NTag, { size: 'small' }, () => t('fx.expired'))
        : s.expires_at
          ? datetime(s.expires_at, locale.value)
          : t('fx.never'),
  },
  { title: () => t('fx.downloads'), key: 'downloads', width: 100, render: (s) => `${s.downloads}${s.max_downloads ? ` / ${s.max_downloads}` : ''}` },
  {
    title: '',
    key: 'a',
    width: 150,
    render: (s) =>
      h(NSpace, { size: 4 }, () => [
        h(NButton, { size: 'tiny', onClick: () => copy(link(s)) }, () => t('common.copy')),
        h(NPopconfirm, { onPositiveClick: () => delShare(s) }, { trigger: () => h(NButton, { size: 'tiny', type: 'error', quaternary: true }, () => t('common.delete')), default: () => t('common.confirm_delete') }),
      ]),
  },
])

defineExpose({ share })
</script>

<template>
  <n-dropdown :options="favMenu" trigger="click" @select="onFav"><n-button size="small">{{ isFav ? '★' : '☆' }} {{ t('fx.favorites') }}</n-button></n-dropdown>
  <n-button size="small" @click="openSearch">{{ t('fx.search') }}</n-button>
  <n-button size="small" @click="fetchForm = { url: '', name: '' }">{{ t('fx.remote') }}</n-button>
  <n-button size="small" @click="openShares">{{ t('fx.shares') }}</n-button>

  <n-modal :show="!!search" @update:show="(v: boolean) => !v && (search = null)">
    <n-card v-if="search" :title="`${t('fx.search')} · ${cwd}`" style="width: min(860px, calc(100vw - 32px))" closable @close="search = null">
      <n-space align="center" style="margin-bottom: 12px">
        <n-input v-model:value="search.q" :placeholder="t('fx.query')" style="width: 260px" @keyup.enter="runSearch" />
        <n-checkbox v-model:checked="search.content">{{ t('fx.in_content') }}</n-checkbox>
        <n-input v-if="search.content" v-model:value="search.ext" placeholder="php" style="width: 90px" />
        <n-button type="primary" :loading="search.loading" :disabled="search.q.trim().length < 2" @click="runSearch">{{ t('fx.search') }}</n-button>
      </n-space>
      <n-data-table size="small" :columns="hitCols" :data="search.hits" :max-height="460" :row-key="(x: Hit) => x.path" />
      <p v-if="search.truncated" class="sub">{{ t('fx.truncated') }}</p>
    </n-card>
  </n-modal>

  <n-modal :show="!!fetchForm" @update:show="(v: boolean) => !v && (fetchForm = null)">
    <n-card v-if="fetchForm" :title="t('fx.remote')" style="width: min(560px, calc(100vw - 32px))" closable @close="fetchForm = null">
      <n-form label-placement="top">
        <n-form-item label="URL"><n-input v-model:value="fetchForm.url" placeholder="https://wordpress.org/latest.zip" /></n-form-item>
        <n-form-item :label="t('fx.save_as')" :feedback="t('fx.save_in', { dir: cwd })"><n-input v-model:value="fetchForm.name" :placeholder="fetchForm.url.split('/').pop()?.split('?')[0]" /></n-form-item>
      </n-form>
      <template #footer>
        <n-space justify="end"><n-button @click="fetchForm = null">{{ t('common.cancel') }}</n-button><n-button type="primary" :disabled="!/^https?:\/\//.test(fetchForm.url)" @click="startFetch">{{ t('fx.download') }}</n-button></n-space>
      </template>
    </n-card>
  </n-modal>

  <n-modal :show="!!shareForm" @update:show="(v: boolean) => !v && (shareForm = null)">
    <n-card v-if="shareForm" :title="t('fx.share')" style="width: min(560px, calc(100vw - 32px))" closable @close="shareForm = null">
      <p style="word-break: break-all"><b>{{ shareForm.path }}</b></p>
      <template v-if="!shareForm.created">
        <n-form label-placement="left" label-width="140">
          <n-form-item :label="t('fx.valid_for')"><n-input-number v-model:value="shareForm.hours" :min="0" :max="8760" style="width: 150px"><template #suffix>{{ t('fx.hours') }}</template></n-input-number></n-form-item>
          <n-form-item :label="t('fx.max_downloads')"><n-input-number v-model:value="shareForm.max" :min="0" style="width: 150px" /></n-form-item>
          <n-form-item :label="t('ftp.password')"><n-input v-model:value="shareForm.password" :placeholder="t('fx.optional')" /></n-form-item>
        </n-form>
        <p class="sub">{{ t('fx.share_hint') }}</p>
      </template>
      <template v-else>
        <n-input :value="link(shareForm.created)" readonly />
        <n-button style="margin-top: 8px" @click="copy(link(shareForm.created!))">{{ t('common.copy') }}</n-button>
      </template>
      <template #footer>
        <n-space justify="end">
          <n-button @click="shareForm = null">{{ t('common.close') }}</n-button>
          <n-button v-if="!shareForm.created" type="primary" @click="createShare">{{ t('fx.create_link') }}</n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>

  <n-modal :show="!!shares" @update:show="(v: boolean) => !v && (shares = null)">
    <n-card v-if="shares" :title="t('fx.shares')" style="width: min(820px, calc(100vw - 32px))" closable @close="shares = null">
      <n-data-table size="small" :columns="shareCols" :data="shares" :row-key="(s: Share) => s.id" />
    </n-card>
  </n-modal>
  <TaskModal :task-id="task?.id ?? null" :title="task?.title ?? ''" @close="task = null; emit('changed')" @done="emit('changed')" />
</template>

<style scoped>
.sub {
  font-size: 12px;
  opacity: 0.65;
  margin-top: 2px;
}
.link {
  cursor: pointer;
  word-break: break-all;
}
</style>
