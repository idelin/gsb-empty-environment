package netsec_test

import (
	"errors"
	"sync"
	"testing"

	"example.org/netsec/q0274"
)

func restrictedRing(t *testing.T) *netsec.Ring {
	t.Helper()

	ring := netsec.NewRing()
	err := ring.RegisterRestrictions(netsec.Restrictions{
		AllowedRegisterOps: []netsec.RegisterOpcode{
			netsec.IORING_REGISTER_FILES_UPDATE,
		},
		AllowedSQEOps: []netsec.Opcode{
			netsec.IORING_OP_READ,
			netsec.IORING_OP_WRITE,
		},
		AllowedFlags: netsec.IOSQE_FIXED_FILE |
			netsec.IOSQE_IO_DRAIN |
			netsec.IOSQE_IO_LINK |
			netsec.IOSQE_ASYNC,
		RequiredFlags: netsec.IOSQE_FIXED_FILE,
	})
	if err != nil {
		t.Fatalf("RegisterRestrictions() error = %v", err)
	}
	if err := ring.Enable(); err != nil {
		t.Fatalf("Enable() error = %v", err)
	}
	return ring
}

func TestNewRingIsDisabled(t *testing.T) {
	ring := netsec.NewRing()

	if !ring.Disabled() {
		t.Fatal("NewRing() did not create a disabled ring")
	}
	if ring.Enabled() {
		t.Fatal("NewRing() created an enabled ring")
	}
}

func TestRestrictionsRequireDisabledRing(t *testing.T) {
	var ring netsec.Ring

	err := ring.RegisterRestrictions(netsec.Restrictions{})
	if !errors.Is(err, netsec.ErrRingNotDisabled) {
		t.Fatalf("RegisterRestrictions() error = %v, want ErrRingNotDisabled", err)
	}
}

func TestEnableRequiresRestrictions(t *testing.T) {
	ring := netsec.NewRing()

	if err := ring.Enable(); !errors.Is(err, netsec.ErrRestrictionsNotRegistered) {
		t.Fatalf("Enable() error = %v, want ErrRestrictionsNotRegistered", err)
	}
	if ring.Enabled() {
		t.Fatal("ring enabled without restrictions")
	}
}

func TestRestrictionsCanOnlyBeRegisteredOnce(t *testing.T) {
	ring := netsec.NewRing()
	restrictions := netsec.Restrictions{
		AllowedSQEOps: []netsec.Opcode{netsec.IORING_OP_READ},
	}

	if err := ring.RegisterRestrictions(restrictions); err != nil {
		t.Fatalf("first RegisterRestrictions() error = %v", err)
	}
	if err := ring.RegisterRestrictions(restrictions); !errors.Is(err, netsec.ErrRestrictionsAlreadyRegistered) {
		t.Fatalf("second RegisterRestrictions() error = %v, want ErrRestrictionsAlreadyRegistered", err)
	}
	if err := ring.Enable(); err != nil {
		t.Fatalf("Enable() error = %v", err)
	}
	if err := ring.RegisterRestrictions(restrictions); !errors.Is(err, netsec.ErrRestrictionsAlreadyRegistered) {
		t.Fatalf("post-enable RegisterRestrictions() error = %v, want ErrRestrictionsAlreadyRegistered", err)
	}
}

func TestDisabledRingRejectsSubmissionsAndRegistrations(t *testing.T) {
	ring := netsec.NewRing()
	if err := ring.RegisterRestrictions(netsec.Restrictions{
		AllowedRegisterOps: []netsec.RegisterOpcode{netsec.IORING_REGISTER_FILES},
		AllowedSQEOps:      []netsec.Opcode{netsec.IORING_OP_READ},
	}); err != nil {
		t.Fatalf("RegisterRestrictions() error = %v", err)
	}

	if err := ring.Submit(netsec.SQE{Opcode: netsec.IORING_OP_READ}); !errors.Is(err, netsec.ErrRingNotEnabled) {
		t.Fatalf("Submit() error = %v, want ErrRingNotEnabled", err)
	}
	if err := ring.Register(netsec.IORING_REGISTER_FILES); !errors.Is(err, netsec.ErrRingNotEnabled) {
		t.Fatalf("Register() error = %v, want ErrRingNotEnabled", err)
	}
}

