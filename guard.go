package netsec

import (
	"fmt"
	"math/big"
	"sort"
	"sync"
	"time"
)

// Guard is the importable flash-loan constraint component. Lending-pool code
// calls it before mutating balances or collateral. All methods are safe for
// concurrent use, except that a callback passed to RunAtomic must not be
// executed concurrently with other mutations of the same guard.
type Guard struct {
	mu         sync.Mutex
	cfg        Config
	state      *guardState
	detections []Detection
	nextDetect uint64
}

// FlashLoanGuard is an alias for Guard for callers that prefer a more
// descriptive type name.
type FlashLoanGuard = Guard

type contextKey struct {
	block   uint64
	account string
}

type guardState struct {
	blockStarted bool
	block        uint64
	contexts     map[contextKey]*accountContext
	loans        map[string]*loanState
	activeLoans  map[string]string
	prices       map[string]*priceState
	liquidations map[string]*liquidationState
	nextLoan     uint64
}

type accountContext struct {
	account      string
	block        uint64
	loans        []loanRecord
	repayments   []repaymentRecord
	collateral   []collateralRecord
	priceUpdates []priceUpdateRecord
	liquidations []liquidationRecord
	flags        map[Flag]struct{}
}

type loanRecord struct {
	loanID    string
	asset     string
	principal *big.Int
	feeBps    uint64
	fee       *big.Int
	block     uint64
}

type repaymentRecord struct {
	loanID string
	asset  string
	amount *big.Int
	profit *big.Int
	block  uint64
}

type collateralRecord struct {
	asset     string
	operation CollateralOperation
	amount    *big.Int
	block     uint64
}

type priceUpdateRecord struct {
	asset  string
	price  *big.Int
	source string
	block  uint64
}

type liquidationRecord struct {
	positionID      string
	borrower        string
	collateralAsset string
	debtAsset       string
	debtAmount      *big.Int
	seizedAmount    *big.Int
	block           uint64
}

type loanState struct {
	id                string
	account           string
	asset             string
	principal         *big.Int
	feeBps            uint64
	fee               *big.Int
	requiredRepayment *big.Int
	block             uint64
	open              bool
	repaid            *big.Int
}

type priceState struct {
	set     bool
	asset   string
	price   *big.Int
	block   uint64
	account string
	source  string
}

type liquidationState struct {
	block uint64
	count uint64
}

// NewGuard creates a guard. Without arguments it uses DefaultConfig. Both
// Option values and Config values are accepted and applied in order; Config
// fields set to their zero value keep the safe defaults.
func NewGuard(options ...Option) *Guard {
	cfg := DefaultConfig()
	for _, opt := range options {
		if opt == nil {
			continue
		}
		if ptr, ok := opt.(*Config); ok && ptr == nil {
			continue
		}
		opt.apply(&cfg)
	}
	return newGuard(cfg)
}

// NewFlashLoanGuard is an alias for NewGuard.
func NewFlashLoanGuard(options ...Option) *Guard { return NewGuard(options...) }

// NewGuardWithConfig is the explicit-config constructor. Zero-valued Config
// fields keep the safe defaults; use Options to disable a protection.
func NewGuardWithConfig(cfg Config) *Guard {
	base := DefaultConfig()
	cfg.apply(&base)
	return newGuard(base)
}

func newGuard(cfg Config) *Guard {
	cfg.MaxFlashLoanAmount = cloneInt(cfg.MaxFlashLoanAmount)
	return &Guard{
		cfg: cfg,
		state: &guardState{
			contexts:     make(map[contextKey]*accountContext),
			loans:        make(map[string]*loanState),
			activeLoans:  make(map[string]string),
			prices:       make(map[string]*priceState),
			liquidations: make(map[string]*liquidationState),
		},
	}
}

// Config returns a defensive copy of the guard's configuration.
func (g *Guard) Config() Config {
	g.mu.Lock()
	defer g.mu.Unlock()
	cfg := g.cfg
	cfg.MaxFlashLoanAmount = cloneInt(cfg.MaxFlashLoanAmount)
	return cfg
}

// BeginBlock advances the guard to block. Replaying an older block is rejected
// so that an attacker cannot resurrect an earlier cooldown-free context. The
// same block may be entered repeatedly.
func (g *Guard) BeginBlock(block uint64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.beginBlockLocked(block)
}

func (g *Guard) beginBlockLocked(block uint64) error {
	if g.state.blockStarted && block < g.state.block {
		return &Violation{
			Code:      CodeBlockRegression,
			Account:   "",
			Block:     block,
			Operation: "begin_block",
			Detail:    fmt.Sprintf("current block is %d", g.state.block),
			cause:     ErrBlockRegression,
		}
	}
	g.state.blockStarted = true
	g.state.block = block
	g.pruneLocked()
	return nil
}

// CurrentBlock reports the guard's current block.
func (g *Guard) CurrentBlock() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.state.block
}

func (g *Guard) resolveBlockLocked(requested uint64) (uint64, error) {
	if !g.state.blockStarted {
		g.state.blockStarted = true
		g.state.block = requested
		g.pruneLocked()
		return requested, nil
	}
	if requested == 0 {
		return g.state.block, nil
	}
	if requested != g.state.block {
		return 0, &Violation{
			Code:      CodeBlockMismatch,
			Block:     requested,
			Operation: "block_check",
			Detail:    fmt.Sprintf("active block is %d; call BeginBlock to advance", g.state.block),
			cause:     ErrBlockMismatch,
		}
	}
	return g.state.block, nil
}

