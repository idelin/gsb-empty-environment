# Delta-CRDT 增量压缩同步协议 (Go)

将全量 State-based CRDT 优化为增量 Delta 包的网络同步协议。每个节点为每个
远端对等节点维护一条**已确认水位（acknowledged watermark）**，每轮同步只
生成并发送自上次确认之后变更的 Delta 差异切片。仅使用 Go 标准库。

## 设计

```
delta/dot.go        Dot（操作唯一标识）、VersionVector（因果上下文/水位）
delta/document.go   Delta-state RGA 文本 CRDT：操作存储 + Delta 生成/合并
delta/sync.go       Node：按 peer 记录确认水位，DeltaFor/HandleDelta/HandleAck
delta/transport.go  线协议：4 字节大端长度前缀 + gob 信封，对称 Delta/Ack 轮次
cmd/demo/main.go    运行样例：万字符文档，单字符修改的增量同步演示
```

核心机制：

- **Dot = (replica, seq)**：每个操作（插入/删除）有全局唯一、不可变的标识。
- **VersionVector 作为因果上下文**：同一副本的 dot 连续，因此单个水位值即可
  概括任意多的操作，Delta 大小为 O(副本数) 而非 O(操作数)。
- **确认水位**：`Node.acks[peer]` 记录 peer 合并后回执（Ack）中的因果上下文；
  `DeltaFor(peer)` 只收集不被该水位覆盖的操作。
- **全量即增量特例**：对零水位求 Delta 就是全量状态，首次同步与增量同步共用
  同一条代码路径与合并逻辑。
- **合并语义**：操作存储与因果上下文均单调增长，合并幂等、交换、结合；
  兄弟节点按 Lamport 时间戳定序，保证各副本收敛到同一文档序。

线协议一轮（对称）：

```
A -> B: Delta{Ops: 未被 B 水位覆盖的操作, Ctx: A 的版本向量}
B -> A: Delta{...同上...}
A -> B: Ack{Ctx: A 合并后的版本向量}
B -> A: Ack{Ctx: B 合并后的版本向量}
双方各自将对方 Ack 的上下文记入确认水位
```

## 运行

```sh
go test ./... -v     # 测试
go run ./cmd/demo    # 样例（net.Pipe；换成 TCP dial/listen 即可跨机运行）
```

## 验收结果

样例输出（10000 字符文档，修改 1 个字符 = 删除 + 插入共 2 个操作）：

```
document size: 10000 characters (10000 ops)
round 1 (initial sync):    299168 bytes  (full state, 10000 ops)
round 2 (1 char change):      373 bytes  (incremental delta, 2 ops)
B converged to A:                true
B equals full-state transfer:    true
compression ratio:               802x
```

测试输出（关键用例）：

```
=== RUN   TestDeltaConstantSizeOneCharChange
    sync_test.go:66: docSize=10000  full-state=299168 bytes (10002 ops)  delta=373 bytes
--- PASS: TestDeltaConstantSizeOneCharChange (0.04s)
=== RUN   TestDeltaSizeIndependentOfDocSize
    sync_test.go:99: docSize=1000   delta=346 bytes
    sync_test.go:99: docSize=10000  delta=346 bytes
    sync_test.go:99: docSize=50000  delta=346 bytes
--- PASS: TestDeltaSizeIndependentOfDocSize (0.09s)
=== RUN   TestSyncConnOverPipe
    sync_test.go:177: second-round delta over conn: 343 bytes
--- PASS: TestSyncConnOverPipe (0.02s)
PASS
ok  	deltacrdt/delta	0.500s
```

- **常数级 Delta**：文档从 1000 → 50000 元素增长 50 倍，单字符修改的 Delta
  恒为 346 字节（含 gob 类型描述开销；操作本身仅 2 条）。
- **状态等价**：接收端应用 Delta 后与接收全量状态的参照节点逐操作比对一致
  （`EqualState`：因果上下文 + 操作集合完全相同），文档内容亦一致。

测试覆盖：版本向量语义、插入/删除/并发插入收敛、合并幂等、删除与并发插入
收敛、水位缩减 Delta、第三方操作传递、net.Conn 线协议双轮同步、4 节点并发
编辑全网收敛。
