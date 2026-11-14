package netsec

import (
	"errors"
	"math/big"
	"testing"
)

func bigValue(v int64) *big.Int { return big.NewInt(v) }

func mustBegin(t *testing.T, g *Guard, block uint64) {
	t.Helper()
	if err := g.BeginBlock(block); err != nil {
		t.Fatalf("BeginBlock(%d): %v", block, err)
	}
}

func mustPrice(t *testing.T, g *Guard, block uint64, asset string, price int64) {
	t.Helper()
	mustBegin(t, g, block)
	if err := g.SetPrice(asset, bigValue(price)); err != nil {
		t.Fatalf("SetPrice(%s) at block %d: %v", asset, block, err)
	}
}

func mustLoan(t *testing.T, g *Guard, account string, amount int64, feeBps uint64) *FlashLoan {
	t.Helper()
	loan, err := g.BeginFlashLoan(FlashLoanRequest{
		Account: account,
		Asset:   "USD",
		Amount:  bigValue(amount),
		FeeBps:  feeBps,
	})
	if err != nil {
		t.Fatalf("BeginFlashLoan(%s): %v", account, err)
	}
	return loan
}

func hasFlag(flags []Flag, want Flag) bool {
	for _, flag := range flags {
		if flag == want {
			return true
		}
	}
	return false
}

func TestLegitimateSameBlockFlashLoan(t *testing.T) {
	g := NewGuard()
	mustPrice(t, g, 100, "USD", 1_000_000)

	loan := mustLoan(t, g, "borrower", 1_000_000, 5)
	if loan.RequiredRepayment.Cmp(bigValue(1_000_500)) != 0 {
		t.Fatalf("required repayment = %s, want 1000500", loan.RequiredRepayment)
	}
	if err := g.RepayFlashLoan(RepayRequest{LoanID: loan.ID}); err != nil {
		t.Fatalf("repay: %v", err)
	}

	if active, ok := g.ActiveLoan("borrower"); ok {
		t.Fatalf("loan should be closed, got %+v", active)
	}
	ctx := g.Context("borrower")
	if len(ctx.Loans) != 1 || len(ctx.Repayments) != 1 {
		t.Fatalf("context should contain one borrow and one repayment: %+v", ctx)
	}
	if !ctx.HasFlashLoan() || ctx.HasActiveFlashLoan() {
		t.Fatalf("unexpected loan state in context: %+v", ctx.Loans)
	}
	if len(g.Detections()) != 0 {
		t.Fatalf("legitimate loan produced detections: %+v", g.Detections())
	}
}

func TestConfigConstructorKeepsSafeDefaults(t *testing.T) {
	g := NewGuard(Config{PriceCooldownBlocks: 5})
	cfg := g.Config()
	if cfg.PriceCooldownBlocks != 5 {
		t.Fatalf("price cooldown = %d, want 5", cfg.PriceCooldownBlocks)
	}
	if !cfg.RequireSameBlockRepayment || !cfg.RequireMaturePrice {
		t.Fatalf("partial Config disabled safety defaults: %+v", cfg)
	}
	if cfg.MaxFeeBps != DefaultConfig().MaxFeeBps {
		t.Fatalf("max fee = %d, want default %d", cfg.MaxFeeBps, DefaultConfig().MaxFeeBps)
	}

	empty := NewGuardWithConfig(Config{}).Config()
	if empty.PriceCooldownBlocks != DefaultConfig().PriceCooldownBlocks ||
		!empty.RequireSameBlockRepayment ||
		!empty.RequireMaturePrice {
		t.Fatalf("empty Config did not retain defaults: %+v", empty)
	}
}

