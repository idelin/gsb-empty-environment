package waldb

import "io"

// CrashPoint 枚举了正常执行与恢复过程中所有可注入的持久化切点（退出点）。
// 在任一切点崩溃后重启恢复，已提交事务的效果必须保留，未提交的必须消失。
type CrashPoint int

const (
	CutNever CrashPoint = iota // 不注入

	// ---- 正常执行 ----
	CutLogBuffered       // 日志记录已追加到内存缓冲，尚未持久化
	CutCommitDurable     // commit 记录已持久化，END 未写、锁未放
	CutBeforeEnd         // 事务收尾前，END 记录尚未追加
	CutPageFlush         // 脏页已写并同步到页文件（steal）
	CutCheckpointPartial // begin_checkpoint 已持久化，end_checkpoint 未写（部分检查点）

	// ---- 恢复过程 ----
	CutRecoveryAnalysis // 分析完成，重做未开始
	CutRecoveryRedo     // 重做完成，撤销未开始
	CutRecoveryCLR      // CLR 已持久化，对应页尚未修改
	CutRecoveryUndoPage // 撤销已修改页，END 尚未写
	CutRecoveryEnd      // 恢复全部完成
)

func (c CrashPoint) String() string {
	switch c {
	case CutNever:
		return "never"
	case CutLogBuffered:
		return "log-buffered"
	case CutCommitDurable:
		return "commit-durable"
	case CutBeforeEnd:
		return "before-end"
	case CutPageFlush:
		return "page-flush"
	case CutCheckpointPartial:
		return "checkpoint-partial"
	case CutRecoveryAnalysis:
		return "recovery-analysis"
	case CutRecoveryRedo:
		return "recovery-redo"
	case CutRecoveryCLR:
		return "recovery-clr"
	case CutRecoveryUndoPage:
		return "recovery-undo-page"
	case CutRecoveryEnd:
		return "recovery-end"
	}
	return "unknown"
}

// InjectedFile 是可注入持久化切点的文件适配器：
// 写入先落在易失的内存映像上，Sync 之后才持久；Crash 丢弃未同步内容，
// 模拟操作系统崩溃丢失未 fsync 的数据。测试借此在任意切点切断持久化。
type InjectedFile struct {
	image   []byte // 当前内容（含未同步写）
	durable []byte // 最近一次 Sync 时的内容
}

func NewInjectedFile() *InjectedFile { return &InjectedFile{} }

func (f *InjectedFile) Size() int64 { return int64(len(f.image)) }

func (f *InjectedFile) WriteAt(p []byte, off int64) {
	end := off + int64(len(p))
	if end > int64(len(f.image)) {
		grown := make([]byte, end)
		copy(grown, f.image)
		f.image = grown
	}
	copy(f.image[off:], p)
}

func (f *InjectedFile) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(f.image)) {
		return 0, io.EOF
	}
	n := copy(p, f.image[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// Bytes 返回当前映像的拷贝。
func (f *InjectedFile) Bytes() []byte { return append([]byte(nil), f.image...) }

// Sync 使全部已写内容持久化。
func (f *InjectedFile) Sync() { f.durable = append([]byte(nil), f.image...) }

// Crash 模拟掉电：丢弃所有未 Sync 的内容。
func (f *InjectedFile) Crash() { f.image = append([]byte(nil), f.durable...) }

// Truncate 截断文件（用于去掉不完整的日志尾记录）。
func (f *InjectedFile) Truncate(size int64) {
	if size < int64(len(f.image)) {
		f.image = f.image[:size]
	}
}