func (g *Guard) activeLoanLocked(account string) string {
	return g.state.activeLoans[account]
}

func (g *Guard) pruneLocked() {
	retention := g.cfg.ContextRetentionBlocks
	if retention == 0 || g.state.block < retention {
		return
	}
	cutoff := g.state.block - retention
	for key := range g.state.contexts {
		if key.block < cutoff {
			delete(g.state.contexts, key)
		}
	}
}

func (g *Guard) contextLocked(block uint64, account string) *accountContext {
	key := contextKey{block: block, account: account}
	ctx := g.state.contexts[key]
	if ctx == nil {
		ctx = &accountContext{
			account: account,
			block:   block,
			flags:   make(map[Flag]struct{}),
		}
		g.state.contexts[key] = ctx
	}
	return ctx
}

func (g *Guard) findContextLocked(block uint64, account string) *accountContext {
	return g.state.contexts[contextKey{block: block, account: account}]
}

// BeginFlashLoan opens an unsecured flash loan. The account may have only one
// loan in a block, and it may not have performed a price, liquidation or
// collateral operation earlier in the same block.
func (g *Guard) BeginFlashLoan(req FlashLoanRequest) (*FlashLoan, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	block, err := g.resolveBlockLocked(req.Block)
	if err != nil {
		return nil, err
	}
	if req.Account == "" {
		return nil, g.rejectLocked(block, req.Account, "flash_loan_borrow", CodeInvalidArgument, ErrInvalidArgument, nil, "account is required")
	}
	if !positive(req.Amount) {
		return nil, g.rejectLocked(block, req.Account, "flash_loan_borrow", CodeInvalidArgument, ErrInvalidArgument, nil, "amount must be positive")
	}
	if req.FeeBps > g.cfg.MaxFeeBps {
		return nil, g.rejectLocked(block, req.Account, "flash_loan_borrow", CodeFeeTooHigh, ErrFeeTooHigh, nil,
			fmt.Sprintf("fee %d bps exceeds maximum %d bps", req.FeeBps, g.cfg.MaxFeeBps))
	}
	if g.cfg.MaxFlashLoanAmount != nil && req.Amount.Cmp(g.cfg.MaxFlashLoanAmount) > 0 {
		return nil, g.rejectLocked(block, req.Account, "flash_loan_borrow", CodeFlashLoanLimit, ErrFlashLoanLimit, nil,
			fmt.Sprintf("amount %s exceeds maximum %s", req.Amount, g.cfg.MaxFlashLoanAmount))
	}
	if activeID, ok := g.state.activeLoans[req.Account]; ok {
		return nil, g.rejectLocked(block, req.Account, "flash_loan_borrow", CodeFlashLoanActive, ErrFlashLoanActive, []Flag{FlagSameBlockComposition},
			fmt.Sprintf("loan %s is still open", activeID))
	}

	ctx := g.findContextLocked(block, req.Account)
	if ctx != nil && len(ctx.loans) > 0 {
		return nil, g.rejectLocked(block, req.Account, "flash_loan_borrow", CodeRepeatedFlashLoan, ErrRepeatedFlashLoan,
			[]Flag{FlagRepeatedFlashLoan, FlagSameBlockComposition}, "only one flash loan is allowed per account per block")
	}
	if ctx != nil && len(ctx.priceUpdates) > 0 {
		return nil, g.rejectLocked(block, req.Account, "flash_loan_borrow", CodeSameBlockComposition, ErrSameBlockComposition,
			[]Flag{FlagSameBlockComposition, FlagPriceManipulation}, "price was updated earlier in the same block")
	}
	if ctx != nil && len(ctx.liquidations) > 0 {
		return nil, g.rejectLocked(block, req.Account, "flash_loan_borrow", CodeSameBlockComposition, ErrSameBlockComposition,
			[]Flag{FlagSameBlockComposition}, "liquidation was performed earlier in the same block")
	}
	if ctx != nil && len(ctx.collateral) > 0 && !g.cfg.AllowCollateralDuringFlashLoan {
		return nil, g.rejectLocked(block, req.Account, "flash_loan_borrow", CodeSameBlockComposition, ErrSameBlockComposition,
			[]Flag{FlagSameBlockComposition, FlagCollateralDuringLoan}, "collateral changed earlier in the same block")
	}

	id := req.LoanID
	if id == "" {
		for {
			g.state.nextLoan++
			id = fmt.Sprintf("loan-%d", g.state.nextLoan)
			if _, exists := g.state.loans[id]; !exists {
				break
			}
		}
	} else if _, exists := g.state.loans[id]; exists {
		return nil, g.rejectLocked(block, req.Account, "flash_loan_borrow", CodeInvalidArgument, ErrInvalidArgument, nil,
			fmt.Sprintf("loan id %q already exists", id))
	}

	fee := bpsOf(req.Amount, req.FeeBps)
	required := new(big.Int).Add(cloneInt(req.Amount), fee)
	loan := &loanState{
		id:                id,
		account:           req.Account,
		asset:             req.Asset,
		principal:         cloneInt(req.Amount),
		feeBps:            req.FeeBps,
		fee:               cloneInt(fee),
		requiredRepayment: required,
		block:             block,
		open:              true,
		repaid:            new(big.Int),
	}
	g.state.loans[id] = loan
	g.state.activeLoans[req.Account] = id
	ctx = g.contextLocked(block, req.Account)
	ctx.loans = append(ctx.loans, loanRecord{
		loanID:    id,
		asset:     req.Asset,
		principal: cloneInt(req.Amount),
		feeBps:    req.FeeBps,
		fee:       cloneInt(fee),
		block:     block,
	})
	return loan.snapshot(), nil
}

