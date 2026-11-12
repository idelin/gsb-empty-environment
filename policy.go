package netsec

import "fmt"

// Perm 是访问权限位掩码。
type Perm uint8

const (
	PermRead Perm = 1 << iota
	PermWrite
	PermExec
	PermAppend
)

// ParsePerms 把 "rw"、"r"、"x" 这类字符串解析为权限位掩码。
func ParsePerms(s string) (Perm, error) {
	var p Perm
	for _, c := range s {
		switch c {
		case 'r':
			p |= PermRead
		case 'w':
			p |= PermWrite
		case 'x':
			p |= PermExec
		case 'a':
			p |= PermAppend
		default:
			return 0, fmt.Errorf("netsec: 未知权限字符 %q", c)
		}
	}
	return p, nil
}

// ExecMode 是执行规则携带的配置转换方式。
type ExecMode int

const (
	// ExecNone 表示普通文件规则（非执行规则）。
	ExecNone ExecMode = iota
	// ExecInherit 即 ix：继承当前配置。
	ExecInherit
	// ExecProfile 即 px：切换到 Target 指定的命名配置，缺失则执行失败。
	ExecProfile
	// ExecProfileClean 即 Px：同 px，并在切换前清理环境变量。
	ExecProfileClean
	// ExecUnconfined 即 ux：脱离所有配置，无约束执行。
	ExecUnconfined
)

// Rule 是一条路径规则：glob 模式 + 权限 + deny 标志。
type Rule struct {
	Pattern string
	Perms   Perm
	Deny    bool
	// 以下字段仅在 Perms 含 PermExec 时有意义。
	Exec   ExecMode
	Target string // px/Px 的目标配置名
}

// Profile 是一个命名 AppArmor 配置。
type Profile struct {
	Name  string
	Rules []Rule
}

// Policy 是待发布的一组配置。
type Policy struct {
	Profiles map[string]*Profile
}

// NewPolicy 按配置名索引 profiles，构造 Policy。
func NewPolicy(profiles ...*Profile) *Policy {
	pol := &Policy{Profiles: make(map[string]*Profile, len(profiles))}
	for _, p := range profiles {
		pol.Profiles[p.Name] = p
	}
	return pol
}

// Decide 判定 profile 对 path 的 perm 访问是否放行。
// 同一访问命中任一 deny 规则即拒绝；否则取所有 allow 规则的并集。
func (p *Profile) Decide(path string, perm Perm) bool {
	allowed := false
	for _, r := range p.Rules {
		if r.Perms&perm == 0 || !matchGlob(r.Pattern, path) {
			continue
		}
		if r.Deny {
			return false
		}
		allowed = true
	}
	return allowed
}

// ExecResult 描述一次 exec 转换的结果。
type ExecResult struct {
	Profile    string // 生效的配置名；Unconfined 为 true 时为空
	Unconfined bool   // ux：脱离所有配置
	CleanEnv   bool   // Px：切换前清理环境变量
}

// ResolveExec 解析 current 配置下执行 path 后的配置归属。
// deny 命中即失败；px/Px 目标配置缺失即失败。
func (pol *Policy) ResolveExec(current, path string) (ExecResult, error) {
	prof, ok := pol.Profiles[current]
	if !ok {
		return ExecResult{}, fmt.Errorf("netsec: 配置 %q 不存在", current)
	}
	var match *Rule
	for i := range prof.Rules {
		r := &prof.Rules[i]
		if r.Perms&PermExec == 0 || !matchGlob(r.Pattern, path) {
			continue
		}
		if r.Deny {
			return ExecResult{}, fmt.Errorf("netsec: 配置 %q 拒绝执行 %q", current, path)
		}
		if match == nil {
			match = r
		}
	}
	if match == nil {
		return ExecResult{}, fmt.Errorf("netsec: 配置 %q 不允许执行 %q", current, path)
	}
	switch match.Exec {
	case ExecUnconfined:
		return ExecResult{Unconfined: true}, nil
	case ExecProfile, ExecProfileClean:
		if _, ok := pol.Profiles[match.Target]; !ok {
			return ExecResult{}, fmt.Errorf("netsec: px 目标配置 %q 缺失，执行 %q 失败", match.Target, path)
		}
		return ExecResult{
			Profile:  match.Target,
			CleanEnv: match.Exec == ExecProfileClean,
		}, nil
	default: // ExecInherit，以及未标注模式的 x 按继承处理
		return ExecResult{Profile: current}, nil
	}
}
