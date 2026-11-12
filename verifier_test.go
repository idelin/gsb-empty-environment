package netsec

import (
	"crypto/ed25519"
	"errors"
	"sync"
	"testing"
	"time"
)

type testKeys struct {
	operator         ed25519.PublicKey
	operatorPrivate  ed25519.PrivateKey
	operatorSigning  ed25519.PublicKey
	operatorSignPriv ed25519.PrivateKey
	account          ed25519.PublicKey
	accountPrivate   ed25519.PrivateKey
	accountSigning   ed25519.PublicKey
	accountSignPriv  ed25519.PrivateKey
	user             ed25519.PublicKey
	userPrivate      ed25519.PrivateKey
	rogue            ed25519.PublicKey
	roguePrivate     ed25519.PrivateKey
}

func newTestKeys(t *testing.T) testKeys {
	t.Helper()
	generate := func() (ed25519.PublicKey, ed25519.PrivateKey) {
		publicKey, privateKey, err := ed25519.GenerateKey(nil)
		if err != nil {
			t.Fatalf("generate Ed25519 key: %v", err)
		}
		return publicKey, privateKey
	}

	operator, operatorPrivate := generate()
	operatorSigning, operatorSignPrivate := generate()
	account, accountPrivate := generate()
	accountSigning, accountSignPrivate := generate()
	user, userPrivate := generate()
	rogue, roguePrivate := generate()
	return testKeys{
		operator:         operator,
		operatorPrivate:  operatorPrivate,
		operatorSigning:  operatorSigning,
		operatorSignPriv: operatorSignPrivate,
		account:          account,
		accountPrivate:   accountPrivate,
		accountSigning:   accountSigning,
		accountSignPriv:  accountSignPrivate,
		user:             user,
		userPrivate:      userPrivate,
		rogue:            rogue,
		roguePrivate:     roguePrivate,
	}
}

func testAuthenticator(t *testing.T, keys testKeys, now time.Time) *Authenticator {
	t.Helper()
	authenticator := NewAuthenticator(Operator{
		ID:        PublicKeyID(keys.operator),
		PublicKey: keys.operator,
		SigningKeys: []Key{
			NewKey(keys.operatorSigning),
		},
	})
	authenticator.Now = func() time.Time { return now }
	authenticator.Nonces.Now = func() time.Time { return now }
	return authenticator
}

func signAccount(
	t *testing.T,
	issuer string,
	privateKey ed25519.PrivateKey,
	subject string,
	signingKeys []string,
	revocations map[string]int64,
	iat, exp int64,
) string {
	t.Helper()
	token, err := SignJWT(AccountClaims{
		Claims: Claims{
			Issuer:   issuer,
			Subject:  subject,
			Type:     TypeAccount,
			IssuedAt: iat,
			Expires:  exp,
		},
		NATS: AccountData{
			SigningKeys: signingKeys,
			Revocations: revocations,
		},
	}, privateKey)
	if err != nil {
		t.Fatalf("sign account JWT: %v", err)
	}
	return token
}

func signUser(
	t *testing.T,
	issuer string,
	privateKey ed25519.PrivateKey,
	subject, issuerAccount string,
	iat, exp int64,
) string {
	t.Helper()
	token, err := SignJWT(UserClaims{
		Claims: Claims{
			Issuer:   issuer,
			Subject:  subject,
			Type:     TypeUser,
			IssuedAt: iat,
			Expires:  exp,
		},
		NATS: UserData{IssuerAccount: issuerAccount},
	}, privateKey)
	if err != nil {
		t.Fatalf("sign user JWT: %v", err)
	}
	return token
}

