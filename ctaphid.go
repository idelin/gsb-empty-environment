package netsec

import (
	"crypto/rand"
	"errors"
	"sync"
	"time"
)

// CTAPHID 帧布局与协议常量（FIDO CTAP，HID 报告长 64 字节）。
const (
	ReportSize      = 64
	CIDSize         = 4
	InitHeaderSize  = 7                           // CID(4) | CMD(1) | BCNT(2)
	ContHeaderSize  = 5                           // CID(4) | SEQ(1)
	InitPayloadCap  = ReportSize - InitHeaderSize // 57
	ContPayloadCap  = ReportSize - ContHeaderSize // 59
	MaxBlockCount   = 7609
	BroadcastCID    = uint32(0xFFFFFFFF)
	InitNonceSize   = 8
	InitCmdBit      = 0x80
	SeqMask         = 0x7F
	TransactionTTL  = 500 * time.Millisecond
	ProtocolVersion = 2
	Capabilities    = 0x04 // CBOR
)

// CTAPHID 初始化命令。
const (
	CmdPing   = InitCmdBit | 0x01
	CmdMsg    = InitCmdBit | 0x03
	CmdLock   = InitCmdBit | 0x04
	CmdInit   = InitCmdBit | 0x06
	CmdCancel = InitCmdBit | 0x11
	CmdError  = InitCmdBit | 0x3F
)

// CTAPHID 错误码。
const (
	ErrInvalidCmd   byte = 0x01
	ErrInvalidPar   byte = 0x02
	ErrInvalidLen   byte = 0x03
	ErrInvalidSeq   byte = 0x04
	ErrMsgTimeout   byte = 0x05
	ErrChannelBusy  byte = 0x06
	ErrLockRequired byte = 0x0A
	ErrInvalidChan  byte = 0x0B
	ErrOther        byte = 0x7F
)

var errChannelExhausted = errors.New("ctaphid: channel id space exhausted")

// MessageHandler 处理一条组装完成的 CTAPHID 消息（不含 INIT，INIT 由本包直接应答）。
// 返回的 payload 会被自动分包；errorCode 为 0 表示成功，非 0 时以错误报告应答。
type MessageHandler func(cid uint32, cmd byte, payload []byte) (resp []byte, errorCode byte)

// Assembler 是 HID 回调使用的 CTAPHID 组装器，可被多个 goroutine 安全调用。
type Assembler struct {
	mu        sync.Mutex
	reportLen int
	now       func() time.Time
	newCID    func() (uint32, error)
	handler   MessageHandler
	active    map[uint32]*transaction
	channels  map[uint32]struct{}
}

type transaction struct {
	cid      uint32
	cmd      byte
	bcnt     int
	buf      []byte
	nextSeq  byte
	deadline time.Time
}

// NewAssembler 创建组装器。handler 用于处理组装完成的非 INIT 消息。
func NewAssembler(handler MessageHandler) *Assembler {
	return &Assembler{
		reportLen: ReportSize,
		now:       time.Now,
		newCID:    randomCID,
		handler:   handler,
		active:    make(map[uint32]*transaction),
		channels:  make(map[uint32]struct{}),
	}
}

func randomCID() (uint32, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, err
	}
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3]), nil
}

// HandleReport 处理一个 HID 输入报告，返回需要发送给主机的输出报告。
func (a *Assembler) HandleReport(report []byte) [][]byte {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := a.now()
	expiredCID, expiredReports := a.expireLocked(now)
	hadExpired := len(expiredReports) > 0

	if len(report) < a.reportLen {
		return nil
	}
	report = report[:a.reportLen]
	cid := decodeCID(report[0:4])
	cmd := report[4]

	if cmd&InitCmdBit != 0 {
		return a.handleInitLocked(cid, cmd, report)
	}
	return a.handleContinuationLocked(cid, report[4]&SeqMask, report[ContHeaderSize:], expiredCID, hadExpired)
}

