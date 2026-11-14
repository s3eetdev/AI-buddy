# Локальный стенд синтетического маркетплейса
Это описание вымышленной системы для чтения AI, не команда запуска сервисов.
order-service: создание и отмена заказа; команда orders.
delivery-service: статусы отправления; команда logistics.
notification-service: email-уведомления; команда messaging.
inventory-service: остатки товаров; команда catalog.
OrderCancelled связывает order-service и notification-service.
ShipmentDelivered связывает delivery-service и order-service.
StockAdjusted относится к остаткам, а не к отмене заказа или доставке.
Go-файлы в fixtures — учебные фрагменты, отдельные исполняемые микросервисы не поставляются.
