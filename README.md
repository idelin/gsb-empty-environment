# bwtblock — 块级 BWT 与逆变换（Go 标准库实现）

从零实现的块级 Burrows-Wheeler 变换、逆变换与片段定位索引。仅使用 Go
标准库，不依赖任何压缩库。

## 特性

- **全字节字母表，无专用哨兵**：0x00–0xFF 全部可作为数据；循环移位
  排序替代哨兵方案，主索引（primary index）随块保存。
- **重复旋转稳定决胜**：周期串（如 `aaaa…`、`ababab…`）产生完全相同的
  旋转，按原始起始位置升序决胜，变换结果确定且可逆。
- **基于后缀排序的 BWT**：倍增法 + 计数排序对循环移位排序。
- **压缩排名索引 + LF 映射**：检查点式 rank 索引支持反向搜索；
  逆变换经 LF 映射 O(n) 重建。
- **片段定位**：`Index.Locate` 返回片段在原文中的全部（循环）出现位置。
- **格式自描述、可校验**：魔数、版本、长度、主索引、CRC-32 齐全；
  主索引越界、计数/长度不符、校验和错误、截断均被拒绝。
- **解码输出额度有界**：`Decode(r, maxOutput)` 在分配前按预算拒绝。

## 复杂度

设块长为 n：

| 操作 | 时间 | 额外内存 |
|---|---|---|
| `Transform` | O(n log n)（倍增轮数 log n，每轮 O(n) 计数排序） | O(n)，约 5–6 个 int 切片（64 位下 ≈40–48n 字节）；**绝不构造任何完整旋转字符串** |
| `Inverse` | O(n) | O(n)（LF 表 + 输出） |
| `NewRankIndex` | O(n) 构建 | ≈2n 字节（每 512 符号一个 256×uint32 检查点） |
| `Rank` 查询 | O(RankStride)=O(512) 上界 | — |
| `NewIndex` | O(n log n)（一次逆变换 + 一次循环移位排序） | n + 2n + 8n 字节 |
| `Locate` | O(m·RankStride + k log k)，m=模式长，k=命中数 | O(k) |

关于定位的说明：无哨兵时周期串会使规范 LF 映射分解为多个环，沿 LF
步进无法跨环恢复绝对位置，因此 `Index` 用与 `Transform` 相同的稳定
循环移位排序器重建精确的行→位置表，保证周期输入下定位结果正确。

## 块格式（版本 1）

所有整数小端：

| 偏移 | 大小 | 字段 |
|---|---|---|
| 0 | 4 | 魔数 `BWTK` |
| 4 | 1 | 格式版本 = 1 |
| 5 | 1 | 标志位（bit0 = `FlagEmpty`） |
| 6 | 2 | 保留，必须为 0 |
| 8 | 8 | 原始块长度（字节） |
| 16 | 8 | 主索引 |
| 24 | 4 | 负载的 CRC-32 (IEEE) |
| 28 | … | 负载：BWT 末列，恰好 length 字节 |

- **空块**单独定义：置 `FlagEmpty`，length=0，primary=0，无负载；
  其逆变换为空字节串。标志与长度不一致会被拒绝。
- 单块最大 2 GiB（`MaxBlockSize`）。
- 块可顺序拼接，`Decode` 每次恰好消费一个块。
- 逆变换输出长度恰好等于头部声明的原长。

## 使用

```sh
go build ./cmd/bwt

bwt encode input.bin output.bwtk     # 编码
bwt decode output.bwtk restored.bin  # 解码（预算 2 GiB）
bwt locate output.bwtk "fragment"    # 片段定位
```

库接口：

```go
last, primary := bwt.Transform(block)        // 正变换
orig, err := bwt.Inverse(last, primary)      // 逆变换

var buf bytes.Buffer
err = bwt.Encode(&buf, block)                // 带格式编码
blk, err := bwt.Decode(r, maxOutput)         // 有界解码 + 校验
orig, err = blk.Inverse()
idx, err := blk.Index()                      // 或 bwt.NewIndex(last, primary)
positions := idx.Locate([]byte("fragment"))
```

## 运行样例

```console
$ bwt encode sample.txt sample.bwtk
encoded 90 bytes -> sample.bwtk
$ bwt decode sample.bwtk roundtrip.txt && cmp sample.txt roundtrip.txt
decoded 90 bytes -> roundtrip.txt   # 完全一致
$ bwt locate sample.bwtk "quick brown"
2 occurrence(s): 4 49
$ xxd sample.bwtk | head -2
00000000: 4257 544b 0100 0000 5a00 0000 0000 0000  BWTK....Z.......
00000010: 4b00 0000 0000 0000 68d5 488a ...        K.......h.H..
```

另验证了空文件（0 字节）、全字节集（0x00–0xFF ×16）、10 万字节
全相同字节（0xFF）的编解码回环，均逐字节一致。

## 测试

`go test ./...`，21 个测试全部通过：

- **穷举参考核验**：长度 ≤12 的全部二进制串、长度 ≤9 的全部三进制串，
  与物化全部旋转的朴素参考实现逐一比对（末列与主索引均相同），并验证
  逆变换回环；
- **回环**：随机块（1…65536 字节）、1 MiB 大块、全相同字节
  （0x00/0x61/0xFF ×100000）、全字节集；
- **边界**：空输入、单字节、主索引越界（负数、=n、巨大值）拒绝；
- **损坏检测**：主索引篡改、CRC 不符、声明长度与实际负载不符、截断、
  魔数/版本/标志错误、空块标志不一致；
- **预算**：声明长度超过 `maxOutput` 在分配前拒绝；
- **定位**：周期文本与全相同字节文本上的片段定位与朴素扫描逐一比对；
- **拼接块**：多块顺序解码。

```console
ok  	bwtblock/bwt	2.039s
```
