// Package netsec 提供设备认证消息的双格式分派。
//
// 安全不变量：
//  1. 首部按固定长度、固定偏移解析，版本字节唯一决定解码器；
//     解码失败（格式或身份）绝不尝试另一格式的解释。
//  2. 已登记升级的设备永久拒绝旧版消息（降级拒绝）。
//  3. 升级记录先可靠保存到 Store，保存成功后才在内存中确认；
//     保存失败则升级不生效。重启后由 Store 恢复升级状态，
//     无法回退到旧版许可。
package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
)

// 首部布局（定长 8 字节）：
//
//	[0:4]  魔数 "NSA1"
//	[4]    版本（VersionLegacy / VersionCurrent）
//	[5]    保留，必须为 0
//	[6:8]  正文长度（大端，不含首部）
const HeaderLen = 8

const (
	VersionLegacy  byte = 1
	VersionCurrent byte = 2
)

var headerMagic = [4]byte{'N', 'S', 'A', '1'}

var (
	ErrTooShort         = errors.New("netsec: message shorter than fixed header")
	ErrBadMagic         = errors.New("netsec: bad header magic")
	ErrBadReserved      = errors.New("netsec: reserved header byte must be zero")
	ErrTruncatedBody    = errors.New("netsec: body shorter than header declares")
	ErrUnknownVersion   = errors.New("netsec: no decoder registered for version")
	ErrDowngrade        = errors.New("netsec: upgraded device rejected legacy version")
	ErrIdentityMismatch = errors.New("netsec: message identity does not match device")
)

// Header 是定长解析出的消息首部。
type Header struct {
	Version byte
	BodyLen int
}

// ParseHeader 按固定长度与固定偏移解析首部。
// 版本字节是唯一影响后续解码器选择的字段。
func ParseHeader(raw []byte) (Header, error) {
	if len(raw) < HeaderLen {
		return Header{}, ErrTooShort
	}
	if string(raw[0:4]) != string(headerMagic[:]) {
		return Header{}, ErrBadMagic
	}
	if raw[5] != 0 {
		return Header{}, ErrBadReserved
	}
	return Header{
		Version: raw[4],
		BodyLen: int(binary.BigEndian.Uint16(raw[6:8])),
	}, nil
}

// Message 是解码后的认证消息。
type Message struct {
	Version  byte
	DeviceID string
	Payload  []byte
}

// Decoder 把某一版本的正文解码为 Message。
// 返回错误即终止分派，调用方不得尝试其他解码器。
type Decoder func(body []byte) (Message, error)

// Store 持久化设备升级记录，是升级状态的唯一权威来源。
// SaveUpgraded 必须在返回 nil 前保证记录已可靠保存（落盘）。
type Store interface {
	IsUpgraded(deviceID string) (bool, error)
	SaveUpgraded(deviceID string) error
}

// Dispatcher 供宿主调用：按首部版本分派到唯一解码器，
// 并对已升级设备执行降级拒绝。
type Dispatcher struct {
	store    Store
	decoders map[byte]Decoder

	mu       sync.RWMutex
	upgraded map[string]bool // 内存缓存；权威状态在 Store
}

func NewDispatcher(store Store) *Dispatcher {
	return &Dispatcher{
		store:    store,
		decoders: make(map[byte]Decoder),
		upgraded: make(map[string]bool),
	}
}

// RegisterDecoder 登记某版本对应的唯一解码器。
func (d *Dispatcher) RegisterDecoder(version byte, dec Decoder) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.decoders[version] = dec
}

// Upgrade 登记设备升级：先可靠保存，保存成功后才确认。
// 保存失败时升级不生效，设备仍按未升级处理。
func (d *Dispatcher) Upgrade(deviceID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.upgraded[deviceID] {
		return nil
	}
	if err := d.store.SaveUpgraded(deviceID); err != nil {
		return fmt.Errorf("netsec: upgrade not confirmed: %w", err)
	}
	d.upgraded[deviceID] = true
	return nil
}

func (d *Dispatcher) isUpgraded(deviceID string) (bool, error) {
	d.mu.RLock()
	if d.upgraded[deviceID] {
		d.mu.RUnlock()
		return true, nil
	}
	d.mu.RUnlock()
	// 缓存未命中时询问权威存储；重启后由此恢复升级状态。
	up, err := d.store.IsUpgraded(deviceID)
	if err != nil {
		return false, err
	}
	if up {
		d.mu.Lock()
		d.upgraded[deviceID] = true
		d.mu.Unlock()
	}
	return up, nil
}

// Dispatch 解析首部，选择唯一解码器并校验消息身份。
// 已升级设备收到旧版消息直接拒绝；格式失败与身份失败
// 均返回错误，绝不尝试另一格式的解释。
func (d *Dispatcher) Dispatch(deviceID string, raw []byte) (Message, error) {
	h, err := ParseHeader(raw)
	if err != nil {
		return Message{}, err
	}
	if h.Version == VersionLegacy {
		up, err := d.isUpgraded(deviceID)
		if err != nil {
			return Message{}, fmt.Errorf("netsec: upgrade state unknown: %w", err)
		}
		if up {
			return Message{}, ErrDowngrade
		}
	}
	d.mu.RLock()
	dec, ok := d.decoders[h.Version]
	d.mu.RUnlock()
	if !ok {
		return Message{}, fmt.Errorf("%w: %d", ErrUnknownVersion, h.Version)
	}
	if len(raw)-HeaderLen < h.BodyLen {
		return Message{}, ErrTruncatedBody
	}
	msg, err := dec(raw[HeaderLen : HeaderLen+h.BodyLen])
	if err != nil {
		return Message{}, err
	}
	if msg.DeviceID != deviceID {
		return Message{}, ErrIdentityMismatch
	}
	msg.Version = h.Version
	return msg, nil
}
