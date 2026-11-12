package netsec_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"example.org/netsec/q0253"
)

func mustPolicy(t *testing.T, cfg netsec.Config) *netsec.Policy {
	t.Helper()
	policy, err := netsec.NewPolicy(cfg)
	if err != nil {
		t.Fatalf("NewPolicy: %v", err)
	}
	return policy
}

func target(objectType netsec.ObjectType, instance uint32, property netsec.PropertyID) netsec.Target {
	return netsec.Target{
		Object:   netsec.ObjectID{Type: objectType, Instance: instance},
		Property: property,
	}
}

func TestWritePropertyAllowAndUnregistered(t *testing.T) {
	registered := target(netsec.ObjectTypeAnalogValue, 1, netsec.PropertyPresentValue)
	policy := mustPolicy(t, netsec.Config{AllowedTargets: []netsec.Target{registered}})

	allowed := netsec.Request{
		Service:  netsec.ServiceWriteProperty,
		Object:   registered.Object,
		Property: registered.Property,
		Priority: 8,
	}
	if decision := policy.Admit(allowed); !decision.Allowed {
		t.Fatalf("registered target denied: %+v", decision)
	}

	cases := []struct {
		name string
		req  netsec.Request
	}{
		{
			name: "different instance",
			req: netsec.Request{
				Service:  netsec.ServiceWriteProperty,
				Object:   netsec.ObjectID{Type: netsec.ObjectTypeAnalogValue, Instance: 2},
				Property: netsec.PropertyPresentValue,
				Priority: 8,
			},
		},
		{
			name: "different property",
			req: netsec.Request{
				Service:  netsec.ServiceWriteProperty,
				Object:   registered.Object,
				Property: netsec.PropertyPriorityArray,
				Priority: 8,
			},
		},
		{
			name: "different object type",
			req: netsec.Request{
				Service:  netsec.ServiceWriteProperty,
				Object:   netsec.ObjectID{Type: netsec.ObjectTypeAnalogInput, Instance: 1},
				Property: netsec.PropertyPresentValue,
				Priority: 8,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision := policy.Admit(tc.req)
			if decision.Allowed || decision.Reason != netsec.ReasonUnregisteredTarget {
				t.Fatalf("expected unregistered denial, got %+v", decision)
			}
		})
	}
}

func TestPriorityReservedAndOutOfRange(t *testing.T) {
	registered := target(netsec.ObjectTypeAnalogValue, 1, netsec.PropertyPresentValue)
	policy := mustPolicy(t, netsec.Config{
		AllowedTargets: []netsec.Target{registered},
		MinPriority:    5,
		MaxPriority:    10,
	})

	for _, priority := range []uint8{1, 2} {
		decision := policy.Admit(netsec.Request{
			Service:  netsec.ServiceWriteProperty,
			Object:   registered.Object,
			Property: registered.Property,
			Priority: priority,
		})
		if decision.Allowed || decision.Reason != netsec.ReasonPriorityReserved {
			t.Fatalf("priority %d: expected reserved denial, got %+v", priority, decision)
		}
	}

	for _, priority := range []uint8{0, 4, 11, 17, 255} {
		decision := policy.Admit(netsec.Request{
			Service:  netsec.ServiceWriteProperty,
			Object:   registered.Object,
			Property: registered.Property,
			Priority: priority,
		})
		if decision.Allowed {
			t.Fatalf("priority %d: expected denial", priority)
		}
		if priority == 0 && decision.Reason != netsec.ReasonMissingPriority {
			t.Fatalf("priority 0: expected missing-priority denial, got %+v", decision)
		}
		if priority != 0 && decision.Reason != netsec.ReasonPriorityOutOfRange {
			t.Fatalf("priority %d: expected out-of-range denial, got %+v", priority, decision)
		}
	}

	for _, priority := range []uint8{5, 10} {
		if decision := policy.Admit(netsec.Request{
			Service:  netsec.ServiceWriteProperty,
			Object:   registered.Object,
			Property: registered.Property,
			Priority: priority,
		}); !decision.Allowed {
			t.Fatalf("priority %d should be allowed: %+v", priority, decision)
		}
	}
}

