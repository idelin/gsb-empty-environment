package netsec

import (
	"errors"
	"math/big"
	"testing"
)

func TestNewEngineRejectsNonPositiveWindow(t *testing.T) {
	for _, w := range []int{0, -3} {
		if _, err := NewEngine(w); !errors.Is(err, ErrWindowRounds) {
			t.Fatalf("window %d: want ErrWindowRounds, got %v", w, err)
		}
	}
}

func TestRejectBadReceiversAndIDs(t *testing.T) {
	e, _ := NewEngine(2)
	if err := e.RegisterReceiver("", big.NewInt(1)); !errors.Is(err, ErrEmptyID) {
		t.Fatalf("empty id: %v", err)
	}
	if err := e.RegisterReceiver("r", nil); !errors.Is(err, ErrZeroIndex) {
		t.Fatalf("nil x: want ErrZeroIndex, got %v", err)
	}
	if err := e.RegisterReceiver("r", big.NewInt(0)); !errors.Is(err, ErrZeroIndex) {
		t.Fatalf("zero x: %v", err)
	}
	if err := e.RegisterReceiver("r", new(big.Int).Set(Curve.Params().N)); !errors.Is(err, ErrScalarOutOfRange) {
		t.Fatalf("x=N: %v", err)
	}
	if err := e.RegisterReceiver("r", big.NewInt(4)); err != nil {
		t.Fatal(err)
	}
	if err := e.RegisterReceiver("r", big.NewInt(5)); !errors.Is(err, ErrDuplicateReceiver) {
		t.Fatalf("dup id: %v", err)
	}
}

func TestRejectBadCommitmentRegistration(t *testing.T) {
	e, _ := NewEngine(2)
	if err := e.RegisterCommitment("", genDealer("x", 2).commit); !errors.Is(err, ErrEmptyID) {
		t.Fatalf("empty id: %v", err)
	}
	m := genDealer("d", 2)
	if err := e.RegisterCommitment("d", m.commit); err != nil {
		t.Fatal(err)
	}
	if err := e.RegisterCommitment("d", m.commit); !errors.Is(err, ErrDuplicateDealer) {
		t.Fatalf("dup dealer: %v", err)
	}
	bad := cloneCommit(m.commit)
	bad.Points[0] = infinityPoint()
	if err := e.RegisterCommitment("d2", bad); !errors.Is(err, ErrInvalidCommitmentPoint) {
		t.Fatalf("identity constant: %v", err)
	}
}

func TestReceiptAndShareUnknowns(t *testing.T) {
	e, _ := NewEngine(2)
	_ = e.RegisterReceiver("r1", big.NewInt(1))
	m := genDealer("d1", 2)
	_ = e.RegisterCommitment("d1", m.commit)

	var h [32]byte
	if err := e.RegisterReceipt("ghost", "r1", h); !errors.Is(err, ErrUnknownDealer) {
		t.Fatalf("unknown dealer receipt: %v", err)
	}
	if err := e.RegisterReceipt("d1", "ghost", h); !errors.Is(err, ErrUnknownReceiver) {
		t.Fatalf("unknown receiver receipt: %v", err)
	}
	if err := e.RegisterShare("ghost", "r1", big.NewInt(2)); !errors.Is(err, ErrUnknownDealer) {
		t.Fatalf("unknown dealer share: %v", err)
	}
	if err := e.RegisterShare("d1", "ghost", big.NewInt(2)); !errors.Is(err, ErrUnknownReceiver) {
		t.Fatalf("unknown receiver share: %v", err)
	}
	if err := e.RegisterShare("d1", "r1", big.NewInt(0)); !errors.Is(err, ErrScalarOutOfRange) {
		t.Fatalf("zero share: %v", err)
	}
	_ = e.RegisterReceipt("d1", "r1", h)
	if err := e.RegisterShare("d1", "r1", big.NewInt(2)); err != nil {
		t.Fatal(err)
	}
	if err := e.RegisterShare("d1", "r1", big.NewInt(3)); !errors.Is(err, ErrDuplicateShare) {
		t.Fatalf("dup share: %v", err)
	}
}

