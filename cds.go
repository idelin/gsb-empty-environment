package netsec

import "time"

// ServerObservation 是单台权威服务器在一轮查询中返回的 CDS/CDNSKEY 结果。
// 切片为 nil 表示该类型记录不存在；记录存在时必须同时提供对应 RRSIG。
type ServerObservation struct {
	Server     string
	CDS        []DSRecord
	CDSSig     *RRSIGRecord
	CDNSKEY    []DNSKEYRecord
	CDNSKEYSig *RRSIGRecord
}

// Poll 是一轮对全部权威服务器的并行观察结果。
type Poll struct {
	Zone    string
	At      time.Time
	Servers []ServerObservation
}

// Config 控制接纳策略。
type Config struct {
	// RequireHours 记录集须连续保持一致的小时数，必须 >0。
	RequireHours int
	// PollInterval 两轮观察之间的预期间隔；超过 Interval+MaxClockSkew
	// 视为观察中断。默认 1 小时。
	PollInterval time.Duration
	// MaxClockSkew 轮询时刻与本地时钟、相邻轮询间隔的容差。默认 5 分钟。
	MaxClockSkew time.Duration
	// DefaultDigestType 仅存在 CDNSKEY 时推导 DS 所用摘要算法。默认 SHA-256。
	DefaultDigestType uint8
	// Now 注入本地时钟，便于测试；默认 time.Now。
	Now func() time.Time
}

// Decision 是一轮观察后的接纳判定。未达接纳条件时 Action 为 ActionPending。
type Decision struct {
	Action string
	// NewDS 为 ActionReplace 时父区应发布的 DS 集（已去敏拷贝，可直接持有）。
	NewDS []DSRecord
	// Since 是当前一致记录集首次被观察到的时间。
	Since time.Time
	// ObservedFor 是当前记录集已连续观察到的时长。
	ObservedFor time.Duration
	// Fingerprint 是本轮记录集的规范指纹，变化即意味着信号替换、观察重新计时。
	Fingerprint string
}

// Tracker 维护 CDS/CDNSKEY 的连续观察状态，零值不可用，须经 NewTracker 构造。
type Tracker struct {
	cfg       Config
	currentDS []DSRecord
	published []Signer

	fp     string
	since  time.Time
	lastAt time.Time
}

// acceptedSet 是一轮观察通过全部校验后的规范化结果。
type acceptedSet struct {
	fingerprint string
	delete      bool
	cds         []DSRecord
	cdnskey     []DNSKEYRecord
}

