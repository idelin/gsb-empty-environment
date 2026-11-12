// Package netsec 实现接入端口上的 LLDP 邻居表准入控制。
//
// 它面向交换机软件的帧接收线程设计：接收线程在确认帧为 LLDP 帧后，
// 调用 Table.Admit 完成格式校验、每端口容量限制以及 TTL=0 的安全撤销。
// 包内自带互斥锁，多个端口的接收线程可以并发调用。
package netsec

import (
	"bytes"
	"errors"
	"net"
	"sync"
	"time"
)

// etherTypeLLDP 是 LLDP 帧的 EtherType（IEEE 802.1AB）。
const etherTypeLLDP = 0x88CC

// dstMACLLDP 是 LLDP 唯一允许的组播目的地址。
var dstMACLLDP = net.HardwareAddr{0x01, 0x80, 0xC2, 0x00, 0x00, 0x0E}

// 帧解析与准入失败的错误。调用方可通过 errors.Is 判别失败原因。
var (
	// ErrWrongDestination 表示目的 MAC 不是 01:80:C2:00:00:0E。
	ErrWrongDestination = errors.New("lldp: destination MAC must be 01:80:C2:00:00:0E")
	// ErrFrameTooShort 表示帧长度不足以容纳以太网头或必需 TLV。
	ErrFrameTooShort = errors.New("lldp: frame too short")
	// ErrWrongEtherType 表示 EtherType 不是 0x88CC。
	ErrWrongEtherType = errors.New("lldp: EtherType must be 0x88CC")
	// ErrMalformedTLV 表示 TLV 头越界、字段长度非法或结束 TLV 后仍有数据。
	ErrMalformedTLV = errors.New("lldp: malformed TLV stream")
	// ErrOutOfOrderTLV 表示前三个 TLV 没有依次为机箱 ID、端口 ID、TTL。
	ErrOutOfOrderTLV = errors.New("lldp: first three TLVs must be Chassis ID, Port ID, TTL in order")
	// ErrTableFull 表示该端口的邻居条目已达到上限。
	ErrTableFull = errors.New("lldp: neighbor table full for this port")
	// ErrSourceMismatch 表示撤销或刷新条目的源 MAC 与创建该条目的源 MAC 不一致。
	ErrSourceMismatch = errors.New("lldp: source MAC does not own the existing entry")
)

// LLDP TLV 类型（IEEE 802.1AB 8.4）。
const (
	tlvEndOfPDU         = 0
	tlvChassisID        = 1
	tlvPortID           = 2
	tlvTimeToLive       = 3
	tlvOptionalFirst    = 4
	tlvOrganizationally = 127
)

// neighborKey 唯一标识一个端口上的邻居。
// 机箱 ID 与端口 ID 按帧中的原始字节比较，避免不同字符串表示的歧义。
type neighborKey struct {
	chassisID string
	portID    string
}

// PDU 保存一个 LLDP 帧解析后的关键信息。字段中的字节均为帧数据的副本。
type PDU struct {
	SrcMAC    net.HardwareAddr
	ChassisID []byte
	PortID    []byte
	TTL       uint16
}

// Entry 是邻居表中的一个条目。
type Entry struct {
	PortID    int
	SrcMAC    net.HardwareAddr
	ChassisID []byte
	PortTLV   []byte
	TTL       time.Duration
	CreatedAt time.Time
	UpdatedAt time.Time
	ExpiresAt time.Time
}

// Stats 汇总 Table 自创建以来的准入统计，便于洪泛攻击的监测与告警。
type Stats struct {
	Accepted          uint64
	Refreshed         uint64
	Withdrawn         uint64
	AgedOut           uint64
	RejectedMalformed uint64
	RejectedFull      uint64
	RejectedSource    uint64
}

// Table 是按端口隔离的 LLDP 邻居表。零值不可用，必须用 NewTable 创建。
type Table struct {
	maxPerPort int
	now        func() time.Time

	// 外层 map 的键是接入端口编号，内层 map 的键是机箱 ID+端口 ID。
	neighbors map[int]map[neighborKey]*Entry

	stats Stats
	mu    sync.Mutex
}