func TestWritePropertyMultipleIsAllOrNothing(t *testing.T) {
	presentValue := target(netsec.ObjectTypeAnalogValue, 1, netsec.PropertyPresentValue)
	policy := mustPolicy(t, netsec.Config{AllowedTargets: []netsec.Target{presentValue}})

	allowedBatch := netsec.Request{
		Service: netsec.ServiceWritePropertyMultiple,
		Writes: []netsec.WriteAccess{
			{Object: presentValue.Object, Property: presentValue.Property, Priority: 8},
			{Object: presentValue.Object, Property: presentValue.Property, Priority: 12},
		},
	}
	if decision := policy.Admit(allowedBatch); !decision.Allowed {
		t.Fatalf("allowed batch denied: %+v", decision)
	}

	cases := []struct {
		name       string
		badMember  int
		writes     []netsec.WriteAccess
		wantReason netsec.Reason
	}{
		{
			name:      "unregistered property smuggled after valid write",
			badMember: 1,
			writes: []netsec.WriteAccess{
				{Object: presentValue.Object, Property: presentValue.Property, Priority: 8},
				{Object: presentValue.Object, Property: netsec.PropertyPriorityArray, Priority: 8},
			},
			wantReason: netsec.ReasonUnregisteredTarget,
		},
		{
			name:      "unregistered object smuggled after valid write",
			badMember: 2,
			writes: []netsec.WriteAccess{
				{Object: presentValue.Object, Property: presentValue.Property, Priority: 8},
				{Object: presentValue.Object, Property: presentValue.Property, Priority: 9},
				{Object: netsec.ObjectID{Type: netsec.ObjectTypeAnalogValue, Instance: 2}, Property: presentValue.Property, Priority: 8},
			},
			wantReason: netsec.ReasonUnregisteredTarget,
		},
		{
			name:      "reserved priority smuggled in batch",
			badMember: 1,
			writes: []netsec.WriteAccess{
				{Object: presentValue.Object, Property: presentValue.Property, Priority: 6},
				{Object: presentValue.Object, Property: presentValue.Property, Priority: 1},
			},
			wantReason: netsec.ReasonPriorityReserved,
		},
		{
			name:      "missing priority smuggled in batch",
			badMember: 0,
			writes: []netsec.WriteAccess{
				{Object: presentValue.Object, Property: presentValue.Property, Priority: 0},
				{Object: presentValue.Object, Property: presentValue.Property, Priority: 8},
			},
			wantReason: netsec.ReasonMissingPriority,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision := policy.Admit(netsec.Request{
				Service: netsec.ServiceWritePropertyMultiple,
				Writes:  tc.writes,
			})
			if decision.Allowed {
				t.Fatalf("batch with violation was allowed")
			}
			if decision.Reason != tc.wantReason {
				t.Fatalf("reason = %s, want %s (%+v)", decision.Reason, tc.wantReason, decision)
			}
			if decision.Member != tc.badMember {
				t.Fatalf("member = %d, want %d", decision.Member, tc.badMember)
			}
			if len(decision.Violations) == 0 {
				t.Fatalf("denial did not report violations: %+v", decision)
			}
		})
	}

	if decision := policy.Admit(netsec.Request{Service: netsec.ServiceWritePropertyMultiple}); decision.Allowed {
		t.Fatalf("empty WritePropertyMultiple was allowed")
	} else if decision.Reason != netsec.ReasonEmptyWriteList {
		t.Fatalf("empty batch reason = %s", decision.Reason)
	}
}

