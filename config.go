package netsec

import (
	"crypto/ed25519"
	"fmt"
	"time"
)

func normalizeConfig(cfg Config, accountID AccountID) (Config, error) {
	out := cfg.clone()
	if out.AccountID == "" {
		out.AccountID = accountID
	}
	if accountID != "" && out.AccountID != accountID {
		return Config{}, fmt.Errorf("%w: config accountID %q does not match %q", ErrInvalidConfig, out.AccountID, accountID)
	}
	if out.AccountID == "" {
		return Config{}, fmt.Errorf("%w: accountID is required", ErrInvalidConfig)
	}
	if out.RecoveryEpoch == 0 {
		out.RecoveryEpoch = recoveryEpochInitial
	}
	if out.CoolingPeriod == 0 {
		out.CoolingPeriod = DefaultCoolingPeriod
	}
	if out.ApprovalWindow == 0 {
		out.ApprovalWindow = DefaultApprovalWindow
	}
	if out.CoolingPeriod < 0 {
		return Config{}, fmt.Errorf("%w: cooling period must be positive", ErrInvalidConfig)
	}
	if out.ApprovalWindow < 0 {
		return Config{}, fmt.Errorf("%w: approval window must be positive", ErrInvalidConfig)
	}
	if out.Threshold == 0 {
		return Config{}, fmt.Errorf("%w: threshold must be positive", ErrInvalidConfig)
	}
	if out.MinDistinctPersons == 0 {
		return Config{}, fmt.Errorf("%w: minimum distinct persons must be positive", ErrInvalidConfig)
	}

	seenIDs := make(map[ContactID]struct{}, len(out.Contacts))
	for i := range out.Contacts {
		contact := out.Contacts[i]
		if contact.ContactID == "" {
			return Config{}, fmt.Errorf("%w: contact at index %d has no contactID", ErrInvalidConfig, i)
		}
		if _, ok := seenIDs[contact.ContactID]; ok {
			return Config{}, fmt.Errorf("%w: duplicate contactID %q", ErrInvalidConfig, contact.ContactID)
		}
		seenIDs[contact.ContactID] = struct{}{}
		if contact.PersonID == "" {
			return Config{}, fmt.Errorf("%w: contact %q has no personID", ErrInvalidConfig, contact.ContactID)
		}
		if len(contact.PublicKey) != ed25519.PublicKeySize {
			return Config{}, fmt.Errorf("%w: contact %q has invalid ed25519 public key", ErrInvalidConfig, contact.ContactID)
		}
		if contact.Weight == 0 {
			return Config{}, fmt.Errorf("%w: contact %q has zero weight", ErrInvalidConfig, contact.ContactID)
		}
		if contact.EndEpoch != 0 && contact.EndEpoch <= contact.StartEpoch {
			return Config{}, fmt.Errorf("%w: contact %q has invalid epoch range [%d,%d)", ErrInvalidConfig, contact.ContactID, contact.StartEpoch, contact.EndEpoch)
		}
		if contact.Fingerprint.IsZero() {
			contact.Fingerprint = PublicKeyFingerprint(contact.PublicKey)
		} else if contact.Fingerprint != PublicKeyFingerprint(contact.PublicKey) {
			return Config{}, fmt.Errorf("%w: contact %q fingerprint does not match public key", ErrInvalidConfig, contact.ContactID)
		}
		out.Contacts[i] = contact
	}
	return out, nil
}

func validateStrongFactors(factors []StrongFactor) error {
	seen := make(map[StrongFactorID]struct{}, len(factors))
	for _, factor := range factors {
		if factor.ID == "" {
			return fmt.Errorf("%w: strong factor has no id", ErrInvalidConfig)
		}
		if _, ok := seen[factor.ID]; ok {
			return fmt.Errorf("%w: duplicate strong factor id %q", ErrInvalidConfig, factor.ID)
		}
		seen[factor.ID] = struct{}{}
		if len(factor.PublicKey) != ed25519.PublicKeySize {
			return fmt.Errorf("%w: strong factor %q has invalid ed25519 public key", ErrInvalidConfig, factor.ID)
		}
	}
	return nil
}

func invalidateActiveRecovery(state *AccountState, reason string, now time.Time) {
	if state.ActiveRecovery == "" {
		return
	}
	recovery, ok := state.Recoveries[state.ActiveRecovery]
	if !ok {
		state.ActiveRecovery = ""
		return
	}
	switch recovery.Status {
	case StatusPending, StatusCooling:
		recovery.Status = StatusInvalidated
		recovery.InvalidationReason = reason
		recovery.CompletedAt = time.Time{}
		state.Recoveries[recovery.ID] = recovery
	}
	state.ActiveRecovery = ""
}
