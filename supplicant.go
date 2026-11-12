package netsec

import "errors"

// ErrUnexpectedPhase 表示接入端收到了不符合当前阶段的 EAP 消息。
var ErrUnexpectedPhase = errors.New("netsec: unexpected EAP phase")

// Supplicant 是接入端（客户端）模拟实现，供宿主集成测试与联调使用。
// 生产部署中接入端在被保护设备上运行，不属于本包的宿主侧。
type Supplicant struct {
	OuterID string // 匿名外层身份，仅用于路由
	InnerID string // 受保护内层身份
	CredKey []byte // 内层凭据密钥

	cNonce []byte
	sNonce []byte
}

func NewSupplicant(outerID, innerID string, credKey []byte) *Supplicant {
	return &Supplicant{OuterID: outerID, InnerID: innerID, CredKey: credKey}
}

// Identity 发起交换：携带外层身份与本端隧道随机数。
func (s *Supplicant) Identity() EAP {
	s.cNonce = randBytes(16)
	return EAP{Phase: PhaseIdentity, OuterID: s.OuterID, Nonce: s.cNonce}
}

// Handle 处理服务器/宿主下发的 EAP 消息并产生应答。
func (s *Supplicant) Handle(e EAP) (EAP, error) {
	switch e.Phase {
	case PhaseTunnel:
		s.sNonce = append([]byte(nil), e.Nonce...)
		tk := tunnelKey(s.cNonce, s.sNonce)
		proof := mac(s.CredKey, labelInner, frame(e.Body, []byte(s.InnerID), tk))
		body := putString(nil, s.InnerID)
		body = putBytes(body, proof)
		return EAP{Phase: PhaseInner, Body: body}, nil
	case PhaseConfirm:
		exporter := exporterKey(tunnelKey(s.cNonce, s.sNonce))
		mic := mac(confirmKey(exporter), labelConfirm, frame(e.Nonce, exporter))
		return EAP{Phase: PhaseConfirm, Body: mic}, nil
	default:
		return EAP{}, ErrUnexpectedPhase
	}
}
