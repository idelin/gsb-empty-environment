# Delta-CRDT 增量压缩同步协议 (Go)

将全量 State-based CRDT 同步优化为增量 Delta 包传输：各节点记录远端对等节点的
**确认水位**（版本向量），每轮仅为对方生成并发送自上次确认以来的**差异切片**。

## 设计

- **CRDT 内核**：RGA（Replicated Growable Array）字符序列。每个字符节点带唯一
  Dot（站点ID + 计数器），插入生成新节点，删除打墓碑，合并天然幂等、可交换。
- **水位跟踪**：`Replica.acks[peer]` 保存每个远端节点回传的 Ack 版本向量。
- **Delta 生成**：`GenerateDelta(since)` 仅收集水位未覆盖的节点/墓碑，
  包大小正比于变动量，与文档总规模无关（常数级）。
- **全量即特例**：`FullState()` = 零水位 Delta，首次同步与增量走同一编码通道。
- **编码**：紧凑二进制格式，单字符变更（删除+插入）编码为 94 字节。

## 目录

- `dot.go` — Dot 与版本向量（确认水位）
- `document.go` — RGA 文档 CRDT 内核
- `delta.go` — Delta 生成/应用与二进制编解码
- `replica.go` — 网络节点：水位簿记、Delta 收发、Ack 回执
- `delta_test.go` — go test 验收测试
- `cmd/example/main.go` — 运行样例

## 运行样例

```sh
go run ./cmd/example
```

输出：

```
[首次同步] 全量状态包: 370004 字节 (10000 字符文档)
[增量同步] 修改 1 字符的 Delta 包: 94 字节 (压缩率 0.0%)
[空闲心跳] 无变动 Delta 包: 4 字节
[等价性] Bob(增量) == Carol(全量): true, 文本一致: true
```

## 测试结果

```sh
go test ./... -v
```

```
=== RUN   TestDeltaSizeConstantForSingleCharEdit
--- PASS: TestDeltaSizeConstantForSingleCharEdit (0.38s)
    全量状态包大小: 370004 字节 (10000 字符)
    修改 1 字符后的 Delta 包大小: 94 字节
    10 倍规模文档修改 1 字符的 Delta 包大小: 94 字节
=== RUN   TestDeltaEquivalenceWithFullState        --- PASS
=== RUN   TestAckWatermarkSuppressesRedundantDelta --- PASS
=== RUN   TestConcurrentEditsConverge              --- PASS
=== RUN   TestDeltaIdempotentAndCommutative        --- PASS
=== RUN   TestEncodeDecodeRoundTrip                --- PASS
=== RUN   ExampleReplica_SyncTo                    --- PASS
ok  	deltacrdt
```

## 验收边界核对

| 要求 | 结果 |
| --- | --- |
| 10000 字符文档改 1 字符，Delta 常数级字节 | 94 字节（上界断言 128 字节） |
| Delta 大小与文档规模无关 | 10000 与 100000 字符文档均为 94 字节 |
| 增量应用后与全量传输完全等价 | 文本与版本向量均一致（见等价性测试） |
| 仅标准库 | 仅使用 `sort` / `bytes` / `encoding/binary` / `errors` / `fmt` / `testing` |
