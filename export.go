package netsec

import (
	"context"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const exportGrantFormat = "netsec.state.export-grant.v1"

// ExportRequest describes a plaintext export. Authorization is the grant token
// issued by a trusted operator or service. Token is accepted as an alias for
// compatibility with callers that use that name.
type ExportRequest struct {
	Reason        string
	Destination   string
	Authorization string
	Token         string
	Approver      string
}

func (r ExportRequest) grantToken() string {
	if r.Authorization != "" {
		return r.Authorization
	}
	return r.Token
}

// ExportAuthorizer decides whether a plaintext export may proceed. The Store
// denies export when no authorizer is configured. Implementations must fail
// closed and should bind approval to the caller, workspace, account, and
// request purpose.
type ExportAuthorizer interface {
	AuthorizeExport(ctx context.Context, caller Caller, request ExportRequest) error
}

// ExportAuthorizerFunc adapts a function to ExportAuthorizer.
type ExportAuthorizerFunc func(ctx context.Context, caller Caller, request ExportRequest) error

// AuthorizeExport implements ExportAuthorizer.
func (f ExportAuthorizerFunc) AuthorizeExport(ctx context.Context, caller Caller, request ExportRequest) error {
	return f(ctx, caller, request)
}

// StaticExportAuthorizer permits only the listed caller IDs. It is useful for
// tightly scoped service accounts; the signed ExportAuthority is preferable
// when approval must be request-specific.
type StaticExportAuthorizer struct {
	AllowedCallerIDs map[string]struct{}
}

// AuthorizeExport implements ExportAuthorizer.
func (a StaticExportAuthorizer) AuthorizeExport(_ context.Context, caller Caller, _ ExportRequest) error {
	if _, ok := a.AllowedCallerIDs[caller.ID]; !ok {
		return ErrExportDenied
	}
	return nil
}

// ExportState returns plaintext state only after caller authentication,
// workspace/account checks, and explicit export authorization.
func (s *Store) ExportState(ctx context.Context, caller Caller, request ExportRequest) ([]byte, error) {
	caller, err := s.authorize(ctx, caller, "export")
	if err != nil {
		return nil, err
	}
	if s.exportAuthorizer == nil {
		err := fmt.Errorf("%w: no export authorizer is configured", ErrExportDenied)
		s.audit(ctx, "export", caller, false, "export denied by default", err)
		return nil, err
	}
	if err := s.exportAuthorizer.AuthorizeExport(ctx, caller, request); err != nil {
		denied := fmt.Errorf("%w: %v", ErrExportDenied, err)
		s.audit(ctx, "export", caller, false, "export authorization failed", denied)
		return nil, denied
	}

	unlockRoot := lockRoot(s.root)
	defer unlockRoot()
	version, _, err := s.readVersionRecord(ctx)
	if err != nil {
		s.audit(ctx, "export", caller, false, "version read failed", err)
		return nil, err
	}
	state, err := s.readStateFor(ctx, version)
	if err != nil {
		s.audit(ctx, "export", caller, false, "state read failed", err)
		return nil, err
	}
	s.audit(ctx, "export", caller, true, "plaintext export authorized", nil)
	return state, nil
}

// Export is an alias for ExportState.
func (s *Store) Export(ctx context.Context, caller Caller, request ExportRequest) ([]byte, error) {
	return s.ExportState(ctx, caller, request)
}

// ExportTo writes authorized plaintext to request.Destination. The destination
// must be outside the encrypted state root, must not be a symlink, and is
// created with 0600 permissions.
func (s *Store) ExportTo(ctx context.Context, caller Caller, request ExportRequest) error {
	if strings.TrimSpace(request.Destination) == "" {
		return fmt.Errorf("%w: export destination is required", ErrExportDenied)
	}
	state, err := s.ExportState(ctx, caller, request)
	if err != nil {
		return err
	}

	destination, err := filepath.Abs(request.Destination)
	if err != nil {
		return fmt.Errorf("netsec: resolve export destination: %w", err)
	}
	relative, err := filepath.Rel(s.root, destination)
	if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%w: refusing to write plaintext inside the encrypted state root", ErrExportDenied)
	}
	if info, err := os.Lstat(destination); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: export destination is a symlink", ErrExportDenied)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("netsec: inspect export destination: %w", err)
	}

	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, stateFileMode)
	if err != nil {
		return fmt.Errorf("netsec: create export file: %w", err)
	}
	if err := file.Chmod(stateFileMode); err != nil {
		file.Close()
		return fmt.Errorf("netsec: secure export file: %w", err)
	}
	if _, err := file.Write(state); err != nil {
		file.Close()
		return fmt.Errorf("netsec: write export file: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("netsec: sync export file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("netsec: close export file: %w", err)
	}
	s.audit(ctx, "export-to-file", caller, true, "plaintext export written", nil)
	return nil
}