func TestSameBlockBorrowPriceLiquidateRepayIsRejectedAndRolledBack(t *testing.T) {
	g := NewGuard(
		WithPriceCooldownBlocks(5),
		WithLiquidationCooldownBlocks(5),
	)
	mustPrice(t, g, 100, "ETH", 2_000)

	err := g.AtBlock(101, func() error {
		if _, err := g.BeginFlashLoan(FlashLoanRequest{
			Account: "attacker",
			Asset:   "USD",
			Amount:  bigValue(10_000_000),
			FeeBps:  5,
		}); err != nil {
			return err
		}
		return g.UpdatePrice(PriceUpdateRequest{Account: "attacker", Asset: "ETH", Price: bigValue(20_000)})
	})
	if !errors.Is(err, ErrPriceManipulation) {
		t.Fatalf("same-block borrow+price error = %v, want ErrPriceManipulation", err)
	}

	if active, ok := g.ActiveLoan("attacker"); ok {
		t.Fatalf("failed attack left an active loan: %+v", active)
	}
	if got := g.CurrentBlock(); got != 100 {
		t.Fatalf("failed attack advanced block to %d, want 100", got)
	}
	if price, ok := g.Price("ETH"); !ok || price.Price.Cmp(bigValue(2_000)) != 0 || price.Block != 100 {
		t.Fatalf("failed attack changed price: %+v, ok=%v", price, ok)
	}
	ctx := g.Context("attacker", 101)
	if ctx.OperationCount() != 0 {
		t.Fatalf("failed attack left protocol operations: %+v", ctx)
	}
	if !hasFlag(ctx.Flags, FlagPriceManipulation) || !hasFlag(ctx.Flags, FlagSameBlockComposition) {
		t.Fatalf("failed attack was not marked: %v", ctx.Flags)
	}
	if !hasFlag(g.Flags("attacker", 101), FlagPriceManipulation) {
		t.Fatalf("detection ledger does not contain the attack flag: %+v", g.Detections())
	}
	found := false
	for _, ctx := range g.ContextsAt(101) {
		if ctx.Account == "attacker" && hasFlag(ctx.Flags, FlagPriceManipulation) {
			found = true
		}
	}
	if !found {
		t.Fatalf("ContextsAt did not expose the detection-only account context: %+v", g.ContextsAt(101))
	}

	// The borrow -> liquidate -> repay leg is also rejected directly.
	mustBegin(t, g, 101)
	loan := mustLoan(t, g, "attacker", 10_000_000, 5)
	err = g.Liquidate(LiquidationRequest{
		Liquidator:      "attacker",
		Borrower:        "victim",
		PositionID:      "victim-eth",
		CollateralAsset: "ETH",
		DebtAsset:       "USD",
		DebtAmount:      bigValue(1_000),
	})
	if !errors.Is(err, ErrSameBlockComposition) {
		t.Fatalf("borrow-then-liquidate error = %v, want ErrSameBlockComposition", err)
	}
	if err := g.RepayFlashLoan(RepayRequest{LoanID: loan.ID}); err != nil {
		t.Fatalf("repay after blocked liquidation: %v", err)
	}
}

func TestSeparateAccountsCannotPriceManipulateThenLiquidate(t *testing.T) {
	g := NewGuard(WithPriceCooldownBlocks(1))
	mustPrice(t, g, 200, "ETH", 2_000)

	err := g.AtBlock(201, func() error {
		if _, err := g.BeginFlashLoan(FlashLoanRequest{
			Account: "attacker",
			Asset:   "USD",
			Amount:  bigValue(5_000_000),
			FeeBps:  0,
		}); err != nil {
			return err
		}
		if err := g.UpdatePrice(PriceUpdateRequest{Account: "oracle", Asset: "ETH", Price: bigValue(1)}); err != nil {
			return err
		}
		return g.Liquidate(LiquidationRequest{
			Liquidator:      "keeper",
			Borrower:        "victim",
			PositionID:      "victim-eth",
			CollateralAsset: "ETH",
			DebtAsset:       "USD",
			DebtAmount:      bigValue(1_000),
		})
	})
	if !errors.Is(err, ErrPriceCooldown) {
		t.Fatalf("manipulate-then-liquidate error = %v, want ErrPriceCooldown", err)
	}
	if active, ok := g.ActiveLoan("attacker"); ok {
		t.Fatalf("failed combined attack left loan open: %+v", active)
	}
	if got := g.Context("keeper", 201); len(got.Liquidations) != 0 {
		t.Fatalf("failed liquidation was committed: %+v", got.Liquidations)
	}
}

