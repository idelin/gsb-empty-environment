package netsec

import (
	"errors"
	"testing"
	"time"
)

func TestDecodeTicketAge(t *testing.T) {
	tests := []struct {
		name       string
		obfuscated uint32
		ageAdd     uint32
		want       time.Duration
	}{
		{
			name:       "no obfuscation",
			obfuscated: 1500,
			ageAdd:     0,
			want:       1500 * time.Millisecond,
		},
		{
			name:       "normal obfuscation",
			obfuscated: 3500,
			ageAdd:     2000,
			want:       1500 * time.Millisecond,
		},
		{
			name:       "wrap around",
			obfuscated: 0,
			ageAdd:     ^uint32(0),
			want:       time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DecodeTicketAge(tt.obfuscated, tt.ageAdd); got != tt.want {
				t.Fatalf("DecodeTicketAge() = %s, want %s", got, tt.want)
			}
			if got := UnobfuscateTicketAge(tt.obfuscated, tt.ageAdd); got != tt.want {
				t.Fatalf("UnobfuscateTicketAge() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestValidateTicketAgeWindow(t *testing.T) {
	const (
		lifetime  = 10 * time.Second
		tolerance = 2 * time.Second
		ageAdd    = uint32(1000)
	)

	tests := []struct {
		name    string
		age     time.Duration
		wantErr bool
	}{
		{name: "fresh ticket", age: 0},
		{name: "inside lifetime", age: lifetime - time.Millisecond},
		{name: "at lifetime", age: lifetime},
		{name: "inside tolerance", age: lifetime + tolerance/2},
		{name: "at window edge", age: lifetime + tolerance},
		{name: "one millisecond outside", age: lifetime + tolerance + time.Millisecond, wantErr: true},
		{name: "one full window outside", age: lifetime + 2*tolerance, wantErr: true},
		{name: "twice the entire window", age: 2 * (lifetime + tolerance), wantErr: true},
		{name: "far beyond lifetime", age: 2 * lifetime, wantErr: true},
		{name: "maximum uint32 milliseconds", age: time.Duration(^uint32(0)) * time.Millisecond, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.age > time.Duration(^uint32(0))*time.Millisecond {
				t.Fatalf("test age %s is not representable in uint32 milliseconds", tt.age)
			}
			obfuscated := uint32(tt.age/time.Millisecond) + ageAdd
			err := ValidateTicketAge(obfuscated, ageAdd, lifetime, tolerance)
			if gotErr := err != nil; gotErr != tt.wantErr {
				t.Fatalf("ValidateTicketAge() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && !errors.Is(err, ErrTicketAgeOutsideWindow) {
				t.Fatalf("ValidateTicketAge() error = %v, want ErrTicketAgeOutsideWindow", err)
			}
			if got := TicketAgeWithinWindow(obfuscated, ageAdd, lifetime, tolerance); got == tt.wantErr {
				t.Fatalf("TicketAgeWithinWindow() = %v, want %v", got, !tt.wantErr)
			}
		})
	}
}

func TestValidateTicketAgePolicy(t *testing.T) {
	tests := []struct {
		name    string
		policy  TicketAgePolicy
		age     time.Duration
		wantErr error
	}{
		{
			name:    "zero lifetime",
			policy:  TicketAgePolicy{Tolerance: time.Second},
			age:     0,
			wantErr: ErrInvalidLifetime,
		},
		{
			name:    "negative lifetime",
			policy:  TicketAgePolicy{Lifetime: -time.Second},
			age:     0,
			wantErr: ErrInvalidLifetime,
		},
		{
			name:    "negative tolerance",
			policy:  TicketAgePolicy{Lifetime: time.Second, Tolerance: -time.Second},
			age:     0,
			wantErr: ErrInvalidTolerance,
		},
		{
			name:    "negative age",
			policy:  TicketAgePolicy{Lifetime: time.Second},
			age:     -time.Nanosecond,
			wantErr: ErrTicketAgeOutsideWindow,
		},
		{
			name:   "zero tolerance exact lifetime",
			policy: TicketAgePolicy{Lifetime: time.Second},
			age:    time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.policy.ValidateAge(tt.age)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("ValidateAge() error = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ValidateAge() error = %v, want %v", err, tt.wantErr)
			}
			if !errors.Is(err, ErrInvalidPolicy) && tt.wantErr != ErrTicketAgeOutsideWindow {
				t.Fatalf("ValidateAge() error = %v, does not wrap ErrInvalidPolicy", err)
			}
		})
	}
}

func TestTicketAgeValidateWithIssueTime(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	const (
		lifetime  = time.Hour
		tolerance = 5 * time.Second
		ageAdd    = uint32(12345)
	)

	tests := []struct {
		name      string
		clientAge time.Duration
		issuedAt  time.Time
		wantErr   error
	}{
		{
			name:      "matching ages",
			clientAge: 20 * time.Minute,
			issuedAt:  now.Add(-20 * time.Minute),
		},
		{
			name:      "client age within tolerance",
			clientAge: 20*time.Minute + tolerance,
			issuedAt:  now.Add(-20 * time.Minute),
		},
		{
			name:      "client age beyond tolerance",
			clientAge: 20*time.Minute + tolerance + time.Millisecond,
			issuedAt:  now.Add(-20 * time.Minute),
			wantErr:   ErrTicketAgeMismatch,
		},
		{
			name:      "server age beyond lifetime window",
			clientAge: lifetime + tolerance + time.Second,
			issuedAt:  now.Add(-lifetime - tolerance - time.Second),
			wantErr:   ErrTicketAgeOutsideWindow,
		},
		{
			name:      "issue time in future within tolerance",
			clientAge: 0,
			issuedAt:  now.Add(tolerance),
		},
		{
			name:      "issue time in future beyond tolerance",
			clientAge: 0,
			issuedAt:  now.Add(tolerance + time.Millisecond),
			wantErr:   ErrTicketAgeMismatch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ticket := TicketAge{
				Obfuscated: uint32(tt.clientAge/time.Millisecond) + ageAdd,
				AgeAdd:     ageAdd,
				IssuedAt:   tt.issuedAt,
				Lifetime:   lifetime,
			}
			err := ticket.Validate(now, tolerance)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Validate() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
