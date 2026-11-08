import { useMutation, useQuery } from '@tanstack/react-query'
import { useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { openOAuthPopup } from '@/features/auth/lib/oauth-popup'
import {
  SecureVerificationDialog,
  useSecureVerification,
} from '@/features/auth/secure-verification'
import { AuthOperationError } from '@/lib/secure-verification'

import {
  disconnectPluginOAuth,
  finishPluginOAuth,
  getPluginOAuthStatus,
  startPluginOAuth,
  type PluginOAuthStart,
} from '../../plugin-oauth-api'

type Props = { channelId: number; channelName: string; onClose: () => void }

export function PluginOAuthDialog(props: Props) {
  const { t } = useTranslation()
  const fieldId = useId()
  const verification = useSecureVerification()
  const [start, setStart] = useState<PluginOAuthStart | null>(null)
  const [callbackUrl, setCallbackUrl] = useState('')
  const [error, setError] = useState('')
  const controller = useRef<AbortController | null>(null)
  const status = useQuery({
    queryKey: ['channel-plugin-oauth', props.channelId],
    queryFn: ({ signal }) => getPluginOAuthStatus(props.channelId, signal),
    retry: false,
    meta: { errorToast: false },
  })
  const cancelVerification = verification.cancel
  useEffect(
    () => () => {
      controller.current?.abort()
      cancelVerification()
    },
    [cancelVerification]
  )

  const connect = useMutation({
    mutationFn: async () => {
      setError('')
      const proof = await verification.requestVerification({
        scope: 'channel.oauth.manage',
        context: { channel_id: props.channelId, action: 'connect' },
        title: t('Verify to connect a provider account'),
      })
      if (!proof) return null
      return startPluginOAuth(props.channelId, proof.proof_token)
    },
    onSuccess: (value) => {
      setStart(value)
      setCallbackUrl('')
    },
    onError: (failure) => setError(AuthOperationError.from(failure).message),
    meta: { errorToast: false },
  })
  const complete = useMutation({
    mutationFn: async (url: string) => {
      if (!start) throw new AuthOperationError('Start connecting again.')
      setError('')
      return finishPluginOAuth(start.state, url, controller.current?.signal)
    },
    onSuccess: () => {
      controller.current?.abort()
      setStart(null)
      setCallbackUrl('')
      void status.refetch()
    },
    onError: (failure) => {
      setError(AuthOperationError.from(failure).message)
      setCallbackUrl('')
      setStart(null)
    },
    meta: { errorToast: false },
  })
  const disconnect = useMutation({
    mutationFn: async () => {
      setError('')
      const proof = await verification.requestVerification({
        scope: 'channel.oauth.manage',
        context: { channel_id: props.channelId, action: 'disconnect' },
        title: t('Verify to disconnect a provider account'),
      })
      if (!proof) return
      await disconnectPluginOAuth(props.channelId, proof.proof_token)
    },
    onSuccess: () => {
      setStart(null)
      setCallbackUrl('')
      void status.refetch()
    },
    onError: (failure) => setError(AuthOperationError.from(failure).message),
    meta: { errorToast: false },
  })
  const [opening, setOpening] = useState(false)
  const busy = connect.isPending || complete.isPending || disconnect.isPending

  const openSignIn = async () => {
    if (!start || busy || opening) return
    controller.current?.abort()
    const current = new AbortController()
    controller.current = current
    setOpening(true)
    setError('')
    try {
      window.postMessage(
        {
          type: 'NEW_API_OAUTH_BRIDGE_START',
          state: start.state,
          redirectUri: start.redirect_uri,
          expiresAt: start.expires_at,
        },
        window.location.origin
      )
      const exchange = await openOAuthPopup({
        provider: 'plugin-connect',
        intent: 'bind',
        signal: controller.current.signal,
        prepare: async () => ({
          state: start.state,
          url: start.authorization_url,
        }),
      })
      if (exchange.callback.error || !exchange.callback.code) {
        exchange.finish({ success: false })
        throw new AuthOperationError('OAuth authorization failed.')
      }
      const url = new URL(start.redirect_uri)
      url.searchParams.set('code', exchange.callback.code)
      url.searchParams.set('state', exchange.callback.state)
      try {
        await complete.mutateAsync(url.toString())
        exchange.finish({ success: true })
      } catch {
        exchange.finish({ success: false })
      }
    } catch (failure) {
      if (!current.signal.aborted) {
        setError(AuthOperationError.from(failure).message)
      }
    } finally {
      setOpening(false)
    }
  }

  return (
    <>
      <Dialog
        open
        onOpenChange={(open) => {
          if (!open) props.onClose()
        }}
        title={t('Provider OAuth account')}
        description={props.channelName}
        footer={
          <Button variant='outline' onClick={props.onClose}>
            {t('Close')}
          </Button>
        }
      >
        <div className='space-y-4'>
          {status.isPending && (
            <p role='status'>{t('Loading account connection...')}</p>
          )}
          {status.isError && (
            <div role='alert' className='space-y-2'>
              <p>{t('Could not load account connection.')}</p>
              <Button variant='outline' onClick={() => void status.refetch()}>
                {t('Retry')}
              </Button>
            </div>
          )}
          {status.data && !status.data.supported && (
            <div className='space-y-2'>
              <p>
                {t('This channel plugin does not support OAuth connections.')}
              </p>
              {status.data.stored && (
                <Button
                  variant='outline'
                  disabled={busy}
                  onClick={() => disconnect.mutate()}
                >
                  {t('Disconnect provider account')}
                </Button>
              )}
            </div>
          )}
          {status.data?.supported && (
            <>
              <p role='status'>
                {status.data.connected
                  ? t('Provider account connected')
                  : t('No provider account connected')}
              </p>
              {status.data.account_label && (
                <p className='text-muted-foreground break-all text-sm'>
                  {status.data.account_label}
                </p>
              )}
              <p className='text-muted-foreground text-sm'>
                {t(
                  'Sign in with your provider. Tokens are stored by the server and refreshed automatically.'
                )}
              </p>
              {!start && (
                <div className='flex flex-wrap gap-2'>
                  <Button
                    className='bg-foreground text-background hover:bg-foreground/90'
                    disabled={busy}
                    onClick={() => connect.mutate()}
                  >
                    {busy ? t('Please wait...') : t('Connect provider account')}
                  </Button>
                  {(status.data.connected || status.data.stored) && (
                    <Button
                      variant='outline'
                      disabled={busy}
                      onClick={() => disconnect.mutate()}
                    >
                      {t('Disconnect provider account')}
                    </Button>
                  )}
                </div>
              )}
              {start && (
                <div className='space-y-4'>
                  <Button
                    className='bg-foreground text-background hover:bg-foreground/90'
                    disabled={busy || opening}
                    onClick={() => void openSignIn()}
                  >
                    {opening
                      ? t('Waiting for provider sign-in...')
                      : t('Open provider sign-in')}
                  </Button>
                  <p className='text-muted-foreground text-sm'>
                    {t(
                      'If your provider redirects to localhost on another machine, paste the complete callback URL below. Do not paste an access token.'
                    )}
                  </p>
                  <form
                    className='space-y-2'
                    onSubmit={(event) => {
                      event.preventDefault()
                      complete.mutate(callbackUrl)
                    }}
                  >
                    <Label htmlFor={fieldId}>{t('Callback URL')}</Label>
                    <Input
                      id={fieldId}
                      type='url'
                      autoComplete='off'
                      spellCheck={false}
                      value={callbackUrl}
                      onChange={(event) => setCallbackUrl(event.target.value)}
                      disabled={complete.isPending}
                    />
                    <div className='flex flex-wrap gap-2'>
                      <Button
                        type='submit'
                        variant='outline'
                        disabled={!callbackUrl || complete.isPending}
                      >
                        {t('Complete connection')}
                      </Button>
                      <Button
                        type='button'
                        variant='ghost'
                        disabled={busy}
                        onClick={() => {
                          controller.current?.abort()
                          setStart(null)
                          setCallbackUrl('')
                        }}
                      >
                        {t('Cancel')}
                      </Button>
                    </div>
                  </form>
                </div>
              )}
            </>
          )}
          {error && (
            <p role='alert' className='text-destructive text-sm'>
              {t(error)}
            </p>
          )}
        </div>
      </Dialog>
      <SecureVerificationDialog {...verification.dialogProps} />
    </>
  )
}
