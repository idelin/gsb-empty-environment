package privacy

import (
	"context"
	"math"
	"math/rand"
	"sync"
)

// ComputeFunc evaluates the true (noiseless) count for a query key.
type ComputeFunc func(ctx context.Context, key string) (float64, error)

// Query describes one count request. Every group in Groups shares the cost:
// Epsilon is reserved from each of them.
type Query struct {
	Key     string
	Groups  []string
	Epsilon float64
}

// Service executes count queries against the ledger. Concurrent requests with
// the same query key share a single flight: one computation, one reservation,
// one noise draw, one published result.
type Service struct {
	ledger      *Ledger
	compute     ComputeFunc
	sensitivity float64

	rngMu sync.Mutex
	rng   *rand.Rand

	mu      sync.Mutex
	flights map[string]*flight

	// AfterPublish, if set, runs after the result is durably settled but
	// before waiters are notified. Returning true simulates a crash: the
	// response is never delivered. Used for fault injection in tests.
	AfterPublish func() bool
}

type flight struct {
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	waiters int
	value   float64
	err     error
}

func NewService(ledger *Ledger, compute ComputeFunc, sensitivity float64, seed int64) *Service {
	return &Service{
		ledger:      ledger,
		compute:     compute,
		sensitivity: sensitivity,
		rng:         rand.New(rand.NewSource(seed)),
		flights:     make(map[string]*flight),
	}
}

// Query returns the noisy count for q. A key that was already published —
// including by a previous process before a crash — is served from the ledger
// with no additional budget charge and the identical noise.
func (s *Service) Query(ctx context.Context, q Query) (float64, error) {
	if v, ok := s.ledger.Result(q.Key); ok {
		return v, nil
	}

	s.mu.Lock()
	if f, ok := s.flights[q.Key]; ok {
		f.waiters++
		s.mu.Unlock()
		return s.wait(ctx, q.Key, f)
	}
	fctx, cancel := context.WithCancel(context.Background())
	f := &flight{ctx: fctx, cancel: cancel, done: make(chan struct{}), waiters: 1}
	s.flights[q.Key] = f
	s.mu.Unlock()

	go s.run(q, f)
	return s.wait(ctx, q.Key, f)
}

func (s *Service) wait(ctx context.Context, key string, f *flight) (float64, error) {
	select {
	case <-f.done:
		return f.value, f.err
	case <-ctx.Done():
		s.leave(f)
		return 0, ctx.Err()
	}
}

// leave detaches a cancelled waiter. When the last waiter leaves before
// publication, the flight's computation is cancelled; if the result was
// already published the spend stands regardless.
func (s *Service) leave(f *flight) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f.waiters--
	if f.waiters == 0 {
		f.cancel()
	}
}

func (s *Service) run(q Query, f *flight) {
	costs := make(map[string]float64, len(q.Groups))
	for _, g := range q.Groups {
		costs[g] += q.Epsilon
	}
	if err := s.ledger.Reserve(q.Key, costs); err != nil {
		if err == errAlreadySettled {
			v, _ := s.ledger.Result(q.Key)
			s.finish(q.Key, f, v, nil)
			return
		}
		s.finish(q.Key, f, 0, err)
		return
	}
	trueCount, err := s.compute(f.ctx, q.Key)
	if err != nil {
		s.ledger.Release(q.Key)
		s.finish(q.Key, f, 0, err)
		return
	}
	noisy := trueCount + s.laplace(q.Epsilon)
	if err := s.ledger.Settle(q.Key, noisy); err != nil {
		s.finish(q.Key, f, 0, err)
		return
	}
	if s.AfterPublish != nil && s.AfterPublish() {
		return // simulated crash: published but never responded
	}
	s.finish(q.Key, f, noisy, nil)
}

func (s *Service) finish(key string, f *flight, value float64, err error) {
	s.mu.Lock()
	delete(s.flights, key)
	s.mu.Unlock()
	f.cancel()
	f.value, f.err = value, err
	close(f.done)
}

// laplace draws Laplace(sensitivity/epsilon) noise. Called once per flight.
func (s *Service) laplace(epsilon float64) float64 {
	s.rngMu.Lock()
	u := s.rng.Float64() - 0.5
	s.rngMu.Unlock()
	b := s.sensitivity / epsilon
	return -b * math.Copysign(math.Log1p(-2*math.Abs(u)), u)
}
