# ARIES 崩溃恢复 + 局部性感知分片路由(Go,仅标准库)

两个组件:

- `aries/` — 经典 ARIES 预写日志与三阶段崩溃恢复引擎(Analysis / Redo / Undo)。
- `router/` — 分层分片(Region → Zone → Node)与虚拟分片、局部性感知路由引擎。

## ARIES 恢复引擎

- WAL 记录类型:`UPDATE / COMMIT / ABORT / END / CLR / BEGIN_CKPT / END_CKPT`,LSN 单调递增,`PrevLSN` 构成事务链。
- **Analysis**:从最近的 END checkpoint(含 TT/DPT 快照)对应的 BEGIN checkpoint 开始正向扫描,重建 TransactionTable 与 DirtyPageTable。
- **Redo**(历史重复):从 DPT 最小 recLSN 起,重放所有 `UPDATE/CLR`;仅当页面在 DPT 中、`recLSN <= rec.LSN` 且 `pageLSN < rec.LSN` 时才应用,保证幂等。
- **Undo**:按全局最大 LSN 反向扫描回 loser 事务;每个被撤销的 UPDATE 写一条 CLR(`UndoNext = PrevLSN`),CLR 只跳不重做,事务完全回滚后写 END。

## 路由引擎

- key → 虚拟分片(固定数量);分片 → 副本集用 **rendezvous(highest-random-weight)hashing**,节点增删时只移动受影响分片(实测扩容 1/13 节点仅 ~9% 主分片迁移)。
- 哈希为 FNV-1a + murmur3 fmix64 finalizer(裸 FNV-1a 对 `node-08`/`node-12` 这类仅末字节不同的短输入雪崩不足,会严重偏斜 rendezvous 排名)。
- **局部性感知调度**:同 Zone > 同 Region > 其他;同分按在途负载(atomic 计数)再按节点 ID 决胜,最大限度压低跨区/跨节点流量。
- 全链路 `sync.RWMutex` + `atomic`,无后台 goroutine,严格无泄漏。

## 运行

```sh
go test ./... -count=1 -race -v   # 全部测试(含竞态检测)
go run ./cmd/demo                 # 运行样例:崩溃恢复 + 路由演示
```

## 验收场景

`aries/recovery_test.go` 构造 WAL:T1、T2 已提交,T3、T4、T5 未提交,两次 fuzzy checkpoint 后崩溃(缓冲区丢失、WAL 持久)。恢复后断言:

- Analysis:TT = {T3,T4,T5: RUNNING},DPT = {P10,P20,P30,P40,P50};
- Undo:losers = [3 4 5],恰好写 6 条 CLR(每个 loser 两条 UPDATE);
- 最终磁盘:P10=a1(T1 已提交)、P20=b2(T2 已提交)、P30=c0、P40=d0、P50=e0(未提交全部回滚);
- 二次恢复幂等(0 loser、0 CLR、状态不变)。

`router/router_test.go` 覆盖:非法参数、空集群、重复/缺失节点、副本数封顶、路由确定性与副本集一致性、Zone/Region 局部性优先与回退、最小分片迁移、负载决胜、并发路由+成员变更(-race)、goroutine 无泄漏。

## 测试结果(go1.24.2,`-race`)

```
ok  ariesdb/aries   (TestThreePhaseRecovery, TestRecoveryIsIdempotent,
                     TestRecoverWithoutCheckpoint, TestRedoSkipsPersistentPages)
ok  ariesdb/router  (11 个测试,含 TestConcurrentRoutingAndMembership、
                     TestNoGoroutineLeakOnIdle)
```
