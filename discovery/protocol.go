package discovery

// Message types carried by Envelope.
const (
	// MsgBootstrap is the cold-start handshake: a new node asks a
	// seed for a full membership snapshot and registers itself.
	MsgBootstrap = "bootstrap"
	// MsgGossip is the periodic push-pull shuffle between peers.
	MsgGossip = "gossip"
)

// Envelope is a request frame sent over a TCP connection.
type Envelope struct {
	Type    string     `json:"type"`
	From    NodeMeta   `json:"from"`
	Members []NodeMeta `json:"members,omitempty"`
}

// Response is the reply frame for both bootstrap and gossip requests.
type Response struct {
	Members []NodeMeta `json:"members"`
}
