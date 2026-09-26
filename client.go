package majorityregister

// Client issues register operations through one simulated network.
type Client struct {
	id  string
	sim *Simulation
	ops map[string]*Operation
}

// Operation is one read or write invocation.
type Operation struct {
	id        string
	client    *Client
	kind      RequestKind
	label     string
	value     []byte
	candidate Tag
	target    Tag
	result    Tag

	round      uint64
	phase      Phase
	Status     Status
	queryVotes map[int]Reply
	writeVotes map[int]Reply
}

// StartRead starts a read. readID must be unique in the simulation.
func (c *Client) StartRead(readID string) (*Operation, error) {
	if readID == "" {
		return nil, ErrEmptyLabel
	}
	if _, exists := c.sim.operationIDs[readID]; exists {
		return nil, ErrDuplicateLabel
	}

	op := &Operation{
		id:         readID,
		client:     c,
		kind:       QueryRequest,
		round:      1,
		phase:      queryPhase,
		Status:     Running,
		queryVotes: make(map[int]Reply),
		writeVotes: make(map[int]Reply),
	}
	c.ops[readID] = op
	c.sim.operationIDs[readID] = true
	op.beginRound()
	return op, nil
}

// StartWrite starts a write. The label is globally unique and participates in
// the total tag order used to break concurrent-write ties.
func (c *Client) StartWrite(label string, value []byte) (*Operation, error) {
	if label == "" {
		return nil, ErrEmptyLabel
	}
	if _, exists := c.sim.operationIDs[label]; exists {
		return nil, ErrDuplicateLabel
	}

	op := &Operation{
		id:         label,
		client:     c,
		kind:       WriteRequest,
		label:      label,
		value:      append([]byte(nil), value...),
		round:      1,
		phase:      queryPhase,
		Status:     Running,
		queryVotes: make(map[int]Reply),
		writeVotes: make(map[int]Reply),
	}
	c.ops[label] = op
	c.sim.operationIDs[label] = true
	op.beginRound()
	return op, nil
}

// ID returns the operation identifier.
func (op *Operation) ID() string { return op.id }

// Tag returns the tag returned by a completed read or installed by a completed
// write.
func (op *Operation) Tag() Tag { return op.result }

// Value returns the value returned by a completed read.
func (op *Operation) Value() []byte {
	return append([]byte(nil), op.value...)
}

// Done reports whether the operation has terminated.
func (op *Operation) Done() bool { return op.Status != Running }

// Completed reports whether the operation completed successfully.
func (op *Operation) Completed() bool { return op.Status == Completed }

func (op *Operation) beginRound() {
	op.queryVotes = make(map[int]Reply)
	op.writeVotes = make(map[int]Reply)
	op.phase = queryPhase
	op.broadcast(Request{
		Kind:  QueryRequest,
		Round: op.round,
		OpID:  op.id,
	})
	op.client.sim.scheduleTimer(op, queryPhase)
}

func (op *Operation) broadcast(req Request) {
	for node := 0; node < op.client.sim.n; node++ {
		op.client.sim.sendRequest(op.client.id, node, req)
	}
}

func (op *Operation) handleTimeout() {
	op.round++
	op.beginRound()
}

func (c *Client) handleReply(reply Reply) {
	op := c.ops[reply.OpID]
	if op == nil || op.Status != Running || reply.Round != op.round {
		return
	}

	if op.phase == queryPhase {
		op.queryVotes[reply.Node] = reply
		if len(op.queryVotes) == c.sim.quorum {
			op.finishQuery()
		}
		return
	}

	if op.kind == QueryRequest {
		op.writeVotes[reply.Node] = reply
		if reply.Tag.Compare(op.target) > 0 {
			op.round++
			op.beginRound()
			return
		}
		if len(op.writeVotes) == c.sim.quorum {
			op.finishRead()
		}
		return
	}

	if !reply.Accepted {
		op.handleRejectedWrite(reply.Tag)
		return
	}
	op.writeVotes[reply.Node] = reply
	if len(op.writeVotes) == c.sim.quorum {
		op.finishWrite()
	}
}

func (op *Operation) finishQuery() {
	highest := Tag{}
	var highestValue []byte
	for _, reply := range op.queryVotes {
		if reply.Tag.Compare(highest) > 0 {
			highest = reply.Tag
			highestValue = reply.Value
		}
	}

	if op.kind == QueryRequest {
		op.target = highest
		op.value = append([]byte(nil), highestValue...)
		op.startWrite(highest, highestValue)
		return
	}

	op.candidate = Tag{Version: highest.Version + 1, Label: op.label}
	op.startWrite(op.candidate, op.value)
}

func (op *Operation) startWrite(tag Tag, value []byte) {
	op.phase = writePhase
	op.writeVotes = make(map[int]Reply)
	op.broadcast(Request{
		Kind:  WriteRequest,
		Round: op.round,
		OpID:  op.id,
		Tag:   tag,
		Value: append([]byte(nil), value...),
	})
	op.client.sim.scheduleTimer(op, writePhase)
}

func (op *Operation) handleRejectedWrite(observed Tag) {
	if op.candidate.Compare(observed) >= 0 {
		return
	}
	if observed.Version == op.candidate.Version {
		op.Status = Aborted
		return
	}
	op.candidate = Tag{Version: observed.Version + 1, Label: op.label}
	op.round++
	op.beginRound()
}

func (op *Operation) finishRead() {
	op.Status = Completed
	op.result = op.target
}

func (op *Operation) finishWrite() {
	op.Status = Completed
	op.result = op.candidate
}
