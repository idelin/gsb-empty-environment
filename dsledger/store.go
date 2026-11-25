package dsledger

import (
	"bufio"
	"crypto/ed25519"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
)

// rosterFile 是固定成员清单的持久化文件名。
const rosterFile = "roster.json"

// persistedRoster 是清单的磁盘表示。
type persistedRoster struct {
	ChainID       string         `json:"chain_id"`
	GenesisDigest Digest         `json:"genesis_digest"`
	Members       []storedMember `json:"members"`
}

type storedMember struct {
	ID        MemberID `json:"id"`
	PublicKey []byte   `json:"public_key"`
}

// store 是基于每成员 WAL 的持久化层。
// 每个成员一个仅追加日志，崩溃后重放即可重建全部内存状态。
type store struct {
	dir   string
	files map[MemberID]*os.File
	bw    map[MemberID]*bufio.Writer
}

func openStore(dir string) (*store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("dsledger: create data dir: %w", err)
	}
	return &store{
		dir:   dir,
		files: make(map[MemberID]*os.File),
		bw:    make(map[MemberID]*bufio.Writer),
	}, nil
}

func memberWALPath(dir string, id MemberID) string {
	return filepath.Join(dir, "member-"+string(id)+".wal")
}

// saveRoster 原子写入固定成员清单（临时文件 + fsync + rename）。
func (s *store) saveRoster(roster *Roster) error {
	pr := persistedRoster{
		ChainID:       roster.ChainID(),
		GenesisDigest: roster.GenesisDigest(),
		Members:       make([]storedMember, 0, len(roster.order)),
	}
	for _, id := range roster.order {
		m := roster.byID[id]
		pr.Members = append(pr.Members, storedMember{ID: id, PublicKey: m.PublicKey})
	}
	data, err := json.MarshalIndent(pr, "", "  ")
	if err != nil {
		return fmt.Errorf("dsledger: marshal roster: %w", err)
	}
	final := filepath.Join(s.dir, rosterFile)
	tmp := final + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("dsledger: open roster temp: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("dsledger: write roster temp: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("dsledger: sync roster temp: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("dsledger: close roster temp: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		return fmt.Errorf("dsledger: rename roster: %w", err)
	}
	return syncDir(s.dir)
}

// loadRoster 读取并重建固定成员清单。
func loadRoster(dir string) (*Roster, error) {
	data, err := os.ReadFile(filepath.Join(dir, rosterFile))
	if err != nil {
		return nil, fmt.Errorf("dsledger: read roster: %w", err)
	}
	var pr persistedRoster
	if err := json.Unmarshal(data, &pr); err != nil {
		return nil, fmt.Errorf("dsledger: parse roster: %w", err)
	}
	members := make([]Member, 0, len(pr.Members))
	for _, sm := range pr.Members {
		members = append(members, Member{ID: sm.ID, PublicKey: ed25519PublicKey(sm.PublicKey)})
	}
	return NewRoster(pr.ChainID, pr.GenesisDigest, members)
}

// append 向成员 WAL 追加一条事件，fsync 成功才返回。
func (s *store) append(id MemberID, ev walEvent) error {
	bw, err := s.writerFor(id)
	if err != nil {
		return err
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("dsledger: marshal event: %w", err)
	}
	frame := make([]byte, 8+4+len(data))
	binary.BigEndian.PutUint64(frame[:8], uint64(len(data)))
	binary.BigEndian.PutUint32(frame[8:12], crc32.ChecksumIEEE(data))
	copy(frame[12:], data)
	if _, err := bw.Write(frame); err != nil {
		return fmt.Errorf("dsledger: write wal: %w", err)
	}
	if err := bw.Flush(); err != nil {
		return fmt.Errorf("dsledger: flush wal: %w", err)
	}
	if err := s.files[id].Sync(); err != nil {
		return fmt.Errorf("dsledger: sync wal: %w", err)
	}
	return nil
}

func (s *store) writerFor(id MemberID) (*bufio.Writer, error) {
	if bw, ok := s.bw[id]; ok {
		return bw, nil
	}
	path := memberWALPath(s.dir, id)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("dsledger: open wal %s: %w", path, err)
	}
	bw := bufio.NewWriter(f)
	s.files[id] = f
	s.bw[id] = bw
	return bw, nil
}

// replay 顺序重放成员 WAL；尾部不完整/损坏帧会被截断丢弃，
// 对应批次崩溃时未确认的写入不会污染状态，而此前已确认事件全部保留。
func (s *store) replay(id MemberID, handle func(walEvent) error) error {
	path := memberWALPath(s.dir, id)
	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("dsledger: open wal for replay: %w", err)
	}
	validBytes := int64(0)
	r := bufio.NewReader(f)
	header := make([]byte, 12)
	for {
		n, err := io.ReadFull(r, header)
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			if n > 0 {
				break // 撕裂的帧头：截断到最后一个完整帧。
			}
			break
		}
		if err != nil {
			return fmt.Errorf("dsledger: read wal header: %w", err)
		}
		length := binary.BigEndian.Uint64(header[:8])
		checksum := binary.BigEndian.Uint32(header[8:12])
		if length > 16*1024*1024 {
			break // 异常长度视为尾部损坏。
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(r, payload); err != nil {
			break // 撕裂的帧体。
		}
		if crc32.ChecksumIEEE(payload) != checksum {
			break // CRC 不匹配：截断损坏帧及之后内容。
		}
		var ev walEvent
		if err := json.Unmarshal(payload, &ev); err != nil {
			break
		}
		if err := handle(ev); err != nil {
			return err
		}
		validBytes += int64(len(header)) + int64(length)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("dsledger: close replay wal: %w", err)
	}
	current, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("dsledger: stat wal: %w", err)
	}
	if current.Size() != validBytes {
		if err := os.Truncate(path, validBytes); err != nil {
			return fmt.Errorf("dsledger: truncate torn wal: %w", err)
		}
		if err := syncDir(s.dir); err != nil {
			return err
		}
	}
	return nil
}

// close 关闭所有打开的 WAL 文件。
func (s *store) close() error {
	var firstErr error
	for id, f := range s.files {
		if err := s.bw[id].Flush(); err != nil && firstErr == nil {
			firstErr = err
		}
		if err := f.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	s.files = nil
	s.bw = nil
	return firstErr
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("dsledger: open dir for sync: %w", err)
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("dsledger: sync dir: %w", err)
	}
	return nil
}

func ed25519PublicKey(raw []byte) ed25519.PublicKey {
	// 直接转换即可：replay 后的 roster 仍会经 NewRoster 校验长度。
	return ed25519.PublicKey(raw)
}
