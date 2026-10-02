package compliance

// Role 访问者角色。
type Role string

// Level 合规级别（数值越大要求越严格）。
type Level int

const (
	LevelRestricted   Level = 1 // 内部受限：明文可见
	LevelConfidential Level = 2 // 机密：敏感字段掩码
	LevelPublic       Level = 3 // 对外：假名化/区间化/拒绝
)

const (
	RoleHRAdmin    Role = "hr_admin"   // HR 管理员
	RoleFinance    Role = "finance"    // 财务
	RoleContractor Role = "contractor" // 外包人员
)

// Policy 声明式合规策略：按字段声明应用的转换。
// 未声明的字段默认 Deny（默认拒绝，最小暴露）。
type Policy struct {
	Role   Role
	Level  Level
	Fields map[string]Transform
}

// TransformFor 返回字段对应的转换；未声明字段默认拒绝。
func (p Policy) TransformFor(field string) Transform {
	if t, ok := p.Fields[field]; ok {
		return t
	}
	return Deny{}
}

// DefaultPolicies 内置角色策略表。
func DefaultPolicies() map[Role]Policy {
	return map[Role]Policy{
		RoleHRAdmin: {
			Role:  RoleHRAdmin,
			Level: LevelRestricted,
			Fields: map[string]Transform{
				"id":      Pass{},
				"name":    Pass{},
				"dept":    Pass{},
				"title":   Pass{},
				"phone":   Pass{},
				"id_card": Pass{},
				"salary":  Pass{},
			},
		},
		// 财务：可查看薪资明细；手机号保留前 3 后 4；身份证掩码。
		RoleFinance: {
			Role:  RoleFinance,
			Level: LevelConfidential,
			Fields: map[string]Transform{
				"id":      Pass{},
				"name":    Pass{},
				"dept":    Pass{},
				"title":   Pass{},
				"phone":   Mask{Head: 3, Tail: 4},
				"id_card": Mask{Head: 6, Tail: 4},
				"salary":  Pass{},
			},
		},
		// 外包：薪资置为区间范围，姓名哈希假名化，手机号/身份证拒绝导出。
		RoleContractor: {
			Role:  RoleContractor,
			Level: LevelPublic,
			Fields: map[string]Transform{
				"id":      Pass{},
				"name":    HashPseudonym{Prefix: "EMP"},
				"dept":    Pass{},
				"title":   Pass{},
				"phone":   Deny{},
				"id_card": Deny{},
				"salary":  Bucket{Edges: []float64{0, 10000, 15000, 20000, 25000, 30000}},
			},
		},
	}
}
