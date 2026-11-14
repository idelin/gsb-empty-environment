package netsec

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

type acquirerRecord struct {
	acquirer Acquirer
	active   bool
}

type merchantRecord struct {
	merchant Merchant
	active   bool
}

type accountRecord struct {
	account SettlementAccount
	active  bool
}

type terminalRecord struct {
	binding TerminalBinding
	active  bool
	pending *rebindRecord
}

type settlementRecord struct {
	settlement Settlement
}

// Attributor owns the signed attribution registry and the rebind review
// workflow. It is safe for concurrent use by multiple goroutines.
type Attributor struct {
	mu sync.Mutex

	cfg config

	acquirers map[string]*acquirerRecord
	merchants map[string]*merchantRecord
	accounts  map[string]*accountRecord
	terminals map[string]*terminalRecord
	rebinds   map[string]*rebindRecord

	settlements map[string]settlementRecord
	refOrder    []string
}

// New returns an Attributor with production-safe defaults.
func New() *Attributor {
	return NewAttributor()
}

// NewAttributor returns an empty registry. The default rebind delay is 24
// hours, and the default per-settlement maximum is 10 billion minor units.
func NewAttributor(options ...Option) *Attributor {
	cfg := newConfig(options)
	return &Attributor{
		cfg:         cfg,
		acquirers:   make(map[string]*acquirerRecord),
		merchants:   make(map[string]*merchantRecord),
		accounts:    make(map[string]*accountRecord),
		terminals:   make(map[string]*terminalRecord),
		rebinds:     make(map[string]*rebindRecord),
		settlements: make(map[string]settlementRecord),
	}
}

// NewRegistry is an alias for NewAttributor.
func NewRegistry(options ...Option) *Attributor {
	return NewAttributor(options...)
}

// NewSettlementAttributor is an alias for NewAttributor.
func NewSettlementAttributor(options ...Option) *Attributor {
	return NewAttributor(options...)
}

// NewSettlementService is an alias for NewAttributor.
func NewSettlementService(options ...Option) *Attributor {
	return NewAttributor(options...)
}

