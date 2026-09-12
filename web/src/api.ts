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
async function workspaceJSON(path: string): Promise<unknown> { const response = await fetch(path, { credentials: 'same-origin', signal: AbortSignal.timeout(5000), headers: { Accept: 'application/json' } }); const body = await bodyOrError(response); if (!response.ok) throw new Error(response.status === 401 ? 'Сессия недоступна' : 'Не удалось загрузить обращения'); return body; }
export async function getWorkspace(): Promise<WorkspacePage> { const body = await workspaceJSON('/api/v1/conversations'); if (typeof body !== 'object' || body === null || !Array.isArray((body as Record<string, unknown>).items)) throw new Error('Некорректный ответ API'); return body as WorkspacePage; }
export async function getConversation(id: string): Promise<unknown> { return workspaceJSON(`/api/v1/conversations/${encodeURIComponent(id)}`); }
export async function getConversationMessages(id: string): Promise<unknown> { return workspaceJSON(`/api/v1/conversations/${encodeURIComponent(id)}/messages`); }
