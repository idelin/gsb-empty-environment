package netsec

import (
	"crypto/tls"
	"errors"
)

// ConnectionSCTs 提取 TLS 连接状态中携带的 SCT。
//
// 优先使用握手对端通过 status_request/sct 扩展下发的 SCT
// （ConnectionState.SignedCertificateTimestamps，元素为单份序列化 SCT）；
// 对端未下发时，回退到叶子证书内嵌的 SCT 扩展。
func ConnectionSCTs(state tls.ConnectionState) ([]*SCT, error) {
	if len(state.SignedCertificateTimestamps) > 0 {
		scts := make([]*SCT, 0, len(state.SignedCertificateTimestamps))
		for _, raw := range state.SignedCertificateTimestamps {
			sct, err := ParseSCT(raw)
			if err != nil {
				return nil, err
			}
			scts = append(scts, sct)
		}
		return scts, nil
	}

	if len(state.PeerCertificates) == 0 || state.PeerCertificates[0] == nil {
		return nil, nil
	}
	return EmbeddedSCTs(state.PeerCertificates[0])
}

// VerifyConnection 适配 tls.Config.VerifyConnection 回调。
//
// 回调在标准证书链校验完成后运行，本方法只负责 SCT 策略：
// 提取连接中的 SCT，对叶子证书执行 Evaluate。策略不满足时返回
// *PolicyError（可通过 errors.Is 匹配 ErrTooFewSCTs / ErrTooFewOperators），
// 从而让握手失败。
func (p *SCTPolicy) VerifyConnection(cs tls.ConnectionState) error {
	if len(cs.PeerCertificates) == 0 || cs.PeerCertificates[0] == nil {
		return errors.New("sct policy: no peer certificate")
	}
	scts, err := ConnectionSCTs(cs)
	if err != nil {
		return err
	}
	_, err = p.Evaluate(cs.PeerCertificates[0], scts)
	return err
}

// VerifyConnectionReport 与 VerifyConnection 相同，但额外返回评估报告，
// 供调用方在握手后记录或上报每一份 SCT 的判定明细。
func (p *SCTPolicy) VerifyConnectionReport(cs tls.ConnectionState) (*SCTReport, error) {
	if len(cs.PeerCertificates) == 0 || cs.PeerCertificates[0] == nil {
		return nil, errors.New("sct policy: no peer certificate")
	}
	scts, err := ConnectionSCTs(cs)
	if err != nil {
		return nil, err
	}
	return p.Evaluate(cs.PeerCertificates[0], scts)
}
