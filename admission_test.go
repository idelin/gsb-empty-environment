package netsec

import (
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

var (
	testAI0 = ObjectID{Type: ObjectAnalogInput, Instance: 0}
	testAI1 = ObjectID{Type: ObjectAnalogInput, Instance: 1}
	testAO0 = ObjectID{Type: ObjectAnalogOutput, Instance: 0}
)

func newTestPolicy(t *testing.T, opts ...Option) *Policy {
	t.Helper()
	policy, err := NewPolicy([]Rule{
		{Object: testAI0, Property: PropertyPresentValue},
		{Object: testAI1, Property: PropertyPresentValue},
		{Object: testAO0, Property: PropertyPresentValue},
	}, opts...)
	if err != nil {
		t.Fatalf("NewPolicy: %v", err)
	}
	return policy
}

func TestWritePropertyAllowsRegisteredTargetAndPriority(t *testing.T) {
	policy := newTestPolicy(t)

	for _, priority := range []uint8{3, 8, 16} {
		req := NewWritePropertyRequest(testAI0, PropertyPresentValue, priority, float32(21.5))
		if err := policy.Admit(req); err != nil {
			t.Errorf("priority %d: expected admission, got %v", priority, err)
		}
	}
}

func TestWritePropertyRejectsUnregisteredTargets(t *testing.T) {
	policy := newTestPolicy(t)

	tests := []struct {
		name     string
		write    Write
		expected error
	}{
		{
			name: "unregistered object",
			write: Write{
				Object:   ObjectID{Type: ObjectAnalogValue, Instance: 7},
				Property: PropertyPresentValue,
				Priority: 8,
			},
			expected: ErrObjectNotAllowed,
		},
		{
			name: "registered object with unregistered property",
			write: Write{
				Object:   testAI0,
				Property: PropertyPriorityArray,
				Priority: 8,
			},
			expected: ErrPropertyNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := policy.Admit(Request{
				Service: ServiceWriteProperty,
				Writes:  []Write{tt.write},
			})
			if !errors.Is(err, tt.expected) {
				t.Fatalf("expected %v, got %v", tt.expected, err)
			}
			if !errors.Is(err, ErrTargetNotAllowed) || !errors.Is(err, ErrDenied) {
				t.Fatalf("denial should be attributable to target policy: %v", err)
			}
		})
	}
}

func TestWritePropertyRejectsUnauthorizedPriorities(t *testing.T) {
	policy := newTestPolicy(t)

	tests := []struct {
		name     string
		priority uint8
		expected error
	}{
		{name: "missing priority", priority: 0, expected: ErrPriorityRequired},
		{name: "life safety priority 1", priority: 1, expected: ErrPriorityReserved},
		{name: "life safety priority 2", priority: 2, expected: ErrPriorityReserved},
		{name: "above BACnet maximum", priority: 17, expected: ErrPriorityOutOfRange},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := policy.AdmitWriteProperty(testAI0, PropertyPresentValue, tt.priority)
			if !errors.Is(err, tt.expected) {
				t.Fatalf("expected %v, got %v", tt.expected, err)
			}
			if !errors.Is(err, ErrPriorityOutOfRange) ||
				!errors.Is(err, ErrPriorityDenied) ||
				!errors.Is(err, ErrDenied) {
				t.Fatalf("denial should be attributable to priority policy: %v", err)
			}
		})
	}
}

func TestConfiguredPriorityRangeCannotExposeReservedPriorities(t *testing.T) {
	policy := newTestPolicy(t, WithPriorityRange(8, 16))

	if err := policy.AdmitWriteProperty(testAI0, PropertyPresentValue, 7); !errors.Is(err, ErrPriorityOutOfRange) {
		t.Fatalf("priority 7 should be outside 8..16, got %v", err)
	}
	if err := policy.AdmitWriteProperty(testAI0, PropertyPresentValue, 8); err != nil {
		t.Fatalf("priority 8 should be admitted: %v", err)
	}
	if err := policy.AdmitWriteProperty(testAI0, PropertyPresentValue, 16); err != nil {
		t.Fatalf("priority 16 should be admitted: %v", err)
	}

	minPriority, maxPriority := policy.PriorityRange()
	if minPriority != 8 || maxPriority != 16 {
		t.Fatalf("PriorityRange() = %d..%d, want 8..16", minPriority, maxPriority)
	}
}

