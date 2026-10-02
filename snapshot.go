package aries

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// snapshotMagic 标识归档文件格式版本。
const snapshotMagic = "ARIESSN1"

// ErrBadSnapshot 表示归档文件损坏或被篡改。
var ErrBadSnapshot = errors.New("aries: bad snapshot archive")

// SnapshotMeta 描述一次增量快照。
type SnapshotMeta struct {
	ID        uint64 `json:"id"`
	BaseLSN   LSN    `json:"baseLSN"`   // 上一份快照的水位
	UpToLSN   LSN    `json:"upToLSN"`   // 本快照覆盖到的日志水位
	PageCount int    `json:"pageCount"` // 本快照归档的变更页数
	File      string `json:"file"`
	CreatedAt int64  `json:"createdAt"`
}

type manifest struct {
	Snapshots []SnapshotMeta `json:"snapshots"`
}

// Snapshotter 生成低停顿增量快照:
// 只在缓冲池锁内拷贝"自上次快照以来变更的页"(通常很少),
// 归档文件落盘与 manifest 更新均在锁外完成。
// 归档后顺带把对应脏页刷盘,使磁盘水位随快照推进,缩短恢复时间。
type Snapshotter struct {
	mu      sync.Mutex
	bp      *BufferPool
	lm      *LogManager
	dir     string
	lastLSN LSN
	nextID  uint64
	meta    manifest
}

