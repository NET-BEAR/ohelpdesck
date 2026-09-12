import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
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
it('keeps queue filters and pagination accessible', async () => {
  vi.spyOn(api, 'getWorkspace').mockResolvedValue({ items: [], next_cursor: 'next-page' });
  mount('/workspace?status=open');
  const filter = await screen.findByLabelText('Фильтр статуса');
  fireEvent.change(filter, { target: { value: 'pending' } });
  fireEvent.click(await screen.findByLabelText('Следующая страница'));
  expect(await screen.findByLabelText('Предыдущая страница')).toBeDefined();
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
  const detail = { id: 'conversation-1', version: 1, assignee: null, capabilities: { can_reply: false, can_reassign: false } };
  const history = { items: [{ id: 'message-1', direction: 'outgoing', status: 'failed', content: { text: 'Здравствуйте' }, failed: { code: 'provider_rejected' } }, { id: 'message-2', direction: 'incoming', status: 'queued' }, { id: 'message-3', direction: 'outgoing', status: 'sent' }, { id: 'message-4', direction: 'outgoing', status: 'delivered' }, { id: 'message-5', direction: 'outgoing', status: 'read' }, { id: 'message-6', direction: 'outgoing', status: 'other' }], next_cursor: null };
  vi.spyOn(api, 'getConversation').mockResolvedValue(detail);
  vi.spyOn(api, 'getConversationMessages').mockResolvedValue(history);
  mount('/workspace/conversation-1');
  expect(screen.getByRole('status').textContent).toContain('Загружаем диалог');
  expect(await screen.findByRole('heading', { name: 'Диалог' })).toBeDefined();
  expect(screen.getByLabelText('Состояние диалога').textContent).toContain('conversation-1');
  expect(screen.getByLabelText('История сообщений').textContent).toContain('Здравствуйте');
  expect(screen.getByLabelText('История сообщений').textContent).toContain('Не отправлено');
  expect(screen.getByText('Ошибка доставки: отклонено провайдером')).toBeDefined();
  for (const label of ['В очереди', 'Отправлено', 'Доставлено', 'Прочитано', 'Статус неизвестен']) expect(screen.getByLabelText('История сообщений').textContent).toContain(label);
  expect(api.getConversation).toHaveBeenCalledWith('conversation-1');
  expect(api.getConversationMessages).toHaveBeenCalledWith('conversation-1');
});
it('uses a safe conversation error when either workspace query fails', async () => {
  vi.spyOn(api, 'getConversation').mockResolvedValue({ id: 'conversation-1', version: 1, assignee: null, capabilities: { can_reply: false, can_reassign: false } });
  vi.spyOn(api, 'getConversationMessages').mockRejectedValue(new Error('upstream detail'));
  mount('/workspace/conversation-1');
  expect((await screen.findByRole('alert')).textContent).toContain('Не удалось загрузить диалог.');
  expect(screen.queryByText('upstream detail')).toBeNull();
});
it('queues a reply with an idempotency key and refreshes the conversation', async () => {
  const detail = { id: 'conversation-1', version: 3, assignee: null, capabilities: { can_reply: true, can_reassign: false } };
  const read = vi.spyOn(api, 'getConversation').mockResolvedValue(detail);
  vi.spyOn(api, 'getConversationMessages').mockResolvedValue({ items: [] });
  const queue = vi.spyOn(api, 'queueReply').mockResolvedValue({ id: 'message-1', conversation_id: detail.id, channel_id: 'channel-1', status: 'queued', duplicate: false });
  mount('/workspace/conversation-1');
  await screen.findByRole('textbox', { name: 'Ответ' });
  fireEvent.change(screen.getByRole('textbox', { name: 'Ответ' }), { target: { value: 'Проверяем обращение' } });
  fireEvent.click(screen.getByRole('button', { name: 'Отправить в очередь' }));
  await screen.findByRole('button', { name: 'Отправить в очередь' });
  expect(queue).toHaveBeenCalledWith('conversation-1', 'Проверяем обращение', expect.any(String));
  expect(read).toHaveBeenCalledTimes(2);
});
it('re-reads the conversation after an assignment version conflict', async () => {
  const detail = { id: 'conversation-1', version: 3, assignee: { id: 'agent-2', name: 'Коллега' }, capabilities: { can_reply: false, can_reassign: true } };
  const read = vi.spyOn(api, 'getConversation').mockResolvedValue(detail);
  vi.spyOn(api, 'getConversationMessages').mockResolvedValue({ items: [] });
  vi.spyOn(api, 'getMe').mockResolvedValue({ id: 'agent-1', login: 'agent', email: 'agent@example.test', name: 'Оператор', role: 'agent', status: 'active', permissions: [] });
  const assign = vi.spyOn(api, 'assignConversation').mockRejectedValue(new api.WorkspaceActionError('stale_version', 'Данные диалога устарели.'));
  mount('/workspace/conversation-1');
  const take = await screen.findByRole('button', { name: 'Взять на себя' });
  await waitFor(() => expect((take as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(take);
  expect((await screen.findByRole('alert')).textContent).toContain('Данные диалога устарели.');
  expect(assign).toHaveBeenCalledWith('conversation-1', 'agent-1', 3);
  expect(read).toHaveBeenCalledTimes(2);
});
it('assigns the conversation to the current operator with its version', async () => {
  const detail = { id: 'conversation-1', version: 4, assignee: null, capabilities: { can_reply: false, can_reassign: true } };
  vi.spyOn(api, 'getConversation').mockResolvedValue(detail);
  vi.spyOn(api, 'getConversationMessages').mockResolvedValue({ items: [] });
  vi.spyOn(api, 'getMe').mockResolvedValue({ id: 'agent-1', login: 'agent', email: 'agent@example.test', name: 'Оператор', role: 'agent', status: 'active', permissions: [] });
  const assign = vi.spyOn(api, 'assignConversation').mockResolvedValue(detail);
  mount('/workspace/conversation-1');
  const take = await screen.findByRole('button', { name: 'Взять на себя' });
  await waitFor(() => expect((take as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(take);
  await waitFor(() => expect(assign).toHaveBeenCalledWith('conversation-1', 'agent-1', 4));
});
it('shows idempotency conflict without treating it as a stale assignment', async () => {
  const detail = { id: 'conversation-1', version: 3, assignee: null, capabilities: { can_reply: true, can_reassign: false } };
  const read = vi.spyOn(api, 'getConversation').mockResolvedValue(detail);
  vi.spyOn(api, 'getConversationMessages').mockResolvedValue({ items: [] });
  vi.spyOn(api, 'queueReply').mockRejectedValue(new api.WorkspaceActionError('idempotency_conflict', 'Этот ключ ответа уже использован с другими данными. Создайте новый ответ.'));
  mount('/workspace/conversation-1');
  const text = await screen.findByRole('textbox', { name: 'Ответ' });
  fireEvent.change(text, { target: { value: 'Повтор' } });
  fireEvent.click(screen.getByRole('button', { name: 'Отправить в очередь' }));
  expect((await screen.findByRole('alert')).textContent).toContain('ключ ответа');
  expect(read).toHaveBeenCalledTimes(1);
});
it('contains rendering errors without exposing their details', () => {
  vi.spyOn(console, 'error').mockImplementation(() => undefined);
  function Broken(): never { throw new Error('secret error'); }
  render(<ErrorBoundary><Broken /></ErrorBoundary>);
  expect(screen.getByRole('alert').textContent).toContain('Не удалось отобразить страницу');
  expect(screen.queryByText('secret error')).toBeNull();
});
