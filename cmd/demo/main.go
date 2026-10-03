// Command demo exercises the compressed bitmap: automatic container
// selection, large-scale set algebra, and fault-injection recovery
// (power loss, disk full, packet loss).
package main

import (
	"fmt"
	"math/rand"
	"time"

	"roaring"
)

func main() {
	fmt.Println("=== 1. 容器自动选择 ===")
	bm := roaring.New()
	for _, id := range []uint32{7, 1000, 65536*3 + 42, 1 << 22} { // 稀疏离散 ID
		bm.Add(id)
	}
	bm.AddRange(10, 0, 65535)   // 密集连续区间
	bm.AddRange(11, 100, 60000) // 密集连续区间
	rnd := rand.New(rand.NewSource(1))
	for i := 0; i < 20000; i++ { // 密集随机
		bm.Add(uint32(rnd.Intn(65536)))
	}
	st := bm.Stats()
	fmt.Printf("Array=%d Bitmap=%d Run=%d Cardinality=%d\n",
		st.ArrayContainers, st.BitmapContainers, st.RunContainers, st.Cardinality)

	fmt.Println("\n=== 2. 大规模交并异或(全量 uint32 空间 ≈ 42.9 亿值/Bitmap) ===")
	const containers = 65536
	big1, big2 := roaring.New(), roaring.New()
	for k := 0; k < containers; k++ {
		big1.AddRange(uint16(k), 0, 65535)
		big2.AddRange(uint16(k), 1000, 65535)
	}
	start := time.Now()
	and := big1.And(big2)
	fmt.Printf("AND: %-12v cardinality=%d\n", time.Since(start), and.Cardinality())
	start = time.Now()
	or := big1.Or(big2)
	fmt.Printf("OR:  %-12v cardinality=%d\n", time.Since(start), or.Cardinality())
	start = time.Now()
	xor := big1.Xor(big2)
	fmt.Printf("XOR: %-12v cardinality=%d\n", time.Since(start), xor.Cardinality())

	fmt.Println("\n=== 3. 崩溃恢复(模拟突发断电) ===")
	fs := roaring.NewSimFS()
	store, err := roaring.OpenStore(fs)
	must(err)
	must(store.Add(1, 11, 22, 33))
	must(store.AddRange(2, 500, 9000))
	must(store.Checkpoint())
	must(store.Add(3, 1, 2, 3))
	before := store.Bitmap()
	fs.Crash() // 突发断电:未 fsync 的数据丢失,已提交的 WAL/快照保留
	recovered, err := roaring.OpenStore(fs)
	must(err)
	fmt.Printf("断电前 cardinality=%d,恢复后 cardinality=%d,状态一致=%v\n",
		before.Cardinality(), recovered.Bitmap().Cardinality(),
		before.Equals(recovered.Bitmap()))

	fmt.Println("\n=== 4. 磁盘满:安全回滚与自愈 ===")
	fs.DiskFull = true
	err = recovered.Add(9, 9, 9)
	fmt.Printf("磁盘满时写入返回: %v\n", err)
	fmt.Printf("失败后内存状态已回滚,一致=%v\n", before.Equals(recovered.Bitmap()))
	fs.DiskFull = false // 磁盘恢复
	must(recovered.Add(9, 9, 9))
	fmt.Printf("磁盘恢复后写入成功,cardinality=%d\n", recovered.Bitmap().Cardinality())

	fmt.Println("\n=== 5. 网络丢包:复制自愈 ===")
	a, b := roaring.ConnPair(16)
	senderConn := &roaring.LossyConn{Inner: a, DropRate: 0.3, Seed: 1}
	receiverConn := &roaring.LossyConn{Inner: b, DropRate: 0.3, Seed: 2}
	replica := roaring.New()
	recv := &roaring.Receiver{Conn: receiverConn, Apply: func(rec []byte) {
		if err := roaring.ApplyRecord(replica, rec); err != nil {
			panic(err)
		}
	}}
	done := make(chan error, 1)
	go func() { done <- recv.Run() }()
	sender := &roaring.Sender{Conn: senderConn, Timeout: 2 * time.Millisecond}
	expected := roaring.New()
	for i := 0; i < 100; i++ {
		key := uint16(i % 4)
		vals := []uint16{uint16(i), uint16(i * 7)}
		must(sender.Send(roaring.EncodeAddRecord(key, vals...)))
		for _, v := range vals {
			expected.Add(uint32(key)<<16 | uint32(v))
		}
	}
	senderConn.Close()
	receiverConn.Close()
	<-done
	fmt.Printf("丢包率 30%%:丢包=%d,投递=%d,复制一致=%v\n",
		senderConn.Dropped, senderConn.Delivered, replica.Equals(expected))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
