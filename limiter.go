package telemetry

import (
	"sort"
	"sync"
	"time"
)

// Config 配置基数额度。额度分两层：每个租户的总存活系列数，
// 以及租户内每个指标的存活系列数。白名单系列不占用额度。
type Config struct {
	TenantQuota int                        // 每个租户允许占用的最大存活系列数（不计白名单）
	MetricQuota int                        // 每个指标允许占用的最大存活系列数（不计白名单）
	TTL         time.Duration              // 系列静默多久后过期回收
	Whitelist   map[string]map[string]bool // metric -> labelKey -> true
}

// Allow 向白名单注册一个指标的标签组合。
func (c *Config) Allow(metric string, labels ...Label) error {
	canon, err := CanonicalLabels(labels)
	if err != nil {
		return err
	}
	if c.Whitelist == nil {
		c.Whitelist = map[string]map[string]bool{}
	}
	m := c.Whitelist[metric]
	if m == nil {
		m = map[string]bool{}
		c.Whitelist[metric] = m
	}
	m[LabelKey(canon)] = true
	return nil
}

func (c *Config) whitelisted(metric, labelKey string) bool {
	return c.Whitelist != nil && c.Whitelist[metric][labelKey]
}

// Sample 是一个遥测样本。Bytes 计入字节账本；Value 为整型便于精确核验守恒。
type Sample struct {
	Tenant string
	Metric string
	Labels []Label
	Value  int64
	Bytes  int64
}

// IngestOutcome 描述一个样本的去向，绝不无声丢弃。
type IngestOutcome int

const (
	AdmittedNew         IngestOutcome = iota // 新系列被录取
	AdmittedExisting                         // 命中存活系列
	AdmittedWhitelisted                      // 白名单录取（不占额度）
	RetractedAdmit                           // 录取并撤回其在溢出桶中的贡献
	Folded                                   // 超额，折叠进溢出聚合桶（计数）
	Rejected                                 // 标签非法，拒绝（计数）
)

func (o IngestOutcome) String() string {
	switch o {
	case AdmittedNew:
		return "admitted-new"
	case AdmittedExisting:
		return "admitted-existing"
	case AdmittedWhitelisted:
		return "admitted-whitelisted"
	case RetractedAdmit:
		return "retracted-admit"
	case Folded:
		return "folded"
	case Rejected:
		return "rejected"
	}
	return "unknown"
}

// IngestResult 是 Ingest 的结果。
type IngestResult struct {
	Outcome    IngestOutcome
	LabelKey   string
	Generation uint64
}

// CorrectOutcome 描述标签更正的结果。
type CorrectOutcome int

const (
	CorrectNoop    CorrectOutcome = iota // 源系列不存在或标签非法
	CorrectedAdmit                       // 更正为新系列并录取
	CorrectedMerge                       // 更正目标已存活，统计被合并
	CorrectedFold                        // 更正后仍超额，折叠进溢出桶
)

// ArchivedGeneration 是一个已过期代际的封存统计，与新代际隔离。
type ArchivedGeneration struct {
	LabelKey   string
	Generation uint64
	Samples    int64
	Bytes      int64
	Value      int64
}

type seriesState struct {
	labels     []Label
	generation uint64
	counted    bool // 是否占用额度（白名单不占）
	samples    int64
	bytes      int64
	value      int64
	expiresAt  time.Time
}

func (s *seriesState) record(v Sample, now time.Time, ttl time.Duration) {
	s.samples++
	s.bytes += v.Bytes
	s.value += v.Value
	s.expiresAt = now.Add(ttl)
}

type contribution struct {
	samples int64
	bytes   int64
	value   int64
}

// overflowBucket 聚合被折叠的样本。每个来源组合的贡献被单独记录，
// 之后该组合被录取时可以原子撤回，不会丢失其统计。
type overflowBucket struct {
	samples       int64
	bytes         int64
	value         int64
	contributions map[string]*contribution // labelKey -> 贡献
}

func newOverflowBucket() *overflowBucket {
	return &overflowBucket{contributions: map[string]*contribution{}}
}

func (o *overflowBucket) add(labelKey string, v Sample) {
	o.samples++
	o.bytes += v.Bytes
	o.value += v.Value
	c := o.contributions[labelKey]
	if c == nil {
		c = &contribution{}
		o.contributions[labelKey] = c
	}
	c.samples++
	c.bytes += v.Bytes
	c.value += v.Value
}

func (o *overflowBucket) addStats(labelKey string, samples, bytes, value int64) {
	o.samples += samples
	o.bytes += bytes
	o.value += value
	c := o.contributions[labelKey]
	if c == nil {
		c = &contribution{}
		o.contributions[labelKey] = c
	}
	c.samples += samples
	c.bytes += bytes
	c.value += value
}

