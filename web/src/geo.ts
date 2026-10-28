// IP geolocation lookups, batched per tick and cached for the session.
import { reactive } from 'vue'
import { post } from './api/client'

export interface GeoInfo {
  ip: string
  private?: boolean
  country?: string
  region?: Record<string, string>
  city?: Record<string, string>
  asn?: number
  org?: string
}

const cache = reactive(new Map<string, GeoInfo>())
let pending = new Set<string>()
let timer: number | undefined

async function flush() {
  const ips = [...pending]
  pending = new Set()
  timer = undefined
  for (let i = 0; i < ips.length; i += 500) {
    try {
      const res = await post<Record<string, GeoInfo>>('/geoip/lookup', { ips: ips.slice(i, i + 500) })
      for (const [ip, info] of Object.entries(res)) cache.set(ip, info)
    } catch {
      /* no database yet: leave blank */
    }
  }
}

export function geo(ip: string): GeoInfo | undefined {
  if (!ip) return undefined
  const hit = cache.get(ip)
  if (hit || pending.has(ip)) return hit
  pending.add(ip)
  timer ??= window.setTimeout(flush, 30)
  return undefined
}

export function flag(code?: string): string {
  if (!code || code.length !== 2) return ''
  return String.fromCodePoint(...[...code.toUpperCase()].map((c) => 0x1f1a5 + c.charCodeAt(0)))
}

const regionNames = new Map<string, Intl.DisplayNames>()
// pick prefers the UI language: zh-HK / zh-TW fall back to DB-IP's zh-CN names.
function pick(names: Record<string, string> | undefined, locale: string): string {
  if (!names) return ''
  const lang = locale.startsWith('zh') ? 'zh-CN' : locale.split('-')[0]
  return names[lang] ?? names.en ?? ''
}

export function geoLabel(info: GeoInfo | undefined, locale: string, lan: string): string {
  if (!info) return ''
  if (info.private) return lan
  if (!info.country) return ''
  let dn = regionNames.get(locale)
  if (!dn) {
    try {
      dn = new Intl.DisplayNames([locale], { type: 'region' })
      regionNames.set(locale, dn)
    } catch {
      /* old browser */
    }
  }
  const parts = [dn?.of(info.country) ?? info.country, pick(info.region, locale), pick(info.city, locale)].filter(Boolean)
  return [...new Set(parts)].join(' · ')
}
