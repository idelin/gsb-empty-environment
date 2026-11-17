package netsec_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	netsec "example.org/netsec/q0155"
)

func mustAttach(t *testing.T, authenticator *netsec.Authenticator, name string) netsec.Port {
	t.Helper()
	port, err := authenticator.AttachPort(name)
	if err != nil {
		t.Fatalf("AttachPort(%q): %v", name, err)
	}
	return port
}

func establish(
	t *testing.T,
	authenticator *netsec.Authenticator,
	port netsec.Port,
	outerIdentity string,
	exporter string,
	exchange []string,
	innerIdentity string,
) (netsec.TunnelID, netsec.Grant) {
	t.Helper()

	tunnel, err := authenticator.BeginTunnel(port, outerIdentity)
	if err != nil {
		t.Fatalf("BeginTunnel: %v", err)
	}
	if err := authenticator.BindExporter(tunnel, []byte(exporter)); err != nil {
		t.Fatalf("BindExporter: %v", err)
	}
	for i, packet := range exchange {
		if err := authenticator.RecordInnerExchange(tunnel, []byte(packet)); err != nil {
			t.Fatalf("RecordInnerExchange(%d): %v", i, err)
		}
	}
	grant, err := authenticator.CompleteInner(tunnel, innerIdentity, true)
	if err != nil {
		t.Fatalf("CompleteInner: %v", err)
	}
	return tunnel, grant
}

func TestOuterIdentityOnlyRoutesAndNeverBecomesPrincipal(t *testing.T) {
	authenticator := netsec.NewAuthenticator()
	port := mustAttach(t, authenticator, "eth0")
	const outer = "anonymous@realm.example"

	tunnel, err := authenticator.BeginTunnel(port, outer)
	if err != nil {
		t.Fatalf("BeginTunnel: %v", err)
	}
	route, err := authenticator.Route(tunnel)
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if route != outer {
		t.Fatalf("Route = %q, want %q", route, outer)
	}
	if err := authenticator.BindExporter(tunnel, []byte("tunnel-exporter-value")); err != nil {
		t.Fatalf("BindExporter: %v", err)
	}
	if err := authenticator.RecordInnerExchange(tunnel, []byte("inner-identity-request")); err != nil {
		t.Fatalf("RecordInnerExchange: %v", err)
	}

	if _, err := authenticator.CompleteInner(tunnel, "   ", true); !errors.Is(err, netsec.ErrInnerIdentityRequired) {
		t.Fatalf("empty inner identity error = %v, want ErrInnerIdentityRequired", err)
	}
	if state, err := authenticator.State(tunnel); err != nil || state != netsec.TunnelStateTunnelEstablished {
		t.Fatalf("state after empty inner identity = %v, %v", state, err)
	}

	const inner = "alice@corp.example"
	grant, err := authenticator.CompleteInner(tunnel, inner, true)
	if err != nil {
		t.Fatalf("CompleteInner: %v", err)
	}
	if grant.InnerIdentity() != inner {
		t.Fatalf("grant inner identity = %q, want %q", grant.InnerIdentity(), inner)
	}

	principal, err := authenticator.Authorize(grant, port, tunnel)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if principal.Name != inner {
		t.Fatalf("principal name = %q, want protected inner identity %q", principal.Name, inner)
	}
	if principal.Name == outer {
		t.Fatal("outer identity became the authorization principal")
	}
	if principal.Tunnel != tunnel || principal.Port != port {
		t.Fatal("principal is not bound to the authenticating tunnel and port instance")
	}

	if route, err = authenticator.Route(tunnel); err != nil || route != outer {
		t.Fatalf("route after authorization = %q, %v", route, err)
	}
}

