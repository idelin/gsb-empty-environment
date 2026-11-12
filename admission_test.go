package netsec

import (
	"testing"
)

func labels(values ...uint32) []Label {
	out := make([]Label, len(values))
	for i, value := range values {
		out[i] = Label{
			Value:         value,
			BottomOfStack: i == len(values)-1,
			TTL:           64,
		}
	}
	return out
}

func testPolicy() Policy {
	return Policy{
		MaxDepth: 4,
		VPNLabels: map[uint32]string{
			100: "vpn-blue",
			200: "vpn-red",
		},
	}
}

func TestDirectVPNLabelFromCustomerIsDropped(t *testing.T) {
	t.Parallel()
	client := NewUntrustedInterface("ce-1", 0x1234)
	a := NewAdmitter(testPolicy())

	for _, label := range []uint32{100, 200} {
		got := a.Admit(client, labels(label))
		if got.Allowed {
			t.Fatalf("VPN label %d from customer interface was admitted", label)
		}
		if got.Reason != ReasonVPNLabelUntrusted {
			t.Fatalf("VPN label %d reason = %q, want %q", label, got.Reason, ReasonVPNLabelUntrusted)
		}
		if got.VRF != "" {
			t.Fatalf("VPN label %d unexpectedly resolved VRF %q", label, got.VRF)
		}
	}
}

func TestVPNLabelInsidePermittedBackboneStackIsDropped(t *testing.T) {
	t.Parallel()
	client := NewUntrustedInterface("ce-1", 0x1234)
	a := NewAdmitter(testPolicy())

	// The attacker puts an allowed backbone label on top to get past a check
	// that only looks at the outermost label, then smuggles a VPN label below.
	got := a.Admit(client, labels(0x1234, 100))
	if got.Allowed {
		t.Fatal("stack with a VPN label below a permitted backbone label was admitted")
	}
	if got.Reason != ReasonVPNLabelUntrusted {
		t.Fatalf("reason = %q, want %q", got.Reason, ReasonVPNLabelUntrusted)
	}
}

func TestVPNLabelCannotBeHiddenInBackboneAllowList(t *testing.T) {
	t.Parallel()
	client := NewUntrustedInterface("ce-1", 100)
	a := NewAdmitter(testPolicy())

	got := a.Admit(client, labels(100))
	if got.Allowed || got.Reason != ReasonVPNLabelUntrusted {
		t.Fatalf("configured VPN label was accepted as a backbone label: %v", got)
	}
}

func TestReservedLabelsDeniedByDefault(t *testing.T) {
	t.Parallel()
	a := NewAdmitter(testPolicy())
	cases := []struct {
		name  string
		iface Interface
	}{
		{"untrusted", NewUntrustedInterface("ce-1", 0x1234)},
		{"trusted", NewTrustedInterface("core-1")},
	}

	for _, tc := range cases {
		for label := ReservedLabelMin; label <= ReservedLabelMax; label++ {
			got := a.Admit(tc.iface, labels(label))
			if got.Allowed {
				t.Fatalf("%s interface admitted reserved label %d", tc.name, label)
			}
			if got.Reason != ReasonReservedLabelDenied {
				t.Fatalf("%s interface reserved label %d reason = %q", tc.name, label, got.Reason)
			}
		}
	}
}

func TestExplicitNullOnlyOnListedInterfaces(t *testing.T) {
	t.Parallel()
	a := NewAdmitter(testPolicy())

	permitted := NewUntrustedInterface("ce-explicit", 0x1234).AllowReserved(ExplicitNullLabel)
	got := a.Admit(permitted, labels(ExplicitNullLabel))
	if !got.Allowed {
		t.Fatalf("explicitly permitted explicit-null label was dropped: %v", got)
	}

	denied := NewUntrustedInterface("ce-other", 0x1234)
	got = a.Admit(denied, labels(ExplicitNullLabel))
	if got.Allowed || got.Reason != ReasonReservedLabelDenied {
		t.Fatalf("explicit-null label was not denied on unlisted interface: %v", got)
	}

	policy := testPolicy()
	policy.ExplicitNullInterfaces = []string{"ce-policy-listed"}
	a = NewAdmitter(policy)
	got = a.Admit(NewUntrustedInterface("ce-policy-listed", 0x1234), labels(0))
	if !got.Allowed {
		t.Fatalf("policy-listed explicit-null interface was denied: %v", got)
	}
}

