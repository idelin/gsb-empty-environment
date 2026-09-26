package main

import (
	"fmt"
	"rlebits"
)

func main() {
	b := rlebits.FromString("0001110000111100001111")
	fmt.Printf("初始:      %s (len=%d, runs=%d)\n", b.String(), b.Len(), b.NumRuns())

	enc := b.Encode()
	fmt.Printf("编码布局:  magic=%q flags=%02x 后接 uvarint(numRuns,totalLen,runs...)\n", enc[:4], enc[4])
	fmt.Printf("编码字节:  % x\n", enc)
	fmt.Printf("占用:      编码=%dB, 未压缩位图=%dB, 内存索引(pos+ones)=%dB\n\n",
		b.EncodedSize(), b.RawSize(), b.IndexBytes())

	b.Flip(5, 16)
	fmt.Printf("Flip(5,16):  %s (runs=%d)\n", b.String(), b.NumRuns())
	b.Flip(5, 16)
	fmt.Printf("再Flip一次:  %s (runs=%d) —— 已还原\n\n", b.String(), b.NumRuns())

	b.Insert(3, 1, 4) // 段边界插入，延长右段，不产生重复位置
	fmt.Printf("Insert(3,1,4): %s (runs=%d)\n", b.String(), b.NumRuns())
	b.Insert(0, 1, 2) // 头部插入异值，产生新段
	fmt.Printf("Insert(0,1,2): %s (runs=%d)\n\n", b.String(), b.NumRuns())

	b.Delete(4, 15) // 跨多段删除，交界同值段自动合并，不留零长段
	fmt.Printf("Delete(4,15):  %s (runs=%d)\n\n", b.String(), b.NumRuns())

	fmt.Printf("Rank1(8)=%d  Rank0(8)=%d\n", b.Rank1(8), b.Rank0(8))
	if p, ok := b.Select1(3); ok {
		fmt.Printf("Select1(3)=%d (第3个1的位置)\n", p)
	}
	if p, ok := b.Select0(2); ok {
		fmt.Printf("Select0(2)=%d (第2个0的位置)\n", p)
	}

	d, err := rlebits.Decode(b.Encode())
	fmt.Printf("Decode(Encode) 一致: %v (err=%v)\n\n", d.String() == b.String(), err)

	big := rlebits.New()
	big.Insert(0, 0, 100000)
	big.Insert(100000, 1, 50000)
	big.Insert(150000, 0, 100000)
	fmt.Printf("大序列: len=%d runs=%d 编码=%dB 位图=%dB 压缩率=%.1fx\n",
		big.Len(), big.NumRuns(), big.EncodedSize(), big.RawSize(),
		float64(big.RawSize())/float64(big.EncodedSize()))
}