// RegisterAcquirer records an institution that may own signed settlements.
func (a *Attributor) RegisterAcquirer(acquirer Acquirer) error {
	id, ok := normalizedID(acquirer.ID)
	if !ok {
		return fmt.Errorf("%w: acquirer id", ErrInvalidID)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if _, exists := a.acquirers[id]; exists {
		return fmt.Errorf("%w: acquirer %q", ErrAlreadyExists, id)
	}
	acquirer.ID = id
	a.acquirers[id] = &acquirerRecord{acquirer: acquirer, active: true}
	return nil
}

// RegisterMerchant records a merchant that may own signed terminals.
func (a *Attributor) RegisterMerchant(merchant Merchant) error {
	id, ok := normalizedID(merchant.ID)
	if !ok {
		return fmt.Errorf("%w: merchant id", ErrInvalidID)
	}
	if merchant.MaxSettlementCents < 0 {
		return fmt.Errorf("%w: merchant %q limit", ErrInvalidAmount, id)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if _, exists := a.merchants[id]; exists {
		return fmt.Errorf("%w: merchant %q", ErrAlreadyExists, id)
	}
	merchant.ID = id
	a.merchants[id] = &merchantRecord{merchant: merchant, active: true}
	return nil
}

// RegisterSettlementAccount records an account contracted to exactly one
// merchant/acquirer pair.
func (a *Attributor) RegisterSettlementAccount(account SettlementAccount) error {
	id, ok := normalizedID(account.ID)
	if !ok {
		return fmt.Errorf("%w: account id", ErrInvalidID)
	}
	merchantID, ok := normalizedID(account.MerchantID)
	if !ok {
		return fmt.Errorf("%w: account merchant id", ErrInvalidID)
	}
	acquirerID, ok := normalizedID(account.AcquirerID)
	if !ok {
		return fmt.Errorf("%w: account acquirer id", ErrInvalidID)
	}
	currency, ok := normalizedCurrency(account.Currency, "CNY")
	if !ok {
		return fmt.Errorf("%w: account %q", ErrInvalidCurrency, id)
	}
	if account.MaxSettlementCents < 0 {
		return fmt.Errorf("%w: account %q limit", ErrInvalidAmount, id)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if _, exists := a.accounts[id]; exists {
		return fmt.Errorf("%w: account %q", ErrAlreadyExists, id)
	}
	merchant, exists := a.merchants[merchantID]
	if !exists {
		return fmt.Errorf("%w: merchant %q", ErrNotFound, merchantID)
	}
	if !merchant.active {
		return ErrMerchantInactive
	}
	acquirer, exists := a.acquirers[acquirerID]
	if !exists {
		return fmt.Errorf("%w: acquirer %q", ErrNotFound, acquirerID)
	}
	if !acquirer.active {
		return ErrAcquirerInactive
	}

	account.ID = id
	account.MerchantID = merchantID
	account.AcquirerID = acquirerID
	account.Currency = currency
	a.accounts[id] = &accountRecord{account: account, active: true}
	return nil
}

// RegisterAccount is an alias for RegisterSettlementAccount.
func (a *Attributor) RegisterAccount(account SettlementAccount) error {
	return a.RegisterSettlementAccount(account)
}

// SignTerminal creates the initial signed binding for a terminal. It
// deliberately cannot replace an existing terminal; changing a signed
// terminal must go through RequestRebind and ApproveRebind.
func (a *Attributor) SignTerminal(binding TerminalBinding) error {
	normalized, err := a.normalizeBinding(binding)
	if err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if _, exists := a.terminals[normalized.TerminalID]; exists {
		return fmt.Errorf("%w: terminal %q; use rebind workflow", ErrAlreadyExists, normalized.TerminalID)
	}
	if err := a.validateBindingLocked(normalized); err != nil {
		return err
	}

	a.terminals[normalized.TerminalID] = &terminalRecord{
		binding: normalized,
		active:  true,
	}
	return nil
}

// EnrollTerminal is an alias for SignTerminal.
func (a *Attributor) EnrollTerminal(binding TerminalBinding) error {
	return a.SignTerminal(binding)
}

// BindTerminal is an alias for SignTerminal. It cannot overwrite an existing
// binding.
func (a *Attributor) BindTerminal(binding TerminalBinding) error {
	return a.SignTerminal(binding)
}

// Attribute validates every attribution field and returns the only account to
// which the settlement may be paid. It does not perform a bank transfer.
func (a *Attributor) Attribute(request SettlementRequest) (Settlement, error) {
	if err := a.validateRequestBasics(&request); err != nil {
		return Settlement{}, err
	}

	now := a.cfg.clock()
	a.mu.Lock()
	defer a.mu.Unlock()

	if request.Reference != "" {
		if previous, ok := a.settlements[request.Reference]; ok {
			if !requestMatches(request, previous) {
				return Settlement{}, fmt.Errorf("%w: %q", ErrIdempotencyConflict, request.Reference)
			}
			return previous.settlement, nil
		}
	}

	terminal, ok := a.terminals[request.TerminalID]
	if !ok {
		return Settlement{}, fmt.Errorf("%w: %q", ErrUnsignedTerminal, request.TerminalID)
	}
	a.applyDueRebindLocked(terminal, now)

	if !terminal.active {
		return Settlement{}, fmt.Errorf("%w: %q", ErrInactiveTerminal, request.TerminalID)
	}
	if err := a.validateBindingLocked(terminal.binding); err != nil {
		return Settlement{}, err
	}
	if terminal.pending != nil && requestMatchesProposed(request, terminal.pending.request) {
		switch terminal.pending.request.Status {
		case RebindPendingReview:
			return Settlement{}, fmt.Errorf(
				"%w: %w: terminal %q rebind %q has not been approved",
				ErrRebindNotApproved, ErrCrossMerchant, request.TerminalID, terminal.pending.request.ID,
			)
		case RebindApproved:
			return Settlement{}, fmt.Errorf(
				"%w: %w: terminal %q rebind %q becomes effective at %s",
				ErrRebindNotEffective, ErrCrossMerchant, request.TerminalID,
				terminal.pending.request.ID, terminal.pending.request.EffectiveAt.Format(time.RFC3339Nano),
			)
		}
	}

	if request.MerchantID != terminal.binding.MerchantID {
		return Settlement{}, fmt.Errorf(
			"%w: terminal %q is signed to merchant %q, not %q",
			ErrCrossMerchant, request.TerminalID, terminal.binding.MerchantID, request.MerchantID,
		)
	}
	if request.AcquirerID != terminal.binding.AcquirerID {
		return Settlement{}, fmt.Errorf(
			"%w: terminal %q is signed to acquirer %q, not %q",
			ErrCrossAcquirer, request.TerminalID, terminal.binding.AcquirerID, request.AcquirerID,
		)
	}
	if request.AccountID != "" && request.AccountID != terminal.binding.AccountID {
		return Settlement{}, fmt.Errorf(
			"%w: terminal %q is signed to account %q, not %q",
			ErrAccountMismatch, request.TerminalID, terminal.binding.AccountID, request.AccountID,
		)
	}
	if request.Currency != "" && request.Currency != terminal.binding.Currency {
		return Settlement{}, fmt.Errorf(
			"%w: terminal %q is signed in %s, not %s",
			ErrCurrencyMismatch, request.TerminalID, terminal.binding.Currency, request.Currency,
		)
	}
	if err := a.validateAmountLocked(terminal.binding, request.AmountCents); err != nil {
		return Settlement{}, err
	}

	settlement := Settlement{
		Reference:   request.Reference,
		TerminalID:  terminal.binding.TerminalID,
		MerchantID:  terminal.binding.MerchantID,
		AcquirerID:  terminal.binding.AcquirerID,
		AccountID:   terminal.binding.AccountID,
		AmountCents: request.AmountCents,
		Currency:    terminal.binding.Currency,
		SettledAt:   now,
	}
	if request.Reference != "" {
		a.rememberSettlementLocked(settlement)
	}
	return settlement, nil
}

// Resolve is an alias for Attribute.
func (a *Attributor) Resolve(request SettlementRequest) (Settlement, error) {
	return a.Attribute(request)
}

// AuthorizeSettlement is an alias for Attribute.
func (a *Attributor) AuthorizeSettlement(request SettlementRequest) (Settlement, error) {
	return a.Attribute(request)
}

// Authorize is an alias for Attribute.
func (a *Attributor) Authorize(request SettlementRequest) (Settlement, error) {
	return a.Attribute(request)
}

// PayoutGateway is called only after attribution succeeds. Implementations
// must use settlement.AccountID as the destination and should make Pay
// idempotent by settlement.Reference.
type PayoutGateway interface {
	Pay(ctx context.Context, settlement Settlement) error
}

// Settle attributes the settlement and optionally submits that authoritative
// result to a payout gateway. A nil gateway performs attribution only.
func (a *Attributor) Settle(ctx context.Context, request SettlementRequest, gateway PayoutGateway) (Settlement, error) {
	settlement, err := a.Attribute(request)
	if err != nil {
		return Settlement{}, err
	}
	if gateway != nil {
		if err := gateway.Pay(ctx, settlement); err != nil {
			return Settlement{}, fmt.Errorf("payout failed: %w", err)
		}
	}
	return settlement, nil
}

// CurrentBinding returns the currently effective signed binding. A due rebind
// is applied atomically before the value is returned.
func (a *Attributor) CurrentBinding(terminalID string) (TerminalBinding, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	terminal, ok := a.terminals[terminalID]
	if !ok {
		return TerminalBinding{}, fmt.Errorf("%w: %q", ErrUnsignedTerminal, terminalID)
	}
	a.applyDueRebindLocked(terminal, a.cfg.clock())
	return terminal.binding, nil
}

// Binding is an alias for CurrentBinding.
func (a *Attributor) Binding(terminalID string) (TerminalBinding, error) {
	return a.CurrentBinding(terminalID)
}

// Settlement returns the previously accepted attribution for an idempotency
// reference. It is useful for an independent audit of the account to which a
// settlement was attributed.
func (a *Attributor) Settlement(reference string) (Settlement, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	record, ok := a.settlements[reference]
	if !ok {
		return Settlement{}, false
	}
	return record.settlement, true
}

// DeactivateTerminal immediately prevents new settlements for a terminal.
func (a *Attributor) DeactivateTerminal(terminalID string) error {
	return a.setTerminalActive(terminalID, false)
}

// ActivateTerminal allows a previously deactivated terminal to settle again.
func (a *Attributor) ActivateTerminal(terminalID string) error {
	return a.setTerminalActive(terminalID, true)
}

func (a *Attributor) setTerminalActive(terminalID string, active bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	terminal, ok := a.terminals[terminalID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnsignedTerminal, terminalID)
	}
	terminal.active = active
	return nil
}

// DeactivateMerchant immediately prevents settlements for all of a merchant's
// terminals.
func (a *Attributor) DeactivateMerchant(merchantID string) error {
	return a.setMerchantActive(merchantID, false)
}

// ActivateMerchant allows a merchant to settle again.
func (a *Attributor) ActivateMerchant(merchantID string) error {
	return a.setMerchantActive(merchantID, true)
}

func (a *Attributor) setMerchantActive(merchantID string, active bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	merchant, ok := a.merchants[merchantID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, merchantID)
	}
	merchant.active = active
	return nil
}

// DeactivateAccount immediately prevents settlements to an account.
func (a *Attributor) DeactivateAccount(accountID string) error {
	return a.setAccountActive(accountID, false)
}

// ActivateAccount allows an account to receive settlements again.
func (a *Attributor) ActivateAccount(accountID string) error {
	return a.setAccountActive(accountID, true)
}

func (a *Attributor) setAccountActive(accountID string, active bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	account, ok := a.accounts[accountID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, accountID)
	}
	account.active = active
	return nil
}

