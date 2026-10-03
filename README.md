# Multi-Paxos 多数派共识与无空洞日志提交引擎（Go 标准库实现）

无单点故障的分布式共识日志协议，配合高性能无锁并发队列与 CAS 加速。
仅使用 Go 标准库，全部测试通过 `go test` 运行。

## 架构

```
lockfree/   Michael & Scott 无锁 MPMC 队列（atomic.Pointer CAS，零互斥锁）
paxos/      Multi-Paxos 核心
  ballot.go   提案编号（轮次<<20 | 节点ID，uint64 直接比较）
  log.go      无锁日志槽数组：copy-on-extend 扩容 + 不可变快照指针 CAS
  message.go  Prepare/Promise、Accept/Accepted、Commit 消息
  node.go     Acceptor（两阶段处理）+ 单 goroutine 有序应用器
  leader.go   竞选、租约、Phase 2 直提、空洞修补、追赶同步
  network.go  进程内确定性传输层，支持动态网络分区与消息计数
cmd/demo/   运行样例：选举 → 分区 → 换主补洞 → 愈合覆写脏日志
```

## 关键设计

**去除粗粒度全局互斥锁**：整个 acceptor 热路径没有任何 `sync.Mutex`——
- `promised` 提案号用 `atomic.Uint64` CAS 单调推进；
- 日志槽位是不可变快照，更新 = 整体替换 + `atomic.Pointer` CAS；
- 日志数组扩容用 copy-on-extend + CAS，读者零阻塞；
- 已提交事件经 `lockfree.Queue` 传递给唯一应用 goroutine，按槽序应用，
  空洞会阻塞后续条目直到被修补。

**两阶段与租约优化**：
- Phase 1（Prepare/Promise）：承诺响应携带 `>= FromSlot` 的全部已接受/已提交条目；
- Phase 2（Accept/Accepted）：多数派接受即提交并广播 Commit；
- 租约有效期内（自己仍是承诺持有者且未过期）跳过 Phase 1，直接连续提交；
- Phase 1 自动修补空洞：无汇报的槽位填 noop，冲突槽位选最高提案号的值，
  已提交值绝对优先（安全不变量：已提交值永不被覆写，违反即 panic）。

## 验收场景（TestPartitionAcceptance）

5 节点集群分区 `{0,1} | {2,3,4}`：
1. 旧 Leader(0) 已提交 `a,b,c`；
2. 分区后旧 Leader 在少数派一侧提案 `"dirty"` → `ErrNoQuorum`，留下未提交脏日志；
3. 多数派选出新 Leader(2)，Phase 1 补全未提交槽位，提交 `x,y`（租约内绕过 Phase 1）；
4. 旧 Leader 再提案仍失败（已被取代）；
5. 分区愈合后新 Leader `SyncTo` 追赶，少数派脏日志被安全覆写，
   5 个节点日志一致收敛为 `a,b,c,x,y`，已提交前缀从未改变。

## 运行

```bash
go test -race -count=1 ./...   # 全部测试（含竞态检测）
go run ./cmd/demo              # 运行样例
```

## 测试结果（Apple M1 Pro, go1.24.2, `go test -race`）

```
ok  multipaxos/lockfree   # 6 个测试：FIFO/交错/空队列并发/MPMC 恰好一次/
                          # 长度最终一致/goroutine 无泄漏
ok  multipaxos/paxos      # 10 个测试：基本提交/拒绝低提案号/承诺约束/已提交不可覆写/
                          # 租约绕过 Phase1/空洞修补/分区验收/并发提案/
                          # goroutine 无泄漏/关闭后队列排空
```

基准（`-benchtime=1000000x`）：Enqueue 201.5 ns/op，Dequeue 25.43 ns/op（并行）。

泄漏断言：`TestNoGoroutineLeak` / `TestQueueNoGoroutineLeak` 在基线快照后运行
完整流程，关闭后限时等待 `runtime.NumGoroutine()` 回落到基线，否则判定失败。
