package dictneg

import "errors"

// 协议级哨兵错误。宿主可用 errors.Is 判定具体原因。
var (
	// ErrUnknownDictionary 在引用的摘要既不在活动集合也不在暂存集合时返回。
	ErrUnknownDictionary = errors.New("dictneg: unknown dictionary digest")
	// ErrStaleEvent 在事件 Epoch 落后于本地已处理 Epoch 时返回。
	ErrStaleEvent = errors.New("dictneg: stale event epoch")
	// ErrDigestMismatch 在安装内容的实际摘要与事件声明摘要不一致时返回。
	ErrDigestMismatch = errors.New("dictneg: dictionary digest mismatch")
	// ErrBufferFull 在未知摘要帧的暂存队列达到容量上限时返回。
	ErrBufferFull = errors.New("dictneg: staging buffer full")
	// ErrBufferLimit 在非活动摘要暂存总量超过 MaxStagedBytes 时返回。
	ErrBufferLimit = errors.New("dictneg: staging byte limit exceeded")
	// ErrNoActiveDictionary 在尚无任何确认启用的字典时收到数据帧。
	ErrNoActiveDictionary = errors.New("dictneg: no active dictionary")
	// ErrInvalidWire 在报文无法解析或字段非法时返回。
	ErrInvalidWire = errors.New("dictneg: invalid wire message")
	// ErrInjectedFailure 由测试故障注入返回，表示一次被模拟的存储失败。
	ErrInjectedFailure = errors.New("dictneg: injected storage failure")
	// ErrInstallRejected 在接收端回 Nack 拒绝本次安装时返回。
	ErrInstallRejected = errors.New("dictneg: install rejected by acceptor")
)
