const CALLBACKS = new Set([
  'http://localhost:1455/auth/callback',
  'http://localhost:54545/callback',
  'http://localhost:51121/callback',
]);

chrome.action.onClicked.addListener(async (tab) => {
  if (!tab.id || !tab.url) return;
  const url = new URL(tab.url);
  if (url.protocol !== 'https:' && !(url.protocol === 'http:' && ['localhost', '127.0.0.1'].includes(url.hostname))) return;
  await chrome.scripting.executeScript({ target: { tabId: tab.id }, files: ['content.js'] });
  await chrome.action.setBadgeText({ tabId: tab.id, text: 'ON' });
});

chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
  if (message?.type !== 'NEW_API_OAUTH_BRIDGE_START' || !sender.tab?.id || sender.frameId !== 0 || !sender.url) return;
  const page = new URL(sender.url);
  if (page.protocol !== 'https:' && !(page.protocol === 'http:' && ['localhost', '127.0.0.1'].includes(page.hostname))) return;
  if (!CALLBACKS.has(message.redirectUri) || !/^[A-Za-z0-9_-]{43}$/.test(message.state)) return;
  const now = Math.floor(Date.now() / 1000);
  if (!Number.isInteger(message.expiresAt) || message.expiresAt <= now || message.expiresAt > now + 610) return;
  chrome.storage.session.set({ [message.state]: { origin: page.origin, tabId: sender.tab.id, redirectUri: message.redirectUri, expiresAt: message.expiresAt } }).then(() => sendResponse({ success: true }));
  return true;
});

chrome.webNavigation.onBeforeNavigate.addListener(async (details) => {
  if (details.frameId !== 0) return;
  const callback = new URL(details.url);
  const target = callback.origin + callback.pathname;
  if (!CALLBACKS.has(target) || callback.hash || callback.username || callback.password) return;
  if (callback.searchParams.getAll('state').length !== 1 || callback.searchParams.getAll('code').length !== 1) return;
  const state = callback.searchParams.get('state');
  const code = callback.searchParams.get('code');
  if (!state || !code || code.length > 8192 || callback.searchParams.has('error')) return;
  const stored = await chrome.storage.session.get(state);
  const flow = stored[state];
  if (!flow || flow.redirectUri !== target || flow.expiresAt <= Date.now() / 1000) return;
  const popup = await chrome.tabs.get(details.tabId);
  if (popup.openerTabId !== flow.tabId) return;
  const opener = await chrome.tabs.get(flow.tabId);
  if (!opener.url || new URL(opener.url).origin !== flow.origin) return;
  await chrome.storage.session.remove(state);
  const destination = new URL('/oauth/plugin-connect', flow.origin);
  destination.searchParams.set('code', code);
  destination.searchParams.set('state', state);
  await chrome.tabs.update(details.tabId, { url: destination.href });
}, { url: [{ hostEquals: 'localhost', schemes: ['http'] }] });

chrome.tabs.onRemoved.addListener(async (tabId) => {
  const flows = await chrome.storage.session.get(null);
  const expired = Object.keys(flows).filter((state) => flows[state].tabId === tabId || flows[state].expiresAt <= Date.now() / 1000);
  if (expired.length) await chrome.storage.session.remove(expired);
});
