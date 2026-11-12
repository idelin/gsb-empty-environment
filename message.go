package netsec

// Phase 标识 EAP 式交换的阶段。
type Phase uint8

const (
	// PhaseIdentity 携带外层身份（仅用于路由）与接入端隧道随机数。
	PhaseIdentity Phase = iota + 1
	// PhaseTunnel 由服务器下发隧道随机数与内层挑战。
	PhaseTunnel
	// PhaseInner 携带受保护的内层身份与内层证明。
	PhaseInner
	// PhaseConfirm 宿主与接入端之间的密钥确认。
	PhaseConfirm
)

// EAP 是接入端与服务器之间、由宿主透明中继的消息。
type EAP struct {
	Phase   Phase
	OuterID string // 仅 PhaseIdentity 使用；只用于路由
	Nonce   []byte
	Body    []byte
}

func (e EAP) encode() []byte {
	var b []byte
	b = putBytes(b, []byte{byte(e.Phase)})
	b = putString(b, e.OuterID)
	b = putBytes(b, e.Nonce)
	b = putBytes(b, e.Body)
	return b
}

// Result 是服务器对一次请求的处理结论。
type Result uint8

const (
	ResultContinue Result = iota
	ResultSuccess
	ResultFailure
)

// Request 是宿主发往服务器（可经代理）的接入请求。
type Request struct {
	OuterID  string // 仅首个请求携带，仅用于路由
	State    []byte // 服务器会话 Cookie，逐跳回显
	PortID   string // 物理端口标识
	PortInst []byte // 物理端口实例，重接即变
	PortMAC  []byte // 宿主对 PortID||PortInst 的签名
	EAP      EAP
}

// Response 是服务器返回给宿主的响应。除 AuthMAC 外的全部字段
// 都被 AuthMAC 覆盖，宿主验签通过后才可使用任何字段。
type Response struct {
	State    []byte
	Result   Result
	EAP      *EAP
	InnerID  string // 授权主体：经验证的内层身份
	Exporter []byte // 隧道导出值
	Digest   []byte // 交换摘要
	PortID   string // 结果绑定的物理端口
	PortInst []byte // 结果绑定的端口实例
	AuthMAC  []byte
}

// authBlob 是 AuthMAC 覆盖内容的规范化编码。
func (r *Response) authBlob() []byte {
	var b []byte
	b = putBytes(b, r.State)
	b = putBytes(b, []byte{byte(r.Result)})
	if r.EAP != nil {
		b = putBytes(b, r.EAP.encode())
	} else {
		b = putBytes(b, nil)
	}
	b = putString(b, r.InnerID)
	b = putBytes(b, r.Exporter)
	b = putBytes(b, r.Digest)
	b = putString(b, r.PortID)
	b = putBytes(b, r.PortInst)
	return b
}
