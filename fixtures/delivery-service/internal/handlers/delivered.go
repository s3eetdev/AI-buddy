package handlers

// Synthetic fragment: подтверждение доставки отправления.
func MarkDelivered(shipment Shipment, bus Bus) error {
    return bus.Publish("ShipmentDelivered", ShipmentDelivered{
        ShipmentID: shipment.ID,
        OrderID: shipment.OrderID,
        DeliveredAt: shipment.DeliveredAt,
    })
}
