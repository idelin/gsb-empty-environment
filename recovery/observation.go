package recovery

// CloseResult 是公开可观察的关闭结果。
type CloseResult uint8

const (
	// CloseNone 表示尚未观察到关闭。
	CloseNone CloseResult = iota
	// CloseClean 表示正常关闭。
	CloseClean
	// CloseDropped 表示掉线,最后一帧是否已发送未知。
	CloseDropped
)

func (c CloseResult) String() string {
	switch c {
	case CloseClean:
		return "clean"
	case CloseDropped:
		return "dropped"
	default:
		return "none"
	}
}

// Observation 是一次公开观察,包含线路、时隙、帧长、重连请求及关闭结果。
// 恢复策略的每一步选择只能依赖由 Observation 序列构成的共同公开历史。
type Observation struct {
	Line      int         // 线路编号
	Slot      int         // 时隙编号
	FrameLen  int         // 帧长
	Reconnect bool        // 是否观察到重连请求
	Close     CloseResult // 关闭结果
}