func TestExplicitNullMustBeBottomLabel(t *testing.T) {
	t.Parallel()
	policy := testPolicy()
	policy.ExplicitNullInterfaces = []string{"core-1"}
	a := NewAdmitter(policy)
	core := NewTrustedInterface("core-1")

	stack := []Label{
		{Value: ExplicitNullLabel, BottomOfStack: false, TTL: 64},
		{Value: 0x1234, BottomOfStack: true, TTL: 64},
	}
	got := a.Admit(core, stack)
	if got.Allowed || got.Reason != ReasonExplicitNullNotBottom {
		t.Fatalf("non-bottom explicit-null label was not rejected: %v", got)
	}
}

func TestReservedLabelIsNeverTreatedAsVPNLabel(t *testing.T) {
	t.Parallel()
	policy := testPolicy()
	policy.VPNLabels[ExplicitNullLabel] = "misconfigured-vrf"
	a := NewAdmitter(policy)

	permitted := NewUntrustedInterface("ce-explicit", 0x1234).AllowReserved(ExplicitNullLabel)
	got := a.Admit(permitted, labels(ExplicitNullLabel))
	if !got.Allowed || got.VRF != "" {
		t.Fatalf("reserved label was treated as a VPN label: %v", got)
	}
}

func TestBottomOfStackMustMatchStackDepth(t *testing.T) {
	t.Parallel()
	core := NewTrustedInterface("core-1")
	a := NewAdmitter(testPolicy())

	cases := []struct {
		name  string
		stack []Label
		want  bool
	}{
		{
			name: "single label set",
			stack: []Label{
				{Value: 0x1234, BottomOfStack: true, TTL: 64},
			},
			want: true,
		},
		{
			name: "single label clear",
			stack: []Label{
				{Value: 0x1234, BottomOfStack: false, TTL: 64},
			},
			want: false,
		},
		{
			name: "outer label marked bottom",
			stack: []Label{
				{Value: 0x1234, BottomOfStack: true, TTL: 64},
				{Value: 100, BottomOfStack: true, TTL: 64},
			},
			want: false,
		},
		{
			name: "inner label not marked bottom",
			stack: []Label{
				{Value: 0x1234, BottomOfStack: false, TTL: 64},
				{Value: 100, BottomOfStack: false, TTL: 64},
			},
			want: false,
		},
		{
			name: "correct two-label stack",
			stack: []Label{
				{Value: 0x1234, BottomOfStack: false, TTL: 64},
				{Value: 100, BottomOfStack: true, TTL: 64},
			},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := a.Admit(core, tc.stack)
			if got.Allowed != tc.want {
				t.Fatalf("allowed = %v, want %v (%v)", got.Allowed, tc.want, got)
			}
			if !tc.want && got.Reason != ReasonBottomOfStackMismatch {
				t.Fatalf("reason = %q, want %q", got.Reason, ReasonBottomOfStackMismatch)
			}
		})
	}
}

func TestStackDepthLimit(t *testing.T) {
	t.Parallel()
	policy := testPolicy()
	policy.MaxDepth = 3
	a := NewAdmitter(policy)
	core := NewTrustedInterface("core-1")

	got := a.Admit(core, labels(0x10, 0x11, 0x12))
	if !got.Allowed {
		t.Fatalf("stack at the depth limit was rejected: %v", got)
	}

	got = a.Admit(core, labels(0x10, 0x11, 0x12, 0x13))
	if got.Allowed || got.Reason != ReasonStackTooDeep {
		t.Fatalf("oversized stack was not rejected: %v", got)
	}
}

func TestDefaultStackDepthIsBounded(t *testing.T) {
	t.Parallel()
	a := NewAdmitter(Policy{VPNLabels: map[uint32]string{}})
	core := NewTrustedInterface("core-1")

	got := a.Admit(core, labels(0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17))
	if !got.Allowed {
		t.Fatalf("stack at default depth was rejected: %v", got)
	}
	got = a.Admit(core, labels(0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18))
	if got.Allowed || got.Reason != ReasonStackTooDeep {
		t.Fatalf("stack above default depth was not rejected: %v", got)
	}
}

