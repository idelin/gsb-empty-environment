package netsec

import (
	"errors"
	"testing"
)

func TestAuthorizeBind(t *testing.T) {
	p := New()
	untrusted := Tenant{ID: "tenant-a", Trusted: false}
	trusted := Tenant{ID: "tenant-b", Trusted: true}

	tests := []struct {
		name string
		req  BindRequest
		want error
	}{
		{
			name: "untrusted copy allowed",
			req:  BindRequest{Tenant: untrusted, Mode: ModeCopy},
			want: nil,
		},
		{
			name: "untrusted zero-copy rejected not downgraded",
			req:  BindRequest{Tenant: untrusted, Mode: ModeZeroCopy},
			want: ErrZeroCopyDenied,
		},
		{
			name: "trusted copy allowed",
			req:  BindRequest{Tenant: trusted, Mode: ModeCopy},
			want: nil,
		},
		{
			name: "trusted zero-copy allowed",
			req:  BindRequest{Tenant: trusted, Mode: ModeZeroCopy},
			want: nil,
		},
		{
			name: "missing tenant id",
			req:  BindRequest{Tenant: Tenant{Trusted: false}, Mode: ModeCopy},
			want: ErrEmptyTenant,
		},
		{
			name: "zero mode rejected",
			req:  BindRequest{Tenant: untrusted, Mode: 0},
			want: ErrInvalidBindMode,
		},
		{
			name: "both flags rejected",
			req:  BindRequest{Tenant: untrusted, Mode: ModeCopy | ModeZeroCopy},
			want: ErrInvalidBindMode,
		},
		{
			name: "unknown flag rejected",
			req:  BindRequest{Tenant: untrusted, Mode: BindMode(0x10)},
			want: ErrInvalidBindMode,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := p.AuthorizeBind(tt.req)
			if !errors.Is(err, tt.want) {
				t.Fatalf("AuthorizeBind() = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestBindModeString(t *testing.T) {
	if got := ModeCopy.String(); got != "copy" {
		t.Fatalf("ModeCopy.String() = %q, want copy", got)
	}
	if got := ModeZeroCopy.String(); got != "zero-copy" {
		t.Fatalf("ModeZeroCopy.String() = %q, want zero-copy", got)
	}
	if got := BindMode(99).String(); got != "unknown" {
		t.Fatalf("BindMode(99).String() = %q, want unknown", got)
	}
}
