package netsec

import (
	"errors"
	"net/netip"
	"sync"
	"time"
)

// DefaultLeaseTime 是未配置时的默认租约时长。
const DefaultLeaseTime = 24 * time.Hour

var errNoIAPD = errors.New("netsec: message carries no IA_PD option")

// Delegator 管理 IA_PD 委派绑定与对应的委派路由。
// 绑定键为客户端 DUID 加 IAID。
type Delegator struct {
	mu       sync.Mutex
	pool     *PrefixPool
	lease    time.Duration
	bindings map[BindingKey]*Binding
	occupied map[netip.Prefix]BindingKey

	// Routes 是委派路由表；路由只在回复被 Commit 确认后安装。
	Routes *RouteTable
	// LinkNets 非空时，最内层中继的 link-address 必须落在其中之一。
	LinkNets []netip.Prefix
	// Now 可注入测试时钟，默认为 time.Now。
	Now func() time.Time
}

func NewDelegator(pool *PrefixPool, lease time.Duration) *Delegator {
	if lease <= 0 {
		lease = DefaultLeaseTime
	}
	return &Delegator{
		pool:     pool,
		lease:    lease,
		bindings: make(map[BindingKey]*Binding),
		occupied: make(map[netip.Prefix]BindingKey),
		Routes:   NewRouteTable(),
		Now:      time.Now,
	}
}

// Reply 是待发送的回复。路由变更在 Commit 确认回复已发出后才生效。
type Reply struct {
	Results []IAResult

	d         *Delegator
	actions   []routeAction
	committed bool
}

type routeAction struct {
	install bool
	route   Route
}

// Commit 确认回复已发出，应用挂起的路由安装与撤销。幂等。
func (r *Reply) Commit() {
	r.d.mu.Lock()
	defer r.d.mu.Unlock()
	if r.committed {
		return
	}
	r.committed = true
	for _, a := range r.actions {
		if a.install {
			r.d.Routes.Install(a.route)
		} else {
			r.d.Routes.Withdraw(a.route.Prefix)
		}
	}
}

// HandleMessage 处理一条解包后的 DHCPv6 消息并生成回复。
// 中继链不合法时返回错误，调用方应丢弃报文。
func (d *Delegator) HandleMessage(msg *Message) (*Reply, error) {
	if len(msg.IAPDs) == 0 {
		return nil, errNoIAPD
	}
	nextHop := msg.SourceAddr
	if msg.Relay != nil {
		if err := ValidateRelayChain(msg.Relay, d.LinkNets); err != nil {
			return nil, err
		}
		// 委派路由的下一跳是最内层中继看到的客户端地址。
		nextHop = msg.Relay[len(msg.Relay)-1].PeerAddress
	}
	if !nextHop.IsValid() || nextHop.IsUnspecified() || nextHop.IsMulticast() {
		return nil, errors.New("netsec: invalid client next-hop address")
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	rep := &Reply{d: d}
	for _, ia := range msg.IAPDs {
		key := BindingKey{DUID: string(msg.ClientDUID), IAID: ia.IAID}
		var res IAResult
		switch msg.Type {
		case MsgSolicit, MsgRequest:
			res = d.handleRequest(key, ia, nextHop, rep)
		case MsgRenew, MsgRebind:
			res = d.handleRenew(key, ia, nextHop, rep)
		case MsgRelease:
			res = d.handleRelease(key, ia, rep)
		default:
			return nil, errors.New("netsec: unsupported message type")
		}
		rep.Results = append(rep.Results, res)
	}
	return rep, nil
}

// bindingLocked 返回未过期的现存绑定，调用方须持锁。
func (d *Delegator) bindingLocked(key BindingKey) *Binding {
	b, ok := d.bindings[key]
	if !ok {
		return nil
	}
	if !d.Now().Before(b.ExpiresAt) {
		return nil
	}
	return b
}

func (d *Delegator) handleRequest(key BindingKey, ia IAPDRequest, nextHop netip.Addr, rep *Reply) IAResult {
	now := d.Now()
	if b := d.bindingLocked(key); b != nil {
		b.ExpiresAt = now.Add(d.lease)
		b.NextHop = nextHop
		rep.actions = append(rep.actions, routeAction{install: true, route: Route{Prefix: b.Prefix, NextHop: nextHop}})
		return IAResult{IAID: ia.IAID, Status: StatusSuccess, Prefix: b.Prefix, ValidUntil: b.ExpiresAt}
	}

	var prefix netip.Prefix
	// 前缀提示只在未被他人占用且位于地址池内时采纳。
	if ia.HasPrefix && d.pool.Contains(ia.Prefix) {
		if owner, taken := d.occupied[ia.Prefix]; !taken || owner == key {
			prefix = ia.Prefix
		}
	}
	if !prefix.IsValid() {
		p, err := d.pool.allocate(func(c netip.Prefix) bool {
			_, taken := d.occupied[c]
			return taken
		})
		if err != nil {
			return IAResult{IAID: ia.IAID, Status: StatusNoPrefixAvail}
		}
		prefix = p
	}

	b := &Binding{Key: key, Prefix: prefix, NextHop: nextHop, ExpiresAt: now.Add(d.lease)}
	d.bindings[key] = b
	d.occupied[prefix] = key
	rep.actions = append(rep.actions, routeAction{install: true, route: Route{Prefix: prefix, NextHop: nextHop}})
	return IAResult{IAID: ia.IAID, Status: StatusSuccess, Prefix: prefix, ValidUntil: b.ExpiresAt}
}

func (d *Delegator) handleRenew(key BindingKey, ia IAPDRequest, nextHop netip.Addr, rep *Reply) IAResult {
	b := d.bindingLocked(key)
	// Renew/Rebind/Release 中的前缀必须与绑定一致，否则 NoBinding。
	if b == nil || !ia.HasPrefix || ia.Prefix != b.Prefix {
		return IAResult{IAID: ia.IAID, Status: StatusNoBinding}
	}
	b.ExpiresAt = d.Now().Add(d.lease)
	b.NextHop = nextHop
	rep.actions = append(rep.actions, routeAction{install: true, route: Route{Prefix: b.Prefix, NextHop: nextHop}})
	return IAResult{IAID: ia.IAID, Status: StatusSuccess, Prefix: b.Prefix, ValidUntil: b.ExpiresAt}
}

func (d *Delegator) handleRelease(key BindingKey, ia IAPDRequest, rep *Reply) IAResult {
	b := d.bindingLocked(key)
	if b == nil || !ia.HasPrefix || ia.Prefix != b.Prefix {
		return IAResult{IAID: ia.IAID, Status: StatusNoBinding}
	}
	delete(d.bindings, key)
	delete(d.occupied, b.Prefix)
	rep.actions = append(rep.actions, routeAction{install: false, route: Route{Prefix: b.Prefix}})
	return IAResult{IAID: ia.IAID, Status: StatusSuccess, Prefix: b.Prefix}
}

// Lookup 返回键对应的未过期绑定。
func (d *Delegator) Lookup(key BindingKey) (Binding, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	b := d.bindingLocked(key)
	if b == nil {
		return Binding{}, false
	}
	return *b, true
}

// ReapExpired 清除到期绑定并撤销其路由，返回清除数量。
func (d *Delegator) ReapExpired() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.Now()
	n := 0
	for key, b := range d.bindings {
		if now.Before(b.ExpiresAt) {
			continue
		}
		delete(d.bindings, key)
		delete(d.occupied, b.Prefix)
		d.Routes.Withdraw(b.Prefix)
		n++
	}
	return n
}
