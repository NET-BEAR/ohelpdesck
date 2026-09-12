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
it('shows loading, empty and populated operator workspaces', async () => {
  const request = vi.spyOn(api, 'getWorkspace');
  request.mockReturnValueOnce(new Promise(() => undefined));
  const first = mount('/workspace');
  expect(screen.getByRole('status').textContent).toContain('Загружаем обращения');
  first.unmount();

  request.mockResolvedValueOnce({ items: [], next_cursor: null });
  mount('/workspace');
  expect(await screen.findByText('В доступных каналах обращений нет.')).toBeDefined();
  cleanup();

  request.mockResolvedValueOnce({ items: [{
    id: 'conversation-1', number: 42, channel: { id: 'channel-1', name: 'Email', type: 'email', status: 'active' },
    contact: { id: 'contact-1', display_name: 'Анна' }, assignee: null, status: 'open', priority: 'normal',
    waiting_since: null, last_activity_at: '2026-09-12T10:00:00Z', version: 1,
  }], next_cursor: null });
  mount('/workspace');
  const conversation = await screen.findByRole('link', { name: '#42 · Анна' });
  expect(conversation.getAttribute('href')).toBe('/workspace/conversation-1');
  expect(screen.getByText('Email · open · normal')).toBeDefined();
});
it('retries a failed operator workspace request explicitly', async () => {
  const request = vi.spyOn(api, 'getWorkspace').mockRejectedValueOnce(new Error('Сессия недоступна')).mockResolvedValue({ items: [], next_cursor: null });
  mount('/workspace');
  expect((await screen.findByRole('alert')).textContent).toContain('Сессия недоступна');
  fireEvent.click(screen.getByRole('button', { name: 'Обновить' }));
  expect(await screen.findByText('В доступных каналах обращений нет.')).toBeDefined();
  expect(request).toHaveBeenCalledTimes(2);
});
it('renders the selected conversation and its message timeline', async () => {
  const detail = { id: 'conversation-1', number: 42, status: 'open' };
  const history = { items: [{ id: 'message-1', body: 'Здравствуйте' }], next_cursor: null };
  vi.spyOn(api, 'getConversation').mockResolvedValue(detail);
  vi.spyOn(api, 'getConversationMessages').mockResolvedValue(history);
  mount('/workspace/conversation-1');
  expect(screen.getByRole('status').textContent).toContain('Загружаем диалог');
  expect(await screen.findByRole('heading', { name: 'Диалог' })).toBeDefined();
  expect(screen.getByLabelText('Состояние диалога').textContent).toContain('conversation-1');
  expect(screen.getByLabelText('История сообщений').textContent).toContain('Здравствуйте');
  expect(api.getConversation).toHaveBeenCalledWith('conversation-1');
  expect(api.getConversationMessages).toHaveBeenCalledWith('conversation-1');
});
it('uses a safe conversation error when either workspace query fails', async () => {
  vi.spyOn(api, 'getConversation').mockResolvedValue({ id: 'conversation-1' });
  vi.spyOn(api, 'getConversationMessages').mockRejectedValue(new Error('upstream detail'));
  mount('/workspace/conversation-1');
  expect((await screen.findByRole('alert')).textContent).toContain('Не удалось загрузить диалог.');
  expect(screen.queryByText('upstream detail')).toBeNull();
});
it('contains rendering errors without exposing their details', () => {
  vi.spyOn(console, 'error').mockImplementation(() => undefined);
  function Broken(): never { throw new Error('secret error'); }
  render(<ErrorBoundary><Broken /></ErrorBoundary>);
  expect(screen.getByRole('alert').textContent).toContain('Не удалось отобразить страницу');
  expect(screen.queryByText('secret error')).toBeNull();
});
