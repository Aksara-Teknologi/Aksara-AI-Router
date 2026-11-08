import { api } from '@/lib/api'
import { authResult } from '@/lib/secure-verification'

export type PluginOAuthStatus = {
  supported: boolean
  connected: boolean
  stored?: boolean
  plugin_key?: string
  expires_at?: number
  account_label?: string
}

export type PluginOAuthStart = {
  authorization_url: string
  state: string
  redirect_uri: string
  expires_at: number
}

export function getPluginOAuthStatus(channelId: number, signal?: AbortSignal) {
  return authResult<PluginOAuthStatus>(
    api.get(`/api/channel/${channelId}/plugin-oauth`, { signal })
  )
}

export function startPluginOAuth(channelId: number, proof: string) {
  return authResult<PluginOAuthStart>(
    api.post(
      `/api/channel/${channelId}/plugin-oauth/start`,
      {},
      {
        headers: { 'X-Security-Proof': proof },
      }
    )
  )
}

export function finishPluginOAuth(
  state: string,
  callbackUrl: string,
  signal?: AbortSignal
) {
  return authResult<{ channel_id: number }>(
    api.post(
      '/api/channel/plugin-oauth/finish',
      {
        state,
        callback_url: callbackUrl,
      },
      { signal }
    )
  )
}

export function disconnectPluginOAuth(channelId: number, proof: string) {
  return authResult<{ channel_id: number }>(
    api.delete(`/api/channel/${channelId}/plugin-oauth`, {
      headers: { 'X-Security-Proof': proof },
    })
  )
}
