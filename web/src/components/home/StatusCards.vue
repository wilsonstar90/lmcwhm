<script setup lang="ts">
// 狀態 (like aaPanel): rings for load, CPU, memory and each disk. Hovering
// (tapping on touch screens) opens the details, the busiest processes, the
// card's alert and, for memory and disks, 立即釋放 / 立即清理.
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { NButton, NCard, NCollapse, NCollapseItem, NPopconfirm, NPopover, NProgress, NSpin, NTag, useMessage } from 'naive-ui'
import { ApiError, get, post } from '../../api/client'
import type { Overview, SysDetail } from '../../api/types'
import { bytes } from '../../format'
import MetricAlert from './MetricAlert.vue'

interface Proc { pid: number; name: string; user: string; cpu: number; rss: number; cmdline: string }

const props = defineProps<{ o: Overview }>()
const { t } = useI18n()
const msg = useMessage()
const router = useRouter()
const touch = typeof window !== 'undefined' && window.matchMedia?.('(hover: none)').matches
const detail = ref<SysDetail | null>(null)
const procs = ref<Proc[] | null>(null)
const showProcs = ref(false)

const pct = (used: number, total: number) => (total ? Math.round((used / total) * 100) : 0)
const color = (p: number) => (p >= 90 ? '#d03050' : p >= 70 ? '#f0a020' : '#20a53a')
const loadPct = computed(() => Math.round((props.o.load[0] / Math.max(props.o.cpu_cores, 1)) * 100))
const loadText = (p: number) => t(p < 30 ? 'hc.smooth' : p < 70 ? 'hc.normal' : p < 90 ? 'hc.slow' : 'hc.blocked')
const memPct = computed(() => pct(props.o.mem_used, props.o.mem_total))
const disks = computed(() => (props.o.disks ?? []).slice(0, 4))

async function open(v: boolean) {
  if (!v) return
  showProcs.value = false
  procs.value = null
  try {
    detail.value = await get<SysDetail>('/system/detail')
  } catch {
    /* keep the last one */
  }
}
async function loadProcs() {
  showProcs.value = !showProcs.value
  if (showProcs.value && !procs.value) {
    try {
      procs.value = (await get<{ items: Proc[] }>('/monitor/processes?n=5')).items
    } catch {
      procs.value = []
    }
  }
}
const topCPU = computed(() => [...(procs.value ?? [])].sort((a, b) => b.cpu - a.cpu).slice(0, 5))
const topMem = computed(() => [...(procs.value ?? [])].sort((a, b) => b.rss - a.rss).slice(0, 5))

const releasing = ref(false)
async function release() {
  releasing.value = true
  try {
    const r = await post<{ freed: number }>('/system/memory/release')
    msg.success(t('hc.released', { n: bytes(r.freed) }))
    detail.value = await get<SysDetail>('/system/detail')
  } catch (e) {
    msg.error(e instanceof ApiError ? e.text : String(e))
  } finally {
    releasing.value = false
  }
}
async function kill(p: Proc) {
  try {
    await post(`/toolbox/processes/${p.pid}/kill`, { force: false })
    msg.success(t('hc.killed', { name: p.name }))
    procs.value = (await get<{ items: Proc[] }>('/monitor/processes?n=5')).items
  } catch (e) {
    msg.error(e instanceof ApiError ? e.text : String(e))
  }
}
const timeKeys = ['user', 'nice', 'system', 'idle', 'iowait', 'irq', 'softirq', 'steal', 'guest', 'guest_nice']
const diskDetail = (mount: string) => detail.value?.disks?.find((d) => d.mount === mount)
</script>

