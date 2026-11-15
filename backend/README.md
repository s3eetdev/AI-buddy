# Go API и worker

Go API и worker запускаются одним процессом; PostgreSQL хранит задания, версии,
прогресс и подтверждённые публикации. API описан в `../specs/backend-openapi.yaml`;
`../specs/openapi.json` описывает отдельный Python AI-сервис.

## Офлайн-демо через Docker Compose

Из корня репозитория:

```powershell
docker compose up --build -d
docker compose ps
curl.exe http://127.0.0.1:8080/ready
```

`.env` для replay не обязателен. Если нужен другой порт/профиль, создайте `.env`
из `.env.example` только если его ещё нет, и измените параметры. Существующий
`.env` не перезаписывайте. Go API и PostgreSQL публикуются только на loopback;
по умолчанию это порты 8080 и 5433. В контейнере Go подключается к
`postgres://navigator:navigator@postgres:5432/navigator?sslmode=disable`, а с хоста
адрес `postgres://navigator:navigator@127.0.0.1:5433/navigator?sslmode=disable`.
Если меняете имя пользователя, БД или пароль, согласуйте `DATABASE_URL` с
`POSTGRES_*`. Миграция встроена в бинарник и применяется при старте до worker.
Для остановки используйте `docker compose down`: именованный том PostgreSQL
сохраняется; `down -v` удаляет сохранённые данные.

## Пример прохождения (PowerShell и curl.exe)

Указан профиль `intern`, режим `replay` и стандартный `JIRA_BASE_URL`. Создание
асинхронно. Используйте новые ключи идемпотентности для новых операций.

```powershell
$base = 'http://127.0.0.1:8080'
$bodyFile = [System.IO.Path]::GetTempFileName()
[System.IO.File]::WriteAllText($bodyFile, '{"jira_url":"https://demo-jira.local/browse/DEMO-1"}', [System.Text.UTF8Encoding]::new($false))
$created = curl.exe -sS -X POST "$base/api/v1/analyses" -H 'Content-Type: application/json' -H 'Idempotency-Key: example-create-demo-1' --data-binary "@$bodyFile" | ConvertFrom-Json
$id = $created.analysis_id
# Повторяйте GET до готовности; failed означает техническую ошибку.
do {
  Start-Sleep -Seconds 2
  $analysis = curl.exe -sS "$base/api/v1/analyses/$id" | ConvertFrom-Json
} while ($analysis.status -in @('queued', 'collecting_context'))
$analysis | ConvertTo-Json -Depth 30
# Продолжайте после проверки status=ready и анализа содержимого.
$revision = $analysis.result_revision
$sourceId = [uri]::EscapeDataString($analysis.result.sources[0].id)
curl.exe -sS "$base/api/v1/analyses/$id/sources/${sourceId}?result_revision=$revision"
[System.IO.File]::WriteAllText($bodyFile, (@{result_revision=$revision; completed=$true} | ConvertTo-Json -Compress), [System.Text.UTF8Encoding]::new($false))
curl.exe -sS -X PATCH "$base/api/v1/analyses/$id/steps/v${revision}-s1" -H 'Content-Type: application/json' --data-binary "@$bodyFile"
[System.IO.File]::WriteAllText($bodyFile, (@{result_revision=$revision; confirmed=$true} | ConvertTo-Json -Compress), [System.Text.UTF8Encoding]::new($false))
$publication = curl.exe -sS -X POST "$base/api/v1/analyses/$id/publish" -H 'Content-Type: application/json' -H 'Idempotency-Key: example-publish-demo-1-v1' --data-binary "@$bodyFile" | ConvertFrom-Json
curl.exe -sS "$base$($publication.url)"
Remove-Item -LiteralPath $bodyFile
```

Повтор публикации одной и той же версии не должен создать второй план. Для
следующего прогона используйте новый ключ создания и ключ публикации. Результаты
и опубликованные mock Jira-планы остаются в PostgreSQL после перезапуска.

## Запуск Go локально

