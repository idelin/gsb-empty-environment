package netsec

import (
	"errors"
	"math/big"
	"sync"
	"testing"
)

// fakeChain is an in-memory ChainClient double. It counts broadcasts so
// tests can assert that payment happened exactly once.
type fakeChain struct {
	mu        sync.Mutex
	sendCount int
	receipts  map[ChainID]map[string]*Receipt
	// preloaded holds receipts that exist on chain before any submission
	// (e.g. an attacker replayed the raw signed transaction).
	preloaded map[ChainID]map[string]*Receipt
}

func newFakeChain() *fakeChain {
	return &fakeChain{
		receipts:  make(map[ChainID]map[string]*Receipt),
		preloaded: make(map[ChainID]map[string]*Receipt),
	}
}

func (f *fakeChain) GetReceipt(chainID ChainID, txHash string) (*Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if rc, ok := f.preloaded[chainID][txHash]; ok {
		cp := *rc
		return &cp, nil
	}
	if rc, ok := f.receipts[chainID][txHash]; ok {
		cp := *rc
		return &cp, nil
	}
	return nil, nil
}

func (f *fakeChain) SendRawTransaction(chainID ChainID, signedTx []byte) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendCount++
	hash := "0xhash-" + string(signedTx)
	if f.receipts[chainID] == nil {
		f.receipts[chainID] = make(map[string]*Receipt)
	}
	f.receipts[chainID][hash] = &Receipt{
		ChainID: chainID,
		TxHash:  hash,
		Status:  TxStatusPending,
	}
	return hash, nil
}

func (f *fakeChain) sends() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sendCount
}

// setReceipt lets a test drive on-chain status/confirmation changes.
func (f *fakeChain) setReceipt(chainID ChainID, txHash string, mutate func(*Receipt)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	mutate(f.receipts[chainID][txHash])
}

// preload plants a receipt as if the tx already landed on chain.
func (f *fakeChain) preload(chainID ChainID, rc *Receipt) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.preloaded[chainID] == nil {
		f.preloaded[chainID] = make(map[string]*Receipt)
	}
	f.preloaded[chainID][rc.TxHash] = rc
}

func testOrder() Order {
	return Order{
		ID:       "order-1",
		ChainID:  1,
		From:     "0xsender",
		To:       "0xrecipient",
		Amount:   big.NewInt(1000),
		Nonce:    7,
		TxHash:   "0xhash-signed-tx-v1",
		SignedTx: []byte("signed-tx-v1"),
	}
}

// fillReceipt makes the fake chain return a receipt matching the order.
func fillReceipt(o Order) *Receipt {
	return &Receipt{
		ChainID: o.ChainID,
		TxHash:  o.TxHash,
		From:    o.From,
		To:      o.To,
		Amount:  new(big.Int).Set(o.Amount),
		Nonce:   o.Nonce,
		Status:  TxStatusPending,
	}
}

func TestLegitimateSubmitAndConfirmations(t *testing.T) {
	fc := newFakeChain()
	svc := NewService(fc)
	o := testOrder()

	rec, err := svc.Submit(o)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if rec.Receipt.Status != TxStatusPending {
		t.Fatalf("want pending, got %v", rec.Receipt.Status)
	}

	fc.setReceipt(o.ChainID, o.TxHash, func(rc *Receipt) {
		rc.From, rc.To = o.From, o.To
		rc.Amount, rc.Nonce = new(big.Int).Set(o.Amount), o.Nonce
		rc.Status = TxStatusConfirmed
		rc.Confirmations = 12
	})

	rc, err := svc.Refresh(o.ID)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if rc.Status != TxStatusConfirmed || rc.Confirmations != 12 {
		t.Fatalf("want confirmed/12, got %v/%d", rc.Status, rc.Confirmations)
	}
	if fc.sends() != 1 {
		t.Fatalf("want exactly 1 broadcast, got %d", fc.sends())
	}
}

func TestDuplicateSubmitPaysOnce(t *testing.T) {
	fc := newFakeChain()
	svc := NewService(fc)
	o := testOrder()

	if _, err := svc.Submit(o); err != nil {
		t.Fatalf("first submit: %v", err)
	}
	for i := 0; i < 5; i++ {
		rec, err := svc.Submit(o)
		if err != nil {
			t.Fatalf("resubmit %d: %v", i, err)
		}
		if rec.Order.TxHash != o.TxHash {
			t.Fatalf("resubmit %d: wrong hash %s", i, rec.Order.TxHash)
		}
	}
	if fc.sends() != 1 {
		t.Fatalf("duplicate submissions broadcast %d times, want 1", fc.sends())
	}
}

func TestConcurrentDuplicateSubmitPaysOnce(t *testing.T) {
	fc := newFakeChain()
	svc := NewService(fc)
	o := testOrder()

	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Submit(o)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent submit: %v", err)
		}
	}
	if fc.sends() != 1 {
		t.Fatalf("concurrent duplicates broadcast %d times, want 1", fc.sends())
	}
}