// BorrowFlashLoan is a convenience wrapper around BeginFlashLoan using the
// guard's current block and a zero fee. The pool should normally call
// BeginFlashLoan directly so it can pass the fee recorded in the loan terms.
func (g *Guard) BorrowFlashLoan(account, asset string, amount *big.Int) (*FlashLoan, error) {
	return g.BeginFlashLoan(FlashLoanRequest{Account: account, Asset: asset, Amount: amount})
}

// Borrow is an alias for BorrowFlashLoan.
func (g *Guard) Borrow(account, asset string, amount *big.Int) (*FlashLoan, error) {
	return g.BorrowFlashLoan(account, asset, amount)
}

// RepayFlashLoan closes a loan. A loan opened in block N must be closed in
// block N. Under-repayment, cross-block repayment and excessive declared
// profit are rejected without changing state.
func (g *Guard) RepayFlashLoan(req RepayRequest) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	block, err := g.resolveBlockLocked(req.Block)
	if err != nil {
		return err
	}
	loan, err := g.loanForRepaymentLocked(req, block)
	if err != nil {
		return err
	}
	if !loan.open {
		return g.rejectLocked(block, req.Account, "flash_loan_repay", CodeInvalidArgument, ErrInvalidArgument, nil, "loan is already closed")
	}
	if g.cfg.RequireSameBlockRepayment && loan.block != block {
		return g.rejectLocked(block, loan.account, "flash_loan_repay", CodeCrossBlockRepayment, ErrCrossBlockRepayment,
			[]Flag{FlagCrossBlockComposition, FlagFlashLoanNotRepaid},
			fmt.Sprintf("loan opened in block %d but repayment is in block %d", loan.block, block))
	}

	amount := cloneInt(req.Amount)
	if amount == nil {
		amount = cloneInt(loan.requiredRepayment)
	}
	if amount.Cmp(loan.requiredRepayment) < 0 {
		return g.rejectLocked(block, loan.account, "flash_loan_repay", CodeUnderRepayment, ErrUnderRepayment, nil,
			fmt.Sprintf("repayment %s is below required %s", amount, loan.requiredRepayment))
	}
	if req.Profit != nil && req.Profit.Sign() < 0 {
		return g.rejectLocked(block, loan.account, "flash_loan_repay", CodeInvalidArgument, ErrInvalidArgument, nil, "profit must not be negative")
	}
	allowedProfit := bpsFloor(loan.principal, g.cfg.MaxProfitBps)
	profit := cloneInt(req.Profit)
	excess := new(big.Int).Sub(amount, loan.requiredRepayment)
	if profit == nil {
		profit = excess
	} else if excess.Cmp(profit) > 0 {
		profit = excess
	}
	if profit.Cmp(allowedProfit) > 0 {
		return g.rejectLocked(block, loan.account, "flash_loan_repay", CodeAbnormalProfit, ErrAbnormalProfit,
			[]Flag{FlagAbnormalProfit, FlagSameBlockComposition},
			fmt.Sprintf("profit %s exceeds allowed %s", profit, allowedProfit))
	}

	loan.open = false
	loan.repaid = cloneInt(amount)
	delete(g.state.activeLoans, loan.account)
	ctx := g.contextLocked(block, loan.account)
	ctx.repayments = append(ctx.repayments, repaymentRecord{
		loanID: loan.id,
		asset:  loan.asset,
		amount: cloneInt(amount),
		profit: cloneInt(profit),
		block:  block,
	})
	return nil
}

func (g *Guard) loanForRepaymentLocked(req RepayRequest, block uint64) (*loanState, error) {
	id := req.LoanID
	account := req.Account
	if id == "" {
		active, ok := g.state.activeLoans[account]
		if !ok {
			return nil, g.rejectLocked(block, account, "flash_loan_repay", CodeInvalidArgument, ErrInvalidArgument, nil, "no open flash loan for account")
		}
		id = active
	}
	loan := g.state.loans[id]
	if loan == nil {
		return nil, g.rejectLocked(block, account, "flash_loan_repay", CodeInvalidArgument, ErrInvalidArgument, nil, fmt.Sprintf("loan %q not found", id))
	}
	if account != "" && account != loan.account {
		return nil, g.rejectLocked(block, account, "flash_loan_repay", CodeInvalidArgument, ErrInvalidArgument, nil, "repayment account does not own the loan")
	}
	return loan, nil
}

// Repay is a convenience wrapper around RepayFlashLoan.
func (g *Guard) Repay(loanID string, amount *big.Int) error {
	return g.RepayFlashLoan(RepayRequest{LoanID: loanID, Amount: amount})
}

