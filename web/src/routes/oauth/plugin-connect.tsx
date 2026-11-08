import { createFileRoute } from '@tanstack/react-router'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { OAUTH_POPUP_CALLBACK_MESSAGE } from '@/features/auth/constants'
import {
  getOAuthSessionStorage,
  resolveOAuthCallbackMode,
} from '@/features/auth/lib/oauth-callback-mode'

function PluginOAuthCallback() {
  const { t } = useTranslation()
  const [callback] = useState(() => {
    const query = new URLSearchParams(window.location.search)
    return {
      code: query.get('code') ?? undefined,
      state: query.get('state') ?? '',
      error: query.get('error') ?? undefined,
    }
  })
  const [available, setAvailable] = useState(true)
  useEffect(() => {
    const mode = resolveOAuthCallbackMode('plugin-connect', callback.state, {
      opener: window.opener,
      storage: getOAuthSessionStorage(window),
    })
    window.history.replaceState(
      window.history.state,
      '',
      window.location.pathname
    )
    if (mode !== 'bind' || !window.opener) {
      setAvailable(false)
      return
    }
    window.opener.postMessage(
      {
        type: OAUTH_POPUP_CALLBACK_MESSAGE,
        intent: 'bind',
        provider: 'plugin-connect',
        ...callback,
      },
      window.location.origin
    )
  }, [callback])
  return (
    <main className='mx-auto max-w-lg space-y-3 p-6'>
      <h1 className='text-xl font-semibold'>{t('Provider OAuth account')}</h1>
      <p role='status'>
        {available
          ? t('Return to the channel window to finish connecting.')
          : t(
              'The connection window is unavailable. Start connecting again from the channel.'
            )}
      </p>
    </main>
  )
}

export const Route = createFileRoute('/oauth/plugin-connect')({
  component: PluginOAuthCallback,
  head: () => ({ meta: [{ name: 'referrer', content: 'no-referrer' }] }),
})
