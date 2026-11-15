package netsec

import (
	"errors"
	"sync"
	"testing"
)

var (
	ao1 = ObjectID{Type: ObjectAnalogOutput, Instance: 1}
	ao2 = ObjectID{Type: ObjectAnalogOutput, Instance: 2}
	bv7 = ObjectID{Type: ObjectBinaryValue, Instance: 7}
)

func testGuard(t *testing.T) *Guard {
	t.Helper()
	guard := NewGuard()
	// scada may write present-value on ao1/ao2 at operator band 8..10.
	err := guard.Register("scada", Policy{
		MinPriority: 8,
		MaxPriority: 10,
		Objects: map[ObjectID]map[uint32]struct{}{
			ao1: AllowProperties(PropPresentValue),
			ao2: AllowProperties(PropPresentValue, 111), // 111 = description
		},
	})
	if err != nil {
		t.Fatalf("register scada: %v", err)
	}
	return guard
}

func TestAdmitWriteAllowed(t *testing.T) {
	guard := testGuard(t)

	for _, priority := range []uint8{8, 9, 10} {
		err := guard.AdmitWrite(WriteRequest{
			Caller: "scada", Object: ao1, Property: PropPresentValue, Priority: priority,
		})
		if err != nil {
			t.Errorf("priority %d: expected admit, got %v", priority, err)
		}
	}

	// No priority level supplied is acceptable when object and property
	// are both registered.
	if err := guard.AdmitWrite(WriteRequest{
		Caller: "scada", Object: ao2, Property: 111, Priority: PriorityUnassigned,
	}); err != nil {
		t.Errorf("unassigned priority: expected admit, got %v", err)
	}
}

func TestAdmitWriteUnknownCaller(t *testing.T) {
	guard := testGuard(t)
	err := guard.AdmitWrite(WriteRequest{
		Caller: "intruder", Object: ao1, Property: PropPresentValue, Priority: 8,
	})
	if !errors.Is(err, ErrUnknownCaller) {
		t.Fatalf("expected ErrUnknownCaller, got %v", err)
	}
}

func TestAdmitWriteObjectNotRegistered(t *testing.T) {
	guard := testGuard(t)
	err := guard.AdmitWrite(WriteRequest{
		Caller: "scada", Object: bv7, Property: PropPresentValue, Priority: 8,
	})
	if !errors.Is(err, ErrObjectNotAllowed) {
		t.Fatalf("expected ErrObjectNotAllowed, got %v", err)
	}
}

func TestAdmitWritePropertyNotRegistered(t *testing.T) {
	guard := testGuard(t)
	err := guard.AdmitWrite(WriteRequest{
		Caller: "scada", Object: ao1, Property: 111, Priority: 8,
	})
	if !errors.Is(err, ErrPropertyNotAllowed) {
		t.Fatalf("expected ErrPropertyNotAllowed, got %v", err)
	}
}

// 越权优先级：攻击者试图用保留的 1/2 级或超出本调用方许可区间的级别写值。
func TestAdmitWritePriorityEscalation(t *testing.T) {
	guard := testGuard(t)

	cases := []struct {
		name     string
		priority uint8
		want     error
	}{
		{"reserved level 1 (manual life-safety)", 1, ErrReservedPriority},
		{"reserved level 2 (slaved life-safety)", 2, ErrReservedPriority},
		{"band floor minus one", 7, ErrPriorityOutOfRange},
		{"band ceiling plus one", 11, ErrPriorityOutOfRange},
		{"non-existent level 17", 17, ErrPriorityOutOfRange},
		{"non-existent level 255", 255, ErrPriorityOutOfRange},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := guard.AdmitWrite(WriteRequest{
				Caller: "scada", Object: ao1, Property: PropPresentValue, Priority: tc.priority,
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("priority %d: expected %v, got %v", tc.priority, tc.want, err)
			}
		})
	}
}

func TestAdmitWriteInvalidObjectID(t *testing.T) {
	guard := testGuard(t)
	bad := ObjectID{Type: ObjectAnalogOutput, Instance: ObjectInstanceMax + 1}
	err := guard.AdmitWrite(WriteRequest{
		Caller: "scada", Object: bad, Property: PropPresentValue, Priority: 8,
	})
	if !errors.Is(err, ErrInvalidObjectID) {
		t.Fatalf("expected ErrInvalidObjectID, got %v", err)
	}
}

