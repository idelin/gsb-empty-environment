package netsec

// OrderError describes why a proposed operation order is not a legal
// linearization, or which operation produced an unexpected result.
type OrderError struct {
	// Index is the zero-based position in the proposed order at which it
	// fails, or -1 when the order is not a permutation of the history.
	Index int
	// OperationID identifies the offending operation when available.
	OperationID int
	// Reason explains the failure.
	Reason string
}

func (e *OrderError) Error() string {
	return e.Reason
}

// VerifyOrder checks that order is a permutation of every operation ID in h,
// respects each invoke/return interval, and drives the reference spec through
// exactly the observed results.
func VerifyOrder(h History, order []int) error {
	if err := h.Validate(); err != nil {
		return err
	}
	byID := make(map[int]Operation, len(h.Operations))
	for _, op := range h.Operations {
		byID[op.ID] = op
	}
	if len(order) != len(byID) {
		return &OrderError{Index: -1, Reason: "order length does not match history length"}
	}

	limit := h.WrongLimit
	if limit <= 0 {
		limit = DefaultWrongLimit
	}
	spec := NewSpec(limit)
	used := make(map[int]bool, len(order))
	for i, id := range order {
		op, ok := byID[id]
		if !ok {
			return &OrderError{Index: i, OperationID: id, Reason: "unknown or duplicated operation id"}
		}
		if used[id] {
			return &OrderError{Index: i, OperationID: id, Reason: "operation id appears more than once"}
		}
		used[id] = true
		if !readyInResidual(byID, used, op) {
			return &OrderError{Index: i, OperationID: id, Reason: "linearization point outside invoke/return interval"}
		}
		if !spec.Apply(op) {
			return &OrderError{Index: i, OperationID: id, Reason: "observed result inconsistent with specification"}
		}
	}
	return nil
}

// readyInResidual verifies that op is Wing-Wing-Gong ready in the residual
// set: its invoke is no later than the earliest return still outstanding, so
// a common linearization point can be placed inside every interval.
func readyInResidual(byID map[int]Operation, used map[int]bool, op Operation) bool {
	minReturn := maxInt64Value
	for id, candidate := range byID {
		if !used[id] && candidate.Return < minReturn {
			minReturn = candidate.Return
		}
	}
	return op.Invoke <= minReturn
}
