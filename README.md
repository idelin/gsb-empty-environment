# perfectindex — 固定键集的无冲突最小完美哈希索引

纯 Go 标准库实现。对一组**固定且唯一**的字符串键构建最小完美哈希
（CHD / hash-displace 方案）：每个键被分配到 `[0, n)` 内互不冲突的槽位。
这不是 `map` 的包装——索引由位移数组、指纹数组和槽序原键池组成，
编码布局固定，占用字节可精确报告。

## 性质

- **无冲突**：每个键独占一个槽位，槽位恰好覆盖 `0..n-1`（最小且完美）。
- **未知键零误报**：先比 8-bit 指纹，再与槽内原键完整比较，两级拒绝。
- **可重现且有界**：候选种子来自确定的 splitmix64 序列，
  重试次数上界 `MaxSeedTries = 4096`；同一键集必得同一种子与编码。
- **重复键先诊断**：构建前排序扫描，返回 `*mph.DuplicateError`，
  列出每个重复键及出现次数。
- **失败不丢键**：有界重试耗尽时返回 `*mph.IncompleteError`，
  不返回任何部分索引。

## 编码布局（小端）

| 偏移 | 大小 | 字段 |
|---:|---:|---|
| 0  | 4 | magic `"MPH1"` |
| 4  | 4 | uint32 numKeys |
| 8  | 4 | uint32 numBuckets |
| 12 | 4 | uint32 attempts（胜出构建消耗的种子尝试次数） |
| 16 | 8 | uint64 seed（胜出种子） |
| 24 | 4 | uint32 keyPoolLen |
| 28 | 4 | 保留，置零 |
| 32 | 2×numBuckets | 位移数组，每桶 uint16 |
| .. | 1×numKeys | 指纹数组，每槽 uint8 |
| .. | 4×numKeys | 键偏移表，每槽 uint32（指向键池） |
| .. | keyPoolLen | 键池：原键按槽序拼接 |

槽 `i` 的原键为 `keyPool[offsets[i]:end]`，`end` 为 `offsets[i+1]`
或 `keyPoolLen`（最后一个槽）。`Index.Footprint()` 返回各段精确字节数。

## 用法

```go
ix, err := mph.Build([]string{"go", "rust", "zig"})
if err != nil { /* *mph.DuplicateError 或 *mph.IncompleteError */ }
slot, ok := ix.Lookup("rust")   // 命中：slot ∈ [0, n)
_, ok = ix.Lookup("cobol")      // 未知键：必为 false
data := ix.MarshalBinary()      // 按上表布局编码
ix2, err := mph.Unmarshal(data) // 解码并校验
```

运行演示：`go run ./cmd/demo`；测试：`go test ./mph/ -v`。

## 运行样例（`go run ./cmd/demo`，节选）

```
== small set: 12 language names ==
  built: n=12 seed=13647215125184110592 attempts=7 (bound 4096)
  lookup "go"     -> slot  3, hit=true
  ...
  lookup "cobol"  -> hit=false (unknown key rejected)
  encoded layout footprint (bytes):
    header            32
    displacements      6   (uint16 x 3 buckets)
    fingerprints      12   (uint8  x 12 slots)
    key offsets       48   (uint32 x 12 slots)
    key pool          48   (original keys, slot order)
    TOTAL            146   (12.17 bytes/key)

== large set: 50000 synthetic keys ==
  built: n=50000 seed=10905525725756348110 attempts=3 (bound 4096)
    TOTAL         925032   (18.50 bytes/key)
  verify: 0 known-key misses, 0 unknown-key false hits (200000 probes)

== reproducibility: same set, shuffled input ==
  rebuild: seed=10905525725756348110 attempts=3 identical=true

== duplicate input: diagnosed before build ==
  build error: mph: duplicate keys diagnosed before build: "a" x2, "c" x3
```

## 测试结果（`go test ./mph/ -v`）

```
--- PASS: TestBuildLookupMinimalPerfect   50000 键全部命中且槽位互不冲突
--- PASS: TestUnknownKeysRejected         103000 个未知键探测，零误命中
--- PASS: TestDuplicateDiagnosedBeforeBuild 重复键诊断 "alpha" x3, "delta" x2
--- PASS: TestDeterministicBuild          乱序输入得到同一种子/次数/编码
--- PASS: TestBoundedRetriesExhausted     重试耗尽返回 IncompleteError，不丢键
--- PASS: TestEmptyIndex
--- PASS: TestSingleKey
--- PASS: TestEncodeDecodeRoundTrip       编码往返一致，长度等于 Footprint
--- PASS: TestUnmarshalRejectsCorruption  截断/坏 magic 被拒绝
--- PASS: TestSlotsAreContiguousRange     槽位恰好为 0..n-1
PASS
ok  	example.com/perfectindex/mph
```