func TestRegisterRejectsReservedBand(t *testing.T) {
	guard := NewGuard()
	cases := []struct {
		name string
		min  uint8
		max  uint8
	}{
		{"band starts at 1", 1, 5},
		{"band starts at 2", 2, 5},
		{"band ends at 2", 2, 2},
		{"inverted band", 10, 8},
		{"band above 16", 12, 17},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := guard.Register("evil", Policy{
				MinPriority: tc.min,
				MaxPriority: tc.max,
				Objects:     map[ObjectID]map[uint32]struct{}{ao1: AllowProperties(PropPresentValue)},
			})
			if !errors.Is(err, ErrInvalidPolicy) {
				t.Fatalf("expected ErrInvalidPolicy, got %v", err)
			}
		})
	}

	if err := guard.Register("zero", Policy{}); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("zero-value policy: expected ErrInvalidPolicy, got %v", err)
	}
	if err := guard.Register("", Policy{MinPriority: 3, MaxPriority: 8}); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("empty caller: expected ErrInvalidPolicy, got %v", err)
	}
}

func TestRegisterClonesPolicy(t *testing.T) {
	guard := NewGuard()
	policy := Policy{
		MinPriority: 8,
		MaxPriority: 10,
		Objects:     map[ObjectID]map[uint32]struct{}{ao1: AllowProperties(PropPresentValue)},
	}
	if err := guard.Register("scada", policy); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Mutating the caller's maps after registration must not widen the
	// policy held by the guard.
	policy.Objects[ao1][111] = struct{}{}
	policy.Objects[bv7] = AllowProperties(PropPresentValue)

	err := guard.AdmitWrite(WriteRequest{
		Caller: "scada", Object: ao1, Property: 111, Priority: 8,
	})
	if !errors.Is(err, ErrPropertyNotAllowed) {
		t.Fatalf("post-register mutation leaked into policy: got %v", err)
	}
}

// 批量夹带：WritePropertyMultiple 中只要有一项越权，整条请求都必须被拒绝。
func TestAdmitWriteMultipleSmuggling(t *testing.T) {
	guard := testGuard(t)

	req := WriteMultipleRequest{
		Caller: "scada",
		Items: []WriteItem{
			{Object: ao1, Property: PropPresentValue, Priority: 9},  // legitimate
			{Object: ao2, Property: PropPresentValue, Priority: 1},  // smuggled reserved level
			{Object: bv7, Property: PropPresentValue, Priority: 9},  // unregistered object
			{Object: ao1, Property: 111, Priority: 9},               // unregistered property
			{Object: ao2, Property: PropPresentValue, Priority: 11}, // above band
		},
	}
	err := guard.AdmitWriteMultiple(req)
	if err == nil {
		t.Fatal("expected whole batch rejected, got nil")
	}

	var violation *BatchViolation
	if !errors.As(err, &violation) {
		t.Fatalf("expected *BatchViolation, got %T: %v", err, err)
	}

	// The first smuggled item determines the wrapped sentinel.
	if !errors.Is(err, ErrReservedPriority) {
		t.Fatalf("expected wrap of ErrReservedPriority, got %v", err)
	}

	got := map[int]error{}
	for _, v := range violation.Violations {
		got[v.Index] = v.Err
	}
	want := map[int]error{
		1: ErrReservedPriority,
		2: ErrObjectNotAllowed,
		3: ErrPropertyNotAllowed,
		4: ErrPriorityOutOfRange,
	}
	if len(violation.Violations) != len(want) {
		t.Fatalf("expected %d violations, got %d: %+v", len(want), len(violation.Violations), violation.Violations)
	}
	for index, wantErr := range want {
		if !errors.Is(got[index], wantErr) {
			t.Errorf("item %d: expected %v, got %v", index, wantErr, got[index])
		}
	}
}

func TestAdmitWriteMultipleAllAllowed(t *testing.T) {
	guard := testGuard(t)
	err := guard.AdmitWriteMultiple(WriteMultipleRequest{
		Caller: "scada",
		Items: []WriteItem{
			{Object: ao1, Property: PropPresentValue, Priority: 8},
			{Object: ao2, Property: 111, Priority: 10},
			{Object: ao2, Property: PropPresentValue, Priority: PriorityUnassigned},
		},
	})
	if err != nil {
		t.Fatalf("expected whole batch admitted, got %v", err)
	}
}

