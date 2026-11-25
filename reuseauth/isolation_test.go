package reuseauth

import (
	"errors"
	"testing"
)

func privateFixture(t *testing.T, knownLength bool) (*fixture, *Navigation, journey, *Broker) {
	t.Helper()
	f := newFixture(t)
	app := f.origins["app"]
	cdn := f.origins["cdn"]
	j := f.evidence(evidenceConfig{
		data:        []byte("PRIVATE!"),
		public:      false,
		audience:    app,
		knownLength: knownLength,
	})
	nav := newTestNav(t, "nav-owner", app, CredentialsInclude)
	allowCDNAnything(t, nav, cdn, VisibilityPrivate)
	broker := NewBroker(f.auth)
	if err := broker.RegisterNavigation(nav); err != nil {
		t.Fatal(err)
	}
	if err := broker.PublishObject(PublishedObject{ID: "dl-private", Chain: j.Chain, Response: j.Response, Manifest: j.Manifest}, 10); err != nil {
		t.Fatal(err)
	}
	return f, nav, j, broker
}

func TestFullReplayWaiterRevocationFencesAllCallbacks(t *testing.T) {
	_, nav, j, broker := privateFixture(t, true)

	cs, err := broker.Wait(nav.ID, "dl-private", j.request("req-revoke", nav.Recipient, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(cs.Added) != 4 {
		t.Fatalf("initial shared ranges = %d, want 4", len(cs.Added))
	}

	resets, err := broker.RevokeNavigation(nav.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(resets) != 1 || !resets[0].Reset || len(resets[0].Removed) != 1 {
		t.Fatalf("resets = %+v, want one coalesced removal [0,8)", resets)
	}

	for _, kind := range []CallbackKind{CallbackComplete, CallbackError, CallbackLength} {
		_, err := broker.DeliverTo(InboundCallback{
			Callback: Callback{
				DownloadID: "dl-private",
				Kind:       kind,
				Public:     false,
				Length:     8,
				At:         21,
			},
			NavID:           nav.ID,
			RequestID:       "req-revoke",
			RecipientOrigin: nav.Recipient,
		})
		if !errors.Is(err, ErrEpoch) || !errors.Is(err, ErrRevoked) {
			t.Fatalf("%s after revoke error = %v, want ErrEpoch/ErrRevoked", kind, err)
		}
	}

	changes := broker.Changes(nav.ID)
	if len(changes) != 2 {
		t.Fatalf("changes len = %d, want initial add and reset only: %+v", len(changes), changes)
	}
	last := changes[len(changes)-1]
	if last.Completed || last.Failed || last.Length != nil {
		t.Fatalf("revoked trace contains late callback data: %+v", last)
	}
}

func TestFullReplayTopLevelSwitchFencesLateLength(t *testing.T) {
	f := newFixture(t)
	app := f.origins["app"]
	cdn := f.origins["cdn"]
	j := f.evidence(evidenceConfig{data: []byte("switch!!"), public: true, knownLength: false})
	nav := newTestNav(t, "nav-switch", app, CredentialsOmit)
	allowCDNAnything(t, nav, cdn, VisibilityPublic)
	broker := NewBroker(f.auth)
	if err := broker.RegisterNavigation(nav); err != nil {
		t.Fatal(err)
	}
	if err := broker.PublishObject(PublishedObject{ID: "dl-switch", Chain: j.Chain, Response: j.Response, Manifest: j.Manifest}, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := broker.Wait(nav.ID, "dl-switch", j.request("req-switch", app, nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := broker.SwitchTopLevel(nav.ID, "https://other.example", 20); err != nil {
		t.Fatal(err)
	}

	_, err := broker.DeliverTo(InboundCallback{
		Callback:        Callback{DownloadID: "dl-switch", Kind: CallbackLength, Public: true, Length: 8, At: 21},
		NavID:           nav.ID,
		RequestID:       "req-switch",
		RecipientOrigin: app,
	})
	if !errors.Is(err, ErrEpoch) {
		t.Fatalf("late length after switch error = %v, want ErrEpoch", err)
	}
	if _, ok := broker.View(nav.ID, "req-switch"); ok {
		t.Fatal("switched navigation retained old request view")
	}

	newGenerationReq := j.request("req-switch-new", app, nil)
	newGenerationReq.Now = 22
	if _, err := broker.Wait(nav.ID, "dl-switch", newGenerationReq); !errors.Is(err, ErrPolicyDenied) && !errors.Is(err, ErrNotFound) {
		t.Fatalf("new top-level site unexpectedly inherited old policy, err=%v", err)
	}
	if err := broker.Allow(nav.ID, PolicyDecision{
		At:         21,
		Source:     app,
		Target:     cdn,
		Action:     ActionAny,
		Visibility: VisibilityPublic,
	}); err != nil {
		t.Fatal(err)
	}
	cs, err := broker.Wait(nav.ID, "dl-switch", newGenerationReq)
	if err != nil {
		t.Fatalf("new explicit epoch policy should allow reuse: %v", err)
	}
	if cs.Epoch != 2 || len(cs.Added) != 4 {
		t.Fatalf("new generation changeset = %+v, want epoch 2 and four ranges", cs)
	}
}

func TestFullReplayLateLengthNotificationIsolation(t *testing.T) {
	f, owner, j, broker := privateFixture(t, false)
	evil := f.origins["evil"]

	if _, err := broker.Wait(owner.ID, "dl-private", j.request("req-length", owner.Recipient, nil)); err != nil {
		t.Fatal(err)
	}

	evilNav := newTestNav(t, "nav-evil", evil, CredentialsInclude)
	if err := evilNav.Allow(PolicyDecision{
		At:         2,
		Source:     evil,
		Target:     f.origins["cdn"],
		Action:     ActionAny,
		Visibility: VisibilityAny,
	}); err != nil {
		t.Fatal(err)
	}
	if err := broker.RegisterNavigation(evilNav); err != nil {
		t.Fatal(err)
	}
	_, privateErr := broker.Wait(evilNav.ID, "dl-private", j.request("req-evil-length", evil, nil))
	if !errors.Is(privateErr, ErrNotFound) || !errors.Is(privateErr, ErrOrigin) {
		t.Fatalf("evil private wait error = %v, want NotFound wrapping Origin", privateErr)
	}
	_, missingErr := broker.Wait(evilNav.ID, "dl-missing", j.request("req-missing", evil, nil))
	if !errors.Is(missingErr, ErrNotFound) {
		t.Fatalf("missing wait error = %v, want NotFound", missingErr)
	}
	if NavigationFacingError(privateErr) != ErrNotFound || NavigationFacingError(missingErr) != ErrNotFound {
		t.Fatal("private and missing lookups are not collapsed to identical ErrNotFound")
	}
	if privateErr.Error() != missingErr.Error() {
		t.Fatalf("error text differs: private=%q missing=%q", privateErr, missingErr)
	}

	_, err := broker.DeliverTo(InboundCallback{
		Callback:        Callback{DownloadID: "dl-private", Kind: CallbackLength, Public: false, Length: 8, At: 11},
		NavID:           owner.ID,
		RequestID:       "req-length",
		RecipientOrigin: evil,
	})
	if !errors.Is(err, ErrOrigin) {
		t.Fatalf("wrong recipient origin error = %v, want ErrOrigin", err)
	}

	_, err = broker.DeliverTo(InboundCallback{
		Callback:        Callback{DownloadID: "dl-private", Kind: CallbackLength, Public: true, Length: 8, At: 11},
		NavID:           owner.ID,
		RequestID:       "req-length",
		RecipientOrigin: owner.Recipient,
	})
	if !errors.Is(err, ErrPublicFlag) {
		t.Fatalf("spoofed public flag error = %v, want ErrPublicFlag", err)
	}

	cs, err := broker.DeliverTo(InboundCallback{
		Callback:        Callback{DownloadID: "dl-private", Kind: CallbackLength, Public: false, Length: 8, At: 11},
		NavID:           owner.ID,
		RequestID:       "req-length",
		RecipientOrigin: owner.Recipient,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cs.Length == nil || *cs.Length != 8 {
		t.Fatalf("owner length change = %+v, want 8", cs.Length)
	}

	_, err = broker.DeliverTo(InboundCallback{
		Callback:        Callback{DownloadID: "dl-private", Kind: CallbackLength, Public: false, Length: 8, At: 11},
		NavID:           evilNav.ID,
		RequestID:       "req-length",
		RecipientOrigin: evil,
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-navigation addressed callback error = %v, want ErrNotFound", err)
	}
	if _, ok := broker.View(evilNav.ID, "req-length"); ok {
		t.Fatal("unauthorized navigation has visible private length state")
	}
	if changes := broker.Changes(evilNav.ID); len(changes) != 0 {
		t.Fatalf("evil navigation replay = %+v, want empty", changes)
	}

	view, ok := broker.View(owner.ID, "req-length")
	if !ok || view.Length == nil || *view.Length != 8 {
		t.Fatalf("owner view = %+v ok=%v, want length 8", view, ok)
	}
}

func TestAllCallbackKindsEnforceOriginAndPublicFlag(t *testing.T) {
	f := newFixture(t)
	app := f.origins["app"]
	evil := f.origins["evil"]
	cdn := f.origins["cdn"]
	j := f.evidence(evidenceConfig{data: []byte("matrix00"), public: true, knownLength: true})
	nav := newTestNav(t, "nav-matrix", app, CredentialsOmit)
	allowCDNAnything(t, nav, cdn, VisibilityPublic)
	broker := NewBroker(f.auth)
	if err := broker.RegisterNavigation(nav); err != nil {
		t.Fatal(err)
	}
	if err := broker.PublishObject(PublishedObject{ID: "dl-matrix", Chain: j.Chain, Response: j.Response, Manifest: j.Manifest}, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := broker.Wait(nav.ID, "dl-matrix", j.request("req-matrix", app, nil)); err != nil {
		t.Fatal(err)
	}

	for _, kind := range []CallbackKind{CallbackComplete, CallbackError, CallbackLength} {
		_, err := broker.DeliverTo(InboundCallback{
			Callback: Callback{
				DownloadID: "dl-matrix",
				Kind:       kind,
				Public:     true,
				Length:     8,
				Code:       "safe-error",
				At:         11,
			},
			NavID:           nav.ID,
			RequestID:       "req-matrix",
			RecipientOrigin: evil,
		})
		if !errors.Is(err, ErrOrigin) {
			t.Fatalf("%s wrong origin error = %v, want ErrOrigin", kind, err)
		}

		_, err = broker.DeliverTo(InboundCallback{
			Callback: Callback{
				DownloadID: "dl-matrix",
				Kind:       kind,
				Public:     false,
				Length:     8,
				Code:       "safe-error",
				At:         11,
			},
			NavID:           nav.ID,
			RequestID:       "req-matrix",
			RecipientOrigin: app,
		})
		if !errors.Is(err, ErrPublicFlag) {
			t.Fatalf("%s wrong public flag error = %v, want ErrPublicFlag", kind, err)
		}
	}

	if changes := broker.Changes(nav.ID); len(changes) != 1 {
		t.Fatalf("rejected callbacks mutated replay: %+v", changes)
	}
}

func TestFullReplayRevocationBeforeCacheHit(t *testing.T) {
	f := newFixture(t)
	app := f.origins["app"]
	cdn := f.origins["cdn"]
	j := f.evidence(evidenceConfig{data: []byte("hit-now!"), public: true, knownLength: true})
	nav := newTestNav(t, "nav-hit", app, CredentialsOmit)
	allowCDNAnything(t, nav, cdn, VisibilityPublic)
	broker := NewBroker(f.auth)
	if err := broker.RegisterNavigation(nav); err != nil {
		t.Fatal(err)
	}
	if err := broker.PublishObject(PublishedObject{ID: "dl-hit", Chain: j.Chain, Response: j.Response, Manifest: j.Manifest}, 10); err != nil {
		t.Fatal(err)
	}

	pending, err := broker.PrepareHit(nav.ID, "dl-hit", j.request("req-hit", app, nil))
	if err != nil {
		t.Fatal(err)
	}
	if changes := broker.Changes(nav.ID); len(changes) != 0 {
		t.Fatalf("PrepareHit leaked changes %+v", changes)
	}
	if err := broker.Deny(nav.ID, PolicyDecision{
		At:         11,
		Source:     app,
		Target:     cdn,
		Action:     ActionRead,
		Visibility: VisibilityPublic,
	}); err != nil {
		t.Fatal(err)
	}
	cs, err := broker.CommitHit(pending)
	if !errors.Is(err, ErrNotFound) || !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("commit after deny error = %v, want NotFound/PolicyDenied", err)
	}
	if !cs.Empty() {
		t.Fatalf("denied commit produced changeset %+v", cs)
	}
	if changes := broker.Changes(nav.ID); len(changes) != 0 {
		t.Fatalf("denied commit wrote replay changes %+v", changes)
	}

	revokeNav := newTestNav(t, "nav-hit-revoked", app, CredentialsOmit)
	allowCDNAnything(t, revokeNav, cdn, VisibilityPublic)
	if err := broker.RegisterNavigation(revokeNav); err != nil {
		t.Fatal(err)
	}
	pendingRevoke, err := broker.PrepareHit(revokeNav.ID, "dl-hit", j.request("req-hit-2", app, nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := broker.RevokeNavigation(revokeNav.ID, 12); err != nil {
		t.Fatal(err)
	}
	_, err = broker.CommitHit(pendingRevoke)
	if !errors.Is(err, ErrNotFound) || !errors.Is(err, ErrEpoch) {
		t.Fatalf("commit after revoke error = %v, want NotFound/Epoch", err)
	}
}
