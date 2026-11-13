package netsec

import (
	"errors"
	"testing"
)

func TestNegotiateEMS(t *testing.T) {
	tests := []struct {
		name    string
		client  bool
		server  bool
		wantEMS bool
		wantErr error
	}{
		{name: "both support", client: true, server: true, wantEMS: true},
		{name: "neither supports", client: false, server: false, wantEMS: false},
		{name: "client only", client: true, server: false, wantErr: ErrEMSNegotiationMismatch},
		{name: "server only", client: false, server: true, wantErr: ErrEMSNegotiationMismatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NegotiateEMS(tt.client, tt.server)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if err == nil && got != tt.wantEMS {
				t.Fatalf("ems = %v, want %v", got, tt.wantEMS)
			}
		})
	}
}

func TestValidateResumption(t *testing.T) {
	tests := []struct {
		name       string
		client     bool
		server     bool
		sessionEMS bool
		wantErr    error
		wantEMS    bool
	}{
		{name: "ems session both present consistent", client: true, server: true, sessionEMS: true, wantEMS: true},
		{name: "legacy session both absent consistent", client: false, server: false, sessionEMS: false, wantEMS: false},
		{name: "ems session lost flag", client: false, server: false, sessionEMS: true, wantErr: ErrEMSResumptionLost},
		{name: "legacy session gains flag", client: true, server: true, sessionEMS: false, wantErr: ErrEMSResumptionUnexpected},
		{name: "client only during ems resume", client: true, server: false, sessionEMS: true, wantErr: ErrEMSNegotiationMismatch},
		{name: "server only during legacy resume", client: false, server: true, sessionEMS: false, wantErr: ErrEMSNegotiationMismatch},
		{name: "client only during legacy resume", client: true, server: false, sessionEMS: false, wantErr: ErrEMSNegotiationMismatch},
		{name: "server only during ems resume", client: false, server: true, sessionEMS: true, wantErr: ErrEMSNegotiationMismatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := &Session{ExtendedMasterSecret: tt.sessionEMS}
			err := ValidateResumption(tt.client, tt.server, session)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ValidateResumption err = %v, want %v", err, tt.wantErr)
			}

			err = ValidateResumptionFlag(tt.client, tt.server, tt.sessionEMS)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ValidateResumptionFlag err = %v, want %v", err, tt.wantErr)
			}
			if err == nil {
				negotiated, negErr := NegotiateEMS(tt.client, tt.server)
				if negErr != nil || negotiated != tt.wantEMS {
					t.Fatalf("negotiated = %v (%v), want %v", negotiated, negErr, tt.wantEMS)
				}
			}
		})
	}
}

func TestValidateResumptionNilSession(t *testing.T) {
	if err := ValidateResumption(true, true, nil); !errors.Is(err, ErrSessionUnknown) {
		t.Fatalf("nil session err = %v, want %v", err, ErrSessionUnknown)
	}
}