func TestInvalidPriorityRangesAreRejected(t *testing.T) {
	tests := []struct {
		name string
		min  uint8
		max  uint8
	}{
		{name: "priority 1 included", min: 1, max: 16},
		{name: "priority 2 included", min: 2, max: 16},
		{name: "zero included", min: 0, max: 16},
		{name: "above BACnet maximum", min: 3, max: 17},
		{name: "inverted interval", min: 9, max: 8},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy, err := NewPolicy(nil, WithPriorityRange(tt.min, tt.max))
			if !errors.Is(err, ErrInvalidPriorityRange) {
				t.Fatalf("expected ErrInvalidPriorityRange, got policy=%v err=%v", policy, err)
			}
			if policy != nil {
				t.Fatal("invalid option should not return a policy")
			}
		})
	}
}

func TestSingleLevelPriorityRangeIsValid(t *testing.T) {
	policy, err := NewPolicy(nil, WithPriorityRange(16, 16))
	if err != nil {
		t.Fatalf("single-level interval should be valid: %v", err)
	}
	if policy == nil {
		t.Fatal("valid interval returned a nil policy")
	}
}

func TestControlServicesAreDeniedByDefault(t *testing.T) {
	policy := newTestPolicy(t)

	requests := []Request{
		{
			Service: ServiceDeviceCommunicationControl,
			Writes: []Write{{
				Object:   testAI0,
				Property: PropertyPresentValue,
				Priority: 8,
			}},
		},
		{
			Service: ServiceReinitializeDevice,
			Writes: []Write{{
				Object:   testAI0,
				Property: PropertyPresentValue,
				Priority: 8,
			}},
		},
	}

	for _, req := range requests {
		t.Run(req.Service.String(), func(t *testing.T) {
			err := policy.Admit(req)
			if !errors.Is(err, ErrControlServiceDenied) {
				t.Fatalf("expected control-service denial, got %v", err)
			}
			if !errors.Is(err, ErrServiceDenied) || !errors.Is(err, ErrDenied) {
				t.Fatalf("control denial should be attributable to service policy: %v", err)
			}
		})
	}
}

func TestUnknownServiceIsDenied(t *testing.T) {
	policy := newTestPolicy(t)
	err := policy.Admit(Request{Service: Service(99)})
	if !errors.Is(err, ErrServiceDenied) {
		t.Fatalf("expected ErrServiceDenied, got %v", err)
	}
}

func TestWritePropertyCannotCarryABatch(t *testing.T) {
	policy := newTestPolicy(t)

	err := policy.Admit(Request{
		Service: ServiceWriteProperty,
		Writes: []Write{
			{Object: testAI0, Property: PropertyPresentValue, Priority: 8},
			{Object: testAI1, Property: PropertyPresentValue, Priority: 8},
		},
	})
	if !errors.Is(err, ErrMalformedRequest) {
		t.Fatalf("expected malformed-request denial, got %v", err)
	}
}

func TestWritePropertyMultipleRejectsBatchSmuggling(t *testing.T) {
	policy := newTestPolicy(t)

	tests := []struct {
		name     string
		smuggled Write
		badIndex int
		expected error
	}{
		{
			name: "second entry targets unregistered object",
			smuggled: Write{
				Object:   ObjectID{Type: ObjectAnalogValue, Instance: 7},
				Property: PropertyPresentValue,
				Priority: 8,
			},
			badIndex: 1,
			expected: ErrObjectNotAllowed,
		},
		{
			name: "second entry targets unregistered property",
			smuggled: Write{
				Object:   testAI1,
				Property: PropertyPriorityArray,
				Priority: 8,
			},
			badIndex: 1,
			expected: ErrPropertyNotAllowed,
		},
		{
			name: "second entry uses reserved priority",
			smuggled: Write{
				Object:   testAO0,
				Property: PropertyPresentValue,
				Priority: 1,
			},
			badIndex: 1,
			expected: ErrPriorityReserved,
		},
		{
			name: "second entry uses out-of-range priority",
			smuggled: Write{
				Object:   testAO0,
				Property: PropertyPresentValue,
				Priority: 17,
			},
			badIndex: 1,
			expected: ErrPriorityOutOfRange,
		},
		{
			name: "second entry is structurally invalid",
			smuggled: Write{
				Object:   ObjectID{Type: 1024, Instance: 0},
				Property: PropertyPresentValue,
				Priority: 8,
			},
			badIndex: 1,
			expected: ErrMalformedRequest,
		},
		{
			name: "first entry targets unregistered object",
			smuggled: Write{
				Object:   ObjectID{Type: ObjectAnalogValue, Instance: 7},
				Property: PropertyPresentValue,
				Priority: 8,
			},
			badIndex: 0,
			expected: ErrObjectNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allowed := Write{Object: testAI0, Property: PropertyPresentValue, Priority: 8}
			writes := []Write{allowed, tt.smuggled}
			if tt.badIndex == 0 {
				writes = []Write{tt.smuggled, allowed}
			}
			req := NewWritePropertyMultipleRequest(writes...)
			err := policy.Admit(req)
			if !errors.Is(err, tt.expected) {
				t.Fatalf("expected whole batch to fail with %v, got %v", tt.expected, err)
			}
			if !strings.Contains(err.Error(), "entry "+strconv.Itoa(tt.badIndex)) {
				t.Fatalf("error should identify the violating entry, got %v", err)
			}
		})
	}
}

