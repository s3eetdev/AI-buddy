# Воспроизводимый отчёт покрытия

Итог: [summary.json](summary.json). Описание проверки: [verification.md](../verification.md).

| Артефакт | Назначение |
|---|---|
| [Python XML](../coverage.xml) | Native coverage.py line report |
| [Python JSON](python.json) | Файлы, исполненные/неисполненные строки и totals |
| [Frontend summary](frontend/coverage-summary.json) | Vitest/V8 totals и все runtime-файлы src |
| [Frontend LCOV](frontend/lcov.info) | Стандартный построчный профиль |
| [Go profile](go/coverage.out) | Native coverprofile с диапазонами и счётчиками |
| [Go functions](go/functions.txt) | go tool cover -func, native statements |
| [Go tests JSON](go/tests.json) | События go test -json, включая тесты PostgreSQL |

Из корня:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File tools/verify-coverage.ps1
```

Python-зависимости предварительно установите по README. Перед npm ci закройте Vite
на Windows. Скрипт восстанавливает npm-зависимости по lockfile, выполняет проверки
и требует >=81% для каждого компонента. Go исполняется в Docker с PostgreSQL;
установка Go на хост не нужна. API модели не вызывается.

Полный прогон генерирует локальные HTML: `frontend/index.html`, `go/index.html`.
Они не хранятся в Git, их можно воспроизвести приведённой командой.
Отдельно пересчитать итог из готовых профилей:

```powershell
.\.venv\Scripts\python.exe tools/coverage_summary.py
```

Python и Vitest предоставляют line coverage; у Go нативная метрика — statements.
Дополнительный подсчёт строк Go использует объединение инструментированных
диапазонов native-профиля (строка покрыта, если покрыт любой её блок). Поэтому
метрики Go lines и Go statements отличаются. Пропущенные тесты и отсутствующий
runtime-файл приводят к отказу общего скрипта, а не исключаются из отчёта.
Непокрытые участки остаются в знаменателе; процент не округляется до 100%.
