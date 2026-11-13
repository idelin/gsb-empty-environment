package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"
)

// MappingMode selects the callback mapping policy under review.
type MappingMode string

const (
	// MappingByRowIndex is the safe policy: output for row i is delivered to
	// row i.
	MappingByRowIndex MappingMode = "row-index"
	// MappingByCompletionOrder models the bug where callback results are
	// assigned to the current queue in completion order.
	MappingByCompletionOrder MappingMode = "completion-order"
	// MappingByQueuePosition models the bug where a cancellation compacts the
	// queue and subsequent rows shift into the wrong slot.
	MappingByQueuePosition MappingMode = "queue-position"
)

// LeakSearchConfig describes a finite deterministic search. Requests must have
// one model generation and one decoding strategy so they form one canonical
// batch.
type LeakSearchConfig struct {
	Requests []Request
	Mapping  MappingMode
	// MaxDepth defaults to 6 lifecycle actions after batch formation.
	MaxDepth int
	// MaxStates defaults to 50000 explored states.
	MaxStates int
}

// TraceActionKind identifies a searched scheduler/callback action.
type TraceActionKind string

const (
	ActionCancelRequest   TraceActionKind = "cancel"
	ActionCompleteRow     TraceActionKind = "complete"
	ActionDuplicateResult TraceActionKind = "duplicate-result"
	ActionSwitchModel     TraceActionKind = "model-switch"
)

// TraceAction is one replayable action in a shortest leak trace.
type TraceAction struct {
	Kind                TraceActionKind
	TenantID            string
	RequestID           string
	CancelGeneration    uint64
	ModelGeneration     uint64
	SourceRowIndex      int
	DestinationRowIndex int
	Done                bool
	// Partial is true when only the first marker token was completed. This
	// models a partially completed result without requiring a full decode.
	Partial     bool
	Description string
}

// LeakTrace is a shortest witness found by the search.
type LeakTrace struct {
	BatchID    string
	Actions    []TraceAction
	Witness    LeakWitness
	Deliveries []Delivery
}

// LeakSearchResult is the result of SearchShortestLeak.
type LeakSearchResult struct {
	Found    bool
	Trace    LeakTrace
	Explored int
	Reason   string
}

// ShortestTrace returns a copy of the shortest action sequence.
func (r LeakSearchResult) ShortestTrace() []TraceAction {
	out := make([]TraceAction, len(r.Trace.Actions))
	copy(out, r.Trace.Actions)
	return out
}

// SearchShortestLeak performs a breadth-first, deterministic search over
// cancellation, completion, duplicate callback result, and model-generation
// switch actions. It returns the shortest sequence that causes a token
// attributable to tenant B's input to be delivered as tenant A's row.
func SearchShortestLeak(cfg LeakSearchConfig) (LeakSearchResult, error) {
	normalized, err := normalizeSearchConfig(cfg)
	if err != nil {
		return LeakSearchResult{}, err
	}

	start, err := replaySearch(normalized, nil)
	if err != nil {
		return LeakSearchResult{}, err
	}
	startKey := searchStateKey(start)
	visited := map[string]struct{}{startKey: {}}
	type node struct {
		actions []TraceAction
		runtime *searchRuntime
	}
	queue := []node{{actions: nil, runtime: start}}

	explored := 0
	for len(queue) != 0 {
		current := queue[0]
		queue = queue[1:]
		explored++
		if explored > normalized.maxStates {
			return LeakSearchResult{
				Explored: explored - 1,
				Reason:   fmt.Sprintf("state limit %d reached", normalized.maxStates),
			}, nil
		}
		if current.runtime.witness != nil {
			return foundSearchResult(current.actions, current.runtime, explored), nil
		}
		if len(current.actions) >= normalized.maxDepth {
			continue
		}

		for _, action := range nextSearchActions(current.runtime) {
			childActions := append(append([]TraceAction(nil), current.actions...), action)
			childRuntime, replayErr := replaySearch(normalized, childActions)
			if replayErr != nil {
				continue
			}
			key := searchStateKey(childRuntime)
			if _, seen := visited[key]; seen {
				continue
			}
			visited[key] = struct{}{}
			if childRuntime.witness != nil {
				return foundSearchResult(childActions, childRuntime, explored), nil
			}
			queue = append(queue, node{actions: childActions, runtime: childRuntime})
		}
	}

	return LeakSearchResult{
		Explored: explored,
		Reason:   fmt.Sprintf("no cross-tenant leak within depth %d", normalized.maxDepth),
	}, nil
}