func TestSubmitEnforcesOpcodeAndFlags(t *testing.T) {
	ring := restrictedRing(t)

	tests := []struct {
		name string
		sqe  netsec.SQE
		want error
	}{
		{
			name: "allowed",
			sqe:  netsec.SQE{Opcode: netsec.IORING_OP_READ, Flags: netsec.IOSQE_FIXED_FILE},
		},
		{
			name: "allowed optional flag",
			sqe: netsec.SQE{
				Opcode: netsec.IORING_OP_WRITE,
				Flags:  netsec.IOSQE_FIXED_FILE | netsec.IOSQE_ASYNC,
			},
		},
		{
			name: "opcode not allowed",
			sqe:  netsec.SQE{Opcode: netsec.IORING_OP_OPENAT, Flags: netsec.IOSQE_FIXED_FILE},
			want: netsec.ErrOpcodeNotAllowed,
		},
		{
			name: "missing required flag",
			sqe:  netsec.SQE{Opcode: netsec.IORING_OP_READ, Flags: netsec.IOSQE_ASYNC},
			want: netsec.ErrRequiredFlagsMissing,
		},
		{
			name: "extra flag",
			sqe: netsec.SQE{
				Opcode: netsec.IORING_OP_READ,
				Flags:  netsec.IOSQE_FIXED_FILE | netsec.IOSQE_BUFFER_SELECT,
			},
			want: netsec.ErrFlagsNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ring.Submit(tt.sqe)
			if tt.want == nil {
				if err != nil {
					t.Fatalf("Submit(%+v) error = %v", tt.sqe, err)
				}
				return
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("Submit(%+v) error = %v, want %v", tt.sqe, err, tt.want)
			}
		})
	}
}

func TestRegistrationAllowlistAfterEnable(t *testing.T) {
	ring := restrictedRing(t)

	err := ring.RegisterFileTable([]int{3, 4})
	if !errors.Is(err, netsec.ErrRegisterOpNotAllowed) {
		t.Fatalf("RegisterFileTable() error = %v, want ErrRegisterOpNotAllowed", err)
	}
	if err := ring.Register(netsec.IORING_REGISTER_FILES_UPDATE); err != nil {
		t.Fatalf("Register(IORING_REGISTER_FILES_UPDATE) error = %v", err)
	}
}

func TestAllowedFileTableRegistration(t *testing.T) {
	ring := netsec.NewRing()
	if err := ring.RegisterRestrictions(netsec.Restrictions{
		AllowedRegisterOps: []netsec.RegisterOpcode{netsec.IORING_REGISTER_FILES},
		AllowedSQEOps:      []netsec.Opcode{netsec.IORING_OP_READ},
	}); err != nil {
		t.Fatalf("RegisterRestrictions() error = %v", err)
	}
	if err := ring.Enable(); err != nil {
		t.Fatalf("Enable() error = %v", err)
	}
	if err := ring.RegisterFileTable([]int{3, 4}); err != nil {
		t.Fatalf("RegisterFileTable() error = %v", err)
	}
}

func TestInvalidFlagPolicyIsRejected(t *testing.T) {
	ring := netsec.NewRing()
	err := ring.RegisterRestrictions(netsec.Restrictions{
		AllowedFlags:  netsec.IOSQE_ASYNC,
		RequiredFlags: netsec.IOSQE_FIXED_FILE,
	})
	if !errors.Is(err, netsec.ErrInvalidRestrictions) {
		t.Fatalf("RegisterRestrictions() error = %v, want ErrInvalidRestrictions", err)
	}
	if !errors.Is(err, netsec.ErrRequiredFlagsNotAllowed) {
		t.Fatalf("RegisterRestrictions() error = %v, want ErrRequiredFlagsNotAllowed", err)
	}
	if ring.RestrictionsRegistered() {
		t.Fatal("invalid restrictions were installed")
	}
}

func TestRestrictionsAreCopiedAtRegistration(t *testing.T) {
	ring := netsec.NewRing()
	sqeOps := []netsec.Opcode{netsec.IORING_OP_READ}
	if err := ring.RegisterRestrictions(netsec.Restrictions{AllowedSQEOps: sqeOps}); err != nil {
		t.Fatalf("RegisterRestrictions() error = %v", err)
	}
	if err := ring.Enable(); err != nil {
		t.Fatalf("Enable() error = %v", err)
	}

	sqeOps[0] = netsec.IORING_OP_WRITE
	if err := ring.Submit(netsec.SQE{Opcode: netsec.IORING_OP_WRITE}); !errors.Is(err, netsec.ErrOpcodeNotAllowed) {
		t.Fatalf("Submit() after mutating input error = %v, want ErrOpcodeNotAllowed", err)
	}
}

func TestConcurrentRestrictionRegistrationSucceedsOnce(t *testing.T) {
	ring := netsec.NewRing()
	var wg sync.WaitGroup
	errs := make(chan error, 16)

	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- ring.RegisterRestrictions(netsec.Restrictions{
				AllowedSQEOps: []netsec.Opcode{netsec.IORING_OP_READ},
			})
		}()
	}
	wg.Wait()
	close(errs)

	var accepted, rejected int
	for err := range errs {
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, netsec.ErrRestrictionsAlreadyRegistered):
			rejected++
		default:
			t.Fatalf("RegisterRestrictions() error = %v", err)
		}
	}
	if accepted != 1 || rejected != 15 {
		t.Fatalf("accepted = %d, rejected = %d; want 1 and 15", accepted, rejected)
	}
}