func TestZeroTTLIsRejected(t *testing.T) {
	t.Parallel()
	a := NewAdmitter(testPolicy())
	core := NewTrustedInterface("core-1")

	topExpired := []Label{
		{Value: 0x1234, BottomOfStack: false, TTL: 0},
		{Value: 100, BottomOfStack: true, TTL: 64},
	}
	got := a.Admit(core, topExpired)
	if got.Allowed || got.Reason != ReasonTTLZero {
		t.Fatalf("top label with TTL zero was not rejected: %v", got)
	}

	innerExpired := []Label{
		{Value: 0x1234, BottomOfStack: false, TTL: 64},
		{Value: 100, BottomOfStack: true, TTL: 0},
	}
	got = a.Admit(core, innerExpired)
	if got.Allowed || got.Reason != ReasonTTLZero {
		t.Fatalf("inner label with TTL zero was not rejected: %v", got)
	}
}

func TestLegitimateBackboneTrafficOnCustomerInterface(t *testing.T) {
	t.Parallel()
	client := NewUntrustedInterface("ce-1", 0x1234, 0x4321)
	a := NewAdmitter(testPolicy())

	for _, stack := range [][]Label{
		labels(0x1234),
		labels(0x1234, 0x4321),
	} {
		got := a.Admit(client, stack)
		if !got.Allowed {
			t.Fatalf("legitimate backbone stack was rejected: %v", got)
		}
		if got.VRF != "" {
			t.Fatalf("untrusted backbone traffic resolved a VRF: %q", got.VRF)
		}
	}

	got := a.Admit(client, labels(0x9999))
	if got.Allowed || got.Reason != ReasonUntrustedLabel {
		t.Fatalf("unlisted label on customer interface was not rejected: %v", got)
	}
}

func TestVPNLabelsResolveOnlyOnTrustedInterfaces(t *testing.T) {
	t.Parallel()
	core := NewTrustedInterface("core-1")
	a := NewAdmitter(testPolicy())

	got := a.Admit(core, labels(100))
	if !got.Allowed || got.VRF != "vpn-blue" {
		t.Fatalf("trusted VPN label did not resolve: %v", got)
	}

	got = a.Admit(core, labels(0x1234, 200))
	if !got.Allowed || got.VRF != "vpn-red" {
		t.Fatalf("trusted stacked VPN label did not resolve: %v", got)
	}

	got = a.Admit(core, labels(0x1234))
	if !got.Allowed || got.VRF != "" {
		t.Fatalf("plain trusted backbone traffic was not admitted: %v", got)
	}
}

func TestConflictingVPNLabelsAreRejected(t *testing.T) {
	t.Parallel()
	a := NewAdmitter(testPolicy())
	core := NewTrustedInterface("core-1")

	got := a.Admit(core, labels(100, 200))
	if got.Allowed || got.Reason != ReasonConflictingVPNLabels {
		t.Fatalf("stack resolving to two VRFs was not rejected: %v", got)
	}
}

func TestMalformedAndEmptyStacks(t *testing.T) {
	t.Parallel()
	a := NewAdmitter(testPolicy())
	core := NewTrustedInterface("core-1")

	got := a.Admit(core, []Label{{Value: MaxLabelValue + 1, BottomOfStack: true, TTL: 64}})
	if got.Allowed || got.Reason != ReasonMalformedLabel {
		t.Fatalf("out-of-range label was not rejected: %v", got)
	}

	got = a.Admit(core, nil)
	if !got.Allowed || got.VRF != "" {
		t.Fatalf("unlabeled packet should be admitted unchanged: %v", got)
	}
}

func TestPolicyIsSnapshotted(t *testing.T) {
	t.Parallel()
	vpn := map[uint32]string{100: "vpn-blue"}
	a := NewAdmitter(Policy{MaxDepth: 4, VPNLabels: vpn})

	vpn[999] = "attacker-vrf"
	core := NewTrustedInterface("core-1")
	got := a.Admit(core, labels(999))
	if !got.Allowed || got.VRF != "" {
		t.Fatalf("mutation after NewAdmitter changed admission: %v", got)
	}
}

func TestParseBackboneLabels(t *testing.T) {
	t.Parallel()
	got, err := ParseBackboneLabels(" 100, 0x1234, 200 ")
	if err != nil {
		t.Fatalf("ParseBackboneLabels: %v", err)
	}
	want := []uint32{100, 0x1234, 200}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
