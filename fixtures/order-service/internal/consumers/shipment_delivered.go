package consumers

// Synthetic fragment. Subscription: ShipmentDelivered.
func OnShipmentDelivered(event ShipmentDelivered, orders Store) error {
    // Current defect: records time but leaves status as shipped.
    return orders.SetDeliveredAt(event.OrderID, event.DeliveredAt)
}