// Collateral records a collateral deposit or withdrawal. While a flash loan
// is in its account context, collateral changes are rejected by default. A
// same-block price update also blocks collateral changes, preventing
// manipulate-then-withdraw sequences.
func (g *Guard) Collateral(req CollateralRequest) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	block, err := g.resolveBlockLocked(req.Block)
	if err != nil {
		return err
	}
	if req.Account == "" {
		return g.rejectLocked(block, req.Account, "collateral", CodeInvalidArgument, ErrInvalidArgument, nil, "account is required")
	}
	if req.Operation != CollateralDeposit && req.Operation != CollateralWithdraw {
		return g.rejectLocked(block, req.Account, "collateral", CodeInvalidArgument, ErrInvalidArgument, nil, "operation must be deposit or withdraw")
	}
	if !positive(req.Amount) {
		return g.rejectLocked(block, req.Account, "collateral", CodeInvalidArgument, ErrInvalidArgument, nil, "amount must be positive")
	}

	ctx := g.findContextLocked(block, req.Account)
	if ctx != nil && len(ctx.loans) > 0 && !g.cfg.AllowCollateralDuringFlashLoan {
		return g.rejectLocked(block, req.Account, "collateral", CodeCollateralDuringLoan, ErrCollateralDuringLoan,
			[]Flag{FlagCollateralDuringLoan, FlagSameBlockComposition}, "collateral cannot change while a flash loan is in this block context")
	}
	if ctx != nil && len(ctx.priceUpdates) > 0 {
		return g.rejectLocked(block, req.Account, "collateral", CodeSameBlockComposition, ErrSameBlockComposition,
			[]Flag{FlagSameBlockComposition, FlagPriceManipulation}, "collateral cannot change after a same-block price update")
	}
	if id := g.activeLoanLocked(req.Account); id != "" && !g.cfg.AllowCollateralDuringFlashLoan {
		return g.rejectLocked(block, req.Account, "collateral", CodeCollateralDuringLoan, ErrCollateralDuringLoan,
			[]Flag{FlagCollateralDuringLoan, FlagSameBlockComposition}, fmt.Sprintf("loan %s is still open", id))
	}

	ctx = g.contextLocked(block, req.Account)
	ctx.collateral = append(ctx.collateral, collateralRecord{
		asset:     req.Asset,
		operation: req.Operation,
		amount:    cloneInt(req.Amount),
		block:     block,
	})
	return nil
}

func (g *Guard) DepositCollateral(account, asset string, amount *big.Int) error {
	return g.Collateral(CollateralRequest{Account: account, Asset: asset, Operation: CollateralDeposit, Amount: amount})
}

func (g *Guard) WithdrawCollateral(account, asset string, amount *big.Int) error {
	return g.Collateral(CollateralRequest{Account: account, Asset: asset, Operation: CollateralWithdraw, Amount: amount})
}

// UpdatePrice accepts an oracle price. It enforces a per-asset cooldown, an
// optional maximum deviation, and rejects price updates by an account that has
// a flash loan or liquidation in the same block.
func (g *Guard) UpdatePrice(req PriceUpdateRequest) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	block, err := g.resolveBlockLocked(req.Block)
	if err != nil {
		return err
	}
	if req.Asset == "" {
		return g.rejectLocked(block, req.Account, "price_update", CodeInvalidArgument, ErrInvalidArgument, nil, "asset is required")
	}
	if !positive(req.Price) {
		return g.rejectLocked(block, req.Account, "price_update", CodeInvalidArgument, ErrInvalidArgument, nil, "price must be positive")
	}

	if req.Account != "" {
		ctx := g.findContextLocked(block, req.Account)
		if ctx != nil {
			if len(ctx.loans) > 0 {
				return g.rejectLocked(block, req.Account, "price_update", CodePriceManipulation, ErrPriceManipulation,
					[]Flag{FlagPriceManipulation, FlagSameBlockComposition}, "price update from an account with a flash loan in the same block")
			}
			if len(ctx.liquidations) > 0 {
				return g.rejectLocked(block, req.Account, "price_update", CodeSameBlockComposition, ErrSameBlockComposition,
					[]Flag{FlagSameBlockComposition, FlagPriceManipulation}, "price update after liquidation in the same block")
			}
		}
		if id := g.activeLoanLocked(req.Account); id != "" {
			return g.rejectLocked(block, req.Account, "price_update", CodePriceManipulation, ErrPriceManipulation,
				[]Flag{FlagPriceManipulation, FlagSameBlockComposition}, fmt.Sprintf("account has an open flash loan %s", id))
		}
	}

	current := g.state.prices[req.Asset]
	if current != nil && current.set {
		if withinCooldown(block, current.block, g.cfg.PriceCooldownBlocks) {
			return g.rejectLocked(block, req.Account, "price_update", CodePriceCooldown, ErrPriceCooldown,
				[]Flag{FlagPriceCooldown, FlagPriceManipulation},
				fmt.Sprintf("price last updated in block %d, cooldown is %d blocks", current.block, g.cfg.PriceCooldownBlocks))
		}
		if g.cfg.PriceDeviationBps > 0 {
			diff := new(big.Int).Sub(req.Price, current.price)
			diff.Abs(diff)
			limit := bpsFloor(current.price, g.cfg.PriceDeviationBps)
			if diff.Cmp(limit) > 0 {
				return g.rejectLocked(block, req.Account, "price_update", CodePriceDeviation, ErrPriceDeviation,
					[]Flag{FlagPriceDeviation, FlagPriceManipulation},
					fmt.Sprintf("price moved by %s, limit is %s", diff, limit))
			}
		}
	}

	next := &priceState{
		set:     true,
		asset:   req.Asset,
		price:   cloneInt(req.Price),
		block:   block,
		account: req.Account,
		source:  req.Source,
	}
	g.state.prices[req.Asset] = next
	if req.Account != "" {
		ctx := g.contextLocked(block, req.Account)
		ctx.priceUpdates = append(ctx.priceUpdates, priceUpdateRecord{
			asset:  req.Asset,
			price:  cloneInt(req.Price),
			source: req.Source,
			block:  block,
		})
	}
	return nil
}

