# Web foundation — SPEC-000 / 000-12

Статус: ready_for_review.

Область: только `web/`; React + TypeScript + Vite + React Router + TanStack Query. Служебная страница `/health`, корень перенаправляет на неё. Продуктовые экраны не создаются. Источники: SPEC-000 §22–24, F09, PLAN.md и UX_DESIGN.md оркестратора. `docs/agents/roles/developer.md` отсутствовал при начале работы.

## Команды

Node.js 22.22.0 (контейнер `node:22.22.0-bookworm-slim`). Из `web/`: `npm ci`, `npm run typecheck`, `npm run lint`, `npm test`, `npm run build`. Результат сборки — `web/dist/`. `npm run dev -- --host 0.0.0.0` слушает 5173; `npm run preview -- --host 0.0.0.0` — 4173. Preview предназначен для проверки сборки, production static serving задаётся deployment-конфигурацией.

Vite dev proxy `/health` обращается к `http://localhost:8080`; `API_PROXY_TARGET` меняет только dev upstream. Production сервер должен проксировать `/health/ready` к API и возвращать index.html для SPA routes. Секретов и публичного API base URL во frontend нет.

## Обоснование зависимостей

React/React DOM, React Router, TanStack Query требуются SPEC-000; TypeScript/Vite реализуют сборку. ESLint/typescript-eslint проверяют исходники. Vitest с V8 coverage, jsdom и Testing Library проверяют API, состояния запроса, маршруты и error boundary. Версии прямых зависимостей зафиксированы в package.json, транзитивные — package-lock.json. Владелец: frontend/platform.

## Проверки

Все команды выполнены в изолированном worktree `/tmp/ohelpdesck-foundation-web` через `docker run --rm -v "$PWD/web:/app" -w /app node:22.22.0-bookworm-slim …`.

| Проверка | Код | Evidence |
|---|---|---|
| RED `npm test` до production TS | 1 | 2 suites failed: отсутствуют `./api`, `./App` |
| Дополнительный RED `npm test` | 1 | `postgres: ["ok"]` ошибочно принимался клиентом; 1 failed / 12 passed |
| GREEN `npm run typecheck` | 0 | tsc --noEmit |
| GREEN `npm run lint` | 0 | eslint src |
| GREEN `npm test` | 0 | 13 tests, 2 suites; Vitest 4.1.11 |
| GREEN `npm run build` | 0 | Vite 6.4.3; 88 modules; JS gzip 87.26 kB |
| `npm audit --audit-level=moderate` | 0 | found 0 vulnerabilities, включая dev dependencies |
| `npm ci --no-fund --no-audit` с npm 11.6.2 и штатным npm 10.9.4 | 0 | Оба clean install прошли, 261 packages |

V8 coverage включает все production TS/TSX, включая bootstrap `main.tsx`: lines **90.47%**, statements **92.30%**, branches/functions **100%**. App.tsx и api.ts: 100%; bootstrap main.tsx не исполняется unit-тестами. CSS/HTML не являются целью V8. Ранее кода и baseline покрытия не было. Порог 80% принудительно включён в Vitest.

Обнаружены и исправлены: permissive String-coercion состояния dependency; уязвимые первично выбранные версии Router/Vite/Vitest. Итоговые pinned версии: Router 7.18.3, Vite 6.4.3, Vitest/coverage 4.1.11. npm 10.9.4 выдал внутреннюю ошибку `edgesOut` при создании графа Vitest 4; lockfile сформирован npm 11.6.2, установленным только в одноразовом Node-контейнере. Предупреждение о снятой поддержке ESLint 9 остаётся, audit уязвимостей не обнаружил.

Readiness client использует same-origin GET `/health/ready`, `Accept: application/json`, deadline 5 секунд, runtime-проверку JSON. Raw server/network errors пользователю не показываются. Query polling 30 секунд, явный retry, loading/error/success, error boundary, fallback route покрыты тестами. OpenAPI future business DTO не вводятся.

Независимый review, QA и проверка remote browser принадлежат последующим ролям. Эти результаты — developer checks, не подтверждение полного SPEC-000 или развёртывания. Root-оркестратор должен включить этот раздел в task DOCUMENTATION.md; самостоятельная запись вне выделенного ownership не выполняется.

## Исправление после независимого review: readiness contract

Замечание runtime reviewer: generic checks пропускал пустой объект, отсутствующие обязательные зависимости и вне-контрактный `error`. Сверен actual `api/openapi.yaml` основного checkout: три обязательных поля; additionalProperties=false; postgres ok/unavailable; redis/object_storage ok/degraded. Успешный ответ дополнительно требует `status=ready` и `postgres=ok` согласно описанию HTTP 200. Клиент возвращает только успешный тип, non-2xx по-прежнему превращает в безопасную ошибку.

До исправления добавлены regression cases: `npm test` exit 1, **9 failed / 18 passed**. После минимального изменения типа и runtime validator, обновления корректных UI fixtures: `npm run typecheck && npm run lint && npm test && npm run build` exit 0; **27 passed**, 2 suites. V8: **90.90% lines**, **92.85% statements**, **100% branches/functions**, api.ts/App.tsx — 100%. Покрытие не снизилось. Команда выполнена в прежнем Node 22.22.0 контейнере; dependencies не менялись. Production bundle JS gzip 87.28 kB. Требуется повторный независимый review/QA затронутой валидации; локальный GREEN не заменяет этот этап.
