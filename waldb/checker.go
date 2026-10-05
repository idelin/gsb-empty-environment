package waldb

import (
	"encoding/binary"
	"fmt"
)

// PageWrite 是测试脚本中一次确定的页写（after 镜像）。
type PageWrite struct {
	PageID int32
	Value  uint64
}

// CommittedTxn 记录一个已提交事务及其全部页写，按提交顺序排列。
type CommittedTxn struct {
	TxnID   int64
	Updates []PageWrite
}

// CheckPages 是独立检查器：不依赖恢复代码，仅用事务结果（已提交事务
// 及其页写）和原始页值复算每页期望值，再与页文件逐页比对；
// 并交叉校验每页 pageLSN 必须指向日志中一条与本页值一致的 update/CLR。
func CheckPages(pageFile, logFile *InjectedFile, numPages int, original []uint64, committed []CommittedTxn) error {
	expected := append([]uint64(nil), original...)
	for _, txn := range committed {
		for _, u := range txn.Updates {
			if u.PageID < 0 || int(u.PageID) >= numPages {
				return fmt.Errorf("txn %d: bad page id %d", txn.TxnID, u.PageID)
			}
			expected[u.PageID] = u.Value
		}
	}
	pages := pageFile.Bytes()
	log := logFile.Bytes()
	for i := 0; i < numPages; i++ {
		off := i * PageSize
		if off+PageSize > len(pages) {
			return fmt.Errorf("page %d: page file too short (%d bytes)", i, len(pages))
		}
		pageLSN := int64(binary.LittleEndian.Uint64(pages[off:]))
		value := binary.LittleEndian.Uint64(pages[off+8:])
		if value != expected[i] {
			return fmt.Errorf("page %d: got value %d, want %d", i, value, expected[i])
		}
		if pageLSN == NilLSN {
			continue // 从未被更新过的页
		}
		rec, _, err := decodeAt(log, pageLSN)
		if err != nil {
			return fmt.Errorf("page %d: pageLSN %d does not reference a valid log record", i, pageLSN)
		}
		if rec.Type != recUpdate && rec.Type != recCLR {
			return fmt.Errorf("page %d: pageLSN %d references record type %d, want update/CLR", i, pageLSN, rec.Type)
		}
		if rec.PageID != int32(i) {
			return fmt.Errorf("page %d: pageLSN %d references page %d", i, pageLSN, rec.PageID)
		}
		if rec.After != value {
			return fmt.Errorf("page %d: value %d disagrees with after-image %d at LSN %d", i, value, rec.After, pageLSN)
		}
	}
	return nil
}
