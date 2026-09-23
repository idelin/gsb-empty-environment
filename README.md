# slz — 滑动窗口 LZ77 编解码（最小位解析）

纯标准库 Go 实现，不依赖任何压缩库。窗口 4096 字节，支持重叠回拷，
编码端采用最小位数（optimal parsing）动态规划，而非最长匹配贪心。

## 格式（版本 1）

位流按 LSB-first 打包。

```
文件头:  magic "SLZ1" (4B) | version u8 | reserved u8 |
         totalLen u64le | crc32le u32 (IEEE, 解码输出校验)
块:      rawLen u32le | bitLen u32le | flags u8 | payload ceil(bitLen/8) B
```

Token：

```
字面量:  0 + 8bit 字节                      -> 9 bit
匹配:    1 + 12bit (dist-1) + 6bit (len-3)  -> 19 bit
```

- 距离 ∈ [1, 4096]，长度 ∈ [3, 66]，均有界。
- `flags` bit0 = checkpoint：解码器清空字典窗口，此后任何匹配不得
  引用 checkpoint 之前产生的字节；未置位的块共享滑动窗口
  （距离同时受窗口大小和 checkpoint 以来已产出字节数约束）。
- 解码输出额度由 `Decompress(src, maxOutput)` 显式限制；
  解码后校验 totalLen 与 CRC-32。

## 编码决胜规则（固定）

1. 等成本时匹配优先于字面量；
2. 等成本的匹配长度中取最长；
3. 等长匹配中取最小距离（哈希链按最近优先遍历，仅严格更长才替换）。

## 最小位解析

匹配 token 成本固定 19 bit，与长度、距离无关，因此每个位置只需
最长匹配即可覆盖所有可达长度。后向 DP：

```
cost[i] = min( 9 + cost[i+1],  19 + min_{l∈[3,bestLen[i]]} cost[i+l] )
```

匹配索引为 3 字节前缀哈希链：`prev` 表固定 `WindowSize` 槽，
链深上限 32，内存与搜索深度均有界，窗口外条目不可达。

## 运行样例

```
$ go run ./cmd/slzdemo
sample:      1800 bytes
compressed:  140 bytes (7.8%)
round trip:  true
counterexample "abcxbcdefgabcdefg": greedy=128 bits, optimal=118 bits
chunked (4 blocks, shared window): 173 bytes, round trip: true
run of 5000 'a': 209 bytes, round trip: true
```

贪心失败反例：偏移 10 处贪心取 `abc`(len3, 19bit) + `defg`(len4, 19bit)；
最优解输出字面量 `a`(9bit) 后取 `bcdefg`(len6, 19bit)，省 10 bit。

## 测试结果

```
$ go test ./...
--- PASS: TestOptimalParseExhaustive   (穷举 {a,b,c}* 长度≤9 共 29524 串，
                                        编码器解析成本 == 暴力最优，且往返一致)
--- PASS: TestGreedyCounterexample     (贪心 128 bit > 最优 118 bit)
--- PASS: TestDistanceOneRepeat        (dist=1 重复拷贝扩展正确)
--- PASS: TestEmpty
--- PASS: TestRandomChunkedRoundTrip   (300 组随机数据 × 随机分块 ×
                                        reset/共享窗口两种模式)
--- PASS: TestWindowEvictionLongRange  (距离 > 窗口的重复不可引用，往返一致)
--- PASS: TestRejectFutureReference    (引用尚未产生的字节 → 拒绝)
--- PASS: TestRejectReferenceBeforeCheckpoint (引用 checkpoint 之前 → 拒绝)
--- PASS: TestCrossBlockWindowContinues(跨块共享窗口合法引用)
--- PASS: TestOverlapCopyExtends       (重叠回拷 dist<len)
--- PASS: TestOutputBudget             (输出额度有界)
--- PASS: TestCorruptions              (magic/version/crc/长度/截断)
ok      slz
```