func TestParallelTunnelsWithSameOuterIdentityCannotBeSpliced(t *testing.T) {
	authenticator := netsec.NewAuthenticator()
	port := mustAttach(t, authenticator, "eth0")
	const outer = "anonymous@realm.example"

	tunnelA, grantA := establish(
		t, authenticator, port, outer, "exporter-A",
		[]string{"inner-request", "alice-response", "inner-success-a"}, "alice@corp.example",
	)
	tunnelB, grantB := establish(
		t, authenticator, port, outer, "exporter-B",
		[]string{"inner-request", "bob-response", "inner-success-b"}, "bob@corp.example",
	)

	if tunnelA == tunnelB {
		t.Fatal("parallel tunnels received the same tunnel ID")
	}
	if grantA.BindingDigest() == grantB.BindingDigest() {
		t.Fatal("parallel tunnels produced the same binding digest")
	}
	for _, tunnel := range []netsec.TunnelID{tunnelA, tunnelB} {
		route, err := authenticator.Route(tunnel)
		if err != nil || route != outer {
			t.Fatalf("Route(%s) = %q, %v", tunnel, route, err)
		}
	}

	if _, err := authenticator.Authorize(grantA, port, tunnelA); err != nil {
		t.Fatalf("grant A on tunnel A: %v", err)
	}
	if _, err := authenticator.Authorize(grantB, port, tunnelB); err != nil {
		t.Fatalf("grant B on tunnel B: %v", err)
	}

	if _, err := authenticator.Authorize(grantA, port, tunnelB); !errors.Is(err, netsec.ErrBindingMismatch) ||
		!errors.Is(err, netsec.ErrSessionMismatch) {
		t.Fatalf("splicing grant A into tunnel B error = %v, want binding and session mismatch", err)
	}
	if _, err := authenticator.Authorize(grantB, port, tunnelA); !errors.Is(err, netsec.ErrBindingMismatch) ||
		!errors.Is(err, netsec.ErrSessionMismatch) {
		t.Fatalf("splicing grant B into tunnel A error = %v, want binding and session mismatch", err)
	}

	tunnelC, err := authenticator.BeginTunnel(port, outer)
	if err != nil {
		t.Fatalf("BeginTunnel C: %v", err)
	}
	if _, err := authenticator.Authorize(grantA, port, tunnelC); !errors.Is(err, netsec.ErrBindingMismatch) {
		t.Fatalf("unrelated parallel tunnel accepted grant A: %v", err)
	}
}

func TestPortReconnectWithSameNameAndOuterIdentityRejectsOldSuccess(t *testing.T) {
	authenticator := netsec.NewAuthenticator()
	first := mustAttach(t, authenticator, "eth0")
	const outer = "anonymous@realm.example"

	oldTunnel, oldGrant := establish(
		t, authenticator, first, outer, "old-exporter",
		[]string{"old-request", "old-response", "old-success"}, "alice@corp.example",
	)
	if _, err := authenticator.Authorize(oldGrant, first, oldTunnel); err != nil {
		t.Fatalf("old grant on original attachment: %v", err)
	}

	reconnected := mustAttach(t, authenticator, "eth0")
	if reconnected.Name() != first.Name() {
		t.Fatalf("reconnect name = %q, want %q", reconnected.Name(), first.Name())
	}
	if reconnected.Instance() == first.Instance() {
		t.Fatal("reconnect reused the previous physical port instance")
	}
	if reconnected == first {
		t.Fatal("reconnect produced the same port identity")
	}

	if _, err := authenticator.Authorize(oldGrant, first, oldTunnel); !errors.Is(err, netsec.ErrStalePort) {
		t.Fatalf("old grant on old attachment error = %v, want ErrStalePort", err)
	}
	if _, err := authenticator.Authorize(oldGrant, reconnected, oldTunnel); !errors.Is(err, netsec.ErrGrantInvalid) {
		t.Fatalf("old grant on reconnected attachment error = %v, want ErrGrantInvalid", err)
	}
	if _, err := authenticator.BeginTunnel(first, outer); !errors.Is(err, netsec.ErrStalePort) {
		t.Fatalf("BeginTunnel on old attachment error = %v, want ErrStalePort", err)
	}

	newTunnel, newGrant := establish(
		t, authenticator, reconnected, outer, "new-exporter",
		[]string{"new-request", "new-response", "new-success"}, "alice@corp.example",
	)
	if _, err := authenticator.Authorize(oldGrant, reconnected, newTunnel); !errors.Is(err, netsec.ErrGrantInvalid) {
		t.Fatalf("old grant on new tunnel error = %v, want ErrGrantInvalid", err)
	}
	principal, err := authenticator.Authorize(newGrant, reconnected, newTunnel)
	if err != nil {
		t.Fatalf("new grant on reconnected attachment: %v", err)
	}
	if principal.Name != "alice@corp.example" {
		t.Fatalf("new principal = %q", principal.Name)
	}
	if _, err := authenticator.Authorize(newGrant, first, newTunnel); !errors.Is(err, netsec.ErrStalePort) {
		t.Fatalf("new grant on stale attachment error = %v, want ErrStalePort", err)
	}
}

