package discovery

// NodeMeta describes a single cluster member as exchanged during
// bootstrap handshakes and gossip shuffles.
type NodeMeta struct {
	ID          string `json:"id"`
	Addr        string `json:"addr"`
	Incarnation uint64 `json:"incarnation"`
	LastSeen    int64  `json:"last_seen"` // unix nano
}
