package sim

const (
	TopicCommands    = "commands"
	TopicOrderEvents = "order.events"
)

type InboxRecord struct {
	MessageID   string
	ProcessedAt int64
}

type OrderRecord struct {
	ID        string
	Status    string
	CreatedAt int64
}

type OutboxRecord struct {
	ID      string
	Topic   string
	Payload string
	Sent    bool
}

type ShipmentRecord struct {
	OrderID    string
	Status     string
	ReceivedAt int64
}
