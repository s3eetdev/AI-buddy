package handlers
// CancelOrder handles seller cancellation.
func CancelOrder(order Order, bus Bus) error {
    return bus.Publish("OrderCancelled", OrderCancelled{OrderID: order.ID, BuyerID: order.BuyerID, Reason: "seller_cancelled"})
}