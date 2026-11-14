package netsec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGuestTicketCannotResumeOnEnterpriseSSID(t *testing.T) {
	rig := newTestRig(t)
	issued := rig.issue(t, guestPolicy(), guestAP(1), testTLS("initial"), 10)
	if issued.Grant.VLAN != 10 {
		t.Fatalf("guest grant VLAN = %d, want 10", issued.Grant.VLAN)
	}

	corpReq := rig.resumeRequest(issued.Ticket, corpPolicy(), corpAP(), resumedTLS("enterprise-resume", issued.Ticket))
	if _, err := rig.auth.Resume(context.Background(), corpReq); !errors.Is(err, ErrPolicyDomainMismatch) {
		t.Fatalf("cross-domain resume error = %v, want %v", err, ErrPolicyDomainMismatch)
	}

	// The failed cross-domain attempt must not consume the legitimate ticket.
	guestReq := rig.resumeRequest(issued.Ticket, guestPolicy(), guestAP(1), resumedTLS("guest-resume", issued.Ticket))
	resumed, err := rig.auth.Resume(context.Background(), guestReq)
	if err != nil {
		t.Fatalf("legitimate guest resume failed: %v", err)
	}
	if resumed.Grant.VLAN != 10 {
		t.Fatalf("guest resume VLAN = %d, want 10", resumed.Grant.VLAN)
	}
	if resumed.Grant.Principal != "alice@corp.example" {
		t.Fatalf("principal = %q, want certificate subject", resumed.Grant.Principal)
	}
	if resumed.Grant.DomainID != "guest-domain" {
		t.Fatalf("grant domain = %q, want guest-domain", resumed.Grant.DomainID)
	}
}

func TestSameSSIDDifferentPolicyDomainDoesNotCross(t *testing.T) {
	rig := newTestRig(t)
	issued := rig.issue(t, guestPolicy(), guestAP(1), testTLS("initial"), 10)

	sharedSSID := guestPolicy()
	sharedSSID.DomainID = "corp-domain"
	sharedSSID.SSID = "Guest"
	sharedSSID.AllowedBSSIDs = []string{"aa:bb:cc:00:00:01"}
	sharedSSID.AllowedVLANs = []uint16{100}
	ap := AccessPoint{DomainID: "corp-domain", SSID: "Guest", BSSID: "aa:bb:cc:00:00:01"}

	req := rig.resumeRequest(issued.Ticket, sharedSSID, ap, resumedTLS("domain-collision", issued.Ticket))
	if _, err := rig.auth.Resume(context.Background(), req); !errors.Is(err, ErrPolicyDomainMismatch) {
		t.Fatalf("same-SSID cross-domain error = %v, want %v", err, ErrPolicyDomainMismatch)
	}
}

func TestSameCertificateIsAuthorizedPerPolicyDomain(t *testing.T) {
	rig := newTestRig(t)

	// One client certificate obtains independent authorizations on Guest and
	// Corp because each ticket freezes the SSID/policy domain and grant.
	guestTicket := rig.issue(t, guestPolicy(), guestAP(1), testTLS("guest-initial"), 10)
	corpTicket := rig.issue(t, corpPolicy(), corpAP(), testTLS("corp-initial"), 100)
	if guestTicket.Grant.VLAN != 10 || corpTicket.Grant.VLAN != 100 {
		t.Fatalf("initial grants = guest:%d corp:%d", guestTicket.Grant.VLAN, corpTicket.Grant.VLAN)
	}
	if guestTicket.Grant.Principal != corpTicket.Grant.Principal {
		t.Fatalf("certificate principals differ: %q vs %q", guestTicket.Grant.Principal, corpTicket.Grant.Principal)
	}

	guestResume, err := rig.auth.Resume(context.Background(),
		rig.resumeRequest(guestTicket.Ticket, guestPolicy(), guestAP(2), resumedTLS("guest-legit", guestTicket.Ticket)))
	if err != nil {
		t.Fatalf("guest resume failed: %v", err)
	}
	if guestResume.Grant.VLAN != 10 || guestResume.Grant.DomainID != "guest-domain" {
		t.Fatalf("guest resumed grant = %+v", guestResume.Grant)
	}

	corpResume, err := rig.auth.Resume(context.Background(),
		rig.resumeRequest(corpTicket.Ticket, corpPolicy(), corpAP(), resumedTLS("corp-legit", corpTicket.Ticket)))
	if err != nil {
		t.Fatalf("corp resume failed: %v", err)
	}
	if corpResume.Grant.VLAN != 100 || corpResume.Grant.DomainID != "corp-domain" {
		t.Fatalf("corp resumed grant = %+v", corpResume.Grant)
	}

	// The Guest ticket cannot be used to acquire the Corp grant.
	if _, err := rig.auth.Resume(context.Background(),
		rig.resumeRequest(guestTicket.Ticket, corpPolicy(), corpAP(), resumedTLS("guest-cross", guestTicket.Ticket))); !errors.Is(err, ErrPolicyDomainMismatch) {
		t.Fatalf("guest ticket on corp error = %v, want %v", err, ErrPolicyDomainMismatch)
	}
}

