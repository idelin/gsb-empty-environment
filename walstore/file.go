package walstore

import (
	"io"
	"os"
)

// File 是存储层依赖的最小文件接口。
// 语义约定:
//   - WriteAt 允许短写(返回 n < len(p) 且 err == nil),调用方必须处理;
//   - Sync 返回后,此前写入的数据必须持久(断电不丢);
//   - 未 Sync 的数据在崩溃(Crash)后可以丢失。
type File interface {
	ReadAt(p []byte, off int64) (int, error)
	WriteAt(p []byte, off int64) (int, error)
	Sync() error
	Size() int64
}

// OSFile 是真实文件实现。
type OSFile struct{ f *os.File }

func OpenOSFile(path string) (*OSFile, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}
	return &OSFile{f: f}, nil
}

func (o *OSFile) ReadAt(p []byte, off int64) (int, error)  { return o.f.ReadAt(p, off) }
func (o *OSFile) WriteAt(p []byte, off int64) (int, error) { return o.f.WriteAt(p, off) }
func (o *OSFile) Sync() error                              { return o.f.Sync() }
func (o *OSFile) Size() int64 {
	st, err := o.f.Stat()
	if err != nil {
		return 0
	}
	return st.Size()
}

// MemFile 是可注入故障的内存文件,用于崩溃恢复测试。
// 它区分易失态(volatile)与持久态(durable):
//   - WriteAt 只修改易失态;
//   - Sync 把易失态整体复制为持久态(除非注入持久化丢失);
//   - Crash 丢弃易失态,回滚到持久态,模拟断电。
type MemFile struct {
	volatile []byte
	durable  []byte

	// 故障注入点:
	ShortWriteMax int  // >0 时,每次 WriteAt 最多写入该字节数(模拟短写/撕裂写)
	SyncLoss      bool // true 时 Sync 不持久化任何数据(模拟持久化丢失/假 fsync)
}

func NewMemFile() *MemFile { return &MemFile{} }

// NewMemFileDurable 构造一个持久态为给定内容的文件(用于按切点重放)。
func NewMemFileDurable(durable []byte) *MemFile {
	m := &MemFile{}
	m.durable = append([]byte(nil), durable...)
	m.Crash()
	return m
}

func (m *MemFile) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(m.volatile)) {
		return 0, io.EOF
	}
	n := copy(p, m.volatile[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (m *MemFile) WriteAt(p []byte, off int64) (int, error) {
	if m.ShortWriteMax > 0 && len(p) > m.ShortWriteMax {
		p = p[:m.ShortWriteMax] // 短写:只完成前缀
	}
	end := int(off) + len(p)
	if end > len(m.volatile) {
		nv := make([]byte, end)
		copy(nv, m.volatile)
		m.volatile = nv
	}
	copy(m.volatile[off:], p)
	return len(p), nil
}

func (m *MemFile) Sync() error {
	if m.SyncLoss {
		return nil // 假成功:数据并未持久化
	}
	m.durable = append([]byte(nil), m.volatile...)
	return nil
}

func (m *MemFile) Size() int64 { return int64(len(m.volatile)) }

// Crash 模拟断电:易失态回滚到最近一次成功 Sync 的持久态。
func (m *MemFile) Crash() {
	m.volatile = append([]byte(nil), m.durable...)
}

// DurableBytes 返回当前持久态内容(测试用来枚举切点)。
func (m *MemFile) DurableBytes() []byte { return append([]byte(nil), m.durable...) }