func TestPriceUpdateAndLiquidationCooldowns(t *testing.T) {
	g := NewGuard(
		WithPriceCooldownBlocks(3),
		WithLiquidationCooldownBlocks(1),
	)
	mustPrice(t, g, 500, "ETH", 1_000)

	// One update is allowed every three blocks.
	mustBegin(t, g, 501)
	if err := g.UpdatePrice(PriceUpdateRequest{Account: "oracle", Asset: "ETH", Price: bigValue(1_010)}); !errors.Is(err, ErrPriceCooldown) {
		t.Fatalf("block 501 update error = %v, want ErrPriceCooldown", err)
	}
	mustBegin(t, g, 502)
	if err := g.UpdatePrice(PriceUpdateRequest{Account: "oracle", Asset: "ETH", Price: bigValue(1_010)}); !errors.Is(err, ErrPriceCooldown) {
		t.Fatalf("block 502 update error = %v, want ErrPriceCooldown", err)
	}
	mustBegin(t, g, 503)
	if err := g.UpdatePrice(PriceUpdateRequest{Account: "oracle", Asset: "ETH", Price: bigValue(1_010)}); err != nil {
		t.Fatalf("block 503 update: %v", err)
	}

	// A liquidation cannot consume the fresh block-503 price until block 506.
	req := LiquidationRequest{
		Liquidator:      "keeper",
		Borrower:        "victim",
		PositionID:      "victim-eth",
		CollateralAsset: "ETH",
		DebtAsset:       "USD",
		DebtAmount:      bigValue(100),
	}
	mustBegin(t, g, 504)
	if err := g.Liquidate(req); !errors.Is(err, ErrPriceCooldown) {
		t.Fatalf("block 504 liquidation error = %v, want ErrPriceCooldown", err)
	}
	mustBegin(t, g, 505)
	if err := g.Liquidate(req); !errors.Is(err, ErrPriceCooldown) {
		t.Fatalf("block 505 liquidation error = %v, want ErrPriceCooldown", err)
	}
	mustBegin(t, g, 506)
	if err := g.Liquidate(req); err != nil {
		t.Fatalf("block 506 liquidation: %v", err)
	}
}

func TestDuplicateLiquidationAndCooldown(t *testing.T) {
	g := NewGuard(WithLiquidationCooldownBlocks(3))
	mustPrice(t, g, 300, "ETH", 2_000)
	mustBegin(t, g, 401)

	req := LiquidationRequest{
		Liquidator:      "keeper",
		Borrower:        "victim",
		PositionID:      "victim-eth",
		CollateralAsset: "ETH",
		DebtAsset:       "USD",
		DebtAmount:      bigValue(100),
	}
	if err := g.Liquidate(req); err != nil {
		t.Fatalf("first liquidation: %v", err)
	}
	err := g.Liquidate(req)
	if !errors.Is(err, ErrDuplicateLiquidation) {
		t.Fatalf("same-block duplicate liquidation error = %v, want ErrDuplicateLiquidation", err)
	}
	if !hasFlag(g.Flags("keeper", 401), FlagDuplicateLiquidation) {
		t.Fatalf("duplicate liquidation was not marked: %v", g.Flags("keeper", 401))
	}
	if got := len(g.Context("keeper", 401).Liquidations); got != 1 {
		t.Fatalf("failed duplicate left %d liquidation records, want 1", got)
	}
	req.PositionID = "victim-eth-renamed"
	if err := g.Liquidate(req); !errors.Is(err, ErrDuplicateLiquidation) {
		t.Fatalf("renamed duplicate liquidation error = %v, want ErrDuplicateLiquidation", err)
	}
	req.PositionID = "victim-eth"

	for _, block := range []uint64{402, 403} {
		mustBegin(t, g, block)
		if err := g.Liquidate(req); !errors.Is(err, ErrLiquidationCooldown) {
			t.Fatalf("block %d liquidation error = %v, want ErrLiquidationCooldown", block, err)
		}
	}
	mustBegin(t, g, 404)
	if err := g.Liquidate(req); err != nil {
		t.Fatalf("block 404 liquidation after cooldown: %v", err)
	}
}

