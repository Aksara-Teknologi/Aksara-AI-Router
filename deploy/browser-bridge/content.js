(() => {
  if (globalThis.newApiOAuthBridgeActive) return;
  globalThis.newApiOAuthBridgeActive = true;
  window.addEventListener('message', (event) => {
    if (event.source !== window || event.origin !== location.origin || event.data?.type !== 'NEW_API_OAUTH_BRIDGE_START') return;
    const { state, redirectUri, expiresAt } = event.data;
    chrome.runtime.sendMessage({ type: 'NEW_API_OAUTH_BRIDGE_START', state, redirectUri, expiresAt }).catch(() => {});
  });
})();
