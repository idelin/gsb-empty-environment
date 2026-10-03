# bigtxn — 大事务分片传输与内存保护限流器

纯 Go 标准库实现。针对“单个事务包含百万级行变更”的场景：

- **自动分片**：`Chunker` 把超大事务切分为带阶段标记（`BEGIN / CONTINUE / END / SINGLE`）的子分片（Chunk），任何时刻只在内存中保留一个分片。
- **内存保护限流**：`MemoryLimiter` 是按字节预算的信号量，在途数据超额时拒绝预留。
- **磁盘溢写**：预算耗尽时 `SpillBuffer` 把分片写入临时磁盘文件再流式回读，防止接收端 OOM；投递成功后立即清理。
- **事务完整性**：`TxnTracker` 校验分片顺序、阶段迁移、行数与滚动 CRC32 校验和，只有 `END` 分片到达且全部分片齐整才允许提交。

## 结构

| 文件 | 职责 |
|---|---|
| `change.go` | 行变更模型与流式 `ChangeSource`（含合成源） |
| `chunker.go` | 分片切分、阶段标记、分片校验和 |
| `limiter.go` | 内存预算限流器（TryReserve / Acquire / Peak） |
| `spill.go` | 临时磁盘溢写缓冲（gob 序列化） |
| `transport.go` | 传输器、事务完整性追踪器、接收端校验 |
| `cmd/demo/main.go` | 验收场景运行样例 |

## 运行样例

```sh
go run ./cmd/demo
```

实际输出（50 万行变更 → 100 个批次流式传输）：

```
=== bigtxn demo: 500k-row transaction sharded transfer ===
transaction id        : 1
rows transferred      : 500000
chunks (batches)      : 100
spilled to disk       : 0 chunks
peak in-flight memory : 0.64 MB (budget 4.00 MB)
peak heap growth      : 4.18 MB (limit 10 MB)
elapsed               : 3.503s
sender checksum       : 303a0ffe
receiver checksum     : 303a0ffe
receiver complete     : true
OK: transaction complete, integrity verified, memory bounded
```

## 测试

```sh
go test -v ./...
```

测试结果（全部通过）：

```
--- PASS: TestChunkerSplitsLargeTransaction   # 50万行 → 100 个带阶段标记的分片
--- PASS: TestChunkerSmallTransaction         # 小事务保持 SINGLE 单分片
--- PASS: TestTransferIntegrity               # 端到端完整性：行数/批数/校验和一致
--- PASS: TestSpillUnderMemoryPressure        # 预算=1字节时全部溢写磁盘且数据无损、文件清理
--- PASS: TestIntegrityDetectsLoss            # 丢片/乱序/篡改被检出
--- PASS: TestPeakMemoryUnder10MB             # 验收门槛：峰值堆增长 4.09 MB < 10 MB
PASS
ok      bigtxn
```

## 验收边界对照

| 验收项 | 结果 |
|---|---|
| 50 万条变更不一次性加载 | 流式 `ChangeSource` + 单片驻留，峰值堆增长 ≈4MB |
| 切分为 100 个批次 | `Chunks=100`，`BEGIN + 98×CONTINUE + END` |
| 追踪总事务完整性 | 顺序/阶段/行数/滚动 CRC32，收发两端校验和一致 |
| 峰值内存 ≤ 10MB | 实测 4.09 MB（`TestPeakMemoryUnder10MB`） |
| 内存超额溢写磁盘 | `TestSpillUnderMemoryPressure`：10/10 分片溢写后无损投递 |
