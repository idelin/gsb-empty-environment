package netsec

import (
	"net"
	"reflect"
	"testing"
)

var (
	hostOne = MustParseMAC("02:00:00:00:00:01")
	hostTwo = MustParseMAC("02:00:00:00:00:02")
	hostThr = MustParseMAC("02:00:00:00:00:03")
)

func TestSingleHostRestrictUsesOnlyFirstMAC(t *testing.T) {
	port := NewPort(SingleHost, Restrict)

	if got := port.Evaluate(hostOne); got.Allowed || got.Action != ActionDrop {
		t.Fatalf("before authentication: %+v", got)
	}
	if !port.Authenticate(hostOne) {
		t.Fatal("first MAC was not accepted")
	}
	if got := port.Evaluate(hostOne); !got.Allowed || got.Action != ActionForward {
		t.Fatalf("authorized MAC: %+v", got)
	}
	if port.Authenticate(hostTwo) {
		t.Fatal("second MAC authenticated in single-host mode")
	}

	got := port.Evaluate(hostTwo)
	if got.Allowed || got.Action != ActionDrop || got.Reason != ReasonForeignSource {
		t.Fatalf("second MAC decision = %+v", got)
	}
	if port.Disabled() {
		t.Fatal("restrict disabled the port")
	}
	if got := port.Evaluate(hostOne); !got.Allowed {
		t.Fatalf("restrict affected primary MAC: %+v", got)
	}
}

func TestSingleHostShutdownDisablesPort(t *testing.T) {
	port := NewPort(SingleHost, Shutdown)

	if !port.Authenticate(hostOne) {
		t.Fatal("first MAC was not accepted")
	}
	got := port.Evaluate(hostTwo)
	if got.Allowed || got.Action != ActionShutdown || got.Reason != ReasonForeignSource {
		t.Fatalf("violation decision = %+v", got)
	}
	if !port.Disabled() {
		t.Fatal("violation did not disable the port")
	}

	if got := port.Evaluate(hostOne); got.Allowed || got.Reason != ReasonPortDisabled {
		t.Fatalf("disabled primary decision = %+v", got)
	}
	if port.Authenticate(hostOne) {
		t.Fatal("disabled port accepted authentication")
	}

	port.Reset()
	if port.Disabled() || port.Authorized() {
		t.Fatal("Reset did not return the port to unauthorized and enabled")
	}
	if !port.Authenticate(hostThr) {
		t.Fatal("reset port did not accept a new host")
	}
	if got := port.Evaluate(hostThr); !got.Allowed {
		t.Fatalf("new host after Reset: %+v", got)
	}
}

func TestSingleHostLogoffAndAgingRequireReauthentication(t *testing.T) {
	tests := []struct {
		name   string
		remove func(*Port, MAC) bool
	}{
		{"logoff", (*Port).Logoff},
		{"aging", (*Port).AgeOut},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			port := NewPort(SingleHost, Restrict)
			if !port.Authenticate(hostOne) {
				t.Fatal("first MAC was not accepted")
			}
			if !tc.remove(port, hostOne) {
				t.Fatal("primary removal reported no state change")
			}
			if got := port.Evaluate(hostOne); got.Allowed || got.Reason != ReasonUnauthenticated {
				t.Fatalf("removed host decision = %+v", got)
			}
			if !port.Authenticate(hostTwo) {
				t.Fatal("port did not accept a replacement host")
			}
			if got := port.Evaluate(hostTwo); !got.Allowed {
				t.Fatalf("replacement host decision = %+v", got)
			}
		})
	}
}

func TestMultiAuthAuthenticatesEachMAC(t *testing.T) {
	port := NewPort(MultiAuth, Restrict)
	if port.Mode() != MultiAuth || port.ViolationAction() != Restrict {
		t.Fatalf("port configuration = %s/%s", port.Mode(), port.ViolationAction())
	}

	if got := port.Evaluate(hostOne); got.Allowed {
		t.Fatalf("unauthenticated host forwarded: %+v", got)
	}
	if !port.Authenticate(hostOne) || !port.Authenticate(hostTwo) {
		t.Fatal("multi-auth did not accept hosts independently")
	}
	for _, mac := range []MAC{hostOne, hostTwo} {
		if got := port.Evaluate(mac); !got.Allowed || got.Action != ActionForward {
			t.Fatalf("%s not forwarded: %+v", mac, got)
		}
	}
	if got := port.Evaluate(hostThr); got.Allowed || got.Action != ActionDrop {
		t.Fatalf("unauthenticated third host: %+v", got)
	}

	if !port.Logoff(hostOne) {
		t.Fatal("logoff did not remove first host")
	}
	if got := port.Evaluate(hostOne); got.Allowed {
		t.Fatalf("logged-off host still forwarded: %+v", got)
	}
	if got := port.Evaluate(hostTwo); !got.Allowed {
		t.Fatalf("logoff of one host affected another: %+v", got)
	}
	if primary, ok := port.Primary(); !ok || primary != hostTwo {
		t.Fatalf("Primary() after first logoff = %v, %v; want %v, true", primary, ok, hostTwo)
	}
	if !port.AgeOut(hostTwo) {
		t.Fatal("aging did not remove second host")
	}
	if got := port.Evaluate(hostTwo); got.Allowed {
		t.Fatalf("aged host still forwarded: %+v", got)
	}
	if !port.Authenticate(hostThr) {
		t.Fatal("multi-auth did not accept a host after all records expired")
	}
	if !port.Expire(hostThr) {
		t.Fatal("Expire alias did not remove the last host")
	}
	if port.Authorized() {
		t.Fatal("port still authorized after last record expired")
	}
}

