# OCC：无阻塞自旋乐观并发控制引擎

Go 标准库实现的经典三阶段 OCC（Optimistic Concurrency Control）KV 引擎，
附带基于全局读写锁的悲观方案作为吞吐对照基线。

## 架构

```
occ/      OCC 引擎
  store.go   Engine：sync.Map 键空间 + 全局版本计数器 + seqlock 序列号 + 提交自旋锁
  record.go  原子记录槽：unsafe.Pointer 无锁发布不可变版本化记录
  txn.go     事务：ReadSet/WriteSet、三阶段提交、无锁只读验证
  spinlock.go CAS 自旋锁（仅提交阶段短暂持有）
lockkv/   对照组：事务全程持有全局 RWMutex 的悲观锁存储
cmd/demo/ 运行样例：读多写少负载下 OCC vs 锁 的吞吐对比
```

## 三阶段执行内核

1. **读取阶段**：`Get` 先查私有 WriteSet（read-your-writes），再经原子记录槽
   无锁读取，把 `(key, version)` 记入 ReadSet；`Set` 只写私有 WriteSet，
   对其他事务不可见。
2. **验证阶段**：`Commit` 时校验 ReadSet 中每个 key 的当前版本是否仍等于
   读取时的版本。写事务在自旋锁临界区内验证；纯读事务走 seqlock 协议
   （验证前后比对全局序列号），完全无锁。
3. **写入阶段**：验证通过后分配全局版本号，在临界区内一次性批量原子发布
   整个 WriteSet。验证失败返回 `ErrConflict`，WriteSet 直接丢弃——
   写入从未离开事务私有缓冲区，回滚无需任何补偿，干净利落。

## 关键设计

- **读路径零锁**：记录不可变，新版本整体原子替换；`sync.Map` 键空间读免锁；
  ReadSet 缓存记录槽指针，验证不再触碰键空间索引。
- **无阻塞**：读操作永远不会被写操作阻塞；唯一的锁是提交阶段的 CAS 自旋锁，
  临界区仅含版本校验与指针替换。
- **幻影检测**：读取不存在的 key 以版本 0 记入 ReadSet，其他事务并发创建
  该 key 会被验证阶段捕获。
- **冲突统计**：`Stats()` / `ConflictRate()` 暴露提交数、冲突数与冲突率。

## 运行

```sh
go test ./occ/ -v            # 单元与并发正确性测试
go test ./occ/ -race         # 竞态检测
go test ./occ/ -run XXX -bench .   # 基准：OCC vs 锁
go run ./cmd/demo            # 运行样例：吞吐对比 + 冲突率
```

## 测试结果（Go 1.11.4, darwin/amd64, 10 核）

```
--- PASS: TestBasicCommitRead           基本写入-提交-读取闭环
--- PASS: TestReadYourWrites            事务内读己之写，未提交不泄漏
--- PASS: TestStaleReadSetDetected      ReadSet 版本过期 -> ErrConflict，回滚零残留
--- PASS: TestOverlapWriteConflict      重叠写同一热键，先提交者赢
--- PASS: TestConcurrentHotKeys         32 线程 x 8 热键：无丢失更新，冲突率 ~36%
--- PASS: TestReadOnlyTxnSeqlock        并发转账下只读事务无撕裂读（a+b 恒守恒）
--- PASS: TestRollbackDiscardsWrites    显式回滚后写入不可见
```

基准（每事务读 4 键，5% 写事务）：

```
BenchmarkOCC_ReadHeavy-10    5000000   227 ns/op
BenchmarkLock_ReadHeavy-10   3000000   544 ns/op
BenchmarkOCC_PureRead-10    10000000   215 ns/op
BenchmarkLock_PureRead-10   10000000   232 ns/op
BenchmarkOCC_HotKeyWrite-10  3000000   717 ns/op   conflict-rate ~41%
```

运行样例（16 线程、1024 键、每事务读 4 键、5% 写事务、各 2 秒）：

```
OCC : 8488439 txns (4244220 txns/s), commits=7743388 conflicts=745052 conflict-rate=8.78%
LOCK: 3906769 txns (1953384 txns/s)
OCC speedup: 2.17x
```