func TestControlServicesDeniedByDefault(t *testing.T) {
	registered := target(netsec.ObjectTypeAnalogValue, 1, netsec.PropertyPresentValue)
	policy := mustPolicy(t, netsec.Config{AllowedTargets: []netsec.Target{registered}})

	for _, service := range []netsec.Service{
		netsec.ServiceDeviceCommunicationControl,
		netsec.ServiceReinitializeDevice,
		netsec.ServiceLifeSafetyOperation,
	} {
		decision := policy.Admit(netsec.Request{Service: service})
		if decision.Allowed || decision.Reason != netsec.ReasonControlServiceDenied {
			t.Fatalf("%s: expected control-service denial, got %+v", service, decision)
		}
	}

	if decision := policy.Admit(netsec.Request{Service: netsec.ServiceReadProperty}); decision.Allowed {
		t.Fatalf("read service should not be writable through this policy")
	}
	if decision := policy.Admit(netsec.Request{Service: netsec.Service(200)}); decision.Allowed {
		t.Fatalf("unknown service was allowed")
	}
}

func TestControlServiceExplicitOptIn(t *testing.T) {
	policy := mustPolicy(t, netsec.Config{})
	if err := policy.AllowControlService(netsec.ServiceReinitializeDevice); err != nil {
		t.Fatalf("AllowControlService: %v", err)
	}
	if err := policy.AllowControlService(netsec.ServiceWriteProperty); err == nil {
		t.Fatalf("non-control service should not be accepted as a control opt-in")
	}
	if decision := policy.Admit(netsec.Request{Service: netsec.ServiceReinitializeDevice}); !decision.Allowed {
		t.Fatalf("explicit control opt-in was ignored: %+v", decision)
	}
	policy.DenyControlService(netsec.ServiceReinitializeDevice)
	if decision := policy.Admit(netsec.Request{Service: netsec.ServiceReinitializeDevice}); decision.Allowed {
		t.Fatalf("DenyControlService did not restore denial")
	}
}

func TestInvalidPolicyConfigurationFailsClosed(t *testing.T) {
	cases := []struct {
		name string
		cfg  netsec.Config
		want error
	}{
		{
			name: "reserved minimum",
			cfg:  netsec.Config{MinPriority: 2},
			want: netsec.ErrReservedPriority,
		},
		{
			name: "maximum above BACnet limit",
			cfg:  netsec.Config{MaxPriority: 17},
			want: netsec.ErrInvalidPolicy,
		},
		{
			name: "inverted range",
			cfg:  netsec.Config{MinPriority: 12, MaxPriority: 5},
			want: netsec.ErrInvalidPolicy,
		},
		{
			name: "invalid object type",
			cfg: netsec.Config{AllowedTargets: []netsec.Target{{
				Object:   netsec.ObjectID{Type: 1024, Instance: 1},
				Property: netsec.PropertyPresentValue,
			}}},
			want: netsec.ErrInvalidTarget,
		},
		{
			name: "invalid instance",
			cfg: netsec.Config{AllowedTargets: []netsec.Target{{
				Object:   netsec.ObjectID{Type: netsec.ObjectTypeAnalogValue, Instance: 0x400000},
				Property: netsec.PropertyPresentValue,
			}}},
			want: netsec.ErrInvalidTarget,
		},
		{
			name: "non-control service opt-in",
			cfg:  netsec.Config{AllowedControlServices: []netsec.Service{netsec.ServiceWriteProperty}},
			want: netsec.ErrInvalidPolicy,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := netsec.NewPolicy(tc.cfg)
			if err == nil {
				t.Fatalf("NewPolicy accepted invalid configuration")
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want errors.Is %v", err, tc.want)
			}
		})
	}
}