// FindShortestLeak is an alias for SearchShortestLeak.
func FindShortestLeak(cfg LeakSearchConfig) (LeakSearchResult, error) {
	return SearchShortestLeak(cfg)
}

// FindShortestLeakTrace is an alias for SearchShortestLeak.
func FindShortestLeakTrace(cfg LeakSearchConfig) (LeakSearchResult, error) {
	return SearchShortestLeak(cfg)
}

// SearchShortestLeakTrace is an alias for SearchShortestLeak.
func SearchShortestLeakTrace(cfg LeakSearchConfig) (LeakSearchResult, error) {
	return SearchShortestLeak(cfg)
}

// SearchMappingLeak is an alias for SearchShortestLeak.
func SearchMappingLeak(cfg LeakSearchConfig) (LeakSearchResult, error) {
	return SearchShortestLeak(cfg)
}

// SearchCrossTenantLeak is an alias for SearchShortestLeak.
func SearchCrossTenantLeak(cfg LeakSearchConfig) (LeakSearchResult, error) {
	return SearchShortestLeak(cfg)
}

type normalizedSearchConfig struct {
	requests  []Request
	mapping   MappingMode
	maxDepth  int
	maxStates int
}

func normalizeSearchConfig(cfg LeakSearchConfig) (normalizedSearchConfig, error) {
	if len(cfg.Requests) == 0 {
		return normalizedSearchConfig{}, fmt.Errorf("%w: search needs at least one request", ErrInvalidRequest)
	}
	if cfg.MaxDepth < 0 || cfg.MaxStates < 0 {
		return normalizedSearchConfig{}, fmt.Errorf("%w: negative search bound", ErrInvalidRequest)
	}
	if cfg.MaxDepth == 0 {
		cfg.MaxDepth = 6
	}
	if cfg.MaxStates == 0 {
		cfg.MaxStates = 50000
	}
	if cfg.Mapping == "" {
		cfg.Mapping = MappingByRowIndex
	}
	switch cfg.Mapping {
	case MappingByRowIndex, MappingByCompletionOrder, MappingByQueuePosition:
	default:
		return normalizedSearchConfig{}, fmt.Errorf("%w: unknown mapping mode %q", ErrInvalidRequest, cfg.Mapping)
	}

	requests := make([]Request, len(cfg.Requests))
	seen := make(map[requestKey]struct{}, len(cfg.Requests))
	generationSet := false
	var generation uint64
	var strategy DecodeStrategy
	for i, raw := range cfg.Requests {
		req, err := normalizeRequest(raw)
		if err != nil {
			return normalizedSearchConfig{}, err
		}
		key := requestKey{tenantID: req.TenantID, requestID: req.RequestID}
		if _, exists := seen[key]; exists {
			return normalizedSearchConfig{}, fmt.Errorf("%w: tenant=%q request=%q", ErrDuplicateRequest, req.TenantID, req.RequestID)
		}
		seen[key] = struct{}{}
		if !generationSet {
			generation = req.ModelGeneration
			strategy = req.DecodeStrategy
			generationSet = true
		} else if req.ModelGeneration != generation || req.DecodeStrategy != strategy {
			return normalizedSearchConfig{}, fmt.Errorf("%w: search requests must share model generation and decode strategy", ErrInvalidRequest)
		}
		if req.ArrivalSequence == 0 {
			req.ArrivalSequence = uint64(i + 1)
		}
		requests[i] = req
	}
	sort.SliceStable(requests, func(i, j int) bool {
		if requests[i].ArrivalSequence != requests[j].ArrivalSequence {
			return requests[i].ArrivalSequence < requests[j].ArrivalSequence
		}
		if requests[i].RequestID != requests[j].RequestID {
			return requests[i].RequestID < requests[j].RequestID
		}
		return requests[i].TenantID < requests[j].TenantID
	})
	return normalizedSearchConfig{
		requests:  requests,
		mapping:   cfg.Mapping,
		maxDepth:  cfg.MaxDepth,
		maxStates: cfg.MaxStates,
	}, nil
}

type searchRuntime struct {
	config     normalizedSearchConfig
	ledger     *Ledger
	batch      Batch
	canceled   []bool
	done       []bool
	outputs    [][]uint32
	lastResult *TensorResult
	switched   bool
	duplicated bool
	deliveries []Delivery
	witness    *LeakWitness
}

