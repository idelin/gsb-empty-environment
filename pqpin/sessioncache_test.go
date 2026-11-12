package pqpin

import (
	"crypto/tls"
	"testing"
	"time"
)

// realSessionState obtains a client-side tls.SessionState from a genuine
// in-process TLS 1.3 handshake, avoiding the unexported SessionState fields.
func realSessionState(t *testing.T) *tls.SessionState {
	t.Helper()

	clientRaw, serverRaw := pipePair("9443")
	serverReady := make(chan *tls.Conn, 1)
	serverDone := make(chan error, 1)
	go runTLSServer(t, serverRaw, serverConfig(t), serverReady, serverDone)

	cache := tls.NewLRUClientSessionCache(2)
	clientConn := tls.Client(clientRaw, &tls.Config{
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS13,
		ClientSessionCache: cache,
		ServerName:         "localhost",
	})
	if err := clientConn.Handshake(); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	// crypto/tls only handles the post-handshake NewSessionTicket during a
	// Read; pump one in the background (the test pipe has no real deadlines).
	go func() {
		buf := make([]byte, 1)
		_, _ = clientConn.Read(buf)
	}()

	// TLS 1.3 NewSessionTicket is processed after the handshake returns.
	var css *tls.ClientSessionState
	var ok bool
	waitUntil := time.Now().Add(2 * time.Second)
	for time.Now().Before(waitUntil) {
		css, ok = cache.Get("localhost")
		if ok && css != nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !ok || css == nil {
		t.Fatalf("no session cached after handshake")
	}
	_, state, err := css.ResumptionState()
	if err != nil {
		t.Fatalf("ResumptionState: %v", err)
	}
	if err := clientConn.Close(); err != nil {
		t.Fatal(err)
	}
	srv := <-serverReady
	if srv != nil {
		_ = srv.Close()
	}
	if serr := <-serverDone; serr != nil {
		t.Fatalf("server handshake: %v", serr)
	}
	return state
}

func TestOriginMarkerRoundTrip(t *testing.T) {
	state := realSessionState(t)
	state.Extra = setOriginMarker(state.Extra, OriginHybrid)

	encoded, err := state.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	reparsed, err := tls.ParseSessionState(encoded)
	if err != nil {
		t.Fatalf("ParseSessionState: %v", err)
	}
	if got := readOrigin(reparsed); got != OriginHybrid {
		t.Fatalf("origin marker lost across serialization: %v", got)
	}
}

func TestSetOriginMarkerReplacesAndPreservesForeignEntries(t *testing.T) {
	extra := [][]byte{[]byte("foreign-extension")}
	extra = setOriginMarker(extra, OriginClassic)
	extra = setOriginMarker(extra, OriginHybrid) // must replace, not duplicate

	var markers int
	var foreign bool
	for _, entry := range extra {
		if len(entry) >= len(originMarkerPrefix) &&
			string(entry[:len(originMarkerPrefix)]) == string(originMarkerPrefix) {
			markers++
		}
		if string(entry) == "foreign-extension" {
			foreign = true
		}
	}
	if markers != 1 {
		t.Fatalf("want exactly 1 marker, got %d", markers)
	}
	if !foreign {
		t.Fatalf("foreign Extra entries must be preserved")
	}
}

func TestWrappedCacheRecordsOrigin(t *testing.T) {
	origins := newOriginCache()
	cache := WrapSessionCache(tls.NewLRUClientSessionCache(8), origins)

	state := realSessionState(t)
	css, err := tls.NewResumptionState([]byte("ticket-classic"), state)
	if err != nil {
		t.Fatal(err)
	}
	origins.setPending("localhost", OriginClassic)
	cache.Put("localhost", css)

	if got := ResumeOrigin(cache, "localhost"); got != OriginClassic {
		t.Fatalf("want classic origin, got %v", got)
	}
	if got := origins.takePending("localhost"); got != OriginUnknown {
		t.Fatalf("pending origin not consumed: %v", got)
	}

	hybridState := realSessionState(t)
	css2, err := tls.NewResumptionState([]byte("ticket-hybrid"), hybridState)
	if err != nil {
		t.Fatal(err)
	}
	origins.setPending("localhost", OriginHybrid)
	cache.Put("localhost", css2)
	if got := ResumeOrigin(cache, "localhost"); got != OriginHybrid {
		t.Fatalf("want hybrid origin, got %v", got)
	}
}

func TestResumeOriginUnknownWithoutMarker(t *testing.T) {
	cache := tls.NewLRUClientSessionCache(4)
	state := realSessionState(t)
	css, err := tls.NewResumptionState([]byte("legacy-ticket"), state)
	if err != nil {
		t.Fatal(err)
	}
	cache.Put("k", css)
	// A plain stdlib cache carries no pqpin marker: origin must be unknown,
	// never implicitly trusted as hybrid.
	if got := ResumeOrigin(cache, "k"); got != OriginUnknown {
		t.Fatalf("unmarked session must be unknown, got %v", got)
	}
}
