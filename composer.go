package netsec

import (
	"fmt"
	"sync"
	"time"
)

// 默认最大追踪跳数，与常见解析器实现的保守上限一致。
const defaultMaxHops = 16

type clock func() time.Time

type cachedAlias struct {
	target    string
	zone      string
	expiresAt time.Time
}

// Composer 在底层 Resolver 之上组合 CNAME 追踪结果。
// 它保证：拒绝循环与相互矛盾的别名，且否定结论的作用域只落在被证明的末端。
type Composer struct {
	resolver Resolver
	now      clock
	maxHops  int

	mu      sync.Mutex
	aliases map[string]cachedAlias
}

// Option 调整 Composer 行为，主要用于测试注入时钟与跳数上限。
type Option func(*Composer)

// WithClock 注入固定或可控时钟，决定各跳有效期的绝对失效时刻。
func WithClock(now func() time.Time) Option {
	return func(c *Composer) { c.now = now }
}

// WithMaxHops 覆盖默认的最大追踪跳数。
func WithMaxHops(n int) Option {
	return func(c *Composer) {
		if n > 0 {
			c.maxHops = n
		}
	}
}

// NewComposer 用给定的底层解析器创建组合器。
func NewComposer(r Resolver, opts ...Option) *Composer {
	c := &Composer{
		resolver: r,
		now:      time.Now,
		maxHops:  defaultMaxHops,
		aliases:  make(map[string]cachedAlias),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Resolve 沿 CNAME 追踪 (qname, qtype)，返回带完整信任路径的组合结果。
// 途中查询失败时返回 *IncompleteChainError，其中保留已确认的部分路径，
// 且绝不会把失败伪装成末端不存在。
func (c *Composer) Resolve(qname string, qtype uint16) (*Result, error) {
	start := canonicalName(qname)
	if start == "" {
		return nil, fmt.Errorf("netsec: 查询名字为空")
	}

	visited := map[string]bool{start: true}
	current := start
	var hops []Hop
	var path []Alias

	for {
		if len(hops) >= c.maxHops {
			return nil, &ChainLimitError{Limit: c.maxHops, Name: current}
		}

		msg, err := c.resolver.Query(current, qtype)
		if err != nil {
			return nil, &IncompleteChainError{
				QueryName: start,
				Partial: &PartialPath{
					QueryName: start,
					Hops:      cloneHops(hops),
					Path:      clonePath(path),
				},
				Err: err,
			}
		}
		if msg == nil {
			return nil, errEmptyReply
		}
		if canonicalName(msg.QueryName) != current || msg.QueryType != qtype {
			return nil, errQueryMismatch
		}

		switch msg.Rcode {
		case RcodeNXDomain:
			hops = append(hops, c.negativeHop(msg, current, qtype, true))
			return c.negativeResult(start, qtype, hops, path), nil

		case RcodeNoError:
			done, ferr := c.processNoData(msg, &current, qtype, visited, &hops, &path)
			if ferr != nil {
				return nil, ferr
			}
			if !done {
				// 本应答只推进了别名，末端需要再次发起查询。
				continue
			}

			records := terminalRecords(msg, current, qtype)
			if len(records) > 0 {
				ttl := minTTL(records)
				hops = append(hops, Hop{
					QueryName: current,
					QueryType: qtype,
					Zone:      replyZone(msg),
					TTL:       ttl,
					ExpiresAt: c.now().Add(ttl),
				})
				return c.positiveResult(start, qtype, hops, path, records), nil
			}

			// NOERROR 但无可用数据：NODATA，只否定 (末端, 查询类型)。
			hops = append(hops, c.negativeHop(msg, current, qtype, false))
			return c.negativeResult(start, qtype, hops, path), nil

		default:
			// SERVFAIL/REFUSED 等不是可缓存的否定结论，绝不冒充 NXDOMAIN/NODATA。
			return nil, fmt.Errorf("%w: rcode=%d 名字=%s", errUnexpectedRcode, msg.Rcode, current)
		}
	}
}

// processNoData 在一个 NOERROR 应答内沿 CNAME 推进，可处理单条或整条别名链。
// done=true 表示本应答已覆盖末端，调用方可读取数据或判定 NODATA；
// done=false 表示别名已推进但末端不在本应答内，需要再次查询。
func (c *Composer) processNoData(
	msg *Message,
	current *string,
	qtype uint16,
	visited map[string]bool,
	hops *[]Hop,
	path *[]Alias,
) (done bool, err error) {
	advanced := false
	for {
		target, cnameRRs, cerr := followCNAME(msg, *current, qtype)
		if cerr != nil {
			return false, cerr
		}
		records := terminalRecords(msg, *current, qtype)

		if len(cnameRRs) > 0 && len(records) > 0 {
			return false, &ContradictionError{
				Name:    *current,
				TargetA: target,
				Reason:  "同一名字同时存在 CNAME 与查询类型数据，违反 RFC 1034 别名规则",
			}
		}
		if target == "" {
			// 本消息没有关于 current 的 CNAME。
			// 若本消息确实覆盖 current（有任意应答记录），或一次都没推进过，
			// 则 current 即末端，由调用方区分数据与 NODATA；
			// 否则只是上一条 CNAME 的目标不在本消息内，需要发起下一跳查询。
			return !advanced || messageMentions(msg, *current), nil
		}

		target = canonicalName(target)
		if visited[target] {
			return false, &LoopError{Name: target, Path: visitedNames(visited, *current)}
		}
		if conflict := c.checkAlias(*current, target); conflict != nil {
			return false, conflict
		}

		ttl := minTTL(cnameRRs)
		zone := replyZone(msg)
		*hops = append(*hops, Hop{
			QueryName:   *current,
			QueryType:   qtype,
			Zone:        zone,
			AliasTarget: target,
			TTL:         ttl,
			ExpiresAt:   c.now().Add(ttl),
		})
		*path = append(*path, Alias{From: *current, To: target, Zone: zone, TTL: ttl})
		c.storeAlias(*current, target, zone, c.now().Add(ttl))

		visited[target] = true
		*current = target
		advanced = true
	}
}

// followCNAME 返回 current 名字应跟随的 CNAME 目标；
// 同一名字出现多个不同目标即构成矛盾。显式查询 CNAME 类型时不跟随。
func followCNAME(msg *Message, current string, qtype uint16) (string, []RR, error) {
	if qtype == TypeCNAME {
		return "", nil, nil
	}
	var out []RR
	target := ""
	for _, rr := range msg.Answer {
		if canonicalName(rr.Name) != current || rr.Type != TypeCNAME {
			continue
		}
		want := canonicalName(rr.Target)
		switch {
		case target == "":
			target = want
		case target != want:
			return "", nil, &ContradictionError{
				Name:    current,
				TargetA: target,
				TargetB: want,
				Reason:  "同一应答中同一名字出现多条不同目标的 CNAME",
			}
		}
		out = append(out, rr)
	}
	return target, out, nil
}

func terminalRecords(msg *Message, current string, qtype uint16) []RR {
	var out []RR
	for _, rr := range msg.Answer {
		if canonicalName(rr.Name) == current && rr.Type == qtype {
			out = append(out, rr)
		}
	}
	return out
}

// messageMentions 判断应答的 Answer 段是否包含该名字的任意记录，
// 用于区分“末端就在本消息内”与“别名目标需要再次查询”。
func messageMentions(msg *Message, name string) bool {
	for _, rr := range msg.Answer {
		if canonicalName(rr.Name) == name {
			return true
		}
	}
	return false
}

func (c *Composer) negativeHop(msg *Message, name string, qtype uint16, nxdomain bool) Hop {
	ttl := negativeTTL(msg)
	proofType := qtype
	if nxdomain {
		proofType = TypeAny
	}
	zone := replyZone(msg)
	return Hop{
		QueryName: name,
		QueryType: qtype,
		Zone:      zone,
		TTL:       ttl,
		ExpiresAt: c.now().Add(ttl),
		Negative: &NegativeProof{
			Name:     name,
			Type:     proofType,
			Zone:     zone,
			TTL:      ttl,
			NXDOMAIN: nxdomain,
		},
	}
}

func (c *Composer) positiveResult(
	qname string, qtype uint16, hops []Hop, path []Alias, records []RR,
) *Result {
	return &Result{
		QueryName: qname,
		QueryType: qtype,
		Path:      clonePath(path),
		Records:   records,
		Hops:      cloneHops(hops),
		MinTTL:    minHopTTL(hops),
	}
}

func (c *Composer) negativeResult(
	qname string, qtype uint16, hops []Hop, path []Alias,
) *Result {
	last := hops[len(hops)-1]
	return &Result{
		QueryName: qname,
		QueryType: qtype,
		Path:      clonePath(path),
		Hops:      cloneHops(hops),
		Negative:  last.Negative,
		MinTTL:    minHopTTL(hops),
	}
}

func (c *Composer) checkAlias(name, target string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	got, ok := c.aliases[name]
	if !ok || !c.now().Before(got.expiresAt) {
		return nil
	}
	if got.target != target {
		return &ContradictionError{
			Name:    name,
			TargetA: got.target,
			TargetB: target,
			Reason:  "别名目标与仍在有效期内的既有结论冲突",
		}
	}
	return nil
}

func (c *Composer) storeAlias(name, target, zone string, expiresAt time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.aliases[name] = cachedAlias{target: target, zone: zone, expiresAt: expiresAt}
}

func negativeTTL(msg *Message) time.Duration {
	if msg.NegativeTTL > 0 {
		return msg.NegativeTTL
	}
	for _, rr := range msg.Authority {
		if rr.Type == TypeSOA {
			return rr.TTL
		}
	}
	return 0
}

func replyZone(msg *Message) string {
	if msg.Zone != "" {
		return canonicalName(msg.Zone)
	}
	for _, rr := range msg.Authority {
		if rr.Type == TypeSOA {
			return canonicalName(rr.Name)
		}
	}
	return ""
}

func minTTL(rrs []RR) time.Duration {
	if len(rrs) == 0 {
		return 0
	}
	best := rrs[0].TTL
	for _, rr := range rrs[1:] {
		if rr.TTL < best {
			best = rr.TTL
		}
	}
	return best
}

func minHopTTL(hops []Hop) time.Duration {
	if len(hops) == 0 {
		return 0
	}
	best := hops[0].TTL
	for _, hop := range hops[1:] {
		if hop.TTL < best {
			best = hop.TTL
		}
	}
	return best
}

func cloneHops(in []Hop) []Hop {
	out := make([]Hop, len(in))
	copy(out, in)
	return out
}

func clonePath(in []Alias) []Alias {
	out := make([]Alias, len(in))
	copy(out, in)
	return out
}

func visitedNames(visited map[string]bool, current string) []string {
	out := make([]string, 0, len(visited))
	for name := range visited {
		if name != current {
			out = append(out, name)
		}
	}
	return append(out, current)
}