func TestAccountJWTIssuerTrustAndExpiry(t *testing.T) {
	keys := newTestKeys(t)
	now := time.Unix(1_800_000_000, 0)
	authenticator := testAuthenticator(t, keys, now)
	accountID := PublicKeyID(keys.account)
	iat := now.Add(-time.Minute).Unix()
	exp := now.Add(time.Hour).Unix()

	rootSigned := signAccount(t, PublicKeyID(keys.operator), keys.operatorPrivate,
		accountID, []string{PublicKeyID(keys.accountSigning)}, nil, iat, exp)
	if _, err := authenticator.VerifyAccount(rootSigned); err != nil {
		t.Fatalf("operator identity key should issue account JWT: %v", err)
	}

	signingKeySigned := signAccount(t, PublicKeyID(keys.operatorSigning),
		keys.operatorSignPriv, accountID, nil, nil, iat, exp)
	if _, err := authenticator.VerifyAccount(signingKeySigned); err != nil {
		t.Fatalf("registered operator signing key should issue account JWT: %v", err)
	}

	unregistered := signAccount(t, PublicKeyID(keys.rogue), keys.roguePrivate,
		accountID, nil, nil, iat, exp)
	if _, err := authenticator.VerifyAccount(unregistered); !errors.Is(err, ErrUntrustedIssuer) {
		t.Fatalf("unregistered operator signer: got %v, want ErrUntrustedIssuer", err)
	}

	// A trusted issuer identifier with a rogue signature must not authenticate.
	forgedIssuer := signAccount(t, PublicKeyID(keys.operator), keys.roguePrivate,
		accountID, nil, nil, iat, exp)
	if _, err := authenticator.VerifyAccount(forgedIssuer); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("forged operator identity signature: got %v, want ErrInvalidSignature", err)
	}

	expired := signAccount(t, PublicKeyID(keys.operator), keys.operatorPrivate,
		accountID, nil, nil, now.Add(-2*time.Hour).Unix(), now.Unix())
	if _, err := authenticator.VerifyAccount(expired); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired account JWT: got %v, want ErrExpired", err)
	}
}

func TestUserJWTRequiresAuthorizedSignerAndCorrectIssuerAccount(t *testing.T) {
	keys := newTestKeys(t)
	now := time.Unix(1_800_000_000, 0)
	authenticator := testAuthenticator(t, keys, now)
	accountID := PublicKeyID(keys.account)
	accountSigningID := PublicKeyID(keys.accountSigning)
	userID := PublicKeyID(keys.user)
	iat := now.Add(-time.Minute).Unix()
	exp := now.Add(time.Hour).Unix()

	accountToken := signAccount(t, PublicKeyID(keys.operator), keys.operatorPrivate,
		accountID, []string{accountSigningID}, nil, iat, exp)
	account, err := authenticator.VerifyAccount(accountToken)
	if err != nil {
		t.Fatalf("verify account: %v", err)
	}

	identitySigned := signUser(t, accountID, keys.accountPrivate, userID, "", iat, exp)
	if _, err := authenticator.VerifyUser(identitySigned, account); err != nil {
		t.Fatalf("account identity key should issue user JWT: %v", err)
	}

	signingKeySigned := signUser(t, accountSigningID, keys.accountSignPriv,
		userID, accountID, iat, exp)
	if _, err := authenticator.VerifyUser(signingKeySigned, account); err != nil {
		t.Fatalf("registered account signing key should issue user JWT: %v", err)
	}

	withoutIssuerAccount := signUser(t, accountSigningID, keys.accountSignPriv,
		userID, "", iat, exp)
	if _, err := authenticator.VerifyUser(withoutIssuerAccount, account); !errors.Is(err, ErrMissingIssuerAccount) {
		t.Fatalf("missing issuer_account: got %v, want ErrMissingIssuerAccount", err)
	}

	impersonated := signUser(t, accountSigningID, keys.accountSignPriv,
		userID, PublicKeyID(keys.rogue), iat, exp)
	if _, err := authenticator.VerifyUser(impersonated, account); !errors.Is(err, ErrIssuerAccountMismatch) {
		t.Fatalf("wrong issuer_account: got %v, want ErrIssuerAccountMismatch", err)
	}

	unregisteredSigner := signUser(t, PublicKeyID(keys.rogue), keys.roguePrivate,
		userID, accountID, iat, exp)
	if _, err := authenticator.VerifyUser(unregisteredSigner, account); !errors.Is(err, ErrUntrustedSigningKey) {
		t.Fatalf("unregistered user signer: got %v, want ErrUntrustedSigningKey", err)
	}

	// Claiming the account identity as issuer is insufficient without the
	// matching identity private key.
	forgedIdentity := signUser(t, accountID, keys.roguePrivate, userID, "", iat, exp)
	if _, err := authenticator.VerifyUser(forgedIdentity, account); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("forged account identity signature: got %v, want ErrInvalidSignature", err)
	}

	// Even the account identity key cannot claim another issuer_account.
	identityImpersonation := signUser(t, accountID, keys.accountPrivate,
		userID, PublicKeyID(keys.rogue), iat, exp)
	if _, err := authenticator.VerifyUser(identityImpersonation, account); !errors.Is(err, ErrIssuerAccountMismatch) {
		t.Fatalf("identity issuer_account mismatch: got %v, want ErrIssuerAccountMismatch", err)
	}
}

