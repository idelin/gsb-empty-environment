package netsec

import (
	"sync"
	"time"
)

// 默认检测参数。
const (
	// DefaultThreshold 是触发封禁所需的连续爆破崩溃次数。
	DefaultThreshold = 8
	// DefaultMaxStep 是相邻两次崩溃之间允许的最大载荷长度增量（逐字节爆破为 1）。
	DefaultMaxStep = 1
)

// Signature 是一次子进程崩溃的签名。
//
// 杀死子进程的信号（如 SIGSEGV、SIGABRT）与退出码共同刻画崩溃方式。
// 同一漏洞点被反复触发时签名保持稳定，可作为区分爆破与偶发崩溃的依据。
type Signature struct {
	// Signal 是导致子进程终止的信号编号，未被信号杀死时为 0。
	Signal int
	// ExitCode 是子进程的退出码，被信号杀死时通常为 0。
	ExitCode int
}

// ExitEvent 描述监督器观察到的一次子进程退出。
type ExitEvent struct {
	// Source 是连接来源地址（IP 或 IP:port），作为归因与封禁的键。
	Source string
	// PayloadLen 是该连接发送的载荷字节长度。
	PayloadLen int
	// Signal 是杀死子进程的信号编号，正常退出时为 0。
	Signal int
	// ExitCode 是子进程退出码，正常退出时为 0。
	ExitCode int
	// Timestamp 是事件发生时间；为零值时由检测器用配置的时钟填充。
	Timestamp time.Time
}

// IsCrash 报告该退出是否属于异常崩溃（被信号杀死或退出码非零）。
func (e ExitEvent) IsCrash() bool {
	return e.Signal != 0 || e.ExitCode != 0
}

// Signature 返回该事件的崩溃签名。
func (e ExitEvent) Signature() Signature {
	return Signature{Signal: e.Signal, ExitCode: e.ExitCode}
}

// BanFunc 在某来源达到爆破阈值、需要封禁时被调用，参数为来源地址。
// 回调在检测器内部锁之外执行，可安全地进行网络或防火墙操作。
type BanFunc func(source string)

// Config 配置 Detector。零值字段由 New 填充默认值。
type Config struct {
	// Threshold 是同一来源触发封禁所需的连续爆破崩溃次数，必须 >= 1。
	Threshold int
	// MaxStep 是相邻崩溃载荷长度允许的最大正增量；
	// 逐字节爆破取 1，长度跳跃超过该值会中断序列。
	MaxStep int
	// BanTTL 是封禁有效期；为 0 表示永久封禁，直到显式 Unban 或 Reset。
	BanTTL time.Duration
	// Now 返回当前时间，主要供测试注入虚拟时钟；为 nil 时使用 time.Now。
	Now func() time.Time
	// OnBan 是封禁回调；为 nil 时仅记录状态、不执行外部动作。
	OnBan BanFunc
}

// sourceState 保存单个来源的爆破序列状态。
type sourceState struct {
	// streak 是当前连续爆破崩溃计数。
	streak int
	// lastLen 是上一次崩溃的载荷长度。
	lastLen int
	// lastSig 是上一次崩溃的签名。
	lastSig Signature
	// hasLast 表示是否已记录过崩溃（用于区分首个事件）。
	hasLast bool
	// bannedUntil 是封禁到期时刻；零值或晚于当前时间的判断由调用方完成。
	bannedUntil time.Time
}

// isBannedAt 报告该来源在给定时刻是否处于封禁状态。
func (s *sourceState) isBannedAt(now time.Time) bool {
	return !s.bannedUntil.IsZero() && s.bannedUntil.After(now)
}

// Stats 是检测器的累计统计快照。
type Stats struct {
	// EventsObserved 是 Observe 收到的全部退出事件数。
	EventsObserved int
	// CrashesObserved 是其中属于异常崩溃的事件数。
	CrashesObserved int
	// BansIssued 是累计触发的封禁次数（重复命中已封禁来源不计入）。
	BansIssued int
	// ActiveBans 是当前仍生效的封禁数量。
	ActiveBans int
}

