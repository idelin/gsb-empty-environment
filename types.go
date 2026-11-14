package netsec

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"
)

// Flag is a machine-readable risk marker attached to rejected operations and
// to the append-only detection ledger.
type Flag string

const (
	FlagSameBlockComposition  Flag = "same_block_composition"
	FlagPriceManipulation     Flag = "price_manipulation"
	FlagPriceCooldown         Flag = "price_cooldown"
	FlagPriceDeviation        Flag = "price_deviation"
	FlagLiquidationCooldown   Flag = "liquidation_cooldown"
	FlagDuplicateLiquidation  Flag = "duplicate_liquidation"
	FlagAbnormalProfit        Flag = "abnormal_profit"
	FlagCollateralDuringLoan  Flag = "collateral_during_flash_loan"
	FlagCrossBlockComposition Flag = "cross_block_composition"
	FlagFlashLoanNotRepaid    Flag = "flash_loan_not_repaid"
	FlagRepeatedFlashLoan     Flag = "repeated_flash_loan"
)

// Code is the stable category of a rejected operation.
type Code string

const (
	CodeInvalidArgument      Code = "invalid_argument"
	CodeBlockRegression      Code = "block_regression"
	CodeBlockMismatch        Code = "block_mismatch"
	CodeFlashLoanActive      Code = "flash_loan_active"
	CodeRepeatedFlashLoan    Code = "repeated_flash_loan"
	CodeFeeTooHigh           Code = "fee_too_high"
	CodeFlashLoanLimit       Code = "flash_loan_limit"
	CodeUnderRepayment       Code = "under_repayment"
	CodeCrossBlockRepayment  Code = "cross_block_repayment"
	CodeSameBlockComposition Code = "same_block_composition"
	CodeCollateralDuringLoan Code = "collateral_during_flash_loan"
	CodePriceManipulation    Code = "price_manipulation"
	CodePriceCooldown        Code = "price_cooldown"
	CodePriceDeviation       Code = "price_deviation"
	CodeDuplicateLiquidation Code = "duplicate_liquidation"
	CodeLiquidationCooldown  Code = "liquidation_cooldown"
	CodeAbnormalProfit       Code = "abnormal_profit"
	CodeFlashLoanNotRepaid   Code = "flash_loan_not_repaid"
)

var (
	ErrInvalidArgument      = errors.New("netsec: invalid argument")
	ErrBlockRegression      = errors.New("netsec: block regression")
	ErrBlockMismatch        = errors.New("netsec: operation block does not match the active block")
	ErrFlashLoanActive      = errors.New("netsec: account already has an open flash loan")
	ErrRepeatedFlashLoan    = errors.New("netsec: repeated flash loan in the same block")
	ErrFeeTooHigh           = errors.New("netsec: flash loan fee exceeds configured maximum")
	ErrFlashLoanLimit       = errors.New("netsec: flash loan exceeds configured maximum")
	ErrUnderRepayment       = errors.New("netsec: flash loan repayment is below principal plus fee")
	ErrCrossBlockRepayment  = errors.New("netsec: flash loan must be repaid in the block it was opened")
	ErrSameBlockComposition = errors.New("netsec: same-block operation composition is not allowed")
	ErrCollateralDuringLoan = errors.New("netsec: collateral operation during a flash loan")
	ErrPriceManipulation    = errors.New("netsec: price manipulation in the flash-loan block")
	ErrPriceCooldown        = errors.New("netsec: price update is still in its cooldown window")
	ErrPriceDeviation       = errors.New("netsec: price update exceeds the allowed deviation")
	ErrDuplicateLiquidation = errors.New("netsec: duplicate liquidation of the same position")
	ErrLiquidationCooldown  = errors.New("netsec: liquidation is still in its cooldown window")
	ErrAbnormalProfit       = errors.New("netsec: profit exceeds the allowed threshold")
	ErrFlashLoanNotRepaid   = errors.New("netsec: flash loan was not repaid before the transaction ended")
)

// Violation describes a rejected operation. The embedded error supports
// errors.Is(err, ErrDuplicateLiquidation) and similar sentinel checks.
type Violation struct {
	Code      Code
	Flags     []Flag
	Account   string
	Block     uint64
	Operation string
	Detail    string
	cause     error
}