func TestNonceReuseRejected(t *testing.T) {
	fc := newFakeChain()
	svc := NewService(fc)
	o := testOrder()

	if _, err := svc.Submit(o); err != nil {
		t.Fatalf("first submit: %v", err)
	}

	// Attacker replays the same transfer with the same account+nonce but a
	// new order ID and re-signed transaction.
	replay := o
	replay.ID = "order-evil"
	replay.TxHash = "0xhash-signed-tx-v2"
	replay.SignedTx = []byte("signed-tx-v2")

	if _, err := svc.Submit(replay); !errors.Is(err, ErrNonceReused) {
		t.Fatalf("want ErrNonceReused, got %v", err)
	}
	if fc.sends() != 1 {
		t.Fatalf("nonce reuse broadcast %d times, want 1", fc.sends())
	}
}

func TestSignatureReplacementRejected(t *testing.T) {
	fc := newFakeChain()
	svc := NewService(fc)
	o := testOrder()

	if _, err := svc.Submit(o); err != nil {
		t.Fatalf("first submit: %v", err)
	}

	// Same order, same nonce, but mutated signature bytes -> different hash.
	malleated := o
	malleated.TxHash = "0xhash-signed-tx-malleated"
	malleated.SignedTx = []byte("signed-tx-malleated")

	if _, err := svc.Submit(malleated); !errors.Is(err, ErrSignatureReplaced) {
		t.Fatalf("want ErrSignatureReplaced, got %v", err)
	}
	if fc.sends() != 1 {
		t.Fatalf("signature replacement broadcast %d times, want 1", fc.sends())
	}
}

func TestCrossChainReplayRejected(t *testing.T) {
	fc := newFakeChain()
	svc := NewService(fc)
	o := testOrder()

	if _, err := svc.Submit(o); err != nil {
		t.Fatalf("first submit: %v", err)
	}

	// Same signed tx hash submitted for a different chain.
	replay := o
	replay.ID = "order-crosschain"
	replay.ChainID = 2
	replay.Nonce = 99 // avoid tripping the nonce guard; isolate the chain check

	if _, err := svc.Submit(replay); !errors.Is(err, ErrCrossChainReplay) {
		t.Fatalf("want ErrCrossChainReplay, got %v", err)
	}
	if fc.sends() != 1 {
		t.Fatalf("cross-chain replay broadcast %d times, want 1", fc.sends())
	}
}

func TestConflictingResubmitRejected(t *testing.T) {
	fc := newFakeChain()
	svc := NewService(fc)
	o := testOrder()

	if _, err := svc.Submit(o); err != nil {
		t.Fatalf("first submit: %v", err)
	}

	bigger := o
	bigger.Amount = big.NewInt(999999)
	if _, err := svc.Submit(bigger); !errors.Is(err, ErrOrderConflict) {
		t.Fatalf("want ErrOrderConflict, got %v", err)
	}
	if fc.sends() != 1 {
		t.Fatalf("conflicting resubmit broadcast %d times, want 1", fc.sends())
	}
}

func TestOnChainReplayAdoptedNotRebroadcast(t *testing.T) {
	fc := newFakeChain()
	svc := NewService(fc)
	o := testOrder()

	// Attacker already replayed the raw signed tx on chain before the
	// service submits. The service must adopt it, not pay again.
	rc := fillReceipt(o)
	rc.Status = TxStatusConfirmed
	rc.Confirmations = 3
	fc.preload(o.ChainID, rc)

	rec, err := svc.Submit(o)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if rec.Receipt.Status != TxStatusConfirmed || rec.Receipt.Confirmations != 3 {
		t.Fatalf("want adopted confirmed receipt, got %v/%d", rec.Receipt.Status, rec.Receipt.Confirmations)
	}
	if fc.sends() != 0 {
		t.Fatalf("pre-submit chain check failed: broadcast %d times, want 0", fc.sends())
	}
}

func TestFailureReadback(t *testing.T) {
	fc := newFakeChain()
	svc := NewService(fc)
	o := testOrder()

	if _, err := svc.Submit(o); err != nil {
		t.Fatalf("submit: %v", err)
	}

	fc.setReceipt(o.ChainID, o.TxHash, func(rc *Receipt) {
		rc.From, rc.To = o.From, o.To
		rc.Amount, rc.Nonce = new(big.Int).Set(o.Amount), o.Nonce
		rc.Status = TxStatusFailed
		rc.Confirmations = 1
	})

	rc, err := svc.Refresh(o.ID)
	if !errors.Is(err, ErrTxFailed) {
		t.Fatalf("want ErrTxFailed, got %v", err)
	}
	if rc == nil || rc.Status != TxStatusFailed {
		t.Fatalf("want failed receipt, got %+v", rc)
	}

	// The failed status must persist in the stored record.
	rec, gerr := svc.Get(o.ID)
	if gerr != nil {
		t.Fatalf("get: %v", gerr)
	}
	if rec.Receipt.Status != TxStatusFailed {
		t.Fatalf("stored status = %v, want failed", rec.Receipt.Status)
	}
}

func TestRefreshUnknownOrder(t *testing.T) {
	svc := NewService(newFakeChain())
	if _, err := svc.Refresh("nope"); !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("want ErrOrderNotFound, got %v", err)
	}
}

func TestInvalidOrderRejected(t *testing.T) {
	svc := NewService(newFakeChain())
	o := testOrder()
	o.Amount = big.NewInt(0)
	if _, err := svc.Submit(o); err == nil {
		t.Fatal("want validation error for zero amount")
	}
}
