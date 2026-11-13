package netsec

import (
	"bytes"
	"crypto"
	"errors"
	"testing"
	"time"
)

type testHarness struct {
	server *Server
	client *MemoryClientSessionCache
	tc     *TicketCipher
	store  *MemorySessionStore
}

func newTestHarness(t *testing.T) *testHarness {
	t.Helper()
	store := NewMemorySessionStore()
	tc, err := NewTicketCipher(bytes.Repeat([]byte{0x5C}, ticketKeyLen), 1)
	if err != nil {
		t.Fatalf("NewTicketCipher: %v", err)
	}
	return &testHarness{
		server: &Server{Store: store, Tickets: tc},
		client: NewMemoryClientSessionCache(),
		tc:     tc,
		store:  store,
	}
}

func fullInput(clientEMS, serverEMS bool, transcript []byte) *HandshakeInput {
	return &HandshakeInput{
		PreMasterSecret:     bytes.Repeat([]byte{0xAA}, 48),
		ClientRandom:        bytes.Repeat([]byte{0x01}, 32),
		ServerRandom:        bytes.Repeat([]byte{0x02}, 32),
		Transcript:          transcript,
		Hash:                HashFunc(crypto.SHA256),
		KeyBlockLength:      40,
		ClientAdvertisesEMS: clientEMS,
		ServerAdvertisesEMS: serverEMS,
		Lifetime:            time.Hour,
	}
}

func TestFullHandshakeEMSMatrix(t *testing.T) {
	tests := []struct {
		name    string
		client  bool
		server  bool
		wantEMS bool
		wantErr error
	}{
		{name: "both support EMS", client: true, server: true, wantEMS: true},
		{name: "neither supports EMS", client: false, server: false, wantEMS: false},
		{name: "client only fails", client: true, server: false, wantErr: ErrEMSNegotiationMismatch},
		{name: "server only fails", client: false, server: true, wantErr: ErrEMSNegotiationMismatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHarness(t)
			result, ticket, err := h.server.FullHandshake(fullInput(tt.client, tt.server, []byte("full-transcript")))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("FullHandshake err = %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if result.ExtendedMasterSecret != tt.wantEMS {
				t.Fatalf("negotiated EMS = %v, want %v", result.ExtendedMasterSecret, tt.wantEMS)
			}
			if len(ticket) == 0 {
				t.Fatal("full handshake must return a resumption ticket")
			}
			if len(result.MasterSecret) != MasterSecretLength || len(result.KeyBlock) != 40 {
				t.Fatal("unexpected derived secret lengths")
			}

			clientResult, err := h.client.FullHandshake("peer", ticket, fullInput(tt.client, tt.server, []byte("full-transcript")))
			if err != nil {
				t.Fatalf("client FullHandshake: %v", err)
			}
			if !bytes.Equal(clientResult.MasterSecret, result.MasterSecret) {
				t.Fatal("client and server must derive the same master secret")
			}
			if clientResult.Session.ExtendedMasterSecret != tt.wantEMS {
				t.Fatal("client session must record the negotiated EMS flag")
			}
		})
	}
}

