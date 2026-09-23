// 演示：共享只读字典 + 独立块编码、随机解块、限额与坏块恢复。
package main

import (
	"bytes"
	"errors"
	"fmt"

	"blockdict"
)

func main() {
	dict := blockdict.NewDict([]byte(
		"the quick brown fox jumps over the lazy dog. " +
			"pack my box with five dozen liquor jugs. "))
	sum := dict.Digest()
	fmt.Printf("字典 %d 字节, 摘要 %x…\n\n", dict.Len(), sum[:8])

	blocks := [][]byte{
		bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog. "), 8),
		bytes.Repeat([]byte("pack my box with five dozen liquor jugs. "), 8),
		[]byte("a short independent note, no repeats at all here"),
	}

	var stream []byte
	var offs []int
	for i, blk := range blocks {
		offs = append(offs, len(stream))
		stream = append(stream, blockdict.AppendBlock(nil, dict, blk)...)
		fmt.Printf("块 %d: 原始 %4d 字节 -> 编码 %4d 字节\n", i, len(blk), len(stream)-offs[i])
	}

	fmt.Println("\n-- 随机解块（只解第 3 块，不读前块状态）--")
	out, _, err := blockdict.DecodeBlockAt(stream, offs[2], dict, 1<<20)
	fmt.Printf("块 2 解码: %q err=%v\n", out[:20], err)

	fmt.Println("\n-- 输出限额 --")
	_, _, err = blockdict.DecodeBlockAt(stream, offs[0], dict, 64)
	fmt.Println("限额 64 字节:", err)

	fmt.Println("\n-- 错字典拒绝 --")
	wrong := blockdict.NewDict([]byte("some other dictionary"))
	_, _, err = blockdict.DecodeBlockAt(stream, offs[0], wrong, 1<<20)
	fmt.Println("错误字典:", err)

	fmt.Println("\n-- 坏块不拖累下一块 --")
	bad := append([]byte(nil), stream...)
	bad[offs[0]+blockdict.HeaderSize+5] ^= 0xA5 // 损坏块 0 的载荷
	_, next, err := blockdict.DecodeBlockAt(bad, offs[0], dict, 1<<20)
	fmt.Println("块 0:", err)
	out, _, err = blockdict.DecodeBlockAt(bad, next, dict, 1<<20)
	fmt.Printf("跳过坏块后块 1 解码: 成功=%v, 前 20 字节 %q\n", err == nil, out[:20])

	fmt.Println("\n-- 头部损坏后重新同步 --")
	bad2 := append([]byte(nil), stream...)
	bad2[offs[0]+40] ^= 0xFF // 损坏块 0 头部的长度字段
	_, _, err = blockdict.DecodeBlockAt(bad2, offs[0], dict, 1<<20)
	fmt.Println("块 0:", err)
	off := blockdict.FindNext(bad2, offs[0]+1)
	out, _, err = blockdict.DecodeBlockAt(bad2, off, dict, 1<<20)
	fmt.Printf("FindNext 定位到偏移 %d, 块 1 解码成功=%v\n", off, err == nil)

	fmt.Println("\n-- 空字典仍可编码 --")
	empty := blockdict.NewDict(nil)
	buf := blockdict.AppendBlock(nil, empty, bytes.Repeat([]byte("no dict needed! "), 16))
	out, _, err = blockdict.DecodeBlockAt(buf, 0, empty, 1<<20)
	fmt.Printf("空字典往返: 成功=%v, 输出 %d 字节\n", err == nil, len(out))

	if errors.Is(err, blockdict.ErrBadMagic) { // 永不触发，仅示例 errors.Is 用法
		fmt.Println("unreachable")
	}
}
