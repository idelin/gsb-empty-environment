// demo 演示 framechain 协议：组帧、任意分块解析、断链定位、
// 检查点后缀验证、重复帧与末帧截断处理。
package main

import (
	"errors"
	"fmt"
	"math/rand"

	fc "framechain"
)

func main() {
	// 1. 构造 5 帧链并编码
	var frames []*fc.Frame
	prev := fc.GenesisPrev
	for i := 0; i < 5; i++ {
		f := &fc.Frame{Seq: uint64(i), Prev: prev, Payload: []byte(fmt.Sprintf("block-%d", i))}
		frames = append(frames, f)
		prev = f.DigestOf()
	}
	var stream []byte
	for _, f := range frames {
		stream = append(stream, f.Encode()...)
	}
	fmt.Printf("1) 编码 %d 帧，共 %d 字节\n", len(frames), len(stream))

	// 2. 随机分块解析：任意分块都能得到同样的帧
	p := fc.NewParser()
	var got []*fc.Frame
	r := rand.New(rand.NewSource(1))
	for i := 0; i < len(stream); {
		n := 1 + r.Intn(37)
		if i+n > len(stream) {
			n = len(stream) - i
		}
		out, err := p.Feed(stream[i : i+n])
		if err != nil {
			panic(err)
		}
		got = append(got, out...)
		i += n
	}
	fmt.Printf("2) 随机分块解析出 %d 帧，残留 %d 字节，FindBreak=%d（-1 表示链完整）\n",
		len(got), p.Finish(), fc.FindBreak(got))

	// 3. 交换相邻帧：各自 CRC 仍正确，但链断
	swapped := append([]*fc.Frame{}, frames...)
	swapped[2], swapped[3] = swapped[3], swapped[2]
	fmt.Printf("3) 交换第 2、3 帧后 FindBreak=%d（定位到断链处）\n", fc.FindBreak(swapped))

	// 4. 中间缺帧：必须报错，不能当未完成
	c := fc.NewChain()
	missing := append(append([]*fc.Frame{}, frames[:2]...), frames[3:]...)
	var err error
	for _, f := range missing {
		if _, err = c.Add(f); err != nil {
			break
		}
	}
	fmt.Printf("4) 抽掉中间一帧后逐帧 Add：err=%v（是 ErrSeqGap: %v）\n", err, errors.Is(err, fc.ErrSeqGap))

	// 5. 检查点 + 后缀验证
	cp := fc.CheckpointOf(frames[2])
	fmt.Printf("5) 检查点 seq=%d，验证后缀 seq=3..4：%v\n", cp.Seq, fc.VerifySuffix(cp, frames[3:]))
	bad := cp
	bad.Digest[0] ^= 0xff
	fmt.Printf("   篡改检查点摘要后验证同一后缀：%v\n", fc.VerifySuffix(bad, frames[3:]))

	// 6. 重复帧
	dup, err := fc.NewChainFrom(cp).Add(frames[3])
	if err != nil {
		panic(err)
	}
	_ = dup
	c2 := fc.NewChain()
	for _, f := range frames {
		if _, err := c2.Add(f); err != nil {
			panic(err)
		}
	}
	dup, err = c2.Add(frames[1])
	fmt.Printf("6) 重发第 1 帧：dup=%v err=%v，链长仍为 %d\n", dup, err, len(c2.Frames()))

	// 7. 末帧截断：缺数据而非坏数据，可视为未完成
	cut := len(frames[4].Encode()) / 2
	p2 := fc.NewParser()
	got2, err := p2.Feed(stream[:len(stream)-cut])
	fmt.Printf("7) 末帧截掉 %d 字节：解析出 %d 帧，err=%v，待补 %d 字节 => 未完成而非损坏\n",
		cut, len(got2), err, p2.Finish())
}
