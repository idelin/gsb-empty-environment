package netsec

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func testContext() RequestContext {
	return RequestContext{
		Origin:     "https://gw.example.com",
		Tenant:     "tenant-a",
		Credential: "cred-fp-1",
		Negotiated: http.Header{
			"Accept":          {"text/css,application/json"},
			"Accept-Encoding": {"br", "gzip"},
			"Accept-Language": {"zh-CN"},
		},
	}
}

func publicItem(url string, size int64) PushItem {
	return PushItem{URL: url, Tenant: "tenant-a", Size: size}
}

func hasErr(errs []error, target error) bool {
	for _, err := range errs {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

func TestCrossTenantPushRejected(t *testing.T) {
	ctx := testContext()
	items := []PushItem{
		{URL: "/assets/other-tenant.css", Tenant: "tenant-b", Size: 100},
		publicItem("/assets/app.css", 100),
	}
	promises, errs := Authorizer{}.Authorize(ctx, items)
	if len(promises) != 1 || promises[0].Path != "/assets/app.css" {
		t.Fatalf("expected only same-tenant promise, got %+v", promises)
	}
	if !hasErr(errs, ErrCrossTenant) {
		t.Fatalf("expected ErrCrossTenant, got %v", errs)
	}
}

func TestCrossOriginPushRejected(t *testing.T) {
	ctx := testContext()
	items := []PushItem{
		{URL: "https://cdn.evil.example/x.js", Tenant: "tenant-a", Size: 10},
		{URL: "https://gw.example.com/ok.js", Tenant: "tenant-a", Size: 10},
	}
	promises, errs := Authorizer{}.Authorize(ctx, items)
	if len(promises) != 1 || promises[0].Path != "/ok.js" {
		t.Fatalf("expected only same-origin promise, got %+v", promises)
	}
	if !hasErr(errs, ErrCrossOrigin) {
		t.Fatalf("expected ErrCrossOrigin, got %v", errs)
	}
}

func TestCredentialedAndPrivateResourcesRejected(t *testing.T) {
	ctx := testContext()
	items := []PushItem{
		{URL: "/me/avatar.png", Tenant: "tenant-a", Private: true, Size: 50},
		{URL: "/me/settings.json", Tenant: "tenant-a", CredentialBound: true, Size: 50},
		publicItem("/assets/logo.svg", 50),
	}
	promises, errs := Authorizer{}.Authorize(ctx, items)
	if len(promises) != 1 || promises[0].Path != "/assets/logo.svg" {
		t.Fatalf("expected only public promise, got %+v", promises)
	}
	if !hasErr(errs, ErrPrivateResource) {
		t.Fatalf("expected ErrPrivateResource, got %v", errs)
	}
	if !hasErr(errs, ErrCredentialBound) {
		t.Fatalf("expected ErrCredentialBound, got %v", errs)
	}
}

func TestPublicResourcePushAndReceiverAccept(t *testing.T) {
	ctx := testContext()
	promises, errs := Authorizer{}.Authorize(ctx, []PushItem{
		publicItem("/assets/app.css?v=2", 1024),
	})
	if len(errs) != 0 || len(promises) != 1 {
		t.Fatalf("expected one clean promise, got promises=%+v errs=%v", promises, errs)
	}
	promise := promises[0]
	if promise.Key != ctx.Key("/assets/app.css?v=2") {
		t.Fatalf("promise key not bound to request context: %+v", promise.Key)
	}
	if promise.Key.NegotiatedHash == "" || promise.Key.Credential != ctx.Credential {
		t.Fatalf("key must carry negotiation and credential context: %+v", promise.Key)
	}

	receiver := NewReceiver(ctx)
	key, err := receiver.Accept(promise)
	if err != nil {
		t.Fatalf("receiver rejected legitimate promise: %v", err)
	}
	if key != promise.Key {
		t.Fatalf("cache key mismatch on legitimate promise: %+v vs %+v", key, promise.Key)
	}
}

func TestReceiverRejectsCrossContextPush(t *testing.T) {
	issuerCtx := testContext()
	promises, _ := Authorizer{}.Authorize(issuerCtx, []PushItem{
		publicItem("/assets/app.css", 100),
	})
	promise := promises[0]

	foreign := testContext()
	foreign.Tenant = "tenant-b"
	foreign.Credential = "cred-fp-2"
	receiver := NewReceiver(foreign)
	if _, err := receiver.Accept(promise); !errors.Is(err, ErrCrossContext) {
		t.Fatalf("expected ErrCrossContext for foreign session, got %v", err)
	}

	otherNeg := testContext()
	otherNeg.Negotiated = http.Header{"Accept-Encoding": {"gzip"}}
	if _, err := NewReceiver(otherNeg).Accept(promise); !errors.Is(err, ErrCrossContext) {
		t.Fatalf("expected ErrCrossContext for different negotiated headers, got %v", err)
	}
}

func TestReceiverDiscardsCacheKeyMismatch(t *testing.T) {
	ctx := testContext()
	promises, _ := Authorizer{}.Authorize(ctx, []PushItem{
		publicItem("/assets/app.css", 100),
	})
	promise := promises[0]

	tampered := promise
	tampered.Key.Path = "/assets/admin.css"
	if _, err := NewReceiver(ctx).Accept(tampered); !errors.Is(err, ErrCacheKeyMismatch) {
		t.Fatalf("expected ErrCacheKeyMismatch, got %v", err)
	}
}

func TestPushLimitsEnforced(t *testing.T) {
	ctx := testContext()
	auth := Authorizer{MaxPushes: 2, MaxPushBytes: 300}
	items := []PushItem{
		publicItem("/a.css", 100),
		publicItem("/b.css", 100),
		publicItem("/c.css", 100), // exceeds count limit
		publicItem("/d.css", 250), // exceeds byte budget
	}
	promises, errs := auth.Authorize(ctx, items)
	if len(promises) != 2 {
		t.Fatalf("expected 2 promises within limits, got %d", len(promises))
	}
	if len(errs) != 2 {
		t.Fatalf("expected 2 budget rejections, got %v", errs)
	}
	var total int64
	for _, p := range promises {
		total += p.Size
	}
	if total > 300 {
		t.Fatalf("pushed bytes exceed budget: %d", total)
	}
}

type recordingPusher struct {
	http.ResponseWriter
	pushed []string
	err    error
}

func (r *recordingPusher) Push(target string, opts *http.PushOptions) error {
	if r.err != nil {
		return r.err
	}
	r.pushed = append(r.pushed, target)
	return nil
}

func TestPushFailuresDoNotAffectMainResponse(t *testing.T) {
	ctx := testContext()
	promises, _ := Authorizer{}.Authorize(ctx, []PushItem{
		publicItem("/a.css", 10),
		publicItem("/b.css", 10),
	})

	failing := &recordingPusher{err: fmt.Errorf("stream reset")}
	Push(failing, promises) // must not panic or propagate the error

	ok := &recordingPusher{}
	Push(ok, promises)
	if strings.Join(ok.pushed, ",") != "/a.css,/b.css" {
		t.Fatalf("unexpected pushed targets: %v", ok.pushed)
	}

	Push(struct{ http.ResponseWriter }{}, promises) // non-pusher writer: no-op
}

func TestOnlySameContextResourcesPushed(t *testing.T) {
	ctx := testContext()
	items := []PushItem{
		{URL: "/private/inbox.json", Tenant: "tenant-a", Private: true, Size: 10},
		{URL: "/tenant-b/home.css", Tenant: "tenant-b", Size: 10},
		{URL: "https://evil.example/x.js", Tenant: "tenant-a", Size: 10},
		{URL: "/me/theme.css", Tenant: "tenant-a", CredentialBound: true, Size: 10},
		publicItem("/assets/site.css", 10),
	}
	promises, errs := Authorizer{}.Authorize(ctx, items)
	if len(promises) != 1 || promises[0].Path != "/assets/site.css" {
		t.Fatalf("only the public same-context resource may be pushed, got %+v", promises)
	}
	if len(errs) != 4 {
		t.Fatalf("expected 4 rejections, got %v", errs)
	}
	receiver := NewReceiver(ctx)
	if _, err := receiver.Accept(promises[0]); err != nil {
		t.Fatalf("legitimate public promise must be accepted: %v", err)
	}
}
