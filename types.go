package netsec

import "time"

// 常用资源记录类型。数值与 DNS 协议一致。
const (
	TypeA     uint16 = 1
	TypeNS    uint16 = 2
	TypeCNAME uint16 = 5
	TypeSOA   uint16 = 6
	TypePTR   uint16 = 12
	TypeTXT   uint16 = 16
	TypeAAAA  uint16 = 28

	// TypeAny 仅用于表达 NXDOMAIN 这类“覆盖整个名字、不限类型”的否定结论。
	TypeAny uint16 = 255
)

// DNS 应答码（RCODE），只列出组合器需要区分的取值。
const (
	RcodeNoError  uint16 = 0
	RcodeFormErr  uint16 = 1
	RcodeServFail uint16 = 2
	RcodeNXDomain uint16 = 3
	RcodeNotImp   uint16 = 4
	RcodeRefused  uint16 = 5
)

// RR 是与线路格式无关的资源记录视图。
// Target 仅在 CNAME/NS/PTR 等指向另一个名字的类型下使用。
type RR struct {
	Name   string
	Type   uint16
	TTL    time.Duration
	Target string
	Data   string
}

// Message 是一次 DNS 查询得到的应答。
// Zone 给出产生该应答的权威区（SOA 起点），组合器原样记入每一跳。
type Message struct {
	QueryName string
	QueryType uint16
	Rcode     uint16
	Zone      string

	Answer    []RR
	Authority []RR
	// NegativeTTL 为否定应答的有效期；为 0 时从 Authority 中的 SOA MINIMUM 推断。
	NegativeTTL time.Duration
}

// Resolver 是 Go 解析宿主需要实现的查询接口。
// 超时、网络中断应返回错误，由组合器保留已取得的部分信任路径。
type Resolver interface {
	Query(name string, qtype uint16) (*Message, error)
}

// Hop 记录 CNAME 追踪链路上的一次查询（一跳）。
type Hop struct {
	// 本跳实际发出的查询。
	QueryName string
	QueryType uint16
	// 产生该应答的权威区。
	Zone string
	// 本跳得到的别名；非别名为空。
	AliasTarget string
	// 本跳应答的有效期（肯定应答取记录 TTL，否定应答取否定 TTL）。
	TTL time.Duration
	// 应答失效的绝对时刻，基于组合器注入的时钟计算。
	ExpiresAt time.Time
	// NXDOMAIN/NODATA 时对末端名字成立的否定结论；肯定应答为 nil。
	Negative *NegativeProof
}

// NegativeProof 是一跳否定应答给出的、作用域被严格限定的结论。
type NegativeProof struct {
	Name     string
	Type     uint16 // NODATA 为具体查询类型；NXDOMAIN 为 TypeAny
	Zone     string
	TTL      time.Duration
	NXDOMAIN bool
}

// Covers 判断该否定结论能否覆盖给定的 (name, qtype) 查询。
//
//   - NXDOMAIN 只覆盖被证明名字本身或其下属名字（依据该权威区的否定缓存语义），
//     绝不能向上覆盖到把它引到这里的起始名字。
//   - NODATA 只覆盖被证明名字的同一类型，连该名字的其它类型都不覆盖。
func (p *NegativeProof) Covers(name string, qtype uint16) bool {
	if p == nil {
		return false
	}
	target := canonicalName(name)
	if p.NXDOMAIN {
		return target == p.Name || isSubDomain(target, p.Name)
	}
	return target == p.Name && p.Type == qtype
}

// Alias 是信任路径上的一条 CNAME 边。
type Alias struct {
	From string
	To   string
	Zone string
	TTL  time.Duration
}

// Result 是一次完整解析的组合结果。
type Result struct {
	// 原始查询的名字与类型。
	QueryName string
	QueryType uint16
	// 别名链按追踪顺序排列，QueryName -> ... -> TerminalName。
	Path []Alias
	// 末端名字上与查询类型一致的肯定记录；否定结论时为 nil。
	Records []RR
	// 每一跳的完整记录，含查询类型、权威区、有效期与否定结论。
	Hops []Hop
	// 末端为否定应答时的受限结论；肯定解析为 nil。
	Negative *NegativeProof
	// 整条路径上最短的有效期。
	MinTTL time.Duration
}

// TerminalName 返回别名链末端（无别名时即起始名字）。
func (r *Result) TerminalName() string {
	if len(r.Path) == 0 {
		return r.QueryName
	}
	return r.Path[len(r.Path)-1].To
}

// TrustPath 返回从起始名字到末端的完整名字序列。
func (r *Result) TrustPath() []string {
	out := make([]string, 0, len(r.Path)+1)
	out = append(out, r.QueryName)
	for _, edge := range r.Path {
		out = append(out, edge.To)
	}
	return out
}

// PartialPath 是解析中途失败（如超时）时仍可交付的不完整信任路径。
type PartialPath struct {
	QueryName string
	Hops      []Hop
	Path      []Alias
}

// Names 返回已确认的名字序列（比完整路径少一个尚未解析的末端）。
func (p *PartialPath) Names() []string {
	out := make([]string, 0, len(p.Path)+1)
	out = append(out, p.QueryName)
	for _, edge := range p.Path {
		out = append(out, edge.To)
	}
	return out
}