func TestBSSIDRoamingWithinPolicyDomain(t *testing.T) {
	rig := newTestRig(t)
	issued := rig.issue(t, guestPolicy(), guestAP(1), testTLS("initial"), 10)

	req := rig.resumeRequest(issued.Ticket, guestPolicy(), guestAP(2), resumedTLS("roam-to-ap2", issued.Ticket))
	req.Mode = ResumeRotate
	resumed, err := rig.auth.Resume(context.Background(), req)
	if err != nil {
		t.Fatalf("same-domain BSSID roam failed: %v", err)
	}
	if resumed.Ticket == nil {
		t.Fatal("rotation did not return a replacement ticket")
	}
	if resumed.Ticket.ID == issued.Ticket.ID {
		t.Fatal("replacement ticket reused the old ID")
	}
	if !resumed.Ticket.ExpiresAt.Equal(issued.Ticket.ExpiresAt) {
		t.Fatalf("rotation changed absolute expiry: %s -> %s", issued.Ticket.ExpiresAt, resumed.Ticket.ExpiresAt)
	}
	if resumed.Grant.VLAN != 10 {
		t.Fatalf("roamed grant VLAN = %d, want 10", resumed.Grant.VLAN)
	}

	// A BSSID outside the current policy's allow-list is rejected without
	// consuming the replacement ticket.
	outsideReq := rig.resumeRequest(*resumed.Ticket, guestPolicy(), guestAP(3), resumedTLS("out-of-policy-ap", *resumed.Ticket))
	if _, err := rig.auth.Resume(context.Background(), outsideReq); !errors.Is(err, ErrAccessPointNotAllowed) {
		t.Fatalf("out-of-policy AP error = %v, want %v", err, ErrAccessPointNotAllowed)
	}

	backReq := rig.resumeRequest(*resumed.Ticket, guestPolicy(), guestAP(1), resumedTLS("roam-back-ap1", *resumed.Ticket))
	if _, err := rig.auth.Resume(context.Background(), backReq); err != nil {
		t.Fatalf("roam back within domain failed: %v", err)
	}

	// The original ticket was atomically rotated and is now a replay.
	replayReq := rig.resumeRequest(issued.Ticket, guestPolicy(), guestAP(1), resumedTLS("old-ticket-replay", issued.Ticket))
	if _, err := rig.auth.Resume(context.Background(), replayReq); !errors.Is(err, ErrTicketReplayed) {
		t.Fatalf("old ticket error = %v, want %v", err, ErrTicketReplayed)
	}
}

func TestLegitimateFastRoamingOnSameTLSChannel(t *testing.T) {
	rig := newTestRig(t)
	initialTLS := testTLS("fast-roam-binding")
	issued := rig.issue(t, guestPolicy(), guestAP(1), initialTLS, 10)

	roamTLS := fastRoamTLS(initialTLS)
	req := rig.resumeRequest(issued.Ticket, guestPolicy(), guestAP(2), roamTLS)
	req.Mode = ResumeRotate
	resumed, err := rig.auth.Resume(context.Background(), req)
	if err != nil {
		t.Fatalf("fast roaming resume failed: %v", err)
	}
	if resumed.Grant.VLAN != 10 || resumed.Grant.DomainID != "guest-domain" {
		t.Fatalf("fast roaming grant = %+v", resumed.Grant)
	}
	if resumed.Ticket == nil {
		t.Fatal("fast roaming did not rotate the ticket")
	}

	// The new ticket remains usable on the same TLS channel.
	nextReq := rig.resumeRequest(*resumed.Ticket, guestPolicy(), guestAP(2), roamTLS)
	if _, err := rig.auth.Resume(context.Background(), nextReq); err != nil {
		t.Fatalf("second fast roaming resume failed: %v", err)
	}

	// A channel-binding change without a TLS resumption is rejected.
	otherTLS := testTLS("different-session-binding")
	fresh := rig.issue(t, guestPolicy(), guestAP(1), initialTLS, 10)
	badReq := rig.resumeRequest(fresh.Ticket, guestPolicy(), guestAP(2), otherTLS)
	if _, err := rig.auth.Resume(context.Background(), badReq); !errors.Is(err, ErrChannelBindingInvalid) {
		t.Fatalf("same-session binding change error = %v, want %v", err, ErrChannelBindingInvalid)
	}
	goodReq := rig.resumeRequest(fresh.Ticket, guestPolicy(), guestAP(2), roamTLS)
	if _, err := rig.auth.Resume(context.Background(), goodReq); err != nil {
		t.Fatalf("ticket was consumed by rejected binding change: %v", err)
	}
}