// NewTable 创建邻居表，maxPerPort 是每个接入端口允许的最大邻居数，必须大于 0。
func NewTable(maxPerPort int) *Table {
	if maxPerPort <= 0 {
		panic("lldp: maxPerPort must be positive")
	}
	return &Table{
		maxPerPort: maxPerPort,
		now:        time.Now,
		neighbors:  make(map[int]map[neighborKey]*Entry),
	}
}

// ParseFrame 校验帧格式并解析出 LLDP PDU。
//
// 校验规则：
//   - 目的 MAC 必须是 01:80:C2:00:00:0E；
//   - EtherType 必须是 0x88CC；
//   - 前三个 TLV 依次为机箱 ID（类型 1）、端口 ID（类型 2）、TTL（类型 3）；
//   - 结束 TLV（类型 0）的长度必须为 0，且其后不得有任何数据。
func ParseFrame(frame []byte) (*PDU, error) {
	// 14 字节以太网头：6 目的 MAC + 6 源 MAC + 2 EtherType。
	if len(frame) < 14 {
		return nil, ErrFrameTooShort
	}
	dst := net.HardwareAddr(frame[0:6])
	if !bytes.Equal(dst, dstMACLLDP) {
		return nil, ErrWrongDestination
	}
	src := cloneMAC(frame[6:12])
	etherType := uint16(frame[12])<<8 | uint16(frame[13])
	if etherType != etherTypeLLDP {
		return nil, ErrWrongEtherType
	}

	payload := frame[14:]
	pdu := &PDU{SrcMAC: src}

	seenChassis, seenPort, seenTTL := false, false, false
	index := 0
	for index < len(payload) {
		if index+2 > len(payload) {
			return nil, ErrMalformedTLV
		}
		tlvType := uint8(payload[index] >> 1)
		tlvLen := (int(payload[index])&0x01)<<8 | int(payload[index+1])
		index += 2
		if index+tlvLen > len(payload) {
			return nil, ErrMalformedTLV
		}
		value := payload[index : index+tlvLen]
		index += tlvLen

		switch {
		case tlvType == tlvEndOfPDU:
			if tlvLen != 0 || index != len(payload) {
				return nil, ErrMalformedTLV
			}
		case tlvType == tlvChassisID && !seenChassis && !seenPort && !seenTTL:
			if tlvLen == 0 {
				return nil, ErrMalformedTLV
			}
			pdu.ChassisID = cloneBytes(value)
			seenChassis = true
		case tlvType == tlvPortID && seenChassis && !seenPort && !seenTTL:
			if tlvLen == 0 {
				return nil, ErrMalformedTLV
			}
			pdu.PortID = cloneBytes(value)
			seenPort = true
		case tlvType == tlvTimeToLive && seenChassis && seenPort && !seenTTL:
			if tlvLen != 2 {
				return nil, ErrMalformedTLV
			}
			pdu.TTL = uint16(value[0])<<8 | uint16(value[1])
			seenTTL = true
		case seenTTL && tlvType >= tlvOptionalFirst && tlvType <= tlvOrganizationally:
			// 可选 TLV（系统名/描述/管理地址/组织自定义等），准入逻辑不关心其内容。
		default:
			// 必需 TLV 乱序、重复或保留类型都按乱序/非法处理。
			return nil, ErrOutOfOrderTLV
		}
	}

	if !seenChassis || !seenPort || !seenTTL {
		return nil, ErrOutOfOrderTLV
	}
	return pdu, nil
}

