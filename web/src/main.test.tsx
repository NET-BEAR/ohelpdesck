import { afterEach, expect, it, vi } from 'vitest';

afterEach(() => {
  vi.doUnmock('react-dom/client');
  vi.resetModules();
  document.body.innerHTML = '';
});

it('mounts the application into the root element', async () => {
  document.body.innerHTML = '<div id="root"></div>';
  const render = vi.fn();
  const createRoot = vi.fn(() => ({ render }));
  vi.doMock('react-dom/client', () => ({ createRoot }));

  await import('./main');

  expect(createRoot).toHaveBeenCalledWith(document.getElementById('root'));
  expect(render).toHaveBeenCalledTimes(1);
});