// retract 取走某组合的全部贡献，供迁入新录取的系列。
func (o *overflowBucket) retract(labelKey string) contribution {
	c := o.contributions[labelKey]
	if c == nil {
		return contribution{}
	}
	out := *c
	o.samples -= c.samples
	o.bytes -= c.bytes
	o.value -= c.value
	delete(o.contributions, labelKey)
	return out
}

type metricState struct {
	series       map[string]*seriesState // labelKey -> 存活系列（不含溢出桶）
	liveCounted  int                     // 占用额度的存活系列数
	overflow     *overflowBucket
	archived     []ArchivedGeneration
	nextGen      uint64
	received     int64 // 合法样本接收总数（含被折叠）
	receivedB    int64
	receivedV    int64
	rejected     int64 // 非法标签被拒总数
	foldedTotal  int64 // 历史折叠样本总数
	retractedSum int64 // 历史撤回到存活系列的样本总数
}

func newMetricState() *metricState {
	return &metricState{series: map[string]*seriesState{}, overflow: newOverflowBucket()}
}

type tenantState struct {
	metrics     map[string]*metricState
	liveCounted int
	ledgerBytes int64 // 存活系列（含溢出桶）的字节账本
}

// Limiter 按租户与指标两层限制系列基数。所有额度调整、过期回收、
// 迟到样本与标签更正都在同一把互斥锁下原子完成。
type Limiter struct {
	cfg     Config
	now     func() time.Time
	mu      sync.Mutex
	tenants map[string]*tenantState
}

// NewLimiter 构造限制器。now 可注入假时钟便于回放。
func NewLimiter(cfg Config, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	if cfg.TTL <= 0 {
		cfg.TTL = time.Minute
	}
	return &Limiter{cfg: cfg, now: now, tenants: map[string]*tenantState{}}
}

func (l *Limiter) tenant(name string) *tenantState {
	t := l.tenants[name]
	if t == nil {
		t = &tenantState{metrics: map[string]*metricState{}}
		l.tenants[name] = t
	}
	return t
}

// reclaimLocked 回收租户内所有已过期系列，把统计封存为历史代际，
// 并把额度与字节账本原子归还。过期回收后允许新系列进入。
func (l *Limiter) reclaimLocked(t *tenantState, now time.Time) {
	for _, m := range t.metrics {
		// 按序回收，保证同一事件流回放出的封存代际顺序确定。
		var expired []string
		for key, s := range m.series {
			if !s.expiresAt.After(now) {
				expired = append(expired, key)
			}
		}
		sort.Strings(expired)
		for _, key := range expired {
			s := m.series[key]
			m.archived = append(m.archived, ArchivedGeneration{
				LabelKey:   key,
				Generation: s.generation,
				Samples:    s.samples,
				Bytes:      s.bytes,
				Value:      s.value,
			})
			delete(m.series, key)
			if s.counted {
				m.liveCounted--
				t.liveCounted--
			}
			t.ledgerBytes -= s.bytes
		}
	}
}

// Reclaim 显式触发一次全量过期回收（使用当前时钟）。
func (l *Limiter) Reclaim() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for _, t := range l.tenants {
		l.reclaimLocked(t, now)
	}
}

// Ingest 接收一个样本。返回结果说明样本去向；被折叠与被拒绝的样本
// 都会被计数，绝不无声丢弃。
func (l *Limiter) Ingest(v Sample) IngestResult {
	canon, err := CanonicalLabels(v.Labels)

	l.mu.Lock()
	defer l.mu.Unlock()

	if err != nil || v.Bytes < 0 {
		t := l.tenant(v.Tenant)
		m := l.metric(t, v.Metric)
		m.rejected++
		return IngestResult{Outcome: Rejected}
	}

	now := l.now()
	t := l.tenant(v.Tenant)
	l.reclaimLocked(t, now) // 过期回收与录取决策原子进行
	m := l.metric(t, v.Metric)
	m.received++
	m.receivedB += v.Bytes
	m.receivedV += v.Value

	key := LabelKey(canon)
	if s, ok := m.series[key]; ok {
		s.record(v, now, l.cfg.TTL)
		t.ledgerBytes += v.Bytes
		return IngestResult{Outcome: AdmittedExisting, LabelKey: key, Generation: s.generation}
	}

	whitelisted := l.cfg.whitelisted(v.Metric, key)
	quotaOK := m.liveCounted < l.cfg.MetricQuota && t.liveCounted < l.cfg.TenantQuota
	if whitelisted || quotaOK {
		gen := m.nextGen
		m.nextGen++
		s := &seriesState{labels: canon, generation: gen, counted: !whitelisted}
		s.record(v, now, l.cfg.TTL)
		outcome := AdmittedNew
		if whitelisted {
			outcome = AdmittedWhitelisted
		}
		// 该组合此前可能被折叠过：撤回其在溢出桶中的贡献，
		// 迁入新系列，统计不丢失。撤回只改变归属，不动账本。
		if c := m.overflow.retract(key); c.samples > 0 {
			s.samples += c.samples
			s.bytes += c.bytes
			s.value += c.value
			m.retractedSum += c.samples
			outcome = RetractedAdmit
		}
		m.series[key] = s
		if s.counted {
			m.liveCounted++
			t.liveCounted++
		}
		t.ledgerBytes += v.Bytes
		return IngestResult{Outcome: outcome, LabelKey: key, Generation: gen}
	}

	// 超额：折叠进溢出聚合桶并计数。
	m.overflow.add(key, v)
	m.foldedTotal++
	t.ledgerBytes += v.Bytes
	return IngestResult{Outcome: Folded, LabelKey: key}
}