func TestGrantBindsExporterExchangeTunnelAndPortInstance(t *testing.T) {
	authenticator := netsec.NewAuthenticator()
	port := mustAttach(t, authenticator, "eth0")
	const outer = "anonymous@realm.example"
	const inner = "alice@corp.example"
	exchange := []string{"inner-request", "alice-response", "inner-success"}

	baseTunnel, baseGrant := establish(t, authenticator, port, outer, "same-exporter", exchange, inner)

	exporterTunnel, exporterGrant := establish(t, authenticator, port, outer, "other-exporter", exchange, inner)
	if exporterGrant.ExporterDigest() == baseGrant.ExporterDigest() {
		t.Fatal("different exporters produced the same exporter digest")
	}
	if exporterGrant.BindingDigest() == baseGrant.BindingDigest() {
		t.Fatal("different exporters produced the same binding digest")
	}
	if _, err := authenticator.Authorize(exporterGrant, port, exporterTunnel); err != nil {
		t.Fatalf("exporter grant on its own tunnel: %v", err)
	}
	if _, err := authenticator.Authorize(exporterGrant, port, baseTunnel); !errors.Is(err, netsec.ErrBindingMismatch) {
		t.Fatalf("exporter-swapped grant accepted: %v", err)
	}

	otherExchange := []string{"inner-request", "alice-response", "different-success"}
	transcriptTunnel, transcriptGrant := establish(t, authenticator, port, outer, "same-exporter", otherExchange, inner)
	if transcriptGrant.TranscriptDigest() == baseGrant.TranscriptDigest() {
		t.Fatal("different exchanges produced the same transcript digest")
	}
	if transcriptGrant.BindingDigest() == baseGrant.BindingDigest() {
		t.Fatal("different exchanges produced the same binding digest")
	}
	if _, err := authenticator.Authorize(transcriptGrant, port, baseTunnel); !errors.Is(err, netsec.ErrBindingMismatch) {
		t.Fatalf("transcript-swapped grant accepted: %v", err)
	}
	if transcriptTunnel == baseTunnel {
		t.Fatal("tunnels reused an ID")
	}

	parallelTunnel, parallelGrant := establish(t, authenticator, port, outer, "same-exporter", exchange, inner)
	if parallelGrant.TranscriptDigest() != baseGrant.TranscriptDigest() {
		t.Fatal("identical exchanges should have identical transcript digests")
	}
	if parallelGrant.BindingDigest() == baseGrant.BindingDigest() {
		t.Fatal("different tunnel instances produced the same binding digest")
	}
	if _, err := authenticator.Authorize(parallelGrant, port, baseTunnel); !errors.Is(err, netsec.ErrBindingMismatch) {
		t.Fatalf("parallel-tunnel grant accepted by base tunnel: %v", err)
	}
	if _, err := authenticator.Authorize(parallelGrant, port, parallelTunnel); err != nil {
		t.Fatalf("parallel grant on its own tunnel: %v", err)
	}

	var packets [][]byte
	for _, packet := range exchange {
		packets = append(packets, []byte(packet))
	}
	if digest := netsec.ExchangeDigest(packets...); digest != baseGrant.TranscriptDigest() {
		t.Fatal("ExchangeDigest does not match the grant transcript digest")
	}

	reconnected := mustAttach(t, authenticator, "eth0")
	reconnectedTunnel, reconnectedGrant := establish(
		t, authenticator, reconnected, outer, "same-exporter", exchange, inner,
	)
	if reconnectedGrant.BindingDigest() == baseGrant.BindingDigest() {
		t.Fatal("different physical port instances produced the same binding digest")
	}
	if _, err := authenticator.Authorize(reconnectedGrant, reconnected, reconnectedTunnel); err != nil {
		t.Fatalf("reconnected grant on its own attachment: %v", err)
	}
}

func TestInnerAuthenticationFailuresProduceNoPrincipal(t *testing.T) {
	authenticator := netsec.NewAuthenticator()
	port := mustAttach(t, authenticator, "eth0")

	tunnel, err := authenticator.BeginTunnel(port, "anonymous")
	if err != nil {
		t.Fatalf("BeginTunnel: %v", err)
	}
	if err := authenticator.BindExporter(tunnel, []byte("exporter")); err != nil {
		t.Fatalf("BindExporter: %v", err)
	}
	if _, err := authenticator.CompleteInner(tunnel, "alice", true); !errors.Is(err, netsec.ErrExchangeRequired) {
		t.Fatalf("completion without exchange error = %v, want ErrExchangeRequired", err)
	}
	if err := authenticator.RecordInnerExchange(tunnel, []byte("failed-response")); err != nil {
		t.Fatalf("RecordInnerExchange: %v", err)
	}
	if _, err := authenticator.CompleteInner(tunnel, "alice", false); !errors.Is(err, netsec.ErrInnerAuthFailed) {
		t.Fatalf("failed inner authentication error = %v, want ErrInnerAuthFailed", err)
	}
	if state, err := authenticator.State(tunnel); err != nil || state != netsec.TunnelStateFailed {
		t.Fatalf("state after failure = %v, %v", state, err)
	}
	if _, err := authenticator.CompleteInner(tunnel, "alice", true); !errors.Is(err, netsec.ErrInnerAuthFailed) {
		t.Fatalf("failed tunnel completed successfully: %v", err)
	}
	if err := authenticator.BindExporter(tunnel, []byte("another")); !errors.Is(err, netsec.ErrTunnelState) {
		t.Fatalf("BindExporter on failed tunnel error = %v, want ErrTunnelState", err)
	}
}

