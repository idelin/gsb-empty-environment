// Package risk 实现一个带时效窗口的流式风控规则引擎。
//
// 核心能力：
//  1. 规则条件结合单笔交易特征与滑动窗口历史频次
//     （例如 "10分钟内转账超3次且累计金额超1万"）。
//  2. 规则版本动态平滑热切换：UpdateRules 立即生效于新会话，
//     已活跃的长事务会话继续按首次评估时钉住的旧版本裁定，
//     直到会话结束（EndSession）后平稳过渡。
//  3. 每次决策写入审计日志，标记实际使用的规则版本号。
package risk

import (
	"fmt"
	"sync"
	"time"
)

// Txn 是一笔待裁定的交易事件。
type Txn struct {
	ID        string
	AccountID string
	SessionID string // 同一长事务内的多笔交易共享 SessionID
	Amount    int64  // 金额，单位：分
	At        time.Time
}

// Rule 描述一条风控规则：在 Window 滑动窗口内，
// 交易次数超过 MaxCount 且累计金额超过 MaxAmount 时拦截；
// MaxSingleAmount 为单笔金额上限（0 表示不限制）。
type Rule struct {
	Name            string
	Window          time.Duration
	MaxCount        int
	MaxAmount       int64
	MaxSingleAmount int64
}

// RuleSet 是一个规则版本快照，热切换的最小单位。
type RuleSet struct {
	Version string
	Rules   []Rule
}

// Decision 是一次风控裁定的结果。
type Decision struct {
	Allowed     bool
	RuleVersion string
	MatchedRule string // 命中规则的名称，未命中为空
	Reason      string
}

// AuditEntry 是审计日志条目，记录裁定所用的规则版本。
type AuditEntry struct {
	Time        time.Time
	TxnID       string
	SessionID   string
	AccountID   string
	RuleVersion string
	Allowed     bool
	MatchedRule string
	Reason      string
}

func (e AuditEntry) String() string {
	verdict := "ALLOW"
	if !e.Allowed {
		verdict = "BLOCK"
	}
	return fmt.Sprintf("[t=%s] txn=%s session=%s account=%s ruleset=%s verdict=%s rule=%q reason=%s",
		e.Time.Format("15:04:05"), e.TxnID, e.SessionID, e.AccountID,
		e.RuleVersion, verdict, e.MatchedRule, e.Reason)
}

// Engine 是流式风控决策引擎，并发安全。
type Engine struct {
	mu       sync.Mutex
	now      func() time.Time
	current  *RuleSet
	versions map[string]*RuleSet // version -> 规则快照（旧版本保留以服务活跃会话）
	sessions map[string]string   // sessionID -> 钉住的规则版本
	history  map[string][]Txn    // accountID -> 已评估交易（滑动窗口数据源）
	audit    []AuditEntry
}

// New 创建引擎。now 为时钟函数，测试中可注入假时钟；传 nil 使用真实时间。
func New(initial *RuleSet, now func() time.Time) *Engine {
	if now == nil {
		now = time.Now
	}
	e := &Engine{
		now:      now,
		versions: make(map[string]*RuleSet),
		sessions: make(map[string]string),
		history:  make(map[string][]Txn),
	}
	e.UpdateRules(initial)
	return e
}

// UpdateRules 热切换规则版本：新会话立即使用新版本，
// 已钉住旧版本的活跃会话不受影响，实现平滑过渡。
func (e *Engine) UpdateRules(rs *RuleSet) {
	e.mu.Lock()
	defer e.mu.Unlock()
	cp := *rs
	e.versions[rs.Version] = &cp
	e.current = &cp
}

// Evaluate 对一笔交易进行流式风控裁定，并写入审计日志。
func (e *Engine) Evaluate(txn Txn) Decision {
	e.mu.Lock()
	defer e.mu.Unlock()

	// 会话版本钉住：活跃会话沿用首次评估时的版本，新会话钉住当前版本。
	version, ok := e.sessions[txn.SessionID]
	if !ok {
		version = e.current.Version
		e.sessions[txn.SessionID] = version
	}
	rs := e.versions[version]

	// 将当前交易并入滑动窗口视图（先判定再记录历史）。
	window := append(append([]Txn{}, e.history[txn.AccountID]...), txn)

	dec := Decision{Allowed: true, RuleVersion: version}
	for _, r := range rs.Rules {
		if r.MaxSingleAmount > 0 && txn.Amount > r.MaxSingleAmount {
			dec.Allowed = false
			dec.MatchedRule = r.Name
			dec.Reason = fmt.Sprintf("单笔金额 %d 超过上限 %d", txn.Amount, r.MaxSingleAmount)
			break
		}
		cutoff := txn.At.Add(-r.Window)
		count := 0
		var sum int64
		for _, h := range window {
			if !h.At.Before(cutoff) && !h.At.After(txn.At) {
				count++
				sum += h.Amount
			}
		}
		if r.MaxCount > 0 && count > r.MaxCount && sum > r.MaxAmount {
			dec.Allowed = false
			dec.MatchedRule = r.Name
			dec.Reason = fmt.Sprintf("%v 内交易 %d 次(上限%d)且累计金额 %d(上限%d)",
				r.Window, count, r.MaxCount, sum, r.MaxAmount)
			break
		}
	}
	if dec.Allowed {
		dec.Reason = "未命中任何风控规则"
	}

	// 记录历史（作为后续滑动窗口的数据源），并追加审计日志。
	e.history[txn.AccountID] = append(e.history[txn.AccountID], txn)
	e.audit = append(e.audit, AuditEntry{
		Time:        txn.At,
		TxnID:       txn.ID,
		SessionID:   txn.SessionID,
		AccountID:   txn.AccountID,
		RuleVersion: version,
		Allowed:     dec.Allowed,
		MatchedRule: dec.MatchedRule,
		Reason:      dec.Reason,
	})
	return dec
}

// EndSession 结束会话，释放版本钉住；该会话后续交易将按当前最新版本裁定。
func (e *Engine) EndSession(sessionID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.sessions, sessionID)
}

// AuditLog 返回审计日志快照。
func (e *Engine) AuditLog() []AuditEntry {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]AuditEntry, len(e.audit))
	copy(out, e.audit)
	return out
}

// CurrentVersion 返回当前生效（对新会话）的规则版本。
func (e *Engine) CurrentVersion() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.current.Version
}
