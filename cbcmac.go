// Package netsec 实现 AES-CBC-MAC 及其可变长度拼接伪造演示与加固格式。
//
// 安全说明：CBC-MAC 仅在消息长度固定（或长度被认证）时才是安全的 MAC；
// 直接用于可变长度消息会遭受拼接伪造。本包同时提供加固格式（域标签 +
// 长度前缀）以修复该问题。CBC-MAC 是消息认证码，绝不能当作加密方案使用。
package netsec

import (
	"crypto/cipher"
	"crypto/subtle"
	"errors"
	"fmt"
)

// BlockSize 是 AES 分组长度（字节）。
const BlockSize = 16

// MaxBlocks 是本题消息允许的最大块数。
const MaxBlocks = 8

// ErrEmptyMessage 表示消息为空。
var ErrEmptyMessage = errors.New("netsec: message must be non-empty")

// ErrUnaligned 表示消息长度不是分组的整数倍。
var ErrUnaligned = errors.New("netsec: message length must be a multiple of the block size")

// ErrTooLong 表示消息块数超过 MaxBlocks。
var ErrTooLong = errors.New("netsec: message exceeds maximum block count")

// KeyHandle 是宿主回调：每次调用返回一个已装载密钥的 cipher.Block。
// 密钥本身始终由宿主持有，调用方无法读取。
type KeyHandle func() (cipher.Block, error)

// validate 检查消息非空、整块且不超过最大块数。
func validate(message []byte) error {
	if len(message) == 0 {
		return ErrEmptyMessage
	}
	if len(message)%BlockSize != 0 {
		return fmt.Errorf("%w: got %d bytes", ErrUnaligned, len(message))
	}
	if len(message)/BlockSize > MaxBlocks {
		return fmt.Errorf("%w: got %d blocks", ErrTooLong, len(message)/BlockSize)
	}
	return nil
}

// chain 对整块消息执行 CBC-MAC 链式计算，返回每一步的链值
// （states[i] 为处理第 i 块后的状态，states[len-1] 即 tag）。
// 初始链值全零，每块计算 state = AES_K(block XOR state)。
func chain(block cipher.Block, message []byte) [][]byte {
	states := make([][]byte, 0, len(message)/BlockSize)
	state := make([]byte, BlockSize) // 初始链值全零
	buf := make([]byte, BlockSize)
	for off := 0; off < len(message); off += BlockSize {
		subtle.XORBytes(buf, message[off:off+BlockSize], state)
		block.Encrypt(state, buf)
		s := make([]byte, BlockSize)
		copy(s, state)
		states = append(states, s)
	}
	return states
}

// Oracle 是生产接口：只暴露 Tag 与 Verify，不暴露密钥与中间链值。
type Oracle struct {
	key KeyHandle
}

// NewOracle 以宿主密钥回调创建生产 oracle。
func NewOracle(k KeyHandle) *Oracle {
	return &Oracle{key: k}
}

// tag 计算原始 CBC-MAC，并可选返回逐块链值。
func (o *Oracle) tag(message []byte) (tag []byte, states [][]byte, err error) {
	if err := validate(message); err != nil {
		return nil, nil, err
	}
	return o.tagRaw(message)
}

// tagRaw 不做消息校验，供内部已自行校验的调用方（如加固格式）使用。
func (o *Oracle) tagRaw(message []byte) (tag []byte, states [][]byte, err error) {
	block, err := o.key()
	if err != nil {
		return nil, nil, fmt.Errorf("netsec: key handle: %w", err)
	}
	states = chain(block, message)
	tag = make([]byte, BlockSize)
	copy(tag, states[len(states)-1])
	return tag, states, nil
}

// Tag 计算消息的 CBC-MAC 标签。
func (o *Oracle) Tag(message []byte) ([]byte, error) {
	tag, _, err := o.tag(message)
	return tag, err
}

// Verify 以常量时间比较校验消息标签。
func (o *Oracle) Verify(message, tag []byte) bool {
	want, err := o.Tag(message)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(want, tag) == 1
}

// DebugOracle 是仅供测试使用的可观察调试模式：除 Tag/Verify 外，
// 还暴露逐块链值。生产环境不得使用。
type DebugOracle struct {
	*Oracle
}

// NewDebugOracle 创建调试 oracle（仅测试）。
func NewDebugOracle(k KeyHandle) *DebugOracle {
	return &DebugOracle{Oracle: NewOracle(k)}
}

// ChainValues 返回消息逐块链值（含最终 tag），仅调试模式可用。
func (d *DebugOracle) ChainValues(message []byte) ([][]byte, error) {
	_, states, err := d.tag(message)
	return states, err
}