func (v *Violation) Error() string {
	if v == nil {
		return "netsec: violation"
	}
	var b strings.Builder
	if v.Code != "" {
		b.WriteString(string(v.Code))
	} else {
		b.WriteString("violation")
	}
	if v.Operation != "" {
		b.WriteString(" op=")
		b.WriteString(v.Operation)
	}
	if v.Account != "" {
		b.WriteString(" account=")
		b.WriteString(v.Account)
	}
	b.WriteString(fmt.Sprintf(" block=%d", v.Block))
	if v.Detail != "" {
		b.WriteString(": ")
		b.WriteString(v.Detail)
	}
	return b.String()
}

func (v *Violation) Unwrap() error {
	if v == nil {
		return nil
	}
	return v.cause
}

// Detection is an append-only security telemetry record. Unlike protocol
// state, detections are deliberately retained when a transaction is rolled
// back so that an attempted exploit remains observable.
type Detection struct {
	Sequence  uint64
	Time      time.Time
	Block     uint64
	Account   string
	Operation string
	Code      Code
	Flags     []Flag
	Detail    string
}

// Config controls the flash-loan constraints. Use DefaultConfig and adjust
// only the fields that need a non-default value.
type Config struct {
	// MaxFeeBps is the maximum flash-loan fee the pool may charge. The default
	// is 100 bps (1%).
	MaxFeeBps uint64

	// MaxProfitBps is the largest profit a borrower may report or return on
	// repayment without triggering an abnormal-profit violation. The default
	// is 500 bps (5%) of the principal.
	MaxProfitBps uint64

	// PriceCooldownBlocks is the minimum number of blocks between two price
	// updates for the same asset and between a price update and a liquidation
	// that relies on it. Zero disables the cooldown.
	PriceCooldownBlocks uint64

	// LiquidationCooldownBlocks is the minimum number of blocks between two
	// liquidations of the same position. Zero disables the cooldown.
	LiquidationCooldownBlocks uint64

	// PriceDeviationBps rejects an update that moves an existing price by more
	// than the configured fraction. Zero disables the check.
	PriceDeviationBps uint64

	// MaxFlashLoanAmount caps a single flash loan. Nil means no cap.
	MaxFlashLoanAmount *big.Int

	// RequireSameBlockRepayment forces a flash loan to be closed in the block
	// in which it was opened. It defaults to true.
	RequireSameBlockRepayment bool

	// RequireMaturePrice prevents a liquidation from using a price that was
	// updated inside the price cooldown window. It defaults to true.
	RequireMaturePrice bool

	// AllowCollateralDuringFlashLoan is intentionally false by default. A
	// borrower must not add or remove collateral while its flash loan is open.
	AllowCollateralDuringFlashLoan bool

	// ContextRetentionBlocks limits how many historical account contexts are
	// retained. Zero keeps all contexts.
	ContextRetentionBlocks uint64
}

// GuardConfig is an alias for Config.
type GuardConfig = Config

// DefaultConfig returns a conservative configuration suitable for a lending
// pool. Cooldowns are enabled, collateral changes are blocked during a flash
// loan, and repayment must happen in the same block.
func DefaultConfig() Config {
	return Config{
		MaxFeeBps:                      100,
		MaxProfitBps:                   500,
		PriceCooldownBlocks:            1,
		LiquidationCooldownBlocks:      1,
		RequireSameBlockRepayment:      true,
		RequireMaturePrice:             true,
		AllowCollateralDuringFlashLoan: false,
	}
}

// Option mutates a Config. Config itself also implements Option, so
// NewGuard(Config{...}) works; zero-valued Config fields retain the safe
// defaults. Use the With... options to deliberately disable a protection.
type Option interface {
	apply(*Config)
}

// OptionFunc adapts a function to Option for callers that need a custom
// configuration rule.
type OptionFunc func(*Config)

func (f OptionFunc) apply(c *Config) {
	if f != nil {
		f(c)
	}
}

