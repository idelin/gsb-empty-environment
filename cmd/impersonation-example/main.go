// Command impersonation-example wires the impersonate package into a tiny
// HTTP server. Run it and try:
//
//	curl -H 'Impersonate-User: alice' \
//	     -H 'Impersonate-Group: platform' \
//	     http://localhost:8080/whoami
package main

import (
	"context"
	"log"
	"net/http"

	"prescreen.local/ps-ecb6db5562/impersonate"
)

func main() {
	// Stand-in for RBAC: this requester may impersonate user alice and the
	// platform group, but nothing else.
	authorizer := &impersonate.RuleAuthorizer{Rules: []impersonate.Rule{
		{Resource: impersonate.ResourceUsers, Name: "alice"},
		{Resource: impersonate.ResourceGroups, Name: "platform"},
	}}

	audit := impersonate.AuditSinkFunc(func(_ context.Context, event impersonate.AuditEvent) {
		target := "<none>"
		if event.Impersonate != nil {
			target = event.Impersonate.GetUserName()
		}
		log.Printf("audit allowed=%t requester=%s impersonate=%s reason=%q",
			event.Allowed, event.Requester.GetUserName(), target, event.Reason)
	})

	mux := http.NewServeMux()
	mux.HandleFunc("/whoami", func(w http.ResponseWriter, req *http.Request) {
		user, ok := impersonate.UserFrom(req.Context())
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		// Downstream authorization sees only this identity.
		_, _ = w.Write([]byte("acting as " + user.GetUserName() + "\n"))
	})

	handler := authenticate(impersonate.Middleware(authorizer, audit)(mux))
	log.Fatal(http.ListenAndServe("localhost:8080", handler))
}

// authenticate is a fake authentication layer: it always installs the same
// requester. A real server would verify credentials here.
func authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requester := &impersonate.DefaultUserInfo{
			Name:   "dev",
			Groups: []string{"dev-team"},
		}
		next.ServeHTTP(w, req.WithContext(impersonate.WithUser(req.Context(), requester)))
	})
}