func TestResumeConsistentFlagsSucceeds(t *testing.T) {
	h := newTestHarness(t)

	full, ticket, err := h.server.FullHandshake(fullInput(true, true, []byte("full-transcript")))
	if err != nil {
		t.Fatalf("FullHandshake: %v", err)
	}
	if _, err := h.client.FullHandshake("peer", ticket, fullInput(true, true, []byte("full-transcript"))); err != nil {
		t.Fatalf("client FullHandshake: %v", err)
	}

	resumeIn := &ResumeInput{
		Ticket:              ticket,
		ClientRandom:        bytes.Repeat([]byte{0x0A}, 32),
		ServerRandom:        bytes.Repeat([]byte{0x0B}, 32),
		Transcript:          []byte("resume-transcript"),
		ClientAdvertisesEMS: true,
		ServerAdvertisesEMS: true,
	}
	serverResume, err := h.server.Resume(resumeIn)
	if err != nil {
		t.Fatalf("server Resume: %v", err)
	}
	if !serverResume.ExtendedMasterSecret {
		t.Fatal("resumed connection must keep the EMS flag")
	}
	if !bytes.Equal(serverResume.MasterSecret, full.MasterSecret) {
		t.Fatal("resumption must reuse the original master secret")
	}

	clientResume, err := h.client.Resume("peer", &ClientResumeInput{
		ClientRandom:        resumeIn.ClientRandom,
		ServerRandom:        resumeIn.ServerRandom,
		Transcript:          resumeIn.Transcript,
		ClientAdvertisesEMS: true,
		ServerAdvertisesEMS: true,
	})
	if err != nil {
		t.Fatalf("client Resume: %v", err)
	}
	if !bytes.Equal(clientResume.KeyBlock, serverResume.KeyBlock) {
		t.Fatal("client and server resumed key blocks must match")
	}

	differentRandoms, err := h.server.Resume(&ResumeInput{
		Ticket:              ticket,
		ClientRandom:        bytes.Repeat([]byte{0x0C}, 32),
		ServerRandom:        bytes.Repeat([]byte{0x0D}, 32),
		Transcript:          []byte("other-resume"),
		ClientAdvertisesEMS: true,
		ServerAdvertisesEMS: true,
	})
	if err != nil {
		t.Fatalf("second Resume: %v", err)
	}
	if bytes.Equal(differentRandoms.KeyBlock, serverResume.KeyBlock) {
		t.Fatal("fresh random values must produce a fresh key block")
	}
}

func TestResumeLegacySessionConsistentFlags(t *testing.T) {
	h := newTestHarness(t)
	_, ticket, err := h.server.FullHandshake(fullInput(false, false, []byte("legacy-full")))
	if err != nil {
		t.Fatalf("FullHandshake: %v", err)
	}
	if _, err := h.client.FullHandshake("legacy", ticket, fullInput(false, false, []byte("legacy-full"))); err != nil {
		t.Fatalf("client FullHandshake: %v", err)
	}

	serverResume, err := h.server.Resume(&ResumeInput{
		Ticket:              ticket,
		ClientRandom:        bytes.Repeat([]byte{0x0A}, 32),
		ServerRandom:        bytes.Repeat([]byte{0x0B}, 32),
		Transcript:          []byte("legacy-resume"),
		ClientAdvertisesEMS: false,
		ServerAdvertisesEMS: false,
	})
	if err != nil {
		t.Fatalf("legacy Resume: %v", err)
	}
	if serverResume.ExtendedMasterSecret {
		t.Fatal("legacy resumption must not negotiate EMS")
	}

	if _, err := h.client.Resume("legacy", &ClientResumeInput{
		ClientRandom:        bytes.Repeat([]byte{0x0A}, 32),
		ServerRandom:        bytes.Repeat([]byte{0x0B}, 32),
		ClientAdvertisesEMS: false,
		ServerAdvertisesEMS: false,
	}); err != nil {
		t.Fatalf("client legacy Resume: %v", err)
	}
}