// apply merges non-zero fields over the guard's defaults. A zero MaxProfitBps,
// cooldown or false protection flag therefore does not silently disable a
// safeguard; pass the corresponding With... option to do that explicitly.
func (c Config) apply(dst *Config) {
	if c.MaxFeeBps != 0 {
		dst.MaxFeeBps = c.MaxFeeBps
	}
	if c.MaxProfitBps != 0 {
		dst.MaxProfitBps = c.MaxProfitBps
	}
	if c.PriceCooldownBlocks != 0 {
		dst.PriceCooldownBlocks = c.PriceCooldownBlocks
	}
	if c.LiquidationCooldownBlocks != 0 {
		dst.LiquidationCooldownBlocks = c.LiquidationCooldownBlocks
	}
	if c.PriceDeviationBps != 0 {
		dst.PriceDeviationBps = c.PriceDeviationBps
	}
	if c.MaxFlashLoanAmount != nil {
		dst.MaxFlashLoanAmount = cloneInt(c.MaxFlashLoanAmount)
	}
	if c.RequireSameBlockRepayment {
		dst.RequireSameBlockRepayment = true
	}
	if c.RequireMaturePrice {
		dst.RequireMaturePrice = true
	}
	if c.AllowCollateralDuringFlashLoan {
		dst.AllowCollateralDuringFlashLoan = true
	}
	if c.ContextRetentionBlocks != 0 {
		dst.ContextRetentionBlocks = c.ContextRetentionBlocks
	}
}

func WithMaxFeeBps(v uint64) OptionFunc {
	return func(c *Config) { c.MaxFeeBps = v }
}
func WithMaxProfitBps(v uint64) OptionFunc {
	return func(c *Config) { c.MaxProfitBps = v }
}
func WithPriceCooldownBlocks(v uint64) OptionFunc {
	return func(c *Config) { c.PriceCooldownBlocks = v }
}
func WithLiquidationCooldownBlocks(v uint64) OptionFunc {
	return func(c *Config) { c.LiquidationCooldownBlocks = v }
}
func WithPriceDeviationBps(v uint64) OptionFunc {
	return func(c *Config) { c.PriceDeviationBps = v }
}
func WithMaxFlashLoanAmount(v *big.Int) OptionFunc {
	return func(c *Config) { c.MaxFlashLoanAmount = cloneInt(v) }
}
func WithSameBlockRepaymentRequired(v bool) OptionFunc {
	return func(c *Config) { c.RequireSameBlockRepayment = v }
}
func WithMaturePriceRequired(v bool) OptionFunc {
	return func(c *Config) { c.RequireMaturePrice = v }
}
func WithCollateralDuringFlashLoan(v bool) OptionFunc {
	return func(c *Config) { c.AllowCollateralDuringFlashLoan = v }
}
func WithContextRetentionBlocks(v uint64) OptionFunc {
	return func(c *Config) { c.ContextRetentionBlocks = v }
}

// FlashLoanRequest opens an unsecured flash loan. Block is optional: when it
// is zero the guard's current block is used. A non-zero block must equal the
// active block; advancing the block requires BeginBlock.
type FlashLoanRequest struct {
	LoanID  string
	Account string
	Asset   string
	Amount  *big.Int
	FeeBps  uint64
	Block   uint64
}

// RepayRequest closes a flash loan. Amount may be nil, in which case the exact
// required repayment (principal plus the fee recorded at open time) is used.
// Profit is optional and is checked against MaxProfitBps. If it is omitted,
// any excess over the required repayment is treated as profit as well.
type RepayRequest struct {
	LoanID  string
	Account string
	Amount  *big.Int
	Profit  *big.Int
	Block   uint64
}

// FlashLoan is a defensive snapshot returned from BeginFlashLoan. Mutating its
// big.Int fields does not affect guard state.
type FlashLoan struct {
	ID                string
	Account           string
	Asset             string
	Principal         *big.Int
	FeeBps            uint64
	Fee               *big.Int
	RequiredRepayment *big.Int
	Block             uint64
	Open              bool
	Repaid            *big.Int
}

// Owed returns a copy of the exact repayment required for the loan.
func (l *FlashLoan) Owed() *big.Int {
	if l == nil {
		return nil
	}
	return cloneInt(l.RequiredRepayment)
}

// IsOpen reports whether the loan is still open.
func (l *FlashLoan) IsOpen() bool { return l != nil && l.Open }

// CollateralOperation identifies a collateral change.
type CollateralOperation string

const (
	CollateralDeposit  CollateralOperation = "deposit"
	CollateralWithdraw CollateralOperation = "withdraw"
)