func replaySearch(cfg normalizedSearchConfig, actions []TraceAction) (*searchRuntime, error) {
	ledger := NewLedger(LedgerConfig{
		Clock: func() time.Time { return time.Time{} },
		BatchID: func() (string, error) {
			return "batch-search-1", nil
		},
	})
	for _, req := range cfg.requests {
		if err := ledger.Submit(req); err != nil {
			return nil, err
		}
	}
	batch, err := ledger.NextBatch()
	if err != nil {
		return nil, err
	}
	if len(batch.Rows) != len(cfg.requests) {
		return nil, fmt.Errorf("%w: canonical batch did not include every request", ErrInvalidRequest)
	}
	rt := &searchRuntime{
		config:   cfg,
		ledger:   ledger,
		batch:    batch,
		canceled: make([]bool, len(batch.Rows)),
		done:     make([]bool, len(batch.Rows)),
		outputs:  make([][]uint32, len(batch.Rows)),
	}
	for _, action := range actions {
		if err := applySearchAction(rt, action); err != nil {
			return nil, err
		}
	}
	return rt, nil
}

func applySearchAction(rt *searchRuntime, action TraceAction) error {
	switch action.Kind {
	case ActionCancelRequest:
		index, ok := searchRowIndex(rt.batch, action.TenantID, action.RequestID)
		if !ok {
			return fmt.Errorf("%w: row for tenant=%q request=%q", ErrUnknownRequest, action.TenantID, action.RequestID)
		}
		err := rt.ledger.Cancel(action.TenantID, action.RequestID, action.CancelGeneration)
		if err != nil {
			return err
		}
		rt.canceled[index] = true
		return nil

	case ActionCompleteRow:
		if action.SourceRowIndex < 0 || action.SourceRowIndex >= len(rt.batch.Rows) ||
			action.DestinationRowIndex < 0 || action.DestinationRowIndex >= len(rt.batch.Rows) {
			return fmt.Errorf("%w: source=%d destination=%d", ErrRowIndex, action.SourceRowIndex, action.DestinationRowIndex)
		}
		if rt.done[action.DestinationRowIndex] {
			return fmt.Errorf("%w: row=%d", ErrFrozenRowModified, action.DestinationRowIndex)
		}
		tokens := MarkerOutputForRow(rt.batch.Rows[action.SourceRowIndex])
		if action.Partial && len(tokens) > 1 {
			tokens = tokens[:1]
		}
		rt.outputs[action.DestinationRowIndex] = cloneTokens(tokens)
		rt.done[action.DestinationRowIndex] = action.Done
		result := searchResultFromRuntime(rt)
		deliveries, err := rt.ledger.ApplyResult(result)
		if err != nil {
			if isModelGenerationError(err) {
				rt.lastResult = copyTensorResult(result)
				return nil
			}
			return err
		}
		rt.lastResult = copyTensorResult(result)
		rt.duplicated = false
		rt.deliveries = append(rt.deliveries, deliveries...)
		if witness := DetectLeak(rt.batch, deliveries); witness != nil {
			rt.witness = witness
		}
		return nil

	case ActionDuplicateResult:
		if rt.lastResult == nil {
			return fmt.Errorf("%w: no previous callback result", ErrResultConflict)
		}
		deliveries, err := rt.ledger.ApplyResult(*copyTensorResult(*rt.lastResult))
		if err != nil {
			if isModelGenerationError(err) {
				rt.duplicated = true
				return nil
			}
			return err
		}
		rt.duplicated = true
		rt.deliveries = append(rt.deliveries, deliveries...)
		if len(deliveries) != 0 {
			if witness := DetectLeak(rt.batch, deliveries); witness != nil {
				rt.witness = witness
			}
		}
		return nil

	case ActionSwitchModel:
		rt.ledger.SetModelGeneration(action.ModelGeneration)
		rt.switched = true
		return nil

	default:
		return fmt.Errorf("%w: unknown action %q", ErrInvalidRequest, action.Kind)
	}
}

func searchResultFromRuntime(rt *searchRuntime) TensorResult {
	result := NewTensorResult(rt.batch)
	for i := range result.Rows {
		result.Rows[i].Tokens = cloneTokens(rt.outputs[i])
		result.Rows[i].Done = rt.done[i]
	}
	return result
}

func copyTensorResult(result TensorResult) *TensorResult {
	out := result
	out.Rows = make([]TensorRow, len(result.Rows))
	for i, row := range result.Rows {
		out.Rows[i] = row
		out.Rows[i].Tokens = cloneTokens(row.Tokens)
	}
	return &out
}

func searchRowIndex(batch Batch, tenantID, requestID string) (int, bool) {
	for i, row := range batch.Rows {
		if row.TenantID == tenantID && row.RequestID == requestID {
			return i, true
		}
	}
	return 0, false
}