func TestConcurrentParallelTunnelsRemainIsolated(t *testing.T) {
	authenticator := netsec.NewAuthenticator()
	port := mustAttach(t, authenticator, "eth0")
	const outer = "anonymous@realm.example"
	const tunnels = 32

	type result struct {
		identity string
		err      error
	}
	results := make(chan result, tunnels)
	var wg sync.WaitGroup

	for i := 0; i < tunnels; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			identity := fmt.Sprintf("user-%02d@corp.example", index)
			tunnel, err := authenticator.BeginTunnel(port, outer)
			if err != nil {
				results <- result{err: fmt.Errorf("tunnel %d begin: %w", index, err)}
				return
			}
			if err := authenticator.BindExporter(tunnel, []byte(fmt.Sprintf("exporter-%02d", index))); err != nil {
				results <- result{err: fmt.Errorf("tunnel %d exporter: %w", index, err)}
				return
			}
			for _, packet := range []string{"inner-request", fmt.Sprintf("response-%02d", index), "inner-success"} {
				if err := authenticator.RecordInnerExchange(tunnel, []byte(packet)); err != nil {
					results <- result{err: fmt.Errorf("tunnel %d exchange: %w", index, err)}
					return
				}
			}
			grant, err := authenticator.CompleteInner(tunnel, identity, true)
			if err != nil {
				results <- result{err: fmt.Errorf("tunnel %d complete: %w", index, err)}
				return
			}
			principal, err := authenticator.Authorize(grant, port, tunnel)
			if err != nil {
				results <- result{err: fmt.Errorf("tunnel %d authorize: %w", index, err)}
				return
			}
			if principal.Name != identity {
				results <- result{err: fmt.Errorf("tunnel %d principal = %q, want %q", index, principal.Name, identity)}
				return
			}
			results <- result{identity: identity}
		}(i)
	}

	wg.Wait()
	close(results)

	seen := make(map[string]bool, tunnels)
	for result := range results {
		if result.err != nil {
			t.Error(result.err)
			continue
		}
		if seen[result.identity] {
			t.Errorf("duplicate principal identity %q", result.identity)
		}
		seen[result.identity] = true
	}
	if len(seen) != tunnels {
		t.Fatalf("authorized %d distinct principals, want %d", len(seen), tunnels)
	}
}

func TestCloseAndDetachInvalidateOutstandingGrants(t *testing.T) {
	authenticator := netsec.NewAuthenticator()
	port := mustAttach(t, authenticator, "eth0")
	tunnel, grant := establish(
		t, authenticator, port, "anonymous", "exporter",
		[]string{"request", "response", "success"}, "alice@corp.example",
	)

	authenticator.CloseTunnel(tunnel)
	if _, err := authenticator.Authorize(grant, port, tunnel); !errors.Is(err, netsec.ErrGrantInvalid) {
		t.Fatalf("grant after CloseTunnel error = %v, want ErrGrantInvalid", err)
	}

	secondTunnel, secondGrant := establish(
		t, authenticator, port, "anonymous", "exporter",
		[]string{"request", "response", "success"}, "alice@corp.example",
	)
	if !authenticator.DetachPort(port) {
		t.Fatal("DetachPort returned false for current attachment")
	}
	if _, err := authenticator.Authorize(secondGrant, port, secondTunnel); !errors.Is(err, netsec.ErrPortNotAttached) {
		t.Fatalf("grant after DetachPort error = %v, want ErrPortNotAttached", err)
	}
	if _, err := authenticator.BeginTunnel(port, "anonymous"); !errors.Is(err, netsec.ErrPortNotAttached) {
		t.Fatalf("BeginTunnel after DetachPort error = %v, want ErrPortNotAttached", err)
	}
}
