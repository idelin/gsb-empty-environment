package leveled

import (
	"bufio"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
)

// Entry 是一条键值记录。Tombstone 为 true 表示该 Key 已被删除。
type Entry struct {
	Key       string
	Value     []byte
	Seq       uint64
	Tombstone bool
}

// TableMeta 描述一个 SSTable 文件的元信息。
type TableMeta struct {
	ID     uint64
	Level  int
	MinKey string
	MaxKey string
	Size   int64
	Path   string
}

func (m *TableMeta) overlaps(minKey, maxKey string) bool {
	return m.MinKey <= maxKey && minKey <= m.MaxKey
}

func (m *TableMeta) contains(key string) bool {
	return key >= m.MinKey && key <= m.MaxKey
}

const tombstoneFlag = byte(1)

// tableWriter 以追加方式写出一个有序的 SSTable 文件。
// 记录格式: seq(uvarint) | flags(byte) | keyLen(uvarint) | valLen(uvarint) | key | value
type tableWriter struct {
	file  *os.File
	buf   *bufio.Writer
	meta  *TableMeta
	count int
	tmp   [binary.MaxVarintLen64]byte
}

func createTable(dir string, id uint64, level int) (*tableWriter, error) {
	path := filepath.Join(dir, tableFileName(id))
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &tableWriter{
		file: f,
		buf:  bufio.NewWriter(f),
		meta: &TableMeta{ID: id, Level: level, Path: path},
	}, nil
}

func tableFileName(id uint64) string {
	return "sst-" + itoa(id) + ".ldb"
}

func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

func (tw *tableWriter) add(e Entry) error {
	n := binary.PutUvarint(tw.tmp[:], e.Seq)
	if _, err := tw.buf.Write(tw.tmp[:n]); err != nil {
		return err
	}
	flags := byte(0)
	if e.Tombstone {
		flags = tombstoneFlag
	}
	if err := tw.buf.WriteByte(flags); err != nil {
		return err
	}
	n = binary.PutUvarint(tw.tmp[:], uint64(len(e.Key)))
	if _, err := tw.buf.Write(tw.tmp[:n]); err != nil {
		return err
	}
	n = binary.PutUvarint(tw.tmp[:], uint64(len(e.Value)))
	if _, err := tw.buf.Write(tw.tmp[:n]); err != nil {
		return err
	}
	if _, err := tw.buf.WriteString(e.Key); err != nil {
		return err
	}
	if _, err := tw.buf.Write(e.Value); err != nil {
		return err
	}
	if tw.count == 0 || e.Key < tw.meta.MinKey {
		tw.meta.MinKey = e.Key
	}
	if e.Key > tw.meta.MaxKey {
		tw.meta.MaxKey = e.Key
	}
	tw.count++
	return nil
}

// finish 落盘并返回元信息；若一条记录都没写，则删除空文件并返回 nil。
func (tw *tableWriter) finish() (*TableMeta, error) {
	if err := tw.buf.Flush(); err != nil {
		tw.file.Close()
		return nil, err
	}
	if err := tw.file.Sync(); err != nil {
		tw.file.Close()
		return nil, err
	}
	if err := tw.file.Close(); err != nil {
		return nil, err
	}
	if tw.count == 0 {
		os.Remove(tw.meta.Path)
		return nil, nil
	}
	fi, err := os.Stat(tw.meta.Path)
	if err != nil {
		return nil, err
	}
	tw.meta.Size = fi.Size()
	return tw.meta, nil
}

// tableIterator 顺序读取一个 SSTable 文件。
type tableIterator struct {
	file *os.File
	buf  *bufio.Reader
}

func openTable(path string) (*tableIterator, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return &tableIterator{file: f, buf: bufio.NewReader(f)}, nil
}

func (it *tableIterator) next() (Entry, error) {
	var e Entry
	seq, err := binary.ReadUvarint(it.buf)
	if err != nil {
		return e, err
	}
	flags, err := it.buf.ReadByte()
	if err != nil {
		return e, err
	}
	keyLen, err := binary.ReadUvarint(it.buf)
	if err != nil {
		return e, err
	}
	valLen, err := binary.ReadUvarint(it.buf)
	if err != nil {
		return e, err
	}
	key := make([]byte, keyLen)
	if _, err := io.ReadFull(it.buf, key); err != nil {
		return e, err
	}
	val := make([]byte, valLen)
	if _, err := io.ReadFull(it.buf, val); err != nil {
		return e, err
	}
	e.Seq = seq
	e.Tombstone = flags&tombstoneFlag != 0
	e.Key = string(key)
	e.Value = val
	return e, nil
}

func (it *tableIterator) close() {
	it.file.Close()
}

// readAll 读取整个 SSTable 的全部记录（测试与调试用）。
func readAll(path string) ([]Entry, error) {
	it, err := openTable(path)
	if err != nil {
		return nil, err
	}
	defer it.close()
	var out []Entry
	for {
		e, err := it.next()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
}

// tableGet 在单个 SSTable 中查找 key。
func tableGet(path, key string) (Entry, bool, error) {
	it, err := openTable(path)
	if err != nil {
		return Entry{}, false, err
	}
	defer it.close()
	for {
		e, err := it.next()
		if err == io.EOF {
			return Entry{}, false, nil
		}
		if err != nil {
			return Entry{}, false, err
		}
		if e.Key == key {
			return e, true, nil
		}
		if e.Key > key {
			return Entry{}, false, nil
		}
	}
}
