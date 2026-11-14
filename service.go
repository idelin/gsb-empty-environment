package netsec

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Authorization is the device authorization response returned to the device.
type Authorization struct {
	DeviceCode   string
	UserCode     string
	ClientID     string
	Scopes       []string
	PollInterval time.Duration
	ExpiresAt    time.Time
	ExpiresIn    time.Duration
}

// DeviceAuthorization is an alias for Authorization.
type DeviceAuthorization = Authorization

// TokenRequest redeems a device code at the token endpoint.
//
// An omitted Scopes field (nil) requests all approved scopes. A non-nil value
// is the exact set requested by the caller and must be a subset of the frozen
// approval. Scope is the whitespace-delimited form; when both fields are set,
// their scopes are combined and checked together. An empty subject lets the
// service use the subject captured during approval; a non-empty subject must
// match it.
type TokenRequest struct {
	DeviceCode string
	ClientID   string
	Subject    string
	Scopes     []string
	Scope      string
}

// Token is the successful token endpoint result.
type Token struct {
	AccessToken string
	TokenType   string
	ClientID    string
	Subject     string
	Scopes      []string
	IssuedAt    time.Time
	ExpiresAt   time.Time
	ExpiresIn   time.Duration
}

// TokenResponse is an alias for Token.
type TokenResponse = Token

// Service is an in-memory device authorization state machine.
type Service struct {
	mutex        sync.Mutex
	config       Config
	byDeviceCode map[string]*authorization
	byUserCode   map[string]*authorization
}

type authorizationStatus uint8

const (
	statusPending authorizationStatus = iota
	statusApproved
)

type authorization struct {
	deviceCode     string
	userCode       string
	clientID       string
	requestedScope []string
	approvedScope  []string
	subject        string
	pollInterval   time.Duration
	expiresAt      time.Time
	lastPollAt     time.Time
	status         authorizationStatus
}

