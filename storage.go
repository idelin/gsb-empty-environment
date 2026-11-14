package netsec

import (
	"errors"
	"fmt"
	"math/big"
)

// EIP-1967 通过 keccak256 命名空间放置代理核心槽位。这些槽位由哈希派生、
// 远离顺序分配的业务槽位（0、1、2……），业务字段在冲突检测中禁止占用，
// 从而保证管理员与实现地址不会与普通存储变量发生槽位冲突。
var (
	// AdminSlot = keccak256("eip1967.proxy.admin") - 1
	AdminSlot = hashToSlot("eip1967.proxy.admin")
	// ImplementationSlot = keccak256("eip1967.proxy.implementation") - 1
	ImplementationSlot = hashToSlot("eip1967.proxy.implementation")
	// BeaconSlot = keccak256("eip1967.proxy.beacon") - 1
	BeaconSlot = hashToSlot("eip1967.proxy.beacon")
)

// reservedSlots 登记全部 EIP-1967 保留槽位及其用途。
var reservedSlots = map[string]string{
	AdminSlot.String():          "EIP-1967 管理员槽",
	ImplementationSlot.String(): "EIP-1967 实现槽",
	BeaconSlot.String():         "EIP-1967 beacon 槽",
}

func hashToSlot(seed string) *big.Int {
	digest := keccak256([]byte(seed))
	slot := new(big.Int).SetBytes(digest[:])
	return slot.Sub(slot, big.NewInt(1))
}

func reservedSlotByName(slot *big.Int) (string, bool) {
	name, ok := reservedSlots[slot.String()]
	return name, ok
}

// AddressLength 是 EVM 地址字节数。
const AddressLength = 20

// Address 表示 20 字节 EVM 地址，内部以大端字节保存。
type Address [AddressLength]byte

// AddressFromHex 解析 0x 前缀的十六进制地址；缺省前缀也可接受。
func AddressFromHex(value string) (Address, error) {
	var addr Address
	hexPart := value
	if len(hexPart) >= 2 && (hexPart[:2] == "0x" || hexPart[:2] == "0X") {
		hexPart = hexPart[2:]
	}
	if len(hexPart) != AddressLength*2 {
		return addr, fmt.Errorf("netsec: 地址 %q 长度非法，应为 20 字节", value)
	}
	parsed, ok := new(big.Int).SetString(hexPart, 16)
	if !ok {
		return addr, fmt.Errorf("netsec: 地址 %q 不是合法十六进制", value)
	}
	parsed.FillBytes(addr[:])
	return addr, nil
}

// Hex 返回 0x 前缀的小写十六进制地址。
func (a Address) Hex() string {
	return "0x" + bytesToHex(a[:])
}

// ProxyStorage 模拟代理合约的 32 字节字存储，供升级服务在无链环境下演练与核对。
// 真实服务可将同一组方法适配到链上 eth_getStorageAt / eth_setStorageAt 调用。
type ProxyStorage struct {
	words map[string][32]byte
}

// NewProxyStorage 创建空存储。
func NewProxyStorage() *ProxyStorage {
	return &ProxyStorage{words: make(map[string][32]byte)}
}

// GetWord 读取整字，未写入时返回全零。
func (s *ProxyStorage) GetWord(slot *big.Int) [32]byte {
	if value, ok := s.words[slot.String()]; ok {
		return value
	}
	return [32]byte{}
}

// SetWord 写入整字。
func (s *ProxyStorage) SetWord(slot *big.Int, value [32]byte) {
	s.words[slot.String()] = value
}

// GetField 按布局字段读取值，自动应用槽内偏移与字节宽度并做右对齐解码。
func (s *ProxyStorage) GetField(entry Entry) *big.Int {
	word := s.GetWord(entry.Slot)
	start := 32 - int(entry.Offset) - int(entry.Size)
	raw := word[start : start+int(entry.Size)]
	return new(big.Int).SetBytes(raw)
}

// SetField 按布局字段写入值，仅修改该字段占用的字节区间，同槽其他打包字段保持不变。
func (s *ProxyStorage) SetField(entry Entry, value *big.Int) error {
	if value == nil {
		return errors.New("netsec: 写入值不能为 nil")
	}
	if value.Sign() < 0 {
		return fmt.Errorf("netsec: 字段 %s 不支持写入负值", entry.Name)
	}
	if uint(len(value.Bytes())) > entry.Size {
		return fmt.Errorf("netsec: 值超出字段 %s 的 %d 字节宽度", entry.Name, entry.Size)
	}
	word := s.GetWord(entry.Slot)
	start := 32 - int(entry.Offset) - int(entry.Size)
	padded := make([]byte, 32)
	value.FillBytes(padded)
	copy(word[start:start+int(entry.Size)], padded[32-int(entry.Size):])
	s.SetWord(entry.Slot, word)
	return nil
}

// GetAddress 读取一个地址型字段（含 EIP-1967 保留槽）。
func (s *ProxyStorage) GetAddress(slot *big.Int) Address {
	word := s.GetWord(slot)
	var addr Address
	copy(addr[:], word[32-AddressLength:])
	return addr
}

// SetAddress 写入一个地址型字段（含 EIP-1967 保留槽），右对齐编码。
func (s *ProxyStorage) SetAddress(slot *big.Int, addr Address) {
	var word [32]byte
	copy(word[32-AddressLength:], addr[:])
	s.SetWord(slot, word)
}

// MappingSlot 按 Solidity 规则计算 mapping 条目槽：keccak256(pad32(key) . pad32(slot))。
// 键按 32 字节右对齐编码，与 address/uint 等 elementary 键的编码一致。
func MappingSlot(declarationSlot *big.Int, key *big.Int) *big.Int {
	var preimage [64]byte
	key.FillBytes(preimage[:32])
	declarationSlot.FillBytes(preimage[32:])
	digest := keccak256(preimage[:])
	return new(big.Int).SetBytes(digest[:])
}

// Snapshot 返回所有非零字的拷贝，升级前后对比与回读验证可基于快照进行。
func (s *ProxyStorage) Snapshot() map[string][32]byte {
	out := make(map[string][32]byte, len(s.words))
	for slot, word := range s.words {
		out[slot] = word
	}
	return out
}

func bytesToHex(data []byte) string {
	const table = "0123456789abcdef"
	out := make([]byte, len(data)*2)
	for i, b := range data {
		out[i*2] = table[b>>4]
		out[i*2+1] = table[b&0x0f]
	}
	return string(out)
}