// Admit 是供帧接收线程调用的入口。
//
// port 标识接收帧的接入端口；frame 是完整的以太网帧（含 14 字节以太网头）。
// 成功（含接受、刷新、撤销）时返回对应条目，TTL=0 撤销成功后返回 nil, nil。
// 帧被拒绝时返回非 nil 错误，调用方应当直接丢弃该帧。
func (t *Table) Admit(port int, frame []byte) (*Entry, error) {
	pdu, err := ParseFrame(frame)

	t.mu.Lock()
	defer t.mu.Unlock()

	if err != nil {
		t.stats.RejectedMalformed++
		return nil, err
	}

	key := neighborKey{chassisID: string(pdu.ChassisID), portID: string(pdu.PortID)}
	entries := t.neighbors[port]

	// TTL=0：撤销。只允许条目属主（同机箱 ID、同端口 ID、同源 MAC）撤销自己。
	if pdu.TTL == 0 {
		if entries == nil {
			t.stats.RejectedSource++
			return nil, ErrSourceMismatch
		}
		existing := entries[key]
		if existing == nil || !bytes.Equal(existing.SrcMAC, pdu.SrcMAC) {
			t.stats.RejectedSource++
			return nil, ErrSourceMismatch
		}
		delete(entries, key)
		t.stats.Withdrawn++
		return nil, nil
	}

	now := t.now()
	if existing := entries[key]; existing != nil {
		// 已有条目：攻击者换源 MAC 既不能篡改也不能借 TTL=0 抹掉它。
		if !bytes.Equal(existing.SrcMAC, pdu.SrcMAC) {
			t.stats.RejectedSource++
			return nil, ErrSourceMismatch
		}
		existing.TTL = time.Duration(pdu.TTL) * time.Second
		existing.UpdatedAt = now
		existing.ExpiresAt = now.Add(existing.TTL)
		t.stats.Refreshed++
		return snapshotEntry(existing), nil
	}

	// 新邻居：先做容量检查，防止伪造帧塞满邻居表。
	if len(entries) >= t.maxPerPort {
		t.stats.RejectedFull++
		return nil, ErrTableFull
	}
	if entries == nil {
		entries = make(map[neighborKey]*Entry)
		t.neighbors[port] = entries
	}

	entry := &Entry{
		PortID:    port,
		SrcMAC:    pdu.SrcMAC,
		ChassisID: cloneBytes(pdu.ChassisID),
		PortTLV:   cloneBytes(pdu.PortID),
		TTL:       time.Duration(pdu.TTL) * time.Second,
		CreatedAt: now,
		UpdatedAt: now,
		ExpiresAt: now.Add(time.Duration(pdu.TTL) * time.Second),
	}
	entries[key] = entry
	t.stats.Accepted++
	return snapshotEntry(entry), nil
}

// Sweep 删除所有已过期的条目并返回被老化的条目数。建议由周期性定时器调用。
func (t *Table) Sweep() int {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	aged := 0
	for port, entries := range t.neighbors {
		for key, entry := range entries {
			if !entry.ExpiresAt.After(now) {
				delete(entries, key)
				aged++
			}
		}
		if len(entries) == 0 {
			delete(t.neighbors, port)
		}
	}
	t.stats.AgedOut += uint64(aged)
	return aged
}

// Lookup 查询指定端口上某个机箱 ID/端口 ID 对应的条目，不存在返回 nil。
func (t *Table) Lookup(port int, chassisID, portID []byte) *Entry {
	t.mu.Lock()
	defer t.mu.Unlock()

	if entries := t.neighbors[port]; entries != nil {
		if entry := entries[neighborKey{chassisID: string(chassisID), portID: string(portID)}]; entry != nil {
			return snapshotEntry(entry)
		}
	}
	return nil
}

// Entries 返回指定端口当前所有条目的副本；传 -1 返回全部端口的条目。
func (t *Table) Entries(port int) []Entry {
	t.mu.Lock()
	defer t.mu.Unlock()

	var out []Entry
	for p, entries := range t.neighbors {
		if port != -1 && p != port {
			continue
		}
		for _, entry := range entries {
			out = append(out, *snapshotEntry(entry))
		}
	}
	return out
}

// Stats 返回准入统计的副本。
func (t *Table) Stats() Stats {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stats
}

func cloneBytes(b []byte) []byte {
	return append([]byte(nil), b...)
}

func cloneMAC(b []byte) net.HardwareAddr {
	return net.HardwareAddr(cloneBytes(b))
}

// snapshotEntry 返回条目的深拷贝，避免调用方在锁外修改表内状态。
func snapshotEntry(entry *Entry) *Entry {
	copy := *entry
	copy.SrcMAC = cloneMAC(entry.SrcMAC)
	copy.ChassisID = cloneBytes(entry.ChassisID)
	copy.PortTLV = cloneBytes(entry.PortTLV)
	return &copy
}