// Tick 应由固件周期性调用，返回超时产生的错误报告。
func (a *Assembler) Tick() [][]byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, reports := a.expireLocked(a.now())
	return reports
}

// Busy 报告某通道是否存在未完成的事务。
func (a *Assembler) Busy(cid uint32) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.active[cid]
	return ok
}

// ActiveChannels 返回当前未完成事务的通道数。
func (a *Assembler) ActiveChannels() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.active)
}

func (a *Assembler) expireLocked(now time.Time) (uint32, [][]byte) {
	var cid uint32
	var reports [][]byte
	for id, tx := range a.active {
		if !now.Before(tx.deadline) {
			delete(a.active, id)
			cid = id
			reports = append(reports, errorReport(a.reportLen, id, ErrMsgTimeout))
		}
	}
	return cid, reports
}

func (a *Assembler) handleInitLocked(cid uint32, cmd byte, report []byte) [][]byte {
	if cid != BroadcastCID {
		if _, known := a.channels[cid]; !known {
			return [][]byte{errorReport(a.reportLen, cid, ErrInvalidChan)}
		}
	}

	bcnt := int(uint16(report[5])<<8 | uint16(report[6]))
	if bcnt > MaxBlockCount {
		return [][]byte{errorReport(a.reportLen, cid, ErrInvalidLen)}
	}

	if cid == BroadcastCID {
		// 广播通道只接受携带 8 字节随机数的 INIT。
		if cmd != CmdInit {
			return [][]byte{errorReport(a.reportLen, cid, ErrInvalidCmd)}
		}
		if bcnt != InitNonceSize {
			return [][]byte{errorReport(a.reportLen, cid, ErrInvalidLen)}
		}
		// 消息未完成时，任何其他通道（含广播）的 INIT 都视为抢占。
		if len(a.active) > 0 {
			return [][]byte{errorReport(a.reportLen, cid, ErrChannelBusy)}
		}
		return [][]byte{a.answerInitLocked(report[InitHeaderSize : InitHeaderSize+InitNonceSize])}
	}

	// 其他通道的事务未完成时，拒绝抢占；同通道重发 INIT 则终止旧事务。
	if len(a.active) > 0 {
		if _, mine := a.active[cid]; !mine {
			return [][]byte{errorReport(a.reportLen, cid, ErrChannelBusy)}
		}
		delete(a.active, cid)
	}
	return a.startTransactionLocked(cid, cmd, bcnt, report[InitHeaderSize:])
}

func (a *Assembler) answerInitLocked(nonce []byte) []byte {
	cid, err := a.allocateCIDLocked()
	if err != nil {
		return errorReport(a.reportLen, BroadcastCID, ErrOther)
	}
	pkt := make([]byte, a.reportLen)
	copy(pkt[0:4], encodeCID(BroadcastCID))
	pkt[4] = CmdInit
	// INIT 响应 BCNT 固定 17：8 字节 nonce + 4 字节 CID + 版本/设备版本/能力。
	pkt[5] = 0
	pkt[6] = 17
	copy(pkt[8:16], nonce)
	pkt[16] = byte(cid >> 24)
	pkt[17] = byte(cid >> 16)
	pkt[18] = byte(cid >> 8)
	pkt[19] = byte(cid)
	pkt[20] = ProtocolVersion
	pkt[21] = 0
	pkt[22] = 0
	pkt[23] = Capabilities
	return pkt
}

func (a *Assembler) allocateCIDLocked() (uint32, error) {
	for attempt := 0; attempt < 16; attempt++ {
		cid, err := a.newCID()
		if err != nil {
			return 0, err
		}
		if cid == BroadcastCID {
			continue
		}
		if _, used := a.channels[cid]; used {
			continue
		}
		a.channels[cid] = struct{}{}
		return cid, nil
	}
	return 0, errChannelExhausted
}

