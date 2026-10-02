package rulecache

import (
	"fmt"
	"strconv"
	"strings"
)

// 维度表名。
const (
	// DimMember 会员维度表，主键为 "租户ID/用户ID"，
	// 字段：level（会员等级）、birthday（生日，YYYY-MM-DD）。
	DimMember = "member"
)

// MemberKey 构造会员维度键。
func MemberKey(tenantID, userID string) string {
	return tenantID + "/" + userID
}

// RuleVIPBirthdayPoints 是规则"VIP生日送积分"的 ID。
const RuleVIPBirthdayPoints = "vip_birthday_points"

// VIPBirthdayPoints 实现规则"VIP生日送积分"：
// 会员生日月与参数 month 相同，且等级为 VIP/SVIP 时赠送积分，
// 否则为 0。参数：tenant、user、month（1-12）。
func VIPBirthdayPoints(tx *ReadTx, params map[string]string) (interface{}, error) {
	key := MemberKey(params["tenant"], params["user"])

	level, _ := tx.Get(DimMember, key, "level")
	birthday, _ := tx.Get(DimMember, key, "birthday")

	month, err := strconv.Atoi(params["month"])
	if err != nil || month < 1 || month > 12 {
		return nil, fmt.Errorf("rulecache: invalid month %q", params["month"])
	}

	parts := strings.SplitN(birthday, "-", 3)
	if len(parts) < 2 {
		return 0, nil
	}
	birthMonth, err := strconv.Atoi(parts[1])
	if err != nil || birthMonth != month {
		return 0, nil
	}

	switch level {
	case "SVIP":
		return 200, nil
	case "VIP":
		return 100, nil
	default:
		return 0, nil
	}
}
