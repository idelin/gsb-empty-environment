package matcher_test

import (
	"fmt"
	"time"

	"example.org/netsec/uid1247/matcher"
)

// ExampleEngine 演示“失败登录后十秒没有成功登录，且随后出现管理操作”
// 的完整生命周期：暂定 → 水位线确认否定 → 告警 → 迟到事件被拒绝。
func ExampleEngine() {
	t0 := time.Date(2026, 11, 11, 8, 0, 0, 0, time.UTC)
	rule := matcher.Rule{
		ID: "R-LOGIN-ADMIN",
		Steps: []matcher.Step{
			{Alias: "failed", EventType: "login_failed"},
			{
				Alias:     "succeeded",
				EventType: "login_succeeded",
				Negate:    true,
				Window:    matcher.Window{Low: 0, High: 10 * time.Second},
			},
			{
				Alias:     "admin",
				EventType: "admin_action",
				Window:    matcher.Window{Low: 0, High: 60 * time.Second, BaseAlias: "failed"},
			},
		},
		EqualKeys: []matcher.KeyEquality{
			{Key: "user_id", Aliases: []string{"failed", "succeeded", "admin"}},
		},
	}

	engine, err := matcher.NewEngine(rule)
	if err != nil {
		panic(err)
	}
	login := matcher.Event{
		ID:    "f-100",
		Type:  "login_failed",
		At:    t0,
		Attrs: map[string]string{"user_id": "alice"},
	}
	admin := matcher.Event{
		ID:    "a-100",
		Type:  "admin_action",
		At:    t0.Add(15 * time.Second),
		Attrs: map[string]string{"user_id": "alice"},
	}

	out, _, _ := engine.ProcessEvent(login)
	fmt.Println("失败登录后:", out[0].Kind, out[0].Match.Status)

	out, _, _ = engine.ProcessEvent(admin)
	fmt.Println("管理操作后输出数:", len(out))

	out, _ = engine.AdvanceWatermark(t0.Add(10 * time.Second))
	fmt.Println("水位线到达段末:", out[0].Kind, out[0].Match.Status, out[0].Match.Evidence)

	_, rejected, _ := engine.ProcessEvent(matcher.Event{
		ID:    "s-late",
		Type:  "login_succeeded",
		At:    t0.Add(3 * time.Second),
		Attrs: map[string]string{"user_id": "alice"},
	})
	fmt.Println("迟到事件被拒绝:", rejected != nil)

	// Output:
	// 失败登录后: TENTATIVE TENTATIVE
	// 管理操作后输出数: 0
	// 水位线到达段末: ALERT ALERTED [f-100 a-100]
	// 迟到事件被拒绝: true
}
