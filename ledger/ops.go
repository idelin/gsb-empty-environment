package ledger

import "fmt"

// Deposit mints supply into an account's available bucket. It is the only
// entry point for new money, keeping the conservation model auditable.
func (s *Store) Deposit(requestID, accountID, currency string, amount Money) (Receipt, error) {
	if amount <= 0 {
		return Receipt{}, ErrInvalidAmount
	}
	return s.withReceipt(requestID, func() (Receipt, error) {
		seq, err := s.mutateAccount(accountID, func(st *accountState, fx *effects) error {
			st.account.Available[currency] += amount
			fx.supply[currency] += amount
			return nil
		})
		if err != nil {
			return Receipt{}, err
		}
		return Receipt{RequestID: requestID, Kind: OpDeposit, AccountID: accountID,
			Currency: currency, Amount: amount, Seq: seq}, nil
	})
}

// Freeze moves amount from available to frozen, binding the hold to the
// request id and currency. Replaying the request id returns the original
// receipt (and freeze id) without freezing twice.
func (s *Store) Freeze(requestID, accountID, currency string, amount Money) (Receipt, error) {
	if amount <= 0 {
		return Receipt{}, ErrInvalidAmount
	}
	return s.withReceipt(requestID, func() (Receipt, error) {
		freezeID := "fz-" + requestID
		seq, err := s.mutateAccount(accountID, func(st *accountState, fx *effects) error {
			if st.account.Available[currency] < amount {
				return ErrInsufficientFunds
			}
			st.account.Available[currency] -= amount
			st.account.Frozen[currency] += amount
			st.freezes[freezeID] = &Freeze{
				ID: freezeID, RequestID: requestID, AccountID: accountID,
				Currency: currency, Amount: amount, Remaining: amount,
				Status: FreezeOpen,
			}
			return nil
		})
		if err != nil {
			return Receipt{}, err
		}
		return Receipt{RequestID: requestID, Kind: OpFreeze, AccountID: accountID,
			Currency: currency, Amount: amount, FreezeID: freezeID, Seq: seq}, nil
	})
}

// Settle partially or fully consumes a freeze: the settled amount leaves
// the system (frozen decreases, settled ledger increases). Only the
// freeze's Remaining can ever be settled or released afterwards.
func (s *Store) Settle(requestID, accountID, freezeID string, amount Money) (Receipt, error) {
	if amount <= 0 {
		return Receipt{}, ErrInvalidAmount
	}
	return s.withReceipt(requestID, func() (Receipt, error) {
		var currency string
		seq, err := s.mutateAccount(accountID, func(st *accountState, fx *effects) error {
			f, ok := st.freezes[freezeID]
			if !ok {
				return ErrFreezeNotFound
			}
			if f.Status != FreezeOpen {
				return ErrFreezeClosed
			}
			if amount > f.Remaining {
				return ErrInsufficientFrozen
			}
			currency = f.Currency
			f.Remaining -= amount
			st.account.Frozen[f.Currency] -= amount
			fx.settled[f.Currency] += amount
			if f.Remaining == 0 {
				f.Status = FreezeSettled
			}
			return nil
		})
		if err != nil {
			return Receipt{}, err
		}
		return Receipt{RequestID: requestID, Kind: OpSettle, AccountID: accountID,
			Currency: currency, Amount: amount, FreezeID: freezeID, Seq: seq}, nil
	})
}

// Release returns the freeze's remaining amount (or a smaller explicit
// amount) to available. After partial settlement only Remaining can be
// released; a fully settled freeze rejects further releases.
func (s *Store) Release(requestID, accountID, freezeID string, amount Money) (Receipt, error) {
	if amount <= 0 {
		return Receipt{}, ErrInvalidAmount
	}
	return s.withReceipt(requestID, func() (Receipt, error) {
		var currency string
		seq, err := s.mutateAccount(accountID, func(st *accountState, fx *effects) error {
			f, ok := st.freezes[freezeID]
			if !ok {
				return ErrFreezeNotFound
			}
			if f.Status != FreezeOpen {
				return ErrFreezeClosed
			}
			if amount > f.Remaining {
				return ErrInsufficientFrozen
			}
			currency = f.Currency
			f.Remaining -= amount
			st.account.Frozen[f.Currency] -= amount
			st.account.Available[f.Currency] += amount
			if f.Remaining == 0 {
				f.Status = FreezeReleased
			}
			return nil
		})
		if err != nil {
			return Receipt{}, err
		}
		return Receipt{RequestID: requestID, Kind: OpRelease, AccountID: accountID,
			Currency: currency, Amount: amount, FreezeID: freezeID, Seq: seq}, nil
	})
}

// BatchOp is one step of an atomic cross-account batch.
type BatchOp struct {
	AccountID string
	Currency  string
	Amount    Money // signed: negative debits available, positive credits
}

// Batch applies every op atomically: all staged mutations commit together
// under a single version adjudication, or none do. Conservation holds
// because debits and credits stay inside the available buckets.
func (s *Store) Batch(requestID string, ops []BatchOp) (Receipt, error) {
	if len(ops) == 0 {
		return Receipt{}, ErrEmptyBatch
	}
	for _, op := range ops {
		if op.Amount == 0 {
			return Receipt{}, ErrInvalidAmount
		}
	}
	return s.withReceipt(requestID, func() (Receipt, error) {
		var seq uint64
		err := s.mutateBatch(ops)
		if err != nil {
			return Receipt{}, err
		}
		s.mu.Lock()
		seq = s.seq
		s.mu.Unlock()
		return Receipt{RequestID: requestID, Kind: OpBatch, Seq: seq}, nil
	})
}

// mutateBatch stages all ops, then commits every touched account in one
// version-checked shot, retrying the whole batch on conflict.
func (s *Store) mutateBatch(ops []BatchOp) error {
	for attempt := 0; attempt < maxCommitRetries; attempt++ {
		staged := map[string]*accountState{}
		expected := map[string]uint64{}
		conflict := false
		for _, op := range ops {
			st, ok := staged[op.AccountID]
			if !ok {
				var ver uint64
				var err error
				st, ver, err = s.snapshot(op.AccountID)
				if err != nil {
					return err
				}
				staged[op.AccountID] = st
				expected[op.AccountID] = ver
			}
			if op.Amount < 0 {
				if st.account.Available[op.Currency] < -op.Amount {
					return ErrInsufficientFunds
				}
				st.account.Available[op.Currency] += op.Amount
			} else {
				st.account.Available[op.Currency] += op.Amount
			}
		}
		_, err := s.commit(staged, expected, newEffects())
		if err == ErrVersionConflict {
			conflict = true
		}
		if conflict {
			continue
		}
		return err
	}
	return ErrVersionConflict
}

// String renders a freeze for debugging.
func (f *Freeze) String() string {
	return fmt.Sprintf("Freeze{%s %s %s amount=%d remaining=%d status=%s}",
		f.ID, f.AccountID, f.Currency, f.Amount, f.Remaining, f.Status)
}