// NewSnapshotter 创建快照器;目录下已有 manifest 时续接水位与编号。
func NewSnapshotter(bp *BufferPool, lm *LogManager, dir string) (*Snapshotter, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Snapshotter{bp: bp, lm: lm, dir: dir, nextID: 1}
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err == nil {
		if err := json.Unmarshal(data, &s.meta); err != nil {
			return nil, fmt.Errorf("aries: corrupt manifest: %w", err)
		}
		if n := len(s.meta.Snapshots); n > 0 {
			last := s.meta.Snapshots[n-1]
			s.nextID = last.ID + 1
			s.lastLSN = last.UpToLSN
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return s, nil
}

// TakeIncremental 生成一份增量快照,返回其元数据。
// 即使没有变更页也会生成合法的空归档,保证水位单调推进。
func (s *Snapshotter) TakeIncremental() (*SnapshotMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	upto := s.lm.NextLSN()
	base := s.lastLSN

	// 短临界区:仅拷贝变更页。
	pages, err := s.bp.DrainChanged(base, upto)
	if err != nil {
		return nil, err
	}

	meta := SnapshotMeta{
		ID:        s.nextID,
		BaseLSN:   base,
		UpToLSN:   upto,
		PageCount: len(pages),
		File:      fmt.Sprintf("snap-%06d.snap", s.nextID),
		CreatedAt: time.Now().Unix(),
	}
	if err := writeSnapshotFile(filepath.Join(s.dir, meta.File), meta, pages); err != nil {
		return nil, err
	}

	// 推进磁盘水位:把已归档的脏页刷盘(锁外,逐页进行)。
	ids := make([]uint64, len(pages))
	for i, p := range pages {
		ids[i] = p.ID
	}
	if err := s.bp.FlushPages(ids); err != nil {
		return nil, err
	}

	s.meta.Snapshots = append(s.meta.Snapshots, meta)
	if err := s.writeManifestLocked(); err != nil {
		return nil, err
	}
	s.lastLSN = upto
	s.nextID++
	return &meta, nil
}

// Snapshots 返回已知快照元数据列表的拷贝。
func (s *Snapshotter) Snapshots() []SnapshotMeta {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SnapshotMeta, len(s.meta.Snapshots))
	copy(out, s.meta.Snapshots)
	return out
}

// LastLSN 返回当前快照水位。
func (s *Snapshotter) LastLSN() LSN {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastLSN
}

func (s *Snapshotter) writeManifestLocked() error {
	data, err := json.MarshalIndent(&s.meta, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.dir, "manifest.json.tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(s.dir, "manifest.json"))
}

// writeSnapshotFile 写入带 CRC32 校验的归档文件。
func writeSnapshotFile(path string, meta SnapshotMeta, pages []PageSnapshot) error {
	var buf bytes.Buffer
	buf.WriteString(snapshotMagic)
	var fixed [32]byte
	binary.LittleEndian.PutUint64(fixed[0:], meta.ID)
	binary.LittleEndian.PutUint64(fixed[8:], meta.BaseLSN)
	binary.LittleEndian.PutUint64(fixed[16:], meta.UpToLSN)
	binary.LittleEndian.PutUint32(fixed[24:], uint32(len(pages)))
	buf.Write(fixed[:28])
	for _, p := range pages {
		var hdr [20]byte
		binary.LittleEndian.PutUint64(hdr[0:], p.ID)
		binary.LittleEndian.PutUint64(hdr[8:], p.PageLSN)
		binary.LittleEndian.PutUint32(hdr[16:], uint32(len(p.Data)))
		buf.Write(hdr[:])
		buf.Write(p.Data)
	}
	crc := crc32.ChecksumIEEE(buf.Bytes())
	var crcBuf [4]byte
	binary.LittleEndian.PutUint32(crcBuf[:], crc)
	buf.Write(crcBuf[:])
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// readSnapshotFile 读取并校验归档文件。
func readSnapshotFile(path string) (SnapshotMeta, []PageSnapshot, error) {
	var meta SnapshotMeta
	raw, err := os.ReadFile(path)
	if err != nil {
		return meta, nil, err
	}
	if len(raw) < 8+28+4 {
		return meta, nil, ErrBadSnapshot
	}
	if string(raw[:8]) != snapshotMagic {
		return meta, nil, ErrBadSnapshot
	}
	body, crcBytes := raw[:len(raw)-4], raw[len(raw)-4:]
	if crc32.ChecksumIEEE(body) != binary.LittleEndian.Uint32(crcBytes) {
		return meta, nil, fmt.Errorf("%w: crc mismatch in %s", ErrBadSnapshot, path)
	}
	meta.ID = binary.LittleEndian.Uint64(body[8:])
	meta.BaseLSN = binary.LittleEndian.Uint64(body[16:])
	meta.UpToLSN = binary.LittleEndian.Uint64(body[24:])
	count := int(binary.LittleEndian.Uint32(body[32:]))
	off := 8 + 28
	pages := make([]PageSnapshot, 0, count)
	for i := 0; i < count; i++ {
		if off+20 > len(body) {
			return meta, nil, ErrBadSnapshot
		}
		id := binary.LittleEndian.Uint64(body[off:])
		pageLSN := LSN(binary.LittleEndian.Uint64(body[off+8:]))
		dataLen := int(binary.LittleEndian.Uint32(body[off+16:]))
		off += 20
		if dataLen != PageDataSize || off+dataLen > len(body) {
			return meta, nil, ErrBadSnapshot
		}
		data := make([]byte, dataLen)
		copy(data, body[off:off+dataLen])
		off += dataLen
		pages = append(pages, PageSnapshot{ID: id, PageLSN: pageLSN, Data: data})
	}
	return meta, pages, nil
}

// RestoreSnapshots 按序把目录下全部增量快照重放到 dm(用于副本/PITR 恢复)。
// 目录中没有 manifest 时为空操作。
func RestoreSnapshots(dir string, dm *DiskManager) ([]SnapshotMeta, error) {
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("aries: corrupt manifest: %w", err)
	}
	for _, meta := range m.Snapshots {
		_, pages, err := readSnapshotFile(filepath.Join(dir, meta.File))
		if err != nil {
			return nil, err
		}
		for _, p := range pages {
			if err := dm.WritePage(p.ID, p.Data, p.PageLSN); err != nil {
				return nil, err
			}
		}
	}
	if err := dm.Sync(); err != nil {
		return nil, err
	}
	return m.Snapshots, nil
}
