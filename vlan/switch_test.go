package vlan

import (
	"bytes"
	"errors"
	"testing"
)

func testPolicy(t *testing.T) *Policy {
	t.Helper()
	p, err := NewPolicy([]PortPolicy{
		{Name: "acc1", Mode: ModeAccess, AccessVID: 10},
		{Name: "acc2", Mode: ModeAccess, AccessVID: 20},
		{
			Name:      "trk",
			Mode:      ModeTrunk,
			NativeVID: 99,
			Allowed:   []uint16{10, 20, 100},
		},
		{Name: "tun", Mode: ModeTunnel, AccessVID: 500},
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestIngressAccess(t *testing.T) {
	sw := NewSwitch(testPolicy(t))
	payload := []byte{0x08, 0x00, 0x01, 0x02}

	t.Run("untagged gets access VID", func(t *testing.T) {
		raw := buildFrame(t, nil, payload)
		out, tenant, err := sw.Ingress("acc1", raw)
		if err != nil {
			t.Fatal(err)
		}
		want := buildFrame(t, []Tag{tag(EtherTypeVLAN, 0, false, 10)}, payload)
		if !bytes.Equal(out, want) {
			t.Fatalf("canonical = %x, want %x", out, want)
		}
		if tenant != (Tenant{InnerVID: 10}) {
			t.Fatalf("tenant = %v", tenant)
		}
	})

	t.Run("priority tag keeps PCP and gains VID", func(t *testing.T) {
		raw := buildFrame(t, []Tag{tag(EtherTypeVLAN, 4, true, 0)}, payload)
		out, _, err := sw.Ingress("acc1", raw)
		if err != nil {
			t.Fatal(err)
		}
		want := buildFrame(t, []Tag{tag(EtherTypeVLAN, 4, true, 10)}, payload)
		if !bytes.Equal(out, want) {
			t.Fatalf("canonical = %x, want %x", out, want)
		}
	})

	t.Run("tagged frame rejected", func(t *testing.T) {
		raw := buildFrame(t, []Tag{tag(EtherTypeVLAN, 0, false, 10)}, payload)
		if _, _, err := sw.Ingress("acc1", raw); !errors.Is(err, ErrIngressDenied) {
			t.Fatalf("err = %v, want ErrIngressDenied", err)
		}
	})
}

func TestEgressAccess(t *testing.T) {
	sw := NewSwitch(testPolicy(t))
	payload := []byte{0x08, 0x00, 0x01}

	t.Run("strips tag on egress", func(t *testing.T) {
		canonical := buildFrame(t, []Tag{tag(EtherTypeVLAN, 0, false, 10)}, payload)
		out, err := sw.Egress("acc1", Tenant{InnerVID: 10}, canonical)
		if err != nil {
			t.Fatal(err)
		}
		if want := buildFrame(t, nil, payload); !bytes.Equal(out, want) {
			t.Fatalf("egress = %x, want %x", out, want)
		}
	})

	t.Run("PCP survives as priority tag", func(t *testing.T) {
		canonical := buildFrame(t, []Tag{tag(EtherTypeVLAN, 4, false, 10)}, payload)
		out, err := sw.Egress("acc1", Tenant{InnerVID: 10}, canonical)
		if err != nil {
			t.Fatal(err)
		}
		want := buildFrame(t, []Tag{tag(EtherTypeVLAN, 4, false, 0)}, payload)
		if !bytes.Equal(out, want) {
			t.Fatalf("egress = %x, want %x", out, want)
		}
	})

	t.Run("foreign VID cannot leave via this access port", func(t *testing.T) {
		canonical := buildFrame(t, []Tag{tag(EtherTypeVLAN, 0, false, 20)}, payload)
		if _, err := sw.Egress("acc1", Tenant{InnerVID: 20}, canonical); !errors.Is(err, ErrEgressDenied) {
			t.Fatalf("err = %v, want ErrEgressDenied", err)
		}
	})
}

func TestTrunkIngressEgress(t *testing.T) {
	sw := NewSwitch(testPolicy(t))
	payload := []byte{0x08, 0x00, 0x09}

	t.Run("untagged native frame", func(t *testing.T) {
		raw := buildFrame(t, nil, payload)
		out, tenant, err := sw.Ingress("trk", raw)
		if err != nil {
			t.Fatal(err)
		}
		if tenant != (Tenant{InnerVID: 99}) {
			t.Fatalf("tenant = %v", tenant)
		}
		back, err := sw.Egress("trk", tenant, out)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(back, raw) {
			t.Fatalf("native roundtrip changed frame:\n got %x\nwant %x", back, raw)
		}
	})

	t.Run("allowed single tag passes transparently", func(t *testing.T) {
		raw := buildFrame(t, []Tag{tag(EtherTypeVLAN, 1, false, 100)}, payload)
		out, _, err := sw.Ingress("trk", raw)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(out, raw) {
			t.Fatal("allowed tagged frame was rewritten")
		}
	})

	t.Run("disallowed outer VID dropped", func(t *testing.T) {
		raw := buildFrame(t, []Tag{tag(EtherTypeVLAN, 0, false, 777)}, payload)
		if _, _, err := sw.Ingress("trk", raw); !errors.Is(err, ErrIngressDenied) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("double tag with allowed outer passes", func(t *testing.T) {
		raw := buildFrame(t, []Tag{
			tag(EtherTypeVLAN, 0, false, 100),
			tag(EtherTypeVLAN, 0, false, 55),
		}, payload)
		out, tenant, err := sw.Ingress("trk", raw)
		if err != nil {
			t.Fatal(err)
		}
		if tenant != (Tenant{OuterVID: 100, InnerVID: 55}) {
			t.Fatalf("tenant = %v", tenant)
		}
		if !bytes.Equal(out, raw) {
			t.Fatal("double-tagged frame was modified")
		}
	})

	t.Run("double tag with disallowed outer dropped", func(t *testing.T) {
		raw := buildFrame(t, []Tag{
			tag(EtherTypeVLAN, 0, false, 777),
			tag(EtherTypeVLAN, 0, false, 55),
		}, payload)
		if _, _, err := sw.Ingress("trk", raw); !errors.Is(err, ErrIngressDenied) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestTunnelPushAndPop(t *testing.T) {
	sw := NewSwitch(testPolicy(t))
	payload := []byte{0x86, 0xDD, 0x11}

	cases := []struct {
		name string
		tags []Tag
	}{
		{"untagged customer", nil},
		{"priority customer", []Tag{tag(EtherTypeVLAN, 6, false, 0)}},
		{"single tagged customer", []Tag{tag(EtherTypeVLAN, 2, false, 33)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := buildFrame(t, tc.tags, payload)
			out, tenant, err := sw.Ingress("tun", raw)
			if err != nil {
				t.Fatal(err)
			}
			f, err := ParseFrame(out)
			if err != nil {
				t.Fatal(err)
			}
			if f.Tags[0].VID != 500 || f.Tags[0].TPID != EtherTypeQinQ {
				t.Fatalf("service push wrong: %+v", f.Tags)
			}
			if len(f.Tags) != len(tc.tags)+1 {
				t.Fatalf("stack depth = %d, want %d", len(f.Tags), len(tc.tags)+1)
			}
			back, err := sw.Egress("tun", tenant, out)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(back, raw) {
				t.Fatalf("pop did not restore original:\n got %x\nwant %x", back, raw)
			}
		})
	}

	t.Run("double tagged customer rejected to keep stack bounded", func(t *testing.T) {
		raw := buildFrame(t, []Tag{
			tag(EtherTypeVLAN, 0, false, 9),
			tag(EtherTypeVLAN, 0, false, 8),
		}, payload)
		if _, _, err := sw.Ingress("tun", raw); !errors.Is(err, ErrIngressDenied) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestTenantMismatchDropped(t *testing.T) {
	sw := NewSwitch(testPolicy(t))
	canonical := buildFrame(t, []Tag{tag(EtherTypeVLAN, 0, false, 10)}, []byte{0x08, 0x00})

	if _, err := sw.Egress("acc1", Tenant{InnerVID: 20}, canonical); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("err = %v, want ErrTenantMismatch", err)
	}
}

func TestUnknownPortAndTruncation(t *testing.T) {
	sw := NewSwitch(testPolicy(t))
	raw := buildFrame(t, nil, []byte{0x08, 0x00})
	if _, _, err := sw.Ingress("nope", raw); !errors.Is(err, ErrUnknownPort) {
		t.Fatalf("ingress err = %v", err)
	}
	if _, err := sw.Egress("nope", Tenant{}, raw); !errors.Is(err, ErrUnknownPort) {
		t.Fatalf("egress err = %v", err)
	}
	if _, _, err := sw.Ingress("acc1", raw[:10]); !errors.Is(err, ErrTruncated) {
		t.Fatalf("truncation err = %v", err)
	}
}

func TestPolicyValidation(t *testing.T) {
	bad := []struct {
		name string
		port PortPolicy
	}{
		{"access vid zero", PortPolicy{Name: "p", Mode: ModeAccess}},
		{"access vid too large", PortPolicy{Name: "p", Mode: ModeAccess, AccessVID: 4096}},
		{"trunk bad allowed", PortPolicy{Name: "p", Mode: ModeTrunk, Allowed: []uint16{0}}},
		{"trunk duplicate allowed", PortPolicy{Name: "p", Mode: ModeTrunk, Allowed: []uint16{5, 5}}},
		{"trunk native collides", PortPolicy{Name: "p", Mode: ModeTrunk, NativeVID: 5, Allowed: []uint16{5}}},
		{"tunnel bad service vid", PortPolicy{Name: "p", Mode: ModeTunnel, AccessVID: 9000}},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewPolicy([]PortPolicy{tc.port}); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	if _, err := NewPolicy(nil); err != nil {
		t.Fatalf("empty policy should be valid: %v", err)
	}
	if _, err := NewPolicy([]PortPolicy{
		{Name: "p", Mode: ModeAccess, AccessVID: 1},
		{Name: "p", Mode: ModeAccess, AccessVID: 2},
	}); err == nil {
		t.Fatal("duplicate port name should fail")
	}
}
