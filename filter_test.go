package netsec

import (
	"bytes"
	"crypto/elliptic"
	"errors"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Unix(1700000000, 0)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func mustGroup(t *testing.T, id GroupID) *Group {
	t.Helper()
	g, ok := LookupGroup(id)
	if !ok {
		t.Fatalf("group %d is not supported", id)
	}
	return g
}

func scalarFor(t *testing.T, g *Group, v int64) []byte {
	t.Helper()
	out, err := g.MarshalScalar(big.NewInt(v))
	if err != nil {
		t.Fatalf("marshal scalar %d: %v", v, err)
	}
	return out
}

func scalarBig(t *testing.T, g *Group, v *big.Int) []byte {
	t.Helper()
	out := make([]byte, g.ScalarSize())
	v.FillBytes(out)
	return out
}

func pointFor(t *testing.T, g *Group, v int64) []byte {
	t.Helper()
	out, err := g.ScalarBaseMult(scalarFor(t, g, v))
	if err != nil {
		t.Fatalf("scalar base mult %d: %v", v, err)
	}
	return out
}

func compressedPoint(t *testing.T, g *Group, raw []byte) []byte {
	t.Helper()
	x, y := elliptic.Unmarshal(g.curve, append([]byte{4}, raw...))
	if x == nil || y == nil {
		t.Fatalf("unmarshal point")
	}
	return elliptic.MarshalCompressed(g.curve, x, y)
}

func macAddress(i int) net.HardwareAddr {
	return net.HardwareAddr{0x02, 0x11, 0x22, byte(i >> 16), byte(i >> 8), byte(i)}
}

func TestGroupScalarBounds(t *testing.T) {
	for _, id := range SupportedGroups() {
		g := mustGroup(t, id)
		n := g.Order()
		nMinusOne := new(big.Int).Sub(n, big.NewInt(1))

		tests := []struct {
			name    string
			scalar  []byte
			wantErr error
		}{
			{"zero", scalarBig(t, g, big.NewInt(0)), ErrScalarRange},
			{"one", scalarBig(t, g, big.NewInt(1)), ErrScalarRange},
			{"two", scalarBig(t, g, big.NewInt(2)), nil},
			{"r-1", scalarBig(t, g, nMinusOne), nil},
			{"r", scalarBig(t, g, n), ErrScalarRange},
			{"short", scalarFor(t, g, 2)[1:], ErrScalarEncoding},
			{"empty", nil, ErrScalarEncoding},
		}
		for _, tc := range tests {
			t.Run(g.Name()+"/"+tc.name, func(t *testing.T) {
				err := g.ValidateScalar(tc.scalar)
				if tc.wantErr == nil {
					if err != nil {
						t.Fatalf("ValidateScalar() = %v, want nil", err)
					}
					return
				}
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("ValidateScalar() = %v, want %v", err, tc.wantErr)
				}
			})
		}
	}
}

func TestGroupElementValidation(t *testing.T) {
	g := mustGroup(t, Group19)
	valid := pointFor(t, g, 3)
	own := pointFor(t, g, 7)

	t.Run("raw and SEC1 encodings", func(t *testing.T) {
		if err := g.ValidateElement(valid); err != nil {
			t.Fatalf("raw element rejected: %v", err)
		}
		uncompressed := append([]byte{4}, valid...)
		if err := g.ValidateElement(uncompressed); err != nil {
			t.Fatalf("SEC1 element rejected: %v", err)
		}
		if err := g.ValidateElement(compressedPoint(t, g, valid)); err != nil {
			t.Fatalf("compressed element rejected: %v", err)
		}
	})

	t.Run("infinity", func(t *testing.T) {
		if err := g.ValidateElement(make([]byte, g.ElementSize())); !errors.Is(err, ErrElementInfinity) {
			t.Fatalf("raw infinity error = %v", err)
		}
		if err := g.ValidateElement([]byte{0}); !errors.Is(err, ErrElementInfinity) {
			t.Fatalf("SEC1 infinity error = %v", err)
		}
	})

	t.Run("off curve and malformed", func(t *testing.T) {
		offCurve := make([]byte, g.ElementSize())
		offCurve[g.CoordinateSize()-1] = 1 // x=1
		offCurve[len(offCurve)-1] = 0      // y=0
		if err := g.ValidateElement(offCurve); !errors.Is(err, ErrElementNotOnCurve) {
			t.Fatalf("off-curve error = %v", err)
		}
		if err := g.ValidateElement([]byte{4}); !errors.Is(err, ErrElementEncoding) {
			t.Fatalf("malformed error = %v", err)
		}
	})

	t.Run("reflection", func(t *testing.T) {
		err := g.ValidateCommit(scalarFor(t, g, 9), own, own)
		if !errors.Is(err, ErrReflection) {
			t.Fatalf("ValidateCommit() = %v, want reflection", err)
		}
		if err := g.ValidateCommit(scalarFor(t, g, 9), valid, own); err != nil {
			t.Fatalf("non-reflected element rejected: %v", err)
		}
	})
}