Нужны Go 1.25 и PostgreSQL 16. Например, запустите только базу из корня:

```powershell
docker compose up -d postgres
Set-Location backend
$env:DATABASE_URL = 'postgres://navigator:navigator@127.0.0.1:5433/navigator?sslmode=disable'
go run ./cmd/server
```

Пути seed, корпуса и записей по умолчанию рассчитаны на рабочую директорию
`backend`. Миграции применяются автоматически при старте; отдельный инструмент
миграций не нужен. Конфигурация через переменные окружения: `HTTP_ADDR`,
`DATABASE_URL`, `AUTH_MODE`, `DEMO_PROFILE`, `AI_MODE`, `AI_BASE_URL`,
`AI_SERVICE_TOKEN`, `AI_REQUEST_TIMEOUT`, `WORKER_CONCURRENCY`, `JOB_LEASE`,
`JOB_HEARTBEAT`, `JOB_MAX_ATTEMPTS`, `JIRA_BASE_URL`, `SEED_PATH`, `CORPUS_PATH`,
`REPLAY_DIR` и `CORS_ORIGINS` (точные origins через запятую). Пример значений —
`../.env.example`. Docker Compose подставляет контейнерные пути и `AI_BASE_URL`.

## Live и ограничения демо

`AUTH_MODE=demo` выбирает один серверный профиль `intern`, `newcomer` или
`reviewer`; это не аутентификация для корпоративного использования. Профиль
нельзя выбирать заголовком браузера. Изменение профиля требует перезапуска и
ограничивает чтение чужих записей. Mock Jira использует синтетические seed-задачи;
адрес задачи только разбирается, HTTP-запрос на него не отправляется.
Публикация выполняется только отдельным запросом с `confirmed: true`.

`AI_MODE=replay` использует ранее записанные ответы, а не запускает модель.
`model` и `usage` относятся к записи, не к текущему показу. Новый ответ на
уточнение нельзя воспроизвести старой записью. При несовместимых правах или
входе replay не раскрывает недоступные источники. В `AI_MODE=live` задайте один
и тот же непустой `AI_SERVICE_TOKEN` для Go и Python и `LLM_API_KEY` только для
Python. Это может расходовать платные вызовы; обычные тесты и replay их не делают.
Ответ Python может повторно запрашиваться после сбоя worker: exactly-once вызов
модели не гарантируется, но устаревшая lease не может записать результат.

Снимок корпуса фиксируется digest при сохранении результата. Если файл корпуса
Go изменился, чтение старой версии вернёт `corpus_mismatch`; отозванный scope
даёт `source_access_denied`. В live Python не передаёт digest своего корпуса в
действующем контракте: несовпадение возвращённых источников обнаруживается, но
разницу в неиспользованных источниках выявить по ответу невозможно. Поэтому оба
сервиса должны использовать один и тот же файл корпуса.

Mock-публикация синхронна: план и квитанция успеха сохраняются атомарно. Если
запись плана завершается ошибкой, при доступной БД сохраняется `publish_failed`
с безопасной ошибкой; публикацию можно повторить с тем же ключом без дубля.
При полном отказе соединения с БД сохранить квитанцию невозможно.

## Проверки

```powershell
Set-Location backend
$env:TEST_DATABASE_URL = 'postgres://navigator:navigator@127.0.0.1:5433/navigator?sslmode=disable'
go test ./...
go test -race ./...
go vet ./...
Set-Location ..
.\.venv\Scripts\python.exe -m pytest -q --basetemp=artifacts/tmp-tests --cov=navigator --cov=scripts --cov-report=term-missing --cov-report=xml:artifacts/coverage.xml --cov-fail-under=81
.\.venv\Scripts\python.exe -m ruff check navigator tests scripts main.py
```

Интеграционные тесты PostgreSQL требуют `TEST_DATABASE_URL` с правом создавать
схемы; каждый тест создаёт и удаляет отдельную схему. Без этой переменной SQL-
сценарии пропускаются. Платный `scripts/demo.py` и live smoke не запускаются
этими командами.
