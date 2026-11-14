# Синтетическая компания «Учебный маркетплейс»

Все задачи, команды, пути и правила вымышлены. Это не внутренние данные Авито.
Код — фрагменты для анализа, не запускаемый набор Go-микросервисов.

Основные цепочки:

- Отмена: order-service → OrderCancelled → notification-service → email.
- Доставка: delivery-service → ShipmentDelivered → order-service.
- Остатки: inventory-service → StockAdjusted → search-service (отвлекающий контекст).

Снимки индексируются по `data/source_manifest.json`. Источник истины — файлы
fixtures; после изменения выполните `python scripts/build_corpus.py`.
Документы legacy/adversarial включаются только в соответствующих тестовых scopes.
Закрытый источник private служит для проверки ACL и содержит лишь тестовый маркер.

Jira-задачи и профили для Go: `data/demo_tasks.json`.
Эталонные ожидания оценивания: `data/evaluation_cases.json`; они НЕ передаются LLM.
