package netsec_test

import (
	"math/big"
	"testing"

	netsec "example.org/netsec/q0455"
)

func TestExternalPackageUsage(t *testing.T) {
	guard := netsec.NewGuard(
		netsec.Config{PriceCooldownBlocks: 2},
		netsec.WithMaxProfitBps(100),
		netsec.OptionFunc(func(cfg *netsec.Config) {
			cfg.LiquidationCooldownBlocks = 2
		}),
	)
	if err := guard.BeginBlock(10); err != nil {
		t.Fatalf("BeginBlock: %v", err)
	}
	if err := guard.SetPrice("USD", big.NewInt(1)); err != nil {
		t.Fatalf("SetPrice: %v", err)
	}
	if err := guard.BeginBlock(11); err != nil {
		t.Fatalf("BeginBlock: %v", err)
	}
	loan, err := guard.BeginFlashLoan(netsec.FlashLoanRequest{
		Account: "alice",
		Asset:   "USD",
		Amount:  netsec.NewAmount(100),
	})
	if err != nil {
		t.Fatalf("BeginFlashLoan: %v", err)
	}
	if err := guard.RepayFlashLoan(netsec.RepayRequest{LoanID: loan.ID}); err != nil {
		t.Fatalf("RepayFlashLoan: %v", err)
	}
	if ctx := guard.Context("alice", 11); ctx.OperationCount() != 2 {
		t.Fatalf("context operations = %d, want 2", ctx.OperationCount())
	}
}
