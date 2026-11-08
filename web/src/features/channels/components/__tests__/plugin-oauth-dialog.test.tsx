import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, test, vi } from 'vitest'

import { openOAuthPopup } from '@/features/auth/lib/oauth-popup'

import {
  disconnectPluginOAuth,
  finishPluginOAuth,
  getPluginOAuthStatus,
  startPluginOAuth,
} from '../../plugin-oauth-api'
import { PluginOAuthDialog } from '../dialogs/plugin-oauth-dialog'

const verification = vi.hoisted(() => ({
  requestVerification: vi.fn(),
  cancel: vi.fn(),
}))
vi.mock('@/features/auth/secure-verification', () => ({
  useSecureVerification: () => ({ ...verification, dialogProps: {} }),
  SecureVerificationDialog: () => null,
}))
vi.mock('../../plugin-oauth-api', () => ({
  getPluginOAuthStatus: vi.fn(),
  startPluginOAuth: vi.fn(),
  finishPluginOAuth: vi.fn(),
  disconnectPluginOAuth: vi.fn(),
}))
vi.mock('@/features/auth/lib/oauth-popup', () => ({ openOAuthPopup: vi.fn() }))

beforeEach(() => {
  vi.mocked(getPluginOAuthStatus).mockResolvedValue({
    supported: true,
    connected: false,
  })
  verification.requestVerification.mockResolvedValue({
    proof_token: 'test-proof',
  })
  vi.mocked(startPluginOAuth).mockResolvedValue({
    authorization_url: 'https://provider.example/authorize',
    state: 'test-state',
    redirect_uri: 'https://gateway.example/oauth/plugin-connect',
    expires_at: Date.now() / 1000 + 600,
  })
  vi.mocked(finishPluginOAuth).mockResolvedValue({ channel_id: 7 })
  vi.mocked(disconnectPluginOAuth).mockResolvedValue({ channel_id: 7 })
})

function mountDialog() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <PluginOAuthDialog
        channelId={7}
        channelName='Provider channel'
        onClose={vi.fn()}
      />
    </QueryClientProvider>
  )
}

test('connects from a pasted callback without requesting an access token', async () => {
  const user = userEvent.setup()
  mountDialog()
  await user.click(
    await screen.findByRole('button', { name: 'Connect provider account' })
  )
  expect(verification.requestVerification).toHaveBeenCalledWith(
    expect.objectContaining({
      scope: 'channel.oauth.manage',
      context: { channel_id: 7, action: 'connect' },
    })
  )
  await screen.findByRole('button', { name: 'Open provider sign-in' })
  const input = screen.getByRole('textbox', { name: 'Callback URL' })
  await user.type(
    input,
    'https://gateway.example/oauth/plugin-connect?code=one-time-code&state=test-state'
  )
  vi.mocked(getPluginOAuthStatus).mockResolvedValue({
    supported: true,
    connected: true,
  })
  await user.click(screen.getByRole('button', { name: 'Complete connection' }))
  await screen.findByText('Provider account connected')
  expect(startPluginOAuth).toHaveBeenCalledWith(7, 'test-proof')
  expect(finishPluginOAuth).toHaveBeenCalledWith(
    'test-state',
    expect.stringContaining('code=one-time-code'),
    undefined
  )
  expect(
    screen.queryByRole('textbox', { name: 'Callback URL' })
  ).not.toBeInTheDocument()
})

test('completes the browser popup through the shared OAuth transport', async () => {
  const finish = vi.fn()
  vi.mocked(openOAuthPopup).mockResolvedValue({
    callback: {
      provider: 'plugin-connect',
      state: 'test-state',
      code: 'popup-code',
    },
    signal: new AbortController().signal,
    finish,
  })
  const user = userEvent.setup()
  mountDialog()
  await user.click(
    await screen.findByRole('button', { name: 'Connect provider account' })
  )
  await user.click(
    await screen.findByRole('button', { name: 'Open provider sign-in' })
  )
  await waitFor(() => expect(finish).toHaveBeenCalledWith({ success: true }))
  expect(finishPluginOAuth).toHaveBeenCalledWith(
    'test-state',
    'https://gateway.example/oauth/plugin-connect?code=popup-code&state=test-state',
    expect.any(AbortSignal)
  )
})

test('requires a new proof to disconnect and respects verification cancellation', async () => {
  vi.mocked(getPluginOAuthStatus).mockResolvedValue({
    supported: true,
    connected: true,
  })
  verification.requestVerification.mockResolvedValueOnce(null)
  const user = userEvent.setup()
  mountDialog()
  await user.click(
    await screen.findByRole('button', { name: 'Disconnect provider account' })
  )
  await waitFor(() =>
    expect(verification.requestVerification).toHaveBeenCalled()
  )
  expect(disconnectPluginOAuth).not.toHaveBeenCalled()
  await user.click(
    screen.getByRole('button', { name: 'Disconnect provider account' })
  )
  await waitFor(() =>
    expect(disconnectPluginOAuth).toHaveBeenCalledWith(7, 'test-proof')
  )
  expect(verification.requestVerification).toHaveBeenLastCalledWith(
    expect.objectContaining({
      context: { channel_id: 7, action: 'disconnect' },
    })
  )
})

test('shows unsupported channels without a connection action', async () => {
  vi.mocked(getPluginOAuthStatus).mockResolvedValue({
    supported: false,
    connected: false,
  })
  mountDialog()
  await screen.findByText(
    'This channel plugin does not support OAuth connections.'
  )
  expect(
    screen.queryByRole('button', { name: 'Connect provider account' })
  ).not.toBeInTheDocument()
})

test('clears the callback and offers a fresh flow after an exchange failure', async () => {
  vi.mocked(finishPluginOAuth).mockRejectedValue(
    new Error('Plugin OAuth connection failed. Start connecting again.')
  )
  const user = userEvent.setup()
  mountDialog()
  await user.click(
    await screen.findByRole('button', { name: 'Connect provider account' })
  )
  await user.type(
    await screen.findByRole('textbox', { name: 'Callback URL' }),
    'https://gateway.example/oauth/plugin-connect?code=expired&state=test-state'
  )
  await user.click(screen.getByRole('button', { name: 'Complete connection' }))
  await screen.findByRole('alert')
  expect(
    screen.queryByRole('textbox', { name: 'Callback URL' })
  ).not.toBeInTheDocument()
  expect(
    screen.getByRole('button', { name: 'Connect provider account' })
  ).toBeEnabled()
})
