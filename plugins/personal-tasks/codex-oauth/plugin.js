// OAuth endpoint configuration follows decolua/9router (MIT); see THIRD-PARTY-NOTICES.
export const meta = {
  apiVersion: 1, key: 'codex-oauth', name: 'Codex OAuth', version: '1.0.0',
  author: { name: 'Aksara Teknologi', url: 'https://github.com/Aksara-Teknologi/Aksara-AI-Router' },
  description: { en: 'ChatGPT subscription via Codex OAuth. Responses API, streaming and synchronous replies.', zh: '通过 Codex OAuth 使用 ChatGPT 订阅，支持 Responses API 流式与同步响应。' },
  baseUrl: 'https://chatgpt.com', allowedHosts: ['auth.openai.com'],
  models: ['gpt-6.1-sol', 'gpt-6.1-terra', 'gpt-6-astra', 'gpt-6-sol', 'gpt-6-luna', 'gpt-daybreak-blue-latest', 'gpt-reserve', 'codex-auto-review'],
  fetchMode: 'per_task', submitResponseTypes: ['sse'],
  requiredCapabilities: ['oauth2-pkce@1', 'submit-sse-render@1'],
  auth: { type: 'oauth2_pkce', oauth: { provider: 'codex' } },
  routes: [{ method: 'POST', path: '/codex-oauth/v1/responses', type: 'submit', decode: 'create', render: 'result', retainResult: false }],
};

export const native = {
  create(ctx) {
    if (ctx.body.kind !== 'json' || !ctx.body.value || Array.isArray(ctx.body.value)) throw new Error('A JSON Responses request is required');
    const body = utils.json.clone(ctx.body.value);
    if (typeof body.model !== 'string' || !body.model) throw new Error('model is required');
    if (typeof body.input !== 'string' && !Array.isArray(body.input)) throw new Error('input must be text or an array');
    if (body.background === true || body.previous_response_id) throw new Error('Codex OAuth requires stateless input; background and previous_response_id are unsupported');
    if (body.stream !== undefined && typeof body.stream !== 'boolean') throw new Error('stream must be a boolean');
    if (body.max_output_tokens !== undefined && (!Number.isInteger(body.max_output_tokens) || body.max_output_tokens < 0 || body.max_output_tokens > 1073741823)) throw new Error('max_output_tokens is outside the supported range');
    return { kind: 'submit', model: body.model, action: 'responses', requestBody: body };
  },
  result(ctx, task) { return task.data; },
};

export function buildSubmitRequest(ctx) {
  const body = utils.json.clone(ctx.requestBody);
  const streaming = body.stream === true;
  if (!ctx.oauth || !ctx.oauth.accountId) throw new Error('Reconnect the Codex account to obtain its account ID');
  if (typeof body.input === 'string') body.input = [{ role: 'user', content: [{ type: 'input_text', text: body.input }] }];
  body.model = ctx.upstreamModel || ctx.model;
  body.instructions = body.instructions || '';
  if (!body.prompt_cache_key && ctx.agentSessionKey) body.prompt_cache_key = ctx.agentSessionKey;
  body.store = false;
  body.stream = true;
  for (const field of ['max_output_tokens', 'max_tokens', 'temperature', 'top_p', 'frequency_penalty', 'presence_penalty', 'stream_options', 'background', 'service_tier']) delete body[field];
  return {
    url: ctx.baseUrl.replace(/\/$/, '') + '/backend-api/codex/responses', method: 'POST',
    responseType: 'sse', bufferStream: !streaming,
    headers: { Authorization: ctx.authHeader, 'ChatGPT-Account-Id': ctx.oauth.accountId, 'Content-Type': 'application/json', Accept: 'text/event-stream', originator: 'codex_cli_rs' },
    body,
  };
}

export function parseSubmitEvent(ctx, event, previous) {
  if (event.data === '[DONE]') {
    if (!previous || !previous.status) throw new Error('Codex stream ended without a terminal response');
    return { state: previous, done: true, clientEvent: { data: event.data } };
  }
  const body = JSON.parse(event.data);
  const terminal = ['response.completed', 'response.failed', 'response.incomplete'].includes(body.type);
  const state = terminal ? body.response : (body.type === 'error' ? { status: 'failed' } : previous || {});
  return { state, done: terminal || body.type === 'error', clientEvent: { event: event.event === 'message' ? '' : event.event, data: event.data } };
}

export function parseSubmitResponse(ctx, response) {
  const body = response.body;
  if (response.statusCode < 200 || response.statusCode >= 300 || !body || !body.status) throw new Error('Codex returned an invalid response');
  const usage = body.usage || {};
  const success = body.status === 'completed' || body.status === 'incomplete';
  return { taskId: ctx.publicTaskId, taskData: body, immediate: { status: success ? 'SUCCESS' : 'FAILURE', reason: success ? '' : 'Codex generation failed', completionTokens: usage.output_tokens || 0, totalTokens: usage.total_tokens || 0 } };
}
export function buildQueryRequest() { throw new Error('Codex OAuth responses are stateless and cannot be retrieved'); }
export function parseTaskResult() { throw new Error('Codex OAuth completes within the original request'); }
