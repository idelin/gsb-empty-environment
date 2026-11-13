package netsec

import (
	"bytes"
	"errors"
	"testing"
)

var secret = []byte("test-traffic-secret")

// runServerSideHandshake plays a full handshake as seen by a server-role
// stack. inject, if set, fires right before the client's second flight —
// the legal slot for a client compatibility CCS.
func runServerSideHandshake(inject func(h *Handler)) (transcriptHash, verifyData []byte) {
	h := NewHandler(Server)
	h.Handshake(Inbound, ClientHello, []byte("client-hello"))
	h.Handshake(Outbound, ServerHello, []byte("server-hello"))
	h.Handshake(Outbound, EncryptedExtensions, []byte("ee"))
	h.Handshake(Outbound, Certificate, []byte("cert"))
	h.Handshake(Outbound, CertificateVerify, []byte("cv"))
	h.Handshake(Outbound, Finished, []byte("server-fin"))
	if inject != nil {
		inject(h)
	}
	h.Handshake(Inbound, Finished, []byte("client-fin"))
	return h.TranscriptHash(), VerifyData(secret, h.TranscriptHash())
}

func TestCCSStaysOutOfTranscript(t *testing.T) {
	baseHash, baseVerify := runServerSideHandshake(nil)
	withHash, withVerify := runServerSideHandshake(func(h *Handler) {
		if err := h.ChangeCipherSpec(Inbound, MarshalCompatCCS()); err != nil {
			t.Fatalf("legal CCS rejected: %v", err)
		}
	})
	if !bytes.Equal(baseHash, withHash) {
		t.Fatal("CCS leaked into the handshake transcript")
	}
	if !bytes.Equal(baseVerify, withVerify) {
		t.Fatal("CCS at a legal position changed Finished verify_data")
	}
}

func TestServerCCSAtLegalPosition(t *testing.T) {
	h := NewHandler(Client)
	h.Handshake(Outbound, ClientHello, []byte("client-hello"))
	h.Handshake(Inbound, ServerHello, []byte("server-hello"))
	if err := h.ChangeCipherSpec(Inbound, MarshalCompatCCS()); err != nil {
		t.Fatalf("server CCS right after ServerHello rejected: %v", err)
	}
	h.Handshake(Inbound, EncryptedExtensions, []byte("ee"))
	h.Handshake(Inbound, Finished, []byte("server-fin"))
}

func TestCCSAtIllegalPositions(t *testing.T) {
	t.Run("before any handshake message", func(t *testing.T) {
		h := NewHandler(Server)
		if err := h.ChangeCipherSpec(Inbound, MarshalCompatCCS()); !errors.Is(err, ErrIllegalCCS) {
			t.Fatalf("got %v, want ErrIllegalCCS", err)
		}
	})
	t.Run("client CCS before ServerHello", func(t *testing.T) {
		h := NewHandler(Server)
		h.Handshake(Inbound, ClientHello, []byte("client-hello"))
		if err := h.ChangeCipherSpec(Inbound, MarshalCompatCCS()); !errors.Is(err, ErrIllegalCCS) {
			t.Fatalf("got %v, want ErrIllegalCCS", err)
		}
	})
	t.Run("server CCS after EncryptedExtensions", func(t *testing.T) {
		h := NewHandler(Client)
		h.Handshake(Outbound, ClientHello, []byte("client-hello"))
		h.Handshake(Inbound, ServerHello, []byte("server-hello"))
		h.Handshake(Inbound, EncryptedExtensions, []byte("ee"))
		if err := h.ChangeCipherSpec(Inbound, MarshalCompatCCS()); !errors.Is(err, ErrIllegalCCS) {
			t.Fatalf("got %v, want ErrIllegalCCS", err)
		}
	})
	t.Run("second CCS in the same window", func(t *testing.T) {
		h := NewHandler(Client)
		h.Handshake(Outbound, ClientHello, []byte("client-hello"))
		h.Handshake(Inbound, ServerHello, []byte("server-hello"))
		if err := h.ChangeCipherSpec(Inbound, MarshalCompatCCS()); err != nil {
			t.Fatalf("first CCS rejected: %v", err)
		}
		if err := h.ChangeCipherSpec(Inbound, MarshalCompatCCS()); !errors.Is(err, ErrIllegalCCS) {
			t.Fatalf("got %v, want ErrIllegalCCS", err)
		}
	})
}