func TestRevocationInvalidatesOldAuthorizationAndEpoch(t *testing.T) {
	rig := newTestRig(t)
	issued := rig.issue(t, guestPolicy(), guestAP(1), testTLS("initial"), 10)

	rig.revocation.Revoke(rig.leaf, "key compromise")
	req := rig.resumeRequest(issued.Ticket, guestPolicy(), guestAP(1), resumedTLS("after-revoke", issued.Ticket))
	if _, err := rig.auth.Resume(context.Background(), req); !errors.Is(err, ErrCertificateRevoked) {
		t.Fatalf("revoked resume error = %v, want %v", err, ErrCertificateRevoked)
	}

	// Reinstatement must not revive a ticket minted before the revocation.
	rig.revocation.Reinstate(rig.leaf)
	if _, err := rig.auth.Resume(context.Background(), req); !errors.Is(err, ErrCertificateRevoked) {
		t.Fatalf("reinstate revived old ticket: %v", err)
	}

	// A fresh authentication after reinstatement is valid under the new epoch.
	fresh := rig.issue(t, guestPolicy(), guestAP(1), testTLS("post-reinstate"), 10)
	freshReq := rig.resumeRequest(fresh.Ticket, guestPolicy(), guestAP(1), resumedTLS("post-reinstate-resume", fresh.Ticket))
	if _, err := rig.auth.Resume(context.Background(), freshReq); err != nil {
		t.Fatalf("fresh ticket after reinstatement failed: %v", err)
	}

	// Issuance itself fails closed while revoked.
	rig.revocation.Revoke(rig.leaf, "second revocation")
	_, err := rig.auth.Issue(context.Background(), IssueRequest{
		OuterEAPIdentity:         "anonymous@example.org",
		InnerEAPIdentity:         "alice@corp.example",
		ServerVirtualIdentity:    "aaa.example.org",
		AccessPoint:              guestAP(1),
		Policy:                   guestPolicy(),
		ClientCertificateChain:   rig.chain,
		CertValidationGeneration: 7,
		TLS:                      testTLS("revoked-issue"),
		RequestedVLAN:            10,
		Lifetime:                 time.Minute,
	})
	if !errors.Is(err, ErrCertificateRevoked) {
		t.Fatalf("issuing while revoked error = %v, want %v", err, ErrCertificateRevoked)
	}
}

func TestEarlyDataHasNoAuthorizationSideEffects(t *testing.T) {
	rig := newTestRig(t)
	issued := rig.issue(t, guestPolicy(), guestAP(1), testTLS("initial"), 10)
	base := rig.resumeRequest(issued.Ticket, guestPolicy(), guestAP(1), resumedTLS("early-data", issued.Ticket))

	earlyOpen := base
	earlyOpen.TLS.EarlyData = true
	earlyOpen.TLS.Finished = false
	if _, err := rig.auth.Resume(context.Background(), earlyOpen); !errors.Is(err, ErrEarlyDataNotAuthorized) {
		t.Fatalf("unfinished early-data error = %v, want %v", err, ErrEarlyDataNotAuthorized)
	}

	earlyFinished := base
	earlyFinished.TLS.EarlyData = true
	earlyFinished.TLS.Finished = true
	if _, err := rig.auth.Resume(context.Background(), earlyFinished); !errors.Is(err, ErrEarlyDataNotAuthorized) {
		t.Fatalf("finished early-data error = %v, want %v", err, ErrEarlyDataNotAuthorized)
	}

	incomplete := base
	incomplete.TLS.Finished = false
	if _, err := rig.auth.Resume(context.Background(), incomplete); !errors.Is(err, ErrHandshakeIncomplete) {
		t.Fatalf("incomplete handshake error = %v, want %v", err, ErrHandshakeIncomplete)
	}

	earlyIssueTLS := testTLS("early-issue")
	earlyIssueTLS.EarlyData = true
	_, err := rig.auth.Issue(context.Background(), IssueRequest{
		OuterEAPIdentity:         "anonymous@example.org",
		InnerEAPIdentity:         "alice@corp.example",
		ServerVirtualIdentity:    "aaa.example.org",
		AccessPoint:              guestAP(1),
		Policy:                   guestPolicy(),
		ClientCertificateChain:   rig.chain,
		CertValidationGeneration: 7,
		TLS:                      earlyIssueTLS,
		RequestedVLAN:            10,
		Lifetime:                 time.Minute,
	})
	if !errors.Is(err, ErrEarlyDataNotAuthorized) {
		t.Fatalf("issue with early data error = %v, want %v", err, ErrEarlyDataNotAuthorized)
	}

	// None of the rejected attempts may have consumed the ticket.
	if _, err := rig.auth.Resume(context.Background(), base); err != nil {
		t.Fatalf("ticket was consumed by rejected early data: %v", err)
	}
}