// RecordPrice is a convenience wrapper around UpdatePrice.
func (g *Guard) RecordPrice(account, asset string, price *big.Int) error {
	return g.UpdatePrice(PriceUpdateRequest{Account: account, Asset: asset, Price: price})
}

// SetPrice is a convenience method for protocol-level price bootstrap. The
// account field is left empty; all cooldown and deviation checks still apply.
func (g *Guard) SetPrice(asset string, price *big.Int) error {
	return g.UpdatePrice(PriceUpdateRequest{Asset: asset, Price: price, Source: "protocol"})
}

// Liquidate records a liquidation after checking flash-loan composition,
// price maturity, and the per-position liquidation cooldown.
func (g *Guard) Liquidate(req LiquidationRequest) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	block, err := g.resolveBlockLocked(req.Block)
	if err != nil {
		return err
	}
	if req.Liquidator == "" || req.Borrower == "" {
		return g.rejectLocked(block, req.Liquidator, "liquidation", CodeInvalidArgument, ErrInvalidArgument, nil, "liquidator and borrower are required")
	}
	if req.Liquidator == req.Borrower {
		return g.rejectLocked(block, req.Liquidator, "liquidation", CodeInvalidArgument, ErrInvalidArgument, nil, "self-liquidation is not allowed")
	}
	if !positive(req.DebtAmount) {
		return g.rejectLocked(block, req.Liquidator, "liquidation", CodeInvalidArgument, ErrInvalidArgument, nil, "debt amount must be positive")
	}

	liquidatorCtx := g.findContextLocked(block, req.Liquidator)
	if liquidatorCtx != nil {
		if len(liquidatorCtx.loans) > 0 {
			return g.rejectLocked(block, req.Liquidator, "liquidation", CodeSameBlockComposition, ErrSameBlockComposition,
				[]Flag{FlagSameBlockComposition}, "liquidator has a flash loan in the same block")
		}
		if len(liquidatorCtx.priceUpdates) > 0 {
			return g.rejectLocked(block, req.Liquidator, "liquidation", CodePriceManipulation, ErrPriceManipulation,
				[]Flag{FlagPriceManipulation, FlagSameBlockComposition}, "liquidator updated a price in the same block")
		}
	}
	if id := g.activeLoanLocked(req.Liquidator); id != "" {
		return g.rejectLocked(block, req.Liquidator, "liquidation", CodeSameBlockComposition, ErrSameBlockComposition,
			[]Flag{FlagSameBlockComposition}, fmt.Sprintf("liquidator has an open flash loan %s", id))
	}
	if id := g.activeLoanLocked(req.Borrower); id != "" {
		return g.rejectLocked(block, req.Liquidator, "liquidation", CodeSameBlockComposition, ErrSameBlockComposition,
			[]Flag{FlagSameBlockComposition}, fmt.Sprintf("borrower has an open flash loan %s", id))
	}
	if borrowerCtx := g.findContextLocked(block, req.Borrower); borrowerCtx != nil && len(borrowerCtx.loans) > 0 {
		return g.rejectLocked(block, req.Liquidator, "liquidation", CodeSameBlockComposition, ErrSameBlockComposition,
			[]Flag{FlagSameBlockComposition}, "borrower has a flash loan in the same block")
	}

	if g.cfg.RequireMaturePrice {
		for _, asset := range []string{req.CollateralAsset, req.DebtAsset} {
			if asset == "" {
				continue
			}
			if price := g.state.prices[asset]; price != nil && price.set {
				if withinCooldown(block, price.block, g.cfg.PriceCooldownBlocks) {
					return g.rejectLocked(block, req.Liquidator, "liquidation", CodePriceCooldown, ErrPriceCooldown,
						[]Flag{FlagPriceCooldown, FlagPriceManipulation},
						fmt.Sprintf("price for %s was updated in block %d and is not mature", asset, price.block))
				}
			}
		}
	}

	positionID := req.PositionID
	if positionID == "" {
		positionID = req.Borrower + "|" + req.CollateralAsset + "|" + req.DebtAsset
	}
	// Enforce the cooldown against both the caller-supplied position ID and the
	// canonical borrower/asset tuple. A caller cannot bypass duplicate
	// liquidation detection by inventing a new ID for the same position.
	canonicalKey := fmt.Sprintf("canonical:%q|%q|%q", req.Borrower, req.CollateralAsset, req.DebtAsset)
	keys := []string{canonicalKey}
	positionKey := "position:" + positionID
	if positionKey != canonicalKey {
		keys = append(keys, positionKey)
	}
	if prior := g.latestLiquidationLocked(keys); prior != nil {
		if prior.block == block {
			return g.rejectLocked(block, req.Liquidator, "liquidation", CodeDuplicateLiquidation, ErrDuplicateLiquidation,
				[]Flag{FlagDuplicateLiquidation, FlagLiquidationCooldown},
				fmt.Sprintf("position %q was already liquidated in this block", positionID))
		}
		if withinCooldown(block, prior.block, g.cfg.LiquidationCooldownBlocks) {
			return g.rejectLocked(block, req.Liquidator, "liquidation", CodeLiquidationCooldown, ErrLiquidationCooldown,
				[]Flag{FlagLiquidationCooldown},
				fmt.Sprintf("position %q was liquidated in block %d, cooldown is %d blocks", positionID, prior.block, g.cfg.LiquidationCooldownBlocks))
		}
	}

	ctx := g.contextLocked(block, req.Liquidator)
	ctx.liquidations = append(ctx.liquidations, liquidationRecord{
		positionID:      positionID,
		borrower:        req.Borrower,
		collateralAsset: req.CollateralAsset,
		debtAsset:       req.DebtAsset,
		debtAmount:      cloneInt(req.DebtAmount),
		seizedAmount:    cloneInt(req.SeizedAmount),
		block:           block,
	})
	for _, key := range keys {
		prior := g.state.liquidations[key]
		if prior == nil {
			g.state.liquidations[key] = &liquidationState{block: block, count: 1}
		} else {
			prior.block = block
			prior.count++
		}
	}
	return nil
}