func TestCCSAfterFinished(t *testing.T) {
	h := NewHandler(Server)
	h.Handshake(Inbound, ClientHello, []byte("client-hello"))
	h.Handshake(Outbound, ServerHello, []byte("server-hello"))
	h.Handshake(Outbound, EncryptedExtensions, []byte("ee"))
	h.Handshake(Outbound, Finished, []byte("server-fin"))
	h.Handshake(Inbound, Finished, []byte("client-fin"))

	if err := h.ChangeCipherSpec(Inbound, MarshalCompatCCS()); err != nil {
		t.Fatalf("one CCS after Finished must be tolerated: %v", err)
	}
	if err := h.ChangeCipherSpec(Inbound, MarshalCompatCCS()); !errors.Is(err, ErrIllegalCCS) {
		t.Fatalf("second post-Finished CCS: got %v, want ErrIllegalCCS", err)
	}
}

func TestCCSNeverChangesKeys(t *testing.T) {
	h := NewHandler(Client)
	if h.KeyPhase() != KeyPhaseInitial {
		t.Fatal("handshake must start in the initial key phase")
	}
	h.Handshake(Outbound, ClientHello, []byte("client-hello"))
	h.Handshake(Inbound, ServerHello, []byte("server-hello"))
	if h.KeyPhase() != KeyPhaseHandshake {
		t.Fatal("ServerHello must move the connection to handshake keys")
	}
	if err := h.ChangeCipherSpec(Inbound, MarshalCompatCCS()); err != nil {
		t.Fatalf("legal CCS rejected: %v", err)
	}
	if h.KeyPhase() != KeyPhaseHandshake {
		t.Fatal("CCS changed the key phase")
	}
	h.Handshake(Inbound, EncryptedExtensions, []byte("ee"))
	h.Handshake(Inbound, Finished, []byte("server-fin"))
	if h.KeyPhase() != KeyPhaseApplication {
		t.Fatal("Finished must move the connection to application keys")
	}
	if err := h.ChangeCipherSpec(Inbound, MarshalCompatCCS()); err != nil {
		t.Fatalf("post-Finished CCS rejected: %v", err)
	}
	if h.KeyPhase() != KeyPhaseApplication {
		t.Fatal("post-Finished CCS changed the key phase")
	}
}

func TestMalformedCCSRejected(t *testing.T) {
	cases := map[string][]byte{
		"empty":             {},
		"truncated":         {RecordTypeChangeCipherSpec, 0x03, 0x03},
		"wrong type":        {23, 0x03, 0x03, 0x00, 0x01, 0x01},
		"bad major version": {RecordTypeChangeCipherSpec, 0x04, 0x03, 0x00, 0x01, 0x01},
		"bad length":        {RecordTypeChangeCipherSpec, 0x03, 0x03, 0x00, 0x02, 0x01},
		"bad body":          {RecordTypeChangeCipherSpec, 0x03, 0x03, 0x00, 0x01, 0x00},
	}
	for name, record := range cases {
		t.Run(name, func(t *testing.T) {
			h := NewHandler(Client)
			h.Handshake(Outbound, ClientHello, []byte("client-hello"))
			h.Handshake(Inbound, ServerHello, []byte("server-hello"))
			if err := h.ChangeCipherSpec(Inbound, record); !errors.Is(err, ErrMalformedCCS) {
				t.Fatalf("got %v, want ErrMalformedCCS", err)
			}
		})
	}
}

func TestOutboundCCSPlacement(t *testing.T) {
	h := NewHandler(Server)
	h.Handshake(Inbound, ClientHello, []byte("client-hello"))
	h.Handshake(Outbound, ServerHello, []byte("server-hello"))
	if err := h.ChangeCipherSpec(Outbound, MarshalCompatCCS()); err != nil {
		t.Fatalf("server sending CCS after ServerHello rejected: %v", err)
	}
	if err := h.ChangeCipherSpec(Outbound, MarshalCompatCCS()); !errors.Is(err, ErrIllegalCCS) {
		t.Fatalf("second outbound CCS: got %v, want ErrIllegalCCS", err)
	}
}
