package netsec

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

type userContextKey struct{}

// WithUser returns a context carrying the authenticated identity. Authentication
// middleware should install the requestor identity with this function before
// the impersonation middleware runs.
func WithUser(ctx context.Context, user Identity) context.Context {
	return context.WithValue(ctx, userContextKey{}, user)
}

// UserFrom retrieves the identity installed by WithUser.
func UserFrom(ctx context.Context) Identity {
	user, _ := ctx.Value(userContextKey{}).(Identity)
	return user
}

// AuditEvent records an impersonation decision. On success both Requestor and
// Impersonated are populated so the audit trail preserves the original
// identity and the identity that was substituted for it.
type AuditEvent struct {
	Time         time.Time
	Method       string
	Path         string
	Requestor    UserInfo
	Impersonated *UserInfo
	Allowed      bool
	Reason       string
}

// AuditSink receives audit events. Implementations must be safe for
// concurrent use because HTTP handlers run in parallel.
type AuditSink interface {
	RecordAudit(ctx context.Context, event AuditEvent)
}

// AuditSinkFunc adapts a plain function to AuditSink.
type AuditSinkFunc func(ctx context.Context, event AuditEvent)

// RecordAudit implements AuditSink.
func (f AuditSinkFunc) RecordAudit(ctx context.Context, event AuditEvent) {
	f(ctx, event)
}

type noopAuditSink struct{}

func (noopAuditSink) RecordAudit(context.Context, AuditEvent) {}

// Middleware validates impersonation headers. On success it strips the
// impersonation headers, installs the impersonated identity in the request
// context and emits an audit event; downstream handlers and authorizers see
// only the impersonated identity. Any failed check yields HTTP 403.
type Middleware struct {
	Authorizer Authorizer
	Sink       AuditSink
}

// Wrap returns an http.Handler enforcing impersonation rules before next.
func (m Middleware) Wrap(next http.Handler) http.Handler {
	if m.Authorizer == nil {
		panic("netsec: impersonation middleware requires an Authorizer")
	}
	sink := m.Sink
	if sink == nil {
		sink = noopAuditSink{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestor := UserFrom(r.Context())
		if requestor == nil {
			http.Error(w, "unauthenticated request", http.StatusUnauthorized)
			return
		}

		impersonated, requested, err := Check(requestor, r.Header, m.Authorizer)
		if requested {
			event := AuditEvent{
				Time:      time.Now().UTC(),
				Method:    r.Method,
				Path:      r.URL.Path,
				Requestor: asUserInfo(requestor),
				Reason:    errString(err),
			}
			if err == nil {
				event.Allowed = true
				event.Impersonated = &impersonated
			}
			sink.RecordAudit(r.Context(), event)
		}
		if err != nil {
			writeForbidden(w, err)
			return
		}

		if requested {
			// The substituted identity is the only identity downstream code
			// may use; the requestor's permissions are not carried forward.
			r = r.WithContext(WithUser(r.Context(), impersonated))
			stripImpersonationHeaders(r)
		}
		next.ServeHTTP(w, r)
	})
}

// stripImpersonationHeaders removes every impersonation header so internal
// components cannot re-parse or trust the raw substitution request.
func stripImpersonationHeaders(r *http.Request) {
	for header := range r.Header {
		if strings.EqualFold(header, ImpersonateUserHeader) ||
			strings.EqualFold(header, ImpersonateUIDHeader) ||
			strings.EqualFold(header, ImpersonateGroupHeader) ||
			strings.HasPrefix(strings.ToLower(header), strings.ToLower(ImpersonateExtraPrefix)) {
			r.Header.Del(header)
		}
	}
}

func asUserInfo(identity Identity) UserInfo {
	if user, ok := identity.(UserInfo); ok {
		return user
	}
	return UserInfo{
		Name:   identity.GetName(),
		UID:    identity.GetUID(),
		Groups: identity.GetGroups(),
		Extra:  identity.GetExtra(),
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

type statusDetails struct {
	Name string `json:"name,omitempty"`
	Kind string `json:"kind,omitempty"`
}

type forbiddenStatus struct {
	Kind    string        `json:"kind"`
	Status  string        `json:"status"`
	Message string        `json:"message"`
	Reason  string        `json:"reason"`
	Details statusDetails `json:"details,omitempty"`
	Code    int           `json:"code"`
}

func writeForbidden(w http.ResponseWriter, err error) {
	message := err.Error()
	reason := "Forbidden"
	var details statusDetails

	var forbidden *ForbiddenError
	if errors.As(err, &forbidden) {
		reason = "Forbidden"
		details = statusDetails{
			Name: forbidden.Attrs.Name,
			Kind: forbidden.Attrs.CombinedResource(),
		}
	}
	if err == ErrGroupWithoutUser {
		message = ErrGroupWithoutUser.Error()
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(forbiddenStatus{
		Kind:    "Status",
		Status:  "Failure",
		Message: message,
		Reason:  reason,
		Details: details,
		Code:    http.StatusForbidden,
	})
}