func (g *Guard) latestLiquidationLocked(keys []string) *liquidationState {
	var latest *liquidationState
	for _, key := range keys {
		prior := g.state.liquidations[key]
		if prior == nil {
			continue
		}
		if latest == nil || prior.block > latest.block {
			latest = prior
		}
	}
	return latest
}

// Context returns a snapshot of the account's context for the current block,
// or for the optional explicit block.
func (g *Guard) Context(account string, block ...uint64) AccountContext {
	g.mu.Lock()
	defer g.mu.Unlock()
	b := g.state.block
	if len(block) > 0 {
		b = block[0]
	}
	return g.contextSnapshotLocked(b, account)
}

// ContextsAt returns all account contexts for a block, ordered by account.
func (g *Guard) ContextsAt(block uint64) []AccountContext {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]AccountContext, 0)
	seen := make(map[string]struct{})
	for key := range g.state.contexts {
		if key.block == block {
			out = append(out, g.contextSnapshotLocked(block, key.account))
			seen[key.account] = struct{}{}
		}
	}
	for _, detection := range g.detections {
		if detection.Block != block || detection.Account == "" {
			continue
		}
		if _, ok := seen[detection.Account]; ok {
			continue
		}
		out = append(out, g.contextSnapshotLocked(block, detection.Account))
		seen[detection.Account] = struct{}{}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Account < out[j].Account })
	return out
}

func (g *Guard) contextSnapshotLocked(block uint64, account string) AccountContext {
	out := AccountContext{Account: account, Block: block}
	flagSet := make(map[Flag]struct{})
	ctx := g.findContextLocked(block, account)
	if ctx == nil {
		g.mergeDetectionFlagsLocked(block, account, flagSet)
		out.Flags = cloneFlagsSet(flagSet)
		return out
	}
	for _, item := range ctx.loans {
		open := false
		if loan := g.state.loans[item.loanID]; loan != nil {
			open = loan.open
		}
		out.Loans = append(out.Loans, FlashLoanRecord{
			LoanID:    item.loanID,
			Asset:     item.asset,
			Principal: cloneInt(item.principal),
			FeeBps:    item.feeBps,
			Fee:       cloneInt(item.fee),
			Block:     item.block,
			Open:      open,
		})
	}
	for _, item := range ctx.repayments {
		out.Repayments = append(out.Repayments, RepaymentRecord{
			LoanID: item.loanID,
			Asset:  item.asset,
			Amount: cloneInt(item.amount),
			Profit: cloneInt(item.profit),
			Block:  item.block,
		})
	}
	for _, item := range ctx.collateral {
		out.Collateral = append(out.Collateral, CollateralRecord{
			Asset:     item.asset,
			Operation: item.operation,
			Amount:    cloneInt(item.amount),
			Block:     item.block,
		})
	}
	for _, item := range ctx.priceUpdates {
		out.PriceUpdates = append(out.PriceUpdates, PriceUpdateRecord{
			Asset:  item.asset,
			Price:  cloneInt(item.price),
			Source: item.source,
			Block:  item.block,
		})
	}
	for _, item := range ctx.liquidations {
		out.Liquidations = append(out.Liquidations, LiquidationRecord{
			PositionID:      item.positionID,
			Borrower:        item.borrower,
			CollateralAsset: item.collateralAsset,
			DebtAsset:       item.debtAsset,
			DebtAmount:      cloneInt(item.debtAmount),
			SeizedAmount:    cloneInt(item.seizedAmount),
			Block:           item.block,
		})
	}
	flagSet = make(map[Flag]struct{}, len(ctx.flags))
	for flag := range ctx.flags {
		flagSet[flag] = struct{}{}
	}
	g.mergeDetectionFlagsLocked(block, account, flagSet)
	out.Flags = cloneFlagsSet(flagSet)
	return out
}

