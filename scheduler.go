package radixkv

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// State is the scheduler lifecycle state.
type State string

const (
	StateWaiting    State = "waiting"
	StatePrefilling State = "prefilling"
	StateDecoding   State = "decoding"
	StateRunning    State = "decoding" // Running is treated as the decode phase after prefill.
	StateDone       State = "done"
	StatePreempted  State = "waiting"
)

// SchedulingStatus is an alias-style enum for callers preferring "Status".
type SchedulingStatus = State

const (
	StatusWaiting    = StateWaiting
	StatusPrefilling = StatePrefilling
	StatusDecoding   = StateDecoding
	StatusRunning    = StateRunning
	StatusDone       = StateDone
	StatusPreempted  = StatePreempted
)

const defaultMaxNewTokens = 16

// Request is a deterministic in-process LLM request record.
type Request struct {
	ID        string
	Prompt    []Token
	Input     []Token
	Generated []Token

	// MaxNewTokens is the generation length target. MaxTokens is accepted as an alias.
	MaxNewTokens int
	MaxTokens    int

	Priority     int
	ArrivalSeq   int64
	ArrivalIndex int

	State  State
	Status State

	CachedTokens         int
	PrefillCursor        int
	ComputedPromptTokens int

	// BlockIDs are unattached physical blocks owned by this running request.
	BlockIDs []BlockID

	PreemptionCount int
}

// StepResult records one deterministic scheduler iteration.
type StepResult struct {
	Step       int
	Budget     int
	UsedTokens int

	Scheduled              []string
	DecodeTokens           map[string]int
	PrefillTokens          map[string]int
	SkippedCachedTokens    map[string]int
	EvictedNodeIDs         []int
	PreemptedRequestIDs    []string
	CompletedRequestIDs    []string
	BlockLimitedRequestIDs []string

	Conservation ConservationReport
}

func newStepResult(step, budget int) *StepResult {
	return &StepResult{
		Step:                step,
		Budget:              budget,
		DecodeTokens:        make(map[string]int),
		PrefillTokens:       make(map[string]int),
		SkippedCachedTokens: make(map[string]int),
	}
}

// Scheduler is a single-step mixed decode/chunked-prefill scheduler.
type Scheduler struct {
	mu               sync.Mutex
	cache            *Cache
	maxTokensPerStep int
	requests         map[string]*Request
	running          map[string]*Request
	arrivalCounter   int64
	stepCount        int
}

// NewScheduler creates a cache and scheduler sharing one totalBlocks pool.
func NewScheduler(totalBlocks, blockSize, maxTokensPerStep int) *Scheduler {
	cache := NewRadixCache(totalBlocks, blockSize)
	return NewSchedulerWithCache(cache, maxTokensPerStep)
}

// NewSchedulerWithCache binds the scheduler to an existing Radix cache/pool.
func NewSchedulerWithCache(cache *Cache, maxTokensPerStep int) *Scheduler {
	if cache == nil {
		panic(fmt.Errorf("%w: nil cache", ErrInvalidConfig))
	}
	if maxTokensPerStep <= 0 {
		panic(fmt.Errorf("%w: maxTokensPerStep must be positive", ErrInvalidConfig))
	}
	return &Scheduler{
		cache:            cache,
		maxTokensPerStep: maxTokensPerStep,
		requests:         make(map[string]*Request),
		running:          make(map[string]*Request),
	}
}

// NewSchedulerFromCache is an alias for NewSchedulerWithCache.
func NewSchedulerFromCache(cache *Cache, maxTokensPerStep int) *Scheduler {
	return NewSchedulerWithCache(cache, maxTokensPerStep)
}

// Cache returns the bound RadixAttention cache.
func (s *Scheduler) Cache() *Cache { return s.cache }

// MaxTokensPerStep returns the compute budget.
func (s *Scheduler) MaxTokensPerStep() int { return s.maxTokensPerStep }

// StepCount returns executed iterations.
func (s *Scheduler) StepCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stepCount
}

func (r *Request) setState(state State) {
	r.State = state
	r.Status = state
}