func TestRegistrationAndRemoval(t *testing.T) {
	policy := mustPolicy(t, netsec.Config{})
	presentValue := target(netsec.ObjectTypeAnalogValue, 1, netsec.PropertyPresentValue)
	if policy.IsRegistered(presentValue) {
		t.Fatalf("empty policy already has a registered target")
	}
	if err := policy.Register(presentValue); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if !policy.IsRegistered(presentValue) {
		t.Fatalf("target was not registered")
	}
	if err := policy.Register(netsec.Target{
		Object:   netsec.ObjectID{Type: 1024},
		Property: netsec.PropertyPresentValue,
	}); err == nil {
		t.Fatalf("Register accepted an invalid target")
	}
	if policy.IsRegistered(presentValue) == false {
		t.Fatalf("failed batch registration removed an existing target")
	}
	policy.Unregister(presentValue)
	if policy.IsRegistered(presentValue) {
		t.Fatalf("target was not removed")
	}
}

func TestDecisionError(t *testing.T) {
	policy := mustPolicy(t, netsec.Config{})
	err := policy.Authorize(netsec.Request{
		Service:  netsec.ServiceWriteProperty,
		Object:   netsec.ObjectID{Type: netsec.ObjectTypeAnalogValue, Instance: 1},
		Property: netsec.PropertyPresentValue,
		Priority: 8,
	})
	if err == nil {
		t.Fatalf("Authorize allowed an unregistered write")
	}
	if !errors.Is(err, &netsec.DeniedError{Decision: netsec.Decision{Reason: netsec.ReasonUnregisteredTarget}}) {
		t.Fatalf("errors.Is could not match denial reason: %v", err)
	}
}

func TestConcurrentAdmitAndRegister(t *testing.T) {
	policy := mustPolicy(t, netsec.Config{})
	var group sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		group.Add(1)
		go func(worker int) {
			defer group.Done()
			id := netsec.ObjectID{Type: netsec.ObjectTypeAnalogValue, Instance: uint32(worker)}
			registered := netsec.Target{Object: id, Property: netsec.PropertyPresentValue}
			if err := policy.Register(registered); err != nil {
				t.Errorf("Register: %v", err)
				return
			}
			for i := 0; i < 100; i++ {
				decision := policy.Admit(netsec.Request{
					Service:  netsec.ServiceWriteProperty,
					Object:   id,
					Property: netsec.PropertyPresentValue,
					Priority: 8,
				})
				if !decision.Allowed {
					t.Errorf("concurrent admitted write denied: %+v", decision)
					return
				}
			}
		}(worker)
	}
	group.Wait()
}

func TestNilPolicyFailsClosed(t *testing.T) {
	var policy *netsec.Policy
	if decision := policy.Admit(netsec.Request{Service: netsec.ServiceWriteProperty}); decision.Allowed {
		t.Fatalf("nil policy allowed a request")
	}
	if err := policy.Register(netsec.Target{}); err == nil {
		t.Fatalf("nil policy registration succeeded")
	}
}

func ExamplePolicy() {
	policy, _ := netsec.NewPolicy(netsec.Config{
		AllowedTargets: []netsec.Target{{
			Object:   netsec.ObjectID{Type: netsec.ObjectTypeAnalogValue, Instance: 1},
			Property: netsec.PropertyPresentValue,
		}},
	})

	decision := policy.Admit(netsec.Request{
		Service:  netsec.ServiceWriteProperty,
		Object:   netsec.ObjectID{Type: netsec.ObjectTypeAnalogValue, Instance: 1},
		Property: netsec.PropertyPresentValue,
		Priority: 8,
	})
	fmt.Printf("allowed=%v\n", decision.Allowed)

	decision = policy.Admit(netsec.Request{
		Service:  netsec.ServiceWriteProperty,
		Object:   netsec.ObjectID{Type: netsec.ObjectTypeAnalogValue, Instance: 1},
		Property: netsec.PropertyPresentValue,
		Priority: 1,
	})
	fmt.Printf("allowed=%v reason=%s\n", decision.Allowed, decision.Reason)
	// Output:
	// allowed=true
	// allowed=false reason=priority_reserved
}