func (g *Guard) mergeDetectionFlagsLocked(block uint64, account string, into map[Flag]struct{}) {
	for _, d := range g.detections {
		if d.Account != account || d.Block != block {
			continue
		}
		for _, flag := range d.Flags {
			into[flag] = struct{}{}
		}
	}
}

// ActiveLoan returns the account's open loan, if any.
func (g *Guard) ActiveLoan(account string) (*FlashLoan, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	id, ok := g.state.activeLoans[account]
	if !ok {
		return nil, false
	}
	loan := g.state.loans[id]
	if loan == nil {
		return nil, false
	}
	return loan.snapshot(), true
}

// Price returns the last accepted price for an asset.
func (g *Guard) Price(asset string) (PriceSnapshot, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	p := g.state.prices[asset]
	if p == nil || !p.set {
		return PriceSnapshot{}, false
	}
	return PriceSnapshot{
		Asset:   p.asset,
		Price:   cloneInt(p.price),
		Block:   p.block,
		Account: p.account,
		Source:  p.source,
	}, true
}

// Detections returns a copy of the append-only risk ledger.
func (g *Guard) Detections() []Detection {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]Detection, len(g.detections))
	for i, d := range g.detections {
		out[i] = d
		out[i].Flags = cloneFlags(d.Flags)
	}
	return out
}

// Flags returns the risk flags observed for an account at a block. Unlike
// AccountContext.Flags, this reads the append-only detection ledger and
// therefore still reports a rejected exploit after state rollback.
func (g *Guard) Flags(account string, block uint64) []Flag {
	g.mu.Lock()
	defer g.mu.Unlock()
	seen := make(map[Flag]struct{})
	g.mergeDetectionFlagsLocked(block, account, seen)
	return cloneFlagsSet(seen)
}

// ClearDetections clears the append-only telemetry ledger. It does not change
// protocol state or cooldowns.
func (g *Guard) ClearDetections() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.detections = nil
}

// Snapshot captures a deep copy of protocol state for later Restore.
func (g *Guard) Snapshot() *Snapshot {
	g.mu.Lock()
	defer g.mu.Unlock()
	return &Snapshot{state: g.state.clone()}
}

// Restore replaces protocol state with a previous snapshot. Detection
// telemetry is intentionally preserved.
func (g *Guard) Restore(snapshot *Snapshot) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if snapshot == nil || snapshot.state == nil {
		return &Violation{Code: CodeInvalidArgument, Operation: "restore", Detail: "nil snapshot", cause: ErrInvalidArgument}
	}
	g.state = snapshot.state.clone()
	return nil
}

// RunAtomic executes fn and restores the complete protocol state if fn
// returns an error or panics. It is the recommended wrapper for a lending-pool
// transaction that performs several guard calls. The callback must not be run
// concurrently with another mutation of the same guard.
func (g *Guard) RunAtomic(fn func() error) (err error) {
	if fn == nil {
		return &Violation{Code: CodeInvalidArgument, Operation: "run_atomic", Detail: "nil callback", cause: ErrInvalidArgument}
	}
	g.mu.Lock()
	snapshot := g.state.clone()
	g.mu.Unlock()

	defer func() {
		if recovered := recover(); recovered != nil {
			g.mu.Lock()
			g.state = snapshot
			g.mu.Unlock()
			panic(recovered)
		}
		if err != nil {
			g.mu.Lock()
			g.state = snapshot
			g.mu.Unlock()
		}
	}()
	return fn()
}

// AtBlock runs fn at the given block atomically. If fn fails, both the
// operations and the block transition are rolled back.
func (g *Guard) AtBlock(block uint64, fn func() error) error {
	return g.RunAtomic(func() error {
		if err := g.BeginBlock(block); err != nil {
			return err
		}
		return fn()
	})
}

// RunFlashLoan borrows, invokes body, and then verifies that the loan was
// repaid. Any error, panic or unbalanced loan rolls back the whole operation.
func (g *Guard) RunFlashLoan(req FlashLoanRequest, body func(*FlashLoan) error) error {
	return g.RunAtomic(func() error {
		loan, err := g.BeginFlashLoan(req)
		if err != nil {
			return err
		}
		// The callback receives a defensive snapshot. Save the immutable
		// identity before invoking untrusted code so mutation of the snapshot
		// cannot bypass the repayment check below.
		loanID := loan.ID
		account := loan.Account
		loanBlock := loan.Block
		if body != nil {
			if err := body(loan); err != nil {
				return err
			}
		}
		active, ok := g.ActiveLoan(account)
		if ok && active.ID == loanID {
			v := &Violation{
				Code:      CodeFlashLoanNotRepaid,
				Flags:     []Flag{FlagFlashLoanNotRepaid, FlagSameBlockComposition},
				Account:   account,
				Block:     loanBlock,
				Operation: "run_flash_loan",
				Detail:    "callback completed without repaying the loan",
				cause:     ErrFlashLoanNotRepaid,
			}
			g.recordDetection(v)
			return v
		}
		return nil
	})
}

