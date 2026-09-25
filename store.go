package diskdeque

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// 磁盘布局（全部位于调用方指定的目录下）：
//   superblock      元数据：txID / 元素总数 / 块 ID 有序列表 / 分配计数器，带 CRC32
//   block_<id>      块文件：自描述的元素列表，带 CRC32；内容不变的块绝不重写
//   journal         提交日志：新块 ID 列表 + 新元数据镜像，带 CRC32
//   *.tmp           上述文件的原子写入临时文件，恢复时清理

const (
	metaMagic    uint32 = 0x44515342 // "DQSB"
	blockMagic   uint32 = 0x4451424B // "DQBK"
	journalMagic uint32 = 0x44514A4E // "DQJN"
	formatVer    uint16 = 1

	metaFile    = "superblock"
	journalFile = "journal"
)

var errCorrupt = errors.New("diskdeque: corrupt on-disk data")

// meta 是超级块内容。Blocks 为空时表示空队列，此时 Head()==Tail()==0。
type meta struct {
	txID   uint64
	count  uint64
	alloc  uint64 // 块 ID 分配计数器，取模 MaxBlocks 环绕
	blocks []uint64
}

func appendU16(b []byte, v uint16) []byte { return binary.LittleEndian.AppendUint16(b, v) }
func appendU32(b []byte, v uint32) []byte { return binary.LittleEndian.AppendUint32(b, v) }
func appendU64(b []byte, v uint64) []byte { return binary.LittleEndian.AppendUint64(b, v) }

func (m *meta) marshal() []byte {
	buf := make([]byte, 0, 34+8*len(m.blocks))
	buf = appendU32(buf, metaMagic)
	buf = appendU16(buf, formatVer)
	buf = appendU64(buf, m.txID)
	buf = appendU64(buf, m.count)
	buf = appendU64(buf, m.alloc)
	buf = appendU32(buf, uint32(len(m.blocks)))
	for _, id := range m.blocks {
		buf = appendU64(buf, id)
	}
	return appendU32(buf, crc32.ChecksumIEEE(buf))
}

func unmarshalMeta(b []byte) (*meta, error) {
	if len(b) < 34 || crc32.ChecksumIEEE(b[:len(b)-4]) != binary.LittleEndian.Uint32(b[len(b)-4:]) {
		return nil, errCorrupt
	}
	if binary.LittleEndian.Uint32(b[0:4]) != metaMagic || binary.LittleEndian.Uint16(b[4:6]) != formatVer {
		return nil, errCorrupt
	}
	m := &meta{
		txID:  binary.LittleEndian.Uint64(b[6:14]),
		count: binary.LittleEndian.Uint64(b[14:22]),
		alloc: binary.LittleEndian.Uint64(b[22:30]),
	}
	n := binary.LittleEndian.Uint32(b[30:34])
	if len(b) != 34+8*int(n)+4 {
		return nil, errCorrupt
	}
	off := 34
	for i := uint32(0); i < n; i++ {
		m.blocks = append(m.blocks, binary.LittleEndian.Uint64(b[off:off+8]))
		off += 8
	}
	return m, nil
}

// 块文件格式：magic(4) ver(2) count(4) [len(4) payload]* crc32(4)
func marshalBlock(elems [][]byte) []byte {
	n := 14
	for _, e := range elems {
		n += 4 + len(e)
	}
	buf := make([]byte, 0, n)
	buf = appendU32(buf, blockMagic)
	buf = appendU16(buf, formatVer)
	buf = appendU32(buf, uint32(len(elems)))
	for _, e := range elems {
		buf = appendU32(buf, uint32(len(e)))
		buf = append(buf, e...)
	}
	return appendU32(buf, crc32.ChecksumIEEE(buf))
}

func unmarshalBlock(b []byte) ([][]byte, error) {
	if len(b) < 14 || crc32.ChecksumIEEE(b[:len(b)-4]) != binary.LittleEndian.Uint32(b[len(b)-4:]) {
		return nil, errCorrupt
	}
	if binary.LittleEndian.Uint32(b[0:4]) != blockMagic || binary.LittleEndian.Uint16(b[4:6]) != formatVer {
		return nil, errCorrupt
	}
	n := binary.LittleEndian.Uint32(b[6:10])
	off := 10
	end := len(b) - 4
	var elems [][]byte
	for i := uint32(0); i < n; i++ {
		if off+4 > end {
			return nil, errCorrupt
		}
		l := int(binary.LittleEndian.Uint32(b[off : off+4]))
		off += 4
		if off+l > end {
			return nil, errCorrupt
		}
		elems = append(elems, append([]byte(nil), b[off:off+l]...))
		off += l
	}
	if off != end {
		return nil, errCorrupt
	}
	return elems, nil
}

// journal 记录一次提交的全部意图：新块（tmp→final）+ 新元数据镜像。
// 恢复时凭它即可幂等地把提交重放完成。
type journal struct {
	newBlocks []uint64
	metaImage []byte
}

