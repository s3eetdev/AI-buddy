package mailer

// Synthetic fragment. Email is the only implemented channel.
type Mailer interface {
    Send(buyerID string, template string, orderID string) error
}
// Registered template: order_cancelled. Push and SMS are not implemented.
// Send has no idempotency guarantee; the caller must handle duplicate events.
