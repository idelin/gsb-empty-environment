# bwt — 块级 Burrows-Wheeler 变换（纯标准库 Go 实现）

对任意二进制数据（完整 0–255 字节字母表，**不使用独占哨兵字节**）实现块级
BWT 正变换、逆变换、支持 LF 映射的压缩排名索引、片段定位，以及带版本/长度/
校验的容器格式。不依赖任何第三方压缩库。

## 功能

- `Encode(src) (bwt, primary)`：基于后缀排序（循环移位前缀倍增排序）的 BWT，
  保存主索引；重复旋转按原始位置稳定决胜，全相同字节块可逆。
- `Decode(bwt, primary)`：LF 映射逆变换，输出长度恰好等于原长；
  主索引越界返回 `ErrPrimaryIndex`。
- `Index`：压缩排名索引（每 64 字节一个 256 项计数检查点），支持
  `LF`、`Locate`（向后搜索片段定位）、`Inverse`（沿 LF 走主索引还原）。
- `Write` / `Read`：容器格式编解码；`Read(r, maxLen)` 的解码输出额度有界，
  并校验魔数、版本、计数表、主索引范围与 CRC-32。
- 空块单独定义：`n=0, primary=0, CRC=0, 计数全 0, 无负载`，解码为空输出。

## 复杂度

| 阶段 | 时间 | 内存 |
| --- | --- | --- |
| `Encode` | O(n log n)（log n 轮计数排序，每轮 O(n+256)） | ~5n 个 int（64 位约 40n 字节）+ n 字节输出 |
| `Decode` | O(n) | n 个 int + n 字节输出 |
| `Index.LF` / `rank` | O(64)（一个检查点 + 至多 63 字节扫描） | n 字节块 + (n/64+1)·1KB 检查点（约 17n 字节） |
| `Index.Locate` | O(64·\|pat\| + n + k log k) | O(n) |

任何阶段都不会构造完整的旋转字符串（参考实现仅存在于短输入测试中）。

## 容器格式（小端）

```
偏移   大小  字段
0      4     魔数 "BWT1"
4      2     格式版本（当前 1）
6      2     标志位（须为 0）
8      8     原始块长度 n
16     8     主索引（0 <= primary < n；n=0 时为 0）
24     4     原始块的 CRC-32 (IEEE)
28     1024  计数表 counts[256]（每种字节出现次数）
1052   n     BWT 负载
```

解码时依次校验：魔数 → 版本 → 标志位 → 计数表总和 == n → n <= maxLen →
主索引在界内 → 负载直方图 == 计数表 → 逆变换 → CRC-32。

## 使用

```sh
go build -o bwt ./cmd/bwt
./bwt encode in.bin out.bwt
./bwt decode out.bwt back.bin [maxLen]
```

## 运行样例

```
$ head -c 200000 /dev/urandom > sample.bin
$ ./bwt encode sample.bin sample.bwt
encoded 200000 bytes -> sample.bwt
$ ls -l sample.bin sample.bwt
-rw-r--r--  200000  sample.bin
-rw-r--r--  201052  sample.bwt   # 200000 字节负载 + 1052 字节头
$ ./bwt decode sample.bwt sample.out
decoded 200000 bytes -> sample.out
$ cmp sample.bin sample.out && echo ROUNDTRIP-OK
ROUNDTRIP-OK
$ ./bwt decode sample.bwt too.out 1000
bwt: bwt: declared length exceeds output limit   # 输出额度有界
```

## 测试结果

`go test -v ./...`（Go 1.24，darwin/arm64）：

```
=== RUN   TestExhaustiveShort        # 短输入穷举：与显式构造全部旋转的参考实现比对
    bwt_test.go:79: verified 1093 strings
--- PASS: TestExhaustiveShort (0.00s)
--- PASS: TestEmptyBlock (0.00s)            # 空块单独定义
--- PASS: TestAllSameByte (0.02s)           # 全相同字节可逆（含 65536 字节）
--- PASS: TestFullByteAlphabet (0.00s)      # 全字节全集，无哨兵
--- PASS: TestRoundTripRandom (0.19s)       # 含 1<<20 字节大块
--- PASS: TestPeriodicBlocks (0.00s)        # 周期块稳定决胜
--- PASS: TestPrimaryIndexOutOfRange (0.00s)
--- PASS: TestCorruptionDetection (0.00s)   # 计数/校验和/负载/魔数/版本/截断
--- PASS: TestBoundedOutput (0.00s)         # 解码输出额度
--- PASS: TestContainerRoundTrip (0.02s)
--- PASS: TestIndexInverseAndLF (0.00s)     # LF 环闭合
--- PASS: TestLocate (0.00s)                # 片段定位 vs 朴素搜索
--- PASS: TestLocateRandom (0.01s)
PASS
ok  	bwt	0.646s
```
