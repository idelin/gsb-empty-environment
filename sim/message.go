package sim

// Message types exchanged between replicas.
const (
	MsgTransfer = 1 // credit transfer request: sender -> receiver
	MsgAck      = 2 // acknowledgement of a transfer: receiver -> sender
)

// Message is a single network packet in the simulated network.
type Message struct {
	Kind   int
	ID     TransferID // unique id of the transfer this message belongs to
	Amount int64      // used by MsgTransfer; 0 for MsgAck
	SentAt int64      // virtual time at which the message entered the network
	Sender int64
	Target int64
}

// TransferID uniquely and globally identifies one transfer attempt.
// A transfer and every retry / duplicate copy of it carry the same ID,
// so application-level dedup can guarantee single crediting.
type TransferID struct {
	From int64
	Seq  int64
}