// DeactivateAcquirer immediately prevents settlements for an institution.
func (a *Attributor) DeactivateAcquirer(acquirerID string) error {
	return a.setAcquirerActive(acquirerID, false)
}

// ActivateAcquirer allows an institution to settle again.
func (a *Attributor) ActivateAcquirer(acquirerID string) error {
	return a.setAcquirerActive(acquirerID, true)
}

func (a *Attributor) setAcquirerActive(acquirerID string, active bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	acquirer, ok := a.acquirers[acquirerID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, acquirerID)
	}
	acquirer.active = active
	return nil
}

func normalizeAndValidateBinding(binding TerminalBinding) (TerminalBinding, error) {
	terminalID, ok := normalizedID(binding.TerminalID)
	if !ok {
		return TerminalBinding{}, fmt.Errorf("%w: terminal id", ErrInvalidID)
	}
	merchantID, ok := normalizedID(binding.MerchantID)
	if !ok {
		return TerminalBinding{}, fmt.Errorf("%w: merchant id", ErrInvalidID)
	}
	acquirerID, ok := normalizedID(binding.AcquirerID)
	if !ok {
		return TerminalBinding{}, fmt.Errorf("%w: acquirer id", ErrInvalidID)
	}
	accountID, ok := normalizedID(binding.AccountID)
	if !ok {
		return TerminalBinding{}, fmt.Errorf("%w: account id", ErrInvalidID)
	}
	currency, ok := normalizedCurrency(binding.Currency, "")
	if !ok {
		return TerminalBinding{}, ErrInvalidCurrency
	}

	binding.TerminalID = terminalID
	binding.MerchantID = merchantID
	binding.AcquirerID = acquirerID
	binding.AccountID = accountID
	binding.Currency = currency
	return binding, nil
}

