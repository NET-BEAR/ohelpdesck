export interface Readiness {
  status: 'ready';
  checks: {
    postgres: 'ok';
    redis: 'ok' | 'degraded';
    object_storage: 'ok' | 'degraded';
  };
}

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
