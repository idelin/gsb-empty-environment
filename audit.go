package netsec

import "sort"

// RiskKind 标识提权风险类别。
type RiskKind string

const (
	// RiskUnconfinedExec：受限配置中出现 ux 规则，可逃逸到无约束执行。
	RiskUnconfinedExec RiskKind = "unconfined-exec"
	// RiskWriteAll：对 "**" 授予 w，可写任意路径。
	RiskWriteAll RiskKind = "write-all"
	// RiskProcMemWrite：对 /proc/*/mem 授予 w，可改写任意进程内存。
	RiskProcMemWrite RiskKind = "proc-mem-write"
)

// Risk 是一条发布前评审发现的提权风险。
type Risk struct {
	Profile string
	Pattern string
	Kind    RiskKind
	Detail  string
}

// procMemProbes 用于探测规则 glob 是否覆盖 /proc/*/mem。
var procMemProbes = []string{"/proc/self/mem", "/proc/1/mem"}

// Audit 评审整组配置，返回所有提权风险；无风险时返回 nil。
func (pol *Policy) Audit() []Risk {
	names := make([]string, 0, len(pol.Profiles))
	for name := range pol.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)

	var risks []Risk
	for _, name := range names {
		risks = append(risks, AuditProfile(pol.Profiles[name])...)
	}
	return risks
}

// AuditProfile 评审单个受限配置：
// 出现 ux 执行规则、对 "**" 的 w 权限、或对 /proc/*/mem 的 w 权限即报告。
// 若同一模式已被 deny 规则封堵对应权限，则视为已缓解，不再报告。
func AuditProfile(p *Profile) []Risk {
	var risks []Risk
	for _, r := range p.Rules {
		if r.Deny {
			continue
		}
		if r.Perms&PermExec != 0 && r.Exec == ExecUnconfined && !denied(p, r.Pattern, PermExec) {
			risks = append(risks, Risk{
				Profile: p.Name,
				Pattern: r.Pattern,
				Kind:    RiskUnconfinedExec,
				Detail:  "ux 无约束执行，子进程脱离全部配置",
			})
		}
		if r.Perms&PermWrite == 0 || denied(p, r.Pattern, PermWrite) {
			continue
		}
		if r.Pattern == "**" {
			risks = append(risks, Risk{
				Profile: p.Name,
				Pattern: r.Pattern,
				Kind:    RiskWriteAll,
				Detail:  "对 ** 授予 w，可写文件系统任意路径",
			})
		}
		for _, probe := range procMemProbes {
			if matchGlob(r.Pattern, probe) {
				risks = append(risks, Risk{
					Profile: p.Name,
					Pattern: r.Pattern,
					Kind:    RiskProcMemWrite,
					Detail:  "对 " + probe + " 授予 w，可改写进程内存实现提权",
				})
				break
			}
		}
	}
	return risks
}

// denied 判断 profile 中是否存在同模式且覆盖 perm 的 deny 规则。
func denied(p *Profile, pattern string, perm Perm) bool {
	for _, r := range p.Rules {
		if r.Deny && r.Pattern == pattern && r.Perms&perm != 0 {
			return true
		}
	}
	return false
}
