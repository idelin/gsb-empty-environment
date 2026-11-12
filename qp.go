package netsec

import "fmt"

// 24 位 QPN / PSN 的位宽与掩码。
const (
	QPNBits = 24
	PSNBits = 24

	QPNMask uint32 = (1 << QPNBits) - 1
	PSNMask uint32 = (1 << PSNBits) - 1

	// QPN 0 按惯例保留，不参与分配。
	MinQPN uint32 = 1
	MaxQPN uint32 = QPNMask
)

// QPState 是 RC 队列对在软 RNIC 中的生命周期状态。
type QPState int

const (
	// StateReset：已分配但尚未与对端建立连接。
	StateReset QPState = iota
	// StateRTR：已登记对端 GID 并转入就绪传输（模型中 RTR/RTS 合并）。
	StateRTR
	// StateError：因 PSN 越窗等违规累计达到阈值而进入错误态。
	StateError
	// StateDestroyed：已销毁，QPN 进入隔离期。
	StateDestroyed
)

func (s QPState) String() string {
	switch s {
	case StateReset:
		return "RESET"
	case StateRTR:
		return "RTR/RTS"
	case StateError:
		return "ERROR"
	case StateDestroyed:
		return "DESTROYED"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", int(s))
	}
}

// DropReason 描述入站报文未获准入的具体原因。
type DropReason int

const (
	// DropNone：报文通过准入。
	DropNone DropReason = iota
	// DropQPNNotFound：目的 QPN 没有对应存活 QP。
	DropQPNNotFound
	// DropNotConnected：QP 未处于可收发的连接态。
	DropNotConnected
	// DropGIDMismatch：源 GID 与连接建立时登记的对端 GID 不符。
	DropGIDMismatch
	// DropPSNOutOfWindow：PSN 落在接收窗口之外。
	DropPSNOutOfWindow
)

func (r DropReason) String() string {
	switch r {
	case DropNone:
		return "ACCEPT"
	case DropQPNNotFound:
		return "QPN_NOT_FOUND"
	case DropNotConnected:
		return "QP_NOT_CONNECTED"
	case DropGIDMismatch:
		return "GID_MISMATCH"
	case DropPSNOutOfWindow:
		return "PSN_OUT_OF_WINDOW"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", int(r))
	}
}

// InboundPacket 是入站处理交给准入模块的最小报文视图。
type InboundPacket struct {
	// SourceGID：报文源 GID（由网络层解封装得到，不可由攻击者伪造校验位）。
	SourceGID GID
	// DestQPN：报文目的 QPN，取值会按 24 位截断。
	DestQPN uint32
	// PSN：报文携带的 24 位包序号，取值会按 24 位截断。
	PSN uint32
}

// Verdict 是一次准入判定的结果。
type Verdict struct {
	// Accepted 为 true 时报文可交付 RC 传输层继续处理。
	Accepted bool
	// Reason 在拒绝时给出原因，接受时为 DropNone。
	Reason DropReason
	// QPN 为本次判定针对的 QPN（24 位）。
	QPN uint32
	// Duplicate 标记接受的是窗口内的重复/重传报文（PSN 早于期望 PSN）。
	Duplicate bool
	// ExpectedPSN 为判定后该 QP 的期望 PSN。
	ExpectedPSN uint32
}

// QPInfo 是 QP 内部状态的只读快照，供观测与测试使用。
type QPInfo struct {
	QPN           uint32
	State         QPState
	PeerGID       GID
	ExpectedPSN   uint32
	PSNWindow     int
	PSNMisses     int
	GIDViolations int
}

// qp 是队列对的内部表示。
type qp struct {
	qpn            uint32
	state          QPState
	peerGID        GID
	expectedPSN    uint32
	psnWindow      int
	psnMisses      int
	gidViolations  int
	errorThreshold int
}

// info 生成 QP 状态快照。
func (q *qp) info() QPInfo {
	return QPInfo{
		QPN:           q.qpn,
		State:         q.state,
		PeerGID:       q.peerGID,
		ExpectedPSN:   q.expectedPSN,
		PSNWindow:     q.psnWindow,
		PSNMisses:     q.psnMisses,
		GIDViolations: q.gidViolations,
	}
}

// psnDelta 计算两个 24 位 PSN 之间带符号的最短回绕距离：
// 结果为 in - expected，范围落在 [-2^23, 2^23-1]，
// 因此在 PSN 自然回绕处仍能得到正确的先后关系。
func psnDelta(in, expected uint32) int32 {
	d := (in - expected) & PSNMask
	if d > PSNMask/2 {
		d |= ^PSNMask
	}
	return int32(d)
}