type CollateralRequest struct {
	Account   string
	Asset     string
	Operation CollateralOperation
	Amount    *big.Int
	Block     uint64
}

// PriceUpdateRequest publishes a price for an asset. Account is the actor that
// supplied the price. Leaving it empty is reserved for protocol/oracle
// bootstrap updates.
type PriceUpdateRequest struct {
	Account string
	Asset   string
	Price   *big.Int
	Block   uint64
	Source  string
}

// LiquidationRequest liquidates a borrower position. PositionID is optional;
// when omitted the position is derived from borrower, collateral and debt
// assets.
type LiquidationRequest struct {
	Liquidator      string
	Borrower        string
	PositionID      string
	CollateralAsset string
	DebtAsset       string
	DebtAmount      *big.Int
	SeizedAmount    *big.Int
	Block           uint64
}

// FlashLoanRecord is the borrow leg captured in an account context.
type FlashLoanRecord struct {
	LoanID    string
	Asset     string
	Principal *big.Int
	FeeBps    uint64
	Fee       *big.Int
	Block     uint64
	Open      bool
}

// RepaymentRecord is the repay leg captured in an account context.
type RepaymentRecord struct {
	LoanID string
	Asset  string
	Amount *big.Int
	Profit *big.Int
	Block  uint64
}

// CollateralRecord captures a collateral operation in an account context.
type CollateralRecord struct {
	Asset     string
	Operation CollateralOperation
	Amount    *big.Int
	Block     uint64
}

// PriceUpdateRecord captures a price update in an account context.
type PriceUpdateRecord struct {
	Asset  string
	Price  *big.Int
	Source string
	Block  uint64
}

// LiquidationRecord captures a liquidation in the liquidator's context.
type LiquidationRecord struct {
	PositionID      string
	Borrower        string
	CollateralAsset string
	DebtAsset       string
	DebtAmount      *big.Int
	SeizedAmount    *big.Int
	Block           uint64
}

// AccountContext is a snapshot of everything the guard observed for one
// account in one block. Borrow, repayment and collateral operations therefore
// always appear in the same context and can be inspected together.
type AccountContext struct {
	Account      string
	Block        uint64
	Loans        []FlashLoanRecord
	Repayments   []RepaymentRecord
	Collateral   []CollateralRecord
	PriceUpdates []PriceUpdateRecord
	Liquidations []LiquidationRecord
	Flags        []Flag
}

func (c AccountContext) HasFlashLoan() bool { return len(c.Loans) > 0 }

func (c AccountContext) HasActiveFlashLoan() bool {
	for _, loan := range c.Loans {
		if loan.Open {
			return true
		}
	}
	return false
}

func (c AccountContext) HasCollateral() bool  { return len(c.Collateral) > 0 }
func (c AccountContext) HasPriceUpdate() bool { return len(c.PriceUpdates) > 0 }
func (c AccountContext) HasLiquidation() bool { return len(c.Liquidations) > 0 }
func (c AccountContext) OperationCount() int {
	return len(c.Loans) + len(c.Repayments) + len(c.Collateral) + len(c.PriceUpdates) + len(c.Liquidations)
}

// PriceSnapshot is a point-in-time view of the last accepted price.
type PriceSnapshot struct {
	Asset   string
	Price   *big.Int
	Block   uint64
	Account string
	Source  string
}

// Snapshot is an opaque, deep copy of the guard's protocol state. Detection
// telemetry is not included; Restore therefore never erases risk markers.
type Snapshot struct {
	state *guardState
}

func cloneInt(v *big.Int) *big.Int {
	if v == nil {
		return nil
	}
	return new(big.Int).Set(v)
}

func cloneFlags(in []Flag) []Flag {
	if len(in) == 0 {
		return nil
	}
	return append([]Flag(nil), in...)
}

func cloneFlagsSet(in map[Flag]struct{}) []Flag {
	if len(in) == 0 {
		return nil
	}
	out := make([]Flag, 0, len(in))
	for f := range in {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func positive(v *big.Int) bool { return v != nil && v.Sign() > 0 }

// NewAmount converts a uint64 to the *big.Int representation used by the
// guard. It is a small convenience for callers that do not already use big.Int.
func NewAmount(v uint64) *big.Int { return new(big.Int).SetUint64(v) }
