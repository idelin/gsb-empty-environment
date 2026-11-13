package netsec

import (
	"errors"
	"testing"
)

func TestRejectsFinishedImmediatelyAfterCertificateRequest(t *testing.T) {
	m := NewStateMachine()
	if err := m.Step(CertificateRequest); err != nil {
		t.Fatalf("CertificateRequest: %v", err)
	}

	err := m.Step(Finished)
	if !errors.Is(err, ErrCertificateRequired) {
		t.Fatalf("Finished after CertificateRequest = %v, want ErrCertificateRequired", err)
	}
	if m.Complete() {
		t.Fatal("machine completed after skipped Certificate")
	}
}

func TestRejectsFinishedAfterCertificateWithoutVerify(t *testing.T) {
	m := NewStateMachine(CertificateRequest, Certificate)

	err := m.Step(Finished)
	if !errors.Is(err, ErrCertificateVerifyRequired) {
		t.Fatalf("Finished after Certificate = %v, want ErrCertificateVerifyRequired", err)
	}
}

func TestRejectsCertificateVerifyBeforeCertificate(t *testing.T) {
	m := NewStateMachine(CertificateRequest)

	err := m.Step(CertificateVerify)
	if !errors.Is(err, ErrCertificateRequired) {
		t.Fatalf("CertificateVerify before Certificate = %v, want ErrCertificateRequired", err)
	}
}

func TestAcceptsOrderedClientAuthentication(t *testing.T) {
	m := NewStateMachine()
	for _, event := range []Event{
		CertificateRequest,
		Certificate,
		CertificateVerify,
		Finished,
	} {
		if err := m.Step(event); err != nil {
			t.Fatalf("Step(%s): %v", event, err)
		}
	}

	if !m.Complete() {
		t.Fatalf("state = %s, want %s", m.State(), Finished)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestAcceptsOrderedClientAuthenticationWithoutFinished(t *testing.T) {
	err := ValidateSequence([]string{
		"CertificateRequest",
		"Certificate",
		"CertificateVerify",
	})
	if err != nil {
		t.Fatalf("ValidateSequence: %v", err)
	}
}

func TestSequenceValidationRejectsSkippedMessages(t *testing.T) {
	tests := []struct {
		name   string
		events []string
		target error
	}{
		{
			name:   "missing certificate",
			events: []string{"CertificateRequest", "Finished"},
			target: ErrCertificateRequired,
		},
		{
			name:   "missing certificate verify",
			events: []string{"CertificateRequest", "Certificate", "Finished"},
			target: ErrCertificateVerifyRequired,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateSequence(test.events); !errors.Is(err, test.target) {
				t.Fatalf("ValidateSequence(%v) = %v, want %v", test.events, err, test.target)
			}
		})
	}
}

func TestIgnoresUnrelatedTLSHandshakeStates(t *testing.T) {
	events := []string{
		"ClientHello",
		"ServerHello",
		"EncryptedExtensions",
		"CertificateRequest",
		"Certificate",
		"CertificateVerify",
		"Finished",
	}
	if err := ValidateSequence(events); err != nil {
		t.Fatalf("ValidateSequence: %v", err)
	}
}

func TestFinishedWithoutCertificateRequestIsAllowed(t *testing.T) {
	m := NewStateMachine()
	if err := m.Step(Finished); err != nil {
		t.Fatalf("Finished: %v", err)
	}
	if !m.Complete() {
		t.Fatal("machine did not complete")
	}
}

func TestCertificateRequestStartsANewExchange(t *testing.T) {
	m := NewStateMachine(
		CertificateRequest,
		Certificate,
		CertificateVerify,
		Finished,
		CertificateRequest,
		Certificate,
		CertificateVerify,
		Finished,
	)
	if !m.Complete() {
		t.Fatalf("state = %s, want %s", m.State(), Finished)
	}
}

func TestAcceptsSpellingVariants(t *testing.T) {
	events := []string{
		"certificate_request",
		"client-certificate",
		"certificate_verify",
		"client_finished",
	}
	if err := ValidateSequence(events); err != nil {
		t.Fatalf("ValidateSequence: %v", err)
	}
}

func TestRejectsDuplicateCertificate(t *testing.T) {
	m := NewStateMachine(CertificateRequest, Certificate)
	if err := m.Step(Certificate); !errors.Is(err, ErrUnexpectedEvent) {
		t.Fatalf("duplicate Certificate = %v, want ErrUnexpectedEvent", err)
	}
}

func TestUnknownEventIsRejected(t *testing.T) {
	if err := NewStateMachine().Step("not-a-handshake-state"); !errors.Is(err, ErrUnknownEvent) {
		t.Fatalf("unknown event = %v, want ErrUnknownEvent", err)
	}
}

func TestZeroValueMachineStartsAtStart(t *testing.T) {
	m := &StateMachine{}
	if got := m.State(); got != Start {
		t.Fatalf("State() = %s, want %s", got, Start)
	}
	if err := m.Step(CertificateRequest); err != nil {
		t.Fatalf("CertificateRequest: %v", err)
	}
	if err := m.Step(Finished); !errors.Is(err, ErrCertificateRequired) {
		t.Fatalf("Finished after CertificateRequest = %v, want ErrCertificateRequired", err)
	}
}