func (a *Attributor) normalizeBinding(binding TerminalBinding) (TerminalBinding, error) {
	if binding.Currency == "" {
		a.mu.Lock()
		if account, ok := a.accounts[binding.AccountID]; ok {
			binding.Currency = account.account.Currency
		}
		a.mu.Unlock()
	}
	return normalizeAndValidateBinding(binding)
}

func (a *Attributor) validateBindingLocked(binding TerminalBinding) error {
	merchant, ok := a.merchants[binding.MerchantID]
	if !ok {
		return fmt.Errorf("%w: merchant %q", ErrNotFound, binding.MerchantID)
	}
	if !merchant.active {
		return fmt.Errorf("%w: %q", ErrMerchantInactive, binding.MerchantID)
	}
	acquirer, ok := a.acquirers[binding.AcquirerID]
	if !ok {
		return fmt.Errorf("%w: acquirer %q", ErrNotFound, binding.AcquirerID)
	}
	if !acquirer.active {
		return fmt.Errorf("%w: %q", ErrAcquirerInactive, binding.AcquirerID)
	}
	account, ok := a.accounts[binding.AccountID]
	if !ok {
		return fmt.Errorf("%w: account %q", ErrNotFound, binding.AccountID)
	}
	if !account.active {
		return fmt.Errorf("%w: %q", ErrAccountInactive, binding.AccountID)
	}
	if account.account.MerchantID != binding.MerchantID {
		return fmt.Errorf(
			"%w: account %q belongs to merchant %q, not %q",
			ErrCrossMerchant, binding.AccountID, account.account.MerchantID, binding.MerchantID,
		)
	}
	if account.account.AcquirerID != binding.AcquirerID {
		return fmt.Errorf(
			"%w: account %q belongs to acquirer %q, not %q",
			ErrCrossAcquirer, binding.AccountID, account.account.AcquirerID, binding.AcquirerID,
		)
	}
	if account.account.Currency != binding.Currency {
		return fmt.Errorf(
			"%w: account %q is %s, binding is %s",
			ErrCurrencyMismatch, binding.AccountID, account.account.Currency, binding.Currency,
		)
	}
	return nil
}