func TestMultiHostOneAuthForwardsAllAndPrimaryRemovalClosesPort(t *testing.T) {
	port := NewPort(MultiHost, Shutdown)

	if got := port.Evaluate(hostTwo); got.Allowed {
		t.Fatalf("unauthorized multi-host forwarded: %+v", got)
	}
	if !port.Authenticate(hostOne) {
		t.Fatal("primary authentication failed")
	}
	for _, mac := range []MAC{hostOne, hostTwo, hostThr} {
		if got := port.Evaluate(mac); !got.Allowed || got.Action != ActionForward {
			t.Fatalf("%s not forwarded in multi-host mode: %+v", mac, got)
		}
	}

	if port.Logoff(hostTwo) {
		t.Fatal("non-primary logoff changed port state")
	}
	if got := port.Evaluate(hostTwo); !got.Allowed {
		t.Fatalf("non-primary logoff closed the port: %+v", got)
	}
	if !port.Logoff(hostOne) {
		t.Fatal("primary logoff did not change state")
	}
	for _, mac := range []MAC{hostOne, hostTwo, hostThr} {
		if got := port.Evaluate(mac); got.Allowed || got.Reason != ReasonUnauthenticated {
			t.Fatalf("%s forwarded after primary logoff: %+v", mac, got)
		}
	}

	if !port.Authenticate(hostTwo) {
		t.Fatal("port did not accept new primary")
	}
	if primary, _ := port.Primary(); primary != hostTwo {
		t.Fatalf("primary = %s, want %s", primary, hostTwo)
	}
	if got := port.Evaluate(hostThr); !got.Allowed {
		t.Fatalf("new primary did not reopen the port: %+v", got)
	}

	if !port.AgeOut(hostTwo) {
		t.Fatal("aging the primary did not change state")
	}
	if got := port.Evaluate(hostOne); got.Allowed {
		t.Fatalf("aging the primary left the port open: %+v", got)
	}
}

func TestAuthorizedMACsAndPrimary(t *testing.T) {
	port := NewPort(MultiAuth, Restrict)
	port.Authenticate(hostTwo)
	port.Authenticate(hostOne)
	if got, want := port.AuthorizedMACs(), []MAC{hostOne, hostTwo}; !reflect.DeepEqual(got, want) {
		t.Fatalf("AuthorizedMACs() = %v, want %v", got, want)
	}

	port.Logoff(hostTwo)
	if got, want := port.AuthorizedMACs(), []MAC{hostOne}; !reflect.DeepEqual(got, want) {
		t.Fatalf("AuthorizedMACs() after logoff = %v, want %v", got, want)
	}
	if primary, ok := port.Primary(); !ok || primary != hostOne {
		t.Fatalf("Primary() = %v, %v; want %v, true", primary, ok, hostOne)
	}
}

func TestMACParsingAndModeStrings(t *testing.T) {
	mac, err := ParseMAC("02-00-00-00-00-01")
	if err != nil {
		t.Fatal(err)
	}
	if mac != hostOne || mac.String() != "02:00:00:00:00:01" {
		t.Fatalf("parsed MAC = %s", mac)
	}
	if mac.HardwareAddr().String() != mac.String() {
		t.Fatalf("HardwareAddr() = %s", mac.HardwareAddr())
	}
	converted, err := MACFromHardwareAddr(mac.HardwareAddr())
	if err != nil {
		t.Fatal(err)
	}
	if converted != mac {
		t.Fatalf("MACFromHardwareAddr() = %s, want %s", converted, mac)
	}
	if _, err := ParseMAC("02:00:00:00:00"); err == nil {
		t.Fatal("accepted a five-byte MAC")
	}
	if _, err := MACFromHardwareAddr(net.HardwareAddr{0x02, 0x00}); err == nil {
		t.Fatal("converted a two-byte hardware address")
	}
	for mode, want := range map[Mode]string{
		SingleHost: "single-host",
		MultiAuth:  "multi-auth",
		MultiHost:  "multi-host",
	} {
		if got := mode.String(); got != want {
			t.Fatalf("%d.String() = %q, want %q", mode, got, want)
		}
	}
	for action, want := range map[ViolationAction]string{
		Restrict: "restrict",
		Shutdown: "shutdown",
	} {
		if got := action.String(); got != want {
			t.Fatalf("%d.String() = %q, want %q", action, got, want)
		}
	}
}
