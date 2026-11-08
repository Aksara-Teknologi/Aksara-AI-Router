export const meta = {
  apiVersion: 1,
  key: 'oauth-example',
  name: 'OAuth Task Example',
  version: '1.0.0',
  author: { name: 'OAuth integration example' },
  description: {
    en: 'Example job adapter; configure a registered OAuth client and provider endpoints before use',
    zh: '任务适配器示例；使用前需配置已注册的 OAuth 客户端及提供商端点',
  },
  baseUrl: 'https://provider.example',
  models: ['oauth-example-job'],
  fetchMode: 'per_task',
  requiredCapabilities: ['oauth2-pkce@1'],
  auth: {
    type: 'oauth2_pkce',
    oauth: {
      authorizationUrl: 'https://provider.example/authorize',
      tokenUrl: 'https://provider.example/token',
      clientId: 'configure-registered-client-id',
      scopes: ['inference', 'offline_access'],
    },
  },
};

export function buildSubmitRequest(ctx) {
  return {
    url: ctx.baseUrl.replace(/\/$/, '') + '/jobs',
    method: 'POST',
    headers: { Authorization: ctx.authHeader, 'Content-Type': 'application/json' },
    body: ctx.requestBody,
  };
}

export function parseSubmitResponse(ctx, response) {
  if (response.statusCode < 200 || response.statusCode >= 300 || !response.body || !response.body.id) {
    throw new Error('Provider job submission failed');
  }
  return { taskId: String(response.body.id), taskData: response.body };
}

export function buildQueryRequest(ctx) {
  return {
    url: ctx.baseUrl.replace(/\/$/, '') + '/jobs/' + encodeURIComponent(ctx.taskId),
    method: 'GET',
    headers: { Authorization: ctx.authHeader },
  };
}

export function parseTaskResult(ctx, body) {
  const states = { queued: 'NOT_START', running: 'IN_PROGRESS', succeeded: 'SUCCESS', failed: 'FAILURE' };
  const status = states[body && body.status] || 'UNKNOWN';
  return { status };
}