func TestConcurrentTicketUseHasSingleWinner(t *testing.T) {
	rig := newTestRig(t)
	issued := rig.issue(t, guestPolicy(), guestAP(1), testTLS("initial"), 10)
	base := rig.resumeRequest(issued.Ticket, guestPolicy(), guestAP(1), resumedTLS("concurrent", issued.Ticket))

	const goroutines = 64
	var wins atomic.Int64
	var replays atomic.Int64
	var failures atomic.Int64
	var wg sync.WaitGroup
	wg.Add(goroutines)
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			<-start
			resumed, err := rig.auth.Resume(context.Background(), base)
			switch {
			case err == nil:
				if resumed == nil || resumed.Grant.VLAN != 10 {
					failures.Add(1)
					return
				}
				wins.Add(1)
			case errors.Is(err, ErrTicketReplayed):
				replays.Add(1)
			default:
				failures.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if wins.Load() != 1 || replays.Load() != goroutines-1 || failures.Load() != 0 {
		t.Fatalf("wins=%d replays=%d failures=%d, want 1/%d/0", wins.Load(), replays.Load(), failures.Load(), goroutines-1)
	}
}

func TestAnonymousOuterIdentityCannotReplaceCertificateSubject(t *testing.T) {
	rig := newTestRig(t)

	// An anonymous inner identity is never a principal, even with a valid cert.
	_, err := rig.auth.Issue(context.Background(), IssueRequest{
		OuterEAPIdentity:         "anonymous@example.org",
		InnerEAPIdentity:         "anonymous@example.org",
		ServerVirtualIdentity:    "aaa.example.org",
		AccessPoint:              guestAP(1),
		Policy:                   guestPolicy(),
		ClientCertificateChain:   rig.chain,
		CertValidationGeneration: 7,
		TLS:                      testTLS("anonymous-inner"),
		RequestedVLAN:            10,
		Lifetime:                 time.Minute,
	})
	if !errors.Is(err, ErrAnonymousIdentity) {
		t.Fatalf("anonymous inner identity error = %v, want %v", err, ErrAnonymousIdentity)
	}

	issued := rig.issue(t, guestPolicy(), guestAP(1), testTLS("initial"), 10)
	if issued.Grant.Principal != "alice@corp.example" {
		t.Fatalf("grant principal = %q, want certificate subject", issued.Grant.Principal)
	}

	changedOuter := rig.resumeRequest(issued.Ticket, guestPolicy(), guestAP(1), resumedTLS("changed-outer", issued.Ticket))
	changedOuter.OuterEAPIdentity = "alice@corp.example"
	if _, err := rig.auth.Resume(context.Background(), changedOuter); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("changed outer identity error = %v, want %v", err, ErrIdentityMismatch)
	}

	anonymousInner := rig.resumeRequest(issued.Ticket, guestPolicy(), guestAP(1), resumedTLS("anonymous-resume", issued.Ticket))
	anonymousInner.InnerEAPIdentity = "anonymous@example.org"
	if _, err := rig.auth.Resume(context.Background(), anonymousInner); !errors.Is(err, ErrAnonymousIdentity) {
		t.Fatalf("anonymous resume inner identity error = %v, want %v", err, ErrAnonymousIdentity)
	}

	// An omitted inner identity is derived from the verified certificate; the
	// anonymous outer identity is still only an outer label.
	emptyInner := rig.resumeRequest(issued.Ticket, guestPolicy(), guestAP(1), resumedTLS("derived-inner", issued.Ticket))
	emptyInner.InnerEAPIdentity = ""
	resumed, err := rig.auth.Resume(context.Background(), emptyInner)
	if err != nil {
		t.Fatalf("resume with certificate-derived inner identity failed: %v", err)
	}
	if resumed.Grant.Principal != "alice@corp.example" {
		t.Fatalf("derived principal = %q", resumed.Grant.Principal)
	}
}