<template>
  <n-card :title="t('hc.title')" size="small" class="status">
    <div class="rings">
      <!-- load -->
      <n-popover :trigger="touch ? 'click' : 'hover'" placement="bottom" :delay="150" style="padding: 0" @update:show="open">
        <template #trigger>
          <div class="ring" tabindex="0">
            <n-progress type="circle" :percentage="Math.min(100, loadPct)" :color="color(loadPct)" :stroke-width="7" :show-indicator="false" class="pg" />
            <div class="mid" :style="{ color: color(loadPct) }">{{ loadPct }}%</div>
            <b>{{ loadText(loadPct) }}</b>
            <span>{{ t('hc.load') }}</span>
          </div>
        </template>
        <div class="pop">
          <n-tag size="small" :type="loadPct >= 90 ? 'error' : loadPct >= 70 ? 'warning' : 'success'">{{ t('hc.load_at', { n: loadPct }) }}</n-tag>
          <h5>{{ t('hc.basic') }}</h5>
          <template v-if="detail">
            <div class="kv"><span>{{ t('hc.load_avg') }}</span><b>{{ detail.load.avg.map((l) => l.toFixed(2)).join(' / ') }}</b></div>
            <div class="kv"><span>{{ t('hc.procs') }}</span><b>{{ detail.load.procs_running }} / {{ detail.load.procs_total }}</b></div>
          </template>
          <n-spin v-else size="small" />
          <MetricAlert kind="load" class="sec" />
          <n-collapse class="sec">
            <n-collapse-item :title="t('hc.more_load')" name="t">
              <div class="grid2">
                <div v-for="k in timeKeys" :key="k" class="kv"><span>{{ k }}</span><b>{{ detail?.load.times?.[k] ?? 0 }}</b></div>
              </div>
            </n-collapse-item>
          </n-collapse>
          <a class="more" @click="loadProcs">{{ t('hc.top_cpu') }} {{ showProcs ? '▾' : '▸' }}</a>
          <table v-if="showProcs" class="procs">
            <tr><th>{{ t('hc.proc') }}</th><th>CPU</th></tr>
            <tr v-for="p in topCPU" :key="p.pid"><td :title="p.cmdline">{{ p.name }}</td><td>{{ p.cpu.toFixed(1) }}%</td></tr>
          </table>
        </div>
      </n-popover>

      <!-- CPU -->
      <n-popover :trigger="touch ? 'click' : 'hover'" placement="bottom" :delay="150" style="padding: 0" @update:show="open">
        <template #trigger>
          <div class="ring" tabindex="0">
            <n-progress type="circle" :percentage="Math.round(o.cpu_percent)" :color="color(o.cpu_percent)" :stroke-width="7" :show-indicator="false" class="pg" />
            <div class="mid" :style="{ color: color(o.cpu_percent) }">{{ Math.round(o.cpu_percent) }}%</div>
            <b>{{ t('hc.cores', { n: o.cpu_cores }) }}</b>
            <span>CPU</span>
          </div>
        </template>
        <div class="pop">
          <n-tag size="small" :type="o.cpu_percent >= 90 ? 'error' : o.cpu_percent >= 70 ? 'warning' : 'success'">{{ t('hc.used', { n: Math.round(o.cpu_percent) }) }}</n-tag>
          <h5>{{ t('hc.basic') }}</h5>
          <template v-if="detail">
            <p class="line">{{ detail.cpu.model }} × {{ detail.cpu.sockets || 1 }}</p>
            <p class="line">{{ t('hc.cpu_counts', { s: detail.cpu.sockets || 1, c: detail.cpu.cores, l: detail.cpu.threads }) }}</p>
          </template>
          <n-spin v-else size="small" />
          <MetricAlert kind="cpu" class="sec" />
          <n-collapse class="sec">
            <n-collapse-item :title="t('hc.per_core')" name="c">
              <div class="grid2">
                <div v-for="(v, i) in detail?.cpu.per_core ?? []" :key="i" class="kv"><span>{{ t('hc.core', { n: i + 1 }) }}</span><b>{{ v.toFixed(1) }}%</b></div>
              </div>
            </n-collapse-item>
          </n-collapse>
          <a class="more" @click="loadProcs">{{ t('hc.top_cpu') }} {{ showProcs ? '▾' : '▸' }}</a>
          <table v-if="showProcs" class="procs">
            <tr><th>{{ t('hc.proc') }}</th><th>CPU</th></tr>
            <tr v-for="p in topCPU" :key="p.pid"><td :title="p.cmdline">{{ p.name }}</td><td>{{ p.cpu.toFixed(1) }}%</td></tr>
          </table>
        </div>
      </n-popover>

      <!-- memory -->
      <n-popover :trigger="touch ? 'click' : 'hover'" placement="bottom" :delay="150" style="padding: 0" @update:show="open">
        <template #trigger>
          <div class="ring" tabindex="0">
            <n-progress type="circle" :percentage="memPct" :color="color(memPct)" :stroke-width="7" :show-indicator="false" class="pg" />
            <div class="mid" :style="{ color: color(memPct) }">{{ memPct }}%</div>
            <b>{{ bytes(o.mem_used) }} / {{ bytes(o.mem_total) }}</b>
            <span>{{ t('hc.memory') }}</span>
          </div>
        </template>
        <div class="pop">
          <div class="head">
            <n-tag size="small" :type="memPct >= 90 ? 'error' : memPct >= 70 ? 'warning' : 'success'">{{ t('hc.used', { n: memPct }) }}</n-tag>
            <n-button size="tiny" type="primary" :loading="releasing" @click="release">{{ t('hc.release') }}</n-button>
          </div>
          <h5>{{ t('hc.mem_info') }}</h5>
          <div v-if="detail" class="grid2">
            <div class="kv"><span>{{ t('hc.free') }}</span><b>{{ bytes(detail.mem.free) }}</b></div>
            <div class="kv"><span>{{ t('hc.used_mem') }}</span><b>{{ bytes(detail.mem.used) }}</b></div>
            <div class="kv"><span>{{ t('hc.total') }}</span><b>{{ bytes(detail.mem.total) }}</b></div>
            <div class="kv"><span>{{ t('hc.shared') }}</span><b>{{ bytes(detail.mem.shared) }}</b></div>
            <div class="kv"><span>{{ t('hc.available') }}</span><b>{{ bytes(detail.mem.available) }}</b></div>
            <div class="kv"><span>buff/cache</span><b>{{ bytes(detail.mem.buffers) }} / {{ bytes(detail.mem.cached) }}</b></div>
          </div>
          <n-spin v-else size="small" />
          <MetricAlert kind="mem" class="sec" />
          <a class="more" @click="loadProcs">{{ t('hc.top_mem') }} {{ showProcs ? '▾' : '▸' }}</a>
          <table v-if="showProcs" class="procs">
            <tr><th>{{ t('hc.proc') }}</th><th>{{ t('hc.memory') }}</th><th></th></tr>
            <tr v-for="p in topMem" :key="p.pid">
              <td :title="p.cmdline">{{ p.name }}</td>
              <td>{{ bytes(p.rss) }}</td>
              <td>
                <n-popconfirm @positive-click="kill(p)">
                  <template #trigger><a class="link">{{ t('hc.kill') }}</a></template>
                  {{ t('hc.kill_confirm', { name: p.name, pid: p.pid }) }}
                </n-popconfirm>
              </td>
            </tr>
          </table>
        </div>
      </n-popover>

      <!-- disks -->
      <n-popover v-for="d in disks" :key="d.mount" :trigger="touch ? 'click' : 'hover'" placement="bottom" :delay="150" style="padding: 0" @update:show="open">
        <template #trigger>
          <div class="ring" tabindex="0">
            <n-progress type="circle" :percentage="pct(d.used, d.total)" :color="color(pct(d.used, d.total))" :stroke-width="7" :show-indicator="false" class="pg" />
            <div class="mid" :style="{ color: color(pct(d.used, d.total)) }">{{ pct(d.used, d.total) }}%</div>
            <b>{{ bytes(d.used) }} / {{ bytes(d.total) }}</b>
            <span class="mount" :title="d.mount">{{ d.mount }}</span>
          </div>
        </template>
        <div class="pop">
          <div class="head">
            <n-tag size="small" :type="pct(d.used, d.total) >= 90 ? 'error' : pct(d.used, d.total) >= 70 ? 'warning' : 'success'">
              {{ t('hc.disk_used', { n: (diskDetail(d.mount)?.percent ?? pct(d.used, d.total)).toFixed(2) }) }}
            </n-tag>
            <n-button size="tiny" type="primary" @click="router.push({ name: 'toolbox', query: { tab: 'cleanup' } })">{{ t('hc.clean') }}</n-button>
          </div>
          <h5>{{ t('hc.basic') }}</h5>
          <template v-if="diskDetail(d.mount)">
            <p class="line">{{ t('hc.mount') }}: {{ d.mount }}</p>
            <p class="line">{{ t('hc.disk_sizes', { t: bytes(d.total), f: bytes(diskDetail(d.mount)!.free), u: bytes(d.used) }) }}</p>
            <p class="line">{{ t('hc.device') }}: {{ diskDetail(d.mount)!.device }} · {{ diskDetail(d.mount)!.fs }}</p>
          </template>
          <n-spin v-else size="small" />
          <MetricAlert kind="disk" class="sec" />
          <n-collapse class="sec">
            <n-collapse-item :title="t('hc.inodes')" name="i">
              <template v-if="diskDetail(d.mount)">
                <div class="kv"><span>{{ t('hc.total') }}</span><b>{{ diskDetail(d.mount)!.inodes_total.toLocaleString() }}</b></div>
                <div class="kv"><span>{{ t('hc.used_mem') }}</span><b>{{ diskDetail(d.mount)!.inodes_used.toLocaleString() }}</b></div>
                <div class="kv"><span>{{ t('hc.free') }}</span><b>{{ diskDetail(d.mount)!.inodes_free.toLocaleString() }}</b></div>
                <div class="kv"><span>{{ t('hc.usage') }}</span><b>{{ diskDetail(d.mount)!.inodes_percent.toFixed(2) }}%</b></div>
              </template>
            </n-collapse-item>
          </n-collapse>
        </div>
      </n-popover>
    </div>
  </n-card>
