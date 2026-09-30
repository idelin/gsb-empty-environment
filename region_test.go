package main

import (
	"net/netip"
	"reflect"
	"testing"
)

func pfx(s string) netip.Prefix { return netip.MustParsePrefix(s).Masked() }

func TestSubtractPrefixDisjoint(t *testing.T) {
	got := subtractPrefix(pfx("10.0.0.0/8"), pfx("11.0.0.0/8"))
	if len(got) != 1 || got[0] != pfx("10.0.0.0/8") {
		t.Fatalf("不相交时应保持原样，得到 %v", got)
	}
}

func TestSubtractPrefixCovered(t *testing.T) {
	if got := subtractPrefix(pfx("10.1.0.0/16"), pfx("10.0.0.0/8")); len(got) != 0 {
		t.Fatalf("被完全覆盖时应为空，得到 %v", got)
	}
}

func TestSubtractPrefixSplit(t *testing.T) {
	got := subtractPrefix(pfx("10.0.0.0/8"), pfx("10.0.0.0/9"))
	want := []netip.Prefix{pfx("10.128.0.0/9")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("分裂错误：got %v want %v", got, want)
	}
}

func TestSubtractPrefixDeep(t *testing.T) {
	// 从 10.0.0.0/8 中挖去 10.0.1.0/24，应得到 9 个前缀且互不重叠、并集正确。
	got := subtractPrefix(pfx("10.0.0.0/8"), pfx("10.0.1.0/24"))
	if len(got) != 16 {
		t.Fatalf("期望 16 个剩余前缀，得到 %d: %v", len(got), got)
	}
	for _, p := range got {
		if p.Overlaps(pfx("10.0.1.0/24")) {
			t.Fatalf("剩余前缀 %v 与被减前缀重叠", p)
		}
	}
	for i := range got {
		for j := i + 1; j < len(got); j++ {
			if got[i].Overlaps(got[j]) {
				t.Fatalf("剩余前缀互相重叠: %v 与 %v", got[i], got[j])
			}
		}
	}
}

func TestSubtractPrefixV6(t *testing.T) {
	got := subtractPrefix(pfx("2001:db8::/32"), pfx("2001:db8::/33"))
	want := []netip.Prefix{pfx("2001:db8:8000::/33")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("v6 分裂错误：got %v want %v", got, want)
	}
}

func TestSubtractPorts(t *testing.T) {
	cases := []struct {
		a, b, want []portInterval
	}{
		{[]portInterval{{0, 65535}}, []portInterval{{80, 80}}, []portInterval{{0, 79}, {81, 65535}}},
		{[]portInterval{{10, 20}}, []portInterval{{0, 65535}}, nil},
		{[]portInterval{{10, 20}}, []portInterval{{30, 40}}, []portInterval{{10, 20}}},
		{[]portInterval{{0, 100}}, []portInterval{{0, 50}}, []portInterval{{51, 100}}},
		{[]portInterval{{0, 65535}}, []portInterval{{0, 1023}, {1024, 65535}}, nil},
	}
	for i, c := range cases {
		got := subtractPorts(c.a, c.b)
		if !reflect.DeepEqual(got, c.want) && !(len(got) == 0 && len(c.want) == 0) {
			t.Errorf("用例 %d：got %v want %v", i, got, c.want)
		}
	}
}

func TestSubtractRegionUnionCover(t *testing.T) {
	// 区域 tcp 172.16.0.0/16 0-65535 被两条端口各半的规则联合覆盖。
	target := region{
		prefixes: []netip.Prefix{pfx("172.16.0.0/16")},
		protos:   ProtoTCP,
		ports:    []portInterval{{0, 65535}},
	}
	low := region{prefixes: target.prefixes, protos: ProtoTCP, ports: []portInterval{{0, 1023}}}
	high := region{prefixes: target.prefixes, protos: ProtoTCP, ports: []portInterval{{1024, 65535}}}
	rest := subtractRegion(target, low)
	rest = append(rest[:0], subtractRegions(rest, high)...)
	if len(rest) != 0 {
		t.Fatalf("联合覆盖后应为空，剩余 %v", rest)
	}
}

func subtractRegions(rs []region, b region) []region {
	var out []region
	for _, r := range rs {
		out = append(out, subtractRegion(r, b)...)
	}
	return out
}

func TestIntersectRegionCrossFamily(t *testing.T) {
	v4 := region{prefixes: []netip.Prefix{pfx("0.0.0.0/0")}, protos: ProtoAny, ports: []portInterval{{0, 65535}}}
	v6 := region{prefixes: []netip.Prefix{pfx("::/0")}, protos: ProtoAny, ports: []portInterval{{0, 65535}}}
	if _, ok := intersectRegion(v4, v6); ok {
		t.Fatal("IPv4 与 IPv6 区域不应相交")
	}
}