func TestTokenRotationAndBinding(t *testing.T) {
	clock := newFakeClock()
	manager, err := NewTokenManager([]byte("test-secret-material"), time.Minute,
		WithClock(clock.now))
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}

	macA := macAddress(1)
	macB := macAddress(2)
	tokenA := manager.Token(macA, Group19)
	tokenB := manager.Token(macA, Group19)
	if len(tokenA) != TokenSize {
		t.Fatalf("token length = %d, want %d", len(tokenA), TokenSize)
	}
	if bytes.Equal(tokenA, tokenB) {
		t.Fatal("tokens must have fresh nonces")
	}
	if !manager.Verify(macA, Group19, tokenA) {
		t.Fatal("token was not accepted for its station and group")
	}
	if manager.Verify(macB, Group19, tokenA) {
		t.Fatal("token was accepted for another station")
	}
	if manager.Verify(macA, Group20, tokenA) {
		t.Fatal("token was accepted for another group")
	}
	tampered := append([]byte(nil), tokenA...)
	tampered[len(tampered)-1] ^= 1
	if manager.Verify(macA, Group19, tampered) {
		t.Fatal("tampered token was accepted")
	}

	clock.advance(2 * time.Minute)
	if manager.Verify(macA, Group19, tokenA) {
		t.Fatal("token survived key rotation without overlap")
	}

	overlapClock := newFakeClock()
	overlapping, err := NewTokenManager([]byte("overlap-secret"), time.Minute,
		WithClock(overlapClock.now), WithOverlap(30*time.Second))
	if err != nil {
		t.Fatalf("NewTokenManager overlap: %v", err)
	}
	oldToken := overlapping.Token(macA, Group19)
	overlapClock.advance(65 * time.Second)
	if !overlapping.Verify(macA, Group19, oldToken) {
		t.Fatal("token was rejected inside the configured overlap")
	}
	overlapClock.advance(20 * time.Second)
	if overlapping.Verify(macA, Group19, oldToken) {
		t.Fatal("token outlived the configured overlap")
	}
}

func TestFloodOnlyReceivesTokens(t *testing.T) {
	clock := newFakeClock()
	g := mustGroup(t, Group19)
	own := pointFor(t, g, 100)
	filter, err := NewFilter(Config{
		MaxIncomplete:           4,
		MaxIncompletePerStation: 1,
		MaxTransactions:         8,
		TokenSecret:             []byte("flood-secret"),
		TokenRotation:           time.Hour,
		Clock:                   clock.now,
		OwnElements:             map[GroupID][]byte{Group19: own},
	})
	if err != nil {
		t.Fatalf("NewFilter: %v", err)
	}

	for i := 0; i < 4; i++ {
		decision := filter.HandleCommit(CommitRequest{
			StationMAC: macAddress(i),
			Group:      Group19,
			Scalar:     scalarFor(t, g, int64(2+i)),
			Element:    pointFor(t, g, int64(2+i)),
		})
		if !decision.Allowed() {
			t.Fatalf("initial commit %d denied: %v", i, decision)
		}
	}
	if got := filter.Incomplete(); got != 4 {
		t.Fatalf("incomplete = %d, want 4", got)
	}
	checksBefore := filter.Stats().CurveValidations
	if checksBefore != 4 {
		t.Fatalf("curve validations = %d, want 4", checksBefore)
	}

	badScalar := bytes.Repeat([]byte{0xff}, g.ScalarSize())
	badElement := bytes.Repeat([]byte{0xab}, g.ElementSize())
	for i := 100; i < 228; i++ {
		decision := filter.HandleCommit(CommitRequest{
			StationMAC: macAddress(i),
			Group:      Group19,
			Scalar:     badScalar,
			Element:    badElement,
		})
		if decision.Action != ActionSendToken {
			t.Fatalf("flood commit %d action = %v, want send-token", i, decision)
		}
		if len(decision.Token) != TokenSize {
			t.Fatalf("flood token length = %d, want %d", len(decision.Token), TokenSize)
		}
		if !errors.Is(decision.Reason, ErrTransactionThreshold) {
			t.Fatalf("flood reason = %v, want threshold", decision.Reason)
		}
		if !filter.VerifyToken(macAddress(i), Group19, decision.Token) {
			t.Fatalf("issued flood token %d does not verify", i)
		}
	}

	after := filter.Stats()
	if after.CurveValidations != checksBefore {
		t.Fatalf("flood performed curve work: before=%d after=%d", checksBefore, after.CurveValidations)
	}
	if after.TokensIssued != 128 {
		t.Fatalf("tokens issued = %d, want 128", after.TokensIssued)
	}
	if got := filter.Incomplete(); got != 4 {
		t.Fatalf("flood changed incomplete count to %d", got)
	}

	filter.Complete(macAddress(0), Group19)
	if got := filter.Incomplete(); got != 3 {
		t.Fatalf("incomplete after completion = %d, want 3", got)
	}
	decision := filter.HandleCommit(CommitRequest{
		StationMAC: macAddress(300),
		Group:      Group19,
		Scalar:     scalarFor(t, g, 12),
		Element:    pointFor(t, g, 12),
	})
	if !decision.Allowed() {
		t.Fatalf("commit after freeing a slot denied: %v", decision)
	}
}