func TestWritePropertyMultipleAllowsOnlyWhenEveryEntryIsRegistered(t *testing.T) {
	policy := newTestPolicy(t)

	err := policy.AdmitWritePropertyMultiple(
		Write{Object: testAI0, Property: PropertyPresentValue, Priority: 3},
		Write{Object: testAI1, Property: PropertyPresentValue, Priority: 16},
		Write{Object: testAO0, Property: PropertyPresentValue, Priority: 8},
	)
	if err != nil {
		t.Fatalf("all-registered batch should be admitted: %v", err)
	}
}

func TestEmptyWritePropertyMultipleIsMalformed(t *testing.T) {
	policy := newTestPolicy(t)
	for _, req := range []Request{
		{Service: ServiceWritePropertyMultiple},
		NewWritePropertyMultipleRequest(),
	} {
		if err := policy.Admit(req); !errors.Is(err, ErrMalformedRequest) {
			t.Fatalf("expected ErrMalformedRequest, got %v", err)
		}
	}
}

func TestInvalidRegistrationRulesAreRejected(t *testing.T) {
	tests := []struct {
		name string
		rule Rule
	}{
		{
			name: "object type too wide",
			rule: Rule{
				Object:   ObjectID{Type: 1024, Instance: 0},
				Property: PropertyPresentValue,
			},
		},
		{
			name: "object instance too wide",
			rule: Rule{
				Object:   ObjectID{Type: ObjectAnalogInput, Instance: 0x400000},
				Property: PropertyPresentValue,
			},
		},
		{
			name: "property identifier too wide",
			rule: Rule{
				Object:   testAI0,
				Property: PropertyID(0x400000),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy, err := NewPolicy([]Rule{tt.rule})
			if !errors.Is(err, ErrInvalidRule) {
				t.Fatalf("expected ErrInvalidRule, got policy=%v err=%v", policy, err)
			}
		})
	}
}

func TestPolicyCopiesRegistrationInput(t *testing.T) {
	rules := []Rule{{Object: testAI0, Property: PropertyPresentValue}}
	policy, err := NewPolicy(rules)
	if err != nil {
		t.Fatalf("NewPolicy: %v", err)
	}

	rules[0] = Rule{Object: testAI1, Property: PropertyPriorityArray}

	if !policy.Allows(testAI0, PropertyPresentValue) {
		t.Fatal("policy changed after caller mutated the input slice")
	}
	if policy.Allows(testAI1, PropertyPriorityArray) {
		t.Fatal("policy unexpectedly picked up a mutated rule")
	}
}

func TestNilPolicyDeniesEverything(t *testing.T) {
	var policy *Policy

	if err := policy.Admit(NewWritePropertyRequest(testAI0, PropertyPresentValue, 8, nil)); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("expected ErrNotConfigured, got %v", err)
	}
	if policy.Allows(testAI0, PropertyPresentValue) {
		t.Fatal("nil policy should not allow targets")
	}
	if policy.RuleCount() != 0 {
		t.Fatal("nil policy should have no rules")
	}
}

func TestPolicyIsSafeForConcurrentAdmission(t *testing.T) {
	policy := newTestPolicy(t)
	allowed := NewWritePropertyRequest(testAI0, PropertyPresentValue, 8, nil)
	denied := NewWritePropertyRequest(testAI0, PropertyPriorityArray, 8, nil)

	var failures atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if err := policy.Admit(allowed); err != nil {
					failures.Add(1)
				}
				if err := policy.Admit(denied); !errors.Is(err, ErrPropertyNotAllowed) {
					failures.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	if got := failures.Load(); got != 0 {
		t.Fatalf("concurrent admission produced %d unexpected results", got)
	}
}
