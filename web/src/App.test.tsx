import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, expect, it, vi } from 'vitest';
import { App, ErrorBoundary } from './App';
import * as api from './api';

afterEach(() => { cleanup(); vi.restoreAllMocks(); });
function mount(path = '/') {
  return render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><MemoryRouter initialEntries={[path]}><App /></MemoryRouter></QueryClientProvider>);
}
it('shows pending then readiness and dependency states', async () => {
  vi.spyOn(api, 'getReadiness').mockResolvedValue({ status: 'ready', checks: { postgres: 'ok', redis: 'degraded', object_storage: 'ok' } });
  mount('/health');
  expect(screen.getByText('Проверяем доступность API…')).toBeDefined();
  expect(await screen.findByText('API готов к работе')).toBeDefined();
  expect(screen.getByText('degraded')).toBeDefined();
});
it('shows a safe error and supports explicit retry', async () => {
  const request = vi.spyOn(api, 'getReadiness').mockRejectedValueOnce(new Error('API недоступен (HTTP 503)')).mockResolvedValue({ status: 'ready', checks: { postgres: 'ok', redis: 'ok', object_storage: 'ok' } });
  mount('/health');
  expect(await screen.findByRole('alert')).toBeDefined();
  fireEvent.click(screen.getByRole('button', { name: 'Повторить проверку' }));
  expect(await screen.findByText('API готов к работе')).toBeDefined();
  expect(request).toHaveBeenCalledTimes(2);
});
it('renders unknown routes', () => {
  mount('/missing');
  expect(screen.getByText('Страница не найдена')).toBeDefined();
});
it('submits local login and displays a profile after session creation', async () => {
  vi.spyOn(api, 'login').mockResolvedValue();
  vi.spyOn(api, 'getMe').mockResolvedValue({ id: 'id', login: 'sysadmin', email: 'x@example.test', name: 'System Administrator', role: 'administrator', status: 'active', permissions: ['user.manage'] });
  mount('/login');
  fireEvent.change(screen.getByLabelText('Логин'), { target: { value: 'sysadmin' } });
  fireEvent.change(screen.getByLabelText('Пароль'), { target: { value: 'correct horse battery staple' } });
  fireEvent.click(screen.getByRole('button', { name: 'Войти' }));
  expect(await screen.findByText('System Administrator')).toBeDefined();
  expect(screen.getByText('user.manage')).toBeDefined();
});
it('contains rendering errors without exposing their details', () => {
  vi.spyOn(console, 'error').mockImplementation(() => undefined);
  function Broken(): never { throw new Error('secret error'); }
  render(<ErrorBoundary><Broken /></ErrorBoundary>);
  expect(screen.getByRole('alert').textContent).toContain('Не удалось отобразить страницу');
  expect(screen.queryByText('secret error')).toBeNull();
});