func (a *Attributor) validateRequestBasics(request *SettlementRequest) error {
	terminalID, ok := normalizedID(request.TerminalID)
	if !ok {
		return fmt.Errorf("%w: terminal id", ErrInvalidID)
	}
	merchantID, ok := normalizedID(request.MerchantID)
	if !ok {
		return fmt.Errorf("%w: merchant id", ErrInvalidID)
	}
	acquirerID, ok := normalizedID(request.AcquirerID)
	if !ok {
		return fmt.Errorf("%w: acquirer id", ErrInvalidID)
	}
	if request.AccountID != "" {
		accountID, ok := normalizedID(request.AccountID)
		if !ok {
			return fmt.Errorf("%w: account id", ErrInvalidID)
		}
		request.AccountID = accountID
	}
	if request.Currency != "" {
		currency, ok := normalizedCurrency(request.Currency, "")
		if !ok {
			return ErrInvalidCurrency
		}
		request.Currency = currency
	}
	if request.AmountCents < a.cfg.minAmountCents {
		return fmt.Errorf("%w: %d", ErrAmountTooSmall, request.AmountCents)
	}
	if request.AmountCents > a.cfg.maxAmountCents {
		return fmt.Errorf("%w: %d", ErrAmountTooLarge, request.AmountCents)
	}

	request.TerminalID = terminalID
	request.MerchantID = merchantID
	request.AcquirerID = acquirerID
	return nil
}

func (a *Attributor) validateAmountLocked(binding TerminalBinding, amountCents int64) error {
	if amountCents < a.cfg.minAmountCents {
		return fmt.Errorf("%w: %d", ErrAmountTooSmall, amountCents)
	}
	if amountCents > a.cfg.maxAmountCents {
		return fmt.Errorf("%w: %d", ErrAmountTooLarge, amountCents)
	}

	merchant := a.merchants[binding.MerchantID]
	if merchant != nil && merchant.merchant.MaxSettlementCents > 0 &&
		amountCents > merchant.merchant.MaxSettlementCents {
		return fmt.Errorf(
			"%w: merchant %q limit is %d, got %d",
			ErrAmountTooLarge, binding.MerchantID, merchant.merchant.MaxSettlementCents, amountCents,
		)
	}
	account := a.accounts[binding.AccountID]
	if account != nil && account.account.MaxSettlementCents > 0 &&
		amountCents > account.account.MaxSettlementCents {
		return fmt.Errorf(
			"%w: account %q limit is %d, got %d",
			ErrAmountTooLarge, binding.AccountID, account.account.MaxSettlementCents, amountCents,
		)
	}
	return nil
}

func requestMatches(request SettlementRequest, previous settlementRecord) bool {
	s := previous.settlement
	if request.TerminalID != s.TerminalID ||
		request.MerchantID != s.MerchantID ||
		request.AcquirerID != s.AcquirerID ||
		request.AmountCents != s.AmountCents {
		return false
	}
	if request.AccountID != "" && request.AccountID != s.AccountID {
		return false
	}
	if request.Currency != "" && !strings.EqualFold(request.Currency, s.Currency) {
		return false
	}
	return true
}

func requestMatchesProposed(request SettlementRequest, rebind RebindRequest) bool {
	proposed := rebind.Proposed
	if request.MerchantID != proposed.MerchantID || request.AcquirerID != proposed.AcquirerID {
		return false
	}
	if request.AccountID != "" && request.AccountID != proposed.AccountID {
		return false
	}
	if request.Currency != "" && request.Currency != proposed.Currency {
		return false
	}
	return true
}

func (a *Attributor) rememberSettlementLocked(settlement Settlement) {
	if len(a.settlements) >= a.cfg.maxIdempotencyLen {
		if len(a.refOrder) > 0 {
			oldest := a.refOrder[0]
			a.refOrder = a.refOrder[1:]
			delete(a.settlements, oldest)
		}
	}
	a.settlements[settlement.Reference] = settlementRecord{
		settlement: settlement,
	}
	a.refOrder = append(a.refOrder, settlement.Reference)
}

// Clock returns the time source used by the component. It is exposed for
// diagnostics and tests that need to compute rebind deadlines.
func (a *Attributor) Clock() time.Time {
	return a.cfg.clock()
}
