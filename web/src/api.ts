export interface Readiness {
  status: 'ready';
  checks: {
    postgres: 'ok';
    redis: 'ok' | 'degraded';
    object_storage: 'ok' | 'degraded';
  };
}

export interface CurrentUser {
  id: string;
  login: string;
  email: string;
  name: string;
  role: 'agent' | 'supervisor' | 'administrator';
  status: 'active' | 'disabled';
  permissions: string[];
}

let csrfToken: string | undefined;

function isReadiness(value: unknown): value is Readiness {
  if (typeof value !== 'object' || value === null) return false;
  const body = value as Record<string, unknown>;
  if (body.status !== 'ready' || typeof body.checks !== 'object' || body.checks === null || Array.isArray(body.checks)) return false;
  const checks = body.checks as Record<string, unknown>;
  return Object.keys(checks).length === 3 && checks.postgres === 'ok' &&
    (checks.redis === 'ok' || checks.redis === 'degraded') &&
    (checks.object_storage === 'ok' || checks.object_storage === 'degraded');
}

export async function getReadiness(): Promise<Readiness> {
  let response: Response;
  try {
    response = await fetch('/health/ready', { signal: AbortSignal.timeout(5000), headers: { Accept: 'application/json' } });
  } catch {
    throw new Error('Не удалось связаться с API');
  }
  if (!response.ok) throw new Error(`API недоступен (HTTP ${response.status})`);
  let body: unknown;
  try { body = await response.json(); } catch { throw new Error('Некорректный ответ API'); }
  if (!isReadiness(body)) throw new Error('Некорректный ответ API');
  return body;
}

function isCurrentUser(value: unknown): value is CurrentUser {
  if (typeof value !== 'object' || value === null) return false;
  const user = value as Record<string, unknown>;
  return typeof user.id === 'string' && typeof user.login === 'string' && typeof user.email === 'string' && typeof user.name === 'string' &&
    (user.role === 'agent' || user.role === 'supervisor' || user.role === 'administrator') &&
    (user.status === 'active' || user.status === 'disabled') && Array.isArray(user.permissions) && user.permissions.every(permission => typeof permission === 'string');
}

async function bodyOrError(response: Response): Promise<unknown> {
  try { return await response.json(); } catch { throw new Error('Некорректный ответ API'); }
}

export async function login(loginValue: string, password: string): Promise<void> {
  const response = await fetch('/api/v1/auth/login', {
    method: 'POST', credentials: 'same-origin', signal: AbortSignal.timeout(5000),
    headers: { Accept: 'application/json', 'Content-Type': 'application/json' }, body: JSON.stringify({ login: loginValue, password }),
  });
  const body = await bodyOrError(response) as Record<string, unknown>;
  if (!response.ok || typeof body.csrf_token !== 'string' || body.csrf_token.length < 32) throw new Error('Не удалось выполнить вход');
  csrfToken = body.csrf_token;
}

export async function getMe(): Promise<CurrentUser> {
  const response = await fetch('/api/v1/me', { credentials: 'same-origin', signal: AbortSignal.timeout(5000), headers: { Accept: 'application/json' } });
  const body = await bodyOrError(response);
  if (!response.ok || !isCurrentUser(body)) throw new Error('Сессия недоступна');
  return body;
}

export async function logout(): Promise<void> {
  if (!csrfToken) return;
  const response = await fetch('/api/v1/auth/logout', { method: 'POST', credentials: 'same-origin', signal: AbortSignal.timeout(5000), headers: { 'X-CSRF-Token': csrfToken } });
  csrfToken = undefined;
  if (!response.ok && response.status !== 401) throw new Error('Не удалось завершить сессию');
}