func (g *Guard) recordDetection(v *Violation) {
	if v == nil || len(v.Flags) == 0 {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.appendDetectionLocked(v)
}

func (g *Guard) rejectLocked(block uint64, account, operation string, code Code, cause error, flags []Flag, detail string) error {
	v := &Violation{
		Code:      code,
		Flags:     cloneFlags(flags),
		Account:   account,
		Block:     block,
		Operation: operation,
		Detail:    detail,
		cause:     cause,
	}
	if len(v.Flags) > 0 {
		g.appendDetectionLocked(v)
	}
	return v
}

func (g *Guard) appendDetectionLocked(v *Violation) {
	g.nextDetect++
	g.detections = append(g.detections, Detection{
		Sequence:  g.nextDetect,
		Time:      time.Now().UTC(),
		Block:     v.Block,
		Account:   v.Account,
		Operation: v.Operation,
		Code:      v.Code,
		Flags:     cloneFlags(v.Flags),
		Detail:    v.Detail,
	})
}

func bpsOf(amount *big.Int, bps uint64) *big.Int {
	out := new(big.Int).Set(amount)
	out.Mul(out, new(big.Int).SetUint64(bps))
	out.Add(out, big.NewInt(9999))
	out.Quo(out, big.NewInt(10000))
	return out
}

func bpsFloor(amount *big.Int, bps uint64) *big.Int {
	out := new(big.Int).Set(amount)
	out.Mul(out, new(big.Int).SetUint64(bps))
	out.Quo(out, big.NewInt(10000))
	return out
}

func withinCooldown(current, last, cooldown uint64) bool {
	if cooldown == 0 || current < last {
		return false
	}
	return current-last < cooldown
}

func (l *loanState) snapshot() *FlashLoan {
	if l == nil {
		return nil
	}
	return &FlashLoan{
		ID:                l.id,
		Account:           l.account,
		Asset:             l.asset,
		Principal:         cloneInt(l.principal),
		FeeBps:            l.feeBps,
		Fee:               cloneInt(l.fee),
		RequiredRepayment: cloneInt(l.requiredRepayment),
		Block:             l.block,
		Open:              l.open,
		Repaid:            cloneInt(l.repaid),
	}
}

func (s *guardState) clone() *guardState {
	if s == nil {
		return nil
	}
	out := &guardState{
		blockStarted: s.blockStarted,
		block:        s.block,
		contexts:     make(map[contextKey]*accountContext, len(s.contexts)),
		loans:        make(map[string]*loanState, len(s.loans)),
		activeLoans:  make(map[string]string, len(s.activeLoans)),
		prices:       make(map[string]*priceState, len(s.prices)),
		liquidations: make(map[string]*liquidationState, len(s.liquidations)),
		nextLoan:     s.nextLoan,
	}
	for key, ctx := range s.contexts {
		cloned := &accountContext{
			account: ctx.account,
			block:   ctx.block,
			flags:   make(map[Flag]struct{}, len(ctx.flags)),
		}
		for _, item := range ctx.loans {
			cloned.loans = append(cloned.loans, loanRecord{
				loanID:    item.loanID,
				asset:     item.asset,
				principal: cloneInt(item.principal),
				feeBps:    item.feeBps,
				fee:       cloneInt(item.fee),
				block:     item.block,
			})
		}
		for _, item := range ctx.repayments {
			cloned.repayments = append(cloned.repayments, repaymentRecord{
				loanID: item.loanID,
				asset:  item.asset,
				amount: cloneInt(item.amount),
				profit: cloneInt(item.profit),
				block:  item.block,
			})
		}
		for _, item := range ctx.collateral {
			cloned.collateral = append(cloned.collateral, collateralRecord{
				asset:     item.asset,
				operation: item.operation,
				amount:    cloneInt(item.amount),
				block:     item.block,
			})
		}
		for _, item := range ctx.priceUpdates {
			cloned.priceUpdates = append(cloned.priceUpdates, priceUpdateRecord{
				asset:  item.asset,
				price:  cloneInt(item.price),
				source: item.source,
				block:  item.block,
			})
		}
		for _, item := range ctx.liquidations {
			cloned.liquidations = append(cloned.liquidations, liquidationRecord{
				positionID:      item.positionID,
				borrower:        item.borrower,
				collateralAsset: item.collateralAsset,
				debtAsset:       item.debtAsset,
				debtAmount:      cloneInt(item.debtAmount),
				seizedAmount:    cloneInt(item.seizedAmount),
				block:           item.block,
			})
		}
		for flag := range ctx.flags {
			cloned.flags[flag] = struct{}{}
		}
		out.contexts[key] = cloned
	}
	for id, loan := range s.loans {
		cloned := *loan
		cloned.principal = cloneInt(loan.principal)
		cloned.fee = cloneInt(loan.fee)
		cloned.requiredRepayment = cloneInt(loan.requiredRepayment)
		cloned.repaid = cloneInt(loan.repaid)
		out.loans[id] = &cloned
	}
	for account, id := range s.activeLoans {
		out.activeLoans[account] = id
	}
	for asset, price := range s.prices {
		out.prices[asset] = &priceState{
			set:     price.set,
			asset:   price.asset,
			price:   cloneInt(price.price),
			block:   price.block,
			account: price.account,
			source:  price.source,
		}
	}
	for id, liquidation := range s.liquidations {
		cloned := *liquidation
		out.liquidations[id] = &cloned
	}
	return out
}
