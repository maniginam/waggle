// Waggle Service Worker — Push Notifications + Offline Cache

const CACHE_NAME = 'waggle-v1';
const OFFLINE_URL = '/';

// Install: cache the shell
self.addEventListener('install', event => {
  event.waitUntil(
    caches.open(CACHE_NAME).then(cache => cache.add(OFFLINE_URL))
  );
  self.skipWaiting();
});

// Activate: clean old caches
self.addEventListener('activate', event => {
  event.waitUntil(
    caches.keys().then(keys =>
      Promise.all(keys.filter(k => k !== CACHE_NAME).map(k => caches.delete(k)))
    )
  );
  self.clients.claim();
});

// Fetch: network-first for API, cache-first for shell
self.addEventListener('fetch', event => {
  const url = new URL(event.request.url);

  // Never cache API calls or SSE
  if (url.pathname.startsWith('/api/') || url.pathname.startsWith('/events')) return;

  event.respondWith(
    fetch(event.request).then(response => {
      // Update cache with fresh copy
      if (response.ok) {
        const clone = response.clone();
        caches.open(CACHE_NAME).then(cache => cache.put(event.request, clone));
      }
      return response;
    }).catch(() => {
      // Offline fallback
      return caches.match(event.request).then(cached => cached || caches.match(OFFLINE_URL));
    })
  );
});

// Push notifications
self.addEventListener('push', event => {
  if (!event.data) return;

  let payload;
  try {
    payload = event.data.json();
  } catch (e) {
    payload = { title: 'waggle', body: event.data.text() };
  }

  const options = {
    body: payload.body || '',
    icon: '/favicon.ico',
    badge: '/favicon.ico',
    tag: payload.tag || 'waggle',
    data: { url: payload.url || '/' },
    vibrate: [100, 50, 100],
    requireInteraction: false,
  };

  event.waitUntil(
    self.registration.showNotification(payload.title || 'waggle', options)
  );
});

self.addEventListener('notificationclick', event => {
  event.notification.close();
  const url = event.notification.data?.url || '/';
  event.waitUntil(
    clients.matchAll({ type: 'window', includeUncontrolled: true }).then(clientList => {
      for (const client of clientList) {
        if (client.url.includes(self.location.origin) && 'focus' in client) {
          return client.focus();
        }
      }
      return clients.openWindow(url);
    })
  );
});
