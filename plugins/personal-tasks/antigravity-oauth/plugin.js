// OAuth and request envelope follow decolua/9router (MIT); see THIRD-PARTY-NOTICES.
export const meta = {
  apiVersion: 1, key: 'antigravity-oauth', name: 'Antigravity OAuth', version: '1.0.0',
  author: { name: 'Aksara Teknologi', url: 'https://github.com/Aksara-Teknologi/Aksara-AI-Router' },
  description: { en: 'Google Antigravity OAuth with Gemini generateContent and streamGenerateContent.', zh: '通过 Google Antigravity OAuth 使用 Gemini generateContent 和 streamGenerateContent。' },
  baseUrl: 'https://daily-cloudcode-pa.googleapis.com',
  allowedHosts: ['accounts.google.com', 'oauth2.googleapis.com', 'cloudcode-pa.googleapis.com'],
  models: ['gemini-4-argon-high', 'gemini-4-argon-medium', 'gemini-4-argon-low', 'gemini-3.8-flash', 'gemini-3.8-flash-tiered', 'gemini-3.8-flash-high', 'gemini-3.8-flash-medium', 'gemini-3.8-flash-low', 'gemini-3.7-flash-high', 'gemini-3.7-flash-medium', 'gemini-3.7-flash-low', 'gemini-3.6-flash-high', 'gemini-3.6-flash-medium', 'gemini-3.6-flash-low', 'gemini-pro-agent', 'gemini-3.1-pro-high', 'gemini-3.1-pro-low', 'gpt-oss-120b-medium'],
  fetchMode: 'per_task', submitResponseTypes: ['json', 'sse'],
  requiredCapabilities: ['oauth2-pkce@1', 'submit-sse-render@1'],
  auth: { type: 'oauth2_pkce', oauth: { provider: 'antigravity' } },
  routes: [{ method: 'POST', path: '/antigravity-oauth/v1beta/models/:modelAction', type: 'submit', decode: 'create', render: 'result', retainResult: false }],
};
export const native = {
  create(ctx) {
    const match = /^([A-Za-z0-9_.()\-]+):(generateContent|streamGenerateContent)$/.exec(ctx.params.modelAction || '');
    if (!match) throw new Error('Use models/{model}:generateContent or :streamGenerateContent');
    if (ctx.body.kind !== 'json' || !ctx.body.value || Array.isArray(ctx.body.value)) throw new Error('A JSON Gemini request is required');
    const body = utils.json.clone(ctx.body.value);
    if (!Array.isArray(body.contents) || !body.contents.length) throw new Error('contents is required');
    if (body.model !== undefined && body.model !== match[1]) throw new Error('Body model must match the URL model');
    const config = body.generationConfig || {};
    if (config.maxOutputTokens !== undefined && (!Number.isInteger(config.maxOutputTokens) || config.maxOutputTokens < 0 || config.maxOutputTokens > 1073741823)) throw new Error('maxOutputTokens is outside the supported range');
    if (config.candidateCount !== undefined && config.candidateCount !== 1) throw new Error('Antigravity supports one candidate per request');
    delete body.model;
    return { kind: 'submit', model: match[1], action: match[2], requestBody: body };
  },
  result(ctx, task) { return task.data; },
};
export function buildSubmitRequest(ctx) {
  if (!ctx.oauth || !ctx.oauth.projectId) throw new Error('Reconnect Antigravity to obtain a Code Assist project');
  const request = utils.json.clone(ctx.requestBody);
  if (!request || !Array.isArray(request.contents) || !request.contents.length || !['generateContent', 'streamGenerateContent'].includes(ctx.action)) throw new Error('A valid generateContent request is required');
  const config = request.generationConfig || {};
  if (config.maxOutputTokens !== undefined && (!Number.isInteger(config.maxOutputTokens) || config.maxOutputTokens < 0 || config.maxOutputTokens > 1073741823)) throw new Error('maxOutputTokens is outside the supported range');
  if (config.candidateCount !== undefined && config.candidateCount !== 1) throw new Error('Antigravity supports one candidate per request');
  const model = ctx.upstreamModel || ctx.model;
  const aliases = { 'gemini-3.8-flash': 'gemini-3.8-flash-tiered', 'claude-sonnet-5-5': 'claude-sonnet-5-5-high(high)', 'claude-opus-5-5': 'claude-opus-5-5-high(high)' };
  request.sessionId = request.sessionId || (ctx.agentSessionKey ? String(parseInt(ctx.agentSessionKey.slice(0, 12), 16)) : ctx.publicTaskId);
  const streaming = ctx.action === 'streamGenerateContent';
  return {
    url: ctx.baseUrl.replace(/\/$/, '') + '/v1internal:' + (streaming ? 'streamGenerateContent?alt=sse' : 'generateContent'),
    method: 'POST', responseType: streaming ? 'sse' : 'json',
    headers: { Authorization: ctx.authHeader, 'Content-Type': 'application/json', 'User-Agent': 'antigravity/hub/2.24.0 darwin/arm64', 'x-request-source': 'local' },
    body: { project: ctx.oauth.projectId, model: aliases[model] || model, userAgent: 'antigravity', requestId: 'agent/' + ctx.publicTaskId + '/' + utils.unixNow() + '/' + ctx.publicTaskId + '/1', request },
  };
}
export function parseSubmitEvent(ctx, event, previous) {
  const envelope = JSON.parse(event.data);
  const body = envelope.response || envelope;
  if (!body || typeof body !== 'object') throw new Error('Invalid Antigravity stream event');
  const state = { usageMetadata: body.usageMetadata || (previous || {}).usageMetadata || {}, error: !!body.error };
  const done = !!body.error || (Array.isArray(body.candidates) && body.candidates.some((item) => !!item.finishReason));
  return { state, done, clientEvent: { data: JSON.stringify(body) } };
}
export function parseSubmitResponse(ctx, response) {
  const envelope = response.body;
  const body = envelope && (envelope.response || envelope);
  if (response.statusCode < 200 || response.statusCode >= 300 || !body || (!body.candidates && !body.usageMetadata && !body.error)) throw new Error('Antigravity returned an invalid response');
  const usage = body.usageMetadata || {};
  return { taskId: ctx.publicTaskId, taskData: body, immediate: { status: body.error ? 'FAILURE' : 'SUCCESS', reason: body.error ? 'Antigravity generation failed' : '', completionTokens: usage.candidatesTokenCount || 0, totalTokens: usage.totalTokenCount || 0 } };
}
export function buildQueryRequest() { throw new Error('Antigravity results cannot be retrieved after completion'); }
export function parseTaskResult() { throw new Error('Antigravity OAuth completes within the original request'); }
