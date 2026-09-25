// vrec-demo 是单写多读版本化记录存储的可运行样例。
//
// 用法：
//
//	go run ./cmd/vrec-demo [数据目录]
//
// 不传目录时使用系统临时目录下的 vrec-demo。样例会依次演示：
// 固定快照的读事务、墓碑删除、长读不阻塞写、以最老活跃读版本为界的回收、
// 关闭老读者后释放链前缀，以及注入崩溃后的恢复。
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	vrec "vrecstore/vrec"
)

func main() {
	dirFlag := flag.String("dir", "", "数据目录（默认临时目录下 vrec-demo）")
	flag.Parse()

	dir := *dirFlag
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "vrec-demo")
	}
	os.RemoveAll(dir)
	os.MkdirAll(dir, 0o755)

	fmt.Printf("数据目录: %s\n\n", dir)

	db, err := vrec.Open(dir, nil)
	must(err)

	section("1) 提交三个版本；读者固定在 v2")
	must(db.Update(func(tx *vrec.Txn) error { return tx.Put("alice", []byte("A1")) }))
	must(db.Update(func(tx *vrec.Txn) error { return tx.Put("alice", []byte("A2")) }))
	old, err := db.BeginReader()
	must(err)
	fmt.Printf("老读者快照版本 = %d\n", old.Snapshot())
	must(db.Update(func(tx *vrec.Txn) error { return tx.Put("alice", []byte("A3")) }))
	must(get(old, "alice"))
	must(getHead(db, "alice"))
	printHistory(db, "alice")

	section("2) 删除 alice（墓碑）；老读者仍读到 A2")
	must(db.Update(func(tx *vrec.Txn) error { return tx.Delete("alice") }))
	must(get(old, "alice"))
	if _, err := readHead(db, "alice"); errors.Is(err, vrec.ErrNotFound) {
		fmt.Println("新读者读 alice -> ErrNotFound（墓碑）")
	} else {
		must(err)
	}
	printHistory(db, "alice")

	section("3) 老读者打开时执行 GC：只能回收水位之下的前缀，v2 边界保留")
	st, err := db.RunGC()
	must(err)
	fmt.Printf("GC 水位=%d 保留节点=%d 释放节点=%d WAL %d->%d 字节\n",
		st.Watermark, st.NodesKept, st.NodesRemoved, st.OldWALBytes, st.NewWALBytes)
	printHistory(db, "alice")
	must(get(old, "alice"))

	section("4) 关闭老读者后再次 GC：链前缀全部释放")
	must(old.Close())
	st, err = db.RunGC()
	must(err)
	fmt.Printf("GC 水位=%d 保留节点=%d 释放节点=%d WAL %d->%d 字节\n",
		st.Watermark, st.NodesKept, st.NodesRemoved, st.OldWALBytes, st.NewWALBytes)
	printHistory(db, "alice")

	section("5) 长读不阻塞写：读者长期打开，期间连续写入")
	long, err := db.BeginReader()
	must(err)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 5; i++ {
			v := fmt.Sprintf("B%d", i)
			must(db.Update(func(tx *vrec.Txn) error { return tx.Put("bob", []byte(v)) }))
			time.Sleep(2 * time.Millisecond)
		}
		close(done)
	}()
	<-done
	v, err := long.Get("bob")
	if errors.Is(err, vrec.ErrNotFound) {
		fmt.Printf("长读期间 bob 被写了 5 次，但长读快照 v%d 上 bob 不存在（不被新写影响）\n", long.Snapshot())
	} else {
		must(err)
		fmt.Printf("长读读到 bob=%s（其快照内的值）\n", v)
	}
	fmt.Printf("当前 head=%d，写全程未被长读阻塞\n", db.HeadVersion())
	must(long.Close())

	section("6) 注入“已 fsync 后进程崩溃”，重开后事务仍在")
	must(db.Close())
	crashDir, err := simulateCrashAfterSync(dir)
	must(err)
	db2 := crashDir
	must(getHead(db2, "bob"))
	fmt.Printf("重开后 head=%d，崩溃前已 fsync 的提交完整保留\n", db2.HeadVersion())
	must(db2.Close())

	fmt.Println("\n样例完成。")
}

func section(title string) {
	fmt.Printf("\n== %s ==\n", title)
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func get(r *vrec.Reader, key string) error {
	v, err := r.Get(key)
	if err != nil {
		return err
	}
	fmt.Printf("读者(v%d) 读 %s = %q\n", r.Snapshot(), key, v)
	return nil
}

func readHead(db *vrec.DB, key string) ([]byte, error) {
	var out []byte
	err := db.View(func(r *vrec.Reader) error {
		v, err := r.Get(key)
		if err != nil {
			return err
		}
		out = v
		return nil
	})
	return out, err
}

func getHead(db *vrec.DB, key string) error {
	return db.View(func(r *vrec.Reader) error {
		v, err := r.Get(key)
		if err != nil {
			return err
		}
		fmt.Printf("最新读者(v%d) 读 %s = %q\n", r.Snapshot(), key, v)
		return nil
	})
}

func printHistory(db *vrec.DB, key string) {
	h := db.History(key)
	if len(h) == 0 {
		fmt.Printf("%s 的版本链: <空>\n", key)
		return
	}
	fmt.Printf("%s 的版本链（GC 后可直接观察前缀释放）:", key)
	for _, p := range h {
		if p.Tombstone {
			fmt.Printf(" v%d=tomb", p.Version)
		} else {
			fmt.Printf(" v%d=%q", p.Version, p.Value)
		}
	}
	fmt.Println()
}

// simulateCrashAfterSync 用注入点在提交帧 fsync 之后让“进程崩溃”，
// 然后在同一目录重新 Open，模拟进程重启恢复。
func simulateCrashAfterSync(dir string) (*vrec.DB, error) {
	fired := false
	hook := func(c vrec.FailContext) error {
		if c.Stage == vrec.StageTxnSync && !fired {
			fired = true
			return vrec.ErrSimCrash
		}
		return nil
	}
	db, err := vrec.Open(dir, &vrec.Options{FailHook: hook})
	if err != nil {
		return nil, err
	}
	err = db.Update(func(tx *vrec.Txn) error {
		return tx.Put("carol", []byte("C1"))
	})
	if !errors.Is(err, vrec.ErrSimCrash) {
		return nil, fmt.Errorf("期望 ErrSimCrash，得到 %v", err)
	}
	db.Close() // 崩溃实例不再可用
	return vrec.Open(dir, nil)
}