func TestPolicyChangesInvalidateResumption(t *testing.T) {
	rig := newTestRig(t)
	issued := rig.issue(t, guestPolicy(), guestAP(1), testTLS("initial"), 10)

	tightenedRevision := guestPolicy()
	tightenedRevision.Revision = 2
	tightenedRevision.AllowedVLANs = []uint16{10}
	req := rig.resumeRequest(issued.Ticket, tightenedRevision, guestAP(1), resumedTLS("tightened-revision", issued.Ticket))
	if _, err := rig.auth.Resume(context.Background(), req); !errors.Is(err, ErrPolicyTightened) {
		t.Fatalf("revision tightening error = %v, want %v", err, ErrPolicyTightened)
	}

	tightenedBSSIDs := guestPolicy()
	tightenedBSSIDs.AllowedBSSIDs = []string{"aa:bb:cc:00:00:01"}
	req = rig.resumeRequest(issued.Ticket, tightenedBSSIDs, guestAP(1), resumedTLS("tightened-bssids", issued.Ticket))
	if _, err := rig.auth.Resume(context.Background(), req); !errors.Is(err, ErrPolicyTightened) {
		t.Fatalf("BSSID tightening error = %v, want %v", err, ErrPolicyTightened)
	}

	tightenedGrant := guestPolicy()
	tightenedGrant.Revision = 2
	tightenedGrant.AllowedVLANs = []uint16{20}
	req = rig.resumeRequest(issued.Ticket, tightenedGrant, guestAP(1), resumedTLS("tightened-grant", issued.Ticket))
	if _, err := rig.auth.Resume(context.Background(), req); !errors.Is(err, ErrPolicyTightened) {
		t.Fatalf("grant tightening error = %v, want %v", err, ErrPolicyTightened)
	}

	loosened := guestPolicy()
	loosened.AllowedVLANs = []uint16{10, 20, 30}
	req = rig.resumeRequest(issued.Ticket, loosened, guestAP(1), resumedTLS("loosened", issued.Ticket))
	if _, err := rig.auth.Resume(context.Background(), req); !errors.Is(err, ErrPolicyChanged) {
		t.Fatalf("loosened policy error = %v, want %v", err, ErrPolicyChanged)
	}

	// All rejected attempts left the ticket intact.
	req = rig.resumeRequest(issued.Ticket, guestPolicy(), guestAP(1), resumedTLS("unchanged-policy", issued.Ticket))
	if _, err := rig.auth.Resume(context.Background(), req); err != nil {
		t.Fatalf("unchanged policy resume failed: %v", err)
	}
}

func TestTicketKeyRotationInvalidatesOutstandingTickets(t *testing.T) {
	rig := newTestRig(t)
	issued := rig.issue(t, guestPolicy(), guestAP(1), testTLS("initial"), 10)

	newKey := sha256.Sum256([]byte("rotated ticket key"))
	if _, err := rig.keyring.Rotate(newKey[:]); err != nil {
		t.Fatalf("rotate ticket key: %v", err)
	}

	req := rig.resumeRequest(issued.Ticket, guestPolicy(), guestAP(1), resumedTLS("after-rotation", issued.Ticket))
	if _, err := rig.auth.Resume(context.Background(), req); !errors.Is(err, ErrTicketKeyRotated) {
		t.Fatalf("rotated-key resume error = %v, want %v", err, ErrTicketKeyRotated)
	}

	fresh := rig.issue(t, guestPolicy(), guestAP(1), testTLS("new-generation"), 10)
	freshReq := rig.resumeRequest(fresh.Ticket, guestPolicy(), guestAP(1), resumedTLS("new-generation-resume", fresh.Ticket))
	if _, err := rig.auth.Resume(context.Background(), freshReq); err != nil {
		t.Fatalf("ticket issued under new key generation failed: %v", err)
	}
}

func TestTLSAndChannelBindingMismatches(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*TLSState, Ticket)
		wantErr error
	}{
		{
			name:    "tls version",
			mutate:  func(tls *TLSState, _ Ticket) { tls.Version = TLSVersion12 },
			wantErr: ErrTLSBindingMismatch,
		},
		{
			name:    "cipher suite",
			mutate:  func(tls *TLSState, _ Ticket) { tls.CipherSuite = TLSAES256GCMSHA384 },
			wantErr: ErrTLSBindingMismatch,
		},
		{
			name: "msk identity",
			mutate: func(tls *TLSState, _ Ticket) {
				other := sha256.Sum256([]byte("different msk identity"))
				tls.MSKIdentity = other[:]
			},
			wantErr: ErrMSKIdentityMismatch,
		},
		{
			name: "unverified channel binding",
			mutate: func(tls *TLSState, _ Ticket) {
				tls.ChannelBindingVerified = false
			},
			wantErr: ErrChannelBindingInvalid,
		},
		{
			name: "channel binding type",
			mutate: func(tls *TLSState, _ Ticket) {
				tls.ChannelBindingType = "other-binding"
			},
			wantErr: ErrChannelBindingInvalid,
		},
		{
			name: "missing psk identity",
			mutate: func(tls *TLSState, _ Ticket) {
				tls.PSKIdentity = nil
			},
			wantErr: ErrChannelBindingInvalid,
		},
		{
			name: "wrong psk identity",
			mutate: func(tls *TLSState, _ Ticket) {
				tls.PSKIdentity = []byte("wrong ticket")
			},
			wantErr: ErrChannelBindingInvalid,
		},
		{
			name: "same-session binding change",
			mutate: func(tls *TLSState, _ Ticket) {
				other := sha256.Sum256([]byte("different same-session binding"))
				tls.Resumed = false
				tls.PSKIdentity = nil
				tls.ChannelBinding = other[:]
			},
			wantErr: ErrChannelBindingInvalid,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := newTestRig(t)
			issued := rig.issue(t, guestPolicy(), guestAP(1), testTLS("initial"), 10)
			tls := resumedTLS("resume-"+tc.name, issued.Ticket)
			tc.mutate(&tls, issued.Ticket)

			req := rig.resumeRequest(issued.Ticket, guestPolicy(), guestAP(2), tls)
			if _, err := rig.auth.Resume(context.Background(), req); !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}

			good := rig.resumeRequest(issued.Ticket, guestPolicy(), guestAP(2), resumedTLS("good-"+tc.name, issued.Ticket))
			if _, err := rig.auth.Resume(context.Background(), good); err != nil {
				t.Fatalf("rejected TLS evidence consumed ticket: %v", err)
			}
		})
	}
}