func TestAbnormalProfitIsRejectedAndRolledBack(t *testing.T) {
	g := NewGuard(WithMaxProfitBps(500))
	mustPrice(t, g, 600, "USD", 1)

	loan := mustLoan(t, g, "arbitrageur", 1_000_000, 5)
	err := g.RepayFlashLoan(RepayRequest{
		LoanID: loan.ID,
		Amount: loan.Owed(),
		Profit: bigValue(50_001),
	})
	if !errors.Is(err, ErrAbnormalProfit) {
		t.Fatalf("abnormal profit error = %v, want ErrAbnormalProfit", err)
	}
	if active, ok := g.ActiveLoan("arbitrageur"); !ok || active.ID != loan.ID {
		t.Fatalf("failed repayment should leave the loan open, active=%+v ok=%v", active, ok)
	}
	if !hasFlag(g.Context("arbitrageur", 600).Flags, FlagAbnormalProfit) {
		t.Fatalf("abnormal profit was not marked: %v", g.Context("arbitrageur", 600).Flags)
	}

	if err := g.RepayFlashLoan(RepayRequest{
		LoanID: loan.ID,
		Amount: loan.Owed(),
		Profit: bigValue(50_000),
	}); err != nil {
		t.Fatalf("repayment at profit threshold: %v", err)
	}

	// Returning excess value without declaring Profit is treated as profit too.
	mustBegin(t, g, 601)
	second := mustLoan(t, g, "arbitrageur", 1_000_000, 5)
	overpay := new(big.Int).Add(second.Owed(), bigValue(50_001))
	if err := g.RepayFlashLoan(RepayRequest{LoanID: second.ID, Amount: overpay}); !errors.Is(err, ErrAbnormalProfit) {
		t.Fatalf("undeclared excess profit error = %v, want ErrAbnormalProfit", err)
	}
	if err := g.RepayFlashLoan(RepayRequest{LoanID: second.ID, Amount: second.Owed()}); err != nil {
		t.Fatalf("clean repayment: %v", err)
	}
}

func TestCrossBlockRepaymentAndBlockMismatch(t *testing.T) {
	g := NewGuard()
	mustPrice(t, g, 700, "USD", 1)
	loan := mustLoan(t, g, "borrower", 10_000, 0)

	mustBegin(t, g, 701)
	err := g.RepayFlashLoan(RepayRequest{LoanID: loan.ID})
	if !errors.Is(err, ErrCrossBlockRepayment) {
		t.Fatalf("cross-block repayment error = %v, want ErrCrossBlockRepayment", err)
	}
	if _, ok := g.ActiveLoan("borrower"); !ok {
		t.Fatal("cross-block repayment should not close the loan")
	}

	err = g.UpdatePrice(PriceUpdateRequest{Account: "oracle", Asset: "USD", Price: bigValue(2), Block: 1_000_000})
	if !errors.Is(err, ErrBlockMismatch) {
		t.Fatalf("future block request error = %v, want ErrBlockMismatch", err)
	}
	if got := g.CurrentBlock(); got != 701 {
		t.Fatalf("future block request advanced guard to %d, want 701", got)
	}
	if price, _ := g.Price("USD"); price.Price.Cmp(bigValue(1)) != 0 {
		t.Fatalf("rejected future-block update changed the price: %+v", price)
	}
}