// Submit validates and enqueues a request.
func (s *Scheduler) Submit(request *Request) error {
	if request == nil {
		return fmt.Errorf("%w: nil request", ErrInvalidConfig)
	}
	if request.ID == "" {
		return fmt.Errorf("%w: empty request id", ErrInvalidConfig)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.requests[request.ID]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateRequestID, request.ID)
	}

	req := &Request{
		ID:           request.ID,
		Prompt:       cloneTokens(request.Prompt),
		Input:        cloneTokens(request.Prompt),
		Generated:    cloneTokens(request.Generated),
		MaxNewTokens: request.MaxNewTokens,
		MaxTokens:    request.MaxTokens,
		Priority:     request.Priority,
		ArrivalSeq:   s.arrivalCounter + 1,
		ArrivalIndex: int(s.arrivalCounter + 1),
		BlockIDs:     cloneBlockIDs(request.BlockIDs),
	}
	s.arrivalCounter++
	if req.MaxNewTokens <= 0 {
		if req.MaxTokens > 0 {
			req.MaxNewTokens = req.MaxTokens
		} else {
			req.MaxNewTokens = defaultMaxNewTokens
		}
	}
	req.setState(StateWaiting)
	s.requests[req.ID] = req
	return nil
}

// Add submits a prepared request and panics on invalid setup.
func (s *Scheduler) Add(request *Request) *Request {
	if err := s.Submit(request); err != nil {
		panic(err)
	}
	return s.requests[request.ID]
}

// AddRequest is a concise setup helper. maxNewTokens may be omitted for a default.
func (s *Scheduler) AddRequest(id string, prompt []Token, maxNewTokens ...int) *Request {
	maxTokens := 0
	if len(maxNewTokens) > 0 {
		maxTokens = maxNewTokens[0]
	}
	req := &Request{ID: id, Prompt: prompt, MaxNewTokens: maxTokens}
	s.Add(req)
	return s.requests[id]
}

// Get returns a stored request.
func (s *Scheduler) Get(id string) (*Request, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	req, ok := s.requests[id]
	return req, ok
}

// Requests returns all requests in arrival order.
func (s *Scheduler) Requests() []*Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sortedRequestsLocked()
}

func (s *Scheduler) sortedRequestsLocked(states ...State) []*Request {
	allowed := make(map[State]bool, len(states))
	for _, state := range states {
		allowed[state] = true
	}
	out := make([]*Request, 0, len(s.requests))
	for _, req := range s.requests {
		if len(allowed) == 0 || allowed[req.State] {
			out = append(out, req)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ArrivalSeq != out[j].ArrivalSeq {
			return out[i].ArrivalSeq < out[j].ArrivalSeq
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Waiting returns waiting/preempted requests in arrival order.
func (s *Scheduler) Waiting() []*Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sortedRequestsLocked(StateWaiting)
}

// Decoding returns in-flight decode requests oldest-first.
func (s *Scheduler) Decoding() []*Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sortedRequestsLocked(StateDecoding)
}

// Prefilling returns active chunked-prefill requests oldest-first.
func (s *Scheduler) Prefilling() []*Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sortedRequestsLocked(StatePrefilling)
}

// AllDone reports whether every submitted request reached Done.
func (s *Scheduler) AllDone() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, req := range s.requests {
		if req.State != StateDone {
			return false
		}
	}
	return len(s.requests) > 0
}

func blocksNeededBetween(cursor, end, blockSize int) int {
	count := 0
	for tokenIndex := cursor; tokenIndex < end; tokenIndex++ {
		if tokenIndex%blockSize == 0 {
			count++
		}
	}
	return count
}

func balancedGrants(candidates []prefillCandidate, budget int) map[string]int {
	grants := make(map[string]int, len(candidates))
	if budget <= 0 {
		return grants
	}
	left := budget
	for left > 0 {
		progressed := false
		for _, candidate := range candidates {
			if left == 0 {
				break
			}
			id := candidate.req.ID
			if grants[id] < candidate.remaining {
				grants[id]++
				left--
				progressed = true
			}
		}
		if !progressed {
			break
		}
	}
	return grants
}