func TestInvalidFloodCannotRefreshFreeCurveWork(t *testing.T) {
	clock := newFakeClock()
	g := mustGroup(t, Group19)
	filter, err := NewFilter(Config{
		MaxIncomplete: 4,
		FailureTTL:    time.Hour,
		TokenSecret:   []byte("invalid-flood-secret"),
		TokenRotation: time.Hour,
		Clock:         clock.now,
		OwnElements:   map[GroupID][]byte{Group19: pointFor(t, g, 100)},
	})
	if err != nil {
		t.Fatalf("NewFilter: %v", err)
	}

	offCurve := make([]byte, g.ElementSize())
	offCurve[g.CoordinateSize()-1] = 1 // x=1, y=0 is not a curve point
	validScalar := scalarFor(t, g, 9)
	for i := 0; i < 4; i++ {
		decision := filter.HandleCommit(CommitRequest{
			StationMAC: macAddress(i),
			Group:      Group19,
			Scalar:     validScalar,
			Element:    offCurve,
		})
		if decision.Action != ActionDrop || !errors.Is(decision.Reason, ErrElementNotOnCurve) {
			t.Fatalf("invalid commit %d = %v", i, decision)
		}
	}
	checks := filter.Stats().CurveValidations
	if checks != 4 {
		t.Fatalf("curve validations = %d, want 4", checks)
	}
	if got := filter.Pressure(); got != 4 {
		t.Fatalf("pressure = %d, want 4", got)
	}

	gated := filter.HandleCommit(CommitRequest{
		StationMAC: macAddress(4),
		Group:      Group19,
		Scalar:     validScalar,
		Element:    offCurve,
	})
	if gated.Action != ActionSendToken || !errors.Is(gated.Reason, ErrTransactionThreshold) {
		t.Fatalf("invalid flood was not gated: %v", gated)
	}
	if got := filter.Stats().CurveValidations; got != checks {
		t.Fatalf("gated commit performed curve work: %d -> %d", checks, got)
	}

	decision := filter.HandleCommit(CommitRequest{
		StationMAC: macAddress(4),
		Group:      Group19,
		Scalar:     validScalar,
		Element:    pointFor(t, g, 9),
		Token:      gated.Token,
	})
	if !decision.Allowed() {
		t.Fatalf("valid token did not bypass failed-attempt pressure: %v", decision)
	}
}