func TestAdmitWriteMultipleEmpty(t *testing.T) {
	guard := testGuard(t)
	if err := guard.AdmitWriteMultiple(WriteMultipleRequest{Caller: "scada"}); !errors.Is(err, ErrEmptyWriteMultiple) {
		t.Fatalf("expected ErrEmptyWriteMultiple, got %v", err)
	}
}

func TestAdmitWriteMultipleUnknownCaller(t *testing.T) {
	guard := testGuard(t)
	err := guard.AdmitWriteMultiple(WriteMultipleRequest{
		Caller: "nobody",
		Items:  []WriteItem{{Object: ao1, Property: PropPresentValue, Priority: 8}},
	})
	if !errors.Is(err, ErrUnknownCaller) {
		t.Fatalf("expected ErrUnknownCaller, got %v", err)
	}
}

// 控制类服务默认拒绝：DeviceCommunicationControl / ReinitializeDevice
// 必须显式授权，否则一律拒绝。
func TestAdmitControlDeniedByDefault(t *testing.T) {
	guard := testGuard(t)

	for _, service := range []Service{ServiceDeviceCommunicationControl, ServiceReinitializeDevice} {
		err := guard.AdmitControl(ControlRequest{Caller: "scada", Service: service})
		if !errors.Is(err, ErrControlDenied) {
			t.Errorf("service %d: expected ErrControlDenied, got %v", service, err)
		}
	}

	err := guard.AdmitControl(ControlRequest{Caller: "intruder", Service: ServiceReinitializeDevice})
	if !errors.Is(err, ErrUnknownCaller) {
		t.Fatalf("unknown caller: expected ErrUnknownCaller, got %v", err)
	}

	err = guard.AdmitControl(ControlRequest{Caller: "scada", Service: ServiceWriteProperty})
	if !errors.Is(err, ErrUnsupportedService) {
		t.Fatalf("expected ErrUnsupportedService, got %v", err)
	}
}

func TestAdmitControlExplicitlyAllowed(t *testing.T) {
	guard := NewGuard()
	err := guard.Register("maint", Policy{
		MinPriority:                     3,
		MaxPriority:                     16,
		Objects:                         map[ObjectID]map[uint32]struct{}{ao1: AllowProperties(PropPresentValue)},
		AllowDeviceCommunicationControl: true,
	})
	if err != nil {
		t.Fatalf("register maint: %v", err)
	}

	if err := guard.AdmitControl(ControlRequest{
		Caller: "maint", Service: ServiceDeviceCommunicationControl,
	}); err != nil {
		t.Fatalf("DCC should be allowed after opt-in: %v", err)
	}

	// ReinitializeDevice remains denied: each service is gated separately.
	if err := guard.AdmitControl(ControlRequest{
		Caller: "maint", Service: ServiceReinitializeDevice,
	}); !errors.Is(err, ErrControlDenied) {
		t.Fatalf("expected ReinitializeDevice still denied, got %v", err)
	}
}

func TestRemoveCaller(t *testing.T) {
	guard := testGuard(t)
	guard.Remove("scada")
	err := guard.AdmitWrite(WriteRequest{
		Caller: "scada", Object: ao1, Property: PropPresentValue, Priority: 8,
	})
	if !errors.Is(err, ErrUnknownCaller) {
		t.Fatalf("expected ErrUnknownCaller after remove, got %v", err)
	}
}

func TestConcurrentAdmissionAndRegistration(t *testing.T) {
	guard := NewGuard()
	policy := Policy{
		MinPriority: 8,
		MaxPriority: 10,
		Objects:     map[ObjectID]map[uint32]struct{}{ao1: AllowProperties(PropPresentValue)},
	}
	if err := guard.Register("scada", policy); err != nil {
		t.Fatalf("register: %v", err)
	}

	var wg sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for iteration := 0; iteration < 200; iteration++ {
				switch worker % 3 {
				case 0:
					_ = guard.AdmitWrite(WriteRequest{
						Caller: "scada", Object: ao1, Property: PropPresentValue, Priority: 8,
					})
				case 1:
					_ = guard.AdmitWrite(WriteRequest{
						Caller: "scada", Object: ao1, Property: PropPresentValue, Priority: 1,
					})
				default:
					_ = guard.Register("scada", policy)
				}
			}
		}(worker)
	}
	wg.Wait()
}
