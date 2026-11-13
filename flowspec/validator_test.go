package flowspec

import (
	"net/netip"
	"testing"
)

const (
	asVictim   = 65001
	asAttacker = 65002
	asThird    = 65003
)

type alarmRecorder struct {
	alarms []Alarm
}

func (r *alarmRecorder) Alert(a Alarm) {
	r.alarms = append(r.alarms, a)
}

func mustPrefix(t *testing.T, s string) netip.Prefix {
	t.Helper()
	p, err := netip.ParsePrefix(s)
	if err != nil {
		t.Fatalf("bad prefix %q: %v", s, err)
	}
	return p
}

// dropRule builds a well-formed "discard traffic to prefix" style rule
// from the given originator AS.
func dropRule(t *testing.T, originAS uint32, dest string) Rule {
	t.Helper()
	return Rule{
		OriginatorAS: originAS,
		NLRI: NLRI{Components: []Component{
			{Type: CompDestPrefix, Value: EncodePrefixComponent(mustPrefix(t, dest))},
			{Type: CompIPProtocol, Value: []byte{0x81, 0x06}}, // == TCP
			{Type: CompDestPort, Value: []byte{0x81, 0x50}},   // == 80
		}},
	}
}

func requireRejected(t *testing.T, res Result, rec *alarmRecorder, want AlarmKind) {
	t.Helper()
	if res.Installed {
		t.Fatalf("rule was installed, want rejection (%s)", res.Reason)
	}
	if len(rec.alarms) != 1 {
		t.Fatalf("got %d alarms, want exactly 1", len(rec.alarms))
	}
	if rec.alarms[0].Kind != want {
		t.Fatalf("alarm kind = %s, want %s", rec.alarms[0].Kind, want)
	}
}

// 合法规则：目的前缀的最佳单播路由与规则来自同一邻居 AS，且范围内没有
// 来自其他 AS 的更具体路由，规则应被安装且不产生告警。
func TestValidateLegitimateRule(t *testing.T) {
	rib := NewTable(
		Route{Prefix: mustPrefix(t, "203.0.113.0/24"), NeighborAS: asVictim},
		Route{Prefix: mustPrefix(t, "203.0.113.0/25"), NeighborAS: asVictim}, // same-AS more specific is fine
	)
	rec := &alarmRecorder{}
	v := &Validator{RIB: rib, Alerter: rec}

	res := v.Validate(dropRule(t, asVictim, "203.0.113.0/24"))
	if !res.Installed {
		t.Fatalf("legitimate rule rejected: %s", res.Reason)
	}
	if len(rec.alarms) != 0 {
		t.Fatalf("unexpected alarms: %+v", rec.alarms)
	}
}

// 他人前缀：攻击者（AS 65002）对属于 AS 65001 的前缀下发丢弃规则，
// 最佳单播路由来源不匹配，必须拒绝并告警。
func TestValidateForeignPrefixRejected(t *testing.T) {
	rib := NewTable(
		Route{Prefix: mustPrefix(t, "203.0.113.0/24"), NeighborAS: asVictim},
	)
	rec := &alarmRecorder{}
	v := &Validator{RIB: rib, Alerter: rec}

	res := v.Validate(dropRule(t, asAttacker, "203.0.113.0/24"))
	requireRejected(t, res, rec, AlarmOriginMismatch)
}

// 更具体路由：目的前缀的最佳路由虽来自同一 AS，但前缀范围内存在来自
// 其他邻居 AS 的更具体单播路由，必须拒绝并告警。
func TestValidateForeignMoreSpecificRejected(t *testing.T) {
	rib := NewTable(
		Route{Prefix: mustPrefix(t, "203.0.113.0/24"), NeighborAS: asVictim},
		Route{Prefix: mustPrefix(t, "203.0.113.128/25"), NeighborAS: asThird},
	)
	rec := &alarmRecorder{}
	v := &Validator{RIB: rib, Alerter: rec}

	res := v.Validate(dropRule(t, asVictim, "203.0.113.0/24"))
	requireRejected(t, res, rec, AlarmForeignMoreSpecific)
}

// 乱序组件：NLRI 组件未按类型升序排列，视为畸形，必须拒绝并告警。
func TestValidateOutOfOrderComponentsRejected(t *testing.T) {
	rib := NewTable(
		Route{Prefix: mustPrefix(t, "203.0.113.0/24"), NeighborAS: asVictim},
	)
	rec := &alarmRecorder{}
	v := &Validator{RIB: rib, Alerter: rec}

	rule := Rule{
		OriginatorAS: asVictim,
		NLRI: NLRI{Components: []Component{
			{Type: CompDestPort, Value: []byte{0x81, 0x50}}, // type 5 before type 1
			{Type: CompDestPrefix, Value: EncodePrefixComponent(mustPrefix(t, "203.0.113.0/24"))},
		}},
	}
	res := v.Validate(rule)
	requireRejected(t, res, rec, AlarmMalformedNLRI)
}

// 无覆盖路由：目的前缀没有任何单播路由覆盖，规则不得安装。
func TestValidateNoUnicastRouteRejected(t *testing.T) {
	rec := &alarmRecorder{}
	v := &Validator{RIB: NewTable(), Alerter: rec}

	res := v.Validate(dropRule(t, asVictim, "192.0.2.0/24"))
	requireRejected(t, res, rec, AlarmNoUnicastRoute)
}

// 最佳匹配须为最长前缀匹配：/24 来自攻击者，但覆盖目的 /25 的最佳
// 路由（/25 自身）来自受害者 AS，规则应通过。
func TestValidateBestMatchIsLongestPrefix(t *testing.T) {
	rib := NewTable(
		Route{Prefix: mustPrefix(t, "203.0.113.0/24"), NeighborAS: asAttacker},
		Route{Prefix: mustPrefix(t, "203.0.113.0/25"), NeighborAS: asVictim},
	)
	rec := &alarmRecorder{}
	v := &Validator{RIB: rib, Alerter: rec}

	res := v.Validate(dropRule(t, asVictim, "203.0.113.0/25"))
	if !res.Installed {
		t.Fatalf("rule rejected: %s", res.Reason)
	}
}

func TestNLRIValidateOrder(t *testing.T) {
	good := NLRI{Components: []Component{{Type: 1}, {Type: 3}, {Type: 5}}}
	if err := good.ValidateOrder(); err != nil {
		t.Fatalf("ascending NLRI rejected: %v", err)
	}
	dup := NLRI{Components: []Component{{Type: 1}, {Type: 1}}}
	if err := dup.ValidateOrder(); err == nil {
		t.Fatal("duplicate component types accepted")
	}
}

func TestPrefixComponentRoundTrip(t *testing.T) {
	for _, s := range []string{"0.0.0.0/0", "203.0.113.0/24", "203.0.113.128/25", "192.0.2.1/32"} {
		p := mustPrefix(t, s)
		got, err := DecodePrefixComponent(EncodePrefixComponent(p), false)
		if err != nil {
			t.Fatalf("decode %s: %v", s, err)
		}
		if got != p {
			t.Fatalf("round trip %s: got %s", s, got)
		}
	}
}