type exportGrant struct {
	Format      string `json:"format"`
	CallerID    string `json:"caller"`
	WorkspaceID string `json:"workspace"`
	AccountID   string `json:"account"`
	Reason      string `json:"reason"`
	Destination string `json:"destination"`
	Approver    string `json:"approver"`
	ExpiresAt   int64  `json:"expires_at"`
	Nonce       string `json:"nonce"`
}

// ExportAuthority issues and verifies short-lived, signed export grants. It
// makes "explicit authorization" a separate, auditable step rather than a
// boolean supplied by the requesting caller.
type ExportAuthority struct {
	key []byte
	now func() time.Time
}

// NewExportAuthority derives the grant-signing key from secret. Use a value
// independent of the state encryption keys, supplied by a KMS or service
// configuration outside the workspace.
func NewExportAuthority(secret []byte) (*ExportAuthority, error) {
	if len(secret) == 0 {
		return nil, fmt.Errorf("%w: empty export authority secret", ErrInvalidConfig)
	}
	key, err := hkdf.Key(sha256.New, secret, nil, "netsec/state/v1/export-authority", 32)
	if err != nil {
		return nil, err
	}
	return &ExportAuthority{key: key, now: time.Now}, nil
}

// WithClock sets the clock used for grant expiry. It is intended for tests.
func (a *ExportAuthority) WithClock(clock func() time.Time) *ExportAuthority {
	if clock != nil {
		a.now = clock
	}
	return a
}

// Grant creates a signed authorization for one caller, workspace, account,
// reason, destination, and time window. A zero ttl uses five minutes.
func (a *ExportAuthority) Grant(caller Caller, reason, destination string, ttl time.Duration) (string, error) {
	if caller.ID == "" || caller.WorkspaceID == "" || caller.AccountID == "" {
		return "", fmt.Errorf("%w: grant requires a complete caller identity", ErrExportDenied)
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	nonce := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("netsec: create export nonce: %w", err)
	}
	payload := exportGrant{
		Format:      exportGrantFormat,
		CallerID:    caller.ID,
		WorkspaceID: caller.WorkspaceID,
		AccountID:   caller.AccountID,
		Reason:      reason,
		Destination: destination,
		Approver:    "authority",
		ExpiresAt:   a.now().Add(ttl).Unix(),
		Nonce:       base64.RawURLEncoding.EncodeToString(nonce),
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("netsec: encode export grant: %w", err)
	}
	mac := hmac.New(sha256.New, a.key)
	mac.Write(payloadBytes)
	return base64.RawURLEncoding.EncodeToString(payloadBytes) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// AuthorizeExport verifies a signed grant against the current caller and
// request.
func (a *ExportAuthority) AuthorizeExport(_ context.Context, caller Caller, request ExportRequest) error {
	token := request.grantToken()
	if token == "" {
		return fmt.Errorf("%w: export grant is required", ErrExportDenied)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return fmt.Errorf("%w: malformed export grant", ErrExportDenied)
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return fmt.Errorf("%w: malformed export grant", ErrExportDenied)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return fmt.Errorf("%w: malformed export grant signature", ErrExportDenied)
	}
	mac := hmac.New(sha256.New, a.key)
	mac.Write(payloadBytes)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return fmt.Errorf("%w: invalid export grant signature", ErrExportDenied)
	}

	var grant exportGrant
	if err := json.Unmarshal(payloadBytes, &grant); err != nil {
		return fmt.Errorf("%w: malformed export grant payload", ErrExportDenied)
	}
	if grant.Format != exportGrantFormat {
		return fmt.Errorf("%w: unsupported export grant", ErrExportDenied)
	}
	if a.now().Unix() > grant.ExpiresAt {
		return fmt.Errorf("%w: export grant expired", ErrExportDenied)
	}
	if grant.CallerID != caller.ID ||
		grant.WorkspaceID != caller.WorkspaceID ||
		grant.AccountID != caller.AccountID {
		return fmt.Errorf("%w: export grant is bound to another caller", ErrExportDenied)
	}
	if grant.Reason != request.Reason {
		return fmt.Errorf("%w: export reason does not match grant", ErrExportDenied)
	}
	if grant.Destination != request.Destination {
		return fmt.Errorf("%w: export destination does not match grant", ErrExportDenied)
	}
	return nil
}
