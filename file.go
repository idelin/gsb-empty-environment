package walstore

import (
	"errors"
	"io"
)

// File 是存储引擎依赖的最小文件接口。
// 实现允许：1) 短写/撕裂写（只落盘前缀）；2) 丢失未 Sync 的数据。
type File interface {
	ReadAt(p []byte, off int64) (int, error)
	WriteAt(p []byte, off int64) (int, error)
	Sync() error
	Size() (int64, error)
}

// OpKind 持久化事件类型。
type OpKind int

const (
	OpWrite OpKind = iota
	OpSync
	OpAck // 提交确认标记（不是文件操作，用于核对“确认晚于落盘”）
)

// Op 一条被追踪的持久化事件。
type Op struct {
	File string // "wal" 或 "db"
	Kind OpKind
	Off  int64  // OpWrite 的写偏移
	Data []byte // OpWrite 实际尝试写入的字节
	Note string // 注解，如 "commit:2" / "ack:2"
}

// Tracer 记录跨文件的全局持久化事件顺序。
type Tracer struct {
	Ops []Op
}

func (t *Tracer) record(op Op) {
	if t != nil {
		t.Ops = append(t.Ops, op)
	}
}

// SimFile 是内存版 File，拥有 volatile（易失）与 durable（已持久）两份镜像：
//   - WriteAt 只写 volatile；
//   - Sync 把 volatile 整体拷贝为 durable；
//   - Crash 丢弃所有未 Sync 的写（持久化丢失）；
//   - TearNextWrite 让下一次 WriteAt 只落盘前缀（短写/撕裂写）。
type SimFile struct {
	name     string
	tracer   *Tracer
	durable  []byte
	volatile []byte
	tear     int // >=0 时，下一次写只持久化前 tear 字节
	nextNote string
}

func NewSimFile(name string, tracer *Tracer) *SimFile {
	return &SimFile{name: name, tracer: tracer, tear: -1}
}

// TearNextWrite 注入短写：下一次 WriteAt 只落盘前 n 字节并返回错误。
func (f *SimFile) TearNextWrite(n int) { f.tear = n }

// Crash 模拟掉电：丢弃所有尚未 Sync 的写。
func (f *SimFile) Crash() { f.volatile = append([]byte(nil), f.durable...) }

// AnnotateNextSync 给下一次 Sync 打注解（用于标记 commit 刷盘点）。
func (f *SimFile) AnnotateNextSync(note string) { f.nextNote = note }

func (f *SimFile) WriteAt(p []byte, off int64) (int, error) {
	n := len(p)
	if f.tear >= 0 {
		if f.tear < n {
			n = f.tear
		}
		f.tear = -1
	}
	f.tracer.record(Op{File: f.name, Kind: OpWrite, Off: off, Data: append([]byte(nil), p[:n]...)})
	end := int(off) + n
	if end > len(f.volatile) {
		nv := make([]byte, end)
		copy(nv, f.volatile)
		f.volatile = nv
	}
	copy(f.volatile[int(off):end], p[:n])
	if n < len(p) {
		return n, errors.New("walstore: short write (torn)")
	}
	return n, nil
}

func (f *SimFile) ReadAt(p []byte, off int64) (int, error) {
	if int(off) >= len(f.volatile) {
		return 0, io.EOF
	}
	n := copy(p, f.volatile[int(off):])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (f *SimFile) Sync() error {
	f.tracer.record(Op{File: f.name, Kind: OpSync, Note: f.nextNote})
	f.nextNote = ""
	f.durable = append([]byte(nil), f.volatile...)
	return nil
}

func (f *SimFile) Size() (int64, error) { return int64(len(f.volatile)), nil }

// ReplayPrefix 把 ops[0:cut] 重放到一对全新的 SimFile 上，然后 Crash，
// 返回崩溃后的文件。若 tear >= 0 且 ops[cut-1] 是写操作，则该写只落盘
// 前 tear 字节（用于枚举“崩溃瞬间正在进行的写只完成前缀”的情形）。
// 注意：重放只作用于 volatile，随后 Crash 丢弃未 Sync 部分，
// 因此只有被 Sync 覆盖的字节才会真正持久。
func ReplayPrefix(ops []Op, cut int, tear int) map[string]*SimFile {
	files := map[string]*SimFile{
		"wal": NewSimFile("wal", nil),
		"db":  NewSimFile("db", nil),
	}
	for i := 0; i < cut; i++ {
		op := ops[i]
		f := files[op.File]
		if f == nil {
			continue // OpAck 等标记
		}
		switch op.Kind {
		case OpWrite:
			data := op.Data
			if i == cut-1 && tear >= 0 && tear < len(data) {
				data = data[:tear]
			}
			end := int(op.Off) + len(data)
			for len(f.volatile) < end {
				f.volatile = append(f.volatile, 0)
			}
			copy(f.volatile[int(op.Off):end], data)
		case OpSync:
			f.durable = append([]byte(nil), f.volatile...)
		}
	}
	for _, f := range files {
		f.Crash()
	}
	return files
}