func TestTicketExpiryAndRotationDoNotExtendAuthorization(t *testing.T) {
	rig := newTestRig(t)
	issued := rig.issueWithLifetime(t, guestPolicy(), guestAP(1), testTLS("initial"), 10, 10*time.Minute)
	if !issued.Ticket.ExpiresAt.Equal(testNow.Add(10 * time.Minute)) {
		t.Fatalf("ticket expiry = %s, want %s", issued.Ticket.ExpiresAt, testNow.Add(10*time.Minute))
	}

	rig.clock.Advance(5 * time.Minute)
	req := rig.resumeRequest(issued.Ticket, guestPolicy(), guestAP(1), resumedTLS("midlife", issued.Ticket))
	req.Mode = ResumeRotate
	resumed, err := rig.auth.Resume(context.Background(), req)
	if err != nil {
		t.Fatalf("midlife rotation failed: %v", err)
	}
	if !resumed.Ticket.ExpiresAt.Equal(issued.Ticket.ExpiresAt) {
		t.Fatalf("rotation extended expiry: %s -> %s", issued.Ticket.ExpiresAt, resumed.Ticket.ExpiresAt)
	}

	rig.clock.Advance(5 * time.Minute)
	expiredReq := rig.resumeRequest(*resumed.Ticket, guestPolicy(), guestAP(1), resumedTLS("expired", *resumed.Ticket))
	if _, err := rig.auth.Resume(context.Background(), expiredReq); !errors.Is(err, ErrTicketExpired) {
		t.Fatalf("expired ticket error = %v, want %v", err, ErrTicketExpired)
	}

	overlong := rig.issueRequest(guestPolicy(), guestAP(1), testTLS("overlong"), 10, 2*time.Hour)
	if _, err := rig.auth.Issue(context.Background(), overlong); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("overlong ticket error = %v, want %v", err, ErrInvalidArgument)
	}
}

func TestCertificateChainValidationGenerationAndTampering(t *testing.T) {
	rig := newTestRig(t)
	issued := rig.issue(t, guestPolicy(), guestAP(1), testTLS("initial"), 10)

	otherChain, _ := rig.ca.issueClient(t, "alice@corp.example")
	req := rig.resumeRequest(issued.Ticket, guestPolicy(), guestAP(1), resumedTLS("other-cert", issued.Ticket))
	req.ClientCertificateChain = otherChain
	if _, err := rig.auth.Resume(context.Background(), req); !errors.Is(err, ErrCertificateMismatch) {
		t.Fatalf("different certificate error = %v, want %v", err, ErrCertificateMismatch)
	}

	req = rig.resumeRequest(issued.Ticket, guestPolicy(), guestAP(1), resumedTLS("other-generation", issued.Ticket))
	req.CertValidationGeneration = 8
	if _, err := rig.auth.Resume(context.Background(), req); !errors.Is(err, ErrCertificatePolicyChanged) {
		t.Fatalf("validation generation error = %v, want %v", err, ErrCertificatePolicyChanged)
	}

	req = rig.resumeRequest(issued.Ticket, guestPolicy(), guestAP(1), resumedTLS("short-chain", issued.Ticket))
	req.ClientCertificateChain = [][]byte{rig.chain[0]}
	if _, err := rig.auth.Resume(context.Background(), req); !errors.Is(err, ErrCertificateMismatch) {
		t.Fatalf("shortened chain error = %v, want %v", err, ErrCertificateMismatch)
	}

	tampered := issued.Ticket.Bytes()
	tampered[len(tampered)-1] ^= 0x80
	req = rig.resumeRequest(issued.Ticket, guestPolicy(), guestAP(1), resumedTLS("tampered", issued.Ticket))
	req.Ticket = tampered
	if _, err := rig.auth.Resume(context.Background(), req); !errors.Is(err, ErrTicketInvalid) {
		t.Fatalf("tampered ticket error = %v, want %v", err, ErrTicketInvalid)
	}

	otherCA := newTestCA(t)
	untrustedChain, _ := otherCA.issueClient(t, "alice@corp.example")
	req = rig.resumeRequest(issued.Ticket, guestPolicy(), guestAP(1), resumedTLS("untrusted", issued.Ticket))
	req.ClientCertificateChain = untrustedChain
	if _, err := rig.auth.Resume(context.Background(), req); !errors.Is(err, ErrCertificateInvalid) {
		t.Fatalf("untrusted chain error = %v, want %v", err, ErrCertificateInvalid)
	}

	// All rejected attempts left the original ticket intact.
	req = rig.resumeRequest(issued.Ticket, guestPolicy(), guestAP(1), resumedTLS("valid-after-failures", issued.Ticket))
	if _, err := rig.auth.Resume(context.Background(), req); err != nil {
		t.Fatalf("ticket consumed by failed certificate checks: %v", err)
	}
}

