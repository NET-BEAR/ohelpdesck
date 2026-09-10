import { afterEach, describe, expect, it, vi } from 'vitest';
import { getReadiness } from './api';

afterEach(() => vi.unstubAllGlobals());
describe('readiness client', () => {
  it('requests the health contract through the shared client', async () => {
    const body = { status: 'ready', checks: { postgres: 'ok', redis: 'degraded', object_storage: 'ok' } };
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify(body)));
    vi.stubGlobal('fetch', fetcher);
    await expect(getReadiness()).resolves.toEqual(body);
    expect(fetcher).toHaveBeenCalledWith('/health/ready', expect.objectContaining({ signal: expect.any(AbortSignal) }));
  });
  it('does not disclose server error details', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('secret database URL', { status: 503 })));
    await expect(getReadiness()).rejects.toThrow('API недоступен (HTTP 503)');
  });
  it.each([{ status: 'ready' }, { status: 'ready', checks: { postgres: 3 } }, { status: 'ready', checks: { postgres: ['ok'] } }, { status: 'broken', checks: {} }, null])('rejects invalid contract %j', async (body) => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify(body))));
    await expect(getReadiness()).rejects.toThrow('Некорректный ответ API');
  });
  it.each([
    {},
    { postgres: 'ok' },
    { postgres: 'ok', redis: 'ok' },
    { postgres: 'ok', object_storage: 'ok' },
    { redis: 'ok', object_storage: 'ok' },
    { postgres: 'degraded', redis: 'ok', object_storage: 'ok' },
    { postgres: 'unavailable', redis: 'ok', object_storage: 'ok' },
    { postgres: 'ok', redis: 'error', object_storage: 'ok' },
    { postgres: 'ok', redis: 'unavailable', object_storage: 'ok' },
    { postgres: 'ok', redis: 'ok', object_storage: 'error' },
    { postgres: 'ok', redis: 'ok', object_storage: 'unavailable' },
    { postgres: 'ok', redis: 'ok', object_storage: 'ok', extra: 'ok' },
  ])('rejects checks outside the successful OpenAPI contract %j', async (checks) => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ status: 'ready', checks }))));
    await expect(getReadiness()).rejects.toThrow('Некорректный ответ API');
  });
  it.each(['ok', 'degraded'])('accepts optional object storage state %s', async (state) => {
    const body = { status: 'ready', checks: { postgres: 'ok', redis: 'ok', object_storage: state } };
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify(body))));
    await expect(getReadiness()).resolves.toEqual(body);
  });
  it('normalizes network failures', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('internal network detail')));
    await expect(getReadiness()).rejects.toThrow('Не удалось связаться с API');
  });
  it('normalizes malformed JSON', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('<html>bad gateway</html>')));
    await expect(getReadiness()).rejects.toThrow('Некорректный ответ API');
  });
});
