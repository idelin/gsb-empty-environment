package parity_test

import (
	"context"
	"errors"
	"math/big"
	"path/filepath"
	"testing"

	parity "prescreen.local/ps-2b994de5fb"
)

func TestCheckpointSaveLoadAndResumeFile(t *testing.T) {
	k := makeTinyKey(13, 23) // N=299
	p := paramsFor(k, 271)
	res, err := parity.Recover(p, k.oracle(), parity.Options{QueryBudget: 6})
	if !errors.Is(err, parity.ErrBudgetExhausted) {
		t.Fatalf("want budget, got %v", err)
	}
	path := filepath.Join(t.TempDir(), "checkpoint.json")
	if err := res.Checkpoint.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := parity.LoadCheckpoint(path)
	if err != nil {
		t.Fatal(err)
	}
	out, err := parity.Resume(context.Background(), p, loaded, k.oracle(), parity.Options{})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if out.Plaintext.Int64() != 271 {
		t.Fatalf("got %s", out.Plaintext)
	}
}

func TestCanceledContextStops(t *testing.T) {
	k := makeTinyKey(11, 13)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := parity.RecoverContext(ctx, paramsFor(k, 40), k.oracle(), parity.Options{})
	if !errors.Is(err, parity.ErrCanceled) {
		t.Fatalf("want ErrCanceled, got %v", err)
	}
	if res.Status != parity.StatusCanceled || res.Checkpoint == nil {
		t.Fatalf("status=%s cp=%v", res.Status, res.Checkpoint != nil)
	}
}

func TestAdaptIntFuncCallback(t *testing.T) {
	k := makeTinyKey(7, 19) // N=133
	intFunc := func(x *big.Int) (int, error) {
		p, err := k.oracle()(x)
		return p.Int(), err
	}
	c := encrypt(k, big.NewInt(81))
	res, err := parity.RecoverFunc(k.N, int(k.E.Int64()), c, intFunc, parity.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Plaintext.Int64() != 81 {
		t.Fatalf("got %s", res.Plaintext)
	}

	// An out-of-range int bit is rejected rather than defaulted to even.
	bad := parity.AdaptFunc(func(*big.Int) (int, error) { return 9, nil })
	if _, err := bad(big.NewInt(1)); !errors.Is(err, parity.ErrInvalidParityBit) {
		t.Fatalf("want ErrInvalidParityBit, got %v", err)
	}
}
