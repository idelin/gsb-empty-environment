package aries

import (
	"encoding/binary"
	"os"
	"path/filepath"
)

// Checkpoint 执行模糊检查点:把当前 ATT 与 DPT 作为一条
// CHECKPOINT 日志记录写入 WAL 并强制落盘,同时更新 master 记录。
// 不刷脏页,因此停顿极小;恢复时从检查点出发重建即可。
func (db *DB) Checkpoint() (LSN, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return NilLSN, ErrClosed
	}
	att := make(map[uint64]LSN, len(db.txns))
	for id, txn := range db.txns {
		att[id] = txn.LastLSN
	}
	dpt := db.bp.DirtyPageTable()
	rec := &LogRecord{
		Type:       TypeCheckpoint,
		Checkpoint: &CheckpointData{ActiveTxns: att, DirtyPages: dpt},
	}
	lsn, err := db.lm.Append(rec)
	if err != nil {
		return NilLSN, err
	}
	if err := db.lm.Flush(lsn); err != nil {
		return NilLSN, err
	}
	master := make([]byte, 8)
	binary.LittleEndian.PutUint64(master, uint64(lsn))
	if err := os.WriteFile(filepath.Join(db.dir, "master"), master, 0o644); err != nil {
		return NilLSN, err
	}
	return lsn, nil
}

// ReadMasterCheckpoint 读取 master 记录中保存的检查点 LSN;不存在返回 NilLSN。
func ReadMasterCheckpoint(dir string) (LSN, error) {
	buf, err := os.ReadFile(filepath.Join(dir, "master"))
	if err != nil {
		if os.IsNotExist(err) {
			return NilLSN, nil
		}
		return NilLSN, err
	}
	if len(buf) != 8 {
		return NilLSN, ErrCorruptRecord
	}
	return LSN(binary.LittleEndian.Uint64(buf)), nil
}
