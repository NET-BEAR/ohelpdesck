import { Component, type FormEvent, type ReactNode, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link, Navigate, Route, Routes, useNavigate } from 'react-router-dom';
import { getMe, getReadiness, login, logout } from './api';

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

export function App() {
  return <ErrorBoundary><header><Link to="/health">Платформа поддержки</Link></header><main><Routes>
    <Route path="/" element={<Navigate to="/login" replace />} />
    <Route path="/login" element={<LoginPage />} />
    <Route path="/me" element={<ProfilePage />} />
    <Route path="/health" element={<HealthPage />} />
    <Route path="*" element={<><h1>Страница не найдена</h1><Link to="/health">Состояние платформы</Link></>} />
  </Routes></main></ErrorBoundary>;
}
