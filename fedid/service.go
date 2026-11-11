package fedid

import "fmt"

// Service implements federated identity binding and login.
type Service struct {
	store Store
}

func NewService(store Store) *Service { return &Service{store: store} }

// CreateAccount opens an account anchored on its first verified identity.
func (s *Service) CreateAccount(accountID string, key IdentityKey, email string) error {
	tx := s.store.Begin()
	committed := false
	defer func() {
		if !committed {
			tx.Rollback()
		}
	}()

	if _, exists := tx.GetIdentity(key); exists {
		return ErrIdentityBound
	}
	tx.PutAccount(Account{ID: accountID, Identities: []IdentityKey{key}})
	tx.PutIdentity(Identity{Key: key, Email: email, AccountID: accountID})
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// BindIdentity binds a second identity to an existing account. It requires
// two one-time proofs: one proving control of the account, one proving
// control of the new identity. Both proofs are consumed atomically inside
// the transaction; any failure rolls back and restores them.
func (s *Service) BindIdentity(accountID, accountProofToken, identityProofToken string) error {
	tx := s.store.Begin()
	committed := false
	defer func() {
		if !committed {
			tx.Rollback()
		}
	}()

	acctProof, err := tx.ConsumeProof(accountProofToken)
	if err != nil {
		return err
	}
	if acctProof.Kind != ProofAccount || acctProof.AccountID != accountID {
		return ErrProofMismatch
	}

	idProof, err := tx.ConsumeProof(identityProofToken)
	if err != nil {
		return err
	}
	if idProof.Kind != ProofIdentity {
		return ErrProofMismatch
	}

	acct, ok := tx.GetAccount(accountID)
	if !ok {
		return ErrAccountNotFound
	}

	key := idProof.Key
	if existing, exists := tx.GetIdentity(key); exists && existing.AccountID != accountID {
		return fmt.Errorf("%w: %s/%s owned by %s", ErrIdentityBound, key.Issuer, key.Sub, existing.AccountID)
	}

	if _, exists := tx.GetIdentity(key); !exists {
		acct.Identities = append(acct.Identities, key)
	}
	tx.PutIdentity(Identity{Key: key, Email: idProof.Email, AccountID: accountID})
	tx.PutAccount(acct)

	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// Login resolves the account strictly by verified issuer+sub. Email is
// treated as a mutable attribute: if the IdP reports a new verified email
// for the same issuer+sub, it is updated in place and the same account is
// returned. Two identities from different issuers sharing one email never
// resolve to the same account.
func (s *Service) Login(key IdentityKey, verifiedEmail string) (Account, error) {
	tx := s.store.Begin()
	committed := false
	defer func() {
		if !committed {
			tx.Rollback()
		}
	}()

	id, ok := tx.GetIdentity(key)
	if !ok {
		return Account{}, ErrUnknownIdentity
	}
	if id.Email != verifiedEmail {
		id.Email = verifiedEmail
		tx.PutIdentity(id)
	}
	acct, ok := tx.GetAccount(id.AccountID)
	if !ok {
		return Account{}, ErrAccountNotFound
	}
	if err := tx.Commit(); err != nil {
		return Account{}, err
	}
	committed = true
	return acct, nil
}