export interface WorkspaceItem { id: string; number: number; channel: { id: string; name: string; type: string; status: string }; contact: { id: string; display_name: string }; assignee: { id: string; name: string } | null; status: string; priority: string; waiting_since: string | null; last_activity_at: string; version: number; }
export interface WorkspacePage { items: WorkspaceItem[]; next_cursor: string | null; }
export interface ConversationDetail {
  id: string;
  version: number;
  assignee: { id: string; name: string } | null;
  capabilities: { can_reply: boolean; can_reassign: boolean };
}
export interface OutboundMessage { id: string; conversation_id: string; channel_id: string; status: string; duplicate: boolean; }
export class WorkspaceActionError extends Error {
  constructor(readonly kind: 'stale_version' | 'idempotency_conflict', message: string) { super(message); }
}
async function workspaceJSON(path: string): Promise<unknown> {
  let response: Response;
  try {
    response = await fetch(path, { credentials: 'same-origin', signal: AbortSignal.timeout(5000), headers: { Accept: 'application/json' } });
  } catch {
    throw new Error('Не удалось связаться с API');
  }
  const body = await bodyOrError(response);
  if (!response.ok) throw new Error(response.status === 401 ? 'Сессия недоступна' : 'Не удалось загрузить обращения');
  return body;
}
export async function getWorkspace(query: Record<string, string> = {}): Promise<WorkspacePage> { const params = new URLSearchParams(Object.entries(query).filter(([, value]) => value)); const body = await workspaceJSON(`/api/v1/conversations${params.size ? `?${params}` : ''}`); if (typeof body !== 'object' || body === null || !Array.isArray((body as Record<string, unknown>).items)) throw new Error('Некорректный ответ API'); return body as WorkspacePage; }
export async function getConversation(id: string): Promise<ConversationDetail> { return workspaceJSON(`/api/v1/conversations/${encodeURIComponent(id)}`) as Promise<ConversationDetail>; }
export async function getConversationMessages(id: string, query: Record<string, string> = {}): Promise<unknown> { const params = new URLSearchParams(Object.entries(query).filter(([, value]) => value)); return workspaceJSON(`/api/v1/conversations/${encodeURIComponent(id)}/messages${params.size ? `?${params}` : ''}`); }

async function workspaceMutation(path: string, method: 'PATCH' | 'POST', body: unknown, headers: Record<string, string> = {}): Promise<unknown> {
  if (!csrfToken) throw new Error('Сессия недоступна');
  let response: Response;
  try {
    response = await fetch(path, { method, credentials: 'same-origin', signal: AbortSignal.timeout(5000), headers: { Accept: 'application/json', 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken, ...headers }, body: JSON.stringify(body) });
  } catch {
    throw new Error('Не удалось связаться с API');
  }
  const result = await bodyOrError(response);
  if (response.ok) return result;
  if (response.status === 401) throw new Error('Сессия недоступна');
  if (response.status === 409) {
    const code = typeof result === 'object' && result !== null && typeof (result as { error?: { code?: unknown } }).error?.code === 'string'
      ? (result as { error: { code: string } }).error.code : '';
    if (code === 'idempotency_conflict') throw new WorkspaceActionError('idempotency_conflict', 'Этот ключ ответа уже использован с другими данными. Создайте новый ответ.');
    throw new WorkspaceActionError('stale_version', 'Данные диалога устарели.');
  }
  throw new Error('Не удалось сохранить изменения.');
}
export async function assignConversation(id: string, assigneeID: string | null, expectedVersion: number): Promise<ConversationDetail> {
  return workspaceMutation(`/api/v1/conversations/${encodeURIComponent(id)}/assignee`, 'PATCH', { assignee_id: assigneeID, expected_version: expectedVersion }) as Promise<ConversationDetail>;
}
export async function queueReply(id: string, text: string, idempotencyKey: string): Promise<OutboundMessage> {
  return workspaceMutation(`/api/v1/conversations/${encodeURIComponent(id)}/messages`, 'POST', { text }, { 'Idempotency-Key': idempotencyKey }) as Promise<OutboundMessage>;
}
