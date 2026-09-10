export interface Readiness {
  status: 'ready';
  checks: Record<string, 'ok' | 'degraded' | 'error'>;
}

function isReadiness(value: unknown): value is Readiness {
  if (typeof value !== 'object' || value === null) return false;
  const body = value as Record<string, unknown>;
  return body.status === 'ready' && typeof body.checks === 'object' && body.checks !== null &&
    !Array.isArray(body.checks) && Object.values(body.checks).every(check => typeof check === 'string' && ['ok', 'degraded', 'error'].includes(check));
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
