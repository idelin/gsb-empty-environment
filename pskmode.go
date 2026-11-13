package netsec

import "errors"

// PSKKeyExchangeMode 对应 RFC 8446 的 PskKeyExchangeMode。
type PSKKeyExchangeMode uint8

const (
	// PSKKe 为 psk_ke(0)：纯 PSK，不要求密钥共享。
	PSKKe PSKKeyExchangeMode = 0
	// PSKDHEKe 为 psk_dhe_ke(1)：PSK 加 (EC)DHE，必须有匹配的密钥共享。
	PSKDHEKe PSKKeyExchangeMode = 1
)

func (m PSKKeyExchangeMode) String() string {
	switch m {
	case PSKKe:
		return "psk_ke"
	case PSKDHEKe:
		return "psk_dhe_ke"
	default:
		return "unknown"
	}
}

// KeyShare 是客户端在 key_share 扩展中提供的一个密钥共享条目。
type KeyShare struct {
	Group       uint16
	KeyExchange []byte
}

var (
	// ErrMissingKeyShare 表示选定了 psk_dhe_ke 但缺少与选定组匹配的密钥共享。
	// 此时握手必须失败，绝不能降级为 psk_ke。
	ErrMissingKeyShare = errors.New("netsec: psk_dhe_ke selected but no key_share for the selected group")
	// ErrModeNotOffered 表示选定的模式不在客户端提供的 psk_key_exchange_modes 中。
	ErrModeNotOffered = errors.New("netsec: selected PSK key exchange mode was not offered by the client")
)

// offered 报告客户端是否提供了指定模式。
func offered(modes []PSKKeyExchangeMode, mode PSKKeyExchangeMode) bool {
	for _, m := range modes {
		if m == mode {
			return true
		}
	}
	return false
}

// hasShareForGroup 报告客户端密钥共享中是否存在与选定组匹配的条目。
func hasShareForGroup(shares []KeyShare, group uint16) bool {
	for _, s := range shares {
		if s.Group == group && len(s.KeyExchange) > 0 {
			return true
		}
	}
	return false
}

// ValidatePSKSelection 校验服务端选定的 PSK 密钥交换模式与客户端
// ClientHello 的一致性，供 TLS 栈在处理 pre_shared_key 时调用。
//
// 规则：
//   - 选定的模式必须出现在客户端的 psk_key_exchange_modes 中；
//   - 选定 psk_dhe_ke 时，客户端必须提供与选定组匹配的 key_share，
//     缺失则返回 ErrMissingKeyShare，握手失败，不允许降级为 psk_ke；
//   - 选定 psk_ke 且客户端也提供了 psk_ke 时，可以没有密钥共享。
//
// selectedGroup 为服务端选定的命名组；仅 psk_dhe_ke 时有意义。
func ValidatePSKSelection(mode PSKKeyExchangeMode, selectedGroup uint16, clientModes []PSKKeyExchangeMode, clientShares []KeyShare) error {
	if !offered(clientModes, mode) {
		return ErrModeNotOffered
	}
	if mode == PSKDHEKe && !hasShareForGroup(clientShares, selectedGroup) {
		return ErrMissingKeyShare
	}
	return nil
}

// SelectPSKMode 按服务端偏好从客户端提供的模式中选定模式。
//
// 仅当客户端提供了 psk_dhe_ke 且存在与选定组匹配的 key_share 时才返回
// PSKDHEKe；否则若客户端提供了 psk_ke 则返回 PSKKe。两者都不满足时返回
// 错误。该函数只依据客户端实际提供的内容做选择，不会把已选定的
// psk_dhe_ke 静默降级。
func SelectPSKMode(selectedGroup uint16, clientModes []PSKKeyExchangeMode, clientShares []KeyShare) (PSKKeyExchangeMode, error) {
	if offered(clientModes, PSKDHEKe) && hasShareForGroup(clientShares, selectedGroup) {
		return PSKDHEKe, nil
	}
	if offered(clientModes, PSKKe) {
		return PSKKe, nil
	}
	return 0, ErrModeNotOffered
}
