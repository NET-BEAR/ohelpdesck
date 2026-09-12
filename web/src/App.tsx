import { Component, type FormEvent, type ReactNode, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link, Navigate, Route, Routes, useNavigate, useParams } from 'react-router-dom';
import { assignConversation, getConversation, getConversationMessages, getMe, getReadiness, getWorkspace, login, logout, queueReply } from './api';

export class ErrorBoundary extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() { return { failed: true }; }
  render() {
    return this.state.failed
      ? <main role="alert"><h1>Не удалось отобразить страницу</h1><a href="/">Перезагрузить приложение</a></main>
      : this.props.children;
  }
}

function HealthPage() {
  const health = useQuery({ queryKey: ['health', 'ready'], queryFn: getReadiness, retry: false, refetchInterval: 30_000 });
  return <section aria-labelledby="health-title">
    <h1 id="health-title">Состояние платформы</h1>
    <p>Служебная страница технического контура. Проверка обновляется каждые 30 секунд.</p>
    {health.isPending && <p role="status">Проверяем доступность API…</p>}
    {health.isError && <p role="alert">{health.error.message}</p>}
    {health.isSuccess && <><h2>API готов к работе</h2><dl>{Object.entries(health.data.checks).map(([name, status]) => <div key={name}><dt>{name}</dt><dd>{status}</dd></div>)}</dl></>}
    <button onClick={() => void health.refetch()} disabled={health.isFetching}>Повторить проверку</button>
  </section>;
}

function LoginPage() {
  const navigate = useNavigate();
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string>();
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const loginValue = String(form.get('login') ?? '');
    const password = String(form.get('password') ?? '');
    setPending(true); setError(undefined);
    try { await login(loginValue, password); navigate('/me', { replace: true }); } catch { setError('Проверьте логин и пароль.'); } finally { setPending(false); }
  }
  return <section aria-labelledby="login-title"><h1 id="login-title">Вход в платформу поддержки</h1>
    <form onSubmit={(event) => void submit(event)}><label>Логин<input name="login" autoComplete="username" required maxLength={255} /></label>
      <label>Пароль<input name="password" type="password" autoComplete="current-password" required /></label>
      {error && <p role="alert">{error}</p>}<button type="submit" disabled={pending}>{pending ? 'Выполняем вход…' : 'Войти'}</button>
    </form><p><Link to="/health">Состояние технического контура</Link></p></section>;
}

function ProfilePage() {
  const navigate = useNavigate();
  const profile = useQuery({ queryKey: ['auth', 'me'], queryFn: getMe, retry: false });
  if (profile.isError) return <Navigate to="/login" replace />;
  if (profile.isPending) return <p role="status">Проверяем сессию…</p>;
  const user = profile.data;
  async function signOut() { await logout(); navigate('/login', { replace: true }); }
  return <section aria-labelledby="profile-title"><h1 id="profile-title">{user.name}</h1><p>{user.login} · {user.role}</p>
    <h2>Доступы</h2><ul>{user.permissions.map(permission => <li key={permission}>{permission}</li>)}</ul>
    <button onClick={() => void signOut()}>Выйти</button></section>;
}


