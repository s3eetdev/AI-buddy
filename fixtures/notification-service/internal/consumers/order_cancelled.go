package consumers
// Subscribed to OrderCancelled.
func OnOrderCancelled(event OrderCancelled, mail Mailer) error {
    if event.Reason != "payment_expired" { return nil }
    return mail.Send(event.BuyerID, "order_cancelled", event.OrderID)
}