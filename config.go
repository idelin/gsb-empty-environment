package auditjoin

import (
	"sync"
	"time"
)

// Config 控制组装器行为。零值字段会在 New 时套用 DefaultConfig 的默认值。
type Config struct {
	// PublishTimeout 是未收齐片段的事件在强制发布前的等待时长；0 表示不超时。
	PublishTimeout time.Duration
	// CorrectionWindow 是发布后仍接受迟到片段做补正的时长；0 表示不封板、无限期补正。
	CorrectionWindow time.Duration
	// WrapModulus 是事件序号的回绕模数；0 表示序号不回绕。
	WrapModulus uint64
	// RequiredShards 是参与全局水位线判定的分片集合；为空时表示所有上报过水位线的分片。
	RequiredShards []ShardID
	// SweepInterval > 0 时启动后台 goroutine 周期执行 Sweep；0 表示仅手动驱动。
	SweepInterval time.Duration
	// Clock 用于注入时间，默认 time.Now。
	Clock func() time.Time
}

// DefaultConfig 返回推荐默认配置：30 秒超时、5 分钟补正窗口、不回绕。
func DefaultConfig() Config {
	return Config{
		PublishTimeout:   30 * time.Second,
		CorrectionWindow: 5 * time.Minute,
	}
}

// AssemblerStats 暴露组装器的可观测计数。
type AssemblerStats struct {
	Ingested           int64 `json:"ingested"`
	DuplicateFragments int64 `json:"duplicate_fragments"`
	Conflicts          int64 `json:"conflicts"`
	CompletePublished  int64 `json:"complete_published"`
	TimeoutPublished   int64 `json:"timeout_published"`
	WatermarkPublished int64 `json:"watermark_published"`
	Corrections        int64 `json:"corrections"`
	SealedRejections   int64 `json:"sealed_rejections"`
	IdempotentPuts     int64 `json:"idempotent_puts"`
	StoreConflicts     int64 `json:"store_conflicts"`
}

// SweepResult 汇总一次 Sweep 的动作。
type SweepResult struct {
	Published []EventKey
	Corrected []EventKey
	Sealed    []EventKey
	Stats     AssemblerStats
}

type streamKey struct {
	Node NodeID     `json:"node"`
	Gen  Generation `json:"generation"`
}

type compositeKey struct {
	Key   EventKey `json:"key"`
	Cycle uint64   `json:"cycle"`
}

type ringState struct {
	LastRaw       uint64 `json:"last_raw"`
	Cycle         uint64 `json:"cycle"`
	InitialRawSet bool   `json:"initial_raw_set"`
}

type shardWatermark struct {
	Raw uint64 `json:"raw"`
}

// openEntry 保存一个组装中的事件。发布后条目继续保留，用于迟到补正与封板判定。
type openEntry struct {
	Key               EventKey                `json:"key"`
	Cycle             uint64                  `json:"cycle"`
	Frags             map[FragmentID]Fragment `json:"frags"`
	Order             []FragmentID            `json:"order"`
	ExpectedFragments int                     `json:"expected_fragments"`
	Deadline          time.Time               `json:"deadline"`
	PublishedVersion  uint64                  `json:"published_version"`
	PublishedAt       time.Time               `json:"published_at"`
	Complete          bool                    `json:"complete"`
	// DirtySincePublish 标记自上次发布后是否收到新片段，决定补正是否递增版本。
	DirtySincePublish bool `json:"dirty_since_publish"`
}

type ringDTO struct {
	Stream streamKey `json:"stream"`
	Ring   ringState `json:"ring"`
}

type watermarkDTO struct {
	Shard     ShardID        `json:"shard"`
	Watermark shardWatermark `json:"watermark"`
}

type assemblerSnapshot struct {
	Open       []openEntry    `json:"open"`
	Rings      []ringDTO      `json:"rings"`
	Watermarks []watermarkDTO `json:"watermarks"`
	Stats      AssemblerStats `json:"stats"`
}

// Assembler 是带水位线的审计记录组装器，并发安全。
type Assembler struct {
	cfg       Config
	store     Store
	statePath string
	now       func() time.Time

	mu         sync.Mutex
	open       map[compositeKey]*openEntry
	rings      map[streamKey]ringState
	watermarks map[ShardID]shardWatermark
	required   map[ShardID]bool
	stats      AssemblerStats
	closed     bool

	stop chan struct{}
	done chan struct{}
}

// New 创建组装器。statePath 非空时组装状态以 JSON 原子落盘，
// 重新 New 同一 path 即可恢复未发布/待补正条目，模拟消费者重启。
func New(cfg Config, store Store, statePath string) (*Assembler, error) {
	d := DefaultConfig()
	if cfg.PublishTimeout == 0 {
		cfg.PublishTimeout = d.PublishTimeout
	}
	if cfg.CorrectionWindow == 0 {
		cfg.CorrectionWindow = d.CorrectionWindow
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	a := &Assembler{
		cfg:        cfg,
		store:      store,
		statePath:  statePath,
		now:        cfg.Clock,
		open:       make(map[compositeKey]*openEntry),
		rings:      make(map[streamKey]ringState),
		watermarks: make(map[ShardID]shardWatermark),
		required:   make(map[ShardID]bool),
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
	}
	for _, sid := range cfg.RequiredShards {
		a.required[sid] = true
	}
	if statePath != "" {
		if err := a.loadLocked(); err != nil {
			return nil, err
		}
	}
	if cfg.SweepInterval > 0 {
		go a.backgroundSweep()
	}
	return a, nil
}

// Close 停止后台 Sweep 并落盘一次状态。
func (a *Assembler) Close() error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	a.mu.Unlock()

	if a.cfg.SweepInterval > 0 {
		close(a.stop)
		<-a.done
	} else {
		close(a.stop)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	return a.persistLocked()
}

// Stats 返回组装器计数快照。
func (a *Assembler) Stats() AssemblerStats {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stats
}

func (a *Assembler) deadline(now time.Time) time.Time {
	if a.cfg.PublishTimeout <= 0 {
		return time.Time{}
	}
	return now.Add(a.cfg.PublishTimeout)
}