// Begin starts a device authorization for a client and requested scope set.
func (service *Service) Begin(ctx context.Context, clientID string, requestedScopes []string) (*Authorization, error) {
	ctx = contextOrBackground(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(clientID) == "" {
		return nil, ErrInvalidClient
	}

	requestedScopes = normalizeScopes(requestedScopes)

	service.mutex.Lock()
	defer service.mutex.Unlock()

	now := service.config.Now()
	var grant *authorization
	for attempt := 0; attempt < 16; attempt++ {
		deviceCode, err := randomDeviceCode(service.config.Random, service.config.DeviceCodeEntropy)
		if err != nil {
			return nil, fmt.Errorf("generate device code: %w", err)
		}
		userCode, err := randomUserCode(service.config.Random, service.config.UserCodeLength)
		if err != nil {
			return nil, fmt.Errorf("generate user code: %w", err)
		}
		if _, exists := service.byDeviceCode[deviceCode]; exists {
			continue
		}
		userKey := normalizeUserCode(userCode)
		if _, exists := service.byUserCode[userKey]; exists {
			continue
		}
		grant = &authorization{
			deviceCode:     deviceCode,
			userCode:       userCode,
			clientID:       clientID,
			requestedScope: cloneStrings(requestedScopes),
			pollInterval:   service.config.PollInterval,
			expiresAt:      now.Add(service.config.DeviceCodeTTL),
			lastPollAt:     time.Time{},
			status:         statusPending,
		}
		service.byDeviceCode[deviceCode] = grant
		service.byUserCode[userKey] = grant
		break
	}
	if grant == nil {
		return nil, errors.New("unable to allocate unique device authorization")
	}

	return &Authorization{
		DeviceCode:   grant.deviceCode,
		UserCode:     grant.userCode,
		ClientID:     grant.clientID,
		Scopes:       cloneStrings(grant.requestedScope),
		PollInterval: grant.pollInterval,
		ExpiresAt:    grant.expiresAt,
		ExpiresIn:    service.config.DeviceCodeTTL,
	}, nil
}

// Start is an alias for Begin.
func (service *Service) Start(ctx context.Context, clientID string, requestedScopes []string) (*Authorization, error) {
	return service.Begin(ctx, clientID, requestedScopes)
}

// Authorize is an alias for Begin.
func (service *Service) Authorize(ctx context.Context, clientID string, requestedScopes []string) (*Authorization, error) {
	return service.Begin(ctx, clientID, requestedScopes)
}

// Approve records the subject's approval of all originally requested scopes.
func (service *Service) Approve(ctx context.Context, userCode, subject string) error {
	return service.approve(ctx, userCode, subject, nil, true)
}

// ApproveScopes records the subject's approval of exactly the supplied scope
// subset. A nil or empty set approves no scopes and never defaults to the
// client's requested set.
func (service *Service) ApproveScopes(ctx context.Context, userCode, subject string, approvedScopes []string) error {
	return service.approve(ctx, userCode, subject, approvedScopes, false)
}

// ApproveWithScopes is an alias for ApproveScopes.
func (service *Service) ApproveWithScopes(ctx context.Context, userCode, subject string, approvedScopes []string) error {
	return service.ApproveScopes(ctx, userCode, subject, approvedScopes)
}

func (service *Service) approve(ctx context.Context, userCode, subject string, approvedScopes []string, defaultToRequested bool) error {
	ctx = contextOrBackground(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return ErrInvalidSubject
	}
	userKey := normalizeUserCode(userCode)
	if userKey == "" {
		return ErrInvalidGrant
	}
	service.mutex.Lock()
	defer service.mutex.Unlock()

	now := service.config.Now()
	grant, exists := service.byUserCode[userKey]
	if !exists {
		return ErrInvalidGrant
	}
	if !now.Before(grant.expiresAt) {
		service.delete(grant)
		return ErrInvalidGrant
	}
	if grant.status != statusPending {
		return ErrAlreadyDecided
	}

	scopes := approvedScopes
	if defaultToRequested && scopes == nil {
		scopes = grant.requestedScope
	}
	scopes = normalizeScopes(scopes)
	if !scopeSubset(scopes, grant.requestedScope) {
		return ErrScopeNotRequested
	}

	grant.approvedScope = cloneStrings(scopes)
	grant.subject = subject
	grant.status = statusApproved
	return nil
}

// Deny rejects a pending authorization. The device code becomes terminal and
// cannot later be approved or redeemed.
func (service *Service) Deny(ctx context.Context, userCode, subject string) error {
	ctx = contextOrBackground(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(subject) == "" {
		return ErrInvalidSubject
	}
	userKey := normalizeUserCode(userCode)
	if userKey == "" {
		return ErrInvalidGrant
	}
	service.mutex.Lock()
	defer service.mutex.Unlock()

	now := service.config.Now()
	grant, exists := service.byUserCode[userKey]
	if !exists {
		return ErrInvalidGrant
	}
	if !now.Before(grant.expiresAt) {
		service.delete(grant)
		return ErrInvalidGrant
	}
	if grant.status == statusApproved {
		return ErrAlreadyDecided
	}
	service.delete(grant)
	return nil
}

// Exchange redeems a device code exactly once.
func (service *Service) Exchange(ctx context.Context, request TokenRequest) (*Token, error) {
	ctx = contextOrBackground(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	service.mutex.Lock()
	defer service.mutex.Unlock()

	now := service.config.Now()
	grant, exists := service.byDeviceCode[request.DeviceCode]
	if !exists {
		return nil, ErrInvalidGrant
	}
	if !secureStringEqual(request.ClientID, grant.clientID) {
		return nil, ErrInvalidGrant
	}
	if !now.Before(grant.expiresAt) {
		service.delete(grant)
		return nil, ErrInvalidGrant
	}

	if !grant.lastPollAt.IsZero() && now.Sub(grant.lastPollAt) < grant.pollInterval {
		return nil, ErrSlowDown
	}

	if grant.status == statusPending {
		grant.lastPollAt = now
		return nil, ErrAuthorizationPending
	}

	if request.Subject != "" && !secureStringEqual(strings.TrimSpace(request.Subject), grant.subject) {
		return nil, ErrInvalidGrant
	}

	scopes := request.Scopes
	if request.Scope != "" {
		formScopes := normalizeScopes([]string{request.Scope})
		if scopes == nil {
			scopes = formScopes
		} else {
			scopes = normalizeScopes(append(cloneStrings(scopes), formScopes...))
		}
	}
	if scopes == nil {
		scopes = grant.approvedScope
	}
	scopes = normalizeScopes(scopes)
	if !scopeSubset(scopes, grant.approvedScope) {
		return nil, ErrInvalidGrant
	}

	accessToken, err := randomAccessToken(service.config.Random, service.config.DeviceCodeEntropy)
	if err != nil {
		return nil, fmt.Errorf("generate access token: %w", err)
	}

	service.delete(grant)
	return &Token{
		AccessToken: accessToken,
		TokenType:   "Bearer",
		ClientID:    grant.clientID,
		Subject:     grant.subject,
		Scopes:      cloneStrings(scopes),
		IssuedAt:    now,
		ExpiresAt:   now.Add(service.config.TokenTTL),
		ExpiresIn:   service.config.TokenTTL,
	}, nil
}

// Poll is a convenience wrapper around Exchange.
func (service *Service) Poll(ctx context.Context, deviceCode, clientID, subject string, requestedScopes []string) (*Token, error) {
	return service.Exchange(ctx, TokenRequest{
		DeviceCode: deviceCode,
		ClientID:   clientID,
		Subject:    subject,
		Scopes:     requestedScopes,
	})
}

// Redeem is an alias for Poll.
func (service *Service) Redeem(ctx context.Context, deviceCode, clientID, subject string, requestedScopes []string) (*Token, error) {
	return service.Poll(ctx, deviceCode, clientID, subject, requestedScopes)
}

func (service *Service) delete(grant *authorization) {
	delete(service.byDeviceCode, grant.deviceCode)
	delete(service.byUserCode, normalizeUserCode(grant.userCode))
}

func contextOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func normalizeScopes(scopes []string) []string {
	if scopes == nil {
		return nil
	}
	result := make([]string, 0, len(scopes))
	seen := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		for _, part := range strings.Fields(scope) {
			if _, exists := seen[part]; exists {
				continue
			}
			seen[part] = struct{}{}
			result = append(result, part)
		}
	}
	return result
}

func scopeSubset(subset, superset []string) bool {
	allowed := make(map[string]struct{}, len(superset))
	for _, scope := range superset {
		allowed[scope] = struct{}{}
	}
	for _, scope := range subset {
		if _, exists := allowed[scope]; !exists {
			return false
		}
	}
	return true
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string(nil), values...)
}

func normalizeUserCode(userCode string) string {
	var result strings.Builder
	for _, character := range strings.ToUpper(strings.TrimSpace(userCode)) {
		if character == '-' || unicode.IsSpace(character) {
			continue
		}
		result.WriteRune(character)
	}
	return result.String()
}

const userCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func randomUserCode(random io.Reader, length int) (string, error) {
	raw := make([]byte, length)
	if _, err := io.ReadFull(random, raw); err != nil {
		return "", err
	}
	encoded := make([]byte, length)
	for index, value := range raw {
		encoded[index] = userCodeAlphabet[int(value)%len(userCodeAlphabet)]
	}

	if length >= 6 {
		middle := length / 2
		return string(encoded[:middle]) + "-" + string(encoded[middle:]), nil
	}
	return string(encoded), nil
}

func randomDeviceCode(random io.Reader, entropyBytes int) (string, error) {
	return randomURLSafe(random, entropyBytes)
}

func randomAccessToken(random io.Reader, entropyBytes int) (string, error) {
	return randomURLSafe(random, entropyBytes)
}

func randomURLSafe(random io.Reader, entropyBytes int) (string, error) {
	raw := make([]byte, entropyBytes)
	if _, err := io.ReadFull(random, raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func secureStringEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}
