# Synthetic demo: cancellation / отмена заказа
order-service publishes OrderCancelled after cancellation.
notification-service subscribes to OrderCancelled and sends email for payment_expired.
Seller cancellation currently does not send email.
BuyerID is present in the contract.
Delivery is at-least-once; consumer deduplication is not implemented in this fixture.
Общие правила новой отмены не заданы: используйте критерии конкретной задачи.
Для DEMO-1 согласованные требования находятся в cancellation_decision.md.
These fixtures are not Avito policies or production code.