func nextSearchActions(rt *searchRuntime) []TraceAction {
	actions := make([]TraceAction, 0, len(rt.batch.Rows)*4+4)

	// Cancellation actions come first in deterministic order.
	for i, row := range rt.batch.Rows {
		if rt.canceled[i] || rt.done[i] {
			continue
		}
		actions = append(actions, TraceAction{
			Kind:             ActionCancelRequest,
			TenantID:         row.TenantID,
			RequestID:        row.RequestID,
			CancelGeneration: row.CancelGeneration,
			Description:      fmt.Sprintf("cancel row %d (%s/%s)", i, row.TenantID, row.RequestID),
		})
	}

	nextQueueRow := -1
	for i := range rt.batch.Rows {
		if !rt.canceled[i] && !rt.done[i] {
			nextQueueRow = i
			break
		}
	}

	for source := range rt.batch.Rows {
		if rt.canceled[source] || rt.done[source] {
			continue
		}
		destination := source
		switch rt.config.mapping {
		case MappingByRowIndex:
			destination = source
		case MappingByCompletionOrder, MappingByQueuePosition:
			destination = nextQueueRow
			if destination < 0 {
				continue
			}
		}
		if rt.canceled[destination] || rt.done[destination] {
			continue
		}
		if len(rt.outputs[destination]) != 0 && MappingByCompletionOrder == rt.config.mapping {
			// A completion-order mapping advances only to a fresh queue row.
			continue
		}
		actions = append(actions,
			TraceAction{
				Kind:                ActionCompleteRow,
				SourceRowIndex:      source,
				DestinationRowIndex: destination,
				Done:                true,
				Description:         completeDescription(rt, source, destination, false),
			},
			TraceAction{
				Kind:                ActionCompleteRow,
				SourceRowIndex:      source,
				DestinationRowIndex: destination,
				Done:                false,
				Partial:             true,
				Description:         completeDescription(rt, source, destination, true),
			},
		)
	}

	if rt.lastResult != nil && !rt.duplicated {
		actions = append(actions, TraceAction{
			Kind:        ActionDuplicateResult,
			Description: "replay the previous callback result",
		})
	}
	if !rt.switched && rt.batch.ModelGeneration != ^uint64(0) {
		actions = append(actions, TraceAction{
			Kind:            ActionSwitchModel,
			ModelGeneration: rt.batch.ModelGeneration + 1,
			Description:     fmt.Sprintf("switch model generation to %d", rt.batch.ModelGeneration+1),
		})
	}
	return actions
}

func completeDescription(rt *searchRuntime, source, destination int, partial bool) string {
	kind := "full"
	if partial {
		kind = "partial"
	}
	return fmt.Sprintf("deliver %s output from row %d (%s/%s) to row %d (%s/%s)",
		kind,
		source,
		rt.batch.Rows[source].TenantID,
		rt.batch.Rows[source].RequestID,
		destination,
		rt.batch.Rows[destination].TenantID,
		rt.batch.Rows[destination].RequestID,
	)
}

func searchStateKey(rt *searchRuntime) string {
	h := sha256.New()
	writeString(h, rt.batch.BatchID)
	writeUint64(h, rt.batch.ModelGeneration)
	if rt.switched {
		writeUint64(h, 1)
	} else {
		writeUint64(h, 0)
	}
	if rt.duplicated {
		writeUint64(h, 1)
	} else {
		writeUint64(h, 0)
	}
	writeUint64(h, uint64(len(rt.batch.Rows)))
	for i := range rt.batch.Rows {
		if rt.canceled[i] {
			writeUint64(h, 1)
		} else {
			writeUint64(h, 0)
		}
		if rt.done[i] {
			writeUint64(h, 1)
		} else {
			writeUint64(h, 0)
		}
		writeUint64(h, uint64(len(rt.outputs[i])))
		for _, token := range rt.outputs[i] {
			writeUint32(h, token)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func foundSearchResult(actions []TraceAction, rt *searchRuntime, explored int) LeakSearchResult {
	trace := LeakTrace{
		BatchID:    rt.batch.BatchID,
		Actions:    append([]TraceAction(nil), actions...),
		Deliveries: append([]Delivery(nil), rt.deliveries...),
	}
	if rt.witness != nil {
		trace.Witness = *rt.witness
	}
	for i := range trace.Deliveries {
		trace.Deliveries[i].Tokens = cloneTokens(trace.Deliveries[i].Tokens)
	}
	return LeakSearchResult{
		Found:    true,
		Trace:    trace,
		Explored: explored,
		Reason:   "shortest cross-tenant mapping trace",
	}
}

func isModelGenerationError(err error) bool {
	return err != nil && (errors.Is(err, ErrModelGeneration) || errors.Is(err, ErrBatchClosed))
}
