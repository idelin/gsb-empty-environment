package netsec

import (
	"net/netip"
	"time"
)

// Entry 是一条已解析的授权条目（例如 authorized_keys 中一行的选项部分），
// 绑定到某一公钥指纹。条目内部各选项之间是合取关系：
// 来源 CIDR、强制命令、转发目标、有效期必须同时满足，
// 该条目才允许一次会话请求。
type Entry struct {
	// ID 是条目来源标识（如 "authorized_keys:17"），用于显式选择与审计。
	ID string
	// Fingerprint 是该条目授权的公钥指纹。
	Fingerprint string
	// FromCIDRs 是来源地址白名单；为空表示不限制来源。
	FromCIDRs []netip.Prefix
	// Command 是强制命令；非空时会话只允许执行该命令，空表示不限制。
	Command string
	// PermitOpen 是允许的转发目标（host:port）；为空表示不限制转发目标。
	PermitOpen []string
	// ValidAfter / ValidBefore 是有效期下/上界；零值表示该方向无界。
	ValidAfter  time.Time
	ValidBefore time.Time
}

// Request 描述一次待授权的会话请求。
type Request struct {
	// Source 是客户端来源地址。
	Source netip.Addr
	// Command 是客户端请求执行的命令；空表示未请求命令（交互会话）。
	Command string
	// Target 是客户端请求的转发目标（host:port）；空表示未请求转发。
	Target string
	// Now 是求值时刻，用于有效期判断。
	Now time.Time
}

// Evidence 是选中条目的完整证据。会话宿主必须按证据中的
// 强制命令与转发目标执行，不得与其他条目的选项组合。
type Evidence struct {
	EntryID       string
	Fingerprint   string
	MatchedCIDR   string // 实际匹配来源的 CIDR；条目未限制来源时为空
	ForcedCommand string // 选中条目的强制命令（可能为空，表示条目不限制）
	MatchedTarget string // 实际匹配的转发目标；未请求转发时为空
	ValidAfter    time.Time
	ValidBefore   time.Time
}

// Allows 判断单条目的合取是否允许该请求，并在允许时返回证据。
// 它只看这一条目，绝不参考其他条目的选项。
func (e Entry) Allows(req Request) (Evidence, bool) {
	ev := Evidence{
		EntryID:       e.ID,
		Fingerprint:   e.Fingerprint,
		ForcedCommand: e.Command,
		ValidAfter:    e.ValidAfter,
		ValidBefore:   e.ValidBefore,
	}

	if !e.ValidAfter.IsZero() && req.Now.Before(e.ValidAfter) {
		return Evidence{}, false
	}
	if !e.ValidBefore.IsZero() && req.Now.After(e.ValidBefore) {
		return Evidence{}, false
	}

	if len(e.FromCIDRs) > 0 {
		matched := ""
		for _, p := range e.FromCIDRs {
			if p.Contains(req.Source) {
				matched = p.String()
				break
			}
		}
		if matched == "" {
			return Evidence{}, false
		}
		ev.MatchedCIDR = matched
	}

	if e.Command != "" && req.Command != e.Command {
		return Evidence{}, false
	}

	if req.Target != "" && len(e.PermitOpen) > 0 {
		ok := false
		for _, t := range e.PermitOpen {
			if t == req.Target {
				ok = true
				break
			}
		}
		if !ok {
			return Evidence{}, false
		}
		ev.MatchedTarget = req.Target
	}

	return ev, true
}