func TestTokenReplayAndBinding(t *testing.T) {
	clock := newFakeClock()
	g := mustGroup(t, Group19)
	filter, err := NewFilter(Config{
		MaxIncomplete:           2,
		MaxIncompletePerStation: 2,
		MaxTransactions:         8,
		TokenSecret:             []byte("replay-secret"),
		TokenRotation:           time.Hour,
		Clock:                   clock.now,
		OwnElements:             map[GroupID][]byte{Group19: pointFor(t, g, 100)},
	})
	if err != nil {
		t.Fatalf("NewFilter: %v", err)
	}

	for i := 0; i < 2; i++ {
		decision := filter.HandleCommit(CommitRequest{
			StationMAC: macAddress(i),
			Group:      Group19,
			Scalar:     scalarFor(t, g, int64(2+i)),
			Element:    pointFor(t, g, int64(2+i)),
		})
		if !decision.Allowed() {
			t.Fatalf("fill commit %d denied: %v", i, decision)
		}
	}

	station := macAddress(50)
	token, err := filter.AntiCloggingToken(station, Group19)
	if err != nil {
		t.Fatalf("AntiCloggingToken: %v", err)
	}
	commit := CommitRequest{
		StationMAC: station,
		Group:      Group19,
		Scalar:     scalarFor(t, g, 5),
		Element:    pointFor(t, g, 5),
		Token:      token,
	}
	decision := filter.HandleCommit(commit)
	if !decision.Allowed() {
		t.Fatalf("valid token denied: %v", decision)
	}

	otherToken, err := filter.AntiCloggingToken(station, Group19)
	if err != nil {
		t.Fatalf("AntiCloggingToken for second commit: %v", err)
	}
	concurrent := commit
	concurrent.Scalar = scalarFor(t, g, 6)
	concurrent.Element = pointFor(t, g, 6)
	concurrent.Token = otherToken
	if got := filter.HandleCommit(concurrent); got.Action != ActionDrop || !errors.Is(got.Reason, ErrTransactionInProgress) {
		t.Fatalf("second active commit was not rejected: %v", got)
	}
	if got := filter.Incomplete(); got != 3 {
		t.Fatalf("incomplete after rejected second commit = %d, want 3", got)
	}

	checks := filter.Stats().CurveValidations
	duplicate := filter.HandleCommit(commit)
	if !duplicate.Allowed() || !duplicate.Duplicate {
		t.Fatalf("retransmission decision = %+v, want duplicate allow", duplicate)
	}
	if got := filter.Stats().CurveValidations; got != checks {
		t.Fatalf("duplicate retransmission repeated curve work: %d -> %d", checks, got)
	}

	if !filter.Complete(station, Group19) {
		t.Fatal("Complete did not find the transaction")
	}

	replay := commit
	replay.Scalar = scalarFor(t, g, 6)
	replay.Element = pointFor(t, g, 6)
	decision = filter.HandleCommit(replay)
	if decision.Action != ActionSendToken {
		t.Fatalf("replayed token action = %v, want send-token", decision)
	}
	if !errors.Is(decision.Reason, ErrTokenReplay) {
		t.Fatalf("replay reason = %v, want token replay", decision.Reason)
	}
	if len(decision.Token) == 0 || bytes.Equal(decision.Token, token) {
		t.Fatal("replay challenge did not issue a fresh token")
	}

	replay.Token = decision.Token
	decision = filter.HandleCommit(replay)
	if !decision.Allowed() {
		t.Fatalf("fresh token after replay was denied: %v", decision)
	}
	filter.Complete(station, Group19)

	decision = filter.HandleCommit(CommitRequest{
		StationMAC: macAddress(51),
		Group:      Group19,
		Scalar:     scalarFor(t, g, 7),
		Element:    pointFor(t, g, 7),
		Token:      token,
	})
	if decision.Action != ActionSendToken || !errors.Is(decision.Reason, ErrTokenInvalid) {
		t.Fatalf("token used for another station: %v", decision)
	}

	decision = filter.HandleCommit(CommitRequest{
		StationMAC: station,
		Group:      Group20,
		Scalar:     scalarFor(t, mustGroup(t, Group20), 7),
		Element:    pointFor(t, mustGroup(t, Group20), 7),
		Token:      token,
	})
	if decision.Action != ActionSendToken || !errors.Is(decision.Reason, ErrTokenInvalid) {
		t.Fatalf("token used for another group: %v", decision)
	}
}

