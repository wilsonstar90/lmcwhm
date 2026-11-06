// Time zone pickers: IANA names labelled with their current UTC offset, so a
// zone such as Etc/GMT+8 shows as UTC-08:00 (POSIX signs are inverted).

/** Minutes east of UTC for zone at date (NaN when the zone is unknown). */
export function tzOffset(zone: string, at = new Date()): number {
  try {
    const parts = new Intl.DateTimeFormat('en-US', {
      timeZone: zone, hourCycle: 'h23', year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit',
    }).formatToParts(at)
    const v = (k: string) => Number(parts.find((p) => p.type === k)?.value)
    const local = Date.UTC(v('year'), v('month') - 1, v('day'), v('hour'), v('minute'), v('second'))
    return Math.round((local - Math.floor(at.getTime() / 1000) * 1000) / 60000)
  } catch {
    return NaN
  }
}

/** "UTC+08:00" for an offset in minutes. */
export function offsetLabel(min: number): string {
  if (Number.isNaN(min)) return ''
  const s = min < 0 ? '-' : '+'
  const a = Math.abs(min)
  return `UTC${s}${String(Math.floor(a / 60)).padStart(2, '0')}:${String(a % 60).padStart(2, '0')}`
}

/** "Asia/Hong_Kong (UTC+08:00)". */
export function zoneLabel(zone: string): string {
  const o = offsetLabel(tzOffset(zone))
  return o ? `${zone} (${o})` : zone
}

/** The zones the browser knows, or `fallback` where Intl cannot list them. */
export function knownZones(fallback: string[] = []): string[] {
  const intl = Intl as unknown as { supportedValuesOf?: (k: string) => string[] }
  let list: string[] = []
  try {
    list = intl.supportedValuesOf?.('timeZone') ?? []
  } catch {
    /* older Safari */
  }
  return list.length ? list : fallback
}

/** Select options sorted by offset, then name; `keep` are always included. */
export function zoneOptions(zones: string[], keep: string[] = []): { label: string; value: string }[] {
  const all = [...new Set([...zones, 'UTC', ...keep.filter(Boolean)])]
  return all
    .map((z) => ({ z, o: tzOffset(z) }))
    .sort((a, b) => (a.o || 0) - (b.o || 0) || a.z.localeCompare(b.z))
    .map(({ z }) => ({ label: zoneLabel(z), value: z }))
}
