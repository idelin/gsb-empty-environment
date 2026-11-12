package netsec

import (
	"crypto/hmac"
	"errors"
	"sync"
)

var (
	// ErrBadState 表示消息与当前端口状态机阶段不符（包括跨会话注入）。
	ErrBadState = errors.New("netsec: unexpected message for current port state")
	// ErrBadMAC 表示服务器响应验签失败。
	ErrBadMAC = errors.New("netsec: response authentication failed")
	// ErrPortMismatch 表示结果绑定的端口实例与当前端口实例不一致。
	ErrPortMismatch = errors.New("netsec: result bound to a different port instance")
	// ErrDigestMismatch 表示服务器交换摘要与宿主中继摘要不一致。
	ErrDigestMismatch = errors.New("netsec: exchange digest mismatch")
	// ErrConfirmFailed 表示接入端无法证明持有本隧道的导出值。
	ErrConfirmFailed = errors.New("netsec: supplicant key confirmation failed")
	// ErrRejected 表示服务器拒绝了本次认证。
	ErrRejected = errors.New("netsec: authentication rejected by server")
)

// PortState 是端口状态机的状态。
type PortState int

const (
	// StateAuthenticating 认证进行中。
	StateAuthenticating PortState = iota + 1
	// StateAuthorized 已授权，授权主体为内层身份。
	StateAuthorized
	// StateHeld 认证失败，端口保持关闭。
	StateHeld
)

// Host 是接入宿主（authenticator）：持有物理端口，驱动 EAP 式状态机，
// 在接入端与服务器之间中继消息，并强制执行结果绑定校验。
type Host struct {
	server  *Server
	hostKey []byte

	mu    sync.Mutex
	ports map[string]*Port
}

// NewServerSide 见 server.go。hostKey 是宿主与服务器之间的共享密钥，
// 类似 RADIUS shared secret，用于端口绑定签名与响应验签。
func NewHost(server *Server, hostKey []byte) *Host {
	return &Host{server: server, hostKey: hostKey, ports: make(map[string]*Port)}
}

// Attach 表示物理端口接入或重接：每次调用都生成全新的端口实例，
// 该端口上此前的一切进行中状态与授权立即作废。
func (h *Host) Attach(portID string) *Port {
	p := &Port{
		host:   h,
		portID: portID,
		inst:   randBytes(16),
		state:  StateAuthenticating,
	}
	h.mu.Lock()
	h.ports[portID] = p
	h.mu.Unlock()
	return p
}

// Port 返回端口当前实例；重接后旧实例句柄不再代表该端口。
func (h *Host) Port(portID string) *Port {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ports[portID]
}

// Port 是某物理端口的一次实例化会话。
type Port struct {
	host   *Host
	portID string
	inst   []byte

	mu       sync.Mutex
	state    PortState
	pending  []byte // 服务器会话 Cookie
	msgs     []EAP  // 已中继消息，用于交换摘要
	innerID  string // 授权主体：仅来自验签后的服务器响应
	exporter []byte
	aNonce   []byte
}

// State 返回端口当前状态。
func (p *Port) State() PortState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

// Authorized 在端口已授权时返回授权主体（内层身份）。
// 外层身份从不作为返回值，也不参与授权决策。
func (p *Port) Authorized() (innerID string, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state != StateAuthorized {
		return "", false
	}
	return p.innerID, true
}

// ReceiveFromSupplicant 处理来自接入端的 EAP 消息。
// 返回非 nil Request 时，宿主应将其发往服务器（可经代理）；
// 返回 nil 表示消息已在本地处理（密钥确认完成，端口已授权）。
func (p *Port) ReceiveFromSupplicant(e EAP) (*Request, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state != StateAuthenticating {
		return nil, ErrBadState
	}
	switch e.Phase {
	case PhaseIdentity:
		if len(p.msgs) != 0 || e.OuterID == "" {
			return nil, ErrBadState
		}
	case PhaseConfirm:
		if p.aNonce == nil {
			return nil, ErrBadState
		}
		want := mac(confirmKey(p.exporter), labelConfirm, frame(p.aNonce, p.exporter))
		if !hmac.Equal(e.Body, want) {
			p.state = StateHeld
			return nil, ErrConfirmFailed
		}
		p.state = StateAuthorized
		return nil, nil
	default:
		if len(p.msgs) == 0 {
			return nil, ErrBadState
		}
	}
	p.msgs = append(p.msgs, e)
	return &Request{
		OuterID:  e.OuterID,
		State:    p.pending,
		PortID:   p.portID,
		PortInst: p.inst,
		PortMAC:  portMAC(p.host.hostKey, p.portID, p.inst),
		EAP:      e,
	}, nil
}

// ReceiveFromServer 处理服务器（或代理转来）的响应。
// 返回非 nil EAP 时，宿主应将其转发给接入端。
//
// 成功响应只有在三重绑定全部校验通过后才被接受：
//  1. AuthMAC 验签（覆盖 InnerID、Exporter、Digest、PortID、PortInst）；
//  2. 响应绑定的端口实例等于当前端口实例；
//  3. 响应携带的交换摘要等于宿主本地中继摘要。
func (p *Port) ReceiveFromServer(resp Response) (*EAP, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state != StateAuthenticating {
		return nil, ErrBadState
	}
	if !hmac.Equal(resp.AuthMAC, mac(p.host.hostKey, labelRespMAC, resp.authBlob())) {
		return nil, ErrBadMAC
	}
	if p.pending == nil {
		if len(resp.State) == 0 {
			if resp.Result == ResultFailure {
				p.state = StateHeld
				return nil, ErrRejected
			}
			return nil, ErrBadState
		}
		p.pending = append([]byte(nil), resp.State...)
	} else if !hmac.Equal(resp.State, p.pending) {
		// 其他会话的响应不能套用到本端口会话。
		return nil, ErrBadState
	}
	switch resp.Result {
	case ResultFailure:
		p.state = StateHeld
		return nil, ErrRejected
	case ResultContinue:
		if resp.EAP == nil {
			return nil, ErrBadState
		}
		if resp.PortID != p.portID || !hmac.Equal(resp.PortInst, p.inst) {
			return nil, ErrPortMismatch
		}
		p.msgs = append(p.msgs, *resp.EAP)
		out := *resp.EAP
		return &out, nil
	case ResultSuccess:
		if resp.PortID != p.portID || !hmac.Equal(resp.PortInst, p.inst) {
			return nil, ErrPortMismatch
		}
		if !hmac.Equal(resp.Digest, exchangeDigest(p.portID, p.inst, p.msgs)) {
			return nil, ErrDigestMismatch
		}
		if resp.InnerID == "" || len(resp.Exporter) == 0 {
			return nil, ErrBadState
		}
		p.innerID = resp.InnerID
		p.exporter = append([]byte(nil), resp.Exporter...)
		p.aNonce = randBytes(16)
		return &EAP{Phase: PhaseConfirm, Nonce: p.aNonce}, nil
	}
	return nil, ErrBadState
}