func TestFilterTokenRotationRejectsOldToken(t *testing.T) {
	clock := newFakeClock()
	g := mustGroup(t, Group19)
	filter, err := NewFilter(Config{
		MaxIncomplete: 1,
		TokenSecret:   []byte("rotation-secret"),
		TokenRotation: 30 * time.Second,
		Clock:         clock.now,
		OwnElements:   map[GroupID][]byte{Group19: pointFor(t, g, 100)},
	})
	if err != nil {
		t.Fatalf("NewFilter: %v", err)
	}
	decision := filter.HandleCommit(CommitRequest{
		StationMAC: macAddress(1),
		Group:      Group19,
		Scalar:     scalarFor(t, g, 2),
		Element:    pointFor(t, g, 2),
	})
	if !decision.Allowed() {
		t.Fatalf("fill commit denied: %v", decision)
	}
	token, err := filter.AntiCloggingToken(macAddress(2), Group19)
	if err != nil {
		t.Fatalf("AntiCloggingToken: %v", err)
	}
	checks := filter.Stats().CurveValidations
	clock.advance(2 * time.Minute)
	decision = filter.HandleCommit(CommitRequest{
		StationMAC: macAddress(2),
		Group:      Group19,
		Scalar:     scalarFor(t, g, 3),
		Element:    pointFor(t, g, 3),
		Token:      token,
	})
	if decision.Action != ActionSendToken || !errors.Is(decision.Reason, ErrTokenInvalid) {
		t.Fatalf("stale token decision = %v", decision)
	}
	if got := filter.Stats().CurveValidations; got != checks {
		t.Fatalf("stale token performed curve work: %d -> %d", checks, got)
	}
}

func TestFailurePressureExpires(t *testing.T) {
	clock := newFakeClock()
	g := mustGroup(t, Group19)
	filter, err := NewFilter(Config{
		MaxIncomplete: 4,
		FailureTTL:    10 * time.Second,
		TokenSecret:   []byte("failure-ttl-secret"),
		TokenRotation: time.Hour,
		Clock:         clock.now,
		OwnElements:   map[GroupID][]byte{Group19: pointFor(t, g, 100)},
	})
	if err != nil {
		t.Fatalf("NewFilter: %v", err)
	}
	offCurve := make([]byte, g.ElementSize())
	offCurve[g.CoordinateSize()-1] = 1
	for i := 0; i < 4; i++ {
		filter.HandleCommit(CommitRequest{
			StationMAC: macAddress(i),
			Group:      Group19,
			Scalar:     scalarFor(t, g, 9),
			Element:    offCurve,
		})
	}
	if got := filter.Pressure(); got != 4 {
		t.Fatalf("pressure = %d, want 4", got)
	}
	clock.advance(11 * time.Second)
	if got := filter.Pressure(); got != 0 {
		t.Fatalf("pressure after expiry = %d, want 0", got)
	}
	decision := filter.HandleCommit(CommitRequest{
		StationMAC: macAddress(10),
		Group:      Group19,
		Scalar:     scalarFor(t, g, 9),
		Element:    pointFor(t, g, 9),
	})
	if !decision.Allowed() {
		t.Fatalf("valid commit after pressure expiry denied: %v", decision)
	}
}

func TestReflectionRejected(t *testing.T) {
	g := mustGroup(t, Group19)
	own := pointFor(t, g, 7)
	filter, err := NewFilter(Config{
		MaxIncomplete: 4,
		TokenSecret:   []byte("reflection-secret"),
		TokenRotation: time.Hour,
		OwnElements:   map[GroupID][]byte{Group19: own},
	})
	if err != nil {
		t.Fatalf("NewFilter: %v", err)
	}

	for _, element := range [][]byte{own, compressedPoint(t, g, own)} {
		decision := filter.HandleCommit(CommitRequest{
			StationMAC: macAddress(1),
			Group:      Group19,
			Scalar:     scalarFor(t, g, 9),
			Element:    element,
		})
		if decision.Action != ActionDrop || !errors.Is(decision.Reason, ErrReflection) {
			t.Fatalf("reflected element decision = %v", decision)
		}
	}
	if got := filter.Incomplete(); got != 0 {
		t.Fatalf("reflection created %d transactions", got)
	}

	dynamicOwn := pointFor(t, g, 11)
	decision := filter.HandleCommit(CommitRequest{
		StationMAC: macAddress(2),
		Group:      Group19,
		Scalar:     scalarFor(t, g, 9),
		Element:    dynamicOwn,
		APElement:  dynamicOwn,
	})
	if decision.Action != ActionDrop || !errors.Is(decision.Reason, ErrReflection) {
		t.Fatalf("per-exchange reflection decision = %v", decision)
	}

	decision = filter.HandleCommit(CommitRequest{
		StationMAC: macAddress(3),
		Group:      Group19,
		Scalar:     scalarFor(t, g, 9),
		Element:    pointFor(t, g, 9),
	})
	if !decision.Allowed() {
		t.Fatalf("legitimate commit after reflection denied: %v", decision)
	}
}

