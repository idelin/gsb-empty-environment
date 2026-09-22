// 演示 crcseg：CRC-32 分段计算、按长度合成、缺/坏块解析、
// 配额检查，以及子段替换后的整体验证（不重扫未变数据）。
package main

import (
	"errors"
	"fmt"

	"crcseg/crcseg"
)

func main() {
	p := crcseg.IEEE
	fmt.Println("=== CRC 参数（IEEE / CRC-32/ISO-HDLC）===")
	fmt.Printf("多项式=0x%08X 初值=0x%08X 末异或=0x%08X 空输入摘要=0x%08X\n\n",
		p.Polynomial, p.Init, p.FinalXOR, p.Empty())

	// 1) 字节任意分块写入，结果等于一次计算。
	full := []byte("segmented-crc32-demo-data-0123456789")
	whole, _ := p.Checksum(full)
	h, _ := crcseg.NewHasher(p)
	chunks := [][]byte{full[:9], {}, full[9:22], full[22:27], full[27:]}
	for _, c := range chunks {
		_, _ = h.Write(c)
	}
	fmt.Println("=== 1. 任意分块 == 一次计算 ===")
	fmt.Printf("一次计算: 0x%08X\n分块流算: 0x%08X\n\n", whole, h.Sum32())

	// 2) 任意切分合成。
	bounds := []int{0, 9, 22, 27, len(full)}
	c, _ := crcseg.NewChain(p)
	for i := 0; i < len(bounds)-1; i++ {
		s, _ := crcseg.SegmentFromData(p, full[bounds[i]:bounds[i+1]])
		_ = c.Append(s)
	}
	fmt.Println("=== 2. 任意切分后按长度合成 ===")
	for i := 0; i < c.Len(); i++ {
		s, _ := c.SegmentAt(i)
		fmt.Printf("段%d: 长度=%2d 摘要=0x%08X\n", i, s.Length, s.CRC)
	}
	fmt.Printf("合成整体: 长度=%d 摘要=0x%08X (与一次计算一致: %v)\n\n",
		c.Segment().Length, c.Segment().CRC,
		c.Verify(crcseg.Segment{Length: int64(len(full)), CRC: whole}))

	// 3) 空段恒等。
	e, _ := crcseg.EmptySegment(p)
	solo, _ := crcseg.SegmentFromData(p, full[:5])
	l, _ := crcseg.Combine(p, e, solo)
	r, _ := crcseg.Combine(p, solo, e)
	fmt.Println("=== 3. 空段是恒等元 ===")
	fmt.Printf("空段: %+v\nCombine(空,A)==A: %v\nCombine(A,空)==A: %v\n\n",
		e, l == solo, r == solo)

	// 4) 子段替换后的整体验证，不重扫未变数据。
	parts := [][]byte{[]byte("HEADER|"), []byte("OLD-BODY-0001"), []byte("|TAIL")}
	chain, _ := crcseg.NewChain(p)
	for _, part := range parts {
		s, _ := crcseg.SegmentFromData(p, part)
		_ = chain.Append(s)
	}
	origFull := append(append([]byte{}, parts[0]...), append(parts[1], parts[2]...)...)
	origCRC, _ := p.Checksum(origFull)
	newBody := []byte("NEW-BODY-0002")
	newSeg, _ := crcseg.SegmentFromData(p, newBody)
	fmt.Println("=== 4. 子段替换（不扫描未变段）===")
	fmt.Printf("替换前整体: 0x%08X 验证=%v\n", origCRC,
		chain.Verify(crcseg.Segment{Length: int64(len(origFull)), CRC: origCRC}))
	if err := chain.ReplaceAt(1, newSeg); err != nil {
		panic(err)
	}
	expected := append(append([]byte{}, parts[0]...), append(newBody, parts[2]...)...)
	expCRC, _ := p.Checksum(expected)
	fmt.Printf("替换后整体: 0x%08X 验证=%v（只重算了中段 %d 字节）\n\n",
		chain.Segment().CRC,
		chain.Verify(crcseg.Segment{Length: int64(len(expected)), CRC: expCRC}),
		len(newBody))

	// 5) 相同摘要不同长度不能混用 + 溢出。
	fmt.Println("=== 5. 长度约束 ===")
	err := chain.ReplaceAt(1, crcseg.Segment{Length: 99, CRC: newSeg.CRC})
	fmt.Printf("不同长度替换（摘要恰好相同也拒绝）: %v -> %v\n", err,
		errors.Is(err, crcseg.ErrLengthMismatch))
	_, err = crcseg.Combine(p,
		crcseg.Segment{Length: 1<<63 - 1, CRC: 1},
		crcseg.Segment{Length: 1, CRC: 2})
	fmt.Printf("长度溢出: %v\n\n", errors.Is(err, crcseg.ErrLengthOverflow))

	// 6) 帧解析：缺数据 vs 坏数据；配额在分配前检查。
	fmt.Println("=== 6. 缺数据/坏数据解析与长度配额 ===")
	for _, frame := range [][]byte{
		{0x00, 'o', 'k'}, // 完整块
		{0x01},           // 缺数据
		{0x02},           // 坏数据
		{0x7F},           // 未知标签
	} {
		_, err := crcseg.ParseFrame(frame)
		switch {
		case err == nil:
			fmt.Printf("帧 0x%02X -> 完整块\n", frame[0])
		case errors.Is(err, crcseg.ErrMissingData):
			fmt.Printf("帧 0x%02X -> 缺数据 (%v)\n", frame[0], err)
		case errors.Is(err, crcseg.ErrBadData):
			fmt.Printf("帧 0x%02X -> 坏数据 (%v)\n", frame[0], err)
		}
	}

	col, _ := crcseg.NewCollector(p, 5)
	_, err = col.Write([]byte("12345"))
	fmt.Printf("写入 5/5: err=%v\n", err)
	_, err = col.Write([]byte("6"))
	fmt.Printf("再写 1 字节（分配前检查额度）: errors.Is(ErrQuota)=%v, 已收长度=%d\n",
		errors.Is(err, crcseg.ErrQuota), col.Len())
	if err := col.IngestFrame([]byte{crcseg.TagMissing}); err != nil {
		fmt.Printf("缺帧不消耗额度: %v, 已收长度=%d\n",
			errors.Is(err, crcseg.ErrMissingData), col.Len())
	}
}
