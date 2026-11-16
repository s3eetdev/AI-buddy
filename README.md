# AI-buddy Task Navigator

Помощник backend-стажёра: получает тестовую Jira-задачу, находит доступные код,
контракты, документацию и тесты, объясняет связи с цитатами и предлагает порядок
изучения. При нехватке требований задаёт вопросы. Пользователь отмечает шаги
и явно подтверждает сохранение плана в имитацию Jira.

Все задачи, пользователи и корпоративные материалы синтетические. Они не являются
данными или регламентами Авито. Jira — локальная имитация, не внешний сервис.

## Состав и зависимости

- `frontend/`: React + TypeScript + Vite; Node.js **22.22.2+ (ветка 22)**,
  **24.15+ (ветка 24)** или **26+**, npm. Требование учитывает jsdom для UI-тестов.
- `backend/`: Go API и асинхронный worker; Go **1.25**, PostgreSQL **16**.
- `navigator/`: Python **3.12**, FastAPI, поиск, LLM и проверка цитат.
- Для основного запуска: Docker Desktop / Engine с Linux-контейнерами и Compose v2.
  Локальные Go и Python нужны только для локального запуска и тестов.
- Версии зависимостей: `frontend/package-lock.json`, `backend/go.mod`, `go.sum`,
  `requirements.txt`, `requirements-dev.txt`. Docker фиксирует Go 1.25.7.

Поток: браузер → Go API → очередь PostgreSQL → worker → Python AI → Polza.ai
(только live). Go сохраняет результат анализа и после подтверждения — mock Jira-план.
Frontend не получает ключ LLM и служебный токен.

## Запуск без ключа модели

```powershell
git clone https://github.com/s3eetdev/AI-buddy.git
cd AI-buddy
docker compose up --build -d
docker compose ps
curl.exe --fail http://127.0.0.1:8080/ready
```

В отдельном терминале из корня проекта:

```powershell
cd frontend
npm ci
npm run dev -- --host 127.0.0.1 --port 5173 --strictPort
```

Откройте **http://127.0.0.1:5173**. Vite проксирует `/api` на Go, порт 8080.
Compose запускает PostgreSQL, Python и Go; frontend запускается отдельно.
Порты 5173, 8080, 8001 и 5433 должны быть свободны.

Без `.env` используется `AI_MODE=replay`: ранее записанные ответы, без вызовов LLM.
Существующий `.env` может переопределять режим. Проверить работающий контейнер:

```powershell
docker compose exec -T backend printenv AI_MODE
```

## Настоящая LLM

Провайдер **Polza.ai**, модель **qwen/qwen3.8-flash**, адрес
**https://polza.ai/api/v1**. Если `.env` ещё нет, скопируйте `.env.example`:

```powershell
Copy-Item .env.example .env  # только если .env ещё нет; настроенный файл не перезаписывайте
```

Укажите в `.env`:

```dotenv
AI_MODE=live
LLM_API_KEY=<собственный ключ Polza.ai>
AI_SERVICE_TOKEN=<случайный непустой служебный секрет>
LLM_BASE_URL=https://polza.ai/api/v1
LLM_MODEL=qwen/qwen3.8-flash
```

`AI_SERVICE_TOKEN` — секрет связи Go и Python, отдельный от ключа провайдера.
Compose передаёт его обоим сервисам, а `LLM_API_KEY` — только Python. Live передаёт
задачу и выбранные разрешённые материалы провайдеру и расходует баланс.
Используйте собственный ключ; `.env` исключён из Git. Не помещайте секреты в `VITE_`.

```powershell
docker compose up -d ai backend
docker compose exec -T backend printenv AI_MODE
curl.exe --fail http://127.0.0.1:8080/ready
```

Запустите **новый анализ**: старый результат не пересчитывается. В live уточнение
отправляется модели; replay отклоняет новые уточнения с `replay_input_not_recorded`.
Чтобы вернуться к офлайн-показу, поставьте `AI_MODE=replay` и повторите команду `up`.

## Тестовые пользователи и проверка

Пароль не нужен: `AUTH_MODE=demo` использует один серверный профиль `DEMO_PROFILE`.
Это имитация авторизации, не production-аутентификация.

| Профиль | Доступные scopes |
|---|---|
| `intern` (по умолчанию) | demo |
| `newcomer` | нет |
| `reviewer` | demo, restricted, legacy, adversarial |

Для смены измените `DEMO_PROFILE` в `.env` и выполните `docker compose up -d backend`.
Профиль нельзя выбирать заголовком браузера; записи другого профиля недоступны.

Основной путь: **DEMO-1 → анализ → цитаты → галочки → «Сохранить в Jira» → ссылка**.
Ссылка открывает сохранённый mock Jira-план. Повтор публикации той же версии не
создаёт второй план. Сохранение окончательного плана разрешено для `ready`.

Адрес страницы содержит `analysis_id`: после обновления браузер загружает этот
анализ и продолжает опрос, если он ещё выполняется. Готовый план, отметки шагов
и публикация восстанавливаются с сервера без нового запуска модели. Кнопка
«Новая задача» убирает ID из адреса. При недоступном анализе или ошибке сети
интерфейс показывает сообщение и позволяет повторить чтение. В адрес не
помещаются тексты источников и секреты.

