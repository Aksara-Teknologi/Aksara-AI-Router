// OAuth endpoint configuration follows decolua/9router (MIT); see THIRD-PARTY-NOTICES.
export const meta = {
  apiVersion: 1, key: 'claude-oauth', name: 'Claude OAuth', version: '1.0.0',
  author: { name: 'Aksara Teknologi', url: 'https://github.com/Aksara-Teknologi/Aksara-AI-Router' },
  description: { en: 'Claude subscription OAuth with the native Messages API, including streaming.', zh: '通过 Claude 订阅 OAuth 使用原生 Messages API，支持流式响应。' },
  baseUrl: 'https://api.anthropic.com', allowedHosts: ['claude.ai'],
  models: ['claude-opus-5-5', 'claude-opus-5', 'claude-fable-5-1', 'claude-fable-5', 'claude-sonnet-5-5', 'claude-sonnet-5', 'claude-haiku-4-5-20251001'],
  fetchMode: 'per_task', submitResponseTypes: ['json', 'sse'],
  requiredCapabilities: ['oauth2-pkce@1'],
  auth: { type: 'oauth2_pkce', oauth: { provider: 'claude' } },
  routes: [{ method: 'POST', path: '/claude-oauth/v1/messages', type: 'submit', decode: 'create', render: 'result', retainResult: false }],
};
export const native = {
  create(ctx) {
    if (ctx.body.kind !== 'json' || !ctx.body.value || Array.isArray(ctx.body.value)) throw new Error('A JSON Messages request is required');
    const body = utils.json.clone(ctx.body.value);
    if (typeof body.model !== 'string' || !body.model || !Array.isArray(body.messages) || !body.messages.length) throw new Error('model and messages are required');
    if (!Number.isInteger(body.max_tokens) || body.max_tokens < 1 || body.max_tokens > 1073741823) throw new Error('max_tokens must be a positive bounded integer');
    if (body.stream !== undefined && typeof body.stream !== 'boolean') throw new Error('stream must be a boolean');
    return { kind: 'submit', model: body.model, action: 'messages', requestBody: body };
  },
  result(ctx, task) { return task.data; },
};
export function buildSubmitRequest(ctx) {
  const body = utils.json.clone(ctx.requestBody);
  if (!body || !Array.isArray(body.messages) || !body.messages.length || !Number.isInteger(body.max_tokens) || body.max_tokens < 1 || body.max_tokens > 1073741823) throw new Error('messages and a bounded positive max_tokens are required');
  if (body.stream !== undefined && typeof body.stream !== 'boolean') throw new Error('stream must be a boolean');
  body.model = ctx.upstreamModel || ctx.model;
  const controlled = Object.prototype.hasOwnProperty.call(body, 'cache_control') ||
    (Array.isArray(body.system) && body.system.some((block) => block && block.cache_control)) ||
    (Array.isArray(body.tools) && body.tools.some((tool) => tool && tool.cache_control)) ||
    body.messages.some((message) => Array.isArray(message.content) && message.content.some((block) => block && block.cache_control));
  if (ctx.autoPromptCache && !controlled) body.cache_control = { type: 'ephemeral' };
  return {
    url: ctx.baseUrl.replace(/\/$/, '') + '/v1/messages?beta=true', method: 'POST',
    responseType: body.stream === true ? 'sse' : 'json',
    headers: { Authorization: ctx.authHeader, 'Content-Type': 'application/json', 'Anthropic-Version': '2023-06-01', 'Anthropic-Beta': 'oauth-2025-04-20,interleaved-thinking-2025-05-14' }, body,
  };
}
export function parseSubmitEvent(ctx, event, previous) {
  const body = JSON.parse(event.data);
  const state = previous ? utils.json.clone(previous) : { type: 'message', usage: {} };
  if (body.type === 'message_start' && body.message) { state.id = body.message.id; state.usage = body.message.usage || {}; }
  if (body.type === 'message_delta' && body.usage) Object.assign(state.usage, body.usage);
  if (body.type === 'error') state.error = true;
  return { state, done: body.type === 'message_stop' || body.type === 'error' };
}
export function parseSubmitResponse(ctx, response) {
  const body = response.body;
  if (response.statusCode < 200 || response.statusCode >= 300 || !body || (!body.id && !body.error)) throw new Error('Claude returned an invalid message');
  const usage = body.usage || {};
  return { taskId: ctx.publicTaskId, taskData: body, immediate: { status: body.error ? 'FAILURE' : 'SUCCESS', reason: body.error ? 'Claude generation failed' : '', completionTokens: usage.output_tokens || 0, totalTokens: (usage.input_tokens || 0) + (usage.output_tokens || 0) + (usage.cache_read_input_tokens || 0) + (usage.cache_creation_input_tokens || 0) } };
}
export function buildQueryRequest() { throw new Error('Claude messages cannot be retrieved after completion'); }
export function parseTaskResult() { throw new Error('Claude OAuth completes within the original request'); }
