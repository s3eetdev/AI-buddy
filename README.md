# AI-buddy Task Navigator — Python AI-сервис

Интерфейс React + TypeScript расположен в [frontend/](frontend/README.md).
Для запуска из корня репозитория:

```sh
cd frontend
npm ci
npm run dev
```

Интерфейс обращается к Go API. В режиме разработки Vite перенаправляет `/api` на `http://127.0.0.1:8080`; бэкенд нужно запустить отдельно по инструкции ниже.

> Go API, PostgreSQL и worker, включая офлайн replay и подтверждаемую mock Jira,
> описаны в [backend/README.md](backend/README.md). Для запуска полного демо из
> корня используйте `docker compose up --build -d` (файл `.env` не обязателен).


Получает описание Jira-задачи от Go Worker, самостоятельно выбирает разрешённые
код, документацию, контракты и тесты из локального корпуса и возвращает
обоснованный план изучения задачи. LLM — **Qwen 3.8 Flash через Polza.ai**.
Все демонстрационные источники синтетические, это не регламенты Авито.

## Запуск (Python 3.12)

```powershell
python -m venv .venv
.\.venv\Scripts\python.exe -m pip install -r requirements.txt -r requirements-dev.txt
Copy-Item .env.example .env  # Только при первичной настройке, если .env ещё нет!
# В .env заполнить LLM_API_KEY и случайный AI_SERVICE_TOKEN.
.\.venv\Scripts\python.exe -m uvicorn main:app --host 127.0.0.1 --port 8001
```

В рабочей копии автора `.env` уже настроен. В GitHub секретов нет:
после клонирования создайте `.env` по примеру и укажите собственный ключ.
Существующий настроенный `.env` не перезаписывайте.
`AI_SERVICE_TOKEN` — отдельный секрет для Go Worker;
он не равен ключу Polza. `.env` исключён из Git. Конфигурация окружения имеет
приоритет над `.env`. Модель задаётся `LLM_MODEL`, адрес — `LLM_BASE_URL`.
Документация провайдера: https://polza.ai/docs.
Reasoning отключён для предсказуемой выдачи JSON в лимите ответа
([параметры Polza](https://polza.ai/docs/osobennosti/reasoning-tokens.md)).

Swagger: http://127.0.0.1:8001/docs, схема: `/openapi.json`, здоровье: `/health`.
Для POST в Swagger передайте заголовок authorization: `Bearer <AI_SERVICE_TOKEN>`.

## Вызов из Go Worker

```http
POST /v1/analyze
Authorization: Bearer <AI_SERVICE_TOKEN>
Content-Type: application/json

{
  "task_id": "DEMO-1",
  "title": "Уведомить покупателя об отмене заказа продавцом",
  "description": "Найти связанные сервисы и предложить порядок изучения",
  "acceptance_criteria": ["Покупатель получает уведомление"],
  "allowed_scopes": ["demo"],
  "question": ""
}
```

Сервис возвращает `plan`, `sources` со снимками прочитанных материалов,
`jira_markdown`, usage и статус `ready`, `needs_clarification` или
`insufficient_context`. Формат полностью описан в OpenAPI. JSON-пример:
`examples/task.json`. Служебный токен и allowed_scopes задаёт доверенный Worker.
Источник `task:<task_id>` — снимок входной задачи. Его путь `input/task/...`
виртуальный; он позволяет цитировать критерии и сравнивать их с документацией.
Не открывайте этот endpoint напрямую браузеру: пользовательские права проверяет Go.

Go отвечает за очередь/статус ожидания, PostgreSQL и подтверждаемую запись в Jira.
AI возвращает план; в нём нет утверждения, что действие в Jira выполнено.
Установите тайм-аут Worker более 270 секунд. Автоповтор платного анализа
на стороне Worker требует собственного ограничения числа попыток.

## Данные и сценарии

Полный синтетический стенд и показ: [docs/demo.md](docs/demo.md).
В нём 21 источник, 6 задач, тестовые права и локальная имитация Jira с SQLite.

`data/demo_sources.json` — небольшой индекс с source id, scope, path, kind, content.
Исходные синтетические материалы лежат в `fixtures/`; пути из ответа указывают
на эти файлы. Индекс содержит их снимки; обновление: `python scripts/build_corpus.py`.
`CORPUS_PATH` позволяет подключить другой такой индекс; загрузка при старте.
`demo` содержит отмену заказа → OrderCancelled → notification-service,
контракт, текущие тесты и каталог. `restricted` — закрытый контрольный источник.
Проверка недостатка прав: передать `allowed_scopes: []`, получить
`insufficient_context` без вызова LLM. Неоднозначность: попросить изменить
уведомление без указания каналов — ожидаются вопросы, а не выдуманные правила.
Для новых материалов добавьте источники в индекс; отдельный GitLab/Wiki-доступ
в этой версии не требуется. Корпус ограничен размером памяти, это MVP-поиск.

## Проверка

```powershell
.\.venv\Scripts\python.exe -m pytest -q --basetemp=artifacts/tmp-tests --cov=navigator --cov=scripts --cov-report=term-missing --cov-report=xml:artifacts/coverage.xml --cov-fail-under=81
.\.venv\Scripts\python.exe -m ruff check navigator tests scripts main.py
# Реальный вызов LLM, расходует баланс Polza:
.\.venv\Scripts\python.exe scripts/demo.py
```

Тесты не используют внешний API. Реальная демонстрация сохраняет полный результат
в `artifacts/demo-result.json`; ошибки провайдера завершаются с кодом 1.
SDD: `specs/ai-navigator.md`. Верификация цитат проверяет текст и строки,
но не доказывает смысловую правильность ответа — её нужно оценивать отдельно.
Это Python-часть командного решения; UI, Jira-интеграция, презентация и видео
сдаются командой отдельно.

Docker-вариант: `docker compose up --build`. Конфигурация подготовлена,
но Docker в среде разработки отсутствовал; проверен запуск Python и тесты.
Итог локальной проверки: 45 тестов, 96% покрытия вместе с CLI-скриптами;
у модуля navigator покрытие 100%. Подробности в
`artifacts/verification.md`, отчёт `artifacts/coverage.xml`.
