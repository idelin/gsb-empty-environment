package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// TokenKind distinguishes access tokens from refresh tokens. The kind is both
// signed and checked so a refresh token can never be used as an access token.
type TokenKind string

const (
	TokenKindAccess  TokenKind = "access"
	TokenKindRefresh TokenKind = "refresh"
)

// TokenClaims are the signed contents of both token kinds. The epoch fields are
// uint64, matching the account record exactly.
type TokenClaims struct {
	Version         int       `json:"v"`
	Kind            TokenKind `json:"kind"`
	AccountID       AccountID `json:"sub"`
	FamilyID        FamilyID  `json:"family,omitempty"`
	Generation      uint64    `json:"gen,omitempty"`
	CredentialEpoch uint64    `json:"ce"`
	SessionEpoch    uint64    `json:"se"`
	IssuedAtNS      int64     `json:"iat"`
	ExpiresAtNS     int64     `json:"exp"`
	TokenID         string    `json:"jti"`
}

// TokenLifetime controls access and refresh token expiry.
type TokenLifetime struct {
	Access  time.Duration
	Refresh time.Duration
}

// DefaultTokenLifetime is 15 minutes for access tokens and 30 days for refresh
// tokens.
func DefaultTokenLifetime() TokenLifetime {
	return TokenLifetime{Access: 15 * time.Minute, Refresh: 30 * 24 * time.Hour}
}

// TokenPair is returned only after a successful refresh or login completion.
type TokenPair struct {
	AccessToken        string
	RefreshToken       string
	AccessExpiresAt    time.Time
	RefreshExpiresAt   time.Time
	FamilyID           FamilyID
	Generation         uint64
	CredentialEpoch    uint64
	SessionEpoch       uint64
	AccessTokenClaims  TokenClaims
	RefreshTokenClaims TokenClaims
}

// TokenSigner creates and verifies HMAC-SHA256 tokens. The key must be stable
// across service restarts, otherwise every existing token becomes invalid.
type TokenSigner struct {
	key []byte
}

// NewTokenSigner creates a signer with a key of at least 256 bits.
func NewTokenSigner(key []byte) (*TokenSigner, error) {
	if len(key) < 32 {
		return nil, errors.New("netsec: token signing key must be at least 32 bytes")
	}
	return &TokenSigner{key: append([]byte(nil), key...)}, nil
}

func (s *TokenSigner) sign(claims TokenClaims) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte("netsec-token-v1\x00"))
	_, _ = mac.Write([]byte(encoded))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encoded + "." + signature, nil
}

func (s *TokenSigner) verify(token string) (TokenClaims, error) {
	if len(token) == 0 || len(token) > 16*1024 {
		return TokenClaims{}, ErrInvalidToken
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return TokenClaims{}, ErrInvalidToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return TokenClaims{}, ErrInvalidToken
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return TokenClaims{}, ErrInvalidToken
	}
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte("netsec-token-v1\x00"))
	_, _ = mac.Write([]byte(parts[0]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return TokenClaims{}, ErrInvalidToken
	}
	var claims TokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return TokenClaims{}, ErrInvalidToken
	}
	if claims.Version != 1 || claims.AccountID == "" || claims.TokenID == "" {
		return TokenClaims{}, ErrInvalidToken
	}
	if claims.Kind != TokenKindAccess && claims.Kind != TokenKindRefresh {
		return TokenClaims{}, ErrInvalidToken
	}
	if claims.ExpiresAtNS <= claims.IssuedAtNS || claims.ExpiresAtNS <= 0 {
		return TokenClaims{}, ErrInvalidToken
	}
	return claims, nil
}

// Token errors. Callers should fail closed and never retry with an older token.
var (
	ErrInvalidToken              = errors.New("netsec: invalid token")
	ErrTokenExpired              = errors.New("netsec: token expired")
	ErrTokenKind                 = errors.New("netsec: wrong token kind")
	ErrTokenEpochStale           = errors.New("netsec: token epoch is stale")
	ErrTokenRevoked              = errors.New("netsec: token family revoked")
	ErrTokenReused               = errors.New("netsec: refresh token already used")
	ErrFamilyGenerationExhausted = errors.New("netsec: refresh family generation exhausted")
)

func (s *TokenSigner) issuePair(now time.Time, lifetime TokenLifetime, account Account, family FamilyID, generation uint64, newID func() (string, error)) (TokenPair, error) {
	accessID, err := newID()
	if err != nil {
		return TokenPair{}, err
	}
	refreshID, err := newID()
	if err != nil {
		return TokenPair{}, err
	}
	accessClaims := TokenClaims{
		Version:         1,
		Kind:            TokenKindAccess,
		AccountID:       account.ID,
		FamilyID:        family,
		Generation:      generation,
		CredentialEpoch: account.CredentialEpoch,
		SessionEpoch:    account.SessionEpoch,
		IssuedAtNS:      now.UnixNano(),
		ExpiresAtNS:     now.Add(lifetime.Access).UnixNano(),
		TokenID:         accessID,
	}
	refreshClaims := TokenClaims{
		Version:         1,
		Kind:            TokenKindRefresh,
		AccountID:       account.ID,
		FamilyID:        family,
		Generation:      generation,
		CredentialEpoch: account.CredentialEpoch,
		SessionEpoch:    account.SessionEpoch,
		IssuedAtNS:      now.UnixNano(),
		ExpiresAtNS:     now.Add(lifetime.Refresh).UnixNano(),
		TokenID:         refreshID,
	}
	access, err := s.sign(accessClaims)
	if err != nil {
		return TokenPair{}, err
	}
	refresh, err := s.sign(refreshClaims)
	if err != nil {
		return TokenPair{}, err
	}
	return TokenPair{
		AccessToken:        access,
		RefreshToken:       refresh,
		AccessExpiresAt:    now.Add(lifetime.Access),
		RefreshExpiresAt:   now.Add(lifetime.Refresh),
		FamilyID:           family,
		Generation:         generation,
		CredentialEpoch:    account.CredentialEpoch,
		SessionEpoch:       account.SessionEpoch,
		AccessTokenClaims:  accessClaims,
		RefreshTokenClaims: refreshClaims,
	}, nil
}
