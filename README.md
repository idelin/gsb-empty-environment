# 日志结构状态检查点生成器 (lsmcheckpoint)

状态机增量检查点组件，纯 Go 标准库实现。

## 设计

- **按页切片**:内存状态树(KV)按 FNV 哈希切分为 256 个固定页,每页记录最后修改版本号。
- **只读快照**:`BeginSnapshot` 固定(pin)全部当前页;写入时若目标页被固定则
  写时复制(copy-on-write),快照在持续写入下保持一致视图,写路径仅在页被固定时
  多一次单页拷贝。
- **并行生成**:脏页(版本号 > 上次检查点)按 16 页一个分块(chunk),由
  `NumCPU` 个 worker 并行序列化并计算 SHA-256;随后按顺序折叠哈希链,保证确定性。
- **增量差量包**:每个检查点文件只包含自上次检查点以来修改过的页(增量 delta),
  文件头记录前一检查点的链哈希,形成跨文件哈希链。
- **完整性校验**:恢复时逐分块校验 `SHA-256(chunkData)`,并折叠验证链哈希
  `chain_i = SHA-256(chain_{i-1} || chunkHash_i)`,同时校验跨文件链接
  (`prevChain`)。任一处不匹配即拒绝,并通过清单(manifest)反查该分块包含的
  页号,精确定位坏页。

## 文件格式(小端)

```
header:   magic(8) | id u64 | version u64 | prevChain [32]byte | numChunks u32
chunks:   [ dataLen u32 | chunkHash [32]byte | data ]...
manifest: numEntries u32 | [ pageID u32 | version u64 | chunkIdx u32 ]...
trailer:  finalChainHash [32]byte
```

## 结构

- `checkpoint/store.go` — 版本化 KV 状态机、页切片、COW 快照
- `checkpoint/checkpoint.go` — 并行增量检查点生成器
- `checkpoint/format.go` — 文件编解码、分块定位工具 `LocateChunk`
- `checkpoint/recover.go` — 哈希链校验恢复、`CorruptionError` 坏页定位
- `checkpoint/checkpoint_test.go` — 全部测试
- `cmd/demo/main.go` — 运行样例

## 运行

```sh
go test ./... -v
go run ./cmd/demo
```

## 测试结果(go test ./... -v,Apple M 系列,go1.24.2)

```
=== RUN   TestRoundTrip
--- PASS: TestRoundTrip (0.01s)
=== RUN   TestIncrementalDelta
--- PASS: TestIncrementalDelta (0.01s)
=== RUN   TestCorruptThirdChunk
    corruption located: .../ckpt-000000.ckpt: chunk #3 corrupted,
    affected pages [32 33 34 35 36 37 38 39 40 41 42 43 44 45 46 47]: chunk hash mismatch
--- PASS: TestCorruptThirdChunk (0.01s)
=== RUN   TestBrokenChainLinkage
--- PASS: TestBrokenChainLinkage (0.01s)
=== RUN   TestTamperedFinalChain
--- PASS: TestTamperedFinalChain (0.00s)
=== RUN   TestWriteLatencyDuringCheckpoint
    writes=100000 checkpoints=5 p50=250ns p99=5.375µs max=8.620083ms
--- PASS: TestWriteLatencyDuringCheckpoint (0.16s)
PASS
ok  	lsmcheckpoint/checkpoint	0.499s
```

验收边界对应:

- **10 万键值写入期间触发检查点**:`TestWriteLatencyDuringCheckpoint` 在写入
  100k 对的同时并发触发 5 次检查点,p99 写延迟 5.375µs、最大 8.6ms,抖动受控
  (断言上界 p99 < 50ms、max < 500ms);恢复链重建出全部 100k 键。
- **损坏第 3 个分块**:`TestCorruptThirdChunk` 翻转第 3 个分块(索引 2)数据区
  一个字节,恢复流程报 `chunk #3 corrupted` 并列出受影响页 32–47,拒绝坏数据。

## 运行样例(go run ./cmd/demo)

```
== writes ==
wrote 100000 KV pairs, p50=250ns p99=5.375µs max=3.763875ms

== checkpoints ==
ckpt-000000: version=20746  delta-pages=256 chunks=16 chain=13a174bffdd550a6...
ckpt-000001: version=41207  delta-pages=256 chunks=16 chain=da010d25c9a80bb1...
ckpt-000002: version=60876  delta-pages=256 chunks=16 chain=aa3615c5be6bb692...
ckpt-000003: version=80351  delta-pages=256 chunks=16 chain=4a3d80e5861c81f2...
ckpt-000004: version=100000 delta-pages=256 chunks=16 chain=75b726f924fb49be...

== recovery ==
recovered 100000 keys at version 100000, integrity verified against hash chain

== corrupted chunk #3 ==
recovery rejected: .../ckpt-000000.ckpt: chunk #3 corrupted,
affected pages [32 33 34 35 36 37 38 39 40 41 42 43 44 45 46 47]: chunk hash mismatch
```
