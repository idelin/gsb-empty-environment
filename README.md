# rlecomp

一个纯 Go 标准库实现的游程长度（RLE）压缩格式与编解码器，支持离线最优切分与在线有界缓冲流式压缩。

## 格式（版本 1）

```
stream  := frame*
frame   := magic(2) version(1) payloadLen(uvarint) payload crc32(4)
payload := block*
block   := tag(1) body
  tag 0x00 literal: uvarint length, 随后 length 个原始字节
  tag 0x01 repeat:  uvarint count,  随后 1 个被重复的字节
```

- 魔数 `RL`，显式版本字节，帧内含显式长度与 CRC32(IEEE) 校验。
- 块长上限 `MaxBlockLen = 1<<24`，块头上限 `MaxHeaderLen = 5`（1 标签 + 4 字节 uvarint）。
- 帧载荷上限 `MaxFrameLen = MaxFramePayload + MaxBlockLen + MaxHeaderLen`，解码器拒绝超限帧。
- 解码输出额度由调用方给定（`DecodeAll(r, quota)`），超额返回 `ErrQuotaExceeded`，解码输出严格有界。

## 关键性质

- **最坏膨胀有界**：离线模式 `n` 字节输入输出 ≤ `n + MaxHeaderLen + 12`（单帧）。
- **离线最优**：`Compress` 用真实块头成本做最小总字节动态规划切分；小输入对穷举分段做了全量核验（`TestOptimalExhaustive`）。
- **在线有界缓冲**：`Encoder` 最多持有 `window` 个未决字节；尾部游程可能延伸，不提前定界。每个强制边界（窗口/flush）相对离线最优最多多花 `MaxHeaderLen+1` 字节：

  ```
  online ≤ offline + (MaxHeaderLen+1) * (ceil(n/window) + flushes)
  ```

  由 `TestOnlineGap` 验证；`WorstCaseBound` 给出含帧开销的总上界。
- **flush 是显式边界**：`Flush` 结束当前帧（必要时产生空帧），不假设未来输入，也不结束流；流在 `Close` 后的 EOF 处结束。
- **空帧可区分**：空输入编码产生 1 个空帧（8 字节），零帧即零字节流。
- **背压安全**：输出先入内部队列，底层 writer 短写/报错时保留未写字节，由 `Resume`/`Flush` 重试，不丢不重（`TestBackpressureResume`）。

## 使用

```go
// 离线最优
stream := rlecomp.Compress(data)

// 在线流式
enc := rlecomp.NewEncoder(w, 4096)
enc.Write(chunk1)
enc.Flush() // 显式边界，流继续
enc.Write(chunk2)
enc.Close()

// 解码，输出额度有界
out, frames, err := rlecomp.DecodeAll(r, 1<<20)
```

## 运行样例

```
$ go run ./cmd/rledemo
input:            340 bytes
offline optimal:  54 bytes (1 frames)
stream hex:       52 4c 01 2e 00 14 68 65 61 64 65 72 3a 20 61 62 61 62 61 62 61 62 61 62 61 62 01 ac 02 5a 00 08 20 6d 69 64 64 6c 65 20 01 07 51 00 05 20 74 61 69 6c 34 02 eb 3e
online (w=64, 1 flush): 68 bytes (2 frames)
online gap bound: 423 bytes total (actual 68)
decoded:          340 bytes in 2 frames, matches input: true
empty input:      8 bytes, frames=1 (no frame = 0 bytes)
```

## 测试结果

```
$ go test -v ./...
--- PASS: TestHeaderBound            块头长度上限恰为 MaxHeaderLen
--- PASS: TestOptimalExhaustive      全部 ≤10 位二进制串：DP == 穷举最优，编码字节数一致
--- PASS: TestOptimalRandomTernary   随机三符号串 DP == 穷举
--- PASS: TestShortRepeatBoundaries  短重复不误吞相邻字面
--- PASS: TestEmptyFrameVsNoFrame    空帧与无帧可区分
--- PASS: TestFlushIsNotEndOfStream  flush 后流继续
--- PASS: TestConsecutiveFlush       连续 flush 不产生重复字节
--- PASS: TestBackpressureResume     背压恢复不丢不重
--- PASS: TestRepeatedShortBlocks    重复短块往返一致
--- PASS: TestQuota                  解码额度有界，恶意 repeat 被拒
--- PASS: TestCorruptStreams         校验和/版本/魔数/截断/超长块均被拒
--- PASS: TestWorstCaseExpansion     不可压缩输入膨胀 ≤ MaxHeaderLen+12
--- PASS: TestOnlineGap              在线输出 ≤ 离线最优 + 6×边界数（实测 698 ≤ 1736）
--- PASS: TestStreamingMatchesOffline 大窗口单次 flush == 离线最优
ok  	rlecomp
```
