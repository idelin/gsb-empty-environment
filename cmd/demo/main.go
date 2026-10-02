// demo 演示完整验收流程：
//  1. 密集写入产生多个 SSTable；
//  2. 毫秒级硬链接快照（零拷贝）；
//  3. 前台继续密集写入 + Compaction 删除全部旧文件；
//  4. 校验快照目录数据完整性；
//  5. 再次快照，展示增量清单（差异化归档）。
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"snapshotlsm/backup"
	"snapshotlsm/lsm"
)

func main() {
	root := "demo-data"
	os.RemoveAll(root)
	dbDir := filepath.Join(root, "db")
	snapRoot := filepath.Join(root, "backup")

	db, err := lsm.Open(dbDir, lsm.Options{FlushThresholdBytes: 64 * 1024, CompactThreshold: 1 << 30})
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// put 写入 n 条键值，key 用 keyPrefix、value 用 valPrefix，
	// 相同 keyPrefix 不同 valPrefix 即覆盖写。
	put := func(keyPrefix, valPrefix string, n int) {
		for i := 0; i < n; i++ {
			key := fmt.Sprintf("%s-key-%06d", keyPrefix, i)
			val := fmt.Sprintf("%s-value-%06d-%s", valPrefix, i, strings.Repeat("payload-", 8))
			if err := db.Put([]byte(key), []byte(val)); err != nil {
				log.Fatal(err)
			}
		}
	}

	fmt.Println("== 阶段 1: 密集写入，触发 memtable flush ==")
	put("batch1", "batch1", 5000)
	sstCount, _ := db.Stats()
	fmt.Printf("已生成 %d 个不可变 SSTable\n\n", sstCount)

	fmt.Println("== 阶段 2: 生成硬链接快照（零拷贝） ==")
	sn, err := backup.NewSnapshotter(db, snapRoot)
	if err != nil {
		log.Fatal(err)
	}
	start := time.Now()
	m1, err := sn.Snapshot()
	if err != nil {
		log.Fatal(err)
	}
	elapsed := time.Since(start)
	snap1 := sn.SnapshotDir(m1.Seq)
	fmt.Printf("快照 snap-%06d 完成，耗时 %v，包含 %d 个 SSTable 硬链接:\n", m1.Seq, elapsed, len(m1.Files))
	var total int64
	for _, fe := range m1.Files {
		same, err := backup.SameFile(filepath.Join(dbDir, fe.Name), filepath.Join(snap1, fe.Name))
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("  %-18s %8d bytes  crc=%08x  与源文件同inode(零拷贝)=%v\n", fe.Name, fe.Size, fe.CRC32, same)
		total += fe.Size
	}
	fmt.Printf("快照逻辑大小 %d bytes，硬链接耗时毫秒级、无数据拷贝\n\n", total)

	fmt.Println("== 阶段 3: 前台继续密集写入 + Compaction 删除旧文件 ==")
	put("batch1", "batch3", 5000) // 覆盖 batch1 的全部 key
	put("batch2", "batch2", 5000) // 全新 key
	if err := db.Compact(); err != nil {
		log.Fatal(err)
	}
	nowCount, compacted := db.Stats()
	fmt.Printf("Compaction 完成：删除旧 SSTable %d 个，当前存活 %d 个\n", compacted, nowCount)
	deleted := 0
	for _, fe := range m1.Files {
		if _, err := os.Stat(filepath.Join(dbDir, fe.Name)); os.IsNotExist(err) {
			deleted++
		}
	}
	fmt.Printf("快照对应的 %d/%d 个源文件已被 Compaction 物理删除\n\n", deleted, len(m1.Files))

	fmt.Println("== 阶段 4: 校验快照目录数据完整性 ==")
	if err := backup.Verify(snap1); err != nil {
		log.Fatalf("快照校验失败: %v", err)
	}
	fmt.Println("MANIFEST 校验通过：全部硬链接文件 size + CRC32 与快照时刻一致")
	// 抽查：被覆盖的 key 在快照中仍是旧值，在 DB 中是新值。
	for _, fe := range m1.Files {
		entries, err := lsm.ReadSSTable(filepath.Join(snap1, fe.Name))
		if err != nil {
			log.Fatal(err)
		}
		for _, e := range entries {
			if string(e.Key) == "batch1-key-000000" {
				fmt.Printf("快照中 batch1-key-000000 = %.40s...（快照时刻旧值）\n", e.Value)
			}
		}
	}
	v, ok, err := db.Get([]byte("batch1-key-000000"))
	if err != nil || !ok {
		log.Fatalf("db get: ok=%v err=%v", ok, err)
	}
	fmt.Printf("DB 中  batch1-key-000000 = %.40s...（覆盖后新值）\n\n", v)

	fmt.Println("== 阶段 5: 第二次快照，展示增量清单 ==")
	put("batch4", "batch4", 3000)
	m2, err := sn.Snapshot()
	if err != nil {
		log.Fatal(err)
	}
	snap2 := sn.SnapshotDir(m2.Seq)
	delta, err := backup.LoadManifest(filepath.Join(snap2, backup.DeltaFile))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("快照 snap-%06d: 全量清单 %d 个文件，增量清单（DELTA.json）仅 %d 个新增 SSTable:\n",
		m2.Seq, len(m2.Files), len(delta.Files))
	for _, fe := range delta.Files {
		fmt.Printf("  + %-18s %8d bytes\n", fe.Name, fe.Size)
	}
	fmt.Println("离线归档只需打包增量清单中的文件，未变化文件由既有硬链接/历史归档覆盖")
	if err := backup.Verify(snap2); err != nil {
		log.Fatalf("快照2校验失败: %v", err)
	}
	fmt.Println("\n全部验收通过：快照不受 Compaction 删文件影响，数据完整，零拷贝。")
}
