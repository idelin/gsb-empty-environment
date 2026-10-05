package catchup

import (
	"errors"
	"hash/crc32"
)

// Follower 是落后副本：分块暂存快照，全部块一致才安装，
// 安装后只重放快照索引之后的日志。
type Follower struct {
	store *MemStore

	installedGen      uint64
	lastIncludedIndex uint64
	lastIncludedTerm  uint64
	state             []string
	log               []LogEntry

	staged *StagedSnapshot
}

// NewFollower 创建一个副本，store 为其持久化暂存区。
func NewFollower(store *MemStore) *Follower {
	return &Follower{store: store}
}

// ReceiveChunk 接收一个快照块并暂存落盘。
// 返回值 complete 为 true 表示该代次全部块已收齐，可调用 Install。
// 校验失败、过期代次、重复块均不会破坏暂存状态。
func (f *Follower) ReceiveChunk(c Chunk) (bool, error) {
	if crc32.ChecksumIEEE(c.Data) != c.Checksum {
		return false, ErrChecksumMismatch
	}
	if c.Meta.Generation <= f.installedGen {
		return false, nil // 已安装更新或同代快照，忽略
	}
	if f.staged == nil {
		f.staged = f.store.LoadStaging()
	}
	switch {
	case f.staged == nil || c.Meta.Generation > f.staged.Meta.Generation:
		// 快照换代：丢弃旧代次的暂存块，以新代次重新开始。
		f.staged = &StagedSnapshot{
			Meta:   c.Meta,
			Chunks: make([][]byte, c.Meta.NumChunks),
			Got:    make([]bool, c.Meta.NumChunks),
		}
	case c.Meta.Generation < f.staged.Meta.Generation:
		return false, nil // 旧代次的迟到块，忽略
	}
	if c.Index < 0 || c.Index >= c.Meta.NumChunks {
		return false, errors.New("catchup: chunk index out of range")
	}
	if c.Index < len(c.Meta.ChunkChecksums) && c.Meta.ChunkChecksums[c.Index] != c.Checksum {
		return false, ErrChecksumMismatch
	}
	if f.staged.Got[c.Index] {
		return f.staged.Complete(), nil // 重复块（含最后一块），幂等忽略
	}
	f.staged.Chunks[c.Index] = append([]byte(nil), c.Data...)
	f.staged.Got[c.Index] = true
	f.staged.Received++
	f.store.SaveStaging(f.staged)
	return f.staged.Complete(), nil
}

// Install 是安装边界：只有暂存区收齐且整体校验一致，
// 才原子地替换状态、截断日志并清除暂存区；否则报错，状态不变。
func (f *Follower) Install() error {
	if f.staged == nil {
		f.staged = f.store.LoadStaging()
	}
	if f.staged == nil || !f.staged.Complete() {
		return errors.New("catchup: staged snapshot incomplete, refusing to install")
	}
	var buf []byte
	for _, p := range f.staged.Chunks {
		buf = append(buf, p...)
	}
	if crc32.ChecksumIEEE(buf) != f.staged.Meta.DataChecksum {
		return ErrChecksumMismatch
	}
	// 原子替换：以下赋值全部成功才对外可见。
	f.state = decodeState(buf)
	f.installedGen = f.staged.Meta.Generation
	f.lastIncludedIndex = f.staged.Meta.LastIncludedIndex
	f.lastIncludedTerm = f.staged.Meta.LastIncludedTerm
	var kept []LogEntry
	for _, e := range f.log {
		if e.Index > f.lastIncludedIndex {
			kept = append(kept, e)
		}
	}
	f.log = kept
	f.store.ClearStaging()
	f.staged = nil
	return nil
}

// Recover 在重启后调用：若暂存区中已有收齐的快照（安装中断），
// 直接从中恢复安装；否则保留暂存进度，等待剩余块。
func (f *Follower) Recover() error {
	f.staged = f.store.LoadStaging()
	if f.staged != nil && f.staged.Complete() {
		return f.Install()
	}
	return nil
}

// Replay 重放日志：只应用快照索引之后、且尚未应用过的条目。
func (f *Follower) Replay(entries []LogEntry) {
	for _, e := range entries {
		if e.Index <= f.lastIncludedIndex {
			continue // 快照已覆盖，跳过
		}
		if len(f.log) > 0 && e.Index <= f.log[len(f.log)-1].Index {
			continue // 已应用，幂等跳过
		}
		f.log = append(f.log, e)
		f.state = append(f.state, e.Command)
	}
}

// State 返回当前状态机内容。
func (f *Follower) State() []string { return append([]string(nil), f.state...) }

// InstalledGen 返回已安装的快照代次。
func (f *Follower) InstalledGen() uint64 { return f.installedGen }

// LastIncludedIndex 返回已安装快照覆盖的最大日志索引。
func (f *Follower) LastIncludedIndex() uint64 { return f.lastIncludedIndex }

// LogEntries 返回快照之后保留的日志。
func (f *Follower) LogEntries() []LogEntry { return append([]LogEntry(nil), f.log...) }

// StagedReceived 返回暂存区已收到的块数（用于观察传输进度）。
func (f *Follower) StagedReceived() int {
	if f.staged == nil {
		if s := f.store.LoadStaging(); s != nil {
			return s.Received
		}
		return 0
	}
	return f.staged.Received
}