func TestServerAndOuterIdentityAreFrozen(t *testing.T) {
	rig := newTestRig(t)
	issued := rig.issue(t, guestPolicy(), guestAP(1), testTLS("initial"), 10)

	req := rig.resumeRequest(issued.Ticket, guestPolicy(), guestAP(1), resumedTLS("server-mismatch", issued.Ticket))
	req.ServerVirtualIdentity = "aaa.other.example"
	if _, err := rig.auth.Resume(context.Background(), req); !errors.Is(err, ErrServerIdentityMismatch) {
		t.Fatalf("server mismatch error = %v, want %v", err, ErrServerIdentityMismatch)
	}

	req = rig.resumeRequest(issued.Ticket, guestPolicy(), guestAP(1), resumedTLS("outer-mismatch", issued.Ticket))
	req.OuterEAPIdentity = "alice@corp.example"
	if _, err := rig.auth.Resume(context.Background(), req); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("outer identity mismatch error = %v, want %v", err, ErrIdentityMismatch)
	}

	req = rig.resumeRequest(issued.Ticket, guestPolicy(), guestAP(1), resumedTLS("valid-after-identity", issued.Ticket))
	if _, err := rig.auth.Resume(context.Background(), req); err != nil {
		t.Fatalf("ticket consumed by identity checks: %v", err)
	}
}

func TestGrantIsFrozenAndCannotBeElevated(t *testing.T) {
	rig := newTestRig(t)
	issued := rig.issue(t, guestPolicy(), guestAP(1), testTLS("initial"), 10)
	if len(issued.Grant.ACL) != 2 {
		t.Fatalf("ACL length = %d, want 2", len(issued.Grant.ACL))
	}
	if issued.Grant.ACL[0].Protocol != "tcp" || issued.Grant.ACL[0].Ports != "443" {
		t.Fatalf("ACL was not canonicalized: %+v", issued.Grant.ACL)
	}

	req := rig.resumeRequest(issued.Ticket, guestPolicy(), guestAP(2), resumedTLS("rotate-grant", issued.Ticket))
	req.Mode = ResumeRotate
	resumed, err := rig.auth.Resume(context.Background(), req)
	if err != nil {
		t.Fatalf("resume with frozen grant failed: %v", err)
	}
	if resumed.Grant.VLAN != 10 || len(resumed.Grant.ACL) != 2 {
		t.Fatalf("resumed grant = %+v", resumed.Grant)
	}

	// Mutating the caller's copy must not change the authorization stored in
	// the replacement ticket.
	resumed.Grant.VLAN = 200
	resumed.Grant.ACL[0].Ports = "1"
	again, err := rig.auth.Resume(context.Background(), rig.resumeRequest(*resumed.Ticket, guestPolicy(), guestAP(2), resumedTLS("grant-again", *resumed.Ticket)))
	if err != nil {
		t.Fatalf("second resume failed: %v", err)
	}
	if again.Grant.VLAN != 10 || again.Grant.ACL[0].Ports != "443" {
		t.Fatalf("ticket grant was mutated by caller: %+v", again.Grant)
	}
}