</template>

<style scoped>
.rings {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(130px, 1fr));
  gap: 16px 8px;
}
.ring {
  position: relative;
  display: flex;
  flex-direction: column;
  align-items: center;
  cursor: pointer;
  outline: none;
  min-width: 0;
}
.pg {
  width: 96px !important;
}
.mid {
  position: absolute;
  top: 48px;
  transform: translateY(-50%);
  font-size: 20px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
.ring b {
  margin-top: 6px;
  font-size: 13px;
  text-align: center;
}
.ring span {
  font-size: 12px;
  opacity: 0.7;
}
.mount {
  max-width: 120px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.pop {
  width: min(360px, calc(100vw - 32px));
  max-height: 70vh;
  max-height: 70dvh;
  overflow: auto;
  padding: 12px 14px;
  font-size: 13px;
}
.head {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.pop h5 {
  margin: 12px 0 6px;
  padding: 6px 8px;
  background: rgba(128, 128, 128, 0.1);
  border-radius: 4px;
  font-size: 13px;
  font-weight: 500;
}
.line {
  margin: 4px 0;
}
.kv {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  padding: 3px 0;
}
.kv span {
  opacity: 0.75;
}
.kv b {
  font-weight: 500;
  font-variant-numeric: tabular-nums;
}
.grid2 {
  display: grid;
  grid-template-columns: 1fr 1fr;
  column-gap: 18px;
}
.sec {
  margin-top: 10px;
  padding-top: 10px;
  border-top: 1px solid rgba(128, 128, 128, 0.15);
}
.more {
  display: block;
  margin-top: 10px;
  padding: 6px 8px;
  background: rgba(128, 128, 128, 0.1);
  border-radius: 4px;
  cursor: pointer;
}
.procs {
  width: 100%;
  border-collapse: collapse;
  margin-top: 6px;
}
.procs th,
.procs td {
  text-align: left;
  padding: 5px 4px;
  border-bottom: 1px solid rgba(128, 128, 128, 0.12);
  max-width: 160px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.procs th {
  font-weight: 500;
  opacity: 0.7;
}
.link {
  color: #20a53a;
  cursor: pointer;
}
</style>