type prefillCandidate struct {
	req       *Request
	remaining int
}

// Step executes one mixed scheduling iteration.
func (s *Scheduler) Step() (result *StepResult, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.stepCount++
	result = newStepResult(s.stepCount, s.maxTokensPerStep)
	defer func() {
		result.Conservation = s.auditLocked()
	}()

	used := 0
	if err = s.runDecodePhaseLocked(result, &used); err != nil {
		return result, err
	}

	prefillBudget := s.maxTokensPerStep - used
	s.runPrefillPhaseLocked(result, prefillBudget, &used)
	result.UsedTokens = used
	return result, nil
}

func (s *Scheduler) runDecodePhaseLocked(result *StepResult, used *int) error {
	decoders := make([]*Request, 0)
	for _, req := range s.running {
		if req.State == StateDecoding {
			decoders = append(decoders, req)
		}
	}
	sort.Slice(decoders, func(i, j int) bool {
		if decoders[i].ArrivalSeq != decoders[j].ArrivalSeq {
			return decoders[i].ArrivalSeq < decoders[j].ArrivalSeq
		}
		return decoders[i].ID < decoders[j].ID
	})

	for _, req := range decoders {
		if *used >= s.maxTokensPerStep {
			break
		}
		current, ok := s.running[req.ID]
		if !ok || current.State != StateDecoding {
			continue
		}

		storedTokens := len(req.Prompt) + len(req.Generated)
		if storedTokens%s.cache.blockSize == 0 {
			if err := s.acquireBlocksWithPreemptionLocked(req, 1, result); err != nil {
				if errors.Is(err, ErrRequestPreempted) {
					continue
				}
				return err
			}
		}

		req.Generated = append(req.Generated, s.deterministicTokenLocked(req))
		*used++
		result.DecodeTokens[req.ID] = 1
		result.Scheduled = append(result.Scheduled, req.ID)

		if req.MaxNewTokens > 0 && len(req.Generated) >= req.MaxNewTokens {
			s.completeLocked(req, result)
		}
	}
	return nil
}

func (s *Scheduler) deterministicTokenLocked(req *Request) Token {
	return Token(1_000_000 + req.ArrivalSeq*1000 + int64(len(req.Generated)) + 1)
}

func (s *Scheduler) acquireBlocksWithPreemptionLocked(requester *Request, count int, result *StepResult) error {
	for acquired := 0; acquired < count; {
		id, err := s.cache.AllocateRunningBlock(requester.ID)
		if err == nil {
			requester.BlockIDs = append(requester.BlockIDs, id)
			acquired++
			continue
		}

		if evicted := s.cache.EvictLRU(); evicted != nil {
			result.EvictedNodeIDs = append(result.EvictedNodeIDs, evicted.ID)
			continue
		}

		victim := s.selectVictimLocked(requester.ID)
		if victim == nil {
			return fmt.Errorf("%w: requester=%s", ErrKVCapacityExhausted, requester.ID)
		}
		if victim.ID == requester.ID {
			s.preemptLocked(victim, result)
			return ErrRequestPreempted
		}
		s.preemptLocked(victim, result)
	}
	return nil
}

func (s *Scheduler) acquirePrefillBlockLocked(req *Request, result *StepResult) error {
	for {
		id, err := s.cache.AllocateRunningBlock(req.ID)
		if err == nil {
			req.BlockIDs = append(req.BlockIDs, id)
			return nil
		}
		evicted := s.cache.EvictLRU()
		if evicted == nil {
			return err
		}
		result.EvictedNodeIDs = append(result.EvictedNodeIDs, evicted.ID)
	}
}

func (s *Scheduler) oldestDecodeIDLocked() string {
	oldest := ""
	var oldestSeq int64
	for id, req := range s.running {
		if req.State != StateDecoding {
			continue
		}
		if oldest == "" || req.ArrivalSeq < oldestSeq || (req.ArrivalSeq == oldestSeq && id < oldest) {
			oldest = id
			oldestSeq = req.ArrivalSeq
		}
	}
	return oldest
}