func TestCollateralAndRepeatedLoanInSameBlockAreRejected(t *testing.T) {
	g := NewGuard()
	mustPrice(t, g, 800, "USD", 1)
	loan := mustLoan(t, g, "borrower", 10_000, 0)

	if err := g.DepositCollateral("borrower", "ETH", bigValue(5)); !errors.Is(err, ErrCollateralDuringLoan) {
		t.Fatalf("collateral during loan error = %v, want ErrCollateralDuringLoan", err)
	}
	if err := g.RepayFlashLoan(RepayRequest{LoanID: loan.ID}); err != nil {
		t.Fatalf("repay: %v", err)
	}
	if _, err := g.BeginFlashLoan(FlashLoanRequest{
		Account: "borrower",
		Asset:   "USD",
		Amount:  bigValue(10_000),
	}); !errors.Is(err, ErrRepeatedFlashLoan) {
		t.Fatalf("second loan in same block error = %v, want ErrRepeatedFlashLoan", err)
	}

	// A price update first, then a loan, is the same composition in reverse.
	mustBegin(t, g, 801)
	if err := g.UpdatePrice(PriceUpdateRequest{Account: "attacker", Asset: "ETH", Price: bigValue(2)}); err != nil {
		t.Fatalf("price update at 801: %v", err)
	}
	if _, err := g.BeginFlashLoan(FlashLoanRequest{
		Account: "attacker",
		Asset:   "USD",
		Amount:  bigValue(10_000),
	}); !errors.Is(err, ErrSameBlockComposition) {
		t.Fatalf("price-then-loan error = %v, want ErrSameBlockComposition", err)
	}
}

func TestRunFlashLoanRequiresBalancedRepayment(t *testing.T) {
	g := NewGuard()
	mustPrice(t, g, 900, "USD", 1)

	err := g.RunFlashLoan(FlashLoanRequest{
		Account: "borrower",
		Asset:   "USD",
		Amount:  bigValue(1_000),
	}, func(*FlashLoan) error {
		return nil
	})
	if !errors.Is(err, ErrFlashLoanNotRepaid) {
		t.Fatalf("unbalanced flash loan error = %v, want ErrFlashLoanNotRepaid", err)
	}
	if _, ok := g.ActiveLoan("borrower"); ok {
		t.Fatal("unbalanced flash loan was not rolled back")
	}

	err = g.RunFlashLoan(FlashLoanRequest{
		Account: "borrower",
		Asset:   "USD",
		Amount:  bigValue(1_000),
	}, func(loan *FlashLoan) error {
		loan.ID = "tampered"
		return nil
	})
	if !errors.Is(err, ErrFlashLoanNotRepaid) {
		t.Fatalf("tampered snapshot error = %v, want ErrFlashLoanNotRepaid", err)
	}
	if _, ok := g.ActiveLoan("borrower"); ok {
		t.Fatal("tampered snapshot bypassed the repayment check")
	}

	err = g.RunFlashLoan(FlashLoanRequest{
		Account: "borrower",
		Asset:   "USD",
		Amount:  bigValue(1_000),
	}, func(loan *FlashLoan) error {
		return g.RepayFlashLoan(RepayRequest{LoanID: loan.ID})
	})
	if err != nil {
		t.Fatalf("balanced flash loan: %v", err)
	}
	if ctx := g.Context("borrower"); len(ctx.Loans) != 1 || len(ctx.Repayments) != 1 {
		t.Fatalf("balanced loan context incomplete: %+v", ctx)
	}
}

func TestRunAtomicRollsBackPanics(t *testing.T) {
	g := NewGuard()
	mustPrice(t, g, 1_000, "USD", 1)

	func() {
		defer func() {
			if recovered := recover(); recovered != "boom" {
				t.Fatalf("recovered = %v, want boom", recovered)
			}
		}()
		_ = g.RunAtomic(func() error {
			if _, err := g.BeginFlashLoan(FlashLoanRequest{
				Account: "borrower",
				Asset:   "USD",
				Amount:  bigValue(1_000),
			}); err != nil {
				return err
			}
			panic("boom")
		})
	}()

	if _, ok := g.ActiveLoan("borrower"); ok {
		t.Fatal("panic rollback left an active loan")
	}
	if got := g.Context("borrower", 1_000).OperationCount(); got != 0 {
		t.Fatalf("panic rollback left %d operations", got)
	}
}

