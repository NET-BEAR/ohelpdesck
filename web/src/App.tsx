import { Component, type ReactNode } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link, Navigate, Route, Routes } from 'react-router-dom';
import { getReadiness } from './api';

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

export function App() {
  return <ErrorBoundary><header><Link to="/health">Платформа поддержки · технический контур</Link></header><main><Routes>
    <Route path="/" element={<Navigate to="/health" replace />} />
    <Route path="/health" element={<HealthPage />} />
    <Route path="*" element={<><h1>Страница не найдена</h1><Link to="/health">Состояние платформы</Link></>} />
  </Routes></main></ErrorBoundary>;
}