function WorkspacePage() {
  const queue = useQuery({ queryKey: ['workspace'], queryFn: getWorkspace, retry: false, refetchInterval: 15_000 });
  if (queue.isPending) return <p role="status">Загружаем обращения…</p>;
  if (queue.isError) return <section><p role="alert">{queue.error.message}</p><button onClick={() => void queue.refetch()} disabled={queue.isFetching}>Обновить</button></section>;
  if (queue.data.items.length === 0) return <section><h1>Обращения</h1><p>В доступных каналах обращений нет.</p></section>;
  return <section aria-labelledby="workspace-title"><h1 id="workspace-title">Обращения</h1><button onClick={() => void queue.refetch()} disabled={queue.isFetching}>Обновить</button><ul>{queue.data.items.map(item => <li key={item.id}><Link to={`/workspace/${item.id}`}>#{item.number} · {item.contact.display_name}</Link><p>{item.channel.name} · {item.status} · {item.priority}</p></li>)}</ul></section>;
}
function ConversationPage() {
  const { id = '' } = useParams<{ id: string }>();
  const detail = useQuery({ queryKey: ['conversation', id], queryFn: () => getConversation(id), retry: false, refetchInterval: 15_000 });
  const messages = useQuery({ queryKey: ['conversation', id, 'messages'], queryFn: () => getConversationMessages(id), retry: false, refetchInterval: 15_000 });
  const currentUser = useQuery({ queryKey: ['auth', 'me'], queryFn: getMe, retry: false, enabled: detail.data?.capabilities.can_reassign === true });
  const [replyText, setReplyText] = useState('');
  const [replyKey, setReplyKey] = useState<string>();
  const [actionError, setActionError] = useState<string>();
  const [pending, setPending] = useState(false);
  if (detail.isPending || messages.isPending) return <p role="status">Загружаем диалог…</p>;
  if (detail.isError || messages.isError) return <p role="alert">Не удалось загрузить диалог.</p>;
  if (!detail.data) return <p role="alert">Не удалось загрузить диалог.</p>;
  const conversation = detail.data;
  async function refresh() { await Promise.all([detail.refetch(), messages.refetch()]); }
  async function submitReply(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const text = replyText.trim();
    if (!text) return;
    const key = replyKey ?? crypto.randomUUID();
    setPending(true); setActionError(undefined);
    try { await queueReply(id, text, key); setReplyText(''); setReplyKey(undefined); await refresh(); } catch (error) { setReplyKey(key); setActionError(error instanceof Error ? error.message : 'Не удалось сохранить изменения.'); } finally { setPending(false); }
  }
  async function changeAssignee(assigneeID: string | null) {
    setPending(true); setActionError(undefined);
    try { await assignConversation(id, assigneeID, conversation.version); await refresh(); } catch (error) { setActionError(error instanceof Error ? error.message : 'Не удалось сохранить изменения.'); if (error instanceof Error && error.message === 'Данные диалога устарели.') await refresh(); } finally { setPending(false); }
  }
  const canReply = conversation.capabilities.can_reply;
  const canReassign = conversation.capabilities.can_reassign;
  return <section><p><Link to="/workspace">К обращениям</Link></p><h1>Диалог</h1>
    {actionError && <p role="alert">{actionError}</p>}
    {canReassign && <p><button type="button" disabled={pending || currentUser.isPending || currentUser.isError} onClick={() => void changeAssignee(currentUser.data?.id ?? null)}>Взять на себя</button><button type="button" disabled={pending || conversation.assignee === null} onClick={() => void changeAssignee(null)}>Снять назначение</button></p>}
    {canReply && <form onSubmit={(event) => void submitReply(event)}><label>Ответ<textarea value={replyText} onChange={(event) => setReplyText(event.target.value)} required maxLength={10_000} /></label><button type="submit" disabled={pending}>{pending ? 'Сохраняем ответ…' : 'Отправить в очередь'}</button></form>}
    <pre aria-label="Состояние диалога">{JSON.stringify(conversation, null, 2)}</pre><h2>Сообщения</h2><pre aria-label="История сообщений">{JSON.stringify(messages.data, null, 2)}</pre></section>;
}

export function App() {
  return <ErrorBoundary><header><Link to="/health">Платформа поддержки</Link></header><main><Routes>
    <Route path="/" element={<Navigate to="/login" replace />} />
    <Route path="/login" element={<LoginPage />} />
    <Route path="/me" element={<ProfilePage />} />
    <Route path="/workspace" element={<WorkspacePage />} />
    <Route path="/workspace/:id" element={<ConversationPage />} />
    <Route path="/health" element={<HealthPage />} />
    <Route path="*" element={<><h1>Страница не найдена</h1><Link to="/health">Состояние платформы</Link></>} />
  </Routes></main></ErrorBoundary>;
}