func (l *Limiter) metric(t *tenantState, name string) *metricState {
	m := t.metrics[name]
	if m == nil {
		m = newMetricState()
		t.metrics[name] = m
	}
	return m
}

// Correct 原子地把一个系列的标签从 old 更正为 new：额度、统计与
// 字节账本在同一把锁内迁移。源可以是存活系列，也可以是溢出桶中
// 被折叠的贡献；目标按当前额度决定录取、合并或继续折叠。
func (l *Limiter) Correct(tenant, metric string, oldLabels, newLabels []Label) CorrectOutcome {
	oldC, errOld := CanonicalLabels(oldLabels)
	newC, errNew := CanonicalLabels(newLabels)

	l.mu.Lock()
	defer l.mu.Unlock()

	t := l.tenants[tenant]
	if t == nil || errOld != nil || errNew != nil {
		return CorrectNoop
	}
	now := l.now()
	l.reclaimLocked(t, now)
	m := t.metrics[metric]
	if m == nil {
		return CorrectNoop
	}
	oldKey, newKey := LabelKey(oldC), LabelKey(newC)
	if oldKey == newKey {
		return CorrectNoop
	}

	// 从源取走统计（存活系列或溢出贡献），并原子归还其额度与账本。
	var samples, bytes, value int64
	found := false
	fromOverflow := false
	if s, ok := m.series[oldKey]; ok {
		samples, bytes, value = s.samples, s.bytes, s.value
		delete(m.series, oldKey)
		if s.counted {
			m.liveCounted--
			t.liveCounted--
		}
		t.ledgerBytes -= s.bytes
		found = true
	} else if c := m.overflow.contributions[oldKey]; c != nil {
		samples, bytes, value = c.samples, c.bytes, c.value
		m.overflow.retract(oldKey)
		t.ledgerBytes -= c.bytes
		found = true
		fromOverflow = true
	}
	if !found {
		return CorrectNoop
	}

	// 目标已存活：直接合并。
	if s, ok := m.series[newKey]; ok {
		s.samples += samples
		s.bytes += bytes
		s.value += value
		t.ledgerBytes += bytes
		if fromOverflow {
			m.retractedSum += samples
		}
		return CorrectedMerge
	}

	whitelisted := l.cfg.whitelisted(metric, newKey)
	quotaOK := m.liveCounted < l.cfg.MetricQuota && t.liveCounted < l.cfg.TenantQuota
	if whitelisted || quotaOK {
		gen := m.nextGen
		m.nextGen++
		s := &seriesState{
			labels:     newC,
			generation: gen,
			counted:    !whitelisted,
			samples:    samples,
			bytes:      bytes,
			value:      value,
			expiresAt:  now.Add(l.cfg.TTL),
		}
		if c := m.overflow.retract(newKey); c.samples > 0 {
			s.samples += c.samples
			s.bytes += c.bytes
			s.value += c.value
			m.retractedSum += c.samples
		}
		m.series[newKey] = s
		if s.counted {
			m.liveCounted++
			t.liveCounted++
		}
		t.ledgerBytes += bytes // 仅迁移来的字节；撤回部分本就在账本中
		if fromOverflow {
			m.retractedSum += samples
		}
		return CorrectedAdmit
	}

	// 目标仍超额：统计以新组合名义继续留在溢出桶。
	m.overflow.addStats(newKey, samples, bytes, value)
	t.ledgerBytes += bytes
	return CorrectedFold
}
