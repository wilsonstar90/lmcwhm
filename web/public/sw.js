// lmcwhm service worker: installable PWA with an offline-capable app shell.
// The API is never cached; hashed /assets/* are cache-first; pages are
// network-first with the cached shell as fallback.
const CACHE = 'lmcwhm-shell-v3'
const PUSH_DEDUPE = 'lmcwhm-push-dedupe-v1'
const recentPushes = new Map()

self.addEventListener('install', (e) => {
  self.skipWaiting()
  e.waitUntil(caches.open(CACHE).then((c) => c.addAll(['/', '/icons/icon-192.png']).catch(() => {})))
})

self.addEventListener('activate', (e) => {
  e.waitUntil(
    caches.keys().then((keys) => Promise.all(keys.filter((k) => k.startsWith('lmcwhm-shell-') && k !== CACHE).map((k) => caches.delete(k)))).then(() => self.clients.claim()),
  )
})

self.addEventListener('fetch', (e) => {
  const req = e.request
  const url = new URL(req.url)
  if (req.method !== 'GET' || url.origin !== location.origin) return
  if (url.pathname.startsWith('/api/') || url.pathname.startsWith('/mcp') || url.pathname.startsWith('/s/') ||
      url.pathname.startsWith('/adminer') || url.pathname.startsWith('/phpmyadmin')) return
  if (url.pathname.startsWith('/assets/') || url.pathname.startsWith('/icons/')) {
    e.respondWith(
      caches.match(req).then(
        (hit) =>
          hit ||
          fetch(req).then((res) => {
            if (res.ok) caches.open(CACHE).then((c) => c.put(req, res.clone()))
            return res
          }),
      ),
    )
    return
  }
  if (req.mode === 'navigate') {
    e.respondWith(
      fetch(req)
        .then((res) => {
          if (res.ok) caches.open(CACHE).then((c) => c.put('/', res.clone()))
          return res
        })
        .catch(() => caches.match('/').then((hit) => hit || Response.error())),
    )
  }
})

async function duplicatePush(data) {
  const identity = data.event_id ? `event-${data.event_id}` : data.time ? `${data.kind || 'alert'}-${data.time}` : ''
  if (!identity) return false
  const now = Date.now()
  const recent = recentPushes.get(identity)
  if (recent && now - recent < 10 * 60 * 1000) return true
  recentPushes.set(identity, now)
  try {
    const cache = await caches.open(PUSH_DEDUPE)
    const key = new Request(`/__push_dedupe__/${encodeURIComponent(identity)}`)
    const previous = await cache.match(key)
    if (previous && now - Number(await previous.text()) < 10 * 60 * 1000) return true
    await cache.put(key, new Response(String(now)))
    const keys = await cache.keys()
    for (let i = 0; i < keys.length - 50; i++) await cache.delete(keys[i])
  } catch {
    // The in-memory map still deduplicates while this service worker is alive.
  }
  return false
}

self.addEventListener('push', (event) => {
  let data = {}
  try {
    data = event.data ? event.data.json() : {}
  } catch {
    data = { body: event.data ? event.data.text() : '' }
  }
  const title = data.title || 'lmcwhm'
  event.waitUntil((async () => {
    if (await duplicatePush(data)) return
    const identity = data.event_id ? `event-${data.event_id}` : `${data.kind || 'alert'}-${data.time || Date.now()}`
    await self.registration.showNotification(title, {
      body: data.body || '',
      icon: '/icons/icon-192.png',
      badge: '/icons/icon-192.png',
      tag: `lmcwhm-${identity}`,
      data: { url: data.url || '/alerts' },
    })
  })())
})

self.addEventListener('notificationclick', (event) => {
  event.notification.close()
  const target = event.notification.data?.url || '/alerts'
  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then(async (windows) => {
      for (const client of windows) {
        if (client.url.startsWith(self.location.origin)) {
          await client.focus()
          if ('navigate' in client) await client.navigate(target)
          return
        }
      }
      return self.clients.openWindow(target)
    }),
  )
})