// NewTracker 校验配置并构造跟踪器。currentDS 是父区当前发布的 DS 集
// （用于锚定合法签名 KSK），published 是子区当前已发布的 DNSKEY 集
// （用于校验新 DS/CDNSKEY 的对应关系）。
func NewTracker(cfg Config, currentDS []DSRecord, published []Signer) (*Tracker, error) {
	if cfg.RequireHours <= 0 {
		return nil, ruleErr(RuleConfig, "RequireHours must be positive", ErrConfig)
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Hour
	}
	if cfg.MaxClockSkew <= 0 {
		cfg.MaxClockSkew = 5 * time.Minute
	}
	if cfg.DefaultDigestType == 0 {
		cfg.DefaultDigestType = DigestSHA256
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if len(currentDS) == 0 {
		return nil, ruleErr(RuleConfig, "at least one current DS is required", ErrConfig)
	}
	if len(published) == 0 {
		return nil, ruleErr(RuleConfig, "published DNSKEY set must not be empty", ErrConfig)
	}
	return &Tracker{
		cfg:       cfg,
		currentDS: cloneDS(currentDS),
		published: published,
	}, nil
}

// Observe 送入一轮观察并返回当前判定。任何校验失败、服务器分歧或观察
// 中断都会清空连续观察状态，调用方须从下一轮重新积累观察小时数；此时
// 返回的 Decision.Action 恒为 ActionPending，失败原因经 error 返回
// （可用 errors.Is 匹配 errors.go 中的哨兵错误）。
func (t *Tracker) Observe(p Poll) (*Decision, error) {
	set, err := t.validatePoll(p)
	if err != nil {
		t.reset()
		return t.pendingDecision(nil, p.At), err
	}

	now := t.cfg.Now()
	skew := now.Sub(p.At)
	if skew > t.cfg.MaxClockSkew || skew < -t.cfg.MaxClockSkew {
		t.reset()
		return t.pendingDecision(nil, p.At),
			ruleErr(RuleObservationGap, "poll timestamp outside local clock tolerance", ErrObservationGap)
	}

	if t.fp != "" && t.fp != set.fingerprint {
		// 信号内容发生变化：旧观察作废，从本轮重新计时。
		t.fp, t.since, t.lastAt = "", time.Time{}, time.Time{}
	} else if !t.lastAt.IsZero() && p.At.Sub(t.lastAt) > t.cfg.PollInterval+t.cfg.MaxClockSkew {
		t.reset()
		return t.pendingDecision(nil, p.At),
			ruleErr(RuleObservationGap, "gap between consecutive polls exceeds interval", ErrObservationGap)
	}

	if t.fp == "" {
		t.fp = set.fingerprint
		t.since = p.At
	}
	t.lastAt = p.At

	observed := p.At.Sub(t.since)
	required := time.Duration(t.cfg.RequireHours) * time.Hour
	if observed < required {
		return t.pendingDecision(set, p.At),
			ruleErr(RuleObservationShort, "consistent observation window not yet satisfied", ErrObservationShort)
	}

	decision := &Decision{
		Action:      ActionReplace,
		Since:       t.since,
		ObservedFor: observed,
		Fingerprint: set.fingerprint,
	}
	if set.delete {
		decision.Action = ActionDelete
		return decision, nil
	}
	if len(set.cds) > 0 {
		decision.NewDS = cloneDS(set.cds)
	} else {
		// 仅提交 CDNSKEY 删除信号（0 3 0 00）时同样走删除分支。
		if len(set.cdnskey) > 0 && allDeleteDNSKEY(set.cdnskey) {
			decision.Action = ActionDelete
			return decision, nil
		}
		derived, err := deriveDS(p.Zone, set.cdnskey, t.cfg.DefaultDigestType)
		if err != nil {
			t.reset()
			return t.pendingDecision(nil, p.At), err
		}
		decision.NewDS = derived
	}
	return decision, nil
}

func (t *Tracker) pendingDecision(set *acceptedSet, at time.Time) *Decision {
	d := &Decision{Action: ActionPending, Since: at}
	if set != nil {
		d.Fingerprint = set.fingerprint
	}
	if !t.since.IsZero() {
		d.Since = t.since
		d.ObservedFor = at.Sub(t.since)
	}
	return d
}

func (t *Tracker) reset() {
	t.fp = ""
	t.since = time.Time{}
	t.lastAt = time.Time{}
}

func (t *Tracker) validatePoll(p Poll) (*acceptedSet, error) {
	if p.Zone == "" || len(p.Servers) == 0 {
		return nil, ruleErr(RuleMalformed, "poll must name a zone and at least one server", ErrMalformed)
	}
	if p.At.IsZero() {
		return nil, ruleErr(RuleMalformed, "poll timestamp is zero", ErrMalformed)
	}

	now32 := uint32(p.At.Unix())
	firstFP := ""
	out := &acceptedSet{}
	for _, srv := range p.Servers {
		cdsSet, cdnsSet, err := t.validateServer(p.Zone, srv, now32)
		if err != nil {
			return nil, err
		}
		fp := fingerprint(cdsSet, cdnsSet)
		if firstFP == "" {
			firstFP = fp
			out.fingerprint = fp
			out.delete = (len(cdsSet) > 0 && allDeleteDS(cdsSet)) ||
				(len(cdnsSet) > 0 && allDeleteDNSKEY(cdnsSet))
			out.cds = cloneDS(cdsSet)
			out.cdnskey = cloneDNSKEY(cdnsSet)
		} else if fp != firstFP {
			return nil, ruleErr(RuleServerDivergence,
				"server "+srv.Server+" returned a different CDS/CDNSKEY set", ErrServerDivergence)
		}
	}
	return out, nil
}

func (t *Tracker) validateServer(zone string, srv ServerObservation, now uint32) ([]DSRecord, []DNSKEYRecord, error) {
	if len(srv.CDS) == 0 && len(srv.CDNSKEY) == 0 {
		return nil, nil, ruleErr(RuleMalformed,
			"server "+srv.Server+" returned neither CDS nor CDNSKEY", ErrMalformed)
	}

	var cds []DSRecord
	if len(srv.CDS) > 0 {
		if srv.CDSSig == nil {
			return nil, nil, ruleErr(RuleRRSIGUnsigned, "CDS RRset unsigned at "+srv.Server, ErrRRSIGUnsigned)
		}
		if err := t.validateSignedSet(zone, RRset{Name: zone, Type: TypeCDS,
			Records: dsAsRecords(srv.CDS), Sig: srv.CDSSig}, now, srv.Server); err != nil {
			return nil, nil, err
		}
		cds = cloneDS(srv.CDS)
		if err := validateCDSStructure(cds); err != nil {
			return nil, nil, err
		}
	}

	var cdns []DNSKEYRecord
	if len(srv.CDNSKEY) > 0 {
		if srv.CDNSKEYSig == nil {
			return nil, nil, ruleErr(RuleRRSIGUnsigned, "CDNSKEY RRset unsigned at "+srv.Server, ErrRRSIGUnsigned)
		}
		if err := t.validateSignedSet(zone, RRset{Name: zone, Type: TypeCDNSKEY,
			Records: dnskeyAsRecords(srv.CDNSKEY), Sig: srv.CDNSKEYSig}, now, srv.Server); err != nil {
			return nil, nil, err
		}
		cdns = cloneDNSKEY(srv.CDNSKEY)
		if err := validateCDNSKEYStructure(cdns); err != nil {
			return nil, nil, err
		}
	}

	cdsDelete := len(cds) > 0 && allDeleteDS(cds)
	cdnsDelete := len(cdns) > 0 && allDeleteDNSKEY(cdns)
	if len(cds) > 0 && len(cdns) > 0 && cdsDelete != cdnsDelete {
		return nil, nil, ruleErr(RuleCDSCDNSKEYMismatch,
			"delete signal present in only one of CDS/CDNSKEY at "+srv.Server, ErrCDSCDNSKEYMismatch)
	}
	if cdsDelete || cdnsDelete {
		return cds, cdns, nil
	}

	if len(cds) > 0 {
		for _, d := range cds {
			if !t.dsMatchesPublished(zone, d) {
				return nil, nil, ruleErr(RuleNewDSNoDNSKEY,
					"CDS keytag "+itoa(d.KeyTag)+" has no corresponding published DNSKEY at "+srv.Server,
					ErrNewDSNoDNSKEY)
			}
		}
	}
	if len(cdns) > 0 {
		for _, k := range cdns {
			if !dnskeyPublished(k, t.published) {
				return nil, nil, ruleErr(RuleNewDSNoDNSKEY,
					"CDNSKEY is not present verbatim in the published DNSKEY set at "+srv.Server,
					ErrNewDSNoDNSKEY)
			}
		}
	}
	if len(cds) > 0 && len(cdns) > 0 {
		if err := validateCorrespondence(zone, cds, cdns); err != nil {
			return nil, nil, err
		}
	}
	return cds, cdns, nil
}

// validateSignedSet 校验 RRset 签名，并强制签名钥是当前 DS 所指的 KSK。
func (t *Tracker) validateSignedSet(zone string, set RRset, now uint32, server string) error {
	signingKey, err := verifyRRset(set, now, t.published)
	if err != nil {
		return err
	}
	if !signingKey.IsKSK() {
		return ruleErr(RuleSignerNotKSK, "signer at "+server+" lacks SEP bit", ErrSignerNotKSK)
	}
	for _, d := range t.currentDS {
		if DSMatchesDNSKEY(d, zone, signingKey) {
			return nil
		}
	}
	return ruleErr(RuleSignerNotAnchored,
		"signing KSK at "+server+" is not covered by a current DS", ErrSignerNotAnchored)
}

func (t *Tracker) dsMatchesPublished(zone string, d DSRecord) bool {
	for _, s := range t.published {
		if DSMatchesDNSKEY(d, zone, s.Key) {
			return true
		}
	}
	return false
}

func dnskeyPublished(want DNSKEYRecord, published []Signer) bool {
	for _, s := range published {
		if s.Key.Flags == want.Flags && s.Key.Protocol == want.Protocol &&
			s.Key.Algorithm == want.Algorithm && bytesEqual(s.Key.PublicKey, want.PublicKey) {
			return true
		}
	}
	return false
}
