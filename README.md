# secded — 扩展 Hamming SECDED（纯 Go 标准库实现）

从零实现扩展 Hamming **SECDED**（单错可纠、双错可检）码，含可随机访问的
交织块格式、擦除标记处理、突发错误防护，以及带版本/长度/校验的容器格式。
仅使用 Go 标准库，不依赖任何同功能压缩/纠错库。

## 码字位位置映射

码字长度 `N = 2^M`，数据位 `K = 2^M - M - 1`，最小距离 4。位置从 1 开始编号：

| 位置 | 内容 |
|---|---|
| 1, 2, 4, …, 2^(M-1) | Hamming 校验位（位置 2^j 覆盖编号第 j 位为 1 的所有位置） |
| 其余 3,5,6,7,…, N-1 | 数据位，按位置升序依次存放数据位 0..K-1 |
| N（= 2^M） | 扩展总校验位（使整个码字偶校验） |

在 `uint64` 码字中，bit `p-1` 存放位置 `p` 的值。默认参数 `M=6`：
Hamming(64,57) 扩展码，每码字 57 数据位。

译码由综合征 `s`（Hamming 部分所有置位位置编号的异或）与总校验 `p` 决定：

| s | p | 结论 |
|---|---|---|
| 0 | 0 | 无（可检出的）错误 |
| ≠0 | 1 | 位置 s 单错 → 纠正 |
| 0 | 1 | 总校验位（位置 N）自身出错，数据无需改动 |
| ≠0 | 0 | 双错 → **检出，绝不猜测修复** |

## 交织块格式与随机访问

每块含 `Depth` 个码字（默认 8，须为 8 的倍数）。块内流比特 `t` 承载
第 `i` 个码字的第 `j` 位：`t = j*Depth + i`。块 `b` 起始比特偏移为
`b * N * Depth`，因此任意块可 O(1) 随机访问、独立解码。

连续突发 L 比特对每个码字至多影响 `ceil(L/Depth)` 位：

- `L ≤ Depth`：每字 ≤1 错 → 按单错全部纠正；
- `L ≤ 3*Depth`：每字 ≤3 位 → **标记为擦除时**可恢复（d_min−1 = 3 擦除）；
- `L > 3*Depth`：超出能力，相关块如实报告为不可纠正，输出置零，绝不冒充已修复。

擦除译码对 ≤3 个已知位置穷举全部 2^e 种赋值，取需要额外纠错次数最少
且唯一的解；恰在 `2t + e ≤ 3`（t 个未知错误 + e 个擦除）时保证成功，
否则返回 `ErrErasureAmbiguous` / `ErrTooManyErasures`，绝不猜测。

## 容器格式

```
偏移  大小  字段
0     4     magic "SCD1"
4     2     格式版本 (= 1)
6     1     M（Hamming 校验位数）
7     1     交织深度
8     2     标志（保留 0）
10    2     保留
12    8     DataLen：原始数据字节数
20    8     BlockCount：块数
28    4     解码后数据的 CRC-32C
32    4     头部 0..31 字节的 CRC-32C
36    …     交织载荷，BlockCount * N * Depth / 8 字节
```

- CRC 覆盖**解码后的数据**而非载荷：载荷本就允许被 ECC 修复；数据 CRC
  同时能捕获 ECC 无法发现的误纠正（如 3 比特错误伪装成单错）。
- 解码前先验证 magic、版本、头 CRC 与结构长度；`MaxOutputBytes` 额度
  在分配任何输出缓冲**之前**强制执行（默认 256 MiB）。
- 即使 CRC 通过也不声称数据必然正确：`Result.Corrected` 如实列出被纠正
  的块，`Result.Uncorrectable` 列出失败块（输出对应位置零），
  `Options.WithMapping` 可导出每个输出比特的来源（块/码字/位置/处置结果）。

## 能力边界（明确声明）

- **3 比特错误**：必然伪装成单错综合征而被"纠正"到错误码字
  （测试 `TestTripleErrorMayMiscorrect` 钉死此行为）——
  `StatusCorrected` 永远是最大似然判断，不是正确性证明；
- **4 比特错误**：可能产生零综合征 + 偶校验，完全不可检测；
- 每码字擦除 > 3，或 `2t + e > 3` 的错误+擦除组合：拒绝译码；
- 未标记突发 > Depth、已标记突发 > 3*Depth：报告为不可纠正块。

## 使用

```bash
go run ./cmd/secded encode in.bin out.scd   # 编码文件
go run ./cmd/secded decode out.scd back.bin # 解码（损坏块会报错）
go run ./cmd/secded demo                    # 损坏演示
go test ./...                               # 全部测试
```

## 运行样例（`go run ./cmd/secded demo`）

```
message 70 bytes -> container 164 bytes

clean                                                -> clean
single payload bit flip                              -> corrected blocks [0], data intact: true
double bit flip in one codeword                      -> UNCORRECTABLE blocks [0] (reported, not guessed)
burst of 8 bits (unmarked)                           -> corrected blocks [0], data intact: true
burst of 24 bits (marked as erasure)                 -> corrected blocks [0], data intact: true
burst of 25 bits (marked, beyond capability)         -> UNCORRECTABLE blocks [1] (reported, not guessed)
```

## 测试结果（`go test ./...`）

全部 23 个测试通过。要点：

- `TestExhaustiveSingleError`：m=2,3,4 全部数据字 × 逐位单翻转
  （含校验位与总校验位自身）全部精确纠正；
- `TestExhaustiveDoubleError`：全部双翻转均报告 `StatusDouble`，无误称修复；
- `TestExhaustiveErasures`：(8,4) 码全部 92 种 ≤3 擦除模式 × 全部数据字均恢复；
- `TestParityCheckMatrixCrossVerify`：用独立构造的校验矩阵 H 交叉验证
  每个综合征与每个合法码字 `H·c=0`；
- `TestMiscorrectionCaughtByChecksum`：3 比特误纠正被端到端 CRC 捕获；
- `TestOutputQuota`：超额输出在分配前被拒绝；
- 突发/擦除/随机访问/流式往返见 `block_test.go`、`container_test.go`。

```
ok  	secded	0.222s
```
