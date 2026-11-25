package impersonate

import (
	"context"
	"net/http"
)

// AuditEvent records an impersonation decision. It is delivered to the
// AuditSink for both allow and deny so the audit log always contains the
// original requester alongside the attempted impersonated identity.
type AuditEvent struct {
	Requester   UserInfo
	Impersonate UserInfo
	Allowed     bool
	Reason      string
}

// AuditSink receives impersonation audit events.
type AuditSink interface {
	Record(ctx context.Context, event AuditEvent)
}

// AuditSinkFunc adapts a plain function to AuditSink.
type AuditSinkFunc func(ctx context.Context, event AuditEvent)

// Record implements AuditSink.
func (f AuditSinkFunc) Record(ctx context.Context, event AuditEvent) { f(ctx, event) }

// deny writes a 403 and records a deny event.
func deny(ctx context.Context, sink AuditSink, requester UserInfo, target UserInfo, reason string, w http.ResponseWriter) {
	if sink != nil {
		sink.Record(ctx, AuditEvent{
			Requester:   requester,
			Impersonate: target,
			Allowed:     false,
			Reason:      reason,
		})
	}
	http.Error(w, "forbidden: "+reason, http.StatusForbidden)
}

// Middleware returns an http.Handler wrapper that enforces impersonation
// authorization.
//
// The authentication layer must install the requester with WithUser. When no
// impersonation headers are present the request passes through unchanged.
// Otherwise every impersonated object is checked independently against
// authorizer; a single "no opinion", explicit deny, error or malformed header
// rejects the whole request with 403. On success the context user is replaced
// by the impersonated identity only, so downstream authorization never sees
// the requester's groups or permissions.
func Middleware(authorizer Authorizer, sink AuditSink) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := req.Context()

			requester, ok := UserFrom(ctx)
			if !ok {
				deny(ctx, sink, nil, nil, "no authenticated user in request context", w)
				return
			}

			requests, err := buildImpersonationRequests(req)
			if err != nil {
				deny(ctx, sink, requester, nil, err.Error(), w)
				return
			}
			if len(requests) == 0 {
				// No impersonation headers: keep the original identity.
				next.ServeHTTP(w, req)
				return
			}

			// The final record carries the fully assembled impersonated user.
			target := requests[len(requests)-1].requester()

			// Each impersonated object is an independent authorization check.
			for _, item := range requests[:len(requests)-1] {
				decision, reason, authErr := authorizer.Authorize(ctx, item.attrs)
				if authErr != nil {
					deny(ctx, sink, requester, target, authErr.Error(), w)
					return
				}
				if decision != DecisionAllow {
					if reason == "" {
						reason = "impersonation not permitted for " + item.attrs.ResourcePath()
					}
					deny(ctx, sink, requester, target, reason, w)
					return
				}
			}

			if sink != nil {
				sink.Record(ctx, AuditEvent{
					Requester:   requester,
					Impersonate: target,
					Allowed:     true,
				})
			}

			// Only the impersonated identity remains; the requester's groups
			// and permissions are not merged into downstream authorization.
			next.ServeHTTP(w, req.WithContext(WithUser(ctx, target)))
		})
	}
}