func (s *Scheduler) selectVictimLocked(requesterID string) *Request {
	oldestDecodeID := s.oldestDecodeIDLocked()
	candidates := make([]*Request, 0, len(s.running))
	for id, req := range s.running {
		_ = requesterID
		if id == oldestDecodeID {
			continue
		}
		if req.State != StateDecoding && req.State != StatePrefilling {
			continue
		}
		candidates = append(candidates, req)
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.Priority != b.Priority {
			// Smaller Priority means lower importance and is preempted first.
			return a.Priority < b.Priority
		}
		// Equal priority: most recently arrived request is preempted first.
		if a.ArrivalSeq != b.ArrivalSeq {
			return a.ArrivalSeq > b.ArrivalSeq
		}
		return a.ID > b.ID
	})
	return candidates[0]
}

func (s *Scheduler) preemptLocked(victim *Request, result *StepResult) {
	delete(s.running, victim.ID)
	// Release the request's path lock. Shared ancestors remain locked by other requests.
	s.cache.UnlockRequest(victim.ID)
	for _, blockID := range victim.BlockIDs {
		if err := s.cache.FreeRunningBlock(blockID, victim.ID); err != nil {
			panic(err)
		}
	}
	victim.BlockIDs = nil
	victim.Generated = nil
	victim.PrefillCursor = 0
	victim.CachedTokens = 0
	victim.PreemptionCount++
	victim.setState(StateWaiting)
	result.PreemptedRequestIDs = append(result.PreemptedRequestIDs, victim.ID)
}

func (s *Scheduler) completeLocked(req *Request, result *StepResult) {
	req.setState(StateDone)
	delete(s.running, req.ID)
	s.cache.UnlockRequest(req.ID)
	for _, blockID := range req.BlockIDs {
		if err := s.cache.FreeRunningBlock(blockID, req.ID); err != nil {
			panic(err)
		}
	}
	req.BlockIDs = nil
	result.CompletedRequestIDs = append(result.CompletedRequestIDs, req.ID)
}

func (s *Scheduler) beginPrefillLocked(req *Request, result *StepResult) error {
	if req.State == StatePrefilling || req.State == StateDecoding {
		s.running[req.ID] = req
		return nil
	}

	match := s.cache.MatchPrefix(req.Prompt)
	cached := match.CachedTokens
	if cached > len(req.Prompt) {
		cached = len(req.Prompt)
	}
	if _, err := s.cache.LockPrefix(req.ID, req.Prompt[:cached]); err != nil {
		return err
	}
	req.CachedTokens = cached
	req.PrefillCursor = cached
	if cached > 0 {
		result.SkippedCachedTokens[req.ID] += cached
	}

	if cached == len(req.Prompt) {
		req.setState(StateDecoding)
	} else {
		req.setState(StatePrefilling)
	}
	s.running[req.ID] = req
	return nil
}