func (a *Assembler) startTransactionLocked(cid uint32, cmd byte, bcnt int, fragment []byte) [][]byte {
	tx := &transaction{
		cid:      cid,
		cmd:      cmd,
		bcnt:     bcnt,
		buf:      make([]byte, 0, bcnt),
		nextSeq:  0,
		deadline: a.now().Add(TransactionTTL),
	}
	a.active[cid] = tx
	return a.feedLocked(tx, fragment, 0)
}

func (a *Assembler) handleContinuationLocked(cid uint32, seq byte, payload []byte, expiredCID uint32, hadExpired bool) [][]byte {
	tx, ok := a.active[cid]
	if !ok {
		if hadExpired && cid == expiredCID {
			return [][]byte{errorReport(a.reportLen, cid, ErrMsgTimeout)}
		}
		return [][]byte{errorReport(a.reportLen, cid, ErrInvalidSeq)}
	}
	if seq != tx.nextSeq {
		delete(a.active, cid)
		return [][]byte{errorReport(a.reportLen, cid, ErrInvalidSeq)}
	}
	tx.deadline = a.now().Add(TransactionTTL)
	return a.feedLocked(tx, payload, tx.nextSeq+1)
}

func (a *Assembler) feedLocked(tx *transaction, fragment []byte, nextSeq byte) [][]byte {
	remaining := tx.bcnt - len(tx.buf)
	if remaining > len(fragment) {
		tx.buf = append(tx.buf, fragment...)
		tx.nextSeq = nextSeq
		return nil
	}
	tx.buf = append(tx.buf, fragment[:remaining]...)
	delete(a.active, tx.cid)
	return a.deliverLocked(tx)
}

func (a *Assembler) deliverLocked(tx *transaction) [][]byte {
	if tx.cmd == CmdInit {
		return [][]byte{errorReport(a.reportLen, tx.cid, ErrInvalidCmd)}
	}
	if a.handler == nil {
		return nil
	}
	payload, code := a.handler(tx.cid, tx.cmd, tx.buf)
	if code != 0 {
		return [][]byte{errorReport(a.reportLen, tx.cid, code)}
	}
	return buildPackets(a.reportLen, tx.cid, tx.cmd, payload)
}

func decodeCID(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func encodeCID(cid uint32) []byte {
	return []byte{byte(cid >> 24), byte(cid >> 16), byte(cid >> 8), byte(cid)}
}

func errorReport(reportLen int, cid uint32, code byte) []byte {
	pkt := make([]byte, reportLen)
	copy(pkt[0:4], encodeCID(cid))
	pkt[4] = CmdError
	pkt[5] = 0
	pkt[6] = 1
	pkt[7] = code
	return pkt
}

// buildPackets 把一条 CTAPHID 消息拆成初始化包 + 续包。
func buildPackets(reportLen int, cid uint32, cmd byte, payload []byte) [][]byte {
	var packets [][]byte
	first := make([]byte, reportLen)
	copy(first[0:4], encodeCID(cid))
	first[4] = cmd
	first[5] = byte(len(payload) >> 8)
	first[6] = byte(len(payload))
	chunk := payload
	if len(chunk) > reportLen-InitHeaderSize {
		chunk = chunk[:reportLen-InitHeaderSize]
	}
	copy(first[InitHeaderSize:], chunk)
	packets = append(packets, first)
	payload = payload[len(chunk):]

	seq := byte(0)
	for len(payload) > 0 {
		pkt := make([]byte, reportLen)
		copy(pkt[0:4], encodeCID(cid))
		pkt[4] = seq
		if len(payload) > reportLen-ContHeaderSize {
			chunk = payload[:reportLen-ContHeaderSize]
		} else {
			chunk = payload
		}
		copy(pkt[ContHeaderSize:], chunk)
		packets = append(packets, pkt)
		payload = payload[len(chunk):]
		seq++
	}
	return packets
}