func TestResumePolicyFailures(t *testing.T) {
	tests := []struct {
		name       string
		sessionEMS bool
		client     bool
		server     bool
		wantErr    error
	}{
		{name: "ems session loses flag", sessionEMS: true, client: false, server: false, wantErr: ErrEMSResumptionLost},
		{name: "legacy session gains flag", sessionEMS: false, client: true, server: true, wantErr: ErrEMSResumptionUnexpected},
		{name: "client only advertises", sessionEMS: true, client: true, server: false, wantErr: ErrEMSNegotiationMismatch},
		{name: "server only advertises", sessionEMS: false, client: false, server: true, wantErr: ErrEMSNegotiationMismatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHarness(t)
			_, ticket, err := h.server.FullHandshake(fullInput(tt.sessionEMS, tt.sessionEMS, []byte("full")))
			if err != nil {
				t.Fatalf("FullHandshake: %v", err)
			}

			_, err = h.server.Resume(&ResumeInput{
				Ticket:              ticket,
				ClientRandom:        bytes.Repeat([]byte{0x0A}, 32),
				ServerRandom:        bytes.Repeat([]byte{0x0B}, 32),
				Transcript:          []byte("resume"),
				ClientAdvertisesEMS: tt.client,
				ServerAdvertisesEMS: tt.server,
			})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("server Resume err = %v, want %v", err, tt.wantErr)
			}

			if _, err := h.client.FullHandshake("peer", ticket, fullInput(tt.sessionEMS, tt.sessionEMS, []byte("full"))); err != nil {
				t.Fatalf("client FullHandshake: %v", err)
			}
			_, err = h.client.Resume("peer", &ClientResumeInput{
				ClientRandom:        bytes.Repeat([]byte{0x0A}, 32),
				ServerRandom:        bytes.Repeat([]byte{0x0B}, 32),
				ClientAdvertisesEMS: tt.client,
				ServerAdvertisesEMS: tt.server,
			})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("client Resume err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestResumeTicketIntegrityAndLifecycle(t *testing.T) {
	h := newTestHarness(t)
	base := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	now := base
	h.store.now = func() time.Time { return now }

	in := fullInput(true, true, []byte("full"))
	in.Now = base
	_, ticket, err := h.server.FullHandshake(in)
	if err != nil {
		t.Fatalf("FullHandshake: %v", err)
	}

	mutated := append([]byte(nil), ticket...)
	mutated[len(mutated)-1] ^= 0xFF
	if _, err := h.server.Resume(&ResumeInput{
		Ticket:              mutated,
		ClientAdvertisesEMS: true,
		ServerAdvertisesEMS: true,
	}); !errors.Is(err, ErrTicketInvalid) {
		t.Fatalf("tampered ticket err = %v, want %v", err, ErrTicketInvalid)
	}

	now = base.Add(in.Lifetime + time.Second)
	if _, err := h.server.Resume(&ResumeInput{
		Ticket:              ticket,
		ClientAdvertisesEMS: true,
		ServerAdvertisesEMS: true,
	}); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("expired ticket err = %v, want %v", err, ErrSessionExpired)
	}

	if _, err := h.client.Resume("never-seen", &ClientResumeInput{
		ClientAdvertisesEMS: true,
		ServerAdvertisesEMS: true,
	}); !errors.Is(err, ErrSessionUnknown) {
		t.Fatalf("missing client session err = %v, want %v", err, ErrSessionUnknown)
	}
}

func TestTicketDoesNotEncodeEMSAndStoreIsAuthoritative(t *testing.T) {
	h := newTestHarness(t)

	emsInput := fullInput(true, true, []byte("full"))
	_, emsTicket, err := h.server.FullHandshake(emsInput)
	if err != nil {
		t.Fatalf("EMS FullHandshake: %v", err)
	}

	legacyInput := fullInput(false, false, []byte("full"))
	_, legacyTicket, err := h.server.FullHandshake(legacyInput)
	if err != nil {
		t.Fatalf("legacy FullHandshake: %v", err)
	}

	emsData, err := h.tc.Open(emsTicket)
	if err != nil {
		t.Fatalf("Open EMS ticket: %v", err)
	}
	legacyData, err := h.tc.Open(legacyTicket)
	if err != nil {
		t.Fatalf("Open legacy ticket: %v", err)
	}

	emsSession, err := h.store.Get(emsData.SessionID)
	if err != nil {
		t.Fatalf("Get EMS session: %v", err)
	}
	legacySession, err := h.store.Get(legacyData.SessionID)
	if err != nil {
		t.Fatalf("Get legacy session: %v", err)
	}
	if !emsSession.ExtendedMasterSecret {
		t.Fatal("EMS session must be marked in the authoritative store")
	}
	if legacySession.ExtendedMasterSecret {
		t.Fatal("legacy session must not be marked EMS in the authoritative store")
	}

	// Replaying the EMS ticket while the client omits EMS must fail even
	// though the ticket itself carries no flag to alter.
	if _, err := h.server.Resume(&ResumeInput{
		Ticket:              emsTicket,
		ClientAdvertisesEMS: false,
		ServerAdvertisesEMS: false,
	}); !errors.Is(err, ErrEMSResumptionLost) {
		t.Fatalf("EMS downgrade err = %v, want %v", err, ErrEMSResumptionLost)
	}
}