func TestPriceDeviationLimit(t *testing.T) {
	g := NewGuard(WithPriceDeviationBps(100), WithPriceCooldownBlocks(0))
	mustPrice(t, g, 1_100, "ETH", 1_000)
	mustBegin(t, g, 1_101)
	if err := g.UpdatePrice(PriceUpdateRequest{Account: "oracle", Asset: "ETH", Price: bigValue(1_011)}); !errors.Is(err, ErrPriceDeviation) {
		t.Fatalf("price jump error = %v, want ErrPriceDeviation", err)
	}
	if err := g.UpdatePrice(PriceUpdateRequest{Account: "oracle", Asset: "ETH", Price: bigValue(1_010)}); err != nil {
		t.Fatalf("allowed price move: %v", err)
	}
}

func TestLoanLimitsUnderRepaymentAndBlockRegression(t *testing.T) {
	g := NewGuard(
		WithMaxFeeBps(5),
		WithMaxFlashLoanAmount(bigValue(1_000)),
	)
	mustPrice(t, g, 1_500, "USD", 1)
	mustBegin(t, g, 1_501)

	if _, err := g.BeginFlashLoan(FlashLoanRequest{
		Account: "borrower",
		Asset:   "USD",
		Amount:  bigValue(500),
		FeeBps:  6,
	}); !errors.Is(err, ErrFeeTooHigh) {
		t.Fatalf("fee cap error = %v, want ErrFeeTooHigh", err)
	}
	if _, err := g.BeginFlashLoan(FlashLoanRequest{
		Account: "borrower",
		Asset:   "USD",
		Amount:  bigValue(1_001),
	}); !errors.Is(err, ErrFlashLoanLimit) {
		t.Fatalf("loan cap error = %v, want ErrFlashLoanLimit", err)
	}
	if _, err := g.BeginFlashLoan(FlashLoanRequest{
		Account: "borrower",
		Asset:   "USD",
		Amount:  nil,
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("nil amount error = %v, want ErrInvalidArgument", err)
	}

	loan := mustLoan(t, g, "borrower", 1_000, 5)
	if err := g.RepayFlashLoan(RepayRequest{LoanID: loan.ID, Amount: bigValue(1_000)}); !errors.Is(err, ErrUnderRepayment) {
		t.Fatalf("under-repayment error = %v, want ErrUnderRepayment", err)
	}
	if _, ok := g.ActiveLoan("borrower"); !ok {
		t.Fatal("under-repayment closed the loan")
	}
	if err := g.RepayFlashLoan(RepayRequest{LoanID: loan.ID, Amount: loan.Owed()}); err != nil {
		t.Fatalf("exact repayment: %v", err)
	}
	if err := g.BeginBlock(1_499); !errors.Is(err, ErrBlockRegression) {
		t.Fatalf("block regression error = %v, want ErrBlockRegression", err)
	}
}

func TestSnapshotRestoresComplexState(t *testing.T) {
	g := NewGuard(WithLiquidationCooldownBlocks(2))
	mustPrice(t, g, 1_600, "ETH", 2_000)
	mustBegin(t, g, 1_601)
	if err := g.Liquidate(LiquidationRequest{
		Liquidator:      "keeper",
		Borrower:        "victim",
		PositionID:      "victim-eth",
		CollateralAsset: "ETH",
		DebtAsset:       "USD",
		DebtAmount:      bigValue(100),
	}); err != nil {
		t.Fatalf("liquidation: %v", err)
	}
	mustBegin(t, g, 1_602)
	loan := mustLoan(t, g, "borrower", 1_000, 0)

	before := g.Snapshot()
	if err := g.RepayFlashLoan(RepayRequest{LoanID: loan.ID}); err != nil {
		t.Fatalf("repay: %v", err)
	}
	mustBegin(t, g, 1_603)
	if err := g.SetPrice("ETH", bigValue(3_000)); err != nil {
		t.Fatalf("price update: %v", err)
	}

	if err := g.Restore(before); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := g.CurrentBlock(); got != 1_602 {
		t.Fatalf("restored block = %d, want 1602", got)
	}
	if active, ok := g.ActiveLoan("borrower"); !ok || active.ID != loan.ID {
		t.Fatalf("restored active loan = %+v, ok=%v", active, ok)
	}
	if ctx := g.Context("borrower", 1_602); len(ctx.Loans) != 1 || len(ctx.Repayments) != 0 {
		t.Fatalf("restored loan context = %+v", ctx)
	}
	if ctx := g.Context("keeper", 1_601); len(ctx.Liquidations) != 1 {
		t.Fatalf("restored liquidation context = %+v", ctx)
	}
	if price, _ := g.Price("ETH"); price.Block != 1_600 || price.Price.Cmp(bigValue(2_000)) != 0 {
		t.Fatalf("restored price = %+v", price)
	}
}

func TestAutoLoanIDDoesNotOverwriteCustomID(t *testing.T) {
	g := NewGuard()
	mustPrice(t, g, 1_700, "USD", 1)
	mustBegin(t, g, 1_701)

	first, err := g.BeginFlashLoan(FlashLoanRequest{
		LoanID:  "loan-1",
		Account: "alice",
		Asset:   "USD",
		Amount:  bigValue(100),
	})
	if err != nil {
		t.Fatalf("custom loan: %v", err)
	}
	if err := g.RepayFlashLoan(RepayRequest{LoanID: first.ID}); err != nil {
		t.Fatalf("repay custom loan: %v", err)
	}

	mustBegin(t, g, 1_702)
	second := mustLoan(t, g, "bob", 100, 0)
	if second.ID == first.ID {
		t.Fatalf("auto loan reused custom ID %q", first.ID)
	}
	if ctx := g.Context("alice", 1_701); len(ctx.Loans) != 1 || ctx.Loans[0].LoanID != first.ID {
		t.Fatalf("custom loan context was overwritten: %+v", ctx)
	}
}

func TestContextsAreSeparatedByAccountAndBlock(t *testing.T) {
	g := NewGuard()
	mustPrice(t, g, 1_200, "USD", 1)

	if err := g.DepositCollateral("alice", "ETH", bigValue(10)); err != nil {
		t.Fatalf("deposit: %v", err)
	}
	mustBegin(t, g, 1_201)
	loan := mustLoan(t, g, "alice", 100, 0)
	if err := g.RepayFlashLoan(RepayRequest{LoanID: loan.ID}); err != nil {
		t.Fatalf("repay: %v", err)
	}

	at1200 := g.Context("alice", 1_200)
	at1201 := g.Context("alice", 1_201)
	if len(at1200.Collateral) != 1 || at1200.HasFlashLoan() {
		t.Fatalf("block 1200 context = %+v", at1200)
	}
	if at1201.HasCollateral() || len(at1201.Loans) != 1 || len(at1201.Repayments) != 1 {
		t.Fatalf("block 1201 context = %+v", at1201)
	}
	if other := g.Context("bob", 1_201); other.OperationCount() != 0 {
		t.Fatalf("other account context leaked: %+v", other)
	}
}

func TestOptInCollateralIsAggregatedInSameContext(t *testing.T) {
	g := NewGuard(WithCollateralDuringFlashLoan(true))
	mustPrice(t, g, 1_300, "USD", 1)
	mustBegin(t, g, 1_301)

	if err := g.DepositCollateral("alice", "ETH", bigValue(10)); err != nil {
		t.Fatalf("deposit: %v", err)
	}
	loan := mustLoan(t, g, "alice", 100, 0)
	if err := g.DepositCollateral("alice", "ETH", bigValue(5)); err != nil {
		t.Fatalf("deposit during loan: %v", err)
	}
	if err := g.RepayFlashLoan(RepayRequest{LoanID: loan.ID}); err != nil {
		t.Fatalf("repay: %v", err)
	}

	ctx := g.Context("alice", 1_301)
	if len(ctx.Loans) != 1 || len(ctx.Repayments) != 1 || len(ctx.Collateral) != 2 {
		t.Fatalf("borrow/repay/collateral were not aggregated: %+v", ctx)
	}
}