После технического `failed` кнопка «Запустить новый разбор» создаёт новый анализ;
при неопределённом сетевом сбое повтор того же запроса использует прежний ключ.
Ссылки на источники в сообщениях открывают снимок версии, к которой относится
сообщение, даже после уточнения результата.

Режим указан рядом с результатом: «Записанный пример» использует ранее записанный
ответ, а «Live-анализ» — результат live-режима. Данные модели и расхода в записи
относятся к исходному ответу и не являются измерением текущего replay-запуска.

Полная ручная приёмка: **[docs/testing.md](docs/testing.md)**.
Данные: `data/demo_tasks.json`, `data/demo_sources.json`, `fixtures/`.
CLI и оценивание Python: [docs/demo.md](docs/demo.md).

## Автоматические проверки

GitHub Actions запускает проверки для каждого pull request и push в `main`:
frontend (`npm ci`, тесты, сборка и lint), Python 3.12 (pytest и Ruff) и Go
(обычные тесты, тесты с race detector и vet). Go использует PostgreSQL 16 и
`TEST_DATABASE_URL`, поэтому интеграционные SQL-тесты выполняются с настоящей БД.
Для проверок не нужны Jira, ключи LLM или платные запросы.

Полный воспроизводимый прогон покрытия на Windows (Python 3.12 с зависимостями,
Node.js и Docker; локальный Go не нужен):

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File tools/verify-coverage.ps1
```

Сначала создайте `.venv` и установите Python-зависимости командами ниже. Перед
прогоном остановите Vite: `npm ci` на Windows не может заменить загруженный native-модуль.
Скрипт запускает Python-тесты, npm ci, API/UI-тесты и сборку frontend, затем Go-тесты
с PostgreSQL в Docker и `go vet`. Платных вызовов LLM нет. Каждый SQL-тест использует
свою временную схему. Существующие данные приложения не удаляются.

Общий отчёт: [artifacts/coverage/summary.json](artifacts/coverage/summary.json).
Порог — **81% для каждого компонента**, включая Go entrypoint, React-компоненты
и Python CLI. При недостающем исходном файле, пропуске SQL-теста или недостижении
порога скрипт завершается ошибкой. Методика и результаты:
[artifacts/verification.md](artifacts/verification.md).

Frontend, из корня:

```powershell
cd frontend
npm ci
npm test
npm run build
npm run lint
npm run test:coverage
cd ..
```

Python 3.12, из корня; тесты не вызывают платный API:

```powershell
python -m venv .venv
.\.venv\Scripts\python.exe -m pip install -r requirements.txt -r requirements-dev.txt
.\.venv\Scripts\python.exe -m pytest tests -q -p no:cacheprovider --basetemp=artifacts/tmp-tests --cov=navigator --cov=scripts --cov-report=term-missing --cov-report=xml:artifacts/coverage.xml --cov-fail-under=81
.\.venv\Scripts\python.exe -m ruff check navigator tests scripts main.py
```

Go 1.25, PostgreSQL из Compose со стандартными параметрами:

```powershell
docker compose up -d postgres
cd backend
$env:TEST_DATABASE_URL = 'postgres://navigator:navigator@127.0.0.1:5433/navigator?sslmode=disable'
go test ./... -coverpkg=./... -coverprofile=coverage.out
go tool cover -func=coverage.out
go vet ./...
cd ..
```

SQL-тесты создают и удаляют отдельные схемы; нужны соответствующие права.
Без `TEST_DATABASE_URL` отдельная команда `go test` пропускает SQL-проверки;
полный скрипт выше задаёт подключение и проверяет отсутствие пропусков.
При других параметрах БД скорректируйте URL. Vitest включает все `src/**/*.ts`
и `src/**/*.tsx`, кроме деклараций типов; неисполненный entrypoint остаётся в знаменателе.
Результаты и ограничения: [artifacts/verification.md](artifacts/verification.md).

## Диагностика

```powershell
docker compose ps
docker compose logs --tail 100 backend ai
curl.exe --fail http://127.0.0.1:8080/ready
```

- Нет подключения в UI: проверьте Go на 8080 и терминал Vite. Другой адрес Go
  задаётся через `BACKEND_PROXY_TARGET`: [frontend/README.md](frontend/README.md).
- `failed` / `ai_invalid_response`: ответ не прошёл проверку формата или доказательств.
  Новый анализ может дать другой ответ; чтение старого не запускает модель заново.
- Live требует ключ, баланс и доступ к Polza.ai. Тайм-аут одного вызова — 90 секунд,
  анализ может включать несколько вызовов. Worker может повторить запрос после сбоя:
  exactly-once вызов и расход не гарантированы.
- Буквальная цитата не доказывает смысловую правильность вывода: проверяйте вручную.
- Корпус синтетический и локальный; реальных Jira/GitLab/Wiki-коннекторов нет.
- `docker compose down` сохраняет том БД; **`down -v` удаляет сохранённые данные**.

## Контракты и материалы сдачи

- [SDD поведения AI](specs/ai-navigator.md).
- [Go API](specs/backend-openapi.yaml), [Python OpenAPI](specs/openapi.json).
- [Go и интеграции](backend/README.md), [frontend](frontend/README.md).
- [Ручная приёмка](docs/testing.md), [отчёт проверки](artifacts/verification.md).

Продуктовая презентация PDF и видеодемонстрация в репозиторий пока не добавлены.
Для окончательной сдачи приложите их или укажите реальные ссылки на материалы.