func TestLegitimateHandshakeAllGroups(t *testing.T) {
	for _, id := range SupportedGroups() {
		g := mustGroup(t, id)
		filter, err := NewFilter(Config{
			MaxIncomplete: 4,
			TokenSecret:   []byte("handshake-secret"),
			TokenRotation: time.Hour,
			OwnElements:   map[GroupID][]byte{id: pointFor(t, g, 17)},
		})
		if err != nil {
			t.Fatalf("group %d: NewFilter: %v", id, err)
		}
		decision := filter.HandleCommit(CommitRequest{
			StationMAC: macAddress(10),
			Group:      id,
			Scalar:     scalarFor(t, g, 23),
			Element:    pointFor(t, g, 23),
		})
		if !decision.Allowed() || decision.Duplicate {
			t.Fatalf("group %d handshake denied: %v", id, decision)
		}
		if !filter.Complete(macAddress(10), id) {
			t.Fatalf("group %d Complete failed", id)
		}
		if got := filter.Incomplete(); got != 0 {
			t.Fatalf("group %d incomplete after completion = %d", id, got)
		}
	}
}

func TestConcurrentFloodIsBounded(t *testing.T) {
	g := mustGroup(t, Group19)
	filter, err := NewFilter(Config{
		MaxIncomplete:           8,
		MaxIncompletePerStation: 1,
		MaxTransactions:         16,
		TokenSecret:             []byte("concurrent-secret"),
		TokenRotation:           time.Hour,
		OwnElements:             map[GroupID][]byte{Group19: pointFor(t, g, 100)},
	})
	if err != nil {
		t.Fatalf("NewFilter: %v", err)
	}

	scalar := scalarFor(t, g, 2)
	element := pointFor(t, g, 2)
	var wg sync.WaitGroup
	decisions := make(chan Decision, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			decisions <- filter.HandleCommit(CommitRequest{
				StationMAC: macAddress(i),
				Group:      Group19,
				Scalar:     scalar,
				Element:    element,
			})
		}(i)
	}
	wg.Wait()
	close(decisions)

	allowed := 0
	for decision := range decisions {
		switch decision.Action {
		case ActionProcess:
			allowed++
		case ActionSendToken:
			if len(decision.Token) != TokenSize {
				t.Fatalf("challenge token length = %d", len(decision.Token))
			}
		default:
			t.Fatalf("unexpected flood decision: %v", decision)
		}
	}
	if allowed > 8 {
		t.Fatalf("allowed %d commits, want at most 8", allowed)
	}
	if got := filter.Incomplete(); got != allowed {
		t.Fatalf("incomplete = %d, allowed = %d", got, allowed)
	}
	if got := filter.Stats().CurveValidations; got != uint64(allowed) {
		t.Fatalf("curve validations = %d, allowed = %d", got, allowed)
	}
}

func TestInvalidRequestAndParseCommitBody(t *testing.T) {
	filter, err := NewFilter(Config{TokenSecret: []byte("parse-secret")})
	if err != nil {
		t.Fatalf("NewFilter: %v", err)
	}
	if decision := filter.HandleCommit(CommitRequest{
		StationMAC: net.HardwareAddr{1, 2, 3},
		Group:      Group19,
	}); decision.Action != ActionDrop || !errors.Is(decision.Reason, ErrInvalidStation) {
		t.Fatalf("bad MAC decision: %v", decision)
	}
	if decision := filter.HandleCommit(CommitRequest{
		StationMAC: macAddress(1),
		Group:      99,
	}); decision.Action != ActionDrop || !errors.Is(decision.Reason, ErrUnsupportedGroup) {
		t.Fatalf("bad group decision: %v", decision)
	}

	g := mustGroup(t, Group19)
	body := append(scalarFor(t, g, 5), pointFor(t, g, 5)...)
	commit, err := ParseCommitBody(Group19, body)
	if err != nil {
		t.Fatalf("ParseCommitBody: %v", err)
	}
	if err := commit.Validate(nil); err != nil {
		t.Fatalf("parsed commit validation: %v", err)
	}
	if _, err := ParseCommitBody(Group19, body[:len(body)-1]); !errors.Is(err, ErrElementEncoding) {
		t.Fatalf("short body error = %v", err)
	}
}
