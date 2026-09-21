package bridge

import "sync"

// Direction selects which way an endpoint converts.
type Direction int

const (
	AtoB Direction = iota
	BtoA
)

// Session is a negotiated bridge session bound to a plan digest. Plans can
// be hot-updated: publishing installs a new epoch, while frames already in
// flight keep the plan of the epoch they started in.
type Session struct {
	mu    sync.Mutex
	epoch uint64
	pair  *PlanPair
}

// Negotiate compiles the bidirectional plan for two schemas. It fails when
// either direction has an unrepresentable field, e.g. a required field the
// other side cannot provide and no migration rule supplies.
func Negotiate(a, b *Schema) (*Session, error) {
	pair, err := CompilePair(a, b)
	if err != nil {
		return nil, err
	}
	return &Session{pair: pair}, nil
}

// Epoch is the current plan generation.
func (s *Session) Epoch() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.epoch
}

// PlanPair returns the plan pair of the current epoch.
func (s *Session) PlanPair() *PlanPair {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pair
}

// Digest is the plan summary this session is bound to.
func (s *Session) Digest() [32]byte { return s.PlanPair().Digest() }

// Publish hot-swaps the plan pair and starts a new epoch. Frames that
// already started keep converting under the previous plan.
func (s *Session) Publish(pair *PlanPair) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pair = pair
	s.epoch++
}

func (s *Session) current() *PlanPair {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pair
}

// Endpoint converts a stream of frames in one direction. It binds the
// session's plan when each frame starts, so a hot update mid-frame does not
// affect the in-flight frame. A conversion failure publishes nothing and
// poisons the endpoint.
type Endpoint struct {
	sess   *Session
	dir    Direction
	dec    *FrameDecoder
	bound  *Plan
	failed error
}

func NewEndpoint(s *Session, dir Direction, maxFrame uint32) *Endpoint {
	e := &Endpoint{sess: s, dir: dir, dec: NewFrameDecoder(maxFrame)}
	e.dec.OnFrameStart = e.bind
	return e
}

func (e *Endpoint) bind() {
	pair := e.sess.current()
	if e.dir == AtoB {
		e.bound = pair.AB
	} else {
		e.bound = pair.BA
	}
}

// Feed consumes an arbitrary chunk and returns fully converted frames
// (each already length-prefixed). Conversion is atomic per frame: a failure
// returns an error, publishes no half-converted frame, and poisons the
// endpoint for all subsequent feeds.
func (e *Endpoint) Feed(chunk []byte) ([][]byte, error) {
	if e.failed != nil {
		return nil, e.failed
	}
	frames, err := e.dec.Feed(chunk)
	if err != nil {
		e.failed = err
		return nil, err
	}
	var out [][]byte
	for _, f := range frames {
		converted, _, err := ConvertFrame(e.bound, f)
		if err != nil {
			e.failed = err
			return nil, err
		}
		out = append(out, EncodeFrame(converted))
	}
	return out, nil
}
