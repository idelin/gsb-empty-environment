package netsec

import "sync"

// publisherState is the per (token, publisher) admission state.
type publisherState struct {
	prefix     [4]byte
	maxCounter uint32
	seen       bool
}

type stateKey struct {
	tokenId   uint32
	publisher string
}

// Gate admits UADP NetworkMessages on the subscriber side based on
// their security header. It accepts only the current and the previous
// security token (one generation retained after key rotation) and
// enforces a strictly increasing nonce counter with a stable random
// prefix per token lifetime and publisher.
//
// The gate never advances state for messages whose signature or
// decryption verification failed; callers must run Admit only after
// cryptographic verification succeeds, or use Check to validate
// without committing.
//
// Gate is safe for concurrent use.
type Gate struct {
	mu        sync.Mutex
	current   uint32
	previous  uint32
	hasTokens bool
	states    map[stateKey]*publisherState
}

// NewGate creates a Gate accepting currentTokenId and previousTokenId.
// After a key rotation the caller is expected to construct (or rotate
// the gate to) the new pair, keeping exactly one retired generation.
func NewGate(currentTokenId, previousTokenId uint32) *Gate {
	g := &Gate{states: make(map[stateKey]*publisherState)}
	g.SetTokens(currentTokenId, previousTokenId)
	return g
}

// SetTokens installs the accepted token pair. State for tokens that
// remain accepted is preserved; state for tokens that fell out of the
// accepted window is discarded.
func (g *Gate) SetTokens(currentTokenId, previousTokenId uint32) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.current = currentTokenId
	g.previous = previousTokenId
	g.hasTokens = true
	for k := range g.states {
		if k.tokenId != currentTokenId && k.tokenId != previousTokenId {
			delete(g.states, k)
		}
	}
}

// Rotate advances the window: the old current token becomes the
// previous one and newTokenId becomes current. State for the token
// that falls out of the window is discarded.
func (g *Gate) Rotate(newTokenId uint32) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.previous, g.current = g.current, newTokenId
	for k := range g.states {
		if k.tokenId != g.current && k.tokenId != g.previous {
			delete(g.states, k)
		}
	}
}

// Tokens returns the currently accepted (current, previous) token ids.
func (g *Gate) Tokens() (current, previous uint32) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.current, g.previous
}

// checkToken validates the token id against the accepted window.
// Caller must hold g.mu.
func (g *Gate) checkToken(tokenId uint32) error {
	switch tokenId {
	case g.current, g.previous:
		return nil
	}
	if tokenId < g.previous {
		return wrapf(ErrTokenTooOld,
			"token id %d predates previous token %d", tokenId, g.previous)
	}
	return wrapf(ErrTokenRejected,
		"token id %d is neither current %d nor previous %d",
		tokenId, g.current, g.previous)
}

// check validates hdr for publisher without mutating any state.
// Caller must hold g.mu.
func (g *Gate) check(publisher string, hdr SecurityHeader) error {
	if err := g.checkToken(hdr.SecurityTokenId); err != nil {
		return err
	}
	k := stateKey{tokenId: hdr.SecurityTokenId, publisher: publisher}
	st, ok := g.states[k]
	if !ok || !st.seen {
		return nil
	}
	if hdr.NoncePrefix() != st.prefix {
		return wrapf(ErrNoncePrefixChanged,
			"publisher %q token %d: prefix changed within token lifetime",
			publisher, hdr.SecurityTokenId)
	}
	if c := hdr.NonceCounter(); c <= st.maxCounter {
		return wrapf(ErrNonceReplay,
			"publisher %q token %d: counter %d not greater than max seen %d",
			publisher, hdr.SecurityTokenId, c, st.maxCounter)
	}
	return nil
}

// commit records hdr as the newest accepted message for publisher.
// Caller must hold g.mu and must have validated hdr via check.
func (g *Gate) commit(publisher string, hdr SecurityHeader) {
	k := stateKey{tokenId: hdr.SecurityTokenId, publisher: publisher}
	st, ok := g.states[k]
	if !ok {
		st = &publisherState{}
		g.states[k] = st
	}
	st.prefix = hdr.NoncePrefix()
	st.maxCounter = hdr.NonceCounter()
	st.seen = true
}

// Check validates hdr for publisher without advancing any state. It is
// intended for callers that verify the message cryptographically
// afterwards and then call Commit on success.
func (g *Gate) Check(publisher string, hdr SecurityHeader) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.check(publisher, hdr)
}

// Commit records hdr as accepted for publisher. It must only be called
// after Check succeeded and the message passed signature and
// decryption verification.
func (g *Gate) Commit(publisher string, hdr SecurityHeader) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.commit(publisher, hdr)
}

// Admit validates hdr for publisher and, only if verified is true,
// advances the admission state. When verified is false (signature or
// decryption failed) the message is rejected with
// ErrVerificationFailed and no state is touched.
func (g *Gate) Admit(publisher string, hdr SecurityHeader, verified bool) error {
	if !verified {
		return wrapf(ErrVerificationFailed,
			"publisher %q token %d counter %d",
			publisher, hdr.SecurityTokenId, hdr.NonceCounter())
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.check(publisher, hdr); err != nil {
		return err
	}
	g.commit(publisher, hdr)
	return nil
}