func (j *journal) marshal() []byte {
	buf := make([]byte, 0, 64+len(j.metaImage))
	buf = appendU32(buf, journalMagic)
	buf = appendU16(buf, formatVer)
	buf = appendU32(buf, uint32(len(j.newBlocks)))
	for _, id := range j.newBlocks {
		buf = appendU64(buf, id)
	}
	buf = appendU32(buf, uint32(len(j.metaImage)))
	buf = append(buf, j.metaImage...)
	return appendU32(buf, crc32.ChecksumIEEE(buf))
}

func unmarshalJournal(b []byte) (*journal, error) {
	if len(b) < 18 || crc32.ChecksumIEEE(b[:len(b)-4]) != binary.LittleEndian.Uint32(b[len(b)-4:]) {
		return nil, errCorrupt
	}
	if binary.LittleEndian.Uint32(b[0:4]) != journalMagic || binary.LittleEndian.Uint16(b[4:6]) != formatVer {
		return nil, errCorrupt
	}
	j := &journal{}
	n := binary.LittleEndian.Uint32(b[6:10])
	off := 10
	if off+8*int(n)+4 > len(b)-4 {
		return nil, errCorrupt
	}
	for i := uint32(0); i < n; i++ {
		j.newBlocks = append(j.newBlocks, binary.LittleEndian.Uint64(b[off:off+8]))
		off += 8
	}
	ml := int(binary.LittleEndian.Uint32(b[off : off+4]))
	off += 4
	if off+ml != len(b)-4 {
		return nil, errCorrupt
	}
	j.metaImage = append([]byte(nil), b[off:off+ml]...)
	return j, nil
}

func blockName(id uint64) string    { return "block_" + strconv.FormatUint(id, 10) }
func blockTmpName(id uint64) string { return blockName(id) + ".tmp" }

func fsyncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// writeTmpFile 写临时文件并 fsync，但不 rename。
func writeTmpFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// writeFileAtomic 通过 tmp+rename 原子替换目标文件，并 fsync 目录。
func writeFileAtomic(dir, name string, data []byte) error {
	if err := writeTmpFile(filepath.Join(dir, name+".tmp"), data); err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(dir, name+".tmp"), filepath.Join(dir, name)); err != nil {
		return err
	}
	return fsyncDir(dir)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// recoverDir 在 Open 时执行崩溃恢复：
//   - 存在合法 journal：提交已 durable，幂等前滚（补 rename、落超级块、删 journal）
//   - journal 缺失或损坏：提交未生效，丢弃
//   - 清理所有 *.tmp 临时文件
func recoverDir(dir string) error {
	jb, err := os.ReadFile(filepath.Join(dir, journalFile))
	if err == nil {
		if j, jerr := unmarshalJournal(jb); jerr == nil {
			for _, id := range j.newBlocks {
				tmp := filepath.Join(dir, blockTmpName(id))
				if fileExists(tmp) {
					if err := os.Rename(tmp, filepath.Join(dir, blockName(id))); err != nil {
						return err
					}
				}
			}
			if err := fsyncDir(dir); err != nil {
				return err
			}
			if err := writeFileAtomic(dir, metaFile, j.metaImage); err != nil {
				return err
			}
		}
		if err := os.Remove(filepath.Join(dir, journalFile)); err != nil && !os.IsNotExist(err) {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	// 清理临时文件
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	return fsyncDir(dir)
}

// gcBlocks 删除未被元数据引用的块文件（崩溃残留的孤儿块）。
func gcBlocks(dir string, keep map[uint64]bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	removed := false
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "block_") || strings.HasSuffix(name, ".tmp") {
			continue
		}
		id, err := strconv.ParseUint(strings.TrimPrefix(name, "block_"), 10, 64)
		if err != nil {
			continue
		}
		if !keep[id] {
			if err := os.Remove(filepath.Join(dir, name)); err == nil {
				removed = true
			}
		}
	}
	if removed {
		return fsyncDir(dir)
	}
	return nil
}

func readBlockFile(dir string, id uint64) ([][]byte, error) {
	b, err := os.ReadFile(filepath.Join(dir, blockName(id)))
	if err != nil {
		return nil, err
	}
	elems, err := unmarshalBlock(b)
	if err != nil {
		return nil, fmt.Errorf("block %d: %w", id, err)
	}
	return elems, nil
}

func blockElemCount(dir string, id uint64) (int, error) {
	f, err := os.Open(filepath.Join(dir, blockName(id)))
	if err != nil {
		return 0, err
	}
	defer f.Close()
	hdr := make([]byte, 10)
	if _, err := f.ReadAt(hdr, 0); err != nil {
		return 0, err
	}
	if binary.LittleEndian.Uint32(hdr[0:4]) != blockMagic {
		return 0, errCorrupt
	}
	return int(binary.LittleEndian.Uint32(hdr[6:10])), nil
}