func TestConcurrentRotationsNeverDuplicateWinners(t *testing.T) {
	rig := newTestRig(t)
	issued := rig.issue(t, guestPolicy(), guestAP(1), testTLS("initial"), 10)
	base := rig.resumeRequest(issued.Ticket, guestPolicy(), guestAP(2), resumedTLS("rotate-race", issued.Ticket))
	base.Mode = ResumeRotate

	const goroutines = 32
	var wins atomic.Int64
	var replays atomic.Int64
	var tickets sync.Map
	var wg sync.WaitGroup
	wg.Add(goroutines)
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			<-start
			resumed, err := rig.auth.Resume(context.Background(), base)
			switch {
			case err == nil:
				wins.Add(1)
				tickets.Store(resumed.Ticket.ID.String(), struct{}{})
			case errors.Is(err, ErrTicketReplayed):
				replays.Add(1)
			default:
				t.Errorf("unexpected resume error: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if wins.Load() != 1 || replays.Load() != goroutines-1 {
		t.Fatalf("wins=%d replays=%d, want 1/%d", wins.Load(), replays.Load(), goroutines-1)
	}
	var ticketCount int
	tickets.Range(func(_, _ any) bool {
		ticketCount++
		return true
	})
	if ticketCount != 1 {
		t.Fatalf("issued %d replacement tickets, want 1", ticketCount)
	}
}

func TestIssueRejectsInvalidConfiguration(t *testing.T) {
	rig := newTestRig(t)
	base := rig.issueRequest(guestPolicy(), guestAP(1), testTLS("initial"), 10, time.Minute)

	cases := []struct {
		name   string
		mutate func(*IssueRequest)
	}{
		{
			name: "empty chain",
			mutate: func(req *IssueRequest) {
				req.ClientCertificateChain = nil
			},
		},
		{
			name: "bad VLAN",
			mutate: func(req *IssueRequest) {
				req.RequestedVLAN = 100
			},
		},
		{
			name: "bad AP domain",
			mutate: func(req *IssueRequest) {
				req.AccessPoint = corpAP()
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := base
			tc.mutate(&req)
			if _, err := rig.auth.Issue(context.Background(), req); err == nil {
				t.Fatal("expected issue to fail")
			}
		})
	}
}

func TestAuthenticatorRequiresFailClosedConfiguration(t *testing.T) {
	key := sha256.Sum256([]byte("configuration key"))
	keyring, err := NewKeyring(key[:])
	if err != nil {
		t.Fatalf("new keyring: %v", err)
	}
	roots := newTestCA(t).roots

	cases := []struct {
		name string
		cfg  AuthenticatorConfig
	}{
		{name: "missing keyring", cfg: AuthenticatorConfig{ReplayStore: NewMemoryReplayStore(), RevocationChecker: NewMemoryRevocationChecker(), TrustRoots: roots}},
		{name: "missing replay", cfg: AuthenticatorConfig{Keyring: keyring, RevocationChecker: NewMemoryRevocationChecker(), TrustRoots: roots}},
		{name: "missing revocation", cfg: AuthenticatorConfig{Keyring: keyring, ReplayStore: NewMemoryReplayStore(), TrustRoots: roots}},
		{name: "missing roots", cfg: AuthenticatorConfig{Keyring: keyring, ReplayStore: NewMemoryReplayStore(), RevocationChecker: NewMemoryRevocationChecker()}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewAuthenticator(tc.cfg); err == nil {
				t.Fatal("expected constructor to fail closed")
			}
		})
	}
}

func (r *testRig) issueWithLifetime(t *testing.T, policy Policy, ap AccessPoint, tls TLSState, vlan uint16, lifetime time.Duration) *Issued {
	t.Helper()
	req := r.issueRequest(policy, ap, tls, vlan, lifetime)
	issued, err := r.auth.Issue(context.Background(), req)
	if err != nil {
		t.Fatalf("issue ticket: %v", err)
	}
	return issued
}

func (r *testRig) issueRequest(policy Policy, ap AccessPoint, tls TLSState, vlan uint16, lifetime time.Duration) IssueRequest {
	return IssueRequest{
		OuterEAPIdentity:         "anonymous@example.org",
		InnerEAPIdentity:         "alice@corp.example",
		ServerVirtualIdentity:    "aaa.example.org",
		AccessPoint:              ap,
		Policy:                   policy,
		ClientCertificateChain:   r.chain,
		CertValidationGeneration: 7,
		TLS:                      tls,
		RequestedVLAN:            vlan,
		ACL: []ACLRule{
			{Action: "allow", Protocol: "udp", Destination: "10.10.0.0/16", Ports: "53"},
			{Action: "allow", Protocol: "tcp", Destination: "10.10.0.0/16", Ports: "443"},
		},
		Lifetime: lifetime,
	}
}

func TestTicketBytesAreCopied(t *testing.T) {
	rig := newTestRig(t)
	issued := rig.issue(t, guestPolicy(), guestAP(1), testTLS("initial"), 10)
	raw := issued.Ticket.Bytes()
	raw[0] ^= 0xff
	if bytes.Equal(raw, issued.Ticket.Raw) {
		t.Fatal("Ticket.Bytes did not return a copy")
	}
}
