package netsec

import (
	"context"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// Result 是策略评估的四种结果前缀外加"无策略"。
type Result int

const (
	None     Result = iota // 域没有发布策略
	Pass                   // +
	Fail                   // -
	SoftFail               // ~
	Neutral                // ?
)

func (r Result) String() string {
	switch r {
	case Pass:
		return "pass"
	case Fail:
		return "fail"
	case SoftFail:
		return "softfail"
	case Neutral:
		return "neutral"
	default:
		return "none"
	}
}

// ErrorKind 区分永久错误与临时错误。
type ErrorKind int

const (
	PermError ErrorKind = iota // 策略环、预算耗尽、记录格式错误等
	TempError                  // DNS 临时故障，向上传播
)

// Error 是评估过程中产生的策略级错误。
type Error struct {
	Kind    ErrorKind
	Message string
}

func (e *Error) Error() string {
	if e.Kind == TempError {
		return "temperror: " + e.Message
	}
	return "permerror: " + e.Message
}

func permErrorf(format string, args ...any) *Error {
	return &Error{Kind: PermError, Message: fmt.Sprintf(format, args...)}
}

func tempErrorf(format string, args ...any) *Error {
	return &Error{Kind: TempError, Message: fmt.Sprintf(format, args...)}
}

// DefaultMaxLookups 是单次评估允许的唯一域 DNS 查询上限。
const DefaultMaxLookups = 10

// Evaluator 供收信宿主调用，执行发送域策略。
type Evaluator struct {
	Resolver Resolver
	// MaxLookups 为单次 CheckHost 的 DNS 查询预算（含 include/redirect
	// 递归共享），<=0 时使用 DefaultMaxLookups。同一域重复查询走缓存，
	// 不重复消耗预算。
	MaxLookups int
}

// CheckHost 评估 domain 发布的策略对 ip 的判定结果。
func (e *Evaluator) CheckHost(ctx context.Context, ip netip.Addr, domain string) (Result, error) {
	max := e.MaxLookups
	if max <= 0 {
		max = DefaultMaxLookups
	}
	s := &session{
		resolver: e.Resolver,
		budget:   max,
		cache:    make(map[string]*policy),
		active:   make(map[string]bool),
	}
	return s.evalDomain(ctx, ip, domain)
}

// session 承载一次 CheckHost 的共享状态：预算、缓存与递归栈。
type session struct {
	resolver Resolver
	budget   int
	cache    map[string]*policy // nil 值表示该域无策略记录
	active   map[string]bool
}

func (s *session) evalDomain(ctx context.Context, ip netip.Addr, domain string) (Result, error) {
	if s.active[domain] {
		return None, permErrorf("policy loop detected at %q", domain)
	}
	pol, err := s.policy(ctx, domain)
	if err != nil {
		return None, err
	}
	if pol == nil {
		return None, nil
	}
	s.active[domain] = true
	defer delete(s.active, domain)
	return s.evalPolicy(ctx, ip, pol)
}

// policy 取回并解析域的策略，带缓存；只有缓存未命中才消耗预算。
func (s *session) policy(ctx context.Context, domain string) (*policy, error) {
	if pol, ok := s.cache[domain]; ok {
		return pol, nil
	}
	if s.budget <= 0 {
		return nil, permErrorf("DNS lookup limit exceeded at %q", domain)
	}
	s.budget--
	txts, err := s.resolver.LookupTXT(ctx, domain)
	if err != nil {
		if de, ok := err.(*DNSError); ok && de.Temporary {
			return nil, tempErrorf("lookup %q: %s", domain, de.Message)
		}
		return nil, permErrorf("lookup %q: %s", domain, err)
	}
	var records []string
	for _, txt := range txts {
		if fields := strings.Fields(txt); len(fields) > 0 && fields[0] == "v=spf1" {
			records = append(records, txt)
		}
	}
	if len(records) == 0 {
		s.cache[domain] = nil
		return nil, nil
	}
	if len(records) > 1 {
		return nil, permErrorf("domain %q publishes multiple policy records", domain)
	}
	pol, err := parsePolicy(records[0])
	if err != nil {
		return nil, err
	}
	s.cache[domain] = pol
	return pol, nil
}

func (s *session) evalPolicy(ctx context.Context, ip netip.Addr, pol *policy) (Result, error) {
	for _, m := range pol.mechanisms {
		matched, res, err := s.evalMechanism(ctx, ip, m)
		if err != nil {
			return None, err
		}
		if matched {
			return res, nil
		}
	}
	// redirect 仅在所有机制都未匹配时执行。
	if pol.hasRedirect {
		res, err := s.evalDomain(ctx, ip, pol.redirect)
		if err != nil {
			return None, err
		}
		if res == None {
			return None, permErrorf("redirect target %q has no policy", pol.redirect)
		}
		return res, nil
	}
	return Neutral, nil
}

func (s *session) evalMechanism(ctx context.Context, ip netip.Addr, m mechanism) (bool, Result, error) {
	switch m.kind {
	case "all":
		return true, m.qual, nil
	case "ip4", "ip6":
		if m.prefix.Contains(ip) {
			return true, m.qual, nil
		}
		return false, None, nil
	case "include":
		res, err := s.evalDomain(ctx, ip, m.domain)
		if err != nil {
			// 子策略的临时错误与永久错误都向上传播。
			return false, None, err
		}
		switch res {
		case Pass:
			// 只有子策略 pass 才算 include 匹配。
			return true, m.qual, nil
		case None:
			return false, None, permErrorf("include target %q has no policy", m.domain)
		default:
			return false, None, nil
		}
	}
	return false, None, permErrorf("unknown mechanism %q", m.kind)
}

type mechanism struct {
	qual   Result
	kind   string
	prefix netip.Prefix // ip4 / ip6
	domain string       // include
}

type policy struct {
	mechanisms  []mechanism
	redirect    string
	hasRedirect bool
}

func parsePolicy(record string) (*policy, error) {
	fields := strings.Fields(record)
	if len(fields) == 0 || fields[0] != "v=spf1" {
		return nil, permErrorf("record %q is not a policy record", record)
	}
	pol := &policy{}
	for _, term := range fields[1:] {
		if target, ok := strings.CutPrefix(term, "redirect="); ok {
			if pol.hasRedirect {
				return nil, permErrorf("duplicate redirect modifier")
			}
			if target == "" {
				return nil, permErrorf("empty redirect target")
			}
			pol.redirect, pol.hasRedirect = target, true
			continue
		}
		if strings.Contains(term, "=") {
			// 未知修饰符按题意忽略。
			continue
		}
		m, err := parseMechanism(term)
		if err != nil {
			return nil, err
		}
		pol.mechanisms = append(pol.mechanisms, m)
	}
	return pol, nil
}

func parseMechanism(term string) (mechanism, error) {
	m := mechanism{qual: Pass}
	if term == "" {
		return m, permErrorf("empty mechanism")
	}
	switch term[0] {
	case '+':
		term = term[1:]
	case '-':
		m.qual = Fail
		term = term[1:]
	case '~':
		m.qual = SoftFail
		term = term[1:]
	case '?':
		m.qual = Neutral
		term = term[1:]
	}
	if term == "all" {
		m.kind = "all"
		return m, nil
	}
	if target, ok := strings.CutPrefix(term, "include:"); ok {
		if target == "" {
			return m, permErrorf("include requires a domain")
		}
		m.kind, m.domain = "include", target
		return m, nil
	}
	for _, kind := range []string{"ip4", "ip6"} {
		spec, ok := strings.CutPrefix(term, kind+":")
		if !ok {
			continue
		}
		prefix, err := parseIPPrefix(spec, kind == "ip4")
		if err != nil {
			return m, err
		}
		m.kind, m.prefix = kind, prefix
		return m, nil
	}
	return m, permErrorf("unsupported mechanism %q", term)
}

func parseIPPrefix(spec string, v4 bool) (netip.Prefix, error) {
	addrPart, bitsPart, hasBits := strings.Cut(spec, "/")
	addr, err := netip.ParseAddr(addrPart)
	if err != nil {
		return netip.Prefix{}, permErrorf("invalid address %q", addrPart)
	}
	if addr.Is4() != v4 {
		return netip.Prefix{}, permErrorf("address family mismatch in %q", spec)
	}
	bits := 128
	if v4 {
		bits = 32
	}
	if hasBits {
		n, err := strconv.Atoi(bitsPart)
		if err != nil {
			return netip.Prefix{}, permErrorf("invalid prefix length %q", bitsPart)
		}
		bits = n
		if bits < 0 || bits > addr.BitLen() {
			return netip.Prefix{}, permErrorf("prefix length %d out of range", bits)
		}
	}
	return netip.PrefixFrom(addr, bits).Masked(), nil
}
