package matcher

import (
	"strings"
	"testing"
	"time"
)

// base 固定测试时钟起点，全部时间均为 UTC，避免时区造成的断言抖动。
var base = time.Date(2026, 11, 11, 8, 0, 0, 0, time.UTC)

func at(sec float64) time.Time {
	return base.Add(time.Duration(sec*1000) * time.Millisecond)
}

// intrusionRule 构造规则：同一用户、同一来源 IP 的“失败登录后 10 秒内没有
// 成功登录，且随后 60 秒内出现管理操作”。
func intrusionRule() Rule {
	return Rule{
		ID: "R-LOGIN-ADMIN",
		Steps: []Step{
			{Alias: "failed", EventType: "login_failed"},
			{
				Alias:     "succeeded",
				EventType: "login_succeeded",
				Negate:    true,
				Window:    Window{Low: 0, High: 10 * time.Second},
			},
			{
				Alias:     "admin",
				EventType: "admin_action",
				Window:    Window{Low: 0, High: 60 * time.Second, BaseAlias: "failed"},
			},
		},
		EqualKeys: []KeyEquality{
			{Key: "user_id", Aliases: []string{"failed", "succeeded", "admin"}},
			{Key: "src_ip", Aliases: []string{"failed", "succeeded", "admin"}},
		},
	}
}

func ev(id, typ string, sec float64, user, ip string) Event {
	return Event{
		ID:    id,
		Type:  typ,
		At:    at(sec),
		Attrs: map[string]string{"user_id": user, "src_ip": ip},
	}
}

// TestRuleValidate 覆盖规则定义的主要非法形式。
func TestRuleValidate(t *testing.T) {
	valid := intrusionRule()
	if err := valid.Validate(); err != nil {
		t.Fatalf("合法规则校验失败: %v", err)
	}

	cases := []struct {
		name string
		mut  func(Rule) Rule
	}{
		{"空规则 ID", func(r Rule) Rule { r.ID = ""; return r }},
		{"段数量不足", func(r Rule) Rule { r.Steps = r.Steps[:1]; return r }},
		{"别名重复", func(r Rule) Rule { r.Steps[1].Alias = "failed"; return r }},
		{"空事件类型", func(r Rule) Rule { r.Steps[0].EventType = ""; return r }},
		{"非左闭右开窗", func(r Rule) Rule { r.Steps[1].Window.High = 0; return r }},
		{"负下界", func(r Rule) Rule { r.Steps[1].Window.Low = -time.Second; return r }},
		{"窗口锚点不存在", func(r Rule) Rule { r.Steps[2].Window.BaseAlias = "ghost"; return r }},
		{"锚点是否定段", func(r Rule) Rule { r.Steps[2].Window.BaseAlias = "succeeded"; return r }},
		{"零号段为否定", func(r Rule) Rule { r.Steps[0].Negate = true; return r }},
		{"键约束别名缺失", func(r Rule) Rule {
			r.EqualKeys[0].Aliases = []string{"failed", "ghost"}
			return r
		}},
		{"键约束属性为空", func(r Rule) Rule { r.EqualKeys[0].Key = ""; return r }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.mut(intrusionRule()).Validate(); err == nil {
				t.Fatalf("%s 应当校验失败", tc.name)
			}
		})
	}
}

// TestInvalidEvents 覆盖事件字段非法与重复 ID。
func TestInvalidEvents(t *testing.T) {
	e, _ := NewEngine(intrusionRule())

	if _, _, err := e.ProcessEvent(Event{ID: "", Type: "x", At: at(0)}); err == nil {
		t.Fatal("空事件 ID 应当报错")
	}
	if _, _, err := e.ProcessEvent(Event{ID: "x", Type: "", At: at(0)}); err == nil {
		t.Fatal("空事件类型应当报错")
	}
	if _, _, err := e.ProcessEvent(Event{ID: "x", Type: "login_failed"}); err == nil {
		t.Fatal("零值事件时间应当报错")
	}
	if _, _, err := e.ProcessEvent(ev("dup", "login_failed", 0, "u", "ip")); err != nil {
		t.Fatalf("首次接收失败: %v", err)
	}
	if _, _, err := e.ProcessEvent(ev("dup", "admin_action", 1, "u", "ip")); err == nil ||
		!strings.Contains(err.Error(), "重复") {
		t.Fatalf("重复 ID 应报错并说明重复，得到 %v", err)
	}
}