func TestUserJWTFromAnotherAccountsSigningKeyIsRejected(t *testing.T) {
	keys := newTestKeys(t)
	now := time.Unix(1_800_000_000, 0)
	authenticator := testAuthenticator(t, keys, now)
	accountID := PublicKeyID(keys.account)
	userID := PublicKeyID(keys.user)
	iat := now.Add(-time.Minute).Unix()
	exp := now.Add(time.Hour).Unix()

	accountToken := signAccount(t, PublicKeyID(keys.operator), keys.operatorPrivate,
		accountID, nil, nil, iat, exp)
	account, err := authenticator.VerifyAccount(accountToken)
	if err != nil {
		t.Fatalf("verify account: %v", err)
	}

	// This is a valid key, but it belongs to an account that registered no
	// signing keys in this trust chain.
	userToken := signUser(t, PublicKeyID(keys.accountSigning), keys.accountSignPriv,
		userID, accountID, iat, exp)
	if _, err := authenticator.VerifyUser(userToken, account); !errors.Is(err, ErrUntrustedSigningKey) {
		t.Fatalf("other account signing key: got %v, want ErrUntrustedSigningKey", err)
	}
}

func TestRevocationBoundaryUsesIssuedAtAtOrBeforeCutoff(t *testing.T) {
	keys := newTestKeys(t)
	now := time.Unix(1_800_000_000, 0)
	authenticator := testAuthenticator(t, keys, now)
	accountID := PublicKeyID(keys.account)
	accountSigningID := PublicKeyID(keys.accountSigning)
	userID := PublicKeyID(keys.user)
	exp := now.Add(time.Hour).Unix()
	cutoff := now.Add(-30 * time.Second).Unix()

	accountToken := signAccount(t, PublicKeyID(keys.operator), keys.operatorPrivate,
		accountID, []string{accountSigningID}, map[string]int64{userID: cutoff},
		now.Add(-2*time.Hour).Unix(), exp)
	account, err := authenticator.VerifyAccount(accountToken)
	if err != nil {
		t.Fatalf("verify account: %v", err)
	}

	for _, iat := range []int64{cutoff - 1, cutoff} {
		userToken := signUser(t, accountSigningID, keys.accountSignPriv, userID, accountID, iat, exp)
		if _, err := authenticator.VerifyUser(userToken, account); !errors.Is(err, ErrRevoked) {
			t.Fatalf("iat %d <= cutoff: got %v, want ErrRevoked", iat, err)
		}
	}

	userToken := signUser(t, accountSigningID, keys.accountSignPriv, userID, accountID, cutoff+1, exp)
	if _, err := authenticator.VerifyUser(userToken, account); err != nil {
		t.Fatalf("iat after cutoff should be accepted: %v", err)
	}
}

func TestExternalRevocationBoundary(t *testing.T) {
	keys := newTestKeys(t)
	now := time.Unix(1_800_000_000, 0)
	authenticator := testAuthenticator(t, keys, now)
	accountID := PublicKeyID(keys.account)
	accountSigningID := PublicKeyID(keys.accountSigning)
	userID := PublicKeyID(keys.user)
	exp := now.Add(time.Hour).Unix()
	cutoff := now.Add(-30 * time.Second).Unix()

	accountToken := signAccount(t, PublicKeyID(keys.operator), keys.operatorPrivate,
		accountID, []string{accountSigningID}, nil, now.Add(-2*time.Hour).Unix(), exp)
	account, err := authenticator.VerifyAccount(accountToken)
	if err != nil {
		t.Fatalf("verify account: %v", err)
	}
	if err := authenticator.SetRevocation(accountID, userID, cutoff); err != nil {
		t.Fatalf("set revocation: %v", err)
	}

	atBoundary := signUser(t, accountSigningID, keys.accountSignPriv, userID, accountID, cutoff, exp)
	if _, err := authenticator.VerifyUser(atBoundary, account); !errors.Is(err, ErrRevoked) {
		t.Fatalf("iat == cutoff: got %v, want ErrRevoked", err)
	}

	afterBoundary := signUser(t, accountSigningID, keys.accountSignPriv, userID, accountID, cutoff+1, exp)
	if _, err := authenticator.VerifyUser(afterBoundary, account); err != nil {
		t.Fatalf("iat > cutoff should be accepted: %v", err)
	}
}

