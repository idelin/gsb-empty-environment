package rulecache

import (
	"fmt"
	"time"
)

// VIPBirthdayBonusRule 规则「VIP生日送积分」：
// 普通会员生日月送 50 分，VIP 送 100 分，SVIP 送 200 分；非生日月不送。
// 依赖维度：用户会员等级、用户生日、租户积分活动开关。
type VIPBirthdayBonusRule struct{}

func (VIPBirthdayBonusRule) Name() string { return "VIP生日送积分" }

func (VIPBirthdayBonusRule) Evaluate(ctx *EvalContext, userID string) (any, error) {
	enabled, _ := ctx.Get(DimKey(ctx.Tenant+":config:campaign"), "birthday_bonus_enabled")
	if enabled != "true" {
		return 0, nil
	}
	level, _ := ctx.Get(DimKey(ctx.Tenant+":user:"+userID+":member"), "level")
	birthday, ok := ctx.Get(DimKey(ctx.Tenant+":user:"+userID+":profile"), "birthday")
	if !ok {
		return 0, fmt.Errorf("用户 %s 缺少生日维度", userID)
	}
	if len(birthday) < 7 || birthday[5:7] != time.Now().Format("01")[0:2] {
		return 0, nil // 非生日月
	}
	switch level {
	case "SVIP":
		return 200, nil
	case "VIP":
		return 100, nil
	default:
		return 50, nil
	}
}