// Detector 识别逐字节 stack canary 爆破序列。
//
// 检测器为每个来源独立维护状态：当同一来源的连续崩溃满足
// 「崩溃签名一致」且「载荷长度按不超过 MaxStep 的正增量逐次增长」时，
// 序列计数累加；任一条件不满足则序列重新开始。计数达到 Threshold 时
// 触发一次 OnBan 回调。正常退出事件不参与、也不中断序列，
// 因此攻击者在两次崩溃之间成功处理连接不会掩盖其行为。
//
// Detector 的所有方法均为并发安全，可供监督器的多个 goroutine 调用。
type Detector struct {
	mu     sync.Mutex
	cfg    Config
	states map[string]*sourceState
	stats  Stats
}

// New 创建检测器并用默认值填充未设置的配置项。
func New(cfg Config) *Detector {
	if cfg.Threshold < 1 {
		cfg.Threshold = DefaultThreshold
	}
	if cfg.MaxStep < 1 {
		cfg.MaxStep = DefaultMaxStep
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Detector{
		cfg:    cfg,
		states: make(map[string]*sourceState),
	}
}

// Observe 录入一次子进程退出事件并更新爆破序列状态。
//
// 返回 true 表示该来源当前已被封禁（本次事件触发了封禁，或此前已被封禁）。
// 封禁回调对每个来源只触发一次；封禁到期或被 Unban 后再次命中阈值会重新触发。
func (d *Detector) Observe(ev ExitEvent) bool {
	d.mu.Lock()

	d.stats.EventsObserved++
	now := ev.Timestamp
	if now.IsZero() {
		now = d.cfg.Now()
	}

	st := d.states[ev.Source]
	if st == nil {
		st = &sourceState{}
		d.states[ev.Source] = st
	}

	// 惰性清理到期封禁。
	if !st.bannedUntil.IsZero() && !st.bannedUntil.After(now) {
		st.bannedUntil = time.Time{}
		d.stats.ActiveBans--
	}

	if !ev.IsCrash() {
		// 正常退出既不参与也不中断爆破序列，直接返回当前封禁状态。
		banned := st.isBannedAt(now)
		d.mu.Unlock()
		return banned
	}

	d.stats.CrashesObserved++

	// 已封禁来源的后续崩溃只做归因，不再重复计数或回调。
	if st.isBannedAt(now) {
		d.mu.Unlock()
		return true
	}

	sig := ev.Signature()
	grew := st.hasLast &&
		ev.PayloadLen > st.lastLen &&
		ev.PayloadLen-st.lastLen <= d.cfg.MaxStep &&
		sig == st.lastSig

	if grew {
		st.streak++
	} else {
		// 首个崩溃、签名变化、长度未严格递增或跳跃过大：序列从头开始。
		st.streak = 1
	}
	st.lastLen = ev.PayloadLen
	st.lastSig = sig
	st.hasLast = true

	if st.streak < d.cfg.Threshold {
		d.mu.Unlock()
		return false
	}

	// 命中阈值：重置序列并执行封禁，避免对同一轮攻击重复回调。
	st.streak = 0
	st.hasLast = false
	if d.cfg.BanTTL > 0 {
		st.bannedUntil = now.Add(d.cfg.BanTTL)
	} else {
		// 9999 年对监督器而言等价于永久封禁，直到显式 Unban 或 Reset。
		st.bannedUntil = time.Date(9999, time.December, 31, 23, 59, 59, 0, time.UTC)
	}
	d.stats.BansIssued++
	d.stats.ActiveBans++

	onBan := d.cfg.OnBan
	d.mu.Unlock()

	if onBan != nil {
		onBan(ev.Source)
	}
	return true
}

// Banned 报告来源当前是否处于封禁状态。
func (d *Detector) Banned(source string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	st := d.states[source]
	if st == nil {
		return false
	}
	now := d.cfg.Now()
	if !st.bannedUntil.IsZero() && !st.bannedUntil.After(now) {
		st.bannedUntil = time.Time{}
		d.stats.ActiveBans--
		return false
	}
	return st.isBannedAt(now)
}

// Unban 提前解除来源的封禁，返回该来源此前是否处于封禁状态。
func (d *Detector) Unban(source string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	st := d.states[source]
	if st == nil || !st.isBannedAt(d.cfg.Now()) {
		return false
	}
	st.bannedUntil = time.Time{}
	st.streak = 0
	st.hasLast = false
	d.stats.ActiveBans--
	return true
}

// Reset 清空全部来源状态、封禁与累计统计。
func (d *Detector) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.states = make(map[string]*sourceState)
	d.stats = Stats{}
}

// Stats 返回当前统计快照。
func (d *Detector) Stats() Stats {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.stats
}