func (s *Scheduler) runPrefillPhaseLocked(result *StepResult, budget int, used *int) {
	ordered := s.sortedRequestsLocked(StateWaiting, StatePrefilling)
	preemptedThisStep := make(map[string]bool, len(result.PreemptedRequestIDs))
	for _, id := range result.PreemptedRequestIDs {
		preemptedThisStep[id] = true
	}

	// Requests whose entire prompt is a block-aligned cache hit become decoders
	// without spending prefill compute budget.
	candidates := make([]prefillCandidate, 0, len(ordered))
	for _, req := range ordered {
		if preemptedThisStep[req.ID] {
			continue
		}
		if req.State == StateWaiting {
			match := s.cache.MatchPrefix(req.Prompt)
			cached := match.CachedTokens
			if cached >= len(req.Prompt) {
				if err := s.beginPrefillLocked(req, result); err != nil {
					panic(err)
				}
				continue
			}
			candidates = append(candidates, prefillCandidate{req: req, remaining: len(req.Prompt) - cached})
			continue
		}
		if req.State == StatePrefilling {
			remaining := len(req.Prompt) - req.PrefillCursor
			if remaining > 0 {
				candidates = append(candidates, prefillCandidate{req: req, remaining: remaining})
			} else {
				if err := s.beginPrefillLocked(req, result); err != nil {
					panic(err)
				}
			}
		}
	}

	grants := balancedGrants(candidates, budget)
	for _, candidate := range candidates {
		req := candidate.req
		grant := grants[req.ID]
		if grant <= 0 {
			continue
		}
		if preemptedThisStep[req.ID] {
			continue
		}
		if err := s.beginPrefillLocked(req, result); err != nil {
			panic(err)
		}
		if req.State != StatePrefilling {
			continue
		}
		result.Scheduled = append(result.Scheduled, req.ID)

	requestTokens:
		for processed := 0; processed < grant; processed++ {
			if req.PrefillCursor >= len(req.Prompt) {
				req.setState(StateDecoding)
				break
			}
			if req.PrefillCursor%s.cache.blockSize == 0 {
				err := s.acquireBlocksWithPreemptionLocked(req, 1, result)
				for _, id := range result.PreemptedRequestIDs {
					preemptedThisStep[id] = true
				}
				if err != nil {
					if errors.Is(err, ErrRequestPreempted) {
						break requestTokens
					}
					result.BlockLimitedRequestIDs = append(result.BlockLimitedRequestIDs, req.ID)
					return
				}
			}

			req.PrefillCursor++
			req.ComputedPromptTokens++
			*used++
			result.PrefillTokens[req.ID]++

			if req.PrefillCursor == len(req.Prompt) {
				req.setState(StateDecoding)
				break
			}
		}
	}
}

// Run executes steps until all requests finish or maxSteps is reached.
func (s *Scheduler) Run(maxSteps int) ([]*StepResult, error) {
	results := make([]*StepResult, 0, maxSteps)
	for step := 0; step < maxSteps; step++ {
		result, err := s.Step()
		if err != nil {
			return results, err
		}
		results = append(results, result)
		if s.AllDone() {
			return results, nil
		}
	}
	return results, fmt.Errorf("radixkv: scheduler did not finish within %d steps", maxSteps)
}

// Conservation audits physical blocks together with scheduler request ownership.
func (s *Scheduler) Conservation() ConservationReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.auditLocked()
}

func (s *Scheduler) auditLocked() ConservationReport {
	s.cache.mu.Lock()
	defer s.cache.mu.Unlock()

	report := s.cache.conservationLocked()
	seenRunning := make(map[BlockID]string)

	for id, req := range s.requests {
		active := req.State == StatePrefilling || req.State == StateDecoding
		if !active && len(req.BlockIDs) != 0 {
			report.Problems = append(report.Problems,
				fmt.Sprintf("inactive request %s retains %d blocks", id, len(req.BlockIDs)))
			report.Conserved = false
		}
		for _, blockID := range req.BlockIDs {
			if owner, duplicated := seenRunning[blockID]; duplicated {
				report.Problems = append(report.Problems,
					fmt.Sprintf("block %d owned by both %s and %s", blockID, owner, id))
				report.Conserved = false
			}
			seenRunning[blockID] = id
			if err := s.cache.pool.checkRunningOwner(blockID, id); err != nil {
				report.Problems = append(report.Problems, err.Error())
				report.Conserved = false
			}
		}
	}

	poolRunning := s.cache.pool.runningIDs()
	if len(poolRunning) != len(seenRunning) {
		report.Problems = append(report.Problems,
			fmt.Sprintf("pool running blocks %d != request block table count %d", len(poolRunning), len(seenRunning)))
		report.Conserved = false
	}
	for _, blockID := range poolRunning {
		if _, ok := seenRunning[blockID]; !ok {
			report.Problems = append(report.Problems, fmt.Sprintf("pool running block %d has no request owner", blockID))
			report.Conserved = false
		}
	}
	report.RunningBlocks = len(poolRunning)
	report.Sum = report.TreeBlocks + report.FreeBlocks + report.RunningBlocks
	if report.Sum != report.TotalBlocks {
		report.Conserved = false
	}
	return report
}
