package netsec

import (
	"hash/fnv"
	"sort"
)

// MarkerToken returns a deterministic 32-bit token derived from the complete
// request identity, including the exact input token sequence. It is intended
// for deterministic scheduling tests, not as a security MAC.
func MarkerToken(req Request) uint32 {
	h := fnv.New32a()
	writeString(h, req.TenantID)
	writeString(h, req.RequestID)
	writeUint64(h, req.ModelGeneration)
	writeUint64(h, uint64(req.OutputLimit()))
	writeUint64(h, uint64(len(req.InputTokens)))
	for _, token := range req.InputTokens {
		writeUint32(h, token)
	}
	return h.Sum32()
}

// MarkerTokenForRow is the batch-row form of MarkerToken.
func MarkerTokenForRow(row BatchRow) uint32 {
	h := fnv.New32a()
	writeString(h, row.TenantID)
	writeString(h, row.RequestID)
	writeUint64(h, row.ModelGeneration)
	writeUint64(h, uint64(row.MaxOutputTokens))
	writeUint64(h, uint64(len(row.InputTokens)))
	for _, token := range row.InputTokens {
		writeUint32(h, token)
	}
	return h.Sum32()
}

// MarkerOutput returns up to MaxOutputTokens tokens. The first token is always
// the request identity marker when the limit is non-zero. The remaining tokens
// encode the input length, the input itself, and identity hashes. This makes
// empty inputs and tenant-local request IDs distinguishable.
func MarkerOutput(req Request) []uint32 {
	limit := req.OutputLimit()
	if limit <= 0 {
		return nil
	}
	out := make([]uint32, 0, min(limit, 2+len(req.InputTokens)+3))
	out = append(out, MarkerToken(req))
	if len(out) >= limit {
		return out
	}
	out = append(out, uint32(len(req.InputTokens)))
	for _, token := range req.InputTokens {
		if len(out) >= limit {
			return out
		}
		out = append(out, token)
	}
	if len(out) < limit {
		out = append(out, hashText("tenant:"+req.TenantID))
	}
	if len(out) < limit {
		out = append(out, hashText("request:"+req.RequestID))
	}
	return out
}

// MarkerOutputForRow is the batch-row form of MarkerOutput.
func MarkerOutputForRow(row BatchRow) []uint32 {
	limit := row.MaxOutputTokens
	if limit <= 0 {
		return nil
	}
	out := make([]uint32, 0, min(limit, 2+len(row.InputTokens)+3))
	out = append(out, MarkerTokenForRow(row))
	if len(out) >= limit {
		return out
	}
	out = append(out, uint32(len(row.InputTokens)))
	for _, token := range row.InputTokens {
		if len(out) >= limit {
			return out
		}
		out = append(out, token)
	}
	if len(out) < limit {
		out = append(out, hashText("tenant:"+row.TenantID))
	}
	if len(out) < limit {
		out = append(out, hashText("request:"+row.RequestID))
	}
	return out
}

func hashText(value string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(value))
	return h.Sum32()
}

// MarkerModel is a deterministic callback that encodes each row's own input
// identity in its output.
type MarkerModel struct{}

// Callback implements TensorCallback.
func (MarkerModel) Callback(batch Batch) (TensorResult, error) {
	return MarkerModel{}.Result(batch), nil
}

// Result returns a completed marker result for batch.
func (MarkerModel) Result(batch Batch) TensorResult {
	result := NewTensorResult(batch)
	for i, row := range batch.Rows {
		result.Rows[i].Tokens = MarkerOutputForRow(row)
		result.Rows[i].Done = true
	}
	return result
}

// StreamCallback implements StreamCallback. A frozen row is returned exactly
// as it was received; other rows receive their complete marker output.
func (MarkerModel) StreamCallback(step StreamStep) (StreamResult, error) {
	result := NewStreamResult(step)
	for i, row := range step.Rows {
		if row.Frozen {
			result.Rows[i].Tokens = cloneTokens(row.OutputTokens)
			result.Rows[i].Done = true
			continue
		}
		result.Rows[i].Tokens = MarkerOutputForRow(BatchRow{
			TenantID:        row.TenantID,
			RequestID:       row.RequestID,
			ModelGeneration: step.ModelGeneration,
			InputTokens:     row.InputTokens,
			InputLength:     row.InputLength,
			MaxOutputTokens: row.MaxOutputTokens,
			IdentityDigest:  row.IdentityDigest,
		})
		result.Rows[i].Done = true
	}
	return result, nil
}

// LeakWitness describes one delivered token that is attributable to another
// tenant's request.
type LeakWitness struct {
	BatchID       string
	ToTenantID    string
	ToRequestID   string
	ToRowIndex    int
	FromTenantID  string
	FromRequestID string
	FromRowIndex  int
	Token         uint32
	TokenIndex    int
}

// DetectLeak checks whether a delivery to one tenant contains the identity
// marker of a request from a different tenant. The source and destination are
// matched by batch row metadata, never by completion order.
func DetectLeak(batch Batch, deliveries []Delivery) *LeakWitness {
	for _, delivery := range deliveries {
		if delivery.RowIndex < 0 || delivery.RowIndex >= len(batch.Rows) {
			continue
		}
		target := batch.Rows[delivery.RowIndex]
		for sourceIndex, source := range batch.Rows {
			if source.TenantID == target.TenantID {
				continue
			}
			marker := MarkerTokenForRow(source)
			for tokenIndex, token := range delivery.Tokens {
				if token != marker {
					continue
				}
				return &LeakWitness{
					BatchID:       batch.BatchID,
					ToTenantID:    target.TenantID,
					ToRequestID:   target.RequestID,
					ToRowIndex:    target.RowIndex,
					FromTenantID:  source.TenantID,
					FromRequestID: source.RequestID,
					FromRowIndex:  sourceIndex,
					Token:         token,
					TokenIndex:    tokenIndex,
				}
			}
		}
	}
	return nil
}

// DetectCrossTenantLeak is an alias for DetectLeak.
func DetectCrossTenantLeak(batch Batch, deliveries []Delivery) *LeakWitness {
	return DetectLeak(batch, deliveries)
}

// IsLeakFree reports whether none of the deliveries contains another tenant's
// request marker.
func IsLeakFree(batch Batch, deliveries []Delivery) bool {
	return DetectLeak(batch, deliveries) == nil
}

// SortDeliveriesByRow returns a copy sorted by stable row index. It is exposed
// for hosts that record completion events before presenting them upstream.
func SortDeliveriesByRow(deliveries []Delivery) []Delivery {
	out := make([]Delivery, len(deliveries))
	for i, delivery := range deliveries {
		out[i] = delivery
		out[i].Tokens = cloneTokens(delivery.Tokens)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RowIndex != out[j].RowIndex {
			return out[i].RowIndex < out[j].RowIndex
		}
		return out[i].IdentityDigest < out[j].IdentityDigest
	})
	return out
}
