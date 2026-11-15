package netsec

import (
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"
)

type testPoint struct {
	scalar  *big.Int
	x       *big.Int
	y       *big.Int
	element []byte
}

func randomPoint(t *testing.T, curve elliptic.Curve) testPoint {
	t.Helper()
	order := curve.Params().N
	limit := new(big.Int).Sub(order, big.NewInt(2))
	scalar, err := rand.Int(rand.Reader, limit)
	if err != nil {
		t.Fatal(err)
	}
	scalar.Add(scalar, big.NewInt(2))
	return pointFromScalar(t, curve, scalar)
}

func pointFromScalar(t *testing.T, curve elliptic.Curve, scalar *big.Int) testPoint {
	t.Helper()
	x, y := curve.ScalarBaseMult(scalar.Bytes())
	size := (curve.Params().BitSize + 7) / 8
	element := make([]byte, 2*size)
	x.FillBytes(element[:size])
	y.FillBytes(element[size:])
	return testPoint{
		scalar:  new(big.Int).Set(scalar),
		x:       x,
		y:       y,
		element: element,
	}
}

func stationMAC(i int) net.HardwareAddr {
	return net.HardwareAddr{0x02, 0x00, 0x00, byte(i >> 16), byte(i >> 8), byte(i)}
}

func newTestFilter(t *testing.T, cfg Config) *Filter {
	t.Helper()
	filter, err := NewFilter(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return filter
}

func TestCommitFloodGetsOnlyTokens(t *testing.T) {
	curve := elliptic.P256()
	local := randomPoint(t, curve)
	peer := randomPoint(t, curve)

	filter := newTestFilter(t, Config{
		Threshold:          3,
		KeyRotation:        time.Minute,
		TokenTTL:           time.Minute,
		TransactionTimeout: time.Hour,
	})
	commit := Commit{
		Group:        GroupP256,
		Scalar:       peer.scalar.FillBytes(make([]byte, 32)),
		Element:      peer.element,
		LocalElement: local.element,
	}

	const floodSize = 256
	for i := 0; i < floodSize; i++ {
		result := filter.HandleCommit(stationMAC(i), commit)
		switch {
		case i < 3:
			if result.Action != ActionAccept {
				t.Fatalf("commit %d: action = %v, err = %v", i, result.Action, result.Err)
			}
			if filter.Incomplete() != i+1 {
				t.Fatalf("commit %d: incomplete = %d", i, filter.Incomplete())
			}
		default:
			if result.Action != ActionChallenge {
				t.Fatalf("commit %d: action = %v, err = %v; want challenge", i, result.Action, result.Err)
			}
			if len(result.Token) != TokenSize {
				t.Fatalf("commit %d: token length = %d", i, len(result.Token))
			}
		}
	}

	if got := filter.Incomplete(); got != 3 {
		t.Fatalf("incomplete after flood = %d, want 3", got)
	}
}

func TestAntiCloggingTokenReplayRejected(t *testing.T) {
	curve := elliptic.P256()
	local := randomPoint(t, curve)
	peer := randomPoint(t, curve)
	client := net.HardwareAddr{0x02, 0xaa, 0xbb, 0xcc, 0xdd, 0xee}

	filter := newTestFilter(t, Config{
		Threshold:          2,
		KeyRotation:        time.Minute,
		TokenTTL:           time.Minute,
		TransactionTimeout: time.Hour,
	})
	commit := Commit{
		Group:        GroupP256,
		Scalar:       peer.scalar.FillBytes(make([]byte, 32)),
		Element:      peer.element,
		LocalElement: local.element,
	}

	for i := 0; i < 2; i++ {
		if got := filter.HandleCommit(stationMAC(i), commit); got.Action != ActionAccept {
			t.Fatalf("filling slot %d: action = %v, err = %v", i, got.Action, got.Err)
		}
	}

	challenge := filter.HandleCommit(client, commit)
	if challenge.Action != ActionChallenge || len(challenge.Token) != TokenSize {
		t.Fatalf("initial client commit: action = %v, token = %d, err = %v",
			challenge.Action, len(challenge.Token), challenge.Err)
	}

	commit.Token = challenge.Token
	if got := filter.HandleCommit(client, commit); got.Action != ActionAccept {
		t.Fatalf("token retry: action = %v, err = %v", got.Action, got.Err)
	}

	// A retransmission of an already admitted commit is served without
	// extra curve work and without another admission slot.
	commit.Token = nil
	if got := filter.HandleCommit(client, commit); got.Action != ActionAccept || !got.Duplicate {
		t.Fatalf("duplicate: action = %v, duplicate = %v, err = %v", got.Action, got.Duplicate, got.Err)
	}
	if got := filter.Incomplete(); got != 3 {
		t.Fatalf("incomplete after duplicate = %d, want 3", got)
	}

	if !filter.Complete(client) {
		t.Fatal("Complete did not find the client transaction")
	}

	commit.Token = challenge.Token
	replay := filter.HandleCommit(client, commit)
	if replay.Action == ActionAccept {
		t.Fatal("replayed token was accepted")
	}
	if !errors.Is(replay.Err, ErrTokenReplay) {
		t.Fatalf("replay error = %v, want ErrTokenReplay", replay.Err)
	}
	if len(replay.Token) != TokenSize {
		t.Fatalf("replay challenge token length = %d", len(replay.Token))
	}
	if got := filter.Incomplete(); got != 2 {
		t.Fatalf("incomplete after replay = %d, want 2", got)
	}

	// Tokens are bound to the station MAC, so copying one to another MAC
	// fails authentication.
	commit.Token = challenge.Token
	other := filter.HandleCommit(net.HardwareAddr{0x02, 0x11, 0x22, 0x33, 0x44, 0x55}, commit)
	if other.Action == ActionAccept {
		t.Fatal("token for one station was accepted for another station")
	}
	if !errors.Is(other.Err, ErrInvalidToken) {
		t.Fatalf("cross-station token error = %v, want ErrInvalidToken", other.Err)
	}
}

func TestReflectedCommitRejected(t *testing.T) {
	curve := elliptic.P256()
	local := randomPoint(t, curve)
	peerScalar := randomPoint(t, curve).scalar

	filter := newTestFilter(t, Config{
		Threshold:          8,
		KeyRotation:        time.Minute,
		TokenTTL:           time.Minute,
		TransactionTimeout: time.Minute,
	})

	reflected := Commit{
		Group:        GroupP256,
		Scalar:       peerScalar.FillBytes(make([]byte, 32)),
		Element:      local.element,
		LocalElement: local.element,
	}
	result := filter.HandleCommit(stationMAC(1), reflected)
	if result.Action != ActionReject || !errors.Is(result.Err, ErrReflectedCommit) {
		t.Fatalf("reflection: action = %v, err = %v", result.Action, result.Err)
	}
	if got := filter.Incomplete(); got != 0 {
		t.Fatalf("reflection consumed %d slots", got)
	}

	// Alternate point encodings must not bypass reflection detection.
	sec1 := make([]byte, 1+len(local.element))
	sec1[0] = 4
	copy(sec1[1:], local.element)
	reflected.Element = sec1
	result = filter.HandleCommit(stationMAC(2), reflected)
	if !errors.Is(result.Err, ErrReflectedCommit) {
		t.Fatalf("SEC1 reflection error = %v, want ErrReflectedCommit", result.Err)
	}
}

func TestScalarAndElementBounds(t *testing.T) {
	curve := elliptic.P256()
	local := randomPoint(t, curve)
	peer := randomPoint(t, curve)

	valid := func(scalar []byte, element []byte) error {
		return ValidateCommit(Commit{
			Group:        GroupP256,
			Scalar:       scalar,
			Element:      element,
			LocalElement: local.element,
		})
	}

	if err := valid(big.NewInt(2).Bytes(), peer.element); err != nil {
		t.Fatalf("scalar 2 rejected: %v", err)
	}
	if err := valid(new(big.Int).Sub(curve.Params().N, big.NewInt(1)).Bytes(), peer.element); err != nil {
		t.Fatalf("scalar r-1 rejected: %v", err)
	}
	for _, scalar := range []*big.Int{
		big.NewInt(0),
		big.NewInt(1),
		new(big.Int).Set(curve.Params().N),
		new(big.Int).Add(curve.Params().N, big.NewInt(1)),
	} {
		if err := valid(scalar.Bytes(), peer.element); !errors.Is(err, ErrInvalidScalar) {
			t.Fatalf("scalar %s: err = %v, want ErrInvalidScalar", scalar, err)
		}
	}

	offCurve := make([]byte, 64)
	offCurve[63] = 1
	if err := valid(peer.scalar.Bytes(), offCurve); !errors.Is(err, ErrInvalidElement) {
		t.Fatalf("off-curve element: err = %v, want ErrInvalidElement", err)
	}
	if err := valid(peer.scalar.Bytes(), make([]byte, 64)); !errors.Is(err, ErrPointAtInfinity) {
		t.Fatalf("infinity element: err = %v, want ErrPointAtInfinity", err)
	}
	if err := valid(peer.scalar.Bytes(), []byte{0}); !errors.Is(err, ErrPointAtInfinity) {
		t.Fatalf("SEC1 infinity element: err = %v, want ErrPointAtInfinity", err)
	}

	if err := ValidateCommit(Commit{Group: GroupID(29), Scalar: []byte{2}, Element: peer.element}); !errors.Is(err, ErrUnsupportedGroup) {
		t.Fatalf("unsupported group: err = %v, want ErrUnsupportedGroup", err)
	}
}

func TestLegitimateHandshakeThroughChallenge(t *testing.T) {
	curve := elliptic.P256()
	ap := randomPoint(t, curve)
	client := randomPoint(t, curve)
	clientMAC := net.HardwareAddr{0x02, 0xde, 0xad, 0xbe, 0xef, 0x01}

	filter := newTestFilter(t, Config{
		Threshold:          2,
		KeyRotation:        time.Minute,
		TokenTTL:           time.Minute,
		TransactionTimeout: time.Hour,
	})

	// Saturate the untokened admission budget with other stations.
	peer := randomPoint(t, curve)
	other := Commit{
		Group:        GroupP256,
		Scalar:       peer.scalar.FillBytes(make([]byte, 32)),
		Element:      peer.element,
		LocalElement: ap.element,
	}
	for i := 0; i < 2; i++ {
		if got := filter.HandleCommit(stationMAC(i), other); got.Action != ActionAccept {
			t.Fatalf("fill %d: action = %v, err = %v", i, got.Action, got.Err)
		}
	}

	commit := Commit{
		Group:        GroupP256,
		Scalar:       client.scalar.FillBytes(make([]byte, 32)),
		Element:      client.element,
		LocalElement: ap.element,
	}
	challenge := filter.HandleCommit(clientMAC, commit)
	if challenge.Action != ActionChallenge {
		t.Fatalf("legitimate client was not challenged: action = %v, err = %v", challenge.Action, challenge.Err)
	}

	commit.Token = challenge.Token
	if got := filter.HandleCommit(clientMAC, commit); got.Action != ActionAccept {
		t.Fatalf("legitimate token retry: action = %v, err = %v", got.Action, got.Err)
	}

	// Verify the accepted commit material really forms the same SAE shared
	// secret for both sides.
	apSharedX, _ := curve.ScalarMult(client.x, client.y, ap.scalar.Bytes())
	clientSharedX, _ := curve.ScalarMult(ap.x, ap.y, client.scalar.Bytes())
	if apSharedX.Cmp(clientSharedX) != 0 {
		t.Fatal("AP and client derived different shared secrets")
	}

	if !filter.Complete(clientMAC) {
		t.Fatal("Complete did not release the legitimate transaction")
	}
	if got := filter.Incomplete(); got != 2 {
		t.Fatalf("incomplete after handshake = %d, want 2", got)
	}
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func TestKeyRotationAndTokenExpiry(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	curve := elliptic.P256()
	local := randomPoint(t, curve)
	peer := randomPoint(t, curve)
	client := net.HardwareAddr{0x02, 0xca, 0xfe, 0xba, 0xbe, 0x02}

	filter := newTestFilter(t, Config{
		Threshold:          1,
		KeyRotation:        10 * time.Second,
		TokenTTL:           20 * time.Second,
		TransactionTimeout: time.Hour,
		Now:                clock.Now,
	})
	commit := Commit{
		Group:        GroupP256,
		Scalar:       peer.scalar.FillBytes(make([]byte, 32)),
		Element:      peer.element,
		LocalElement: local.element,
	}
	if got := filter.HandleCommit(stationMAC(0), commit); got.Action != ActionAccept {
		t.Fatalf("fill: action = %v, err = %v", got.Action, got.Err)
	}

	challenge := filter.HandleCommit(client, commit)
	if challenge.Action != ActionChallenge || len(challenge.Token) != TokenSize {
		t.Fatalf("challenge: action = %v, token = %d, err = %v",
			challenge.Action, len(challenge.Token), challenge.Err)
	}

	clock.Advance(25 * time.Second)
	commit.Token = challenge.Token
	expired := filter.HandleCommit(client, commit)
	if expired.Action == ActionAccept {
		t.Fatal("token generated with an expired rotated key was accepted")
	}
	if !errors.Is(expired.Err, ErrInvalidToken) {
		t.Fatalf("expired token error = %v, want ErrInvalidToken", expired.Err)
	}
	if len(expired.Token) != TokenSize {
		t.Fatalf("expected a fresh challenge token, got length %d", len(expired.Token))
	}

	commit.Token = expired.Token
	if got := filter.HandleCommit(client, commit); got.Action != ActionAccept {
		t.Fatalf("freshly rotated token: action = %v, err = %v", got.Action, got.Err)
	}
}

func TestInvalidFloodDoesNotConsumeSlots(t *testing.T) {
	curve := elliptic.P256()
	local := randomPoint(t, curve)
	peer := randomPoint(t, curve)
	filter := newTestFilter(t, Config{
		Threshold:          4,
		KeyRotation:        time.Minute,
		TokenTTL:           time.Minute,
		TransactionTimeout: time.Hour,
	})

	bad := Commit{
		Group:        GroupP256,
		Scalar:       []byte{1},
		Element:      peer.element,
		LocalElement: local.element,
	}
	for i := 0; i < 128; i++ {
		if got := filter.HandleCommit(stationMAC(i), bad); got.Action != ActionReject {
			t.Fatalf("bad commit %d: action = %v, err = %v", i, got.Action, got.Err)
		}
	}
	if got := filter.Incomplete(); got != 0 {
		t.Fatalf("invalid commits consumed %d slots", got)
	}

	good := Commit{
		Group:        GroupP256,
		Scalar:       peer.scalar.FillBytes(make([]byte, 32)),
		Element:      peer.element,
		LocalElement: local.element,
	}
	if got := filter.HandleCommit(stationMAC(500), good); got.Action != ActionAccept {
		t.Fatalf("valid commit after invalid flood: action = %v, err = %v", got.Action, got.Err)
	}
}

func TestTransactionExpiryReleasesSlot(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	curve := elliptic.P256()
	local := randomPoint(t, curve)
	peer := randomPoint(t, curve)
	filter := newTestFilter(t, Config{
		Threshold:          1,
		KeyRotation:        time.Minute,
		TokenTTL:           time.Minute,
		TransactionTimeout: 10 * time.Second,
		Now:                clock.Now,
	})
	commit := Commit{
		Group:        GroupP256,
		Scalar:       peer.scalar.FillBytes(make([]byte, 32)),
		Element:      peer.element,
		LocalElement: local.element,
	}
	if got := filter.HandleCommit(stationMAC(0), commit); got.Action != ActionAccept {
		t.Fatalf("admit: action = %v, err = %v", got.Action, got.Err)
	}
	clock.Advance(11 * time.Second)
	if got := filter.Incomplete(); got != 0 {
		t.Fatalf("expired transaction still occupies %d slots", got)
	}
	if got := filter.HandleCommit(stationMAC(1), commit); got.Action != ActionAccept {
		t.Fatalf("new commit after expiry: action = %v, err = %v", got.Action, got.Err)
	}
}

func TestUnsupportedGroup(t *testing.T) {
	if err := ValidateCommit(Commit{Group: GroupID(30)}); !errors.Is(err, ErrUnsupportedGroup) {
		t.Fatalf("group 30: err = %v, want ErrUnsupportedGroup", err)
	}
}

func TestSupportedGroups(t *testing.T) {
	tests := []struct {
		group GroupID
		curve elliptic.Curve
	}{
		{GroupP192, p192},
		{GroupP224, elliptic.P224()},
		{GroupP256, elliptic.P256()},
		{GroupP384, elliptic.P384()},
		{GroupP521, elliptic.P521()},
	}
	for _, test := range tests {
		t.Run(test.group.String(), func(t *testing.T) {
			local := randomPoint(t, test.curve)
			peer := randomPoint(t, test.curve)
			if err := ValidateCommit(Commit{
				Group:        test.group,
				Scalar:       peer.scalar.FillBytes(make([]byte, test.group.Size())),
				Element:      peer.element,
				LocalElement: local.element,
			}); err != nil {
				t.Fatalf("valid group %d commit rejected: %v", test.group, err)
			}
		})
	}
}
