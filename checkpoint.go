package batchclient

// ConfirmedItem is the committed state of one request ID at a checkpoint.
type ConfirmedItem struct {
	Version  uint64
	Value    string
	Err      string
	Conflict bool
}

// Checkpoint captures resumable run state: the identity of every request,
// the confirmed commit versions, and the set of IDs still missing.
type Checkpoint struct {
	Requests  []Request
	Confirmed map[string]ConfirmedItem
	Missing   []string
}
