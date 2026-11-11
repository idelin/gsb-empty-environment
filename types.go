package cursorpage

import (
	"context"
	"errors"
)

// ErrCursorExpired 表示服务端游标已过期（或无法识别）。读取器捕获该错误后，
// 会放弃游标模式，退回到“按更新时间 + ID 的回溯窗口”重新扫描。
var ErrCursorExpired = errors.New("cursorpage: remote cursor expired or invalid")

// ErrCrash 是测试注入的模拟崩溃。读取器把它原样向上返回，调用方（测试）
// 随后用同一份落盘状态重建 Reader，验证断点续传。
var ErrCrash = errors.New("cursorpage: simulated crash")

// Record 是远端列表中的一条记录。
//
// Key 是业务主键；Version 在记录每次插入/更新/删除时严格递增，
// 因此 (Key, Version) 唯一标识记录的一个版本，是去重与
// “同一版本只交付一次”的判断依据。
// UpdatedAt 是该版本的更新时间（单调时钟刻度即可），用于回溯扫描。
// Deleted 为 true 表示墓碑（删除事件）。
type Record struct {
	Key       string
	Version   int64
	UpdatedAt int64
	Deleted   bool
}

// Page 是一页列表结果。
type Page struct {
	Records []Record
	// NextCursor 是下一页的不透明游标；为空表示游标模式下已经翻到末尾。
	NextCursor string
	// End 为 true 表示本次扫描（回溯模式）已经越过数据起点，扫描完成。
	End bool
}

// RemoteLister 是远端列表服务的抽象，生产环境用真实 SDK 适配，测试用模拟服务端实现。
type RemoteLister interface {
	// ListByCursor 按不透明游标翻页。cursor 为空表示从列表头部开始。
	// 游标过期或不可识别时必须返回包装了 ErrCursorExpired 的错误。
	ListByCursor(ctx context.Context, cursor string, limit int) (Page, error)
	// ListByUpdatedAt 按 (UpdatedAt, Key) 升序回溯扫描。
	// 从 UpdatedAt >= since（UpdatedAt 相等时按 Key、Version 升序）开始返回一页，
	// 并通过 End 告知是否已经扫到数据起点之前（即没有更多数据）。
	ListByUpdatedAt(ctx context.Context, since int64, versionAfter int64, limit int) (Page, error)
}

// Checkpoint 是断点续传状态，整体作为一个原子单元落盘。
//
// 落盘内容同时包含三种续传坐标：
//   - 服务端游标 Cursor / CursorPageCount（当前页在服务端视角的大小）；
//   - 已发出的最后一个业务键 LastKey 及其版本 LastVersion；
//   - 本页已交付位置 PageDelivered（当前页内已交付到第几条）。
type Checkpoint struct {
	SchemaVersion int `json:"schemaVersion"`

	// Mode: "cursor" 或 "backtrack"。
	Mode string `json:"mode"`

	// 游标模式状态。
	Cursor          string `json:"cursor"`
	CursorPageCount int    `json:"cursorPageCount"`
	PageDelivered   int    `json:"pageDelivered"`

	// 已发出的最后一个键及其版本。
	LastKey     string `json:"lastKey"`
	LastVersion int64  `json:"lastVersion"`

	// HighWaterMark 是已观测到的最大 UpdatedAt，用于计算回溯锚点。
	HighWaterMark int64 `json:"highWaterMark"`

	// 回溯模式状态：扫描锚点与 (UpdatedAt, Key) 位置。
	BacktrackAnchor int64 `json:"backtrackAnchor"`
	BTUpdatedAt     int64 `json:"btUpdatedAt"`
	BTVersion       int64 `json:"btVersion"`
	// BTScanStartHWM 记录本趟回溯开始时的高水位；排空时若当前高水位已经
	// 超过它，说明扫描期间有新写入，需要再开一趟重叠窗口扫描，否则结束。
	BTScanStartHWM int64 `json:"btScanStartHWM"`

	// 已交付版本去重表：key -> 最近一次已交付版本号。
	Delivered map[string]int64 `json:"delivered"`
}

// Store 抽象断点状态的持久化。Save 必须是原子的：
// 进程在任意时刻崩溃后，Load 要么读到上一份完整状态，要么读到这份完整状态，
// 绝不允许读到半截内容。
type Store interface {
	Save(ctx context.Context, cp Checkpoint) error
	Load(ctx context.Context) (Checkpoint, bool, error)
}

// FileStore 把 Checkpoint 以 JSON 写入单个文件，写入采用
// “临时文件 + fsync + 原子 rename”，保证落盘原子性。
type FileStore struct {
	path string
}

func NewFileStore(path string) *FileStore { return &FileStore{path: path} }

// Hooks 用于测试与可观测性。所有钩子都在读取器的单循环内同步调用。
type Hooks struct {
	// BeforeFetch 在每次向远端发请求之前调用；返回 ErrCrash 可模拟“拿到数据前崩溃”。
	BeforeFetch func(mode string) error
	// AfterCheckpoint 在每次断点状态原子落盘之后调用；返回 ErrCrash 可模拟
	// “状态已持久化之后崩溃”（此时不允许再重复交付任何已落盘版本）。
	AfterCheckpoint func(cp Checkpoint) error
	// AfterPage 在每页处理完、准备发起下一页请求之前调用，测试用它在翻页间隙变更数据。
	AfterPage func(mode string)
}

// Config 是 Reader 的配置。
type Config struct {
	Remote   RemoteLister
	Store    Store
	PageSize int
	// BacktrackOverlap 是游标过期/游标耗尽后回溯扫描的回溯窗口（时间刻度）。
	// 扫描锚点 = 已知最大更新时间 - BacktrackOverlap，必须覆盖采集滞后区间，
	// 否则在锚点之前插入、且之后不再变化的记录可能被漏掉。
	BacktrackOverlap int64
	// DedupCap 是去重表保留的最大键数；超出后淘汰更新时间最老的条目。
	// 正常采集场景应配置得足够大；淘汰后若同一键的旧版本再次出现，依赖
	// LastVersion 与下游幂等兜底。
	DedupCap int
	Hooks    Hooks
	// BacktrackOnly 为 true 时，全新任务跳过游标模式，直接从时间刻度 0
	// 开始回溯扫描（适合只提供 UpdatedAt 索引、需要全量历史版本的场景）。
	// 已有断点文件时该选项不生效，仍按断点里的模式恢复。
	BacktrackOnly bool
}