func TestUserExpiryBoundary(t *testing.T) {
	keys := newTestKeys(t)
	now := time.Unix(1_800_000_000, 0)
	authenticator := testAuthenticator(t, keys, now)
	accountID := PublicKeyID(keys.account)
	accountSigningID := PublicKeyID(keys.accountSigning)
	userID := PublicKeyID(keys.user)
	accountExp := now.Add(time.Hour).Unix()

	accountToken := signAccount(t, PublicKeyID(keys.operator), keys.operatorPrivate,
		accountID, []string{accountSigningID}, nil, now.Add(-time.Hour).Unix(), accountExp)
	account, err := authenticator.VerifyAccount(accountToken)
	if err != nil {
		t.Fatalf("verify account: %v", err)
	}

	atExpiry := signUser(t, accountSigningID, keys.accountSignPriv, userID, accountID, now.Unix(), now.Unix())
	if _, err := authenticator.VerifyUser(atExpiry, account); !errors.Is(err, ErrExpired) {
		t.Fatalf("iat == exp: got %v, want ErrExpired", err)
	}

	stillValid := signUser(t, accountSigningID, keys.accountSignPriv, userID, accountID,
		now.Add(-time.Second).Unix(), now.Add(time.Second).Unix())
	if _, err := authenticator.VerifyUser(stillValid, account); err != nil {
		t.Fatalf("user JWT before expiry should be accepted: %v", err)
	}
}

func TestNonceSignatureAndReplay(t *testing.T) {
	keys := newTestKeys(t)
	now := time.Unix(1_800_000_000, 0)
	authenticator := testAuthenticator(t, keys, now)
	accountID := PublicKeyID(keys.account)
	accountSigningID := PublicKeyID(keys.accountSigning)
	userID := PublicKeyID(keys.user)
	iat := now.Add(-time.Minute).Unix()
	exp := now.Add(time.Hour).Unix()

	accountToken := signAccount(t, PublicKeyID(keys.operator), keys.operatorPrivate,
		accountID, []string{accountSigningID}, nil, iat, exp)
	userToken := signUser(t, accountSigningID, keys.accountSignPriv, userID, accountID, iat, exp)

	nonce := []byte("leafnode-challenge-0123456789")
	badSignature := ed25519.Sign(keys.roguePrivate, nonce)
	if err := authenticator.VerifyNonce(keys.user, nonce, badSignature); !errors.Is(err, ErrInvalidNonceSignature) {
		t.Fatalf("nonce signed by wrong key: got %v, want ErrInvalidNonceSignature", err)
	}

	// A failed verification must not consume the challenge.
	goodSignature := ed25519.Sign(keys.userPrivate, nonce)
	if err := authenticator.VerifyNonce(keys.user, nonce, goodSignature); err != nil {
		t.Fatalf("valid nonce signature: %v", err)
	}
	if err := authenticator.VerifyNonce(keys.user, nonce, goodSignature); !errors.Is(err, ErrNonceReplay) {
		t.Fatalf("nonce replay: got %v, want ErrNonceReplay", err)
	}

	challenge, err := authenticator.Challenge()
	if err != nil {
		t.Fatalf("issue challenge: %v", err)
	}
	if _, err := authenticator.AuthenticateWithNonce(userToken, accountToken, challenge,
		ed25519.Sign(keys.userPrivate, challenge)); err != nil {
		t.Fatalf("complete leafnode authentication: %v", err)
	}
	if _, err := authenticator.AuthenticateWithNonce(userToken, accountToken, challenge,
		ed25519.Sign(keys.userPrivate, challenge)); !errors.Is(err, ErrNonceReplay) {
		t.Fatalf("authenticate replay: got %v, want ErrNonceReplay", err)
	}
}

func TestNonceConsumptionIsAtomic(t *testing.T) {
	keys := newTestKeys(t)
	authenticator := NewAuthenticator(NewOperator(keys.operator))
	authenticator.Nonces.Now = func() time.Time { return time.Unix(1_800_000_000, 0) }

	nonce := []byte("concurrent-leafnode-nonce-1234")
	signature := ed25519.Sign(keys.userPrivate, nonce)

	const contenders = 32
	start := make(chan struct{})
	errs := make(chan error, contenders)
	var waitGroup sync.WaitGroup
	waitGroup.Add(contenders)
	for i := 0; i < contenders; i++ {
		go func() {
			defer waitGroup.Done()
			<-start
			errs <- authenticator.VerifyNonce(keys.user, nonce, signature)
		}()
	}
	close(start)
	waitGroup.Wait()
	close(errs)

	successes := 0
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrNonceReplay):
		default:
			t.Fatalf("unexpected nonce result: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("nonce accepted %d times, want exactly once", successes)
	}
}
