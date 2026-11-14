package handlers

// Synthetic fragment: изменение остатка товара, не отмена заказа.
func AdjustStock(product Product, bus Bus) error {
    return bus.Publish("StockAdjusted", StockAdjusted{ProductID: product.ID, Count: product.Count})
}