func TestComplaintAndDisclosureGuardPaths(t *testing.T) {
	e, _ := NewEngine(1)
	_ = e.RegisterReceiver("r1", big.NewInt(1))
	m := genDealer("d1", 2)
	_ = e.RegisterCommitment("d1", m.commit)
	h := m.commit.Hash()
	_ = e.RegisterReceipt("d1", "r1", h)

	if err := e.FileComplaint("ghost", "r1", big.NewInt(3), h); !errors.Is(err, ErrUnknownDealer) {
		t.Fatalf("unknown dealer complaint: %v", err)
	}
	if err := e.FileComplaint("d1", "ghost", big.NewInt(3), h); !errors.Is(err, ErrUnknownReceiver) {
		t.Fatalf("unknown receiver complaint: %v", err)
	}
	if err := e.FileComplaint("d1", "r1", big.NewInt(0), h); !errors.Is(err, ErrScalarOutOfRange) {
		t.Fatalf("zero complaint share: %v", err)
	}
	bogus := new(big.Int).Add(m.shareAt(big.NewInt(1)), big.NewInt(1))
	bogus.Mod(bogus, Curve.Params().N)
	if err := e.FileComplaint("d1", "r1", bogus, h); err != nil {
		t.Fatal(err)
	}
	if err := e.FileComplaint("d1", "r1", bogus, h); !errors.Is(err, ErrDuplicateComplaint) {
		t.Fatalf("dup complaint: %v", err)
	}
	if err := e.DiscloseShare("ghost", "r1", bogus, h); !errors.Is(err, ErrUnknownDealer) {
		t.Fatalf("ghost disclosure: want ErrUnknownDealer, got %v", err)
	}
	if err := e.DiscloseShare("d1", "r2", bogus, h); !errors.Is(err, ErrUnknownReceiver) {
		t.Fatalf("unknown receiver disclosure: want ErrUnknownReceiver, got %v", err)
	}
	var foreign [32]byte
	foreign[31] = 9
	if err := e.DiscloseShare("d1", "r1", m.shareAt(big.NewInt(1)), foreign); !errors.Is(err, ErrCommitmentHashMismatch) {
		t.Fatalf("foreign disclosure hash: %v", err)
	}
	good := m.shareAt(big.NewInt(1))
	if err := e.DiscloseShare("d1", "r1", good, h); err != nil {
		t.Fatal(err)
	}
	if err := e.DiscloseShare("d1", "r1", good, h); !errors.Is(err, ErrDuplicateDisclosure) {
		t.Fatalf("dup disclosure: %v", err)
	}
}

func TestAdvanceNoopAfterFinalizeAndStatusUnknown(t *testing.T) {
	e, _ := NewEngine(1)
	if _, ok := e.DealerStatus("nobody"); ok {
		t.Fatal("unknown dealer reported as known")
	}
	_ = e.RegisterReceiver("r1", big.NewInt(1))
	m := genDealer("d1", 2)
	_ = e.RegisterCommitment("d1", m.commit)
	_ = e.RegisterReceipt("d1", "r1", m.commit.Hash())
	_ = e.RegisterShare("d1", "r1", m.shareAt(big.NewInt(1)))
	e.Advance()
	res, err := e.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	if e.Advance() != res.Round {
		t.Fatal("Advance must be a no-op after finalization")
	}
	if e.WindowOpen() {
		t.Fatal("window must report closed after finalization")
	}
	status, ok := e.DealerStatus("d1")
	if !ok || status.Disqualified {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestMissingShareAtFinalize(t *testing.T) {
	e, _ := NewEngine(1)
	_ = e.RegisterReceiver("r1", big.NewInt(1))
	m := genDealer("d1", 2)
	_ = e.RegisterCommitment("d1", m.commit)
	_ = e.RegisterReceipt("d1", "r1", m.commit.Hash())
	// Deliberately skip RegisterShare.
	e.Advance()
	_, err := e.Finalize()
	if !errors.Is(err, ErrMissingShare) {
		t.Fatalf("want ErrMissingShare, got %v", err)
	}
	// A failed finalization must not freeze the run: register the
	// missing share and finalize again successfully.
	if err := e.RegisterShare("d1", "r1", m.shareAt(big.NewInt(1))); err != nil {
		t.Fatalf("late share registration after failed finalize: %v", err)
	}
	res, err := e.Finalize()
	if err != nil {
		t.Fatalf("retry finalize: %v", err)
	}
	if len(res.Qualified) != 1 {
		t.Fatalf("unexpected result after retry: %+v", res)
	}
}

func TestSmallAccessorsAndEvaluationError(t *testing.T) {
	m := genDealer("d", 3)
	if m.commit.Threshold() != 3 {
		t.Fatal("threshold accessor wrong")
	}
	x := big.NewInt(5)
	if err := EvaluationError(m.commit, x, m.shareAt(x)); err != nil {
		t.Fatalf("valid evaluation errored: %v", err)
	}
	if err := EvaluationError(m.commit, big.NewInt(0), big.NewInt(1)); !errors.Is(err, ErrZeroIndex) {
		t.Fatalf("zero index: %v", err)
	}
	if !infinityPoint().IsIdentity() || generatorPoint().IsIdentity() {
		t.Fatal("identity classification wrong")
	}
}
