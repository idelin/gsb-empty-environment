package netsec

// Event is one item in the replication stream. Relation changes are stream
// metadata and take effect immediately; row events are scoped to a transaction.
type Event interface {
	replicationEvent()
}

// Begin starts a streamed transaction.
type Begin struct {
	XID TransactionID
}

// Commit publishes the transaction's buffered changes and private cache.
type Commit struct {
	XID TransactionID
}

// Abort discards the transaction's buffered changes and private cache.
type Abort struct {
	XID TransactionID
}

// RelationChange installs a new relation version.
type RelationChange struct {
	Relation Relation
}

// Insert inserts a complete row.
type Insert struct {
	XID        TransactionID
	RelationID RelationID
	Row        Row
}

// Update updates a row. OldKey is mandatory. OldTuple and NewTuple may omit
// values only according to the rules documented on Filter.Apply.
type Update struct {
	XID        TransactionID
	RelationID RelationID
	OldKey     Key
	OldTuple   Row
	NewTuple   Row
}

// Delete deletes a row. OldTuple must include the primary key and may otherwise
// be partial; missing non-key values are reconstructed from the cache.
type Delete struct {
	XID        TransactionID
	RelationID RelationID
	OldTuple   Row
}

func (Begin) replicationEvent()          {}
func (Commit) replicationEvent()         {}
func (Abort) replicationEvent()          {}
func (RelationChange) replicationEvent() {}
func (Insert) replicationEvent()         {}
func (Update) replicationEvent()         {}
func (Delete) replicationEvent()         {}
